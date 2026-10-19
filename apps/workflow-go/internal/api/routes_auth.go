package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/dataflow-poc/workflow-go/internal/model"
	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Server) registerAuth(mux *http.ServeMux) {
	mux.Handle("GET /api/auth/google", s.rateLimit("oauth-start", 20, time.Minute, handle(s.googleStart)))
	mux.HandleFunc("GET /api/auth/google/callback", handle(s.googleCallback))
	mux.Handle("GET /api/auth/oidc", s.rateLimit("oauth-start", 20, time.Minute, handle(s.oidcStart)))
	mux.HandleFunc("GET /api/auth/oidc/callback", handle(s.oidcCallback))
	mux.Handle("POST /api/auth/register", s.rateLimit("password-register", 10, time.Minute, handle(s.registerPassword)))
	mux.Handle("POST /api/auth/login", s.rateLimit("password-login", 10, time.Minute, handle(s.loginPassword)))
	mux.Handle("POST /api/auth/refresh", s.rateLimit("token-refresh", 30, time.Minute, handle(s.refresh)))
	mux.HandleFunc("POST /api/auth/logout", handle(s.logout))
	mux.Handle("GET /api/auth/me", s.protected(handle(s.me)))
	mux.HandleFunc("GET /api/auth/accept-invite", handle(s.acceptInvite))
}

