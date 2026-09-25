package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync/atomic"
	"testing"

	"github.com/dataflow-poc/workflow-go/internal/activities"
	"github.com/dataflow-poc/workflow-go/internal/connectors"
	"github.com/dataflow-poc/workflow-go/internal/database"
	"github.com/dataflow-poc/workflow-go/internal/dispatchers"
	"github.com/dataflow-poc/workflow-go/internal/model"
	"github.com/google/uuid"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/testsuite"
)

type controlFixture struct {
	db, app                           *database.DB
	tenant, user, pipeline, execution string
}

func newControlFixture(t *testing.T) controlFixture {
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
	f := controlFixture{db: db, app: app, tenant: uuid.NewString(), user: uuid.NewString(), pipeline: uuid.NewString(), execution: "control-test-" + uuid.NewString()}
	t.Cleanup(func() {
		// audit_log intentionally has no cascading tenant delete.
		_, _ = db.Pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id=$1`, f.tenant)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, f.tenant)
	})
	for _, statement := range []struct {
		sql  string
		args []interface{}
	}{
		{`INSERT INTO tenants(id,name) VALUES($1,'control fixture')`, []interface{}{f.tenant}},
		{`INSERT INTO users(id,tenant_id,email,role) VALUES($1,$2,$3,'member')`, []interface{}{f.user, f.tenant, f.user + "@fixture.invalid"}},
		{`INSERT INTO pipelines(id,pipeline_key,tenant_id,name,definition,created_by) VALUES($1,$1,$2,'control fixture','{}',$3)`, []interface{}{f.pipeline, f.tenant, f.user}},
		{`INSERT INTO executions(id,pipeline_id,tenant_id,trigger_type,environment,workflow_id,run_id) VALUES($1,$2,$3,'manual','test',$1,'fixture-run')`, []interface{}{f.execution, f.pipeline, f.tenant}},
	} {
		if _, err := db.Pool.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f controlFixture) request(t *testing.T, actor model.TenantContext, action string, want int) map[string]interface{} {
	t.Helper()
	s := &Server{DB: f.app} // no Temporal client: durable acknowledgement must survive its outage.
	req := httptest.NewRequest(http.MethodPost, "/api/executions/"+f.execution+"/"+action, nil)
	req.SetPathValue("id", f.execution)
	req.SetPathValue("action", action)
	req = withTenant(req, actor)
	res := httptest.NewRecorder()
	handle(s.executionSignal)(res, req)
	if res.Code != want {
		t.Fatalf("%s status=%d want=%d body=%s", action, res.Code, want, res.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestControlAPITransactionsPermissionsAndTerminality(t *testing.T) {
	f := newControlFixture(t)
	ctx := context.Background()
	owner := model.TenantContext{TenantID: f.tenant, UserID: f.user, Role: "owner"}
	stranger := owner
	stranger.Role = "member"
	stranger.UserID = uuid.NewString()
	f.request(t, stranger, "pause", 404)
	cross := owner
	cross.TenantID = uuid.NewString()
	f.request(t, cross, "pause", 404)
	// Creator is permitted without a separate grant, matching pipelineAccess.
	creator := owner
	creator.Role = "member"
	body := f.request(t, creator, "pause", 200)
	if body["ok"] != true || body["controlState"] != "paused" || body["controlRevision"] != float64(1) {
		t.Fatalf("response=%v", body)
	}
	f.request(t, creator, "pause", 200)
	var revision, audits int
	if err := f.db.Pool.QueryRow(ctx, `SELECT control_revision,(SELECT count(*) FROM audit_log WHERE tenant_id=$2) FROM executions WHERE id=$1 AND tenant_id=$2`, f.execution, f.tenant).Scan(&revision, &audits); err != nil {
		t.Fatal(err)
	}
	if revision != 1 || audits != 1 {
		t.Fatalf("duplicate request changed revision/audit: %d/%d", revision, audits)
	}
	// A visible viewer cannot control; editor/admin grants can.
	if _, err := f.db.Pool.Exec(ctx, `UPDATE pipelines SET created_by=NULL WHERE id=$1 AND tenant_id=$2`, f.pipeline, f.tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool.Exec(ctx, `INSERT INTO pipeline_access(pipeline_id,user_id,role,granted_by) VALUES($1,$2,'viewer',$2)`, f.pipeline, f.user); err != nil {
		t.Fatal(err)
	}
	f.request(t, creator, "resume", 403)
	if _, err := f.db.Pool.Exec(ctx, `UPDATE pipeline_access SET role='editor' WHERE pipeline_id=$1 AND user_id=$2`, f.pipeline, f.user); err != nil {
		t.Fatal(err)
	}
	f.request(t, creator, "resume", 200)
	// Force the audit FK to fail. The intent update must roll back with it.
	invalidActor := owner
	invalidActor.UserID = uuid.NewString()
	f.request(t, invalidActor, "pause", 500)
	var state string
	if err := f.db.Pool.QueryRow(ctx, `SELECT control_state,control_revision FROM executions WHERE id=$1 AND tenant_id=$2`, f.execution, f.tenant).Scan(&state, &revision); err != nil {
		t.Fatal(err)
	}
	if state != "active" || revision != 2 {
		t.Fatalf("audit failure committed intent: %s/%d", state, revision)
	}
	f.request(t, creator, "cancel", 200)
	f.request(t, owner, "resume", 409)
	f.request(t, owner, "pause", 409)
	f.request(t, owner, "cancel", 200)
	if _, err := f.db.Pool.Exec(ctx, `UPDATE executions SET phase='cancelled' WHERE id=$1 AND tenant_id=$2`, f.execution, f.tenant); err != nil {
		t.Fatal(err)
	}
	f.request(t, owner, "cancel", 409)
}

type fixtureSignaler func(context.Context, string, string, string, interface{}) error

func (fn fixtureSignaler) SignalWorkflow(ctx context.Context, id, run, signal string, payload interface{}) error {
	return fn(ctx, id, run, signal, payload)
}

func TestControlDispatcherRecoveryAndRevisionCAS(t *testing.T) {
	f := newControlFixture(t)
	ctx := context.Background()
	owner := model.TenantContext{TenantID: f.tenant, UserID: f.user, Role: "owner"}
	f.request(t, owner, "pause", 200)
	calls := 0
	outage := fixtureSignaler(func(context.Context, string, string, string, interface{}) error {
		calls++
		return errors.New("synthetic Temporal outage")
	})
	if err := dispatchers.DispatchExecutionControls(ctx, f.db, outage, "prod"); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("test execution was delivered in prod namespace")
	}
	if err := dispatchers.DispatchExecutionControls(ctx, f.db, outage, "test"); err == nil {
		t.Fatal("expected outage")
	}
	var delivered int64
	if err := f.db.Pool.QueryRow(ctx, `SELECT control_delivered_revision FROM executions WHERE id=$1 AND tenant_id=$2`, f.execution, f.tenant).Scan(&delivered); err != nil {
		t.Fatal(err)
	}
	if delivered != 0 {
		t.Fatalf("outage acknowledged %d", delivered)
	}
	// A new revision resets the retry delay; update it during the successful RPC.
	f.request(t, owner, "resume", 200)
	signaler := fixtureSignaler(func(ctx context.Context, id, run, signal string, payload interface{}) error {
		if id != f.execution || run != "fixture-run" || signal != "resume" {
			t.Fatalf("bad signal %s/%s/%s", id, run, signal)
		}
		if payload.(model.ExecutionControl).Revision != 2 {
			t.Fatalf("bad payload %v", payload)
		}
		// This update would block if delivery held the execution row across the RPC.
		_, err := f.db.Pool.Exec(ctx, `UPDATE executions SET control_state='cancel_requested',control_revision=3,control_next_delivery_at=now() WHERE id=$1 AND tenant_id=$2`, f.execution, f.tenant)
		return err
	})
	if err := dispatchers.DispatchExecutionControls(ctx, f.db, signaler, "test"); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Pool.QueryRow(ctx, `SELECT control_delivered_revision FROM executions WHERE id=$1 AND tenant_id=$2`, f.execution, f.tenant).Scan(&delivered); err != nil {
		t.Fatal(err)
	}
	if delivered != 0 {
		t.Fatalf("older RPC marked newer intent delivered: %d", delivered)
	}
	signaler = fixtureSignaler(func(_ context.Context, id, run, signal string, payload interface{}) error {
		if signal != "cancel" || payload.(model.ExecutionControl).Revision != 3 {
			t.Fatalf("bad latest signal: %s %+v", signal, payload)
		}
		return nil
	})
	if err := dispatchers.DispatchExecutionControls(ctx, f.db, signaler, "test"); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Pool.QueryRow(ctx, `SELECT control_delivered_revision FROM executions WHERE id=$1 AND tenant_id=$2`, f.execution, f.tenant).Scan(&delivered); err != nil {
		t.Fatal(err)
	}
	if delivered != 3 {
		t.Fatalf("latest revision not delivered: %d", delivered)
	}
}

func TestControlDispatcherStopsOnMissingWorkflow(t *testing.T) {
	f := newControlFixture(t)
	ctx := context.Background()
	owner := model.TenantContext{TenantID: f.tenant, UserID: f.user, Role: "owner"}
	f.request(t, owner, "cancel", 200)
	calls := 0
	gone := fixtureSignaler(func(context.Context, string, string, string, interface{}) error {
		calls++
		return serviceerror.NewNotFound("workflow execution already completed")
	})
	if err := dispatchers.DispatchExecutionControls(ctx, f.db, gone, "test"); err != nil {
		t.Fatalf("missing workflow is not a delivery error: %v", err)
	}
	var delivered, revision int64
	var phase string
	if err := f.db.Pool.QueryRow(ctx, `SELECT control_delivered_revision,control_revision,phase FROM executions WHERE id=$1 AND tenant_id=$2`, f.execution, f.tenant).Scan(&delivered, &revision, &phase); err != nil {
		t.Fatal(err)
	}
	if delivered != revision || phase != "running" {
		t.Fatalf("delivered=%d revision=%d phase=%s; want intent closed and phase untouched", delivered, revision, phase)
	}
	if err := dispatchers.DispatchExecutionControls(ctx, f.db, gone, "test"); err != nil || calls != 1 {
		t.Fatalf("undeliverable intent retried: calls=%d err=%v", calls, err)
	}
}

func TestControlActivityAdmissionAndFinalization(t *testing.T) {
	f := newControlFixture(t)
	ctx := context.Background()
	var effects atomic.Int32
	runtime := &connectors.Runtime{Handlers: map[string]connectors.Handler{"fixture.write": func(context.Context, interface{}, map[string]interface{}, connectors.HandlerContext) (interface{}, map[string]interface{}, error) {
		effects.Add(1)
		return nil, nil, nil
	}}}
	runtime.Sources = map[string]connectors.Source{"fixture.fetch": func(context.Context, connectors.SourceParams) (connectors.SourceResult, error) {
		effects.Add(1)
		return connectors.SourceResult{}, nil
	}}
	a := &activities.Activities{DB: f.db, Runtime: runtime}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(a.DispatchNode)
	env.RegisterActivity(a.FetchSourcePage)
	p := activities.DispatchParams{TenantID: f.tenant, ExecutionID: f.execution, NodeID: "sink", ActivityType: "fixture.write", ControlRequired: true}
	for _, state := range []string{"paused", "cancel_requested"} {
		if _, err := f.db.Pool.Exec(ctx, `UPDATE executions SET control_state=$3 WHERE tenant_id=$1 AND id=$2`, f.tenant, f.execution, state); err != nil {
			t.Fatal(err)
		}
		if _, err := env.ExecuteActivity(a.DispatchNode, p); err == nil {
			t.Fatalf("%s admitted a write", state)
		}
		if _, err := env.ExecuteActivity(a.FetchSourcePage, activities.FetchSourceParams{TenantID: f.tenant, ExecutionID: f.execution, NodeID: "source", ActivityType: "fixture.fetch", ControlRequired: true}); err == nil {
			t.Fatalf("%s admitted a source fetch", state)
		}
	}
	if effects.Load() != 0 {
		t.Fatal("blocked activity performed external effect")
	}
	if _, err := f.db.Pool.Exec(ctx, `UPDATE executions SET control_state='active' WHERE tenant_id=$1 AND id=$2`, f.tenant, f.execution); err != nil {
		t.Fatal(err)
	}
	if _, err := env.ExecuteActivity(a.DispatchNode, p); err != nil {
		t.Fatal(err)
	}
	if effects.Load() != 1 {
		t.Fatalf("active effects=%d", effects.Load())
	}
	cross := p
	cross.TenantID = uuid.NewString()
	if _, err := env.ExecuteActivity(a.DispatchNode, cross); err == nil {
		t.Fatal("cross-tenant execution admitted")
	}
	if effects.Load() != 1 {
		t.Fatal("cross-tenant effect")
	}
	if _, err := f.db.Pool.Exec(ctx, `UPDATE executions SET control_state='cancel_requested' WHERE tenant_id=$1 AND id=$2`, f.tenant, f.execution); err != nil {
		t.Fatal(err)
	}
	phase, err := a.FinalizeExecutionControl(ctx, activities.MarkExecutionParams{TenantID: f.tenant, ExecutionID: f.execution, Phase: "completed"})
	if err != nil || phase != "cancelled" {
		t.Fatalf("cancel lost finalization race: %s %v", phase, err)
	}
	phase, err = a.FinalizeExecutionControl(ctx, activities.MarkExecutionParams{TenantID: f.tenant, ExecutionID: f.execution, Phase: "failed"})
	if err != nil || phase != "cancelled" {
		t.Fatalf("terminal outcome changed: %s %v", phase, err)
	}
	if err = a.MarkExecution(ctx, activities.MarkExecutionParams{TenantID: f.tenant, ExecutionID: f.execution, Phase: "failed"}); err != nil {
		t.Fatal(err)
	}
	var alerts int
	if err = f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM pipeline_alerts WHERE tenant_id=$1 AND execution_id=$2`, f.tenant, f.execution).Scan(&alerts); err != nil {
		t.Fatal(err)
	}
	if alerts != 0 {
		t.Fatal("stale failure created an alert after terminal cancellation")
	}
	if _, err = a.FinalizeExecutionControl(ctx, activities.MarkExecutionParams{TenantID: uuid.NewString(), ExecutionID: f.execution, Phase: "completed"}); err == nil {
		t.Fatal("cross-tenant finalization succeeded")
	}
}

func TestControlFirstResumeWakesLegacyHistory(t *testing.T) {
	f := newControlFixture(t)
	owner := model.TenantContext{TenantID: f.tenant, UserID: f.user, Role: "owner"}
	for i := 0; i < 2; i++ {
		body := f.request(t, owner, "resume", 200)
		if body["controlRevision"] != float64(1) || body["controlState"] != "active" {
			t.Fatalf("first/duplicate legacy resume response=%v", body)
		}
	}
	var delivered int64
	if err := f.db.Pool.QueryRow(context.Background(), `SELECT control_delivered_revision FROM executions WHERE tenant_id=$1 AND id=$2`, f.tenant, f.execution).Scan(&delivered); err != nil {
		t.Fatal(err)
	}
	if delivered != 0 {
		t.Fatal("first legacy resume was not left pending")
	}
	calls := 0
	err := dispatchers.DispatchExecutionControls(context.Background(), f.db, fixtureSignaler(func(_ context.Context, id, run, signal string, payload interface{}) error {
		calls++
		if signal != "resume" {
			t.Fatalf("legacy wakeup=%s", signal)
		}
		return nil
	}), "test")
	if err != nil || calls != 1 {
		t.Fatalf("legacy resume not delivered: calls=%d err=%v", calls, err)
	}
}
