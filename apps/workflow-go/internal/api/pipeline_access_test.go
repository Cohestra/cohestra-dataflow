package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dataflow-poc/workflow-go/internal/model"
	"github.com/google/uuid"
)

func TestPipelineAccessWithApplicationRoleRLS(t *testing.T) {
	f := newControlFixture(t)
	other := newControlFixture(t)
	ctx := context.Background()
	var bypass bool
	if err := f.app.Pool.QueryRow(ctx, `SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&bypass); err != nil {
		t.Fatal(err)
	}
	if bypass {
		t.Fatal("regression requires the application role with RLS enforced")
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
		return model.TenantContext{TenantID: f.tenant, UserID: user, Role: "member"}
	}
	s := &Server{DB: f.app}
	cases := []struct {
		name  string
		actor model.TenantContext
		want  [3]int // viewer, editor, admin requirements
	}{
		{"owner", model.TenantContext{TenantID: f.tenant, UserID: f.user, Role: "owner"}, [3]int{200, 200, 200}},
		{"creator", model.TenantContext{TenantID: f.tenant, UserID: f.user, Role: "member"}, [3]int{200, 200, 200}},
		{"editor", member("editor"), [3]int{200, 200, 403}},
		{"viewer", member("viewer"), [3]int{200, 403, 403}},
		{"admin", member("admin"), [3]int{200, 200, 200}},
		{"no-grant", member(""), [3]int{404, 404, 404}},
		{"cross-tenant-member", model.TenantContext{TenantID: other.tenant, UserID: other.user, Role: "member"}, [3]int{404, 404, 404}},
		{"cross-tenant-owner", model.TenantContext{TenantID: other.tenant, UserID: other.user, Role: "owner"}, [3]int{404, 404, 404}},
	}
	for _, tc := range cases {
		for i, minimum := range []string{"viewer", "editor", "admin"} {
			t.Run(tc.name+"/"+minimum, func(t *testing.T) {
				mux := http.NewServeMux()
				// Exercise the real read handler behind each access level without
				// starting workflows or mutating a pipeline just to test permission.
				mux.Handle("GET /api/pipelines/{rowId}", s.pipelineAccess(minimum, handle(s.pipelineGet)))
				response := httptest.NewRecorder()
				request := withTenant(httptest.NewRequest(http.MethodGet, "/api/pipelines/"+f.pipeline, nil), tc.actor)
				mux.ServeHTTP(response, request)
				if response.Code != tc.want[i] {
					t.Fatalf("status=%d want=%d body=%s", response.Code, tc.want[i], response.Body.String())
				}
				if response.Code == http.StatusOK {
					var body map[string]interface{}
					if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
						t.Fatal(err)
					}
					if body["id"] != f.pipeline || body["tenant_id"] != f.tenant {
						t.Fatalf("wrong pipeline returned: %v", body)
					}
				}
			})
		}
	}
}
