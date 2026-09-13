package model

import (
	"fmt"
	"math"
	"time"
)

// ValidateNodePolicies rejects policies that would be ignored, overflow a
// duration, or select Temporal's unlimited-retry sentinel (zero).
func ValidateNodePolicies(def PipelineDefinition) error {
	for _, node := range def.Nodes {
		if node.TimeoutSec != nil && (*node.TimeoutSec <= 0 || int64(*node.TimeoutSec) > math.MaxInt64/int64(time.Second)) {
			return fmt.Errorf("node %s timeoutSec must be an integer between 1 and %d", node.ID, math.MaxInt64/int64(time.Second))
		}
		if node.Retry != nil && node.Retry.MaximumAttempts != nil && (*node.Retry.MaximumAttempts <= 0 || int64(*node.Retry.MaximumAttempts) > math.MaxInt32) {
			return fmt.Errorf("node %s retry.maximumAttempts must be an integer between 1 and %d", node.ID, math.MaxInt32)
		}
		hasPolicy := node.TimeoutSec != nil || (node.Retry != nil && node.Retry.MaximumAttempts != nil)
		if hasPolicy && def.Execution != nil && def.Execution.Engine != "" && def.Execution.Engine != "workflow" {
			return fmt.Errorf("node %s timeoutSec and retry.maximumAttempts are supported only by the workflow execution engine", node.ID)
		}
	}
	return nil
}
