package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dataflow-poc/workflow-go/internal/chsql"
	"github.com/jackc/pgx/v5"
)

// ponytail: flat per-query caps; make per-tenant if a tenant needs more
const (
	chMaxExecution = "max_execution_time=10"
	chMaxRows      = "max_rows_to_read=10000000"
)

func analyticsScope(tenantID, collection string) []chsql.Expr {
	exprs := []chsql.Expr{chsql.Raw("tenant_id=") + chsql.String(tenantID)}
	if collection != "" {
		exprs = append(exprs, chsql.Raw("collection=")+chsql.String(collection))
	}
	return exprs
}

func (s *Server) registerAnalytics(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/analytics/datasets", handle(s.analyticsDatasets))
	mux.HandleFunc("GET /api/analytics/datasets/{name}/schema", handle(s.analyticsSchema))
	mux.HandleFunc("GET /api/analytics/datasets/{name}/rows", handle(s.analyticsRows))
	mux.HandleFunc("POST /api/analytics/query", handle(s.analyticsQuery))
	mux.HandleFunc("GET /api/analytics/dashboards", handle(s.dashboardList))
	mux.HandleFunc("POST /api/analytics/dashboards", handle(s.dashboardCreate))
	mux.HandleFunc("GET /api/analytics/dashboards/{id}", handle(s.dashboardGet))
	mux.HandleFunc("PUT /api/analytics/dashboards/{id}", handle(s.dashboardUpdate))
	mux.HandleFunc("DELETE /api/analytics/dashboards/{id}", handle(s.dashboardDelete))
	mux.HandleFunc("POST /api/analytics/dashboards/{id}/share", handle(s.dashboardShare))
	mux.HandleFunc("GET /api/analytics/dashboards/{id}/shares", handle(s.dashboardShareList))
	mux.HandleFunc("DELETE /api/analytics/dashboards/{id}/shares/{hash}", handle(s.dashboardShareRevoke))
}

// requireDashboardOwnership gates destructive/sharing actions: tenant owners
// always pass, members only for dashboards they created.
func (s *Server) requireDashboardOwnership(r *http.Request, dashboardID string) error {
	tenant := tenantFrom(r)
	var createdBy *string
	err := s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `SELECT created_by::text FROM dashboards WHERE id=$1`, dashboardID).Scan(&createdBy)
	})
	if err != nil {
		return notFound(ErrNotFound, "not found")
	}
	if tenant.Role == "owner" || (createdBy != nil && *createdBy == tenant.UserID) {
		return nil
	}
	return &HTTPError{Status: http.StatusForbidden, Message: "only the dashboard creator or a workspace owner can do this"}
}

func analyticsTimeRangeClauses(from, to string) ([]chsql.Expr, error) {
	start, err := time.Parse(time.RFC3339, from)
	if err != nil {
		return nil, fmt.Errorf("timeRange.from must be RFC3339")
	}
	end, err := time.Parse(time.RFC3339, to)
	if err != nil {
		return nil, fmt.Errorf("timeRange.to must be RFC3339")
	}
	if !start.Before(end) {
		return nil, fmt.Errorf("timeRange.from must be before timeRange.to")
	}
	return []chsql.Expr{
		chsql.Raw("created_at >= ") + chsql.Literal("date", start.UTC().Format(time.RFC3339)),
		chsql.Raw("created_at < ") + chsql.Literal("date", end.UTC().Format(time.RFC3339)),
	}, nil
}

// clickhouseTenantSetting carries the caller's tenant to ClickHouse, where
// the analytics user's row filters (db/clickhouse/users-analytics.xml) only
// return that tenant's rows. It backs up the tenant_id clause in each query.
const clickhouseTenantSetting = "SQL_dataflow_tenant"

func (s *Server) clickhouseQuery(r *http.Request, query string) ([]map[string]interface{}, error) {
	tenantID := tenantFrom(r).TenantID
	if tenantID == "" {
		return nil, errors.New("clickhouse query without a tenant")
	}
	// A SETTINGS clause in the query would override the tenant below. Values
	// from requests are always quoted literals, so this only trips on injection.
	if strings.Contains(query, clickhouseTenantSetting) {
		return nil, errors.New("clickhouse query names the tenant setting")
	}
	endpoint := strings.TrimSuffix(s.Config.ClickHouseURL, "/") + "/?database=" + url.QueryEscape(s.Config.ClickHouseDB) +
		"&default_format=JSONEachRow&" + clickhouseTenantSetting + "=" + url.QueryEscape(tenantID)
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint, strings.NewReader(query))
	if err != nil {
		return nil, err
	}
	user, password := s.Config.ClickHouseUser, s.Config.ClickHousePassword
	if s.Config.ClickHouseAnalyticsUser != "" {
		user, password = s.Config.ClickHouseAnalyticsUser, s.Config.ClickHouseAnalyticsPassword
	}
	request.SetBasicAuth(user, password)
	response, err := s.HTTP.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		var body bytes.Buffer
		_, _ = body.ReadFrom(response.Body)
		return nil, fmt.Errorf("clickhouse %d: %s", response.StatusCode, body.String())
	}
	rows := []map[string]interface{}{}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 1024), 10<<20)
	for scanner.Scan() {
		row := map[string]interface{}{}
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, scanner.Err()
}

