package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dataflow-poc/workflow-go/internal/model"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"
)

var sensitiveText = regexp.MustCompile(`(?i)(password|secret|token|authorization|api[-_]?key)(\s*[=:]\s*)[^\s,;}]+`)

func redact(value string) string { return sensitiveText.ReplaceAllString(value, "$1$2[REDACTED]") }

func (s *Server) registerExecutions(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/executions", handle(s.executionList))
	mux.HandleFunc("GET /api/executions/monitoring/overview", handle(s.monitoringOverview))
	mux.HandleFunc("GET /api/executions/logs", handle(s.executionLogs))
	mux.Handle("GET /api/executions/{id}/trace", s.requireFeature("deepObservability", handle(s.executionTrace)))
	mux.HandleFunc("GET /api/executions/{id}/status", handle(s.executionStatus))
	mux.HandleFunc("POST /api/executions/{id}/retry", handle(s.executionRetry))
	mux.HandleFunc("POST /api/executions/{id}/{action}", handle(s.executionSignal))
	mux.HandleFunc("GET /api/executions/{id}", handle(s.executionGet))
}

func (s *Server) executionList(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	where := []string{}
	args := []interface{}{}
	add := func(column, value string) {
		if value != "" {
			args = append(args, value)
			where = append(where, fmt.Sprintf("%s=$%d", column, len(args)))
		}
	}
	add("e.pipeline_id", q.Get("pipeline"))
	add("e.environment", q.Get("env"))
	phase := q.Get("phase")
	if phase == "" {
		phase = q.Get("status")
	}
	add("e.phase", phase)
	if q.Get("from") != "" {
		args = append(args, q.Get("from"))
		where = append(where, fmt.Sprintf("e.started_at >= $%d", len(args)))
	}
	if q.Get("to") != "" {
		args = append(args, q.Get("to"))
		where = append(where, fmt.Sprintf("e.started_at <= $%d", len(args)))
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit < 1 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	if cursor := q.Get("cursor"); cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return badRequest(ErrInvalidRequest, "invalid execution cursor")
		}
		var value struct{ StartedAt, ID string }
		if json.Unmarshal(decoded, &value) != nil || value.StartedAt == "" || value.ID == "" {
			return badRequest(ErrInvalidRequest, "invalid execution cursor")
		}
		args = append(args, value.StartedAt, value.ID)
		where = append(where, fmt.Sprintf("(e.started_at,e.id)<($%d::timestamptz,$%d)", len(args)-1, len(args)))
	}
	query := `SELECT e.*,p.name FROM executions e JOIN pipelines p ON p.id=e.pipeline_id`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += ` ORDER BY e.started_at DESC,e.id DESC LIMIT ` + strconv.Itoa(limit)
	if q.Get("paged") == "1" {
		query = `SELECT * FROM (` + query + `) page LIMIT ` + strconv.Itoa(limit+1)
	}
	tenant := tenantFrom(r)
	rows, err := tenantQueryRows(r.Context(), s.DB, tenant.TenantID, query, args...)
	if err != nil {
		return err
	}
	if q.Get("paged") != "1" {
		jsonResponse(w, http.StatusOK, rows)
		return nil
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	var next interface{}
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		started := timeValue(last["started_at"]).UTC().Format(time.RFC3339Nano)
		b, _ := json.Marshal(map[string]string{"startedAt": started, "id": stringValue(last["id"])})
		next = base64.RawURLEncoding.EncodeToString(b)
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"items": rows, "nextCursor": next})
	return nil
}

