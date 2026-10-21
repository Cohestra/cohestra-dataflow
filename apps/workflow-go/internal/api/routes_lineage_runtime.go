package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Runtime lineage: aggregated execution metrics over a bounded time window,
// following the New Relic grouped-traces pattern — stable pipeline/asset
// topology from definitions, metrics only from executions inside the window,
// drill-down into a single execution on demand.

const maxRuntimeWindow = 7 * 24 * time.Hour

var runtimePhases = map[string]bool{"running": true, "completed": true, "failed": true, "cancelled": true}

func (s *Server) registerLineageRuntime(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/lineage/runtime/overview", handle(s.runtimeLineageOverview))
	mux.HandleFunc("GET /api/lineage/runtime/runs", handle(s.runtimeLineageRuns))
	mux.Handle("GET /api/lineage/runtime/runs/{executionId}", s.executionAccess("viewer", handle(s.runtimeLineageRunDetail)))
}

// parseRuntimeWindow reads from/to (UTC RFC3339). Missing bounds default to
// the last hour ending now. Ranges longer than seven days are rejected —
// enforced here so no aggregate or run query can scan unbounded history.
func parseRuntimeWindow(q url.Values) (time.Time, time.Time, error) {
	to := time.Now().UTC()
	if raw := q.Get("to"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return time.Time{}, time.Time{}, badRequest(ErrInvalidRequest, "to must be an RFC3339 UTC timestamp")
		}
		to = parsed.UTC()
	}
	from := to.Add(-time.Hour)
	if raw := q.Get("from"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return time.Time{}, time.Time{}, badRequest(ErrInvalidRequest, "from must be an RFC3339 UTC timestamp")
		}
		from = parsed.UTC()
	}
	if !from.Before(to) {
		return time.Time{}, time.Time{}, badRequest(ErrInvalidRequest, "from must be before to")
	}
	if to.Sub(from) > maxRuntimeWindow {
		return time.Time{}, time.Time{}, badRequest(ErrInvalidRequest, "time range cannot exceed 7 days")
	}
	// The whole runtime view is bounded to the last seven days (the detail
	// endpoint enforces the same recency), so a bounded-but-older custom
	// window would list runs whose drill-down 404s. Reject it up front.
	if from.Before(time.Now().UTC().Add(-maxRuntimeWindow - time.Minute)) {
		return time.Time{}, time.Time{}, badRequest(ErrInvalidRequest, "time range must fall within the last 7 days")
	}
	return from, to, nil
}

var runtimeLayers = map[string]bool{"external": true, "bronze": true, "silver": true, "gold": true}

type runtimeFilters struct {
	from, to    time.Time
	environment string
	pipelineKey string // resolved from the pipeline row id filter
	status      string
	layer       string // medallion layer any of the pipeline's nodes touches
	viewer      string // non-owner user id: only pipelines they may view
}

func (s *Server) parseRuntimeFilters(r *http.Request) (runtimeFilters, error) {
	q := r.URL.Query()
	from, to, err := parseRuntimeWindow(q)
	if err != nil {
		return runtimeFilters{}, err
	}
	filters := runtimeFilters{from: from, to: to, environment: q.Get("environment"), status: q.Get("status")}
	if tenant := tenantFrom(r); tenant.Role != "owner" {
		filters.viewer = tenant.UserID
	}
	if filters.environment != "" && filters.environment != "test" && filters.environment != "prod" {
		return runtimeFilters{}, badRequest(ErrInvalidRequest, "environment must be test or prod")
	}
	if filters.status != "" && !runtimePhases[filters.status] {
		return runtimeFilters{}, badRequest(ErrInvalidRequest, "status must be running, completed, failed, or cancelled")
	}
	filters.layer = q.Get("layer")
	if filters.layer != "" && !runtimeLayers[filters.layer] {
		return runtimeFilters{}, badRequest(ErrInvalidRequest, "layer must be external, bronze, silver, or gold")
	}
	if pipeline := q.Get("pipeline"); pipeline != "" {
		tenant := tenantFrom(r)
		rows, err := tenantQueryRows(r.Context(), s.DB, tenant.TenantID, `SELECT pipeline_key FROM pipelines WHERE id::text=$1 OR pipeline_key::text=$1 LIMIT 1`, pipeline)
		if err != nil {
			return runtimeFilters{}, err
		}
		if len(rows) == 0 {
			return runtimeFilters{}, notFound(ErrNotFound, "pipeline not found")
		}
		filters.pipelineKey = stringValue(rows[0]["pipeline_key"])
	}
	return filters, nil
}

