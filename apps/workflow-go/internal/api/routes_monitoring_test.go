package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/dataflow-poc/workflow-go/internal/model"
	"github.com/google/uuid"
)

func TestMonitoringPipelineRunFields(t *testing.T) {
	f := newControlFixture(t)
	other := newControlFixture(t) // An actual second tenant exercises RLS, not an empty tenant id.
	ctx := context.Background()
	exec := func(sql string, args ...interface{}) {
		t.Helper()
		if _, err := f.db.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE pipelines SET definition='{"metadata":{"owner":"acceptance"}}' WHERE id=$1`, f.pipeline)
	exec(`UPDATE executions SET phase='completed',started_at=now()-interval '1 hour',completed_at=now()-interval '1 hour'+interval '2 seconds' WHERE id=$1`, f.execution)
	s := &Server{DB: f.app}
	actor := model.TenantContext{TenantID: f.tenant, UserID: f.user, Role: "owner"}
	get := func(handler func(http.ResponseWriter, *http.Request) error, path string) map[string]interface{} {
		t.Helper()
		response := httptest.NewRecorder()
		handle(handler)(response, withTenant(httptest.NewRequest(http.MethodGet, path, nil), actor))
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		var body map[string]interface{}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	pipeline := func() map[string]interface{} {
		t.Helper()
		overview := get(s.monitoringOverview, "/api/executions/monitoring/overview?days=7")
		rows := overview["pipelines"].([]interface{})
		if len(rows) != 1 {
			t.Fatalf("expected only own pipeline, got %v (other tenant %s)", rows, other.tenant)
		}
		p := rows[0].(map[string]interface{})
		if p["id"] != f.pipeline || p["metadata"].(map[string]interface{})["owner"] != "acceptance" {
			t.Fatalf("pipeline metadata=%v", p)
		}
		return p
	}
	p := pipeline()
	if p["runs"] != float64(1) || p["avg_duration_ms"] != float64(2000) || p["last_execution_id"] != f.execution || p["last_phase"] != "completed" || p["last_started_at"] == nil {
		t.Fatalf("completed run fields=%v", p)
	}
	// The execution API uses completed_at; finished_at belongs to individual node runs.
	list := get(s.executionList, "/api/executions?paged=1")
	row := list["items"].([]interface{})[0].(map[string]interface{})
	if row["completed_at"] == nil {
		t.Fatalf("execution completion missing: %v", row)
	}
	// Last execution is lifetime data, even when all runs fall outside the selected window.
	exec(`UPDATE executions SET started_at=now()-interval '8 days',completed_at=now()-interval '8 days'+interval '2 seconds' WHERE id=$1`, f.execution)
	p = pipeline()
	if p["runs"] != float64(0) || p["avg_duration_ms"] != float64(0) || p["last_execution_id"] != f.execution {
		t.Fatalf("outside-window fields=%v", p)
	}
	exec(`DELETE FROM executions WHERE id=$1`, f.execution)
	p = pipeline()
	if p["runs"] != float64(0) || p["last_execution_id"] != nil || p["last_phase"] != nil || p["last_started_at"] != nil {
		t.Fatalf("unrun pipeline fields=%v", p)
	}
}

func TestPipelineCurrentViewTargetsActiveVersionAndEffectivePhase(t *testing.T) {
	f := newControlFixture(t)
	ctx := context.Background()
	exec := func(sql string, args ...interface{}) {
		t.Helper()
		if _, err := f.db.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	// v1 (fixture, draft) → v2 active → v3 newer unactivated draft, all in test.
	var active, draft string
	if err := f.db.Pool.QueryRow(ctx, `INSERT INTO pipelines(pipeline_key,version,tenant_id,name,definition,status,created_by,created_at)
    VALUES($1,2,$2,'control fixture','{}','active',$3,now()-interval '1 minute') RETURNING id`, f.pipeline, f.tenant, f.user).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Pool.QueryRow(ctx, `INSERT INTO pipelines(pipeline_key,version,tenant_id,name,definition,created_by)
    VALUES($1,3,$2,'control fixture','{}',$3) RETURNING id`, f.pipeline, f.tenant, f.user).Scan(&draft); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE executions SET control_state='paused',control_revision=1 WHERE id=$1`, f.execution)

	s := &Server{DB: f.app}
	actor := model.TenantContext{TenantID: f.tenant, UserID: f.user, Role: "owner"}
	get := func(handler func(http.ResponseWriter, *http.Request) error, path string) map[string]interface{} {
		t.Helper()
		response := httptest.NewRecorder()
		handle(handler)(response, withTenant(httptest.NewRequest(http.MethodGet, path, nil), actor))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
		var body map[string]interface{}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	rows := get(s.pipelineList, "/api/pipelines?view=current")["rows"].([]interface{})
	if len(rows) != 1 {
		t.Fatalf("view=current rows=%v", rows)
	}
	row := rows[0].(map[string]interface{})
	if row["id"] != draft || row["active_id"] != active || row["active_version"] != float64(2) || row["version_count"] != float64(3) || row["status"] != "active" {
		t.Fatalf("current row must point Edit at v3 and Run at active v2: %v", row)
	}
	if row["last_run_phase"] != "paused" {
		t.Fatalf("pipeline list phase=%v, want paused", row["last_run_phase"])
	}
	// Version history filters by environment server-side.
	if versions := get(s.pipelineList, "/api/pipelines?key="+f.pipeline+"&env=test")["rows"].([]interface{}); len(versions) != 3 {
		t.Fatalf("test-environment versions=%d, want 3", len(versions))
	}
	if versions := get(s.pipelineList, "/api/pipelines?key="+f.pipeline+"&env=prod")["rows"].([]interface{}); len(versions) != 0 {
		t.Fatalf("prod versions=%d, want 0", len(versions))
	}
	items := get(s.executionList, "/api/executions?paged=1&pipelineKey="+f.pipeline+"&env=test")["items"].([]interface{})
	if len(items) != 1 || items[0].(map[string]interface{})["effective_phase"] != "paused" {
		t.Fatalf("execution list must report the same effective phase: %v", items)
	}
}

func TestMonitoringOverviewHidesPipelinesTheUserCannotView(t *testing.T) {
	f := newControlFixture(t)
	ctx := context.Background()
	exec := func(sql string, args ...interface{}) {
		t.Helper()
		if _, err := f.db.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	// The fixture's pipeline (created by f.user) fails with a quality issue; a
	// second pipeline belongs to someone else and is shared with one viewer.
	other, viewer, stranger := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, user := range []string{other, viewer, stranger} {
		exec(`INSERT INTO users(id,tenant_id,email,role) VALUES($1,$2,$3,'member')`, user, f.tenant, user+"@fixture.invalid")
	}
	shared, sharedRun := uuid.NewString(), "control-test-"+uuid.NewString()
	exec(`INSERT INTO pipelines(id,pipeline_key,tenant_id,name,definition,created_by) VALUES($1,$1,$2,'shared pipeline','{}',$3)`, shared, f.tenant, other)
	exec(`INSERT INTO executions(id,pipeline_id,tenant_id,trigger_type,environment,workflow_id,run_id,phase,started_at,completed_at) VALUES($1,$2,$3,'manual','test',$1,'fixture-run','completed',now()-interval '1 hour',now())`, sharedRun, shared, f.tenant)
	exec(`INSERT INTO pipeline_access(pipeline_id,user_id,role,granted_by) VALUES($1,$2,'viewer',$3)`, shared, viewer, other)
	exec(`UPDATE executions SET phase='failed',started_at=now()-interval '1 hour',completed_at=now() WHERE id=$1`, f.execution)
	exec(`INSERT INTO data_quality_results(tenant_id,pipeline_id,execution_id,node_id,status,failed_count,error_samples) VALUES($1,$2,$3,'check','failed',3,'["secret row"]')`, f.tenant, f.pipeline, f.execution)

	s := &Server{DB: f.app}
	overview := func(actor model.TenantContext) map[string]interface{} {
		t.Helper()
		response := httptest.NewRecorder()
		handle(s.monitoringOverview)(response, withTenant(httptest.NewRequest(http.MethodGet, "/api/executions/monitoring/overview?days=7", nil), actor))
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		var body map[string]interface{}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	trendRuns := func(body map[string]interface{}) (total float64) {
		for _, day := range body["trend"].([]interface{}) {
			total += day.(map[string]interface{})["runs"].(float64)
		}
		return total
	}
	pipelineIDs := func(body map[string]interface{}) (ids []string) {
		for _, row := range body["pipelines"].([]interface{}) {
			ids = append(ids, row.(map[string]interface{})["id"].(string))
		}
		return ids
	}
	cases := []struct {
		name                        string
		actor                       model.TenantContext
		runs, failures, qualityRows int
		pipelines                   []string
	}{
		{"owner sees everything", model.TenantContext{TenantID: f.tenant, UserID: stranger, Role: "owner"}, 2, 1, 1, []string{f.pipeline, shared}},
		{"creator sees own pipeline", model.TenantContext{TenantID: f.tenant, UserID: f.user, Role: "member"}, 1, 1, 1, []string{f.pipeline}},
		{"viewer sees shared pipeline", model.TenantContext{TenantID: f.tenant, UserID: viewer, Role: "member"}, 1, 0, 0, []string{shared}},
		{"member without access sees nothing", model.TenantContext{TenantID: f.tenant, UserID: stranger, Role: "member"}, 0, 0, 0, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := overview(tc.actor)
			summary := body["summary"].(map[string]interface{})
			quality := body["quality"].(map[string]interface{})
			ids := pipelineIDs(body)
			if summary["runs"] != float64(tc.runs) || trendRuns(body) != float64(tc.runs) ||
				len(body["recentFailures"].([]interface{})) != tc.failures ||
				quality["checks"] != float64(tc.qualityRows) || len(body["recentQualityIssues"].([]interface{})) != tc.qualityRows ||
				len(ids) != len(tc.pipelines) {
				t.Fatalf("summary=%v trend=%v failures=%v quality=%v issues=%v pipelines=%v", summary, trendRuns(body), body["recentFailures"], quality, body["recentQualityIssues"], ids)
			}
			for _, want := range tc.pipelines {
				if !slices.Contains(ids, want) {
					t.Fatalf("pipelines=%v, want %v", ids, tc.pipelines)
				}
			}
		})
	}
}
