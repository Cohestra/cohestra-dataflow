package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dataflow-poc/workflow-go/internal/model"
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