// executionWhere builds the WHERE fragment shared by the aggregate queries.
// alias e = executions, p = pipelines (already joined by the caller).
func (f runtimeFilters) executionWhere(args *[]interface{}) string {
	*args = append(*args, f.from, f.to)
	where := []string{fmt.Sprintf("e.started_at>=$%d AND e.started_at<$%d", len(*args)-1, len(*args))}
	if f.viewer != "" {
		*args = append(*args, f.viewer)
		where = append(where, pipelineVisibleSQL("p", len(*args)))
	}
	if f.environment != "" {
		*args = append(*args, f.environment)
		where = append(where, fmt.Sprintf("e.environment=$%d", len(*args)))
	}
	if f.pipelineKey != "" {
		*args = append(*args, f.pipelineKey)
		where = append(where, fmt.Sprintf("p.pipeline_key::text=$%d", len(*args)))
	}
	if f.status != "" {
		*args = append(*args, f.status)
		where = append(where, fmt.Sprintf("e.phase=$%d", len(*args)))
	}
	if f.layer != "" {
		*args = append(*args, f.layer)
		where = append(where, fmt.Sprintf(`EXISTS (SELECT 1 FROM jsonb_array_elements(p.definition->'nodes') node WHERE node->'config'->>'layer'=$%d)`, len(*args)))
	}
	return strings.Join(where, " AND ")
}

func (s *Server) runtimeLineageOverview(w http.ResponseWriter, r *http.Request) error {
	filters, err := s.parseRuntimeFilters(r)
	if err != nil {
		return err
	}
	tenant := tenantFrom(r)

	var topology, pipelineMetrics, nodeMetrics []map[string]interface{}
	err = s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		topologyWhere, topologyArgs := []string{"TRUE"}, []interface{}{}
		if filters.viewer != "" {
			topologyArgs = append(topologyArgs, filters.viewer)
			topologyWhere = append(topologyWhere, pipelineVisibleSQL("p", len(topologyArgs)))
		}
		if filters.environment != "" {
			topologyArgs = append(topologyArgs, filters.environment)
			topologyWhere = append(topologyWhere, fmt.Sprintf("environment=$%d", len(topologyArgs)))
		}
		if filters.pipelineKey != "" {
			topologyArgs = append(topologyArgs, filters.pipelineKey)
			topologyWhere = append(topologyWhere, fmt.Sprintf("pipeline_key::text=$%d", len(topologyArgs)))
		}
		rows, err := tx.Query(r.Context(), fmt.Sprintf(`WITH ranked AS (
			SELECT id,pipeline_key,version,name,status,environment,definition,
			  row_number() OVER(PARTITION BY pipeline_key,environment ORDER BY CASE status WHEN 'active' THEN 0 WHEN 'draft' THEN 1 ELSE 2 END,version DESC) rank
			FROM pipelines p WHERE %s) SELECT id,pipeline_key,version,name,status,environment,definition FROM ranked WHERE rank=1 ORDER BY environment,name`,
			strings.Join(topologyWhere, " AND ")), topologyArgs...)
		if err != nil {
			return err
		}
		if topology, err = rowsToMaps(rows); err != nil {
			return err
		}

		args := []interface{}{}
		where := filters.executionWhere(&args)
		rows, err = tx.Query(r.Context(), fmt.Sprintf(`SELECT p.pipeline_key::text AS pipeline_key,e.environment,
			count(*)::int AS runs,
			count(*) FILTER (WHERE e.phase='completed')::int AS succeeded,
			count(*) FILTER (WHERE e.phase='failed')::int AS failed,
			count(*) FILTER (WHERE e.phase='running')::int AS running,
			count(*) FILTER (WHERE e.phase='cancelled')::int AS cancelled,
			round(coalesce((percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM e.completed_at-e.started_at)*1000) FILTER (WHERE e.completed_at IS NOT NULL)),0))::bigint AS p50_ms,
			round(coalesce((percentile_cont(0.95) WITHIN GROUP (ORDER BY extract(epoch FROM e.completed_at-e.started_at)*1000) FILTER (WHERE e.completed_at IS NOT NULL)),0))::bigint AS p95_ms,
			max(e.started_at) AS last_run_at
			FROM executions e JOIN pipelines p ON p.id=e.pipeline_id WHERE %s GROUP BY 1,2`, where), args...)
		if err != nil {
			return err
		}
		if pipelineMetrics, err = rowsToMaps(rows); err != nil {
			return err
		}

		args = []interface{}{}
		where = filters.executionWhere(&args)
		rows, err = tx.Query(r.Context(), fmt.Sprintf(`SELECT p.pipeline_key::text AS pipeline_key,e.environment,nr.node_id,
			count(*)::int AS runs,
			count(*) FILTER (WHERE nr.status='failed')::int AS failed,
			coalesce(sum(nr.record_count),0)::bigint AS records,
			max(coalesce(nr.finished_at,nr.started_at)) AS last_at
			FROM node_runs nr JOIN executions e ON e.id=nr.execution_id JOIN pipelines p ON p.id=e.pipeline_id
			WHERE %s GROUP BY 1,2,3`, where), args...)
		if err != nil {
			return err
		}
		nodeMetrics, err = rowsToMaps(rows)
		return err
	})
	if err != nil {
		return err
	}

	jsonResponse(w, http.StatusOK, buildRuntimeOverview(filters, topology, pipelineMetrics, nodeMetrics))
	return nil
}