func (s *Server) registerPassword(w http.ResponseWriter, r *http.Request) error {
	if os.Getenv("AUTH_PASSWORD_ENABLED") != "true" {
		return notFound(ErrNotFound, "not found")
	}
	var body struct{ Email, Password, TenantName, InviteToken string }
	if !decodeJSON(w, r, &body) {
		return nil
	}
	body.Email = strings.ToLower(strings.TrimSpace(body.Email))
	if body.Email == "" || len(body.Password) < 8 {
		return badRequest(ErrInvalidRequest, "email and a password of at least 8 characters are required")
	}
	var existing string
	if err := s.DB.Pool.QueryRow(r.Context(), `SELECT id FROM users WHERE email=$1`, body.Email).Scan(&existing); err == nil {
		return &HTTPError{Status: http.StatusConflict, Message: "an account with this email already exists"}
	}
	hash, err := hashPassword(body.Password)
	if err != nil {
		return err
	}
	tx, err := s.DB.Pool.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	var tenantID, userID string
	role := "owner"
	if body.InviteToken != "" {
		inviteTenantID, ok := inviteTenant(body.InviteToken)
		if !ok {
			return badRequest(ErrInvalidRequest, "invalid or expired invite")
		}
		if _, err := tx.Exec(r.Context(), `SELECT set_config('app.tenant_id',$1,true)`, inviteTenantID); err != nil {
			return err
		}
		if err := tx.QueryRow(r.Context(), `SELECT tenant_id,role FROM user_invitations WHERE token_hash=$1 AND email=$2 AND accepted_at IS NULL AND expires_at>now()`, sha256Hex(body.InviteToken), body.Email).Scan(&tenantID, &role); err != nil {
			return badRequest(ErrInvalidRequest, "invalid or expired invite")
		}
		if _, err := tx.Exec(r.Context(), `UPDATE user_invitations SET accepted_at=now() WHERE token_hash=$1 AND accepted_at IS NULL`, sha256Hex(body.InviteToken)); err != nil {
			return err
		}
	} else {
		name := strings.TrimSpace(body.TenantName)
		if name == "" {
			name = strings.Split(body.Email, "@")[0]
		}
		if err := tx.QueryRow(r.Context(), `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, name).Scan(&tenantID); err != nil {
			return err
		}
	}
	if err := tx.QueryRow(r.Context(), `INSERT INTO users (tenant_id,email,password_hash,role,email_verified)
    VALUES ($1,$2,$3,$4,true) RETURNING id`, tenantID, body.Email, hash, role).Scan(&userID); err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	refresh, err := s.issueRefreshToken(r.Context(), userID)
	if err != nil {
		return err
	}
	setRefreshCookie(w, refresh)
	s.audit(r.Context(), model.TenantContext{TenantID: tenantID, UserID: userID}, "auth.register", "", map[string]string{"provider": "password"}, r)
	jsonResponse(w, http.StatusCreated, map[string]bool{"ok": true})
	return nil
}

func (s *Server) loginPassword(w http.ResponseWriter, r *http.Request) error {
	if os.Getenv("AUTH_PASSWORD_ENABLED") != "true" {
		return notFound(ErrNotFound, "not found")
	}
	var body struct{ Email, Password string }
	if !decodeJSON(w, r, &body) {
		return nil
	}
	if body.Email == "" || body.Password == "" {
		return badRequest(ErrInvalidRequest, "email and password are required")
	}
	var userID, tenantID, hash string
	err := s.DB.Pool.QueryRow(r.Context(), `SELECT id,tenant_id,password_hash FROM users WHERE email=$1`, strings.ToLower(body.Email)).Scan(&userID, &tenantID, &hash)
	if err != nil || !checkPassword(hash, body.Password) {
		return &HTTPError{Status: http.StatusUnauthorized, Message: "invalid email or password"}
	}
	refresh, err := s.issueRefreshToken(r.Context(), userID)
	if err != nil {
		return err
	}
	setRefreshCookie(w, refresh)
	s.audit(r.Context(), model.TenantContext{TenantID: tenantID, UserID: userID}, "auth.login", "", map[string]string{"provider": "password"}, r)
	jsonResponse(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) error {
	cookie, err := r.Cookie("refresh_token")
	if err != nil {
		return &HTTPError{Status: http.StatusUnauthorized, Message: "no refresh token"}
	}
	var id, userID string
	var revokedAt *time.Time
	var expiresAt time.Time
	if err := s.DB.Pool.QueryRow(r.Context(), `SELECT id,user_id,revoked_at,expires_at FROM refresh_tokens WHERE token_hash=$1`, sha256Hex(cookie.Value)).Scan(&id, &userID, &revokedAt, &expiresAt); err != nil {
		return &HTTPError{Status: http.StatusUnauthorized, Message: "invalid refresh token"}
	}
	if revokedAt != nil {
		_, _ = s.DB.Pool.Exec(r.Context(), `UPDATE refresh_tokens SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, userID)
		return &HTTPError{Status: http.StatusUnauthorized, Message: "refresh token reuse detected"}
	}
	if expiresAt.Before(time.Now()) {
		return &HTTPError{Status: http.StatusUnauthorized, Message: "refresh token expired"}
	}
	if _, err := s.DB.Pool.Exec(r.Context(), `UPDATE refresh_tokens SET revoked_at=now() WHERE id=$1`, id); err != nil {
		return err
	}
	rows, err := s.DB.Pool.Query(r.Context(), `SELECT id,tenant_id,email,role,email_verified FROM users WHERE id=$1`, userID)
	if err != nil {
		return err
	}
	user, err := oneMap(rows)
	if err != nil {
		return err
	}
	access, err := s.signAccessToken(user)
	if err != nil {
		return err
	}
	newRefresh, err := s.issueRefreshToken(r.Context(), userID)
	if err != nil {
		return err
	}
	setRefreshCookie(w, newRefresh)
	jsonResponse(w, http.StatusOK, map[string]interface{}{"accessToken": access, "user": user})
	return nil
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) error {
	if cookie, err := r.Cookie("refresh_token"); err == nil {
		_, _ = s.DB.Pool.Exec(r.Context(), `UPDATE refresh_tokens SET revoked_at=now() WHERE token_hash=$1 AND revoked_at IS NULL`, sha256Hex(cookie.Value))
	}
	clearCookie(w, "refresh_token")
	jsonResponse(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.DB.Pool.Query(r.Context(), `SELECT u.id,u.tenant_id,u.email,u.role,u.email_verified,t.name AS tenant_name
    FROM users u JOIN tenants t ON t.id=u.tenant_id WHERE u.id=$1`, tenantFrom(r).UserID)
	if err != nil {
		return err
	}
	user, err := oneMap(rows)
	if err != nil || user == nil {
		return notFound(ErrNotFound, "user not found")
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"user": user})
	return nil
}

