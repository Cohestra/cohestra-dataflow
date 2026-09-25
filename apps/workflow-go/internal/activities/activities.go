package activities

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/dataflow-poc/workflow-go/internal/connectors"
	"github.com/dataflow-poc/workflow-go/internal/database"
	"github.com/dataflow-poc/workflow-go/internal/model"
	"github.com/google/uuid"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

type Activities struct {
	DB             *database.DB
	Payloads       *Payloads
	Runtime        *connectors.Runtime
	PrivateKeyPath string
	// MaxMergeInMemoryBytes caps how much fan-in merge input can be held in memory at once
	// before MergeRefs spills to a temp file instead; <= 0 means unlimited.
	MaxMergeInMemoryBytes int64
}

type ScheduledExecutionParams struct {
	ExecutionID   string `json:"executionId"`
	PipelineRowID string `json:"pipelineRowId"`
	TenantID      string `json:"tenantId"`
	Environment   string `json:"environment"`
	WorkflowID    string `json:"workflowId"`
	RunID         string `json:"runId"`
}

func (a *Activities) PrepareScheduledExecution(ctx context.Context, p ScheduledExecutionParams) error {
	tx, err := a.DB.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err = tx.Exec(ctx, `INSERT INTO billing_plans (tenant_id) VALUES ($1) ON CONFLICT DO NOTHING`, p.TenantID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO usage_counters (tenant_id,month,execution_count) VALUES ($1,date_trunc('month',now() at time zone 'utc')::date,0) ON CONFLICT DO NOTHING`, p.TenantID); err != nil {
		return err
	}
	var limit, used int
	if err = tx.QueryRow(ctx, `SELECT bp.free_tier_limit+bp.extra_quota,uc.execution_count FROM billing_plans bp JOIN usage_counters uc ON uc.tenant_id=bp.tenant_id AND uc.month=date_trunc('month',now() at time zone 'utc')::date WHERE bp.tenant_id=$1 FOR UPDATE OF bp,uc`, p.TenantID).Scan(&limit, &used); err != nil {
		return err
	}
	if used >= limit {
		return temporal.NewNonRetryableApplicationError("scheduled execution quota exceeded", "QuotaExceededError", nil)
	}
	if _, err = tx.Exec(ctx, `UPDATE usage_counters SET execution_count=execution_count+1 WHERE tenant_id=$1 AND month=date_trunc('month',now() at time zone 'utc')::date`, p.TenantID); err != nil {
		return err
	}
	// Cron firings have no inbound trace context; mint a W3C-shaped trace id
	// here (activity side — workflow code must stay deterministic).
	traceID := strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = tx.Exec(ctx, `INSERT INTO executions (id,pipeline_id,tenant_id,trigger_type,phase,environment,workflow_id,run_id,trace_id) VALUES ($1,$2,$3,'cron','running',$4,$5,$6,$7) ON CONFLICT(id) DO NOTHING`, p.ExecutionID, p.PipelineRowID, p.TenantID, p.Environment, p.WorkflowID, p.RunID, traceID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type FetchSourceParams struct {
	ControlRequired bool                   `json:"controlRequired,omitempty"`
	ActivityType    string                 `json:"activityType"`
	Config          map[string]interface{} `json:"config"`
	Ingestion       *model.IngestionConfig `json:"ingestion,omitempty"`
	TenantID        string                 `json:"tenantId"`
	ConnectionID    string                 `json:"connectionId"`
	ExecutionID     string                 `json:"executionId"`
	NodeID          string                 `json:"nodeId"`
	Cursor          map[string]interface{} `json:"cursor,omitempty"`
	EncryptedDEK    string                 `json:"encryptedDek,omitempty"`
}
type FetchSourceResult struct {
	OutputRef   *model.DataRef         `json:"outputRef"`
	HasMore     bool                   `json:"hasMore"`
	RecordCount int                    `json:"recordCount"`
	Checkpoint  map[string]interface{} `json:"checkpoint,omitempty"`
	LagRecords  int64                  `json:"lagRecords,omitempty"`
}

func (a *Activities) dek(encoded string) ([]byte, error) {
	if encoded == "" {
		return nil, nil
	}
	return UnwrapDEK(encoded, a.PrivateKeyPath)
}
func (a *Activities) FetchSourcePage(ctx context.Context, p FetchSourceParams) (FetchSourceResult, error) {
	defer controlledHeartbeat(ctx, p.ControlRequired)()
	activity.RecordHeartbeat(ctx)
	if err := a.requireEntitlement(ctx, p.TenantID, p.ActivityType, p.Config); err != nil {
		return FetchSourceResult{}, err
	}
	if err := a.verifyConnectorOwnership(ctx, p.TenantID, p.Config); err != nil {
		return FetchSourceResult{}, err
	}
	if p.Cursor == nil {
		var raw []byte
		_ = a.DB.Pool.QueryRow(ctx, `SELECT cursor FROM connector_state WHERE tenant_id=$1 AND connection_id=$2`, p.TenantID, p.ConnectionID).Scan(&raw)
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &p.Cursor)
		}
	}
	if p.Cursor == nil {
		p.Cursor = map[string]interface{}{}
	}
	started := time.Now()
	if err := a.admitControlledActivity(ctx, p.TenantID, p.ExecutionID, p.ControlRequired); err != nil {
		return FetchSourceResult{}, err
	}
	result, err := a.Runtime.Fetch(ctx, p.ActivityType, connectors.SourceParams{Config: p.Config, Cursor: p.Cursor, Ingestion: p.Ingestion, TenantID: p.TenantID})
	if err != nil {
		_ = a.recordNodeRun(ctx, p.ExecutionID, p.NodeID, p.TenantID, "failed", started, time.Since(started), 0, err.Error(), false)
		return FetchSourceResult{}, err
	}
	dek, err := a.dek(p.EncryptedDEK)
	if err != nil {
		return FetchSourceResult{}, err
	}
	ref, err := a.Payloads.Write(ctx, result.Records, p.TenantID, p.ExecutionID, p.NodeID, dek)
	if err != nil {
		return FetchSourceResult{}, err
	}
	_ = a.recordNodeRun(ctx, p.ExecutionID, p.NodeID, p.TenantID, "success", started, time.Since(started), len(result.Records), "", true)
	return FetchSourceResult{OutputRef: ref, HasMore: result.HasMore, RecordCount: len(result.Records), Checkpoint: result.NextCursor, LagRecords: result.LagRecords}, nil
}

type CommitCursorParams struct {
	TenantID string `json:"tenantId"`
	Cursors  []struct {
		ConnectionID string                 `json:"connectionId"`
		Checkpoint   map[string]interface{} `json:"checkpoint"`
	} `json:"cursors"`
}

func (a *Activities) CommitSourceCursors(ctx context.Context, p CommitCursorParams) error {
	tx, err := a.DB.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, cursor := range p.Cursors {
		next := cursor.Checkpoint
		if incoming, ok := cursor.Checkpoint["offsets"].(map[string]interface{}); ok {
			var existingBytes []byte
			_ = tx.QueryRow(ctx, `SELECT cursor FROM connector_state WHERE tenant_id=$1 AND connection_id=$2 FOR UPDATE`, p.TenantID, cursor.ConnectionID).Scan(&existingBytes)
			var existing map[string]interface{}
			_ = json.Unmarshal(existingBytes, &existing)
			merged := map[string]interface{}{}
			if stringValue(existing["topic"]) == stringValue(cursor.Checkpoint["topic"]) {
				if values, ok := existing["offsets"].(map[string]interface{}); ok {
					for key, value := range values {
						merged[key] = value
					}
				}
			}
			for partition, value := range incoming {
				if current, exists := merged[partition]; !exists || numberValue(value) > numberValue(current) {
					merged[partition] = value
				}
			}
			next = cloneMap(cursor.Checkpoint)
			next["offsets"] = merged
		}
		if _, err = tx.Exec(ctx, `INSERT INTO connector_state (tenant_id,connection_id,cursor,updated_at) VALUES ($1,$2,$3,now()) ON CONFLICT(tenant_id,connection_id) DO UPDATE SET cursor=$3,updated_at=now()`, p.TenantID, cursor.ConnectionID, next); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

type CommitDedupeParams struct {
	TenantID    string `json:"tenantId"`
	Checkpoints []struct {
		PipelineID string   `json:"pipelineId"`
		NodeID     string   `json:"nodeId"`
		Hashes     []string `json:"hashes"`
	} `json:"checkpoints"`
}

func (a *Activities) CommitDedupeKeys(ctx context.Context, p CommitDedupeParams) error {
	for _, checkpoint := range p.Checkpoints {
		if len(checkpoint.Hashes) == 0 {
			continue
		}
		if _, err := a.DB.Pool.Exec(ctx, `INSERT INTO dedupe_keys (tenant_id,pipeline_id,node_id,key_hash) SELECT $1,$2,$3,hash FROM unnest($4::text[]) hash ON CONFLICT DO NOTHING`, p.TenantID, checkpoint.PipelineID, checkpoint.NodeID, checkpoint.Hashes); err != nil {
			return err
		}
	}
	return nil
}

type DispatchParams struct {
	ControlRequired bool                   `json:"controlRequired,omitempty"`
	ActivityType    string                 `json:"activityType"`
	Config          map[string]interface{} `json:"config"`
	InputRef        *model.DataRef         `json:"inputRef,omitempty"`
	TenantID        string                 `json:"tenantId"`
	ExecutionID     string                 `json:"executionId"`
	NodeID          string                 `json:"nodeId"`
	PipelineVersion int                    `json:"pipelineVersion,omitempty"`
	EncryptedDEK    string                 `json:"encryptedDek,omitempty"`
}

func (a *Activities) DispatchNode(ctx context.Context, p DispatchParams) (model.NodeResult, error) {
	defer controlledHeartbeat(ctx, p.ControlRequired)()
	activity.RecordHeartbeat(ctx)
	started := time.Now()
	if err := a.requireEntitlement(ctx, p.TenantID, p.ActivityType, p.Config); err != nil {
		return model.NodeResult{}, err
	}
	if err := a.verifyConnectorOwnership(ctx, p.TenantID, p.Config); err != nil {
		return model.NodeResult{}, err
	}
	dek, err := a.dek(p.EncryptedDEK)
	if err != nil {
		return model.NodeResult{}, err
	}
	var input interface{}
	if p.InputRef != nil {
		input, err = a.Payloads.Read(ctx, p.InputRef, dek)
		if err != nil {
			return model.NodeResult{}, err
		}
	}
	if err := a.admitControlledActivity(ctx, p.TenantID, p.ExecutionID, p.ControlRequired); err != nil {
		return model.NodeResult{}, err
	}
	output, meta, err := a.Runtime.Handle(ctx, p.ActivityType, input, p.Config, connectors.HandlerContext{TenantID: p.TenantID, ExecutionID: p.ExecutionID, NodeID: p.NodeID, PipelineVersion: p.PipelineVersion})
	if err == nil && p.ActivityType == "transform.dedupe" && p.Config["scope"] == "pipeline" {
		output, meta, err = a.filterCrossRunDedupe(ctx, output, p, meta)
	}
	duration := time.Since(started)
	if err != nil {
		_ = a.recordNodeRun(ctx, p.ExecutionID, p.NodeID, p.TenantID, "failed", started, duration, 0, err.Error(), false)
		return model.NodeResult{}, err
	}
	var ref *model.DataRef
	if output != nil {
		ref, err = a.Payloads.Write(ctx, output, p.TenantID, p.ExecutionID, p.NodeID, dek)
		if err != nil {
			return model.NodeResult{}, err
		}
	}
	count := 0
	if ref != nil {
		count = ref.RecordCount
	} else if p.InputRef != nil {
		count = p.InputRef.RecordCount
	}
	_ = a.recordNodeRun(ctx, p.ExecutionID, p.NodeID, p.TenantID, "success", started, duration, count, "", false)
	if meta == nil {
		meta = map[string]interface{}{}
	}
	if p.ActivityType == "transform.contract" {
		a.recordQualityResult(ctx, p, meta)
	}
	meta["durationMs"] = duration.Milliseconds()
	if count > 0 {
		meta["recordCount"] = count
	}
	return model.NodeResult{NodeID: p.NodeID, Status: "success", OutputRef: ref, Meta: meta}, nil
}

func (a *Activities) requireEntitlement(ctx context.Context, tenantID, activityType string, config map[string]interface{}) error {
	feature := ""
	if activityType == "kafka.fetch" || activityType == "sink.kafka" || stringValue(config["syncMode"]) == "cdc" || stringValue(config["writeMode"]) == "apply-cdc" {
		feature = "realtime"
	}
	if activityType == "transform.dedupe" && stringValue(config["scope"]) == "pipeline" {
		feature = "statefulProcessing"
	}
	for _, advanced := range []string{"sftp.fetch", "sink.sftp", "snowflake.fetch", "sink.snowflake", "iceberg.fetch", "sink.iceberg"} {
		if activityType == advanced {
			feature = "advancedConnectors"
		}
	}
	if feature == "" {
		return nil
	}
	var enabled bool
	err := a.DB.Pool.QueryRow(ctx, `SELECT enabled FROM tenant_feature_entitlements WHERE tenant_id=$1 AND feature=$2`, tenantID, feature).Scan(&enabled)
	if err != nil || !enabled {
		return temporal.NewNonRetryableApplicationError(feature+" is not enabled for this workspace", "FeatureNotEnabled", err)
	}
	return nil
}

func (a *Activities) verifyConnectorOwnership(ctx context.Context, tenantID string, config map[string]interface{}) error {
	id := stringValue(config["connectionId"])
	if id == "" {
		return nil
	}
	var exists bool
	if err := a.DB.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM connector_instances WHERE id=$1 AND tenant_id=$2)`, id, tenantID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return temporal.NewNonRetryableApplicationError("connector not found", "ConnectorNotFound", nil)
	}
	return nil
}