// buildRuntimeOverview derives the stable asset/pipeline topology from the
// pipeline definitions and decorates it with the window's execution metrics.
// Metrics are grouped by (pipeline_key, environment) so runs of superseded
// versions still roll up into the currently displayed pipeline node.
func buildRuntimeOverview(filters runtimeFilters, topology, pipelineMetrics, nodeMetrics []map[string]interface{}) map[string]interface{} {
	graph := buildWorkspaceLineage(topology)
	nodes, _ := graph["nodes"].([]map[string]interface{})
	edges, _ := graph["edges"].([]map[string]interface{})

	rowKey := map[string]string{} // pipeline row id -> "key:env"
	for _, row := range topology {
		rowKey[stringValue(row["id"])] = stringValue(row["pipeline_key"]) + ":" + stringValue(row["environment"])
	}
	metricsByPipeline := map[string]map[string]interface{}{}
	totals := map[string]int{"runs": 0, "succeeded": 0, "failed": 0, "running": 0}
	for _, row := range pipelineMetrics {
		runs, succeeded, failed := numberInt(row["runs"]), numberInt(row["succeeded"]), numberInt(row["failed"])
		metric := map[string]interface{}{
			"runs": runs, "succeeded": succeeded, "failed": failed,
			"running": numberInt(row["running"]), "cancelled": numberInt(row["cancelled"]),
			"p50Ms": row["p50_ms"], "p95Ms": row["p95_ms"], "lastRunAt": row["last_run_at"],
		}
		if runs > 0 {
			metric["successRate"] = float64(int(float64(succeeded)/float64(runs)*1000)) / 10
			metric["errorRate"] = float64(int(float64(failed)/float64(runs)*1000)) / 10
		}
		metricsByPipeline[stringValue(row["pipeline_key"])+":"+stringValue(row["environment"])] = metric
		totals["runs"] += runs
		totals["succeeded"] += succeeded
		totals["failed"] += failed
		totals["running"] += numberInt(row["running"])
	}
	metricsByNode := map[string]map[string]interface{}{} // "key:env:nodeId"
	for _, row := range nodeMetrics {
		metricsByNode[stringValue(row["pipeline_key"])+":"+stringValue(row["environment"])+":"+stringValue(row["node_id"])] = map[string]interface{}{
			"runs": numberInt(row["runs"]), "failed": numberInt(row["failed"]),
			"records": row["records"], "lastAt": row["last_at"],
		}
	}

	// Pipeline records = records leaving through its sink edges; assets
	// accumulate from every sink edge writing into them.
	pipelineRecords := map[string]int64{}
	assetMetrics := map[string]map[string]interface{}{}
	for _, edge := range edges {
		pipelineRow := stringValue(edge["pipelineRowId"])
		key := rowKey[pipelineRow]
		nodeMetric := metricsByNode[key+":"+stringValue(edge["nodeId"])]
		if nodeMetric == nil {
			continue
		}
		edge["metrics"] = nodeMetric
		target := stringValue(edge["target"])
		if !strings.HasPrefix(target, "asset:") {
			continue // asset -> pipeline (source read) edge
		}
		records := int64(numberInt(nodeMetric["records"]))
		pipelineRecords[key] += records
		metric := assetMetrics[target]
		if metric == nil {
			metric = map[string]interface{}{"records": int64(0), "lastAt": nodeMetric["lastAt"], "failed": 0, "runs": 0}
			assetMetrics[target] = metric
		}
		metric["records"] = metric["records"].(int64) + records
		metric["runs"] = metric["runs"].(int) + numberInt(nodeMetric["runs"])
		metric["failed"] = metric["failed"].(int) + numberInt(nodeMetric["failed"])
		if timeValue(nodeMetric["lastAt"]).After(timeValue(metric["lastAt"])) {
			metric["lastAt"] = nodeMetric["lastAt"]
		}
	}

	activePipelines := 0
	for _, node := range nodes {
		switch node["kind"] {
		case "pipeline":
			pipeline, _ := node["pipeline"].(map[string]interface{})
			key := rowKey[stringValue(pipeline["rowId"])]
			metric := metricsByPipeline[key]
			if metric == nil {
				metric = map[string]interface{}{"runs": 0, "succeeded": 0, "failed": 0, "running": 0, "cancelled": 0}
			}
			metric["records"] = pipelineRecords[key]
			node["metrics"] = metric
			if numberInt(metric["runs"]) > 0 {
				activePipelines++
			}
		case "asset":
			if metric := assetMetrics[stringValue(node["id"])]; metric != nil {
				node["metrics"] = metric
			}
		}
	}

	stats, _ := graph["stats"].(map[string]int)
	return map[string]interface{}{
		"from": filters.from.Format(time.RFC3339), "to": filters.to.Format(time.RFC3339),
		"nodes": nodes, "edges": edges,
		"stats": map[string]interface{}{
			"pipelines": stats["pipelines"], "assets": stats["assets"], "activePipelines": activePipelines,
			"runs": totals["runs"], "succeeded": totals["succeeded"], "failed": totals["failed"], "running": totals["running"],
		},
	}
}