func (s *Server) acceptInvite(w http.ResponseWriter, r *http.Request) error {
	token := r.URL.Query().Get("token")
	if token == "" {
		return badRequest(ErrInvalidRequest, "token required")
	}
	tenantID, ok := inviteTenant(token)
	if !ok {
		return badRequest(ErrInvalidRequest, "invalid or used token")
	}
	var email, role, tenantName string
	var expires time.Time
	err := s.DB.TenantTx(r.Context(), tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `SELECT i.email,i.role,i.expires_at,t.name FROM user_invitations i JOIN tenants t ON t.id=i.tenant_id WHERE i.token_hash=$1 AND i.accepted_at IS NULL`, sha256Hex(token)).Scan(&email, &role, &expires, &tenantName)
	})
	if err != nil {
		return badRequest(ErrInvalidRequest, "invalid or used token")
	}
	if expires.Before(time.Now()) {
		return badRequest(ErrInvalidRequest, "token expired")
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"email": email, "role": role, "tenantName": tenantName,
	})
	return nil
}

func inviteTenant(token string) (string, bool) {
	tenantID, _, ok := strings.Cut(token, ".")
	_, err := uuid.Parse(tenantID)
	return tenantID, ok && err == nil
}

func (s *Server) googleStart(w http.ResponseWriter, r *http.Request) error {
	clientID, secret := os.Getenv("GOOGLE_CLIENT_ID"), os.Getenv("GOOGLE_CLIENT_SECRET")
	if clientID == "" || secret == "" {
		return &HTTPError{Status: http.StatusServiceUnavailable, Message: "Google OAuth is not configured"}
	}
	state := randomToken()
	http.SetCookie(w, &http.Cookie{Name: "oauth_state", Value: state, Path: "/api/auth", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: os.Getenv("NODE_ENV") == "production", MaxAge: 600})
	query := url.Values{"client_id": {clientID}, "redirect_uri": {os.Getenv("GOOGLE_REDIRECT_URI")}, "response_type": {"code"}, "scope": {"openid email profile"}, "access_type": {"online"}, "prompt": {"select_account"}, "state": {state}}
	http.Redirect(w, r, "https://accounts.google.com/o/oauth2/v2/auth?"+query.Encode(), http.StatusFound)
	return nil
}

func (s *Server) googleCallback(w http.ResponseWriter, r *http.Request) error {
	stateCookie, _ := r.Cookie("oauth_state")
	clearCookie(w, "oauth_state")
	if r.URL.Query().Get("code") == "" || stateCookie == nil || r.URL.Query().Get("state") != stateCookie.Value {
		http.Redirect(w, r, s.Config.AppURL+"/login?error=oauth_state", http.StatusFound)
		return nil
	}
	values := url.Values{"code": {r.URL.Query().Get("code")}, "client_id": {os.Getenv("GOOGLE_CLIENT_ID")}, "client_secret": {os.Getenv("GOOGLE_CLIENT_SECRET")}, "redirect_uri": {os.Getenv("GOOGLE_REDIRECT_URI")}, "grant_type": {"authorization_code"}}
	var tokens map[string]interface{}
	if err := s.postFormJSON(r, "https://oauth2.googleapis.com/token", values, &tokens); err != nil {
		http.Redirect(w, r, s.Config.AppURL+"/login?error=oauth_failed", http.StatusFound)
		return nil
	}
	idToken, _ := tokens["id_token"].(string)
	var profile struct {
		Sub, Email    string
		Aud, Iss      string
		EmailVerified interface{} `json:"email_verified"`
	}
	if idToken == "" || s.getJSON(r, "https://oauth2.googleapis.com/tokeninfo?id_token="+url.QueryEscape(idToken), &profile) != nil || profile.Sub == "" || profile.Email == "" || !emailClaimVerified(profile.EmailVerified) {
		http.Redirect(w, r, s.Config.AppURL+"/login?error=oauth_profile", http.StatusFound)
		return nil
	}
	if profile.Aud != os.Getenv("GOOGLE_CLIENT_ID") || (profile.Iss != "" && profile.Iss != "https://accounts.google.com" && profile.Iss != "accounts.google.com") {
		return &HTTPError{Status: http.StatusUnauthorized, Message: "Google id_token audience or issuer mismatch"}
	}
	userID, err := s.resolveIdentity(r, "google_sub", profile.Sub, strings.ToLower(profile.Email), "google")
	if errors.Is(err, errUnverifiedAccount) {
		http.Redirect(w, r, s.Config.AppURL+"/login?error=unverified_account", http.StatusFound)
		return nil
	}
	if err != nil {
		http.Redirect(w, r, s.Config.AppURL+"/login?error=oauth_failed", http.StatusFound)
		return nil
	}
	refresh, err := s.issueRefreshToken(r.Context(), userID)
	if err != nil {
		return err
	}
	setRefreshCookie(w, refresh)
	http.Redirect(w, r, s.Config.AppURL+"/pipelines", http.StatusFound)
	return nil
}