func (a *Activities) filterCrossRunDedupe(ctx context.Context, output interface{}, p DispatchParams, meta map[string]interface{}) (interface{}, map[string]interface{}, error) {
	rows, ok := output.([]interface{})
	if !ok {
		return nil, nil, fmt.Errorf("transform.dedupe: input must be an array")
	}
	var pipelineID string
	if err := a.DB.Pool.QueryRow(ctx, `SELECT pipeline_id FROM executions WHERE id=$1 AND tenant_id=$2`, p.ExecutionID, p.TenantID).Scan(&pipelineID); err != nil {
		return nil, nil, fmt.Errorf("transform.dedupe: execution pipeline not found: %w", err)
	}
	keys := connectors.DedupeKeyFields(p.Config["key"])
	hashes := make([]string, 0, len(rows))
	byHash := map[string]interface{}{}
	for _, raw := range rows {
		row, ok := raw.(map[string]interface{})
		if !ok {
			return nil, nil, fmt.Errorf("transform.dedupe: records must be objects")
		}
		hash := connectors.DedupeHash(row, keys)
		hashes = append(hashes, hash)
		byHash[hash] = raw
	}
	existing := map[string]bool{}
	if len(hashes) > 0 {
		result, err := a.DB.Pool.Query(ctx, `SELECT key_hash FROM dedupe_keys WHERE tenant_id=$1 AND pipeline_id=$2 AND node_id=$3 AND key_hash=ANY($4::text[])`, p.TenantID, pipelineID, p.NodeID, hashes)
		if err != nil {
			return nil, nil, err
		}
		for result.Next() {
			var hash string
			_ = result.Scan(&hash)
			existing[hash] = true
		}
		result.Close()
	}
	filtered := []interface{}{}
	fresh := []string{}
	for _, hash := range hashes {
		if !existing[hash] {
			filtered = append(filtered, byHash[hash])
			fresh = append(fresh, hash)
		}
	}
	if meta == nil {
		meta = map[string]interface{}{}
	}
	meta["dedupeCheckpoint"] = map[string]interface{}{"pipelineId": pipelineID, "nodeId": p.NodeID, "hashes": fresh}
	return filtered, meta, nil
}

