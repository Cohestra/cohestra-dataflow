package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/dataflow-poc/workflow-go/internal/model"
	"github.com/jackc/pgx/v5"
)

func (s *Server) executionSignal(w http.ResponseWriter, r *http.Request) error {
	action := r.PathValue("action")
	states := map[string]string{"pause": "paused", "resume": "active", "cancel": "cancel_requested"}
	target, durable := states[action]
	if !durable && action != "rollback" {
		return notFound(ErrNotFound, "not found")
	}
	tenant := tenantFrom(r)
	var control model.ExecutionControl
	var environment, workflowID, runID string
	err := s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		var creator, role *string
		err := tx.QueryRow(r.Context(), `SELECT e.phase,e.control_state,e.control_revision,e.environment,
    COALESCE(e.workflow_id,e.id),COALESCE(e.run_id,''),p.created_by,pa.role
    FROM executions e JOIN pipelines p ON p.id=e.pipeline_id AND p.tenant_id=e.tenant_id
    LEFT JOIN pipeline_access pa ON pa.pipeline_id=p.id AND pa.user_id=$3
    WHERE e.id=$1 AND e.tenant_id=$2 FOR UPDATE OF e`, r.PathValue("id"), tenant.TenantID, tenant.UserID).
			Scan(&control.Phase, &control.State, &control.Revision, &environment, &workflowID, &runID, &creator, &role)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound(ErrNotFound, "not found")
		}
		if err != nil {
			return err
		}
		if tenant.Role != "owner" && (creator == nil || *creator != tenant.UserID) {
			if role == nil {
				return notFound(ErrNotFound, "not found")
			}
			if *role != "editor" && *role != "admin" {
				return &HTTPError{Status: http.StatusForbidden, Code: ErrForbidden, Message: "pipeline access requires editor role"}
			}
		}
		if model.TerminalExecutionPhase(control.Phase) {
			return &HTTPError{Status: http.StatusConflict, Message: "terminal executions cannot be controlled"}
		}
		if control.State == "cancel_requested" && action != "cancel" {
			return &HTTPError{Status: http.StatusConflict, Message: "cancellation has already been requested"}
		}
		if !durable {
			return nil
		}
		// Revision zero predates durable intent: even resume(active) must wake
		// a legacy workflow that may be paused only in Temporal.
		if control.State == target && control.Revision > 0 {
			return nil
		}
		if err = tx.QueryRow(r.Context(), `UPDATE executions SET control_state=$3,control_revision=control_revision+1,control_next_delivery_at=now()
    WHERE id=$1 AND tenant_id=$2 RETURNING control_revision`, r.PathValue("id"), tenant.TenantID, target).Scan(&control.Revision); err != nil {
			return err
		}
		control.State = target
		// Service tokens use a token id, not a users FK. Keep their actor in metadata.
		actor := nullString(tenant.UserID)
		if strings.HasPrefix(tenant.Email, "sa:") {
			actor = nil
		}
		_, err = tx.Exec(r.Context(), `INSERT INTO audit_log (tenant_id,user_id,action,resource,ip_address,user_agent,metadata)
    VALUES ($1,$2,$3,$4,$5,$6,$7)`, tenant.TenantID, actor, "execution."+action, r.PathValue("id"), requestIP(r), r.UserAgent(),
			map[string]interface{}{"actorId": tenant.UserID, "controlState": control.State, "controlRevision": control.Revision})
		return err
	})
	if err != nil {
		return err
	}
	if !durable {
		if err := s.Temporal[environment].SignalWorkflow(r.Context(), workflowID, runID, action, nil); err != nil {
			return err
		}
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"ok": true, "controlState": control.State, "controlRevision": control.Revision})
	return nil
}
