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

	"github.com/dataflow-poc/workflow-go/internal/config"
	"github.com/dataflow-poc/workflow-go/internal/model"
	"github.com/jackc/pgx/v5"
)

func (s *Server) registerPipelines(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/pipelines", handle(s.pipelineCreate))
	mux.HandleFunc("GET /api/pipelines", handle(s.pipelineList))
	mux.Handle("POST /api/pipelines/{rowId}/activate", s.pipelineAccess("editor", handle(s.pipelineActivate)))
	mux.Handle("POST /api/pipelines/{rowId}/run", s.pipelineAccess("editor", handle(s.pipelineRun)))
	mux.Handle("POST /api/pipelines/{rowId}/promote", s.pipelineAccess("admin", handle(func(w http.ResponseWriter, r *http.Request) error {
		var body struct {
			Allow bool `json:"allowBreakingContract"`
		}
		if !decodeJSON(w, r, &body) {
			return nil
		}
		return s.pipelinePromote(w, r, body.Allow)
	})))
	mux.Handle("POST /api/pipelines/{rowId}/stage", s.pipelineAccess("admin", handle(s.pipelineStage)))
	mux.Handle("POST /api/pipelines/{rowId}/backfills/plan", s.pipelineAccess("viewer", handle(s.backfillPlanRoute)))
	mux.Handle("POST /api/pipelines/{rowId}/backfills", s.pipelineAccess("admin", handle(s.backfillCreate)))
	mux.Handle("GET /api/pipelines/{rowId}/backfills", s.pipelineAccess("viewer", handle(s.backfillList)))
	mux.Handle("DELETE /api/pipelines/{rowId}/backfills/{jobId}", s.pipelineAccess("admin", handle(s.backfillCancel)))
	mux.Handle("POST /api/pipelines/{rowId}/backfills/{jobId}/retry", s.pipelineAccess("admin", handle(s.backfillRetry)))
	mux.HandleFunc("POST /api/pipelines/lineage/openlineage", handle(s.openLineageIngest))
	mux.Handle("POST /api/pipelines/lineage/openlineage-key", owner(handle(s.openLineageKeyCreate)))
	mux.Handle("DELETE /api/pipelines/lineage/openlineage-key", owner(handle(s.openLineageKeyDelete)))
	mux.HandleFunc("GET /api/pipelines/lineage/changes", handle(s.lineageChanges))
	mux.HandleFunc("GET /api/pipelines/lineage/workspace", handle(s.lineageWorkspace))
	mux.Handle("GET /api/pipelines/{rowId}/access", s.pipelineAccess("viewer", handle(s.pipelineAccessList)))
	mux.Handle("POST /api/pipelines/{rowId}/access", s.pipelineAccess("admin", handle(s.pipelineAccessGrant)))
	mux.Handle("DELETE /api/pipelines/{rowId}/access/{userId}", s.pipelineAccess("admin", handle(s.pipelineAccessDelete)))
	mux.Handle("GET /api/pipelines/{rowId}", s.pipelineAccess("viewer", handle(s.pipelineGet)))
}

func (s *Server) enforcePipelineFeatures(r *http.Request, def model.PipelineDefinition) error {
	enabled, err := s.paidFeatures(r)
	if err != nil {
		return err
	}
	for feature := range pipelineFeatures(def) {
		if !enabled[feature] {
			return &HTTPError{Status: http.StatusPaymentRequired, Message: feature + " is not enabled for this workspace"}
		}
	}
	return nil
}

func (s *Server) pipelineCreate(w http.ResponseWriter, r *http.Request) error {
	var def model.PipelineDefinition
	if !decodeJSON(w, r, &def) {
		return nil
	}
	id, err := pipelineID(def.ID)
	if err != nil {
		return badRequest(ErrInvalidRequest, err.Error())
	}
	tenant := tenantFrom(r)
	def.ID = id
	def.TenantID = tenant.TenantID
	if err := validatePipeline(def); err != nil {
		return badRequest(ErrInvalidRequest, err.Error())
	}
	if err := validatePipelineLimits(def, s.Config); err != nil {
		return badRequest(ErrInvalidRequest, err.Error())
	}
	if err := s.enforcePipelineFeatures(r, def); err != nil {
		return err
	}
	var rowID string
	version := 0
	err = s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(), `SELECT coalesce(MAX(version),0)+1 FROM pipelines WHERE pipeline_key=$1`, id).Scan(&version); err != nil {
			return err
		}
		def.Version = version
		body, _ := json.Marshal(def)
		return tx.QueryRow(r.Context(), `INSERT INTO pipelines (pipeline_key,version,tenant_id,name,definition,status,created_by) VALUES ($1,$2,$3,$4,$5,'draft',$6) RETURNING id`, id, version, tenant.TenantID, def.Name, body, tenant.UserID).Scan(&rowID)
	})
	if err != nil {
		return err
	}
	s.audit(r.Context(), tenant, "pipeline.saved", rowID, map[string]interface{}{"name": def.Name, "version": version}, r)
	jsonResponse(w, http.StatusOK, map[string]interface{}{"rowId": rowID, "pipelineKey": id, "version": version})
	return nil
}

func (s *Server) loadPipeline(r *http.Request, rowID string) (map[string]interface{}, model.PipelineDefinition, error) {
	tenant := tenantFrom(r)
	rows, err := tenantQueryRows(r.Context(), s.DB, tenant.TenantID, `SELECT * FROM pipelines WHERE id=$1`, rowID)
	if err != nil || len(rows) == 0 {
		return nil, model.PipelineDefinition{}, err
	}
	body, _ := json.Marshal(rows[0]["definition"])
	var def model.PipelineDefinition
	err = json.Unmarshal(body, &def)
	return rows[0], def, err
}

