package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dataflow-poc/workflow-go/internal/config"
	"github.com/dataflow-poc/workflow-go/internal/model"
	"github.com/google/uuid"
)

// TestControlPipelineVersionAndExecutionAccess covers the role checks on saving
// a new version of an existing pipeline and on execution routes, as the
// RLS-bound application role.
func TestControlPipelineVersionAndExecutionAccess(t *testing.T) {
	f := newControlFixture(t)
	ctx := context.Background()
	collaborator := uuid.NewString()
	if _, err := f.db.Pool.Exec(ctx, `INSERT INTO users(id,tenant_id,email,role) VALUES($1,$2,$3,'member')`, collaborator, f.tenant, collaborator+"@fixture.invalid"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool.Exec(ctx, `INSERT INTO pipeline_access(pipeline_id,user_id,role,granted_by) VALUES($1,$2,'viewer',$3)`, f.pipeline, collaborator, f.user); err != nil {
		t.Fatal(err)
	}
	member := func(grant string) model.TenantContext {
		t.Helper()
		user := uuid.NewString()
		if _, err := f.db.Pool.Exec(ctx, `INSERT INTO users(id,tenant_id,email,role) VALUES($1,$2,$3,'member')`, user, f.tenant, user+"@fixture.invalid"); err != nil {
			t.Fatal(err)
		}
		if grant != "" {
			if _, err := f.db.Pool.Exec(ctx, `INSERT INTO pipeline_access(pipeline_id,user_id,role,granted_by) VALUES($1,$2,$3,$4)`, f.pipeline, user, grant, f.user); err != nil {
				t.Fatal(err)
			}
		}
		return model.TenantContext{TenantID: f.tenant, UserID: user, Role: "member", EmailVerified: true}
	}
	s := &Server{DB: f.app, Config: config.Load()}
	save := func(actor model.TenantContext, key string) *httptest.ResponseRecorder {
		def := model.PipelineDefinition{ID: key, Name: "rbac fixture", Trigger: model.Trigger{Type: "manual"},
			Nodes: []model.Node{{ID: "source", Type: "source", ActivityType: "http.fetch"}, {ID: "sink", Type: "sink", ActivityType: "sink.records"}},
			Edges: []model.Edge{{ID: "edge", Source: "source", Target: "sink"}}}
		body, _ := json.Marshal(def)
		req := withTenant(httptest.NewRequest(http.MethodPost, "/api/pipelines", bytes.NewReader(body)), actor)
		res := httptest.NewRecorder()
		handle(s.pipelineCreate)(res, req)
		return res
	}
	for _, tc := range []struct {
		name  string
		actor model.TenantContext
		want  int
	}{
		{"no grant", member(""), http.StatusNotFound},
		{"viewer", member("viewer"), http.StatusForbidden},
	} {
		if res := save(tc.actor, f.pipeline); res.Code != tc.want {
			t.Fatalf("%s saving another user's pipeline: status=%d want=%d body=%s", tc.name, res.Code, tc.want, res.Body.String())
		}
	}
	editor := member("editor")
	res := save(editor, f.pipeline)
	if res.Code != http.StatusOK {
		t.Fatalf("editor save: status=%d body=%s", res.Code, res.Body.String())
	}
	var saved struct{ RowID string }
	_ = json.Unmarshal(res.Body.Bytes(), &saved)
	var creatorRole, collaboratorRole string
	if err := f.db.Pool.QueryRow(ctx, `SELECT
      (SELECT role FROM pipeline_access WHERE pipeline_id=$1 AND user_id=$2),
      (SELECT role FROM pipeline_access WHERE pipeline_id=$1 AND user_id=$3)`, saved.RowID, f.user, collaborator).Scan(&creatorRole, &collaboratorRole); err != nil {
		t.Fatal(err)
	}
	if creatorRole != "admin" || collaboratorRole != "viewer" {
		t.Fatalf("new version access: previous author=%q collaborator=%q, want admin and viewer", creatorRole, collaboratorRole)
	}
	// A brand-new key is free for any member.
	if res := save(member(""), uuid.NewString()); res.Code != http.StatusOK {
		t.Fatalf("new pipeline: status=%d body=%s", res.Code, res.Body.String())
	}

	reached := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for _, tc := range []struct {
		name    string
		actor   model.TenantContext
		minimum string
		want    int
	}{
		{"no grant reads run", member(""), "viewer", http.StatusNotFound},
		{"viewer reads run", member("viewer"), "viewer", http.StatusNoContent},
		{"viewer retries run", member("viewer"), "editor", http.StatusForbidden},
		{"editor retries run", member("editor"), "editor", http.StatusNoContent},
		{"owner", model.TenantContext{TenantID: f.tenant, UserID: uuid.NewString(), Role: "owner"}, "editor", http.StatusNoContent},
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/executions/"+f.execution, nil)
		req.SetPathValue("id", f.execution)
		res := httptest.NewRecorder()
		s.executionAccess(tc.minimum, reached).ServeHTTP(res, withTenant(req, tc.actor))
		if res.Code != tc.want {
			t.Fatalf("%s: status=%d want=%d", tc.name, res.Code, tc.want)
		}
	}
	list := func(actor model.TenantContext) string {
		req := withTenant(httptest.NewRequest(http.MethodGet, "/api/executions", nil), actor)
		res := httptest.NewRecorder()
		handle(s.executionList)(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("list status=%d body=%s", res.Code, res.Body.String())
		}
		return res.Body.String()
	}
	if strings.Contains(list(member("")), f.execution) {
		t.Fatal("execution list shows runs of a pipeline the member cannot view")
	}
	if !strings.Contains(list(member("viewer")), f.execution) {
		t.Fatal("execution list hides runs from a viewer")
	}

	// Pipeline lists and run lineage follow the same roles.
	pipelines := func(actor model.TenantContext) string {
		req := withTenant(httptest.NewRequest(http.MethodGet, "/api/pipelines", nil), actor)
		res := httptest.NewRecorder()
		handle(s.pipelineList)(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("pipeline list status=%d body=%s", res.Code, res.Body.String())
		}
		return res.Body.String()
	}
	if strings.Contains(pipelines(member("")), f.pipeline) {
		t.Fatal("pipeline list shows a pipeline the member cannot view")
	}
	if !strings.Contains(pipelines(member("viewer")), f.pipeline) {
		t.Fatal("pipeline list hides a pipeline from its viewer")
	}
	lineage := s.executionAccess("viewer", reached)
	req := httptest.NewRequest(http.MethodGet, "/api/lineage/runtime/runs/"+f.execution, nil)
	req.SetPathValue("executionId", f.execution)
	lineageRes := httptest.NewRecorder()
	lineage.ServeHTTP(lineageRes, withTenant(req, member("")))
	if lineageRes.Code != http.StatusNotFound {
		t.Fatalf("run lineage without a grant: status=%d", lineageRes.Code)
	}

	// Promotion keeps the author and sharing, so editors can still save.
	if _, err := f.db.Pool.Exec(ctx, `INSERT INTO executions(id,pipeline_id,tenant_id,trigger_type,environment,workflow_id,run_id,phase) VALUES($1,$2,$3,'manual','test',$1,'fixture-run-2','completed')`, "control-test-"+uuid.NewString(), saved.RowID, f.tenant); err != nil {
		t.Fatal(err)
	}
	owner := model.TenantContext{TenantID: f.tenant, UserID: f.user, Role: "owner"}
	promoteRow := map[string]interface{}{"id": saved.RowID, "environment": "test", "pipeline_key": f.pipeline, "name": "rbac fixture", "version": 2}
	promoteDef := model.PipelineDefinition{ID: f.pipeline, Name: "rbac fixture", Trigger: model.Trigger{Type: "manual"},
		Nodes: []model.Node{{ID: "source", Type: "source", ActivityType: "http.fetch"}, {ID: "sink", Type: "sink", ActivityType: "sink.records"}},
		Edges: []model.Edge{{ID: "edge", Source: "source", Target: "sink"}}}
	promoted, err := s.createProductionVersion(withTenant(httptest.NewRequest(http.MethodPost, "/", nil), owner), promoteRow, promoteDef, false)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	var prodCreator *string
	var prodGrants int
	if err := f.db.Pool.QueryRow(ctx, `SELECT created_by,(SELECT count(*) FROM pipeline_access WHERE pipeline_id=p.id) FROM pipelines p WHERE id=$1`, promoted["rowId"]).Scan(&prodCreator, &prodGrants); err != nil {
		t.Fatal(err)
	}
	if prodCreator == nil || *prodCreator != editor.UserID || prodGrants == 0 {
		t.Fatalf("prod version: created_by=%v grants=%d, want the promoted version's author and sharing", prodCreator, prodGrants)
	}
	if res := save(editor, f.pipeline); res.Code != http.StatusOK {
		t.Fatalf("editor save after promotion: status=%d body=%s", res.Code, res.Body.String())
	}

	// Promotion refuses a webhook path another workspace has active.
	other := uuid.NewString()
	t.Cleanup(func() { _, _ = f.db.Pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, other) })
	path := "shared-" + uuid.NewString()[:8]
	if _, err := f.db.Pool.Exec(ctx, `INSERT INTO tenants(id,name) VALUES($1,'webhook neighbour')`, other); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool.Exec(ctx, `INSERT INTO pipelines(pipeline_key,tenant_id,name,definition,status) VALUES($1,$2,'neighbour',$3,'active')`, uuid.NewString(), other, `{"trigger":{"type":"webhook","path":"`+path+`"}}`); err != nil {
		t.Fatal(err)
	}
	promoteDef.Trigger = model.Trigger{Type: "webhook", Path: path, Secret: strings.Repeat("ab", 10)}
	_, err = s.createProductionVersion(withTenant(httptest.NewRequest(http.MethodPost, "/", nil), owner), promoteRow, promoteDef, false)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != http.StatusConflict || !strings.Contains(httpErr.Message, "already in use") {
		t.Fatalf("promoting onto another workspace's webhook path: err=%v", err)
	}
}