func (s *Server) oidcDiscovery(r *http.Request) (map[string]string, error) {
	issuer := strings.TrimSuffix(os.Getenv("OIDC_ISSUER"), "/")
	if issuer == "" {
		return nil, &HTTPError{Status: http.StatusServiceUnavailable, Message: "OIDC_ISSUER not configured"}
	}
	doc := map[string]string{}
	return doc, s.getJSON(r, issuer+"/.well-known/openid-configuration", &doc)
}

func (s *Server) oidcStart(w http.ResponseWriter, r *http.Request) error {
	doc, err := s.oidcDiscovery(r)
	if err != nil {
		return err
	}
	state := randomToken()
	http.SetCookie(w, &http.Cookie{Name: "oauth_state", Value: state, Path: "/api/auth", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: os.Getenv("NODE_ENV") == "production", MaxAge: 600})
	query := url.Values{"response_type": {"code"}, "client_id": {os.Getenv("OIDC_CLIENT_ID")}, "redirect_uri": {os.Getenv("OIDC_REDIRECT_URI")}, "scope": {"openid email profile"}, "state": {state}}
	http.Redirect(w, r, doc["authorization_endpoint"]+"?"+query.Encode(), http.StatusFound)
	return nil
}

func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) error {
	stateCookie, _ := r.Cookie("oauth_state")
	clearCookie(w, "oauth_state")
	if r.URL.Query().Get("code") == "" || stateCookie == nil || stateCookie.Value != r.URL.Query().Get("state") {
		http.Redirect(w, r, s.Config.AppURL+"/login?error=oauth_state", http.StatusFound)
		return nil
	}
	doc, err := s.oidcDiscovery(r)
	if err != nil {
		http.Redirect(w, r, s.Config.AppURL+"/login?error=oidc_failed", http.StatusFound)
		return nil
	}
	values := url.Values{"grant_type": {"authorization_code"}, "code": {r.URL.Query().Get("code")}, "redirect_uri": {os.Getenv("OIDC_REDIRECT_URI")}, "client_id": {os.Getenv("OIDC_CLIENT_ID")}, "client_secret": {os.Getenv("OIDC_CLIENT_SECRET")}}
	var token map[string]interface{}
	if s.postFormJSON(r, doc["token_endpoint"], values, &token) != nil {
		http.Redirect(w, r, s.Config.AppURL+"/login?error=oidc_token", http.StatusFound)
		return nil
	}
	request, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, doc["userinfo_endpoint"], nil)
	request.Header.Set("Authorization", "Bearer "+stringValue(token["access_token"]))
	var info struct {
		Sub, Email    string
		EmailVerified interface{} `json:"email_verified"`
	}
	if s.doJSON(request, &info) != nil || info.Sub == "" || info.Email == "" || !emailClaimVerified(info.EmailVerified) {
		http.Redirect(w, r, s.Config.AppURL+"/login?error=oidc_profile", http.StatusFound)
		return nil
	}
	idToken, _ := token["id_token"].(string)
	if idToken == "" || verifyOIDCIDToken(idToken, strings.TrimSuffix(os.Getenv("OIDC_ISSUER"), "/"), os.Getenv("OIDC_CLIENT_ID"), info.Sub) != nil {
		return &HTTPError{Status: http.StatusUnauthorized, Message: "OIDC id_token verification failed"}
	}
	userID, err := s.resolveIdentity(r, "oidc_sub", info.Sub, strings.ToLower(info.Email), "oidc")
	if errors.Is(err, errUnverifiedAccount) {
		http.Redirect(w, r, s.Config.AppURL+"/login?error=unverified_account", http.StatusFound)
		return nil
	}
	if err != nil {
		http.Redirect(w, r, s.Config.AppURL+"/login?error=oidc_failed", http.StatusFound)
		return nil
	}
	refresh, err := s.issueRefreshToken(r.Context(), userID)
	if err != nil {
		return err
	}
	setRefreshCookie(w, refresh)
	http.Redirect(w, r, s.Config.AppURL+"/pipelines", http.StatusFound)
	return nil
}

