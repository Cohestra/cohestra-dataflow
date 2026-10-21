package api

import (
	"testing"

	"github.com/dataflow-poc/workflow-go/internal/config"
	"github.com/google/uuid"
)

func TestQuotaEnforcedOnlyWhenBillingEnabled(t *testing.T) {
	ctx, db, app := openPublicEntryFixture(t)
	tenant := uuid.NewString()
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM usage_counters WHERE tenant_id=$1`, tenant)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM billing_plans WHERE tenant_id=$1`, tenant)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, tenant)
	})
	if _, err := db.Pool.Exec(ctx, `INSERT INTO tenants(id,name) VALUES($1,'quota fixture')`, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO billing_plans(tenant_id,free_tier_limit) VALUES($1,2)`, tenant); err != nil {
		t.Fatal(err)
	}
	selfHosted := &Server{DB: app}
	for i := 0; i < 5; i++ {
		if err := selfHosted.consumeQuota(ctx, tenant); err != nil {
			t.Fatalf("self-hosted run %d refused: %v", i+1, err)
		}
	}
	var used int
	if err := db.Pool.QueryRow(ctx, `SELECT execution_count FROM usage_counters WHERE tenant_id=$1`, tenant).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if used != 5 {
		t.Fatalf("usage counted %d runs, want 5", used)
	}
	hosted := &Server{DB: app, Config: config.Config{BillingEnabled: true}}
	err := hosted.consumeQuota(ctx, tenant)
	if _, ok := err.(*quotaExceeded); !ok {
		t.Fatalf("billing-enabled run over the limit: err=%v, want quotaExceeded", err)
	}
}
