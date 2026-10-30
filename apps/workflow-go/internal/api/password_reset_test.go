package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dataflow-poc/workflow-go/internal/config"
	"github.com/dataflow-poc/workflow-go/internal/database"
	"github.com/google/uuid"
)

// authFixtureUser creates a verified password user in its own tenant.
func authFixtureUser(t *testing.T, ctx context.Context, db *database.DB, password string) (userID, email string) {
	t.Helper()
	tenant := uuid.NewString()
	email = "auth-" + tenant + "@fixture.invalid"
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id=$1`, tenant)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM users WHERE tenant_id=$1`, tenant)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, tenant)
	})
	if _, err := db.Pool.Exec(ctx, `INSERT INTO tenants(id,name) VALUES($1,'auth fixture')`, tenant); err != nil {
		t.Fatal(err)
	}
	hash, err := hashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `INSERT INTO users(tenant_id,email,password_hash,role,email_verified) VALUES($1,$2,$3,'owner',true) RETURNING id`, tenant, email, hash).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	return userID, email
}

func authCall(handler func(http.ResponseWriter, *http.Request) error, target, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	res := httptest.NewRecorder()
	handle(handler)(res, req)
	return res
}

func refreshCookie(t *testing.T, res *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range res.Result().Cookies() {
		if cookie.Name == "refresh_token" && cookie.Value != "" {
			return cookie
		}
	}
	t.Fatalf("no refresh cookie: status=%d body=%s", res.Code, res.Body.String())
	return nil
}

func TestPublicEntryPasswordResetIsSingleUseAndSignsOut(t *testing.T) {
	ctx, db, app := openPublicEntryFixture(t)
	t.Setenv("AUTH_PASSWORD_ENABLED", "true")
	userID, email := authFixtureUser(t, ctx, db, "the old password")
	server := &Server{DB: app, Config: config.Config{AppURL: "https://app.example.com", JWTAccessSecret: "synthetic-access-secret"}}
	login := func(password string) *httptest.ResponseRecorder {
		return authCall(server.loginPassword, "/api/auth/login", `{"email":"`+email+`","password":"`+password+`"}`)
	}
	session := refreshCookie(t, login("the old password"))

	// Known and unknown addresses get the same answer.
	for _, address := range []string{email, "nobody-" + uuid.NewString() + "@fixture.invalid"} {
		if res := authCall(server.forgotPassword, "/api/auth/forgot-password", `{"email":"`+address+`"}`); res.Code != http.StatusOK || res.Body.String() != "{\"ok\":true}\n" {
			t.Fatalf("forgot %s: status=%d body=%q", address, res.Code, res.Body.String())
		}
	}
	var links int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM password_resets WHERE user_id=$1`, userID).Scan(&links); err != nil || links != 1 {
		t.Fatalf("reset links=%d err=%v, want 1", links, err)
	}
	// A second request within a minute sends nothing and keeps the first link.
	_ = authCall(server.forgotPassword, "/api/auth/forgot-password", `{"email":"`+email+`"}`)
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM password_resets WHERE user_id=$1`, userID).Scan(&links); err != nil || links != 1 {
		t.Fatalf("throttled request changed links: %d, %v", links, err)
	}

	// The emailed link carries a token whose hash is stored; plant a known one.
	token := "reset-" + uuid.NewString()
	if _, err := db.Pool.Exec(ctx, `UPDATE password_resets SET token_hash=$2 WHERE user_id=$1`, userID, sha256Hex(token)); err != nil {
		t.Fatal(err)
	}
	if res := authCall(server.resetPassword, "/api/auth/reset-password", `{"token":"`+token+`","password":"short"}`); res.Code != http.StatusBadRequest {
		t.Fatalf("short password: status=%d", res.Code)
	}
	reset := `{"token":"` + token + `","password":"the new password"}`
	if res := authCall(server.resetPassword, "/api/auth/reset-password", reset); res.Code != http.StatusOK {
		t.Fatalf("reset: status=%d body=%s", res.Code, res.Body.String())
	}
	if res := authCall(server.resetPassword, "/api/auth/reset-password", reset); res.Code != http.StatusBadRequest {
		t.Fatalf("a used reset link must not work twice: status=%d", res.Code)
	}
	if res := login("the old password"); res.Code != http.StatusUnauthorized {
		t.Fatalf("old password still works: status=%d", res.Code)
	}
	if res := login("the new password"); res.Code != http.StatusOK {
		t.Fatalf("new password: status=%d body=%s", res.Code, res.Body.String())
	}
	if res := authCall(server.refresh, "/api/auth/refresh", "", session); res.Code != http.StatusUnauthorized {
		t.Fatalf("a session from before the reset still refreshes: status=%d", res.Code)
	}

	// An expired link is refused.
	expired := "reset-" + uuid.NewString()
	if _, err := db.Pool.Exec(ctx, `INSERT INTO password_resets(token_hash,user_id,expires_at) VALUES($1,$2,now()-interval '1 minute')`, sha256Hex(expired), userID); err != nil {
		t.Fatal(err)
	}
	if res := authCall(server.resetPassword, "/api/auth/reset-password", `{"token":"`+expired+`","password":"another password"}`); res.Code != http.StatusBadRequest {
		t.Fatalf("expired link: status=%d", res.Code)
	}
}

