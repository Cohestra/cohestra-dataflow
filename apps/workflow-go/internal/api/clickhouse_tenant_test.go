package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/dataflow-poc/workflow-go/internal/config"
	"github.com/dataflow-poc/workflow-go/internal/model"
	"github.com/google/uuid"
)

func tenantRequest(tenantID string) *http.Request {
	return withTenant(httptest.NewRequest(http.MethodGet, "/api/analytics/datasets", nil), model.TenantContext{TenantID: tenantID})
}

func TestClickhouseQuerySendsTenantAndUsesAnalyticsUser(t *testing.T) {
	var gotUser, gotTenant, gotQuery string
	clickhouse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, _, _ = r.BasicAuth()
		gotTenant = r.URL.Query().Get(clickhouseTenantSetting)
		body, _ := io.ReadAll(r.Body)
		gotQuery = string(body)
		_, _ = io.WriteString(w, `{"n":1}`+"\n")
	}))
	defer clickhouse.Close()
	cfg := config.Config{ClickHouseURL: clickhouse.URL, ClickHouseDB: "dataflow", ClickHouseUser: "dataflow", ClickHousePassword: "main",
		ClickHouseAnalyticsUser: "dataflow_analytics", ClickHouseAnalyticsPassword: "analytics"}
	server := &Server{Config: cfg, HTTP: clickhouse.Client()}
	tenant := uuid.NewString()

	rows, err := server.clickhouseQuery(tenantRequest(tenant), "SELECT 1 AS n")
	if err != nil || len(rows) != 1 || gotUser != "dataflow_analytics" || gotTenant != tenant || gotQuery != "SELECT 1 AS n" {
		t.Fatalf("rows=%v err=%v user=%q tenant=%q query=%q", rows, err, gotUser, gotTenant, gotQuery)
	}
	// Without an analytics user (custom installs) the main user is used and the
	// setting is still sent, so turning the user on later needs no code change.
	server.Config.ClickHouseAnalyticsUser = ""
	if _, err := server.clickhouseQuery(tenantRequest(tenant), "SELECT 1 AS n"); err != nil || gotUser != "dataflow" || gotTenant != tenant {
		t.Fatalf("fallback: err=%v user=%q tenant=%q", err, gotUser, gotTenant)
	}

	gotQuery = ""
	if _, err := server.clickhouseQuery(tenantRequest(""), "SELECT 1"); err == nil || gotQuery != "" {
		t.Fatalf("query without a tenant reached ClickHouse: err=%v", err)
	}
	if _, err := server.clickhouseQuery(tenantRequest(tenant), "SELECT 1 SETTINGS SQL_dataflow_tenant='"+uuid.NewString()+"'"); err == nil || gotQuery != "" {
		t.Fatalf("query overriding the tenant reached ClickHouse: err=%v", err)
	}
}

// TestClickhouseAnalyticsUserIsolatesTenants runs against a real ClickHouse
// started with db/clickhouse/users-analytics.xml (see controls-regression.yml).
func TestClickhouseAnalyticsUserIsolatesTenants(t *testing.T) {
	url := os.Getenv("CLICKHOUSE_TEST_URL")
	if url == "" {
		t.Skip("CLICKHOUSE_TEST_URL names a disposable ClickHouse with users-analytics.xml mounted")
	}
	admin := &Server{Config: config.Config{ClickHouseURL: url, ClickHouseDB: "dataflow", ClickHouseUser: "dataflow", ClickHousePassword: "dataflow"}, HTTP: http.DefaultClient}
	analytics := &Server{Config: admin.Config, HTTP: http.DefaultClient}
	analytics.Config.ClickHouseAnalyticsUser, analytics.Config.ClickHouseAnalyticsPassword = "dataflow_analytics", os.Getenv("CLICKHOUSE_TEST_ANALYTICS_PASSWORD")
	a, b, collection := uuid.NewString(), uuid.NewString(), "isolation-"+uuid.NewString()

	exec := func(query string) {
		t.Helper()
		request, _ := http.NewRequest(http.MethodPost, url+"/?database=dataflow", strings.NewReader(query))
		request.SetBasicAuth("dataflow", "dataflow")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d %s", query, response.StatusCode, body)
		}
	}
	exec(`INSERT INTO sink_records (tenant_id,collection,record,dedup_key) VALUES ('` + a + `','` + collection + `','{}','1'),('` + b + `','` + collection + `','{}','2')`)
	exec(`INSERT INTO execution_metrics (tenant_id,execution_id,pipeline_id,node_id,activity_type,status,duration_ms,record_count) VALUES ('` + b + `','e','` + b + `','n','t','ok',1,1)`)
	t.Cleanup(func() {
		exec(`ALTER TABLE sink_records DELETE WHERE collection='` + collection + `' SETTINGS mutations_sync=1`)
		exec(`ALTER TABLE execution_metrics DELETE WHERE tenant_id='` + b + `' SETTINGS mutations_sync=1`)
	})

	// The query deliberately has no tenant_id clause: the row filter is what
	// keeps tenant B's record out.
	unscoped := "SELECT toString(tenant_id) AS tenant FROM sink_records WHERE collection='" + collection + "'"
	rows, err := analytics.clickhouseQuery(tenantRequest(a), unscoped)
	if err != nil || len(rows) != 1 || rows[0]["tenant"] != a {
		t.Fatalf("tenant A saw %v (err %v), want only its own record", rows, err)
	}
	if rows, err := analytics.clickhouseQuery(tenantRequest(a), "SELECT count() AS n FROM execution_metrics WHERE tenant_id='"+b+"'"); err != nil || len(rows) != 1 || rows[0]["n"] != "0" && rows[0]["n"] != float64(0) {
		t.Fatalf("tenant A read tenant B's metrics: %v (err %v)", rows, err)
	}
	// The real analytics routes (FINAL, JSON functions) work under the grants.
	res := httptest.NewRecorder()
	handle(analytics.analyticsDatasets)(res, tenantRequest(a))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), collection) {
		t.Fatalf("datasets as the analytics user: %d %s", res.Code, res.Body.String())
	}
	if rows, err := admin.clickhouseQuery(tenantRequest(a), unscoped); err != nil || len(rows) != 2 {
		t.Fatalf("the main user (writers, Grafana) must still see every tenant: %v (err %v)", rows, err)
	}
	for name, query := range map[string]string{
		"malformed tenant": "SELECT count() FROM sink_records",
		"table function":   "SELECT * FROM url('http://127.0.0.1:1/', 'CSV', 'a String')",
		"other table":      "SELECT name FROM system.users",
		"write":            "INSERT INTO sink_records (tenant_id,collection,record,dedup_key) VALUES ('" + a + "','" + collection + "','{}','3')",
	} {
		tenant := a
		if name == "malformed tenant" {
			tenant = "not-a-uuid"
		}
		if _, err := analytics.clickhouseQuery(tenantRequest(tenant), query); err == nil {
			t.Errorf("%s: analytics user was allowed", name)
		}
	}
}