func (s *Server) runtimeLineageRuns(w http.ResponseWriter, r *http.Request) error {
	filters, err := s.parseRuntimeFilters(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit < 1 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}

	args := []interface{}{}
	where := []string{filters.executionWhere(&args)}
	if query := strings.TrimSpace(q.Get("query")); query != "" {
		args = append(args, query)
		where = append(where, fmt.Sprintf("(e.id=$%d OR e.run_id=$%d OR e.trace_id=$%d)", len(args), len(args), len(args)))
	}
	if cursor := q.Get("cursor"); cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return badRequest(ErrInvalidRequest, "invalid runs cursor")
		}
		var value struct{ StartedAt, ID string }
		if json.Unmarshal(decoded, &value) != nil || value.StartedAt == "" || value.ID == "" {
			return badRequest(ErrInvalidRequest, "invalid runs cursor")
		}
		args = append(args, value.StartedAt, value.ID)
		where = append(where, fmt.Sprintf("(e.started_at,e.id)<($%d::timestamptz,$%d)", len(args)-1, len(args)))
	}

	sql := fmt.Sprintf(`SELECT e.id,e.pipeline_id,p.pipeline_key,p.name,e.environment,e.phase,e.trigger_type,
		e.started_at,e.completed_at,e.run_id,e.trace_id,e.retry_of,
		round(extract(epoch FROM e.completed_at-e.started_at)*1000)::bigint AS duration_ms,
		nr.records,nr.failed_nodes
		FROM executions e JOIN pipelines p ON p.id=e.pipeline_id
		LEFT JOIN LATERAL (SELECT coalesce(sum(record_count),0)::bigint AS records,count(*) FILTER (WHERE status='failed')::int AS failed_nodes FROM node_runs WHERE execution_id=e.id) nr ON true
		WHERE %s ORDER BY e.started_at DESC,e.id DESC LIMIT %d`, strings.Join(where, " AND "), limit+1)

	tenant := tenantFrom(r)
	rows, err := tenantQueryRows(r.Context(), s.DB, tenant.TenantID, sql, args...)
	if err != nil {
		return err
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	var next interface{}
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		encoded, _ := json.Marshal(map[string]string{"startedAt": timeValue(last["started_at"]).UTC().Format(time.RFC3339Nano), "id": stringValue(last["id"])})
		next = base64.RawURLEncoding.EncodeToString(encoded)
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"from": filters.from.Format(time.RFC3339), "to": filters.to.Format(time.RFC3339),
		"items": rows, "nextCursor": next,
	})
	return nil
}

func (s *Server) runtimeLineageRunDetail(w http.ResponseWriter, r *http.Request) error {
	detail, err := s.executionDetail(r, r.PathValue("executionId"))
	if err != nil {
		return err
	}
	if detail == nil {
		return notFound(ErrNotFound, "not found")
	}
	execution, _ := detail["execution"].(map[string]interface{})
	if timeValue(execution["started_at"]).Before(time.Now().UTC().Add(-maxRuntimeWindow)) {
		return notFound(ErrNotFound, "execution is outside the 7-day runtime lineage window")
	}
	jsonResponse(w, http.StatusOK, detail)
	return nil
}