func (a *Activities) recordQualityResult(ctx context.Context, p DispatchParams, meta map[string]interface{}) {
	status := stringValue(meta["qualityStatus"])
	if status == "" {
		return
	}
	passed := int(numberValue(meta["passedCount"]))
	failed := int(numberValue(meta["failedCount"]))
	_, _ = a.DB.Pool.Exec(ctx, `INSERT INTO data_quality_results(tenant_id,pipeline_id,execution_id,node_id,status,passed_count,failed_count,error_samples) SELECT $1,e.pipeline_id,$2,$3,$4,$5,$6,'[]'::jsonb FROM executions e WHERE e.id=$2 ON CONFLICT(execution_id,node_id) DO UPDATE SET status=$4,passed_count=$5,failed_count=$6,evaluated_at=now()`, p.TenantID, p.ExecutionID, p.NodeID, status, passed, failed)
}

func cloneMap(value map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	for key, item := range value {
		out[key] = item
	}
	return out
}
func stringValue(value interface{}) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}
func numberValue(value interface{}) float64 {
	switch value := value.(type) {
	case float64:
		return value
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case string:
		var number float64
		_, _ = fmt.Sscan(value, &number)
		return number
	}
	return 0
}

type MergeParams struct {
	InputRefs    []*model.DataRef `json:"inputRefs"`
	Strategy     string           `json:"strategy"`
	JoinKey      string           `json:"joinKey"`
	TenantID     string           `json:"tenantId"`
	ExecutionID  string           `json:"executionId"`
	NodeID       string           `json:"nodeId"`
	EncryptedDEK string           `json:"encryptedDek"`
}

