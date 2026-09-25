package workflows

import (
	"errors"
	"fmt"
	"time"

	"github.com/dataflow-poc/workflow-go/internal/model"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type controlContextKey struct{}

type executionController struct {
	ref            model.ExecutionControlRef
	control        model.ExecutionControl
	state          *workflowState
	cancelBusiness workflow.CancelFunc
	failure        error
}

func controlActivityContext(ctx workflow.Context) workflow.Context {
	options := workflow.GetActivityOptions(ctx)
	options.StartToCloseTimeout = 5 * time.Second
	options.ScheduleToCloseTimeout = 30 * time.Second
	options.HeartbeatTimeout = 0
	options.RetryPolicy = &temporal.RetryPolicy{InitialInterval: time.Second, MaximumInterval: 2 * time.Second, MaximumAttempts: 10}
	return workflow.WithActivityOptions(ctx, options)
}

func (c *executionController) refresh(ctx workflow.Context) error {
	readCtx := controlActivityContext(ctx)
	var value model.ExecutionControl
	if err := workflow.ExecuteActivity(readCtx, "readExecutionControl", c.ref).Get(readCtx, &value); err != nil {
		return err
	}
	if value.Revision < c.control.Revision {
		return nil
	}
	c.control = value
	c.state.Paused = value.State == "paused"
	if value.State == "cancel_requested" || model.TerminalExecutionPhase(value.Phase) {
		c.state.Cancelled = true
		c.cancelBusiness()
	}
	return nil
}

func (c *executionController) waitActive(ctx workflow.Context) error {
	if err := c.refresh(ctx); err != nil {
		return err
	}
	if err := workflow.Await(ctx, func() bool { return !c.state.Paused || c.state.Cancelled || c.failure != nil }); err != nil {
		return err
	}
	if c.failure != nil {
		return c.failure
	}
	if c.state.Cancelled {
		return temporal.NewCanceledError("execution cancellation requested")
	}
	return ctx.Err()
}

func (c *executionController) watch(ctx workflow.Context) {
	// Histories from before v1 failed the run on any watcher read error. New runs
	// keep the last known control state through a transient database outage:
	// every business activity still re-reads control and fails closed on admission.
	nonfatalRefresh := workflow.GetVersion(ctx, "control-watch-nonfatal-refresh-v1", workflow.DefaultVersion, 1) != workflow.DefaultVersion
	pause := workflow.GetSignalChannel(ctx, "pause")
	resume := workflow.GetSignalChannel(ctx, "resume")
	cancel := workflow.GetSignalChannel(ctx, "cancel")
	for ctx.Err() == nil {
		timerCtx, stop := workflow.WithCancel(ctx)
		selector := workflow.NewSelector(ctx)
		// Hints, including old/nil payloads, only wake an authoritative re-read.
		for _, ch := range []workflow.ReceiveChannel{pause, resume, cancel} {
			selector.AddReceive(ch, func(ch workflow.ReceiveChannel, _ bool) { ch.Receive(ctx, nil) })
		}
		selector.AddFuture(workflow.NewTimer(timerCtx, 30*time.Second), func(workflow.Future) {})
		selector.AddReceive(ctx.Done(), func(workflow.ReceiveChannel, bool) {})
		selector.Select(ctx)
		stop()
		if ctx.Err() != nil {
			return
		}
		if err := c.refresh(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			if nonfatalRefresh {
				workflow.GetLogger(ctx).Warn("execution control refresh failed; keeping last known state", "error", err)
				continue
			}
			c.failure = err
			c.cancelBusiness()
			return
		}
		if c.state.Cancelled {
			return
		}
	}
}

// executeNodeActivity applies durable gates only to new histories. A worker
// blocked before admission has performed no business attempt: wait for resume
// and preserve any attempts consumed before that blocked automatic retry.
func executeNodeActivity(ctx workflow.Context, name string, args map[string]interface{}, out interface{}) error {
	c, _ := ctx.Value(controlContextKey{}).(*executionController)
	if c == nil {
		return workflow.ExecuteActivity(ctx, name, args).Get(ctx, out)
	}
	args["controlRequired"] = true
	options := workflow.GetActivityOptions(ctx)
	// The blocked-attempt arithmetic below needs a finite budget. ValidateNodePolicies
	// rejects 0 (Temporal's "unlimited") and the workflow default is 5.
	if options.RetryPolicy == nil || options.RetryPolicy.MaximumAttempts < 1 {
		return fmt.Errorf("controlled activity %s requires a finite retry budget", name)
	}
	remaining := options.RetryPolicy.MaximumAttempts
	for {
		if err := c.waitActive(ctx); err != nil {
			return err
		}
		attemptCtx := ctx
		if remaining != options.RetryPolicy.MaximumAttempts {
			policy := *options.RetryPolicy
			policy.MaximumAttempts = remaining
			next := options
			next.RetryPolicy = &policy
			attemptCtx = workflow.WithActivityOptions(ctx, next)
		}
		err := workflow.ExecuteActivity(attemptCtx, name, args).Get(attemptCtx, out)
		var appErr *temporal.ApplicationError
		if !errors.As(err, &appErr) || appErr.Type() != "ExecutionControlBlocked" {
			return err
		}
		var state string
		var attempt int32
		if detailsErr := appErr.Details(&state, &attempt); detailsErr != nil {
			return detailsErr
		}
		if state == "cancel_requested" {
			c.state.Cancelled = true
			c.cancelBusiness()
			return temporal.NewCanceledError("execution cancellation requested")
		}
		if state != "paused" || attempt < 1 || attempt > remaining {
			return fmt.Errorf("invalid blocked activity attempt")
		}
		remaining -= attempt - 1
	}
}

func controlledDAGWorkflow(ctx workflow.Context, input model.WorkflowInput, nodePolicies bool) (status model.ExecutionStatus, runErr error) {
	state := &workflowState{Results: map[string]model.NodeResult{}}
	started := workflow.Now(ctx).UTC().Format(time.RFC3339Nano)
	businessCtx, cancelBusiness := workflow.WithCancel(ctx)
	monitorCtx, stopMonitor := workflow.WithCancel(ctx)
	defer cancelBusiness()
	defer stopMonitor()
	c := &executionController{ref: model.ExecutionControlRef{TenantID: input.TenantID, ExecutionID: input.ExecutionID}, control: model.ExecutionControl{State: "active"}, state: state, cancelBusiness: cancelBusiness}
	businessCtx = workflow.WithValue(businessCtx, controlContextKey{}, c)
	status = model.ExecutionStatus{ExecutionID: input.ExecutionID, Phase: "running", NodeResults: state.Results, StartedAt: started}
	// Finalization remains possible after cancellation, but cannot keep a workflow
	// alive indefinitely when the database/activity worker is unavailable.
	defer func() {
		cancelBusiness()
		stopMonitor()
		if c.failure != nil {
			runErr = c.failure
		}
		phase := "completed"
		if state.Cancelled || (temporal.IsCanceledError(runErr) && c.failure == nil) {
			phase = "cancelled"
		} else if runErr != nil || c.failure != nil {
			phase = "failed"
		} else {
			for _, result := range state.Results {
				if result.Status == "failed" {
					phase = "failed"
					break
				}
			}
		}
		cleanup, stop := workflow.NewDisconnectedContext(ctx)
		defer stop()
		cleanup = controlActivityContext(cleanup)
		var actual string
		err := workflow.ExecuteActivity(cleanup, "finalizeExecutionControl", map[string]interface{}{"tenantId": input.TenantID, "executionId": input.ExecutionID, "phase": phase}).Get(cleanup, &actual)
		status.Phase = phase
		status.CompletedAt = workflow.Now(ctx).UTC().Format(time.RFC3339Nano)
		if err != nil {
			if runErr == nil {
				runErr = err
			}
			return
		}
		status.Phase = actual
		if actual == "cancelled" && c.failure == nil {
			runErr = nil
		}
	}()
	if err := workflow.SetQueryHandler(ctx, "status", func() (map[string]interface{}, error) {
		phase := status.Phase
		if state.Paused && !state.Cancelled {
			phase = "paused"
		}
		return map[string]interface{}{"executionId": input.ExecutionID, "phase": phase, "nodeResults": state.Results, "startedAt": started, "controlState": c.control.State, "controlRevision": c.control.Revision}, nil
	}); err != nil {
		return status, err
	}
	// The API starts Temporal before inserting the execution row. This bounded
	// retried read must succeed before scheduling any source/transform/sink.
	if err := c.refresh(ctx); err != nil {
		return status, err
	}
	workflow.Go(monitorCtx, func(ctx workflow.Context) { c.watch(ctx) })
	plan, err := buildPlan(input.Definition.Nodes, input.Definition.Edges)
	if err != nil {
		return status, err
	}
	parallel := 5
	if input.Definition.Concurrency != nil && input.Definition.Concurrency.MaxParallelNodes > 0 {
		parallel = input.Definition.Concurrency.MaxParallelNodes
	}
	for _, level := range plan.Levels {
		for start := 0; start < len(level); start += parallel {
			if err := c.waitActive(businessCtx); err != nil {
				return status, err
			}
			end := start + parallel
			if end > len(level) {
				end = len(level)
			}
			futures := make([]workflow.Future, 0, end-start)
			for _, node := range level[start:end] {
				node := node
				future, settable := workflow.NewFuture(ctx)
				workflow.Go(businessCtx, func(nodeCtx workflow.Context) {
					if nodePolicies {
						nodeCtx = withNodeActivityOptions(nodeCtx, node)
					}
					result, err := runNode(nodeCtx, input, node, plan.Incoming, state.Results)
					settable.Set(result, err)
				})
				futures = append(futures, future)
			}
			for index, future := range futures {
				var result model.NodeResult
				if err := future.Get(businessCtx, &result); err != nil {
					return status, err
				}
				state.Results[level[start+index].ID] = result
			}
			if c.failure != nil {
				return status, c.failure
			}
			if state.Cancelled {
				return status, temporal.NewCanceledError("execution cancellation requested")
			}
		}
	}
	for _, result := range state.Results {
		if result.Status == "failed" {
			return status, nil
		}
	}
	if err := c.waitActive(businessCtx); err != nil {
		return status, err
	}
	cursors := make([]map[string]interface{}, 0)
	dedupe := make([]map[string]interface{}, 0)
	// Traverse definition order so multi-source completion commits are deterministic.
	for _, node := range input.Definition.Nodes {
		result := state.Results[node.ID]
		checkpoint, ok := result.Meta["checkpoint"].(map[string]interface{})
		connectionID, hasID := result.Meta["connectionId"].(string)
		if ok && hasID {
			cursors = append(cursors, map[string]interface{}{"connectionId": connectionID, "checkpoint": checkpoint})
		}
		if checkpoint, ok := result.Meta["dedupeCheckpoint"].(map[string]interface{}); ok {
			dedupe = append(dedupe, checkpoint)
		}
	}
	if len(cursors) > 0 {
		if err := workflow.ExecuteActivity(businessCtx, "commitSourceCursors", map[string]interface{}{"tenantId": input.TenantID, "cursors": cursors}).Get(businessCtx, nil); err != nil {
			return status, err
		}
	}
	if len(dedupe) > 0 {
		if err := c.waitActive(businessCtx); err != nil {
			return status, err
		}
		if err := workflow.ExecuteActivity(businessCtx, "commitDedupeKeys", map[string]interface{}{"tenantId": input.TenantID, "checkpoints": dedupe}).Get(businessCtx, nil); err != nil {
			return status, err
		}
	}
	return status, nil
}
