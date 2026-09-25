package activities

import (
	"context"
	"errors"
	"time"

	"github.com/dataflow-poc/workflow-go/internal/model"
	"github.com/jackc/pgx/v5"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

func (a *Activities) ReadExecutionControl(ctx context.Context, p model.ExecutionControlRef) (model.ExecutionControl, error) {
	var control model.ExecutionControl
	if p.TenantID == "" || p.ExecutionID == "" {
		return control, temporal.NewNonRetryableApplicationError("execution identity is required", "InvalidExecutionIdentity", nil)
	}
	// SHARE orders effect admission with API UPDATE, without keeping a lock over
	// an external request. Work admitted first may still finish after a pause.
	err := a.DB.TenantTx(ctx, p.TenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT control_state,control_revision,phase FROM executions
    WHERE tenant_id=$1 AND id=$2 FOR SHARE`, p.TenantID, p.ExecutionID).Scan(&control.State, &control.Revision, &control.Phase)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return control, temporal.NewApplicationError("execution metadata is not ready", "ExecutionNotReady")
	}
	return control, err
}

func (a *Activities) admitControlledActivity(ctx context.Context, tenantID, executionID string, required bool) error {
	if !required {
		return nil
	} // Payloads from pre-control histories retain compatibility.
	control, err := a.ReadExecutionControl(ctx, model.ExecutionControlRef{TenantID: tenantID, ExecutionID: executionID})
	if err != nil {
		return err
	}
	if control.State != "active" || model.TerminalExecutionPhase(control.Phase) {
		if model.TerminalExecutionPhase(control.Phase) {
			control.State = "cancel_requested"
		}
		return temporal.NewNonRetryableApplicationError("execution admission is blocked", "ExecutionControlBlocked", nil, control.State, activity.GetInfo(ctx).Attempt)
	}
	return ctx.Err()
}

// Temporal delivers cancellation on heartbeat for activities already running.
func controlledHeartbeat(ctx context.Context, required bool) func() {
	if !required {
		return func() {}
	}
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				activity.RecordHeartbeat(ctx)
			}
		}
	}()
	return func() { close(stop) }
}

// FinalizeExecutionControl is called with a bounded disconnected workflow
// context. A cancel that commits before finalization wins over completion.
func (a *Activities) FinalizeExecutionControl(ctx context.Context, p MarkExecutionParams) (string, error) {
	if p.TenantID == "" {
		return "", temporal.NewNonRetryableApplicationError("tenant is required", "InvalidExecutionIdentity", nil)
	}
	err := a.DB.TenantTx(ctx, p.TenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `UPDATE executions SET
    phase=CASE WHEN phase IN ('completed','failed','cancelled') THEN phase
      WHEN control_state='cancel_requested' THEN 'cancelled' ELSE $3 END,
    completed_at=COALESCE(completed_at,now())
    WHERE tenant_id=$1 AND id=$2 RETURNING phase`, p.TenantID, p.ExecutionID, p.Phase).Scan(&p.Phase)
	})
	if err != nil {
		return "", err
	}
	// Preserve existing backfill and alert side effects; retries are idempotent.
	if err = a.MarkExecution(ctx, p); err != nil {
		return "", err
	}
	return p.Phase, nil
}
