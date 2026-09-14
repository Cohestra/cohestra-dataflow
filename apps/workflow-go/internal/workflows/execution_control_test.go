package workflows

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dataflow-poc/workflow-go/internal/model"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
)

type controlTestState struct {
	mu    sync.Mutex
	value model.ExecutionControl
}

func (s *controlTestState) set(state string, revision int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value = model.ExecutionControl{State: state, Revision: revision, Phase: "running"}
}
func (s *controlTestState) get() model.ExecutionControl {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value
}

func controlEnvironment(t *testing.T) (*testsuite.TestWorkflowEnvironment, *controlTestState) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(worker.Options{MaxHeartbeatThrottleInterval: 10 * time.Millisecond})
	state := &controlTestState{}
	state.set("active", 0)
	env.RegisterActivityWithOptions(func(context.Context, model.ExecutionControlRef) (model.ExecutionControl, error) {
		return state.get(), nil
	}, activity.RegisterOptions{Name: "readExecutionControl"})
	env.RegisterActivityWithOptions(func(ctx context.Context, p map[string]interface{}) (string, error) {
		info := activity.GetInfo(ctx)
		if info.ScheduleToCloseTimeout > 30*time.Second || info.StartToCloseTimeout > 5*time.Second {
			t.Errorf("unbounded cleanup options: %+v", info)
		}
		if p["tenantId"] != "tenant" {
			t.Errorf("missing finalization tenant: %v", p)
		}
		if state.get().State == "cancel_requested" {
			return "cancelled", nil
		}
		return p["phase"].(string), nil
	}, activity.RegisterOptions{Name: "finalizeExecutionControl"})
	return env, state
}

func controlInput(nodes []model.Node, edges ...model.Edge) model.WorkflowInput {
	return model.WorkflowInput{ExecutionID: "execution", TenantID: "tenant", Environment: model.EnvironmentTest, Trigger: model.TriggerInput{Type: "manual"}, Definition: model.PipelineDefinition{ID: "pipeline", Nodes: nodes, Edges: edges}}
}

