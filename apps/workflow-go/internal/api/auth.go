package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dataflow-poc/workflow-go/internal/database"
	"github.com/dataflow-poc/workflow-go/internal/model"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

const refreshTTLDays = 30

type accessClaims struct {
	TenantID      string `json:"tenantId"`
	Email         string `json:"email"`
	Role          string `json:"role"`
	EmailVerified bool   `json:"emailVerified"`
	jwt.RegisteredClaims
}

func (s *Server) signAccessToken(user map[string]interface{}) (string, error) {
	claims := accessClaims{
		TenantID: stringValue(user["tenant_id"]), Email: stringValue(user["email"]),
		Role: stringValue(user["role"]), EmailVerified: boolValue(user["email_verified"]),
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   stringValue(user["id"]),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(s.Config.JWTAccessSecret))
}

func (s *Server) authenticate(r *http.Request) (model.TenantContext, error) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return model.TenantContext{}, &HTTPError{Status: http.StatusUnauthorized, Message: "missing bearer token"}
	}
	token := strings.TrimPrefix(header, "Bearer ")
	claims := &accessClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(parsed *jwt.Token) (interface{}, error) {
		if parsed.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(s.Config.JWTAccessSecret), nil
	})
	if err == nil && parsed.Valid {
		return model.TenantContext{
			TenantID: claims.TenantID, UserID: claims.Subject, Email: claims.Email,
			Role: claims.Role, EmailVerified: claims.EmailVerified,
		}, nil
	}
	hash := sha256Hex(token)
	var tenant model.TenantContext
	var revokedAt, expiresAt *time.Time
	err = s.DB.Pool.QueryRow(r.Context(), `SELECT t.id,t.created_by,t.tenant_id,t.role,t.revoked_at,t.expires_at
      FROM api_tokens t JOIN users u ON u.id=t.created_by WHERE t.token_hash=$1`, hash).
		Scan(&tenant.TokenID, &tenant.UserID, &tenant.TenantID, &tenant.Role, &revokedAt, &expiresAt)
	if err != nil || revokedAt != nil || (expiresAt != nil && !expiresAt.After(time.Now())) {
		return model.TenantContext{}, &HTTPError{Status: http.StatusUnauthorized, Message: "invalid token"}
	}
	tenant.Email = "sa:" + tenant.TokenID
	tenant.EmailVerified = true
	return tenant, nil
}

func (s *Server) protected(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenant, err := s.authenticate(r)
		if err != nil {
			var clientErr *HTTPError
			if errors.As(err, &clientErr) {
				jsonError(w, clientErr.Status, clientErr.Code, clientErr.Message, clientErr.Details)
			} else {
				jsonError(w, http.StatusUnauthorized, ErrUnauthorized, "invalid token", nil)
			}
			return
		}
		if !tenant.EmailVerified {
			jsonError(w, http.StatusForbidden, ErrForbidden, "email not verified", nil)
			return
		}
		next.ServeHTTP(w, withTenant(r, tenant))
	})
}

