package workflows

import (
	"context"
	"testing"

	"github.com/dataflow-poc/workflow-go/internal/model"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

func TestReplayV128History(t *testing.T) {
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(DynamicDAGWorkflow)
	if err := replayer.ReplayWorkflowHistoryFromJSONFile(nil, "testdata/dynamic_dag_v1_28.json"); err != nil {
		t.Fatal(err)
	}
}

func TestSourcePagesAreMergedBeforeDownstream(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.OnGetVersion("durable-execution-controls-v1", workflow.DefaultVersion, workflow.Version(1)).Return(workflow.DefaultVersion)
	env.RegisterActivityWithOptions(func(context.Context, map[string]interface{}) (map[string]interface{}, error) { return nil, nil }, activity.RegisterOptions{Name: "fetchSourcePage"})
	env.RegisterActivityWithOptions(func(context.Context, map[string]interface{}) (model.NodeResult, error) {
		return model.NodeResult{}, nil
	}, activity.RegisterOptions{Name: "mergeRefs"})
	env.RegisterActivityWithOptions(func(context.Context, map[string]interface{}) error { return nil }, activity.RegisterOptions{Name: "markExecution"})
	env.RegisterActivityWithOptions(func(context.Context, map[string]interface{}) error { return nil }, activity.RegisterOptions{Name: "commitSourceCursors"})
	env.OnActivity("fetchSourcePage", mock.Anything, mock.Anything).Return(map[string]interface{}{
		"outputRef": map[string]interface{}{"type": "pg", "key": "page-1", "tenantId": "tenant"},
		"hasMore":   true, "recordCount": 2, "checkpoint": map[string]interface{}{"value": "c1"},
	}, nil).Once()
	env.OnActivity("fetchSourcePage", mock.Anything, mock.MatchedBy(func(params map[string]interface{}) bool {
		cursor, ok := params["cursor"].(map[string]interface{})
		return ok && cursor["value"] == "c1"
	})).Return(map[string]interface{}{
		"outputRef": map[string]interface{}{"type": "pg", "key": "page-2", "tenantId": "tenant"},
		"hasMore":   false, "recordCount": 1, "checkpoint": map[string]interface{}{"value": "c2"},
	}, nil).Once()
	env.OnActivity("mergeRefs", mock.Anything, mock.Anything).Return(model.NodeResult{
		NodeID: "source", Status: "success", OutputRef: &model.DataRef{Type: "pg", Key: "merged", TenantID: "tenant"},
		Meta: map[string]interface{}{"recordCount": 3},
	}, nil).Once()
	env.OnActivity("markExecution", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("commitSourceCursors", mock.Anything, mock.Anything).Return(nil).Once()
	env.ExecuteWorkflow(DynamicDAGWorkflow, model.WorkflowInput{
		Definition: model.PipelineDefinition{ID: "pipeline", Nodes: []model.Node{{ID: "source", Type: "source", ActivityType: "postgres.fetch", Config: map[string]interface{}{}}}},
		TenantID:   "tenant", ExecutionID: "exec", Trigger: model.TriggerInput{Type: "manual"},
	})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	var result model.ExecutionStatus
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatal(err)
	}
	if got := result.NodeResults["source"].OutputRef.Key; got != "merged" {
		t.Fatalf("output ref = %s", got)
	}
	env.AssertExpectations(t)
}

func TestBuildPlanCreatesParallelLevels(t *testing.T) {
	nodes := []model.Node{
		{ID: "a"}, {ID: "b"}, {ID: "merge"}, {ID: "sink"},
	}
	edges := []model.Edge{
		{Source: "a", Target: "merge"},
		{Source: "b", Target: "merge"},
		{Source: "merge", Target: "sink"},
	}
	plan, err := buildPlan(nodes, edges)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Levels) != 3 {
		t.Fatalf("levels = %d", len(plan.Levels))
	}
	if len(plan.Levels[0]) != 2 {
		t.Fatalf("first level size = %d", len(plan.Levels[0]))
	}
}

func TestDedupeKeysCommitOnlyAfterWorkflowSuccess(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.OnGetVersion("durable-execution-controls-v1", workflow.DefaultVersion, workflow.Version(1)).Return(workflow.DefaultVersion)
	env.RegisterActivityWithOptions(func(context.Context, map[string]interface{}) (model.NodeResult, error) {
		return model.NodeResult{}, nil
	}, activity.RegisterOptions{Name: "dispatchNode"})
	env.RegisterActivityWithOptions(func(context.Context, map[string]interface{}) error { return nil }, activity.RegisterOptions{Name: "commitDedupeKeys"})
	env.RegisterActivityWithOptions(func(context.Context, map[string]interface{}) error { return nil }, activity.RegisterOptions{Name: "markExecution"})
	env.OnActivity("dispatchNode", mock.Anything, mock.Anything).Return(model.NodeResult{
		NodeID: "dedupe", Status: "success", Meta: map[string]interface{}{
			"dedupeCheckpoint": map[string]interface{}{"pipelineId": "pipeline-row", "nodeId": "dedupe", "hashes": []interface{}{"hash"}},
		},
	}, nil).Once()
	env.OnActivity("commitDedupeKeys", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("markExecution", mock.Anything, mock.Anything).Return(nil).Once()
	env.ExecuteWorkflow(DynamicDAGWorkflow, model.WorkflowInput{
		Definition: model.PipelineDefinition{ID: "pipeline", Nodes: []model.Node{{ID: "dedupe", Type: "transform", ActivityType: "transform.dedupe", Config: map[string]interface{}{}}}},
		TenantID:   "tenant", ExecutionID: "exec", Trigger: model.TriggerInput{Type: "manual"},
	})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	env.AssertExpectations(t)
}

func TestBuildPlanRejectsCycles(t *testing.T) {
	nodes := []model.Node{{ID: "a"}, {ID: "b"}}
	_, err := buildPlan(nodes, []model.Edge{
		{Source: "a", Target: "b"},
		{Source: "b", Target: "a"},
	})
	if err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestBuildPlanRejectsUnknownNodes(t *testing.T) {
	nodes := []model.Node{{ID: "a"}}
	_, err := buildPlan(nodes, []model.Edge{{Source: "a", Target: "missing"}})
	if err == nil {
		t.Fatal("expected unknown-node error")
	}
}

func TestSourceConnectionIDIsolatesBackfillPartition(t *testing.T) {
	node := model.Node{ID: "source", Config: map[string]interface{}{}, Ingestion: &model.IngestionConfig{StateKey: "backfill-7"}}
	if got := sourceConnectionID("pipeline", node); got != "pipeline:source:backfill-7" {
		t.Fatalf("connection id = %s", got)
	}
	node.Config["syncMode"] = "cdc"
	if got := sourceConnectionID("pipeline", node); got != "pipeline:source:cdc:backfill-7" {
		t.Fatalf("cdc connection id = %s", got)
	}
}
