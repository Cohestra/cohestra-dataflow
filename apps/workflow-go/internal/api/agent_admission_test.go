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

	"github.com/dataflow-poc/workflow-go/internal/activities"
	"github.com/dataflow-poc/workflow-go/internal/config"
	"github.com/dataflow-poc/workflow-go/internal/database"
	"github.com/dataflow-poc/workflow-go/internal/model"
	"github.com/google/uuid"
)

func TestAgentAdmissionRejectsStoredDefinitionsBeforeWrites(t *testing.T) {
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
	tenant, user, pipeline := uuid.NewString(), uuid.NewString(), uuid.NewString()
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id=$1`, tenant)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM node_payloads WHERE tenant_id=$1`, tenant)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, tenant)
	})
	for _, statement := range []struct {
		sql  string
		args []interface{}
	}{
		{`INSERT INTO tenants(id,name) VALUES($1,'agent admission fixture')`, []interface{}{tenant}},
		{`INSERT INTO users(id,tenant_id,email,role) VALUES($1,$2,$3,'owner')`, []interface{}{user, tenant, user + "@fixture.invalid"}},
		{`INSERT INTO pipelines(id,pipeline_key,tenant_id,name,definition,status,environment,created_by) VALUES($1,$1,$2,'agent admission fixture','{}','active','test',$3)`, []interface{}{pipeline, tenant, user}},
	} {
		if _, err := db.Pool.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	fixture, err := os.ReadFile("../../../../tests/contracts/agent-pipeline.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"agent", "transform"} {
		t.Run(kind, func(t *testing.T) {
			var def model.PipelineDefinition
			if err := json.Unmarshal(fixture, &def); err != nil {
				t.Fatal(err)
			}
			def.ID, def.TenantID = pipeline, tenant
			def.Nodes[1].Type = kind
			def.Nodes[0].ActivityType = "postgres.fetch"
			def.Nodes[0].Config = map[string]interface{}{"syncMode": "cursor", "cursorType": "date", "cursorColumn": "created_at"}
			def.Trigger = model.Trigger{Type: "webhook", Path: "agent-admission-" + pipeline, Secret: "synthetic-signing-key"}
			stored, err := json.Marshal(def)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Pool.Exec(ctx, `UPDATE pipelines SET definition=$2 WHERE id=$1`, pipeline, stored); err != nil {
				t.Fatal(err)
			}

			// The route can load a stored definition, but must not create a durable job.
			backfill := &Server{DB: app, Config: config.Config{MaxBackfillPartitions: 366}}
			req := httptest.NewRequest(http.MethodPost, "/api/pipelines/"+pipeline+"/backfills", strings.NewReader(`{"from":"2026-01-01T00:00:00Z","to":"2026-01-03T00:00:00Z","partitionDays":1,"maxConcurrency":1}`))
			req.SetPathValue("rowId", pipeline)
			req = withTenant(req, model.TenantContext{TenantID: tenant, UserID: user, Role: "owner"})
			res := httptest.NewRecorder()
			handle(backfill.backfillCreate)(res, req)
			if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "agent") {
				t.Fatalf("backfill status=%d body=%s", res.Code, res.Body.String())
			}

			// Signed public webhooks use the stored row selected before tenant context.
			// Configure a real PostgreSQL payload writer: a misplaced guard would leave
			// an orphan payload for this >4 KiB request even though execution is denied.
			webhook := &Server{DB: db, Payloads: &activities.Payloads{DB: db}}
			payload, err := json.Marshal(map[string]interface{}{"synthetic": strings.Repeat("x", 5000)})
			if err != nil {
				t.Fatal(err)
			}
			req = httptest.NewRequest(http.MethodPost, "/webhooks/"+def.Trigger.Path, bytes.NewReader(payload))
			req.SetPathValue("path", def.Trigger.Path)
			signature := hmac.New(sha256.New, []byte(def.Trigger.Secret))
			_, _ = signature.Write(payload)
			req.Header.Set("X-Signature-Sha256", hex.EncodeToString(signature.Sum(nil)))
			res = httptest.NewRecorder()
			handle(webhook.webhookTrigger)(res, req)
			if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "agent") {
				t.Fatalf("webhook status=%d body=%s", res.Code, res.Body.String())
			}
			for _, table := range []string{"backfill_jobs", "backfill_partitions", "node_payloads", "executions", "audit_log"} {
				var count int
				// Table names are a fixed test-only list, never request input.
				if err := db.Pool.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE tenant_id=$1", tenant).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatalf("rejected agent created %d rows in %s", count, table)
				}
			}
		})
	}
}
