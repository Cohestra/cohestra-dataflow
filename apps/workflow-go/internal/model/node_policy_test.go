package model

import (
	"encoding/json"
	"testing"
)

func TestNodePolicyWireValidation(t *testing.T) {
	for _, tc := range []struct {
		name, fields, engine string
		valid                bool
	}{
		{"omitted", `{}`, "", true},
		{"empty retry", `{"retry":{}}`, "workflow", true},
		{"null optional fields", `{"timeoutSec":null,"retry":null}`, "workflow", true},
		{"bounded overrides", `{"timeoutSec":17,"retry":{"maximumAttempts":1}}`, "workflow", true},
		{"zero timeout", `{"timeoutSec":0}`, "", false},
		{"negative timeout", `{"timeoutSec":-1}`, "", false},
		{"overflow timeout", `{"timeoutSec":9223372037}`, "", false},
		{"fractional timeout", `{"timeoutSec":1.5}`, "", false},
		{"unlimited attempts", `{"retry":{"maximumAttempts":0}}`, "", false},
		{"negative attempts", `{"retry":{"maximumAttempts":-1}}`, "", false},
		{"overflow attempts", `{"retry":{"maximumAttempts":2147483648}}`, "", false},
		{"stream policy", `{"timeoutSec":1}`, "stream-direct", false},
		{"spark policy", `{"retry":{"maximumAttempts":1}}`, "spark-sql", false},
		{"flink policy", `{"timeoutSec":1}`, "flink-sql", false},
		{"stream defaults", `{}`, "stream-direct", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var node Node
			err := json.Unmarshal([]byte(tc.fields), &node)
			if err == nil {
				node.ID = "fixture"
				err = ValidateNodePolicies(PipelineDefinition{Nodes: []Node{node}, Execution: &ExecutionConfig{Engine: tc.engine}})
			}
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}