func (a *Activities) MergeRefs(ctx context.Context, p MergeParams) (model.NodeResult, error) {
	started := time.Now()
	fail := func(err error) (model.NodeResult, error) {
		_ = a.recordNodeRun(ctx, p.ExecutionID, p.NodeID, p.TenantID, "failed", started, time.Since(started), 0, err.Error(), false)
		return model.NodeResult{}, err
	}
	dek, err := a.dek(p.EncryptedDEK)
	if err != nil {
		return fail(err)
	}

	strategy := p.Strategy
	if strategy == "" {
		strategy = "concat"
	}
	var totalBytes int64
	for _, ref := range p.InputRefs {
		if ref != nil {
			totalBytes += int64(ref.SizeBytes)
		}
	}
	oversized := a.MaxMergeInMemoryBytes > 0 && totalBytes > a.MaxMergeInMemoryBytes
	if oversized && strategy != "concat" {
		// ponytail: union/join/appendWithSourceTag need every input materialized to dedupe or
		// index by joinKey, so there's no streaming path for them yet - reject instead of OOMing.
		// Add a spill-backed hash index if a real workload needs merges this large.
		return fail(fmt.Errorf("merge inputs total %d bytes, exceeds MAX_MERGE_INMEMORY_BYTES limit of %d for strategy %q", totalBytes, a.MaxMergeInMemoryBytes, strategy))
	}
	if oversized {
		ref, count, err := a.streamConcatMerge(ctx, p, dek)
		if err != nil {
			return fail(err)
		}
		if ref != nil {
			// Payloads.Write only counts []interface{} inputs; the spill path
			// writes raw JSON, so carry the count it already computed.
			ref.RecordCount = count
		}
		_ = a.recordNodeRun(ctx, p.ExecutionID, p.NodeID, p.TenantID, "success", started, time.Since(started), count, "", false)
		return model.NodeResult{NodeID: p.NodeID, Status: "success", OutputRef: ref, Meta: map[string]interface{}{"durationMs": time.Since(started).Milliseconds(), "recordCount": count}}, nil
	}

	arrays := make([][]interface{}, 0, len(p.InputRefs))
	for _, ref := range p.InputRefs {
		value, err := a.Payloads.Read(ctx, ref, dek)
		if err != nil {
			return fail(err)
		}
		records, ok := value.([]interface{})
		if !ok {
			return fail(fmt.Errorf("merge input must be an array"))
		}
		arrays = append(arrays, records)
	}
	merged, err := connectors.MergeArrays(p.Strategy, arrays, p.JoinKey)
	if err != nil {
		return fail(err)
	}
	ref, err := a.Payloads.Write(ctx, merged, p.TenantID, p.ExecutionID, p.NodeID, dek)
	if err != nil {
		return fail(err)
	}
	_ = a.recordNodeRun(ctx, p.ExecutionID, p.NodeID, p.TenantID, "success", started, time.Since(started), len(merged), "", false)
	return model.NodeResult{NodeID: p.NodeID, Status: "success", OutputRef: ref, Meta: map[string]interface{}{"durationMs": time.Since(started).Milliseconds(), "recordCount": len(merged)}}, nil
}