func owner(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tenantFrom(r).Role != "owner" {
			jsonError(w, http.StatusForbidden, ErrForbidden, "owner role required", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

var pipelineRoleRank = map[string]int{"viewer": 0, "editor": 1, "admin": 2}

// pipelineAllows reports whether the caller holds at least minimum on a pipeline
// row. Workspace owners and the row's creator always do; everyone else needs a
// pipeline_access grant.
func pipelineAllows(tenant model.TenantContext, createdBy, role *string, minimum string) bool {
	if tenant.Role == "owner" || (createdBy != nil && *createdBy == tenant.UserID) {
		return true
	}
	return role != nil && pipelineRoleRank[*role] >= pipelineRoleRank[minimum]
}

// pipelineAccessError returns nil when the caller may use the pipeline row at
// the given level, otherwise a 404 (no visibility) or 403 (too low a role).
func pipelineAccessError(tenant model.TenantContext, createdBy, role *string, minimum string) error {
	if pipelineAllows(tenant, createdBy, role, minimum) {
		return nil
	}
	if role == nil {
		return notFound(ErrNotFound, "not found")
	}
	return &HTTPError{Status: http.StatusForbidden, Code: ErrForbidden, Message: "pipeline access requires " + minimum + " role"}
}

// pipelineVisibleSQL limits a pipelines alias to rows the user may view; $n is
// the user id. Only applied to non-owners.
func pipelineVisibleSQL(alias string, userParam int) string {
	return fmt.Sprintf(`(%[1]s.created_by=$%[2]d OR EXISTS (SELECT 1 FROM pipeline_access pa WHERE pa.pipeline_id=%[1]s.id AND pa.user_id=$%[2]d))`, alias, userParam)
}

func (s *Server) pipelineAccess(minimum string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenant := tenantFrom(r)
		if tenant.Role == "owner" {
			next.ServeHTTP(w, r)
			return
		}
		var createdBy, role *string
		err := s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
			return tx.QueryRow(r.Context(), `SELECT p.created_by,pa.role FROM pipelines p
        LEFT JOIN pipeline_access pa ON pa.pipeline_id=p.id AND pa.user_id=$2
        WHERE p.id=$1 AND p.tenant_id=$3 LIMIT 1`, r.PathValue("rowId"), tenant.UserID, tenant.TenantID).Scan(&createdBy, &role)
		})
		if err != nil {
			jsonError(w, http.StatusNotFound, ErrNotFound, "not found", nil)
			return
		}
		if err := pipelineAccessError(tenant, createdBy, role, minimum); err != nil {
			var httpErr *HTTPError
			errors.As(err, &httpErr)
			jsonError(w, httpErr.Status, httpErr.Code, httpErr.Message, nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// executionAccess guards /api/executions/{id} routes with the same pipeline
// roles as the pipeline the execution belongs to.
func (s *Server) executionAccess(minimum string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenant := tenantFrom(r)
		if tenant.Role == "owner" {
			next.ServeHTTP(w, r)
			return
		}
		var createdBy, role *string
		err := s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
			return tx.QueryRow(r.Context(), `SELECT p.created_by,pa.role FROM executions e
        JOIN pipelines p ON p.id=e.pipeline_id
        LEFT JOIN pipeline_access pa ON pa.pipeline_id=p.id AND pa.user_id=$2
        WHERE e.id=$1 AND e.tenant_id=$3 LIMIT 1`, r.PathValue("id"), tenant.UserID, tenant.TenantID).Scan(&createdBy, &role)
		})
		if err != nil {
			jsonError(w, http.StatusNotFound, ErrNotFound, "not found", nil)
			return
		}
		if err := pipelineAccessError(tenant, createdBy, role, minimum); err != nil {
			var httpErr *HTTPError
			errors.As(err, &httpErr)
			jsonError(w, httpErr.Status, httpErr.Code, httpErr.Message, nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) audit(ctx context.Context, tenant model.TenantContext, action, resource string, metadata interface{}, r *http.Request) {
	metadata = auditMetadata(tenant, metadata)
	err := s.DB.TenantTx(ctx, tenant.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO audit_log (tenant_id,user_id,action,resource,ip_address,user_agent,metadata)
      VALUES ($1,$2,$3,$4,$5,$6,$7)`, tenant.TenantID, nullString(tenant.UserID), action, resource, requestIP(r), r.UserAgent(), metadata)
		return err
	})
	if err != nil {
		// The audited action already happened; a lost audit row must be visible.
		slog.Error("audit write failed", "action", action, "resource", resource, "tenant", tenant.TenantID, "error", err)
	}
}

// auditMetadata records which API token acted, next to the user it acts for.
func auditMetadata(tenant model.TenantContext, metadata interface{}) interface{} {
	if tenant.TokenID == "" {
		return metadata
	}
	merged := map[string]interface{}{}
	switch value := metadata.(type) {
	case nil:
	case map[string]interface{}:
		for k, v := range value {
			merged[k] = v
		}
	case map[string]string:
		for k, v := range value {
			merged[k] = v
		}
	default:
		merged["details"] = value
	}
	merged["tokenId"] = tenant.TokenID
	return merged
}

func (s *Server) issueRefreshToken(ctx context.Context, userID string) (string, error) {
	token := randomToken()
	_, err := s.DB.Pool.Exec(ctx, `INSERT INTO refresh_tokens (user_id,token_hash,expires_at)
    VALUES ($1,$2,$3)`, userID, sha256Hex(token), time.Now().Add(refreshTTLDays*24*time.Hour))
	return token, err
}

func setRefreshCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: token, Path: "/api/auth", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: os.Getenv("NODE_ENV") == "production", MaxAge: refreshTTLDays * 86400})
}

func clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Path: "/api/auth", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func hashPassword(value string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(value), 12)
	return string(hash), err
}

func checkPassword(hash, value string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(value)) == nil
}

func stringValue(value interface{}) string {
	switch value := value.(type) {
	case string:
		return value
	case []byte:
		return string(value)
	case [16]byte:
		return uuid.UUID(value).String()
	default:
		return fmt.Sprint(value)
	}
}

func boolValue(value interface{}) bool {
	valueBool, _ := value.(bool)
	return valueBool
}

func nullString(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}

func tenantQueryRows(ctx context.Context, db *database.DB, tenantID, query string, args ...interface{}) ([]map[string]interface{}, error) {
	return database.TenantQuery(ctx, db, tenantID, func(tx pgx.Tx) ([]map[string]interface{}, error) {
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		return rowsToMaps(rows)
	})
}
