package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/dataflow-poc/workflow-go/internal/database"
	"github.com/google/uuid"
)

// openPublicEntryFixture opens a superuser pool for fixtures and an RLS-bound
// dataflow_app pool for the code under test, like production compose.
func openPublicEntryFixture(t *testing.T) (context.Context, *database.DB, *database.DB) {
	t.Helper()
	dsn := os.Getenv("CONTROL_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("CONTROL_TEST_DATABASE_URL must name a disposable migrated PostgreSQL database")
	}
	ctx := context.Background()
	db, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	appURL, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	appURL.User = url.UserPassword("dataflow_app", "dataflow_app")
	app, err := database.Open(ctx, appURL.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	return ctx, db, app
}

func TestPublicEntryPaymentWebhookCreditsTenantUnderRLS(t *testing.T) {
	ctx, db, app := openPublicEntryFixture(t)
	tenant, order := uuid.NewString(), "order_"+uuid.NewString()
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id=$1`, tenant)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM payment_orders WHERE tenant_id=$1`, tenant)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM billing_plans WHERE tenant_id=$1`, tenant)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, tenant)
	})
	if _, err := db.Pool.Exec(ctx, `INSERT INTO tenants(id,name) VALUES($1,'payment fixture')`, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO payment_orders(tenant_id,razorpay_order_id,amount_paise,quota_units) VALUES($1,$2,10000,2)`, tenant, order); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RAZORPAY_WEBHOOK_SECRET", "synthetic-razorpay-secret")
	server := &Server{DB: app}
	deliver := func() *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]interface{}{
			"event":   "payment.captured",
			"payload": map[string]interface{}{"payment": map[string]interface{}{"entity": map[string]interface{}{"id": "pay_" + order, "order_id": order}}},
		})
		mac := hmac.New(sha256.New, []byte("synthetic-razorpay-secret"))
		_, _ = mac.Write(body)
		req := httptest.NewRequest(http.MethodPost, "/api/billing/webhook", bytes.NewReader(body))
		req.Header.Set("X-Razorpay-Signature", hex.EncodeToString(mac.Sum(nil)))
		res := httptest.NewRecorder()
		handle(server.billingWebhook)(res, req)
		return res
	}
	if res := deliver(); res.Code != http.StatusOK || strings.Contains(res.Body.String(), "idempotent") {
		t.Fatalf("first delivery status=%d body=%s", res.Code, res.Body.String())
	}
	if res := deliver(); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "idempotent") {
		t.Fatalf("repeat delivery status=%d body=%s", res.Code, res.Body.String())
	}
	var status string
	var extra int
	if err := db.Pool.QueryRow(ctx, `SELECT o.status,b.extra_quota FROM payment_orders o JOIN billing_plans b ON b.tenant_id=o.tenant_id WHERE o.razorpay_order_id=$1`, order).Scan(&status, &extra); err != nil {
		t.Fatal(err)
	}
	if status != "paid" || extra != 10 {
		t.Fatalf("order status=%q extra_quota=%d, want paid and 10 (credited once)", status, extra)
	}
}

func TestPublicEntryOAuthSignupClaimsInvitationUnderRLS(t *testing.T) {
	ctx, db, app := openPublicEntryFixture(t)
	tenant, inviter := uuid.NewString(), uuid.NewString()
	invited := "invited-" + uuid.NewString() + "@fixture.invalid"
	unverified := "unverified-" + uuid.NewString() + "@fixture.invalid"
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id=$1`, tenant)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM user_invitations WHERE tenant_id=$1`, tenant)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM users WHERE tenant_id=$1`, tenant)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, tenant)
	})
	for _, statement := range []struct {
		sql  string
		args []interface{}
	}{
		{`INSERT INTO tenants(id,name) VALUES($1,'invite fixture')`, []interface{}{tenant}},
		{`INSERT INTO users(id,tenant_id,email,role,email_verified) VALUES($1,$2,$3,'owner',true)`, []interface{}{inviter, tenant, inviter + "@fixture.invalid"}},
		{`INSERT INTO users(tenant_id,email,role,email_verified) VALUES($1,$2,'owner',false)`, []interface{}{tenant, unverified}},
		{`INSERT INTO user_invitations(token_hash,tenant_id,invited_by,email,role,expires_at) VALUES($1,$2,$3,$4,'member',now()+interval '1 hour')`, []interface{}{uuid.NewString(), tenant, inviter, invited}},
	} {
		if _, err := db.Pool.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	server := &Server{DB: app}
	req := httptest.NewRequest(http.MethodGet, "/api/auth/google/callback", nil)
	userID, err := server.resolveIdentity(req, "google_sub", "sub-"+uuid.NewString(), invited, "google")
	if err != nil {
		t.Fatal(err)
	}
	var gotTenant, role string
	if err := db.Pool.QueryRow(ctx, `SELECT tenant_id,role FROM users WHERE id=$1`, userID).Scan(&gotTenant, &role); err != nil {
		t.Fatal(err)
	}
	if gotTenant != tenant || role != "member" {
		t.Fatalf("invited OAuth user landed in tenant=%s role=%s, want %s member", gotTenant, role, tenant)
	}
	var pending int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM user_invitations WHERE email=$1 AND accepted_at IS NULL`, invited).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Fatalf("invitation still pending after OAuth sign-up")
	}
	// A provider login must not be linked to an account nobody verified.
	if _, err := server.resolveIdentity(req, "google_sub", "sub-"+uuid.NewString(), unverified, "google"); err != errUnverifiedAccount {
		t.Fatalf("linking to unverified account: err=%v, want errUnverifiedAccount", err)
	}
	var linked *string
	if err := db.Pool.QueryRow(ctx, `SELECT google_sub FROM users WHERE email=$1`, unverified).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked != nil {
		t.Fatalf("unverified account was linked to a provider identity")
	}
}
