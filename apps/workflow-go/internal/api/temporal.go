package api

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/dataflow-poc/workflow-go/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
)

// W3C traceparent: version-traceid-parentid-flags. Trace id must be 32 hex
// chars and non-zero. https://www.w3.org/TR/trace-context/
var traceparentPattern = regexp.MustCompile(`^[0-9a-f]{2}-([0-9a-f]{32})-[0-9a-f]{16}-[0-9a-f]{2}$`)

func newTraceID() string { return strings.ReplaceAll(uuid.NewString(), "-", "") }

// traceIDFromContext reuses the trace id from an inbound W3C traceparent
// header (stashed by middleware) so external callers can correlate an
// execution with their own distributed trace; otherwise a fresh id is minted.
// The Temporal run_id is deliberately never used as a trace id.
func traceIDFromContext(ctx context.Context) string {
	header, _ := ctx.Value(traceParentKey).(string)
	if match := traceparentPattern.FindStringSubmatch(strings.ToLower(strings.TrimSpace(header))); match != nil && match[1] != strings.Repeat("0", 32) {
		return match[1]
	}
	return newTraceID()
}

type quotaExceeded struct{ Used, Limit int }

func (e *quotaExceeded) Error() string { return "Quota exceeded" }

func monthStart() string { return time.Now().UTC().Format("2006-01") + "-01" }

func (s *Server) consumeQuota(ctx context.Context, tenantID string) error {
	return s.DB.TenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO billing_plans (tenant_id) VALUES ($1) ON CONFLICT DO NOTHING`, tenantID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO usage_counters (tenant_id,month,execution_count) VALUES ($1,$2,0) ON CONFLICT DO NOTHING`, tenantID, monthStart()); err != nil {
			return err
		}
		var limit, used int
		if err := tx.QueryRow(ctx, `SELECT bp.free_tier_limit+bp.extra_quota,uc.execution_count FROM billing_plans bp
      JOIN usage_counters uc ON uc.tenant_id=bp.tenant_id AND uc.month=$2 WHERE bp.tenant_id=$1 FOR UPDATE OF bp,uc`, tenantID, monthStart()).Scan(&limit, &used); err != nil {
			return err
		}
		if used >= limit {
			return &quotaExceeded{Used: used, Limit: limit}
		}
		_, err := tx.Exec(ctx, `UPDATE usage_counters SET execution_count=execution_count+1 WHERE tenant_id=$1 AND month=$2`, tenantID, monthStart())
		return err
	})
}

func (s *Server) releaseQuota(ctx context.Context, tenantID string) {
	_ = s.DB.TenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE usage_counters SET execution_count=GREATEST(0,execution_count-1) WHERE tenant_id=$1 AND month=$2`, tenantID, monthStart())
		return err
	})
}

func (s *Server) fireExecution(ctx context.Context, def model.PipelineDefinition, pipelineRowID, triggerType string, environment model.Environment, payloadRef *model.DataRef, encryptedDEK, retryOf, partitionID string) (string, error) {
	if err := model.ValidateAgentAdmission(def); err != nil {
		return "", err
	}
	if err := model.ValidateNodePolicies(def); err != nil {
		return "", err
	}
	if err := s.consumeQuota(ctx, def.TenantID); err != nil {
		return "", err
	}
	executionID := "exec-" + uuid.NewString()
	traceID := traceIDFromContext(ctx)
	input := model.WorkflowInput{Definition: def, TenantID: def.TenantID, ExecutionID: executionID,
		Environment: environment, Trigger: model.TriggerInput{Type: triggerType, PayloadRef: payloadRef, FiredAt: time.Now().UTC().Format(time.RFC3339Nano)}, TraceID: traceID, EncryptedDEK: encryptedDEK}
	temporalClient := s.Temporal[string(environment)]
	workflowName := "DynamicDAGWorkflow"
	if def.Execution != nil && def.Execution.Engine == "stream-direct" {
		workflowName = "StreamDirectWorkflow"
	}
	if def.Execution != nil && def.Execution.Engine == "spark-sql" {
		workflowName = "SparkJobWorkflow"
	}
	if def.Execution != nil && def.Execution.Engine == "flink-sql" {
		workflowName = "FlinkJobWorkflow"
	}
	run, err := temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: executionID, TaskQueue: "dynamic-dag-" + string(environment)}, workflowName, input)
	if err != nil {
		s.releaseQuota(ctx, def.TenantID)
		return "", err
	}
	err = s.DB.TenantTx(ctx, def.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO executions
      (id,pipeline_id,tenant_id,trigger_type,build_id,environment,workflow_id,run_id,retry_of,backfill_partition_id,trace_id)
      VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, executionID, pipelineRowID, def.TenantID, triggerType, nullString(os.Getenv("BUILD_ID")), environment, executionID, run.GetRunID(), nullString(retryOf), nullString(partitionID), traceID)
		if err != nil {
			return err
		}
		if partitionID != "" {
			_, err = tx.Exec(ctx, `UPDATE backfill_partitions SET status='running',execution_id=$2 WHERE id=$1`, partitionID, executionID)
		}
		return err
	})
	if err != nil {
		_ = temporalClient.TerminateWorkflow(ctx, executionID, run.GetRunID(), "execution metadata insert failed")
		s.releaseQuota(ctx, def.TenantID)
		return "", err
	}
	return executionID, nil
}

func (s *Server) syncSchedule(ctx context.Context, def model.PipelineDefinition, pipelineRowID string, environment model.Environment) error {
	if err := model.ValidateAgentAdmission(def); err != nil {
		return err
	}
	if err := model.ValidateNodePolicies(def); err != nil {
		return err
	}
	if def.Trigger.Type != "cron" {
		return nil
	}
	temporalClient := s.Temporal[string(environment)]
	id := fmt.Sprintf("sched-%s-%s", def.ID, environment)
	_ = temporalClient.ScheduleClient().GetHandle(ctx, id).Delete(ctx)
	_, err := temporalClient.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID: id, Spec: client.ScheduleSpec{CronExpressions: []string{def.Trigger.Schedule}},
		Action: &client.ScheduleWorkflowAction{ID: fmt.Sprintf("schedule-%s-%s", def.ID, environment), Workflow: "DynamicDAGWorkflow", TaskQueue: "dynamic-dag-" + string(environment),
			Args: []interface{}{model.WorkflowInput{Definition: def, TenantID: def.TenantID, PipelineRowID: pipelineRowID, Environment: environment, Trigger: model.TriggerInput{Type: "cron"}}}},
		Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
	})
	return err
}