func (s *Server) pipelineActivate(w http.ResponseWriter, r *http.Request) error {
	row, def, err := s.loadPipeline(r, r.PathValue("rowId"))
	if err != nil {
		return err
	}
	if row == nil {
		return notFound(ErrNotFound, "not found")
	}
	if err := model.ValidateAgentAdmission(def); err != nil {
		return badRequest(ErrInvalidRequest, err.Error())
	}
	if err := s.enforcePipelineFeatures(r, def); err != nil {
		return err
	}
	env := model.Environment(stringValue(row["environment"]))
	if env == "" {
		env = model.EnvironmentTest
	}
	tenant := tenantFrom(r)
	err = s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `UPDATE pipelines SET status='archived' WHERE pipeline_key=$1 AND environment=$2 AND status='active'`, row["pipeline_key"], env); err != nil {
			return err
		}
		_, err := tx.Exec(r.Context(), `UPDATE pipelines SET status='active' WHERE id=$1`, r.PathValue("rowId"))
		return err
	})
	if err != nil {
		return err
	}
	if err := s.syncSchedule(r.Context(), def, r.PathValue("rowId"), env); err != nil {
		return err
	}
	s.audit(r.Context(), tenant, "pipeline.activated", r.PathValue("rowId"), map[string]interface{}{"trigger": def.Trigger.Type, "environment": env}, r)
	jsonResponse(w, http.StatusOK, map[string]interface{}{"ok": true, "trigger": def.Trigger, "environment": env})
	return nil
}