func (s *Server) analyticsDatasets(w http.ResponseWriter, r *http.Request) error {
	tenant := tenantFrom(r)
	query, err := chsql.NewSelect().
		Column(chsql.Raw("collection"), "").
		Column(chsql.Raw("count()"), "row_count").
		From("sink_records").Final().
		Where(analyticsScope(tenant.TenantID, "")...).
		GroupBy(chsql.Raw("collection")).
		OrderBy(chsql.Raw("collection"), false).
		Build()
	if err != nil {
		return err
	}
	rows, err := s.clickhouseQuery(r, query)
	if err != nil {
		return &HTTPError{Status: http.StatusBadGateway, Message: "clickhouse error"}
	}
	for _, row := range rows {
		row["row_count"] = numberInt(row["row_count"])
	}
	jsonResponse(w, http.StatusOK, rows)
	return nil
}

func inferSchema(rows []map[string]interface{}) []map[string]string {
	types := map[string]string{}
	for _, row := range rows {
		for key, value := range row {
			kind := "string"
			switch value.(type) {
			case float64, json.Number:
				kind = "number"
			case bool:
				kind = "boolean"
			}
			if text, ok := value.(string); ok {
				if _, err := time.Parse(time.RFC3339, text); err == nil {
					kind = "date"
				}
			}
			if existing := types[key]; existing == "" || existing == kind {
				types[key] = kind
			} else {
				types[key] = "string"
			}
		}
	}
	keys := make([]string, 0, len(types))
	for key := range types {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]map[string]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, map[string]string{"name": key, "type": types[key]})
	}
	return out
}
func (s *Server) datasetRecords(r *http.Request, name string) ([]map[string]interface{}, error) {
	tenant := tenantFrom(r)
	query, err := chsql.NewSelect().
		Column(chsql.Raw("record"), "").
		From("sink_records").Final().
		Where(analyticsScope(tenant.TenantID, name)...).
		Limit(100).
		Build()
	if err != nil {
		return nil, err
	}
	raw, err := s.clickhouseQuery(r, query)
	if err != nil {
		return nil, err
	}
	records := []map[string]interface{}{}
	for _, row := range raw {
		switch value := row["record"].(type) {
		case string:
			record := map[string]interface{}{}
			if json.Unmarshal([]byte(value), &record) == nil {
				records = append(records, record)
			}
		case map[string]interface{}:
			records = append(records, value)
		}
	}
	return records, nil
}
func (s *Server) analyticsSchema(w http.ResponseWriter, r *http.Request) error {
	records, err := s.datasetRecords(r, r.PathValue("name"))
	if err != nil {
		return &HTTPError{Status: http.StatusBadGateway, Message: "clickhouse error"}
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"collection": r.PathValue("name"), "schema": inferSchema(records)})
	return nil
}

func (s *Server) analyticsRows(w http.ResponseWriter, r *http.Request) error {
	tenant := tenantFrom(r)
	name := r.PathValue("name")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	scope := analyticsScope(tenant.TenantID, name)
	countQuery, err := chsql.NewSelect().
		Column(chsql.Raw("count()"), "total").
		From("sink_records").Final().
		Where(scope...).
		Build()
	if err != nil {
		return err
	}
	total, err := s.clickhouseQuery(r, countQuery)
	if err != nil {
		return &HTTPError{Status: http.StatusBadGateway, Message: "clickhouse error"}
	}
	rowsQuery, err := chsql.NewSelect().
		Column(chsql.Raw("record"), "").
		Column(chsql.Raw("created_at"), "").
		From("sink_records").Final().
		Where(scope...).
		OrderBy(chsql.Raw("created_at"), true).
		Limit(limit).Offset(offset).
		Settings(chMaxExecution).Settings(chMaxRows).
		Build()
	if err != nil {
		return err
	}
	raw, err := s.clickhouseQuery(r, rowsQuery)
	if err != nil {
		return &HTTPError{Status: http.StatusBadGateway, Message: "clickhouse error"}
	}
	rows := make([]map[string]interface{}, 0, len(raw))
	for _, row := range raw {
		record := map[string]interface{}{}
		if text, ok := row["record"].(string); ok {
			_ = json.Unmarshal([]byte(text), &record)
		} else if m, ok := row["record"].(map[string]interface{}); ok {
			record = m
		}
		record["_ingested_at"] = row["created_at"]
		rows = append(rows, record)
	}
	count := 0
	if len(total) > 0 {
		count = numberInt(total[0]["total"])
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"rows": rows, "total": count, "limit": limit, "offset": offset})
	return nil
}