func TestPublicEntryRefreshRotationIsAtomic(t *testing.T) {
	ctx, db, app := openPublicEntryFixture(t)
	t.Setenv("AUTH_PASSWORD_ENABLED", "true")
	userID, email := authFixtureUser(t, ctx, db, "rotation password")
	server := &Server{DB: app, Config: config.Config{JWTAccessSecret: "synthetic-access-secret"}}
	session := refreshCookie(t, authCall(server.loginPassword, "/api/auth/login", `{"email":"`+email+`","password":"rotation password"}`))
	other := refreshCookie(t, authCall(server.loginPassword, "/api/auth/login", `{"email":"`+email+`","password":"rotation password"}`))

	// Eight concurrent refreshes of one token: exactly one rotates it, and the
	// rest are treated as a race, not as theft.
	const racers = 8
	results := make([]*httptest.ResponseRecorder, racers)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = authCall(server.refresh, "/api/auth/refresh", "", session)
		}(i)
	}
	wg.Wait()
	var rotated *http.Cookie
	for _, res := range results {
		switch {
		case res.Code == http.StatusOK:
			if rotated != nil {
				t.Fatal("one refresh token rotated twice")
			}
			rotated = refreshCookie(t, res)
		case !strings.Contains(res.Body.String(), "already used"):
			t.Fatalf("racing refresh: status=%d body=%s", res.Code, res.Body.String())
		}
	}
	if rotated == nil {
		t.Fatal("no refresh succeeded")
	}
	if res := authCall(server.refresh, "/api/auth/refresh", "", other); res.Code != http.StatusOK {
		t.Fatalf("a race must not sign out other sessions: status=%d body=%s", res.Code, res.Body.String())
	}

	// Replaying the old token after the grace window is theft: every session ends.
	if _, err := db.Pool.Exec(ctx, `UPDATE refresh_tokens SET revoked_at=now()-interval '1 minute' WHERE token_hash=$1`, sha256Hex(session.Value)); err != nil {
		t.Fatal(err)
	}
	if res := authCall(server.refresh, "/api/auth/refresh", "", session); res.Code != http.StatusUnauthorized || !strings.Contains(res.Body.String(), "reuse detected") {
		t.Fatalf("replay: status=%d body=%s", res.Code, res.Body.String())
	}
	var live int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM refresh_tokens WHERE user_id=$1 AND revoked_at IS NULL`, userID).Scan(&live); err != nil || live != 0 {
		t.Fatalf("live sessions after replay=%d err=%v", live, err)
	}

	// An expired token is refused without touching other sessions.
	fresh := refreshCookie(t, authCall(server.loginPassword, "/api/auth/login", `{"email":"`+email+`","password":"rotation password"}`))
	if _, err := db.Pool.Exec(ctx, `UPDATE refresh_tokens SET expires_at=$2 WHERE token_hash=$1`, sha256Hex(fresh.Value), time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if res := authCall(server.refresh, "/api/auth/refresh", "", fresh); res.Code != http.StatusUnauthorized || !strings.Contains(res.Body.String(), "expired") {
		t.Fatalf("expired: status=%d body=%s", res.Code, res.Body.String())
	}
}