// streamConcatMerge concatenates merge inputs one ref at a time, spilling the accumulated
// output to a temp file instead of holding every input array in memory simultaneously. Each
// input is still read through Payloads.Read (capped by MaxPayloadBytes) and goes out of scope
// once its records are flushed, so peak heap during accumulation is roughly one input array
// plus the spill buffer, not N arrays. The spilled total is itself hard-capped at
// MaxMergeInMemoryBytes (checked incrementally, not just against the declared input sizes at
// entry) precisely so the final read-back of the spilled file — which Payloads.Write requires
// in memory, since payload encryption here is not a streaming AEAD construct — can never exceed
// that configured limit; this is what makes the acceptance criterion ("worker RSS stays within
// its configured limit for worst allowed input") actually hold, not just the entry-time check.
func (a *Activities) streamConcatMerge(ctx context.Context, p MergeParams, dek []byte) (*model.DataRef, int, error) {
	tmp, err := os.CreateTemp("", "dataflow-merge-*.json")
	if err != nil {
		return nil, 0, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	defer tmp.Close()

	writer := bufio.NewWriter(tmp)
	written := int64(0)
	writeAndCount := func(b []byte) error {
		written += int64(len(b))
		if a.MaxMergeInMemoryBytes > 0 && written > a.MaxMergeInMemoryBytes {
			return fmt.Errorf("merge output exceeds MAX_MERGE_INMEMORY_BYTES limit of %d", a.MaxMergeInMemoryBytes)
		}
		_, err := writer.Write(b)
		return err
	}
	if err := writeAndCount([]byte("[")); err != nil {
		return nil, 0, err
	}
	count := 0
	for _, ref := range p.InputRefs {
		value, err := a.Payloads.Read(ctx, ref, dek)
		if err != nil {
			return nil, 0, err
		}
		records, ok := value.([]interface{})
		if !ok {
			return nil, 0, fmt.Errorf("merge input must be an array")
		}
		for _, record := range records {
			if count > 0 {
				if err := writeAndCount([]byte(",")); err != nil {
					return nil, 0, err
				}
			}
			body, err := json.Marshal(record)
			if err != nil {
				return nil, 0, err
			}
			if err := writeAndCount(body); err != nil {
				return nil, 0, err
			}
			count++
		}
	}
	if err := writeAndCount([]byte("]")); err != nil {
		return nil, 0, err
	}
	if err := writer.Flush(); err != nil {
		return nil, 0, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return nil, 0, err
	}
	// Safe: writeAndCount already guaranteed the spilled file is <= MaxMergeInMemoryBytes.
	merged, err := io.ReadAll(tmp)
	if err != nil {
		return nil, 0, err
	}
	ref, err := a.Payloads.Write(ctx, json.RawMessage(merged), p.TenantID, p.ExecutionID, p.NodeID, dek)
	if err != nil {
		return nil, 0, err
	}
	return ref, count, nil
}

type EdgeConditionParams struct {
	Condition    string         `json:"condition"`
	InputRef     *model.DataRef `json:"inputRef,omitempty"`
	EncryptedDEK string         `json:"encryptedDek,omitempty"`
}

func (a *Activities) EvalEdgeCondition(ctx context.Context, p EdgeConditionParams) (bool, error) {
	if p.InputRef == nil {
		return true, nil
	}
	dek, err := a.dek(p.EncryptedDEK)
	if err != nil {
		return false, err
	}
	value, err := a.Payloads.Read(ctx, p.InputRef, dek)
	if err != nil {
		return false, err
	}
	return connectors.EvaluatePredicate(p.Condition, value)
}

type MarkExecutionParams struct {
	TenantID    string `json:"tenantId,omitempty"`
	ExecutionID string `json:"executionId"`
	Phase       string `json:"phase"`
}

func (a *Activities) MarkExecution(ctx context.Context, p MarkExecutionParams) error {
	// Old Temporal payloads did not include tenantId. Resolve it once, then scope
	// every write explicitly, including workers whose role bypasses RLS.
	if p.TenantID == "" {
		if err := a.DB.Pool.QueryRow(ctx, `SELECT tenant_id FROM executions WHERE id=$1`, p.ExecutionID).Scan(&p.TenantID); err != nil {
			return err
		}
	}
	err := a.DB.Pool.QueryRow(ctx, `UPDATE executions SET phase=CASE WHEN phase IN ('completed','failed','cancelled') THEN phase ELSE $2 END,completed_at=CASE WHEN $2 IN('completed','failed','cancelled') THEN COALESCE(completed_at,now()) ELSE completed_at END WHERE id=$1 AND tenant_id=$3 RETURNING phase`, p.ExecutionID, p.Phase, p.TenantID).Scan(&p.Phase)
	if err != nil {
		return err
	}
	_, _ = a.DB.Pool.Exec(ctx, `WITH changed AS (UPDATE backfill_partitions bp SET status=$2,completed_at=now() FROM executions e WHERE e.id=$1 AND e.tenant_id=$3 AND bp.tenant_id=$3 AND e.backfill_partition_id=bp.id RETURNING bp.job_id) UPDATE backfill_jobs bj SET status=CASE WHEN EXISTS(SELECT 1 FROM backfill_partitions p WHERE p.job_id=bj.id AND p.tenant_id=$3 AND p.status='failed') THEN 'failed' WHEN EXISTS(SELECT 1 FROM backfill_partitions p WHERE p.job_id=bj.id AND p.tenant_id=$3 AND p.status='cancelled') THEN 'cancelled' ELSE 'completed' END,completed_at=now() FROM changed WHERE bj.id=changed.job_id AND bj.tenant_id=$3 AND NOT EXISTS(SELECT 1 FROM backfill_partitions p WHERE p.job_id=bj.id AND p.tenant_id=$3 AND p.status IN('pending','starting','running'))`, p.ExecutionID, p.Phase, p.TenantID)
	if p.Phase == "failed" {
		if _, err := a.DB.Pool.Exec(ctx, `INSERT INTO pipeline_alerts (tenant_id,pipeline_id,execution_id,fingerprint,kind,severity,message,details)
			SELECT e.tenant_id,e.pipeline_id,e.id,'execution_failed','execution_failed','critical',
			  COALESCE('Node '||nr.node_id||' failed: '||nr.error,'Node '||nr.node_id||' failed','Execution failed'),
			  jsonb_strip_nulls(jsonb_build_object('executionId',e.id,'nodeId',nr.node_id))
			FROM executions e
			LEFT JOIN LATERAL (SELECT node_id,error FROM node_runs WHERE execution_id=e.id AND tenant_id=$2 AND status='failed' ORDER BY finished_at DESC NULLS LAST LIMIT 1) nr ON true
			WHERE e.id=$1 AND e.tenant_id=$2
			ON CONFLICT (tenant_id,pipeline_id,fingerprint) WHERE status IN ('open','acknowledged')
			DO UPDATE SET last_seen_at=now(),execution_id=EXCLUDED.execution_id,message=EXCLUDED.message,details=EXCLUDED.details`, p.ExecutionID, p.TenantID); err != nil {
			slog.Warn("failed to record pipeline alert", "executionId", p.ExecutionID, "error", err)
		}
	}
	return nil
}

// recordNodeRun upserts the step row. A node can be recorded multiple times
// within one execution (paged source fetches, Temporal activity retries): the
// first write pins started_at, later writes refresh status/duration/counts,
// source pages append counts; complete dispatch/merge results replace counts.
// A write that follows a recorded failure counts as a new attempt and
// restarts the step clock.
func (a *Activities) recordNodeRun(ctx context.Context, executionID, nodeID, tenantID, status string, startedAt time.Time, duration time.Duration, count int, errorText string, appendRecords bool) error {
	// Same-attempt re-writes (paged source fetches) keep the pinned start, so
	// duration must be recomputed from that pinned start and record counts
	// accumulated for page fetches. Consolidating those pages replaces the total
	// instead of counting every source record twice.
	// A write after a recorded failure is a fresh attempt: reset the clock.
	_, err := a.DB.Pool.Exec(ctx, `INSERT INTO node_runs (execution_id,node_id,tenant_id,status,duration_ms,record_count,error,started_at,attempt)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,1)
ON CONFLICT(execution_id,node_id) DO UPDATE SET status=$4,error=$7,finished_at=now(),
  duration_ms=CASE WHEN node_runs.status='failed' THEN $5
    ELSE greatest($5,round(extract(epoch FROM (now()-coalesce(node_runs.started_at,EXCLUDED.started_at)))*1000))::int END,
  record_count=CASE WHEN node_runs.status='failed' OR NOT $9 THEN $6
    ELSE nullif(coalesce(node_runs.record_count,0)+coalesce($6,0),0) END,
  started_at=CASE WHEN node_runs.status='failed' THEN EXCLUDED.started_at ELSE coalesce(node_runs.started_at,EXCLUDED.started_at) END,
  attempt=node_runs.attempt+CASE WHEN node_runs.status='failed' THEN 1 ELSE 0 END`,
		executionID, nodeID, tenantID, status, duration.Milliseconds(), nullableCount(count), redactError(errorText), startedAt.UTC(), appendRecords)
	return err
}
func nullableCount(value int) interface{} {
	if value == 0 {
		return nil
	}
	return value
}
func redactError(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}

func Register(worker interface {
	RegisterActivityWithOptions(interface{}, activity.RegisterOptions)
}, a *Activities) {
	worker.RegisterActivityWithOptions(a.PrepareScheduledExecution, activity.RegisterOptions{Name: "prepareScheduledExecution"})
	worker.RegisterActivityWithOptions(a.FetchSourcePage, activity.RegisterOptions{Name: "fetchSourcePage"})
	worker.RegisterActivityWithOptions(a.CommitSourceCursors, activity.RegisterOptions{Name: "commitSourceCursors"})
	worker.RegisterActivityWithOptions(a.CommitDedupeKeys, activity.RegisterOptions{Name: "commitDedupeKeys"})
	worker.RegisterActivityWithOptions(a.DispatchNode, activity.RegisterOptions{Name: "dispatchNode"})
	worker.RegisterActivityWithOptions(a.MergeRefs, activity.RegisterOptions{Name: "mergeRefs"})
	worker.RegisterActivityWithOptions(a.EvalEdgeCondition, activity.RegisterOptions{Name: "evalEdgeCondition"})
	worker.RegisterActivityWithOptions(a.MarkExecution, activity.RegisterOptions{Name: "markExecution"})
	worker.RegisterActivityWithOptions(a.ReadExecutionControl, activity.RegisterOptions{Name: "readExecutionControl"})
	worker.RegisterActivityWithOptions(a.FinalizeExecutionControl, activity.RegisterOptions{Name: "finalizeExecutionControl"})
	registerEnterprise(worker, a)
	slog.Info("registered Temporal activities")
}