func (s *Server) analyticsQuery(w http.ResponseWriter, r *http.Request) error {
	var spec struct {
		Dataset         string `json:"dataset"`
		Select, GroupBy []string
		Where           []struct {
			Field, Op string
			Value     interface{}
		}
		Aggregate *struct{ Field, Fn string }
		OrderBy   *struct{ Field, Dir string }
		TimeRange *struct{ From, To string }
		Bucket    string
		Limit     int
	}
	if !decodeJSON(w, r, &spec) {
		return nil
	}
	if spec.Dataset == "" {
		return badRequest(ErrInvalidRequest, "dataset is required")
	}
	records, err := s.datasetRecords(r, spec.Dataset)
	if err != nil {
		return &HTTPError{Status: http.StatusBadGateway, Message: "clickhouse error fetching schema"}
	}
	schema := map[string]string{}
	for _, item := range inferSchema(records) {
		schema[item["name"]] = item["type"]
	}
	field := func(name string) (chsql.Expr, error) {
		kind, ok := schema[name]
		if !ok {
			return "", fmt.Errorf("Field %q is not in the dataset schema", name)
		}
		return chsql.JSONField(chsql.Raw("record"), name, kind)
	}
	builder := chsql.NewSelect().From("sink_records").Final()
	hasColumns := false
	for _, name := range spec.Select {
		expr, err := field(name)
		if err != nil {
			return badRequest(ErrInvalidRequest, err.Error())
		}
		builder.Column(expr, name)
		hasColumns = true
	}
	groupExprs := []chsql.Expr{}
	for _, name := range spec.GroupBy {
		expr, err := field(name)
		if err != nil {
			return badRequest(ErrInvalidRequest, err.Error())
		}
		groupExprs = append(groupExprs, expr)
		if !slices.Contains(spec.Select, name) {
			builder.Column(expr, name)
			hasColumns = true
		}
	}
	hasBucket := false
	if spec.Bucket != "" {
		intervals := map[string]string{
			"minute": "1 minute", "5 minute": "5 minute", "15 minute": "15 minute",
			"hour": "1 hour", "day": "1 day", "week": "1 week",
		}
		interval, ok := intervals[spec.Bucket]
		if !ok {
			return badRequest(ErrInvalidRequest, "invalid bucket; use minute, 5 minute, 15 minute, hour, day, or week")
		}
		bucketExpr := chsql.Raw("toStartOfInterval(created_at, INTERVAL " + interval + ")")
		builder.Column(bucketExpr, "time_bucket")
		groupExprs = append(groupExprs, bucketExpr)
		hasBucket = true
		hasColumns = true
	}
	if spec.Aggregate != nil {
		if !map[string]bool{"count": true, "sum": true, "avg": true, "min": true, "max": true}[spec.Aggregate.Fn] {
			return badRequest(ErrInvalidRequest, "invalid aggregate function")
		}
		expr, err := field(spec.Aggregate.Field)
		if err != nil {
			return badRequest(ErrInvalidRequest, err.Error())
		}
		builder.Column(chsql.Raw(spec.Aggregate.Fn+"(")+expr+chsql.Raw(")"), "aggregate_value")
		hasColumns = true
	}
	if !hasColumns {
		builder.Column(chsql.Raw("record"), "")
	}
	tenant := tenantFrom(r)
	builder.Where(analyticsScope(tenant.TenantID, spec.Dataset)...)
	if spec.TimeRange != nil {
		clauses, err := analyticsTimeRangeClauses(spec.TimeRange.From, spec.TimeRange.To)
		if err != nil {
			return badRequest(ErrInvalidRequest, err.Error())
		}
		builder.Where(clauses...)
	}
	for _, clause := range spec.Where {
		expr, err := field(clause.Field)
		if err != nil {
			return badRequest(ErrInvalidRequest, err.Error())
		}
		kind := schema[clause.Field]
		if clause.Op == "IN" {
			values, ok := clause.Value.([]interface{})
			if !ok || len(values) == 0 {
				return badRequest(ErrInvalidRequest, "IN value must be a non-empty array")
			}
			literals := make([]chsql.Expr, len(values))
			for i, value := range values {
				literals[i] = chsql.Literal(kind, value)
			}
			builder.Where(chsql.In(expr, literals))
		} else {
			cond, err := chsql.Compare(expr, clause.Op, chsql.Literal(kind, clause.Value))
			if err != nil {
				return badRequest(ErrInvalidRequest, "invalid operator")
			}
			builder.Where(cond)
		}
	}
	builder.GroupBy(groupExprs...)
	if spec.OrderBy == nil && hasBucket {
		builder.OrderBy(chsql.Raw("time_bucket"), false)
	}
	if spec.OrderBy != nil {
		desc := spec.OrderBy.Dir == "DESC"
		if spec.OrderBy.Field == "aggregate_value" || spec.OrderBy.Field == "time_bucket" {
			builder.OrderBy(chsql.Raw(spec.OrderBy.Field), desc)
		} else {
			expr, err := field(spec.OrderBy.Field)
			if err != nil {
				return badRequest(ErrInvalidRequest, err.Error())
			}
			builder.OrderBy(expr, desc)
		}
	}
	if spec.Limit < 1 {
		spec.Limit = 1000
	}
	if spec.Limit > 10000 {
		spec.Limit = 10000
	}
	query, err := builder.Limit(spec.Limit).Settings(chMaxExecution).Settings(chMaxRows).Build()
	if err != nil {
		return err
	}
	rows, err := s.clickhouseQuery(r, query)
	if err != nil {
		return &HTTPError{Status: http.StatusBadGateway, Message: "clickhouse query failed"}
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"rows": rows, "count": len(rows)})
	return nil
}
func (s *Server) dashboardList(w http.ResponseWriter, r *http.Request) error {
	tenant := tenantFrom(r)
	rows, err := tenantQueryRows(r.Context(), s.DB, tenant.TenantID, `SELECT id,name,definition,created_by,created_at,updated_at FROM dashboards ORDER BY updated_at DESC`)
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, rows)
	return nil
}
func (s *Server) dashboardCreate(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Name       string
		Definition map[string]interface{}
	}
	if !decodeJSON(w, r, &body) {
		return nil
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		return badRequest(ErrInvalidRequest, "name is required")
	}
	if body.Definition == nil {
		return badRequest(ErrInvalidRequest, "definition object is required")
	}
	tenant := tenantFrom(r)
	var row map[string]interface{}
	err := s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `INSERT INTO dashboards (tenant_id,name,definition,created_by) VALUES ($1,$2,$3,$4) RETURNING id,name,definition,created_by,created_at,updated_at`, tenant.TenantID, body.Name, body.Definition, tenant.UserID)
		if err != nil {
			return err
		}
		row, err = oneMap(rows)
		return err
	})
	if err != nil {
		return err
	}
	s.audit(r.Context(), tenant, "dashboard.created", stringValue(row["id"]), map[string]string{"name": body.Name}, r)
	jsonResponse(w, http.StatusCreated, row)
	return nil
}
func (s *Server) dashboardGet(w http.ResponseWriter, r *http.Request) error {
	tenant := tenantFrom(r)
	rows, err := tenantQueryRows(r.Context(), s.DB, tenant.TenantID, `SELECT id,name,definition,created_by,created_at,updated_at FROM dashboards WHERE id=$1`, r.PathValue("id"))
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return notFound(ErrNotFound, "not found")
	}
	jsonResponse(w, http.StatusOK, rows[0])
	return nil
}
func (s *Server) dashboardUpdate(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Name       *string
		Definition map[string]interface{}
	}
	if !decodeJSON(w, r, &body) {
		return nil
	}
	if body.Name == nil && body.Definition == nil {
		return badRequest(ErrInvalidRequest, "name or definition is required")
	}
	if body.Name != nil {
		trimmed := strings.TrimSpace(*body.Name)
		if trimmed == "" {
			return badRequest(ErrInvalidRequest, "name is required")
		}
		body.Name = &trimmed
	}
	tenant := tenantFrom(r)
	var row map[string]interface{}
	err := s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `UPDATE dashboards SET name=coalesce($2,name),definition=coalesce($3,definition),updated_at=now() WHERE id=$1 RETURNING id,name,definition,created_by,created_at,updated_at`, r.PathValue("id"), body.Name, body.Definition)
		if err != nil {
			return err
		}
		row, err = oneMap(rows)
		return err
	})
	if err != nil {
		return err
	}
	if row == nil {
		return notFound(ErrNotFound, "not found")
	}
	jsonResponse(w, http.StatusOK, row)
	return nil
}
func (s *Server) dashboardDelete(w http.ResponseWriter, r *http.Request) error {
	if err := s.requireDashboardOwnership(r, r.PathValue("id")); err != nil {
		return err
	}
	tenant := tenantFrom(r)
	changed := false
	err := s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		cmd, err := tx.Exec(r.Context(), `DELETE FROM dashboards WHERE id=$1`, r.PathValue("id"))
		changed = cmd.RowsAffected() > 0
		return err
	})
	if err != nil {
		return err
	}
	if !changed {
		return notFound(ErrNotFound, "not found")
	}
	jsonResponse(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}