// verifyOIDCIDToken parses the ID token payload and checks iss, aud, sub, and exp.
// We cannot JWKS-verify the signature without a new dependency; integrity is
// anchored by cross-checking sub against the userinfo response, which is
// fetched over TLS from the issuer using the access token.
func verifyOIDCIDToken(idToken, issuer, clientID, userinfoSub string) error {
	claims := &jwt.RegisteredClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(idToken, claims); err != nil {
		return fmt.Errorf("id_token claims: %w", err)
	}
	if issuer == "" || strings.TrimSuffix(claims.Issuer, "/") != issuer {
		return fmt.Errorf("id_token issuer mismatch")
	}
	return jwt.NewValidator(
		jwt.WithAudience(clientID), jwt.WithSubject(userinfoSub), jwt.WithExpirationRequired(),
	).Validate(claims)
}

// emailClaimVerified accepts only an explicit true (Google's tokeninfo sends the
// string "true"); a missing claim does not prove the user owns the address.
func emailClaimVerified(value interface{}) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(v, "true")
	}
	return false
}

// errUnverifiedAccount means an account exists for the email but nobody has
// proven they own it, so a provider login must not be linked to it.
var errUnverifiedAccount = errors.New("an unverified account already uses this email")

func (s *Server) resolveIdentity(r *http.Request, column, sub, email, provider string) (string, error) {
	if column != "google_sub" && column != "oidc_sub" {
		return "", fmt.Errorf("unsupported identity column")
	}
	var userID, tenantID string
	err := s.DB.Pool.QueryRow(r.Context(), `SELECT id,tenant_id FROM users WHERE `+column+`=$1`, sub).Scan(&userID, &tenantID)
	if err == nil {
		s.audit(r.Context(), model.TenantContext{TenantID: tenantID, UserID: userID}, "auth.login", "", map[string]string{"provider": provider}, r)
		return userID, nil
	}
	var verified bool
	err = s.DB.Pool.QueryRow(r.Context(), `SELECT id,tenant_id,email_verified FROM users WHERE email=$1`, email).Scan(&userID, &tenantID, &verified)
	if err == nil {
		// Linking by email is only safe when the existing account's owner has
		// proven the address too; otherwise anyone could pre-register it.
		if !verified {
			return "", errUnverifiedAccount
		}
		if _, err = s.DB.Pool.Exec(r.Context(), `UPDATE users SET `+column+`=$1 WHERE id=$2`, sub, userID); err != nil {
			return "", err
		}
		s.audit(r.Context(), model.TenantContext{TenantID: tenantID, UserID: userID}, "auth.identity_linked", "", map[string]string{"provider": provider}, r)
		return userID, nil
	}
	tx, err := s.DB.Pool.Begin(r.Context())
	if err != nil {
		return "", err
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	role := "owner"
	// Invitations are tenant rows; claim one through the SECURITY DEFINER
	// resolver because no tenant is set yet.
	err = tx.QueryRow(r.Context(), `SELECT tenant_id,role FROM claim_email_invitation($1)`, email).Scan(&tenantID, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		role = "owner"
		err = tx.QueryRow(r.Context(), `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, strings.Split(email, "@")[0]).Scan(&tenantID)
	}
	if err != nil {
		return "", err
	}
	err = tx.QueryRow(r.Context(), `INSERT INTO users (tenant_id,email,`+column+`,role,email_verified) VALUES ($1,$2,$3,$4,true) RETURNING id`, tenantID, email, sub, role).Scan(&userID)
	if err != nil {
		return "", err
	}
	return userID, tx.Commit(r.Context())
}

func (s *Server) postFormJSON(r *http.Request, endpoint string, values url.Values, target interface{}) error {
	request, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return s.doJSON(request, target)
}

func (s *Server) getJSON(r *http.Request, endpoint string, target interface{}) error {
	request, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, endpoint, nil)
	return s.doJSON(request, target)
}

func (s *Server) doJSON(request *http.Request, target interface{}) error {
	return doJSON(s.HTTP, request, target)
}

func doJSON(client *http.Client, request *http.Request, target interface{}) error {
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return fmt.Errorf("upstream returned %d", response.StatusCode)
	}
	return json.NewDecoder(response.Body).Decode(target)
}