func (s *Server) pipelineRun(w http.ResponseWriter, r *http.Request) error {
	row, def, err := s.loadPipeline(r, r.PathValue("rowId"))
	if err != nil {
		return err
	}
	if row == nil {
		return notFound(ErrNotFound, "not found")
	}
	if err := model.ValidateAgentAdmission(def); err != nil {
		return badRequest(ErrInvalidRequest, err.Error())
	}
	if err := s.enforcePipelineFeatures(r, def); err != nil {
		return err
	}
	var body struct {
		EncryptedDEK string `json:"encryptedDek"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	env := model.Environment(stringValue(row["environment"]))
	if env == "" {
		env = model.EnvironmentTest
	}
	id, err := s.fireExecution(r.Context(), def, r.PathValue("rowId"), "manual", env, nil, body.EncryptedDEK, "", "")
	if quota, ok := err.(*quotaExceeded); ok {
		jsonResponse(w, http.StatusPaymentRequired, map[string]interface{}{"error": "monthly execution quota exceeded", "used": quota.Used, "limit": quota.Limit})
		return nil
	}
	if err != nil {
		return err
	}
	s.audit(r.Context(), tenantFrom(r), "execution.started", id, map[string]interface{}{"trigger": "manual", "environment": env}, r)
	jsonResponse(w, http.StatusOK, map[string]interface{}{"executionId": id, "environment": env})
	return nil
}

// pipelineList returns a summary projection (no definition column) with
// keyset pagination on (created_at,id) plus optional search/stage/trigger
// filters. Use pipelineGet for the full definition of a single row.
func (s *Server) pipelineList(w http.ResponseWriter, r *http.Request) error {
	tenant := tenantFrom(r)
	q := r.URL.Query()

	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit < 1 {
		limit = 50
	}
	maxLimit := s.Config.MaxPipelineListPageSize
	if maxLimit <= 0 || maxLimit > 200 {
		maxLimit = 200
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	where := []string{}
	args := []interface{}{}
	if search := strings.TrimSpace(q.Get("search")); search != "" {
		args = append(args, search)
		where = append(where, fmt.Sprintf("p.name ILIKE '%%'||$%d||'%%'", len(args)))
	}
	switch q.Get("stage") {
	case "production":
		where = append(where, "p.status='active' AND p.environment='prod'")
	case "testing":
		where = append(where, "p.status='active' AND p.environment='test'")
	case "draft":
		where = append(where, "NOT (p.status='active' AND p.environment IN ('prod','test'))")
	}
	if key := q.Get("key"); key != "" {
		args = append(args, key)
		where = append(where, fmt.Sprintf("p.pipeline_key=$%d", len(args)))
	}
	if trigger := q.Get("trigger"); trigger != "" {
		args = append(args, trigger)
		where = append(where, fmt.Sprintf("p.definition->'trigger'->>'type'=$%d", len(args)))
	}
	if cursor := q.Get("cursor"); cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return badRequest(ErrInvalidRequest, "invalid pipeline cursor")
		}
		var value struct{ CreatedAt, ID string }
		if json.Unmarshal(decoded, &value) != nil || value.CreatedAt == "" || value.ID == "" {
			return badRequest(ErrInvalidRequest, "invalid pipeline cursor")
		}
		args = append(args, value.CreatedAt, value.ID)
		where = append(where, fmt.Sprintf("(p.created_at,p.id)<($%d::timestamptz,$%d)", len(args)-1, len(args)))
	}

	// last_run_phase is the effective phase: a durable pause/cancel request
	// on a live execution is reported before Temporal acknowledges it.
	lastRun := `SELECT CASE WHEN e.phase NOT IN ('completed','failed','cancelled') AND e.control_state='paused' THEN 'paused'
    WHEN e.phase NOT IN ('completed','failed','cancelled') AND e.control_state='cancel_requested' THEN 'cancelling'
    ELSE e.phase END AS phase,e.started_at,e.id,v.version FROM executions e JOIN pipelines v ON v.id=e.pipeline_id`
	from := `pipelines p LEFT JOIN LATERAL (` + lastRun + ` WHERE e.pipeline_id=p.id ORDER BY e.started_at DESC LIMIT 1) lr ON true`
	extra := ""
	if q.Get("view") == "current" {
		// One row per logical pipeline/environment: its latest saved version.
		// Stage reflects any active version; last run spans every version.
		from = `(SELECT c.id,c.pipeline_key,c.version,c.name,c.environment,c.promoted_from_version,c.created_at,c.definition,
    CASE WHEN av.version IS NOT NULL THEN 'active' ELSE c.status END AS status,av.version AS active_version,vc.n AS version_count
    FROM (SELECT DISTINCT ON (pipeline_key,environment) * FROM pipelines ORDER BY pipeline_key,environment,version DESC) c
    LEFT JOIN LATERAL (SELECT version FROM pipelines a WHERE a.pipeline_key=c.pipeline_key AND a.environment=c.environment AND a.status='active' ORDER BY version DESC LIMIT 1) av ON true
    LEFT JOIN LATERAL (SELECT count(*)::int AS n FROM pipelines a WHERE a.pipeline_key=c.pipeline_key AND a.environment=c.environment) vc ON true) p
    LEFT JOIN LATERAL (` + lastRun + ` WHERE v.pipeline_key=p.pipeline_key AND v.environment=p.environment ORDER BY e.started_at DESC LIMIT 1) lr ON true`
		extra = ",p.active_version,p.version_count"
	}
	query := `SELECT p.id,p.pipeline_key,p.version,p.name,p.status,p.environment,p.promoted_from_version,p.created_at,p.definition->'trigger'->>'type' AS trigger_type,lr.phase AS last_run_phase,lr.started_at AS last_run_at,lr.id AS last_run_id,lr.version AS last_run_version` + extra + ` FROM ` + from
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += fmt.Sprintf(" ORDER BY p.created_at DESC,p.id DESC LIMIT %d", limit+1)

	rows, err := tenantQueryRows(r.Context(), s.DB, tenant.TenantID, query, args...)
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
		b, _ := json.Marshal(map[string]string{"createdAt": timeValue(last["created_at"]).UTC().Format(time.RFC3339Nano), "id": stringValue(last["id"])})
		next = base64.RawURLEncoding.EncodeToString(b)
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"rows": rows, "nextCursor": next})
	return nil
}
func (s *Server) pipelineGet(w http.ResponseWriter, r *http.Request) error {
	row, _, err := s.loadPipeline(r, r.PathValue("rowId"))
	if err != nil {
		return err
	}
	if row == nil {
		return notFound(ErrNotFound, "not found")
	}
	jsonResponse(w, http.StatusOK, row)
	return nil
}

// validatePipelineLimits enforces config-driven size/shape ceilings on a
// pipeline definition so an oversized or pathological graph is rejected
// before it ever reaches the database.
func validatePipelineLimits(def model.PipelineDefinition, cfg config.Config) error {
	if len(def.Nodes) > cfg.MaxPipelineNodes {
		return fmt.Errorf("pipeline has %d nodes, exceeding the limit of %d", len(def.Nodes), cfg.MaxPipelineNodes)
	}
	if len(def.Edges) > cfg.MaxPipelineEdges {
		return fmt.Errorf("pipeline has %d edges, exceeding the limit of %d", len(def.Edges), cfg.MaxPipelineEdges)
	}
	fanout := map[string]int{}
	for _, edge := range def.Edges {
		fanout[edge.Source]++
		if fanout[edge.Source] > cfg.MaxPipelineFanout {
			return fmt.Errorf("node %s fans out to more than %d edges", edge.Source, cfg.MaxPipelineFanout)
		}
	}
	for _, node := range def.Nodes {
		body, _ := json.Marshal(node.Config)
		if len(body) > cfg.MaxPipelineConfigBytes {
			return fmt.Errorf("node %s config is %d bytes, exceeding the limit of %d", node.ID, len(body), cfg.MaxPipelineConfigBytes)
		}
		if node.Ingestion != nil && node.Ingestion.PageSize > cfg.MaxPipelineNodePageSize {
			return fmt.Errorf("node %s ingestion pageSize %d exceeds the limit of %d", node.ID, node.Ingestion.PageSize, cfg.MaxPipelineNodePageSize)
		}
	}
	if def.Concurrency != nil && def.Concurrency.MaxParallelNodes > cfg.MaxPipelineMaxParallel {
		return fmt.Errorf("concurrency.maxParallelNodes %d exceeds the limit of %d", def.Concurrency.MaxParallelNodes, cfg.MaxPipelineMaxParallel)
	}
	return nil
}

func deriveStage(status, environment string) string {
	if status == "active" && environment == "prod" {
		return "production"
	}
	if status == "active" && environment == "test" {
		return "testing"
	}
	return "draft"
}

// extractContracts extracts all data contracts from a pipeline definition
func extractContracts(def model.PipelineDefinition) map[string]map[string]interface{} {
	contracts := make(map[string]map[string]interface{})

	// Build adjacency map for traversing edges
	incoming := make(map[string][]string)
	for _, edge := range def.Edges {
		incoming[edge.Target] = append(incoming[edge.Target], edge.Source)
	}

	// Build node lookup map for efficiency
	nodeMap := make(map[string]model.Node)
	for _, node := range def.Nodes {
		nodeMap[node.ID] = node
	}

	// Helper function to traverse upstream and find contracts with cycle detection
	var findContract func(nodeID string, visited map[string]bool) map[string]interface{}
	findContract = func(nodeID string, visited map[string]bool) map[string]interface{} {
		if visited[nodeID] {
			return nil
		}
		visited[nodeID] = true

		for _, sourceID := range incoming[nodeID] {
			if node, exists := nodeMap[sourceID]; exists && node.ActivityType == "transform.contract" {
				if schema, ok := node.Config["schemaJson"].(map[string]interface{}); ok {
					return schema
				}
			}
			if contract := findContract(sourceID, visited); contract != nil {
				return contract
			}
		}
		return nil
	}

	// Find contracts for all sink nodes
	for _, node := range def.Nodes {
		if node.Type == "sink" {
			visited := make(map[string]bool)
			if contract := findContract(node.ID, visited); contract != nil {
				contracts[node.ID] = contract
			}
		}
	}

	return contracts
}

// compareContracts compares contracts between two versions and returns breaking changes
func compareContracts(oldContracts, newContracts map[string]map[string]interface{}) []string {
	var breaks []string

	// Check for removed or modified contracts
	for sinkID, oldContract := range oldContracts {
		newContract, exists := newContracts[sinkID]
		if !exists {
			breaks = append(breaks, fmt.Sprintf("contract removed for sink %s", sinkID))
			continue
		}

		// Check for removed fields
		for field := range oldContract {
			if _, exists := newContract[field]; !exists {
				breaks = append(breaks, fmt.Sprintf("field %s removed from contract for sink %s", field, sinkID))
			}
		}

		// Check for type changes
		for field, oldSpec := range oldContract {
			if newSpec, exists := newContract[field]; exists {
				oldType := fmt.Sprintf("%v", oldSpec)
				newType := fmt.Sprintf("%v", newSpec)
				// Remove optional marker for comparison
				oldType = trimOptional(oldType)
				newType = trimOptional(newType)
				if oldType != newType {
					breaks = append(breaks, fmt.Sprintf("field %s type changed from %s to %s in sink %s", field, oldType, newType, sinkID))
				}
			}
		}
	}

	return breaks
}

func trimOptional(spec string) string {
	if len(spec) > 0 && spec[len(spec)-1] == '?' {
		return spec[:len(spec)-1]
	}
	return spec
}

func (s *Server) createProductionVersion(r *http.Request, row map[string]interface{}, def model.PipelineDefinition, allowBreakingContract bool) (map[string]interface{}, error) {
	if err := model.ValidateAgentAdmission(def); err != nil {
		return nil, badRequest(ErrInvalidRequest, err.Error())
	}
	if stringValue(row["environment"]) != "test" {
		return nil, &HTTPError{Status: http.StatusConflict, Message: "only Integration versions can be promoted"}
	}
	tenant := tenantFrom(r)
	var out = map[string]interface{}{}
	err := s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		var one int
		if err := tx.QueryRow(r.Context(), `SELECT 1 FROM executions WHERE pipeline_id=$1 AND environment='test' AND phase='completed' LIMIT 1`, row["id"]).Scan(&one); err != nil {
			return &HTTPError{Status: http.StatusConflict, Message: "promotion gate: this version has no successful Integration run"}
		}

		// Check for contract breaking changes with current production version
		var currentProdDef model.PipelineDefinition
		var currentProdBody []byte
		err := tx.QueryRow(r.Context(), `SELECT definition FROM pipelines WHERE pipeline_key=$1 AND environment='prod' AND status='active'`, row["pipeline_key"]).Scan(&currentProdBody)
		if err == nil {
			// There's an existing production version, check contracts
			if err := json.Unmarshal(currentProdBody, &currentProdDef); err == nil {
				oldContracts := extractContracts(currentProdDef)
				newContracts := extractContracts(def)
				breaks := compareContracts(oldContracts, newContracts)
				if len(breaks) > 0 && !allowBreakingContract {
					return &HTTPError{
						Status:  http.StatusConflict,
						Message: fmt.Sprintf("breaking data contract: %s", fmt.Sprintf("%v", breaks)),
					}
				}
			}
		}

		var version int
		if err := tx.QueryRow(r.Context(), `SELECT coalesce(MAX(version),0)+1 FROM pipelines WHERE pipeline_key=$1`, row["pipeline_key"]).Scan(&version); err != nil {
			return err
		}
		def.Version = version
		body, _ := json.Marshal(def)
		if _, err := tx.Exec(r.Context(), `UPDATE pipelines SET status='archived' WHERE pipeline_key=$1 AND environment='prod' AND status='active'`, row["pipeline_key"]); err != nil {
			return err
		}
		var id string
		if err := tx.QueryRow(r.Context(), `INSERT INTO pipelines (pipeline_key,version,tenant_id,name,definition,status,environment,promoted_from_version) VALUES ($1,$2,$3,$4,$5,'active','prod',$6) RETURNING id`, row["pipeline_key"], version, tenant.TenantID, row["name"], body, row["version"]).Scan(&id); err != nil {
			return err
		}
		out = map[string]interface{}{"rowId": id, "version": version, "fromVersion": row["version"], "def": def, "env": model.EnvironmentProd, "stage": "production", "contractOverride": allowBreakingContract}
		return nil
	})
	return out, err
}

func (s *Server) pipelinePromote(w http.ResponseWriter, r *http.Request, allowBreakingContract bool) error {
	row, def, err := s.loadPipeline(r, r.PathValue("rowId"))
	if err != nil {
		return err
	}
	if row == nil {
		return notFound(ErrNotFound, "not found")
	}
	if err = s.enforcePipelineFeatures(r, def); err != nil {
		return err
	}
	out, err := s.createProductionVersion(r, row, def, allowBreakingContract)
	if err != nil {
		return err
	}
	id := stringValue(out["rowId"])
	if err = s.syncSchedule(r.Context(), def, id, model.EnvironmentProd); err != nil {
		return err
	}
	s.audit(r.Context(), tenantFrom(r), "pipeline.promoted", id, out, r)
	jsonResponse(w, http.StatusOK, map[string]interface{}{"ok": true, "rowId": id, "environment": "prod", "version": out["version"], "contractOverride": allowBreakingContract})
	return nil
}

func (s *Server) pipelineStage(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		To    string `json:"to"`
		Allow bool   `json:"allowBreakingContract"`
	}
	if !decodeJSON(w, r, &body) {
		return nil
	}
	if body.To != "testing" && body.To != "production" {
		return badRequest(ErrInvalidRequest, `body.to must be "testing" or "production"`)
	}
	row, _, err := s.loadPipeline(r, r.PathValue("rowId"))
	if err != nil {
		return err
	}
	if row == nil {
		return notFound(ErrNotFound, "not found")
	}
	from := deriveStage(stringValue(row["status"]), stringValue(row["environment"]))
	if from == "draft" && body.To == "testing" {
		if err := s.pipelineActivate(noopWriter{}, r); err != nil {
			return err
		}
		jsonResponse(w, http.StatusOK, map[string]interface{}{"ok": true, "stage": "testing", "environment": "test", "rowId": r.PathValue("rowId"), "contractOverride": false})
		return nil
	}
	if from == "testing" && body.To == "production" {
		return s.pipelinePromote(w, r, body.Allow)
	}
	return &HTTPError{Status: http.StatusConflict, Message: fmt.Sprintf("unsupported stage transition %s → %s", from, body.To)}
}

type noopWriter struct{ header http.Header }

func (n noopWriter) Header() http.Header {
	if n.header == nil {
		return http.Header{}
	}
	return n.header
}
func (noopWriter) Write([]byte) (int, error) { return 0, nil }
func (noopWriter) WriteHeader(int)           {}

func (s *Server) backfillPlanRoute(w http.ResponseWriter, r *http.Request) error {
	_, def, err := s.loadPipeline(r, r.PathValue("rowId"))
	if err != nil {
		return err
	}
	if err = validateBackfillSources(def); err != nil {
		return badRequest(ErrInvalidRequest, err.Error())
	}
	body := map[string]interface{}{}
	if !decodeJSON(w, r, &body) {
		return nil
	}
	plan, err := planBackfill(body)
	if err != nil {
		return badRequest(ErrInvalidRequest, err.Error())
	}
	jsonResponse(w, http.StatusOK, plan)
	return nil
}
func (s *Server) backfillCreate(w http.ResponseWriter, r *http.Request) error {
	body := map[string]interface{}{}
	if !decodeJSON(w, r, &body) {
		return nil
	}
	plan, err := planBackfill(body)
	if err != nil {
		return badRequest(ErrInvalidRequest, err.Error())
	}
	if plan.PartitionCount > s.Config.MaxBackfillPartitions {
		return badRequest(ErrInvalidRequest, fmt.Sprintf("backfill has %d partitions, exceeding the limit of %d", plan.PartitionCount, s.Config.MaxBackfillPartitions))
	}
	row, def, err := s.loadPipeline(r, r.PathValue("rowId"))
	if err != nil {
		return err
	}
	if row == nil {
		return notFound(ErrNotFound, "not found")
	}
	if err := model.ValidateAgentAdmission(def); err != nil {
		return badRequest(ErrInvalidRequest, err.Error())
	}
	if err = validateBackfillSources(def); err != nil {
		return badRequest(ErrInvalidRequest, err.Error())
	}
	tenant := tenantFrom(r)
	var jobID, status string
	err = s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(), `INSERT INTO backfill_jobs (tenant_id,pipeline_id,environment,range_start,range_end,partition_days,max_concurrency,created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id,status`, tenant.TenantID, r.PathValue("rowId"), row["environment"], plan.From, plan.To, plan.PartitionDays, plan.MaxConcurrency, tenant.UserID).Scan(&jobID, &status); err != nil {
			return err
		}
		for i, p := range plan.Partitions {
			if _, err := tx.Exec(r.Context(), `INSERT INTO backfill_partitions (job_id,tenant_id,ordinal,range_start,range_end) VALUES ($1,$2,$3,$4,$5)`, jobID, tenant.TenantID, i, p["from"], p["to"]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.audit(r.Context(), tenant, "backfill.started", jobID, map[string]interface{}{"pipelineId": r.PathValue("rowId"), "partitionCount": plan.PartitionCount}, r)
	jsonResponse(w, http.StatusAccepted, map[string]interface{}{"jobId": jobID, "status": status, "partitionCount": plan.PartitionCount})
	return nil
}
func (s *Server) backfillList(w http.ResponseWriter, r *http.Request) error {
	tenant := tenantFrom(r)
	rows, err := tenantQueryRows(r.Context(), s.DB, tenant.TenantID, `SELECT bj.id,bj.status,bj.range_start AS from,bj.range_end AS to,bj.partition_days,bj.max_concurrency,bj.created_at,bj.completed_at,count(bp.*)::int AS partition_count,count(*) FILTER(WHERE bp.status='pending')::int AS pending,count(*) FILTER(WHERE bp.status IN('starting','running'))::int AS running,count(*) FILTER(WHERE bp.status='completed')::int AS completed,count(*) FILTER(WHERE bp.status='failed')::int AS failed FROM backfill_jobs bj JOIN backfill_partitions bp ON bp.job_id=bj.id WHERE bj.pipeline_id=$1 GROUP BY bj.id ORDER BY bj.created_at DESC LIMIT 50`, r.PathValue("rowId"))
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"jobs": rows})
	return nil
}
func (s *Server) backfillCancel(w http.ResponseWriter, r *http.Request) error {
	tenant := tenantFrom(r)
	changed := false
	err := s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		cmd, err := tx.Exec(r.Context(), `UPDATE backfill_jobs SET status='cancelled',completed_at=now() WHERE id=$1 AND pipeline_id=$2 AND status IN('queued','running')`, r.PathValue("jobId"), r.PathValue("rowId"))
		if err != nil {
			return err
		}
		changed = cmd.RowsAffected() > 0
		if changed {
			_, err = tx.Exec(r.Context(), `UPDATE backfill_partitions SET status='cancelled',completed_at=now() WHERE job_id=$1 AND status IN('pending','starting')`, r.PathValue("jobId"))
		}
		return err
	})
	if err != nil {
		return err
	}
	if !changed {
		return notFound(ErrNotFound, "not found or already terminal")
	}
	jsonResponse(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}
func (s *Server) backfillRetry(w http.ResponseWriter, r *http.Request) error {
	tenant := tenantFrom(r)
	count := int64(0)
	err := s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		cmd, err := tx.Exec(r.Context(), `UPDATE backfill_partitions SET status='pending',error=NULL,started_at=NULL,completed_at=NULL WHERE job_id=$1 AND status='failed'`, r.PathValue("jobId"))
		if err != nil {
			return err
		}
		count = cmd.RowsAffected()
		if count > 0 {
			_, err = tx.Exec(r.Context(), `UPDATE backfill_jobs SET status='running',completed_at=NULL WHERE id=$1 AND pipeline_id=$2`, r.PathValue("jobId"), r.PathValue("rowId"))
		}
		return err
	})
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"ok": true, "retried": count})
	return nil
}

func (s *Server) pipelineAccessList(w http.ResponseWriter, r *http.Request) error {
	tenant := tenantFrom(r)
	rows, err := tenantQueryRows(r.Context(), s.DB, tenant.TenantID, `SELECT pa.user_id,pa.role,pa.created_at,u.email FROM pipeline_access pa JOIN users u ON u.id=pa.user_id WHERE pa.pipeline_id=$1 ORDER BY pa.created_at`, r.PathValue("rowId"))
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"grants": rows})
	return nil
}
func (s *Server) pipelineAccessGrant(w http.ResponseWriter, r *http.Request) error {
	var body struct{ UserID, Role string }
	if !decodeJSON(w, r, &body) {
		return nil
	}
	if body.UserID == "" || !map[string]bool{"viewer": true, "editor": true, "admin": true}[body.Role] {
		return badRequest(ErrInvalidRequest, "userId and role (viewer|editor|admin) required")
	}
	tenant := tenantFrom(r)
	err := s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		var one int
		if err := tx.QueryRow(r.Context(), `SELECT 1 FROM users WHERE id=$1 AND tenant_id=$2`, body.UserID, tenant.TenantID).Scan(&one); err != nil {
			return notFound(ErrNotFound, "user not found in tenant")
		}
		_, err := tx.Exec(r.Context(), `INSERT INTO pipeline_access (pipeline_id,user_id,role,granted_by) VALUES ($1,$2,$3,$4) ON CONFLICT(pipeline_id,user_id) DO UPDATE SET role=EXCLUDED.role,granted_by=EXCLUDED.granted_by`, r.PathValue("rowId"), body.UserID, body.Role, tenant.UserID)
		return err
	})
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusCreated, map[string]bool{"ok": true})
	return nil
}
func (s *Server) pipelineAccessDelete(w http.ResponseWriter, r *http.Request) error {
	tenant := tenantFrom(r)
	err := s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(), `DELETE FROM pipeline_access WHERE pipeline_id=$1 AND user_id=$2`, r.PathValue("rowId"), r.PathValue("userId"))
		return err
	})
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}

func (s *Server) openLineageKeyCreate(w http.ResponseWriter, r *http.Request) error {
	token := randomToken()
	tenant := tenantFrom(r)
	err := s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(), `INSERT INTO openlineage_ingest_keys (tenant_id,key_hash,created_by,created_at,revoked_at) VALUES ($1,$2,$3,now(),NULL) ON CONFLICT(tenant_id) DO UPDATE SET key_hash=EXCLUDED.key_hash,created_by=EXCLUDED.created_by,created_at=now(),revoked_at=NULL`, tenant.TenantID, sha256Hex(token), tenant.UserID)
		return err
	})
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusCreated, map[string]string{"token": token})
	return nil
}
func (s *Server) openLineageKeyDelete(w http.ResponseWriter, r *http.Request) error {
	tenant := tenantFrom(r)
	err := s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(), `UPDATE openlineage_ingest_keys SET revoked_at=now() WHERE tenant_id=$1`, tenant.TenantID)
		return err
	})
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}
func (s *Server) openLineageIngest(w http.ResponseWriter, r *http.Request) error {
	environment := r.URL.Query().Get("environment")
	if environment == "" {
		environment = "prod"
	}
	if environment != "test" && environment != "prod" {
		return badRequest(ErrInvalidRequest, "environment must be test or prod")
	}
	var event map[string]interface{}
	if !decodeJSON(w, r, &event) {
		return nil
	}
	job, _ := event["job"].(map[string]interface{})
	run, _ := event["run"].(map[string]interface{})
	inputs, _ := event["inputs"]
	outputs, _ := event["outputs"]
	tenant := tenantFrom(r)
	err := s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(), `INSERT INTO external_lineage_events (tenant_id,environment,event_type,event_time,run_id,job_namespace,job_name,inputs,outputs,payload) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, tenant.TenantID, environment, event["eventType"], event["eventTime"], run["runId"], job["namespace"], job["name"], inputs, outputs, event)
		return err
	})
	if err != nil {
		return badRequest(ErrInvalidRequest, err.Error())
	}
	jsonResponse(w, http.StatusCreated, map[string]bool{"ok": true})
	return nil
}

