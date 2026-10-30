package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dataflow-poc/workflow-go/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type contextKey string

const (
	tenantKey      contextKey = "tenant"
	traceParentKey contextKey = "traceparent"
)

func tenantFrom(r *http.Request) model.TenantContext {
	value, _ := r.Context().Value(tenantKey).(model.TenantContext)
	return value
}

func withTenant(r *http.Request, tenant model.TenantContext) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), tenantKey, tenant))
}

func jsonResponse(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if value == nil {
		w.WriteHeader(status)
		return
	}
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(value); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	out := body.Bytes()
	if bytes.Contains(out, []byte(`"secret"`)) {
		out = redactTriggerSecrets(out)
	}
	w.WriteHeader(status)
	_, _ = w.Write(out)
}

// redactTriggerSecrets blanks every pipeline trigger's signing secret in a JSON
// response. Webhook secrets are write-only: many routes return definitions to
// any member, and only the sender of the webhook needs the secret.
func redactTriggerSecrets(body []byte) []byte {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value interface{}
	if decoder.Decode(&value) != nil {
		return body
	}
	if !blankTriggerSecrets(value) {
		return body
	}
	var out bytes.Buffer
	if json.NewEncoder(&out).Encode(value) != nil {
		return body
	}
	return out.Bytes()
}

func blankTriggerSecrets(value interface{}) bool {
	changed := false
	switch v := value.(type) {
	case map[string]interface{}:
		if trigger, ok := v["trigger"].(map[string]interface{}); ok {
			if secret, ok := trigger["secret"].(string); ok && secret != "" {
				trigger["secret"] = ""
				trigger["secretSet"] = true
				changed = true
			}
		}
		for _, child := range v {
			changed = blankTriggerSecrets(child) || changed
		}
	case []interface{}:
		for _, child := range v {
			changed = blankTriggerSecrets(child) || changed
		}
	}
	return changed
}

type APIErrorCode string

const (
	ErrInvalidRequest     APIErrorCode = "ERR_INVALID_REQUEST"
	ErrMissingField       APIErrorCode = "ERR_MISSING_FIELD"
	ErrNotFound           APIErrorCode = "ERR_NOT_FOUND"
	ErrRateLimit          APIErrorCode = "ERR_RATE_LIMIT_EXCEEDED"
	ErrInternal           APIErrorCode = "ERR_INTERNAL"
	ErrUnauthorized       APIErrorCode = "ERR_UNAUTHORIZED"
	ErrForbidden          APIErrorCode = "ERR_FORBIDDEN"
	ErrEmailNotVerified   APIErrorCode = "ERR_EMAIL_NOT_VERIFIED"
	ErrConflict           APIErrorCode = "ERR_CONFLICT"
	ErrValidation         APIErrorCode = "ERR_VALIDATION"
	ErrInvalidEnvironment APIErrorCode = "ERR_INVALID_ENVIRONMENT"
	ErrConnectorExists    APIErrorCode = "ERR_CONNECTOR_EXISTS"
	ErrOAuthRevoked       APIErrorCode = "ERR_OAUTH_REVOKED"
	ErrNotSupported       APIErrorCode = "ERR_NOT_SUPPORTED"
)

type ErrorResponse struct {
	Error   string            `json:"error"`
	Code    APIErrorCode      `json:"code,omitempty"`
	Details map[string]string `json:"details,omitempty"`
}

func jsonError(w http.ResponseWriter, status int, code APIErrorCode, message string, details map[string]string) {
	jsonResponse(w, status, ErrorResponse{Error: message, Code: code, Details: details})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target interface{}) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 5<<20)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(target); err != nil {
		jsonError(w, http.StatusBadRequest, ErrInvalidRequest, "invalid JSON body", nil)
		return false
	}
	return true
}

