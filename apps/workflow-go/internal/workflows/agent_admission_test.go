package workflows

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/dataflow-poc/workflow-go/internal/model"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

func TestAgentAdmissionRejectsBeforeAnyActivity(t *testing.T) {
	body, err := os.ReadFile("../../../../tests/contracts/agent-pipeline.json")
	if err != nil {
		t.Fatal(err)
	}
	var definition model.PipelineDefinition
	if err := json.Unmarshal(body, &definition); err != nil {
		t.Fatal(err)
	}
	for _, trigger := range []string{"manual", "cron"} {
		t.Run(trigger, func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			// No activities are registered: even scheduled preparation must not run.
			env.ExecuteWorkflow(DynamicDAGWorkflow, model.WorkflowInput{
				Definition: definition, Trigger: model.TriggerInput{Type: trigger}, PipelineRowID: "fixture",
			})
			var applicationError *temporal.ApplicationError
			if err := env.GetWorkflowError(); !errors.As(err, &applicationError) || applicationError.Type() != "AgentAdmissionRejected" || !applicationError.NonRetryable() {
				t.Fatalf("expected non-retryable agent admission rejection; got %v", err)
			}
		})
	}
}