func (s *Server) lineageChanges(w http.ResponseWriter, r *http.Request) error {
	environment := r.URL.Query().Get("environment")
	if environment != "" && environment != "test" && environment != "prod" {
		return badRequest(ErrInvalidRequest, "environment must be test or prod")
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 {
		limit = 30
	}
	if limit > 100 {
		limit = 100
	}
	tenant := tenantFrom(r)
	rows, err := tenantQueryRows(r.Context(), s.DB, tenant.TenantID, `SELECT id,pipeline_key,version,name,status,environment,definition,created_at FROM pipelines WHERE ($1::text IS NULL OR environment=$1) ORDER BY pipeline_key,environment,version`, nullString(environment))
	if err != nil {
		return err
	}
	items := make([]map[string]interface{}, 0)
	previous := map[string]map[string]interface{}{}
	for _, row := range rows {
		key := stringValue(row["pipeline_key"]) + ":" + stringValue(row["environment"])
		if old := previous[key]; old != nil {
			if stringValue(old["definition"]) != stringValue(row["definition"]) {
				items = append(items, map[string]interface{}{"rowId": row["id"], "pipelineKey": row["pipeline_key"], "name": row["name"], "status": row["status"], "environment": row["environment"], "fromVersion": old["version"], "toVersion": row["version"], "createdAt": row["created_at"], "summary": map[string]int{"breaking": 0, "warning": 0, "info": 1}, "changes": []map[string]string{{"severity": "info", "kind": "definition.changed", "message": "pipeline definition changed"}}})
			}
		}
		previous[key] = row
	}
	if len(items) > limit {
		items = items[len(items)-limit:]
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"items": items})
	return nil
}