func rowsToMaps(rows pgx.Rows) ([]map[string]interface{}, error) {
	defer rows.Close()
	fields := rows.FieldDescriptions()
	result := make([]map[string]interface{}, 0)
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, err
		}
		row := make(map[string]interface{}, len(values))
		for i, value := range values {
			if fields[i].DataTypeOID == pgtype.UUIDOID {
				switch raw := value.(type) {
				case [16]byte:
					value = uuid.UUID(raw).String()
				case []byte:
					if parsed, parseErr := uuid.FromBytes(raw); parseErr == nil {
						value = parsed.String()
					}
				}
			}
			row[string(fields[i].Name)] = value
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func oneMap(rows pgx.Rows) (map[string]interface{}, error) {
	values, err := rowsToMaps(rows)
	if err != nil || len(values) == 0 {
		return nil, err
	}
	return values[0], nil
}

// trustedProxies are the peers whose X-Real-IP header names the client.
// TRUSTED_PROXY_CIDRS narrows them (for example to the web tier's pod range);
// by default any loopback or private peer is trusted, which suits compose and
// the Helm chart where only the web nginx can reach the API.
var trustedProxies = sync.OnceValue(func() []*net.IPNet {
	return parseTrustedProxies(os.Getenv("TRUSTED_PROXY_CIDRS"))
})

const defaultTrustedProxies = "127.0.0.0/8,::1/128,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,fc00::/7"

func parseTrustedProxies(value string) []*net.IPNet {
	if strings.TrimSpace(value) == "" {
		value = defaultTrustedProxies
	}
	var networks []*net.IPNet
	for _, cidr := range strings.Split(value, ",") {
		if _, network, err := net.ParseCIDR(strings.TrimSpace(cidr)); err == nil {
			networks = append(networks, network)
		} else if cidr = strings.TrimSpace(cidr); cidr != "" && cidr != "none" {
			slog.Warn("ignoring invalid TRUSTED_PROXY_CIDRS entry", "cidr", cidr)
		}
	}
	return networks
}

func requestIP(r *http.Request) string {
	return clientIP(r, trustedProxies())
}

func clientIP(r *http.Request, trusted []*net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if peer := net.ParseIP(host); peer != nil {
		for _, network := range trusted {
			if network.Contains(peer) {
				if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(realIP) != nil {
					return realIP
				}
				break
			}
		}
	}
	return host
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowed := env("APP_URL", "http://localhost:3000")
		if origin != "" && origin == allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Add("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if traceparent := r.Header.Get("traceparent"); traceparent != "" {
			r = r.WithContext(context.WithValue(r.Context(), traceParentKey, traceparent))
		}
		writer := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		started := time.Now()
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("unhandled route panic", "error", recovered, "stack", string(debug.Stack()))
				if writer.status == http.StatusOK {
					jsonError(writer, http.StatusInternalServerError, ErrInternal, "internal error", nil)
				}
			}
			slog.Info("http request", "method", r.Method, "path", r.URL.Path, "status", writer.status, "duration_ms", time.Since(started).Milliseconds())
		}()
		next.ServeHTTP(writer, r)
	})
}

func (s *Server) rateLimit(scope string, limit int64, window time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bucket := time.Now().Unix() / int64(window/time.Second)
		key := fmt.Sprintf("dataflow:rate:%s:%s:%d", scope, requestIP(r), bucket)
		// INCR and EXPIRE in one round trip, so a key never outlives its window.
		pipe := s.Redis.TxPipeline()
		incr := pipe.Incr(r.Context(), key)
		pipe.Expire(r.Context(), key, window)
		_, err := pipe.Exec(r.Context())
		count := incr.Val()
		if err != nil {
			// Without Redis each replica still enforces the limit on its own,
			// rather than letting unlimited login attempts through.
			warnRateLimitFallback(err)
			count = localRateLimiter.incr(key, window)
		}
		if count > limit {
			jsonError(w, http.StatusTooManyRequests, ErrRateLimit, "rate limit exceeded", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// fallbackLimiter counts requests in process while Redis is unavailable.
type fallbackLimiter struct {
	mu     sync.Mutex
	counts map[string]int64
	expiry map[string]time.Time
}

var localRateLimiter = &fallbackLimiter{counts: map[string]int64{}, expiry: map[string]time.Time{}}

func (l *fallbackLimiter) incr(key string, window time.Duration) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.counts) > 10000 {
		for k, expires := range l.expiry {
			if now.After(expires) {
				delete(l.counts, k)
				delete(l.expiry, k)
			}
		}
	}
	if expires, ok := l.expiry[key]; !ok || now.After(expires) {
		l.counts[key], l.expiry[key] = 0, now.Add(window)
	}
	l.counts[key]++
	return l.counts[key]
}

var lastRateLimitWarning atomic.Int64

func warnRateLimitFallback(err error) {
	now := time.Now().Unix()
	if last := lastRateLimitWarning.Load(); now-last >= 60 && lastRateLimitWarning.CompareAndSwap(last, now) {
		slog.Warn("rate limiter cannot reach Redis; limiting per replica", "error", err)
	}
}

func handle(fn func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			var clientErr *HTTPError
			if errors.As(err, &clientErr) {
				jsonError(w, clientErr.Status, clientErr.Code, clientErr.Message, clientErr.Details)
				return
			}
			slog.Error("route failed", "method", r.Method, "path", r.URL.Path, "error", err)
			jsonError(w, http.StatusInternalServerError, ErrInternal, "internal error", nil)
		}
	}
}

type HTTPError struct {
	Status  int
	Code    APIErrorCode
	Message string
	Details map[string]string
}

func (e *HTTPError) Error() string { return e.Message }

func badRequest(code APIErrorCode, message string) error {
	return &HTTPError{Status: http.StatusBadRequest, Code: code, Message: message}
}

func badRequestWithDetails(code APIErrorCode, message string, details map[string]string) error {
	return &HTTPError{Status: http.StatusBadRequest, Code: code, Message: message, Details: details}
}

func notFound(code APIErrorCode, message string) error {
	return &HTTPError{Status: http.StatusNotFound, Code: code, Message: message}
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func requireString(body map[string]interface{}, key string) (string, error) {
	value, ok := body[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", badRequest(ErrMissingField, fmt.Sprintf("%s required", key))
	}
	return value, nil
}
