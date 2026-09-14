package dispatchers

import (
	"context"
	"time"

	"github.com/dataflow-poc/workflow-go/internal/database"
	"github.com/dataflow-poc/workflow-go/internal/model"
)

type controlSignaler interface {
	SignalWorkflow(context.Context, string, string, string, interface{}) error
}

// DispatchExecutionControls uses the worker's existing namespace client. Rows
// are snapshots: duplicate wake-ups are safe and no SQL lock spans the RPC.
func DispatchExecutionControls(ctx context.Context, db *database.DB, signaler controlSignaler, namespace string) error {
	rows, err := db.Pool.Query(ctx, `SELECT tenant_id,id,COALESCE(workflow_id,id),COALESCE(run_id,''),control_state,control_revision
   FROM executions WHERE environment=$1 AND control_delivered_revision<control_revision
   AND control_next_delivery_at<=now()
   AND phase NOT IN ('completed','failed','cancelled') ORDER BY control_next_delivery_at,tenant_id,id LIMIT 20`, namespace)
	if err != nil {
		return err
	}
	type pending struct {
		tenant, id, workflowID, runID, state string
		revision                             int64
	}
	batch := make([]pending, 0, 20)
	for rows.Next() {
		var p pending
		if err = rows.Scan(&p.tenant, &p.id, &p.workflowID, &p.runID, &p.state, &p.revision); err != nil {
			rows.Close()
			return err
		}
		batch = append(batch, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var firstErr error
	for _, p := range batch {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		signal := map[string]string{"active": "resume", "paused": "pause", "cancel_requested": "cancel"}[p.state]
		callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err = signaler.SignalWorkflow(callCtx, p.workflowID, p.runID, signal, model.ExecutionControl{State: p.state, Revision: p.revision})
		cancel()
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			_, updateErr := db.Pool.Exec(ctx, `UPDATE executions SET control_next_delivery_at=now()+interval '30 seconds'
     WHERE tenant_id=$1 AND id=$2 AND environment=$4 AND control_revision=$3
     AND control_delivered_revision<$3`, p.tenant, p.id, p.revision, namespace)
			if updateErr != nil {
				return updateErr
			}
			continue
		}
		// A newer intent must remain pending, even if the older RPC succeeded.
		_, err = db.Pool.Exec(ctx, `UPDATE executions SET control_delivered_revision=$3
    WHERE tenant_id=$1 AND id=$2 AND environment=$4 AND control_revision=$3
    AND control_delivered_revision<$3`, p.tenant, p.id, p.revision, namespace)
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (g *Group) StartExecutionControls(db *database.DB, signaler controlSignaler, namespace string) {
	g.run(g.ctx, 2*time.Second, func(ctx context.Context) error {
		callCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		return DispatchExecutionControls(callCtx, db, signaler, namespace)
	})
}