func (s *Server) lineageWorkspace(w http.ResponseWriter, r *http.Request) error {
	environment := r.URL.Query().Get("environment")
	if environment != "" && environment != "test" && environment != "prod" {
		return badRequest(ErrInvalidRequest, "environment must be test or prod")
	}
	tenant := tenantFrom(r)
	rows, err := tenantQueryRows(r.Context(), s.DB, tenant.TenantID, `WITH ranked AS (SELECT id,pipeline_key,version,name,status,environment,definition,row_number() OVER(PARTITION BY pipeline_key,environment ORDER BY CASE status WHEN 'active' THEN 0 WHEN 'draft' THEN 1 ELSE 2 END,version DESC) rank FROM pipelines WHERE ($1::text IS NULL OR environment=$1)) SELECT * FROM ranked WHERE rank=1 ORDER BY environment,name`, nullString(environment))
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, buildWorkspaceLineage(rows))
	return nil
}

type fieldOrigin struct {
	assetURN        string
	field           string
	fieldType       string
	nullable        bool
	transformNodeID string
}

type lineageField struct {
	fieldType string
	nullable  bool
}

var mapFieldPattern = regexp.MustCompile(`(\w+)\s*:\s*r\.(\w+)`)

func assetURN(cfg map[string]interface{}) (string, string, string, string, bool) {
	bucket, key, layer := stringValue(cfg["bucket"]), stringValue(cfg["key"]), stringValue(cfg["layer"])
	if bucket == "" || key == "" || layer == "" {
		return "", "", "", "", false
	}
	return fmt.Sprintf("s3://%s/%s", bucket, key), bucket, key, layer, true
}

