package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/dataflow-poc/workflow-go/internal/model"
)

// forgotPassword emails a one-hour reset link. It answers the same way whether
// or not the address has an account, so it cannot be used to find out who does.
func (s *Server) forgotPassword(w http.ResponseWriter, r *http.Request) error {
	if os.Getenv("AUTH_PASSWORD_ENABLED") != "true" {
		return notFound(ErrNotFound, "not found")
	}
	var body struct{ Email string }
	if !decodeJSON(w, r, &body) {
		return nil
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	var userID string
	if email != "" && s.DB.Pool.QueryRow(r.Context(), `SELECT id FROM users WHERE email=$1`, email).Scan(&userID) == nil {
		if err := s.sendPasswordReset(r, userID, email); err != nil {
			slog.Error("send password reset", "error", err)
		}
	}
	jsonResponse(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}

// sendPasswordReset replaces any earlier link for the user, and sends at most
// one a minute per account so the form cannot be used to flood an inbox.
func (s *Server) sendPasswordReset(r *http.Request, userID, email string) error {
	token := randomToken()
	tag, err := s.DB.Pool.Exec(r.Context(), `WITH recent AS (
      SELECT 1 FROM password_resets WHERE user_id=$2 AND created_at>now()-interval '1 minute'
    ), cleared AS (
      DELETE FROM password_resets WHERE user_id=$2 AND NOT EXISTS (SELECT 1 FROM recent)
    )
    INSERT INTO password_resets (token_hash,user_id,expires_at)
    SELECT $1,$2,now()+interval '1 hour' WHERE NOT EXISTS (SELECT 1 FROM recent)`, sha256Hex(token), userID)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	link := strings.TrimSuffix(s.Config.AppURL, "/") + "/reset-password?token=" + url.QueryEscape(token)
	if !s.Config.MailConfigured {
		slog.Warn("SMTP is not configured; give this password reset link to the user", "email", email, "link", link)
		return nil
	}
	return s.sendMail(email, "Reset your DataFlow password", fmt.Sprintf(`<p>Someone asked to reset the password for your DataFlow account.</p><p><a href="%s">Choose a new password</a></p><p>The link expires in 1 hour. If you did not ask for this, ignore this email; your password has not changed.</p>`, link))
}

// resetPassword consumes a reset link and sets a new password. Opening the
// link proves the user owns the inbox, so the address counts as verified, and
// every existing session is signed out.
func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) error {
	if os.Getenv("AUTH_PASSWORD_ENABLED") != "true" {
		return notFound(ErrNotFound, "not found")
	}
	var body struct{ Token, Password string }
	if !decodeJSON(w, r, &body) {
		return nil
	}
	if len(body.Password) < 8 {
		return badRequest(ErrInvalidRequest, "a password of at least 8 characters is required")
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
	var userID, tenantID string
	// DELETE ... RETURNING makes the link single-use even under concurrent submits.
	if body.Token == "" || tx.QueryRow(r.Context(), `DELETE FROM password_resets WHERE token_hash=$1 AND expires_at>now() RETURNING user_id`, sha256Hex(body.Token)).Scan(&userID) != nil {
		return badRequest(ErrInvalidRequest, "this reset link is invalid or has expired; ask for a new one")
	}
	if err := tx.QueryRow(r.Context(), `UPDATE users SET password_hash=$2,email_verified=true WHERE id=$1 RETURNING tenant_id`, userID, hash).Scan(&tenantID); err != nil {
		return err
	}
	for _, statement := range []string{
		`DELETE FROM password_resets WHERE user_id=$1`,
		`DELETE FROM email_verifications WHERE user_id=$1`,
		`UPDATE refresh_tokens SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`,
	} {
		if _, err := tx.Exec(r.Context(), statement, userID); err != nil {
			return err
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	s.audit(r.Context(), model.TenantContext{TenantID: tenantID, UserID: userID}, "auth.password_reset", "", map[string]string{"provider": "password"}, r)
	jsonResponse(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}