func (s *Server) monitoringOverview(w http.ResponseWriter, r *http.Request) error {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days < 1 {
		days = 7
	}
	if days > 90 {
		days = 90
	}
	tenant := tenantFrom(r)
	var result = map[string]interface{}{}
	err := s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		queries := []struct{ name, sql string }{
			{"summary", `SELECT count(*)::int AS runs,count(*) FILTER(WHERE phase='completed')::int AS succeeded,count(*) FILTER(WHERE phase='failed')::int AS failed,count(*) FILTER(WHERE phase='running')::int AS running,coalesce(round(avg(extract(epoch FROM(completed_at-started_at))*1000) FILTER(WHERE completed_at IS NOT NULL)),0)::bigint AS avg_duration_ms FROM executions WHERE started_at>=now()-make_interval(days=>$1)`},
			{"trend", `WITH dates AS(SELECT generate_series(current_date-($1::int-1),current_date,interval '1 day')::date AS day) SELECT dates.day,count(e.id)::int runs,count(e.id) FILTER(WHERE e.phase='completed')::int succeeded,count(e.id) FILTER(WHERE e.phase='failed')::int failed FROM dates LEFT JOIN executions e ON e.started_at::date=dates.day GROUP BY dates.day ORDER BY dates.day`},
			{"pipelines", `SELECT p.id,p.pipeline_key,p.name,p.version,p.status,p.environment,p.definition,p.definition->'metadata' AS metadata,
                count(e.id)::int runs,count(e.id) FILTER(WHERE e.phase='failed')::int failed,
                coalesce(round(avg(extract(epoch FROM(e.completed_at-e.started_at))*1000) FILTER(WHERE e.completed_at IS NOT NULL)),0)::bigint AS avg_duration_ms,
                latest.id AS last_execution_id,latest.phase AS last_phase,latest.started_at AS last_started_at
                FROM pipelines p
                LEFT JOIN executions e ON e.pipeline_id=p.id AND e.started_at>=now()-make_interval(days=>$1)
                LEFT JOIN LATERAL(SELECT id,phase,started_at FROM executions WHERE pipeline_id=p.id ORDER BY started_at DESC,id DESC LIMIT 1) latest ON true
                GROUP BY p.id,latest.id,latest.phase,latest.started_at ORDER BY p.environment,p.name`},
			{"recentFailures", `SELECT e.id,e.pipeline_id,p.name,e.environment,e.started_at,e.completed_at,nr.node_id,nr.error FROM executions e JOIN pipelines p ON p.id=e.pipeline_id LEFT JOIN LATERAL(SELECT node_id,error FROM node_runs WHERE execution_id=e.id AND status='failed' ORDER BY finished_at DESC LIMIT 1)nr ON true WHERE e.phase='failed' AND e.started_at>=now()-make_interval(days=>$1) ORDER BY e.started_at DESC LIMIT 10`},
			{"quality", `SELECT count(*)::int checks,coalesce(sum(passed_count),0)::bigint passed_rows,coalesce(sum(failed_count),0)::bigint failed_rows,count(*) FILTER(WHERE status IN('warning','failed'))::int issues FROM data_quality_results WHERE evaluated_at>=now()-make_interval(days=>$1)`},
			{"recentQualityIssues", `SELECT q.execution_id,q.node_id,q.status,q.passed_count,q.failed_count,q.error_samples,q.evaluated_at,p.name,p.environment FROM data_quality_results q JOIN pipelines p ON p.id=q.pipeline_id WHERE q.status IN('warning','failed') AND q.evaluated_at>=now()-make_interval(days=>$1) ORDER BY q.evaluated_at DESC LIMIT 10`}}
		for _, item := range queries {
			rows, err := tx.Query(r.Context(), item.sql, days)
			if err != nil {
				return err
			}
			values, err := rowsToMaps(rows)
			if err != nil {
				return err
			}
			if item.name == "summary" || item.name == "quality" {
				if len(values) > 0 {
					result[item.name] = values[0]
				}
			} else {
				result[item.name] = values
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	summary, _ := result["summary"].(map[string]interface{})
	runs := numberInt(summary["runs"])
	success := numberInt(summary["succeeded"])
	var rate interface{}
	if runs > 0 {
		rate = float64(int(float64(success)/float64(runs)*1000)) / 10
	}
	summary["successRate"] = rate
	summary["avgDurationMs"] = numberInt(summary["avg_duration_ms"])
	quality, _ := result["quality"].(map[string]interface{})
	result["days"] = days
	result["summary"] = summary
	result["quality"] = map[string]int{"checks": numberInt(quality["checks"]), "passedRows": numberInt(quality["passed_rows"]), "failedRows": numberInt(quality["failed_rows"]), "issues": numberInt(quality["issues"])}
	jsonResponse(w, http.StatusOK, result)
	return nil
}

func (s *Server) executionLogs(w http.ResponseWriter, r *http.Request) error {
	q := strings.TrimSpace(r.URL.Query().Get("query"))
	if len(q) > 200 {
		q = q[:200]
	}
	level := r.URL.Query().Get("level")
	if level == "" {
		level = "all"
	}
	if level != "all" && level != "info" && level != "error" {
		return badRequest(ErrInvalidRequest, "level must be all, info, or error")
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 {
		limit = 100
	}
	if limit > 200 {
		limit = 200
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days < 1 {
		days = 7
	}
	if days > 90 {
		days = 90
	}
	where := []string{`nr.finished_at>=now()-make_interval(days=>$1)`}
	args := []interface{}{days}
	if level != "all" {
		status := "success"
		if level == "error" {
			status = "failed"
		}
		args = append(args, status)
		where = append(where, fmt.Sprintf("nr.status=$%d", len(args)))
	}
	if q != "" {
		args = append(args, "%"+q+"%")
		where = append(where, fmt.Sprintf("(p.name ILIKE $%d OR nr.execution_id ILIKE $%d OR nr.node_id ILIKE $%d OR coalesce(nr.error,'') ILIKE $%d)", len(args), len(args), len(args), len(args)))
	}
	args = append(args, limit)
	sql := fmt.Sprintf(`SELECT nr.execution_id,nr.node_id,nr.status,nr.duration_ms,nr.record_count,nr.error,nr.finished_at,p.id AS pipeline_id,p.name,e.environment FROM node_runs nr JOIN executions e ON e.id=nr.execution_id JOIN pipelines p ON p.id=e.pipeline_id WHERE %s ORDER BY nr.finished_at DESC,nr.execution_id DESC,nr.node_id DESC LIMIT $%d`, strings.Join(where, " AND "), len(args))
	tenant := tenantFrom(r)
	rows, err := tenantQueryRows(r.Context(), s.DB, tenant.TenantID, sql, args...)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row["status"] == "failed" {
			row["level"] = "error"
			row["message"] = redact(stringValue(row["error"]))
		} else {
			row["level"] = "info"
			row["message"] = fmt.Sprintf("%d records in %dms", numberInt(row["record_count"]), numberInt(row["duration_ms"]))
		}
		delete(row, "error")
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"items": rows})
	return nil
}

func (s *Server) executionGet(w http.ResponseWriter, r *http.Request) error {
	response, err := s.executionDetail(r, r.PathValue("id"))
	if err != nil {
		return err
	}
	if response == nil {
		return notFound(ErrNotFound, "not found")
	}
	jsonResponse(w, http.StatusOK, response)
	return nil
}

// executionDetail loads one execution with its definition graph, node runs,
// and quality results. Returns nil when the id is unknown to this tenant.
func (s *Server) executionDetail(r *http.Request, id string) (map[string]interface{}, error) {
	tenant := tenantFrom(r)
	var response map[string]interface{}
	err := s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT e.*,p.name,p.definition FROM executions e JOIN pipelines p ON p.id=e.pipeline_id WHERE e.id=$1`, id)
		if err != nil {
			return err
		}
		execution, err := oneMap(rows)
		if err != nil || execution == nil {
			return err
		}
		definition, _ := execution["definition"].(map[string]interface{})
		delete(execution, "definition")
		nodeRows, err := tx.Query(r.Context(), `SELECT node_id,status,duration_ms,record_count,error,started_at,finished_at,attempt FROM node_runs WHERE execution_id=$1`, id)
		if err != nil {
			return err
		}
		nodeRuns, err := rowsToMaps(nodeRows)
		if err != nil {
			return err
		}
		for _, run := range nodeRuns {
			if run["error"] != nil {
				run["error"] = redact(stringValue(run["error"]))
			}
		}
		qualityRows, err := tx.Query(r.Context(), `SELECT node_id,status,passed_count,failed_count,error_samples,evaluated_at,(quarantine_ref IS NOT NULL) AS quarantine_available FROM data_quality_results WHERE execution_id=$1 ORDER BY evaluated_at`, id)
		if err != nil {
			return err
		}
		quality, err := rowsToMaps(qualityRows)
		if err != nil {
			return err
		}
		response = map[string]interface{}{"execution": execution, "definition": map[string]interface{}{"nodes": definition["nodes"], "edges": definition["edges"]}, "nodeRuns": nodeRuns, "qualityResults": quality}
		return nil
	})
	return response, err
}

func (s *Server) executionIdentity(r *http.Request) (environment, workflowID, runID string, phase interface{}, nodeRuns []map[string]interface{}, err error) {
	tenant := tenantFrom(r)
	err = s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		err := tx.QueryRow(r.Context(), `SELECT phase,environment,workflow_id,run_id FROM executions WHERE id=$1`, r.PathValue("id")).Scan(&phase, &environment, &workflowID, &runID)
		if err != nil {
			return err
		}
		rows, err := tx.Query(r.Context(), `SELECT * FROM node_runs WHERE execution_id=$1`, r.PathValue("id"))
		if err != nil {
			return err
		}
		nodeRuns, err = rowsToMaps(rows)
		return err
	})
	return
}

func (s *Server) executionStatus(w http.ResponseWriter, r *http.Request) error {
	environment, workflowID, runID, phase, nodeRuns, err := s.executionIdentity(r)
	if err != nil {
		return notFound(ErrNotFound, "not found")
	}
	phaseString := stringValue(phase)
	if map[string]bool{"completed": true, "failed": true, "cancelled": true}[phaseString] {
		jsonResponse(w, http.StatusOK, map[string]interface{}{"executionId": r.PathValue("id"), "phase": phaseString, "nodeRuns": nodeRuns})
		return nil
	}
	query, err := s.Temporal[environment].QueryWorkflow(r.Context(), workflowID, runID, "status")
	if err != nil {
		jsonResponse(w, http.StatusOK, map[string]interface{}{"executionId": r.PathValue("id"), "phase": phaseString, "nodeRuns": nodeRuns})
		return nil
	}
	var status interface{}
	if err = query.Get(&status); err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, status)
	return nil
}

func (s *Server) executionRetry(w http.ResponseWriter, r *http.Request) error {
	tenant := tenantFrom(r)
	rows, err := tenantQueryRows(r.Context(), s.DB, tenant.TenantID, `SELECT e.id,e.phase,e.pipeline_id,e.environment,e.trigger_type,p.definition,(SELECT id FROM executions retry WHERE retry.retry_of=e.id AND retry.phase='running' LIMIT 1) AS active_retry_id FROM executions e JOIN pipelines p ON p.id=e.pipeline_id WHERE e.id=$1`, r.PathValue("id"))
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return notFound(ErrNotFound, "not found")
	}
	previous := rows[0]
	if previous["trigger_type"] == "backfill" {
		return &HTTPError{Status: http.StatusConflict, Message: "backfill partitions cannot be retried; start a new backfill for the failed range"}
	}
	if previous["phase"] != "failed" {
		return &HTTPError{Status: http.StatusConflict, Message: "only failed executions can be retried"}
	}
	if previous["active_retry_id"] != nil {
		jsonResponse(w, http.StatusConflict, map[string]interface{}{"error": "a retry is already running", "executionId": previous["active_retry_id"]})
		return nil
	}
	body, _ := json.Marshal(previous["definition"])
	var def model.PipelineDefinition
	if err = json.Unmarshal(body, &def); err != nil {
		return err
	}
	id, err := s.fireExecution(r.Context(), def, stringValue(previous["pipeline_id"]), "retry", model.Environment(stringValue(previous["environment"])), nil, "", stringValue(previous["id"]), "")
	if err != nil {
		return err
	}
	s.audit(r.Context(), tenant, "execution.retried", id, map[string]interface{}{"retryOf": previous["id"], "environment": previous["environment"]}, r)
	jsonResponse(w, http.StatusCreated, map[string]interface{}{"executionId": id, "retryOf": previous["id"], "environment": previous["environment"]})
	return nil
}

func (s *Server) executionTrace(w http.ResponseWriter, r *http.Request) error {
	environment, workflowID, runID, _, _, err := s.executionIdentity(r)
	if err != nil {
		return notFound(ErrNotFound, "not found")
	}
	iterator := s.Temporal[environment].GetWorkflowHistory(r.Context(), workflowID, runID, false, 0)
	events := []map[string]interface{}{}
	for iterator.HasNext() {
		event, err := iterator.Next()
		if err != nil {
			return err
		}
		body, _ := protojson.MarshalOptions{UseProtoNames: false}.Marshal(event)
		var value map[string]interface{}
		_ = json.Unmarshal(body, &value)
		for key := range value {
			if regexp.MustCompile(`(?i)payload|input|result|header|memo|searchAttributes|identity|details`).MatchString(key) {
				delete(value, key)
			}
		}
		events = append(events, map[string]interface{}{"eventId": fmt.Sprint(value["eventId"]), "eventType": value["eventType"], "eventTime": value["eventTime"], "attributes": value})
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"events": events})
	return nil
}

func numberInt(value interface{}) int {
	switch value := value.(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float64:
		return int(value)
	case string:
		n, _ := strconv.Atoi(value)
		return n
	default:
		return 0
	}
}