func (s *Server) dashboardShare(w http.ResponseWriter, r *http.Request) error {
	if err := s.requireDashboardOwnership(r, r.PathValue("id")); err != nil {
		return err
	}
	tenant := tenantFrom(r)
	token := randomToken()
	expires := time.Now().Add(24 * time.Hour)
	err := s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		var one string
		if err := tx.QueryRow(r.Context(), `SELECT id FROM dashboards WHERE id=$1`, r.PathValue("id")).Scan(&one); err != nil {
			return notFound(ErrNotFound, "dashboard not found")
		}
		_, err := tx.Exec(r.Context(), `INSERT INTO dashboard_shares (share_token_hash,dashboard_id,tenant_id,expires_at,created_by) VALUES ($1,$2,$3,$4,$5)`, sha256Hex(token), r.PathValue("id"), tenant.TenantID, expires, tenant.UserID)
		return err
	})
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"shareToken": token, "expiresAt": expires.UTC().Format(time.RFC3339Nano), "shareUrl": "/api/analytics/shared/" + token})
	return nil
}
func (s *Server) dashboardShareList(w http.ResponseWriter, r *http.Request) error {
	if err := s.requireDashboardOwnership(r, r.PathValue("id")); err != nil {
		return err
	}
	tenant := tenantFrom(r)
	rows, err := tenantQueryRows(r.Context(), s.DB, tenant.TenantID,
		`SELECT share_token_hash,expires_at,created_by,created_at FROM dashboard_shares WHERE dashboard_id=$1 AND tenant_id=$2 AND expires_at > now() ORDER BY created_at DESC`, r.PathValue("id"), tenant.TenantID)
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, rows)
	return nil
}
func (s *Server) dashboardShareRevoke(w http.ResponseWriter, r *http.Request) error {
	if err := s.requireDashboardOwnership(r, r.PathValue("id")); err != nil {
		return err
	}
	tenant := tenantFrom(r)
	changed := false
	err := s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		cmd, err := tx.Exec(r.Context(), `DELETE FROM dashboard_shares WHERE dashboard_id=$1 AND share_token_hash=$2 AND tenant_id=$3`, r.PathValue("id"), r.PathValue("hash"), tenant.TenantID)
		changed = cmd.RowsAffected() > 0
		return err
	})
	if err != nil {
		return err
	}
	if !changed {
		return notFound(ErrNotFound, "share not found")
	}
	s.audit(r.Context(), tenant, "dashboard.share_revoked", r.PathValue("id"), map[string]string{"share": r.PathValue("hash")}, r)
	jsonResponse(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}

func (s *Server) analyticsShared(w http.ResponseWriter, r *http.Request) error {
	var dashboardID, tenantID string
	var expires time.Time
	if err := s.DB.Pool.QueryRow(r.Context(), `SELECT dashboard_id,tenant_id,expires_at FROM dashboard_shares WHERE share_token_hash=$1`, sha256Hex(r.PathValue("token"))).Scan(&dashboardID, &tenantID, &expires); err != nil {
		return notFound(ErrNotFound, "share not found or expired")
	}
	if expires.Before(time.Now()) {
		return &HTTPError{Status: http.StatusGone, Message: "share has expired"}
	}
	rows, err := tenantQueryRows(r.Context(), s.DB, tenantID, `SELECT id,name,definition,created_at,updated_at FROM dashboards WHERE id=$1`, dashboardID)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return notFound(ErrNotFound, "dashboard not found")
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"dashboard": rows[0], "expiresAt": expires, "readOnly": true})
	return nil
}