func lineageContractSchema(value interface{}) map[string]interface{} {
	switch value := value.(type) {
	case map[string]interface{}:
		return value
	case string:
		var schema map[string]interface{}
		if err := json.Unmarshal([]byte(value), &schema); err == nil {
			return schema
		}
	}
	return nil
}

// buildWorkspaceLineage walks each pipeline's node chain (assumed linear —
// source → transform* → sink, which is all the DAG shapes this feature
// currently needs to support) to derive asset nodes, pipeline↔asset edges,
// and field-level (column) lineage through contract/map transforms.
func buildWorkspaceLineage(rows []map[string]interface{}) map[string]interface{} {
	nodes := []map[string]interface{}{}
	edges := []map[string]interface{}{}
	columnEdges := []map[string]interface{}{}
	assetSchemas := map[string]map[string]lineageField{}
	assetTouchedBy := map[string]map[string]bool{}
	assetOrder := []string{}

	ensureAsset := func(urn, bucket, key, layer string) string {
		id := "asset:" + urn
		if _, ok := assetSchemas[id]; !ok {
			assetSchemas[id] = map[string]lineageField{}
			assetTouchedBy[id] = map[string]bool{}
			assetOrder = append(assetOrder, id)
			nodes = append(nodes, map[string]interface{}{"id": id, "kind": "asset", "asset": map[string]interface{}{
				"urn": urn, "platform": "s3", "namespace": bucket, "name": key, "type": "file", "layer": layer,
			}})
		}
		return id
	}

	for _, row := range rows {
		rowID := stringValue(row["id"])
		pipelineNodeID := "pipeline:" + rowID
		raw, _ := json.Marshal(row["definition"])
		var def model.PipelineDefinition
		definitionErr := json.Unmarshal(raw, &def)
		pipeline := map[string]interface{}{"rowId": row["id"], "pipelineKey": row["pipeline_key"], "name": row["name"], "version": row["version"], "status": row["status"], "environment": row["environment"]}
		if definitionErr == nil {
			if def.Metadata != nil {
				pipeline["metadata"] = def.Metadata
			}
			if def.SLO != nil {
				pipeline["slo"] = def.SLO
			}
		}
		nodes = append(nodes, map[string]interface{}{"id": pipelineNodeID, "kind": "pipeline", "pipeline": pipeline})
		if definitionErr != nil || len(def.Nodes) == 0 {
			continue
		}
		byID := map[string]model.Node{}
		indegree := map[string]int{}
		outgoing := map[string]string{}
		for _, node := range def.Nodes {
			byID[node.ID] = node
			indegree[node.ID] = 0
		}
		for _, edge := range def.Edges {
			indegree[edge.Target]++
			outgoing[edge.Source] = edge.Target
		}
		var current string
		for _, node := range def.Nodes {
			if indegree[node.ID] == 0 {
				current = node.ID
				break
			}
		}
		schema := map[string]fieldOrigin{}
		var upstreamAssetURN string
		for current != "" {
			node := byID[current]
			if node.Type == "source" {
				if urn, bucket, key, layer, ok := assetURN(node.Config); ok {
					assetID := ensureAsset(urn, bucket, key, layer)
					assetTouchedBy[assetID][rowID] = true
					edges = append(edges, map[string]interface{}{
						"id": fmt.Sprintf("edge:%s:%s:source", rowID, node.ID), "source": assetID, "target": pipelineNodeID,
						"pipelineRowId": rowID, "nodeId": node.ID,
					})
					upstreamAssetURN = urn
				}
			}
			if node.ActivityType == "transform.contract" {
				if schemaJSON := lineageContractSchema(node.Config["schemaJson"]); schemaJSON != nil {
					for field, rawType := range schemaJSON {
						fieldType := stringValue(rawType)
						nullable := strings.HasSuffix(fieldType, "?")
						fieldType = strings.TrimSuffix(fieldType, "?")
						if fieldType == "" {
							fieldType = "unknown"
						}
						schema[field] = fieldOrigin{assetURN: upstreamAssetURN, field: field, fieldType: fieldType, nullable: nullable, transformNodeID: node.ID}
					}
				}
			}
			if node.ActivityType == "transform.map" {
				if expression, ok := node.Config["expression"].(string); ok {
					next := map[string]fieldOrigin{}
					for _, match := range mapFieldPattern.FindAllStringSubmatch(expression, -1) {
						newField, oldField := match[1], match[2]
						if origin, known := schema[oldField]; known {
							origin.transformNodeID = node.ID
							next[newField] = origin
						}
					}
					if len(next) > 0 {
						schema = next
					}
				}
			}
			if node.Type == "sink" {
				if urn, bucket, key, layer, ok := assetURN(node.Config); ok {
					assetID := ensureAsset(urn, bucket, key, layer)
					assetTouchedBy[assetID][rowID] = true
					edges = append(edges, map[string]interface{}{
						"id": fmt.Sprintf("edge:%s:%s:sink", rowID, node.ID), "source": pipelineNodeID, "target": assetID,
						"pipelineRowId": rowID, "nodeId": node.ID,
					})
					for renamed, origin := range schema {
						assetSchemas[assetID][renamed] = lineageField{fieldType: origin.fieldType, nullable: origin.nullable}
						if origin.assetURN != "" {
							columnEdge := map[string]interface{}{
								"id": fmt.Sprintf("column:%s:%s:%s:%s", rowID, node.ID, origin.field, renamed), "pipelineRowId": rowID,
								"sourceAssetUrn": origin.assetURN, "sourceField": origin.field, "targetAssetUrn": urn, "targetField": renamed,
							}
							if origin.transformNodeID != "" {
								columnEdge["transformNodeId"] = origin.transformNodeID
							}
							columnEdges = append(columnEdges, columnEdge)
						}
					}
				}
			}
			current = outgoing[current]
		}
	}

	for _, node := range nodes {
		if node["kind"] != "asset" {
			continue
		}
		assetID := node["id"].(string)
		fields := []map[string]interface{}{}
		for name, field := range assetSchemas[assetID] {
			item := map[string]interface{}{"name": name, "type": field.fieldType}
			if field.nullable {
				item["nullable"] = true
			}
			fields = append(fields, item)
		}
		if len(fields) > 0 {
			node["asset"].(map[string]interface{})["schema"] = map[string]interface{}{"fields": fields}
		}
	}
	sharedAssets := 0
	for _, id := range assetOrder {
		if len(assetTouchedBy[id]) >= 2 {
			sharedAssets++
		}
	}
	return map[string]interface{}{
		"nodes": nodes, "edges": edges, "columnEdges": columnEdges,
		"stats": map[string]int{"pipelines": len(rows), "assets": len(assetOrder), "links": len(edges), "sharedAssets": sharedAssets, "columnLinks": len(columnEdges), "externalJobs": 0},
	}
}