func controlResult(t *testing.T, env *testsuite.TestWorkflowEnvironment) model.ExecutionStatus {
	t.Helper()
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	var result model.ExecutionStatus
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestControlPauseBetweenPagesAndStaleWakeups(t *testing.T) {
	env, state := controlEnvironment(t)
	var pages, sinks atomic.Int32
	env.RegisterActivityWithOptions(func(_ context.Context, p map[string]interface{}) (map[string]interface{}, error) {
		page := pages.Add(1)
		if p["controlRequired"] != true {
			t.Errorf("source admission not enabled: %v", p)
		}
		if page == 1 {
			state.set("paused", 1)
		}
		return map[string]interface{}{"hasMore": page == 1, "recordCount": 1}, nil
	}, activity.RegisterOptions{Name: "fetchSourcePage"})
	env.RegisterActivityWithOptions(func(_ context.Context, p map[string]interface{}) (model.NodeResult, error) {
		sinks.Add(1)
		return model.NodeResult{NodeID: p["nodeId"].(string), Status: "success"}, nil
	}, activity.RegisterOptions{Name: "dispatchNode"})
	env.RegisterDelayedCallback(func() {
		if pages.Load() != 1 || sinks.Load() != 0 {
			t.Errorf("work escaped pause: pages=%d sinks=%d", pages.Load(), sinks.Load())
		}
		state.set("active", 2)
		// An old cancel signal is only a hint; current durable active state wins.
		env.SignalWorkflow("cancel", model.ExecutionControl{State: "cancel_requested", Revision: 1})
	}, 3*time.Second)
	env.ExecuteWorkflow(DynamicDAGWorkflow, controlInput([]model.Node{{ID: "source", Type: "source"}, {ID: "sink", Type: "sink"}}, model.Edge{Source: "source", Target: "sink"}))
	result := controlResult(t, env)
	if result.Phase != "completed" || pages.Load() != 2 || sinks.Load() != 1 {
		t.Fatalf("result=%+v pages=%d sinks=%d", result, pages.Load(), sinks.Load())
	}
}

func TestControlCancelWhileActivityIsActive(t *testing.T) {
	env, state := controlEnvironment(t)
	var started, cancelRequests, sinks atomic.Int32
	release := make(chan struct{})
	defer close(release)
	env.SetOnActivityCanceledListener(func(info *activity.Info) {
		if info.ActivityType.Name == "dispatchNode" {
			cancelRequests.Add(1)
		}
	})
	env.RegisterActivityWithOptions(func(ctx context.Context, p map[string]interface{}) (model.NodeResult, error) {
		if p["nodeId"] == "sink" {
			sinks.Add(1)
			return model.NodeResult{Status: "success"}, nil
		}
		started.Add(1)
		// The mock server cancels the activity Future; it does not run a real
		// heartbeat RPC after workflow completion. Keep the admitted body active
		// until the test ends and assert the actual cancellation request below.
		select {
		case <-ctx.Done():
			return model.NodeResult{}, ctx.Err()
		case <-release:
			return model.NodeResult{}, nil
		}
	}, activity.RegisterOptions{Name: "dispatchNode"})
	env.RegisterDelayedCallback(func() { state.set("cancel_requested", 1); env.SignalWorkflow("cancel", nil) }, time.Second)
	env.ExecuteWorkflow(DynamicDAGWorkflow, controlInput([]model.Node{{ID: "work", Type: "transform"}, {ID: "sink", Type: "sink"}}, model.Edge{Source: "work", Target: "sink"}))
	result := controlResult(t, env)
	if result.Phase != "cancelled" || sinks.Load() != 0 || started.Load() != 1 || cancelRequests.Load() != 1 {
		t.Fatalf("result=%+v started=%d sinks=%d cancelRequests=%d", result, started.Load(), sinks.Load(), cancelRequests.Load())
	}
}

func TestControlMissingMetadataPreventsBusinessWork(t *testing.T) {
	for _, ready := range []bool{false, true} {
		name := "never_ready"
		if ready {
			name = "delayed_row"
		}
		t.Run(name, func(t *testing.T) {
			env, _ := controlEnvironment(t)
			var reads, effects atomic.Int32
			env.RegisterActivityWithOptions(func(context.Context, model.ExecutionControlRef) (model.ExecutionControl, error) {
				n := reads.Add(1)
				if !ready || n < 3 {
					return model.ExecutionControl{}, temporal.NewApplicationError("metadata missing", "ExecutionNotReady")
				}
				return model.ExecutionControl{State: "active", Phase: "running"}, nil
			}, activity.RegisterOptions{Name: "readExecutionControl", DisableAlreadyRegisteredCheck: true})
			env.RegisterActivityWithOptions(func(context.Context, map[string]interface{}) (model.NodeResult, error) {
				effects.Add(1)
				return model.NodeResult{NodeID: "work", Status: "success"}, nil
			}, activity.RegisterOptions{Name: "dispatchNode"})
			env.ExecuteWorkflow(DynamicDAGWorkflow, controlInput([]model.Node{{ID: "work", Type: "transform"}}))
			if ready {
				if result := controlResult(t, env); result.Phase != "completed" || effects.Load() != 1 {
					t.Fatalf("result=%+v effects=%d", result, effects.Load())
				}
			} else {
				if env.GetWorkflowError() == nil || effects.Load() != 0 || reads.Load() > 10 {
					t.Fatalf("unbounded admission: error=%v reads=%d effects=%d", env.GetWorkflowError(), reads.Load(), effects.Load())
				}
			}
		})
	}
}

func TestControlPauseOnRetryPreservesBusinessAttemptBudget(t *testing.T) {
	env, state := controlEnvironment(t)
	attempts := 2
	var effects, blocked atomic.Int32
	env.RegisterActivityWithOptions(func(ctx context.Context, p map[string]interface{}) (model.NodeResult, error) {
		if state.get().State == "paused" {
			blocked.Add(1)
			return model.NodeResult{}, temporal.NewNonRetryableApplicationError("paused", "ExecutionControlBlocked", nil, "paused", activity.GetInfo(ctx).Attempt)
		}
		if effects.Add(1) == 1 {
			state.set("paused", 1)
		}
		return model.NodeResult{}, errors.New("synthetic remote failure")
	}, activity.RegisterOptions{Name: "dispatchNode"})
	env.RegisterDelayedCallback(func() {
		if effects.Load() != 1 || blocked.Load() != 1 {
			t.Errorf("paused retry effects=%d blocked=%d", effects.Load(), blocked.Load())
		}
		state.set("active", 2)
		env.SignalWorkflow("resume", nil)
	}, 5*time.Second)
	env.ExecuteWorkflow(DynamicDAGWorkflow, controlInput([]model.Node{{ID: "work", Type: "transform", Retry: &model.RetryConfig{MaximumAttempts: &attempts}}}))
	result := controlResult(t, env)
	if result.Phase != "failed" || effects.Load() != 2 {
		t.Fatalf("retry budget reset: phase=%s effects=%d", result.Phase, effects.Load())
	}
}

func TestControlCleanupFailureIsBounded(t *testing.T) {
	env, _ := controlEnvironment(t)
	var calls atomic.Int32
	env.RegisterActivityWithOptions(func(context.Context, map[string]interface{}) (string, error) {
		calls.Add(1)
		return "", errors.New("cleanup database unavailable")
	}, activity.RegisterOptions{Name: "finalizeExecutionControl", DisableAlreadyRegisteredCheck: true})
	env.ExecuteWorkflow(DynamicDAGWorkflow, controlInput(nil))
	if env.GetWorkflowError() == nil || calls.Load() > 10 || calls.Load() == 0 {
		t.Fatalf("cleanup error=%v calls=%d", env.GetWorkflowError(), calls.Load())
	}
}

func TestControlPollingRecoversMissingWakeup(t *testing.T) {
	env, state := controlEnvironment(t)
	state.set("paused", 1)
	var effects atomic.Int32
	env.RegisterActivityWithOptions(func(context.Context, map[string]interface{}) (model.NodeResult, error) {
		effects.Add(1)
		return model.NodeResult{NodeID: "work", Status: "success"}, nil
	}, activity.RegisterOptions{Name: "dispatchNode"})
	env.RegisterDelayedCallback(func() {
		if effects.Load() != 0 {
			t.Error("effect admitted while paused")
		}
		state.set("active", 2) // Deliberately lose the signal; polling must recover.
	}, time.Second)
	env.ExecuteWorkflow(DynamicDAGWorkflow, controlInput([]model.Node{{ID: "work", Type: "transform"}}))
	if result := controlResult(t, env); result.Phase != "completed" || effects.Load() != 1 {
		t.Fatalf("poll recovery result=%+v effects=%d", result, effects.Load())
	}
}
