package workflows

import (
	"context"
	"testing"

	"github.com/dataflow-poc/workflow-go/internal/model"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type pageCapRun struct {
	result    model.ExecutionStatus
	committed []map[string]interface{}
}

func runPageCapWorkflow(t *testing.T, capVersion workflow.Version, ingestion *model.IngestionConfig) pageCapRun {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.OnGetVersion("durable-execution-controls-v1", workflow.DefaultVersion, workflow.Version(1)).Return(workflow.DefaultVersion)
	env.OnGetVersion("source-page-cap-resume-v1", workflow.DefaultVersion, workflow.Version(1)).Return(capVersion)
	env.RegisterActivityWithOptions(func(context.Context, map[string]interface{}) (map[string]interface{}, error) { return nil, nil }, activity.RegisterOptions{Name: "fetchSourcePage"})
	env.RegisterActivityWithOptions(func(context.Context, map[string]interface{}) (model.NodeResult, error) {
		return model.NodeResult{}, nil
	}, activity.RegisterOptions{Name: "mergeRefs"})
	env.RegisterActivityWithOptions(func(context.Context, map[string]interface{}) error { return nil }, activity.RegisterOptions{Name: "markExecution"})
	env.RegisterActivityWithOptions(func(context.Context, map[string]interface{}) error { return nil }, activity.RegisterOptions{Name: "commitSourceCursors"})
	page := 0
	env.OnActivity("fetchSourcePage", mock.Anything, mock.Anything).Return(func(_ context.Context, _ map[string]interface{}) (map[string]interface{}, error) {
		page++
		return map[string]interface{}{
			"outputRef": map[string]interface{}{"type": "pg", "key": "page", "tenantId": "tenant"},
			"hasMore":   true, "recordCount": 10, "checkpoint": map[string]interface{}{"value": float64(page)},
		}, nil
	})
	env.OnActivity("mergeRefs", mock.Anything, mock.Anything).Return(model.NodeResult{
		NodeID: "source", Status: "success", OutputRef: &model.DataRef{Type: "pg", Key: "merged", TenantID: "tenant"},
	}, nil).Maybe()
	env.OnActivity("markExecution", mock.Anything, mock.Anything).Return(nil)
	var run pageCapRun
	env.OnActivity("commitSourceCursors", mock.Anything, mock.Anything).Return(func(_ context.Context, params map[string]interface{}) error {
		for _, cursor := range params["cursors"].([]interface{}) {
			run.committed = append(run.committed, cursor.(map[string]interface{}))
		}
		return nil
	}).Maybe()
	env.ExecuteWorkflow(DynamicDAGWorkflow, model.WorkflowInput{
		Definition: model.PipelineDefinition{ID: "pipeline", Nodes: []model.Node{{ID: "source", Type: "source", ActivityType: "postgres.fetch", Config: map[string]interface{}{}, Ingestion: ingestion}}},
		TenantID:   "tenant", ExecutionID: "exec", Trigger: model.TriggerInput{Type: "manual"},
	})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	if err := env.GetWorkflowResult(&run.result); err != nil {
		t.Fatal(err)
	}
	return run
}

func TestResumableSourceStopsAtPageCapAndCommitsCursor(t *testing.T) {
	run := runPageCapWorkflow(t, workflow.Version(1), nil)
	source := run.result.NodeResults["source"]
	if run.result.Phase != "completed" || source.Status != "success" {
		t.Fatalf("phase = %s, source = %s (%s)", run.result.Phase, source.Status, source.Error)
	}
	if source.Meta["truncated"] != true || source.Meta["warning"] == nil {
		t.Fatalf("truncation not surfaced: %#v", source.Meta)
	}
	if len(run.committed) != 1 {
		t.Fatalf("committed cursors = %#v", run.committed)
	}
	checkpoint := run.committed[0]["checkpoint"].(map[string]interface{})
	if checkpoint["value"] != float64(maxSourcePagesPerRun) {
		t.Fatalf("committed checkpoint = %#v, want the last fetched page", checkpoint)
	}
}

func TestPageCapStillFailsForOldHistoriesAndBackfills(t *testing.T) {
	for name, test := range map[string]struct {
		version   workflow.Version
		ingestion *model.IngestionConfig
	}{
		"history before resume": {version: workflow.DefaultVersion},
		"backfill partition":    {version: workflow.Version(1), ingestion: &model.IngestionConfig{Mode: "backfill", StateKey: "partition-1"}},
	} {
		t.Run(name, func(t *testing.T) {
			run := runPageCapWorkflow(t, test.version, test.ingestion)
			if source := run.result.NodeResults["source"]; source.Status != "failed" {
				t.Fatalf("source status = %s, want failed", source.Status)
			}
			if len(run.committed) != 0 {
				t.Fatalf("failed run committed cursors: %#v", run.committed)
			}
		})
	}
}
