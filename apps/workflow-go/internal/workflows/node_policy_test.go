package workflows

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/dataflow-poc/workflow-go/internal/model"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestNodePoliciesApplyToPagesAndDoNotLeak(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%v", legacy), func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			if legacy {
				env.OnGetVersion("node-activity-policy-v1", workflow.DefaultVersion, workflow.Version(1)).Return(workflow.DefaultVersion)
			}
			var mu sync.Mutex
			calls := map[string]int{}
			check := func(ctx context.Context, key string, timeout time.Duration, attempts int32) int {
				mu.Lock()
				defer mu.Unlock()
				calls[key]++
				info := activity.GetInfo(ctx)
				if legacy {
					timeout, attempts = 10*time.Minute, 5
				}
				if info.StartToCloseTimeout != timeout || info.Attempt > attempts {
					t.Errorf("%s timeout=%v attempt=%d; want timeout=%v and at most %d attempts", key, info.StartToCloseTimeout, info.Attempt, timeout, attempts)
				}
				if info.TaskQueue != "dynamic-activities-test" || info.HeartbeatTimeout > timeout {
					t.Errorf("%s queue/heartbeat: %+v", key, info)
				}
				return calls[key]
			}
			env.RegisterActivityWithOptions(func(ctx context.Context, params map[string]interface{}) (map[string]interface{}, error) {
				page := 1
				if params["cursor"] != nil {
					page = 2
				}
				if check(ctx, fmt.Sprintf("page%d", page), 17*time.Second, 2) == 1 {
					return nil, errors.New("retry fixture")
				}
				return map[string]interface{}{
					"outputRef": &model.DataRef{Type: "pg", Key: fmt.Sprint(page), TenantID: "tenant"},
					"hasMore":   page == 1, "recordCount": 1, "checkpoint": map[string]interface{}{"page": page},
				}, nil
			}, activity.RegisterOptions{Name: "fetchSourcePage"})
			env.RegisterActivityWithOptions(func(ctx context.Context, _ map[string]interface{}) (model.NodeResult, error) {
				check(ctx, "merge", 17*time.Second, 2)
				return model.NodeResult{Status: "success", OutputRef: &model.DataRef{Type: "pg", Key: "merged", TenantID: "tenant"}}, nil
			}, activity.RegisterOptions{Name: "mergeRefs"})
			env.RegisterActivityWithOptions(func(ctx context.Context, params map[string]interface{}) (model.NodeResult, error) {
				id := params["nodeId"].(string)
				if id == "sink" {
					check(ctx, id, 23*time.Second, 1)
					return model.NodeResult{}, errors.New("permanent fixture failure")
				}
				if check(ctx, id, 10*time.Minute, 5) < 5 {
					return model.NodeResult{}, errors.New("default retry fixture")
				}
				return model.NodeResult{NodeID: id, Status: "success"}, nil
			}, activity.RegisterOptions{Name: "dispatchNode"})
			env.RegisterActivityWithOptions(func(ctx context.Context, _ map[string]interface{}) error {
				check(ctx, "bookkeeping", 10*time.Minute, 5)
				return nil
			}, activity.RegisterOptions{Name: "markExecution"})
			sourceTimeout, sinkTimeout, sourceAttempts, sinkAttempts := 17, 23, 2, 1
			env.ExecuteWorkflow(DynamicDAGWorkflow, model.WorkflowInput{
				TenantID: "tenant", ExecutionID: "fixture", Environment: model.EnvironmentTest,
				Trigger: model.TriggerInput{Type: "manual"},
				Definition: model.PipelineDefinition{Nodes: []model.Node{
					{ID: "source", Type: "source", TimeoutSec: &sourceTimeout, Retry: &model.RetryConfig{MaximumAttempts: &sourceAttempts}},
					{ID: "default", Type: "transform"},
					{ID: "sink", Type: "sink", TimeoutSec: &sinkTimeout, Retry: &model.RetryConfig{MaximumAttempts: &sinkAttempts}},
				}, Edges: []model.Edge{{Source: "source", Target: "sink"}}},
			})
			if err := env.GetWorkflowError(); err != nil {
				t.Fatal(err)
			}
			var result model.ExecutionStatus
			if err := env.GetWorkflowResult(&result); err != nil {
				t.Fatal(err)
			}
			wantSinkCalls := 1
			if legacy {
				wantSinkCalls = 5
			}
			if result.Phase != "failed" || calls["page1"] != 2 || calls["page2"] != 2 || calls["merge"] != 1 || calls["default"] != 5 || calls["sink"] != wantSinkCalls || calls["bookkeeping"] != 1 {
				t.Fatalf("phase=%s, activity calls=%v", result.Phase, calls)
			}
		})
	}
}

func TestInvalidNodePolicyFailsBeforeScheduling(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	zero := 0
	env.ExecuteWorkflow(DynamicDAGWorkflow, model.WorkflowInput{Definition: model.PipelineDefinition{Nodes: []model.Node{{ID: "invalid", TimeoutSec: &zero}}}})
	if err := env.GetWorkflowError(); err == nil {
		t.Fatal("expected invalid policy failure before any activity")
	}
}
