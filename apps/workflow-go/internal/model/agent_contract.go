package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// ValidateAgentNodes validates the reserved contract without enabling execution.
// Runtime preparation must additionally verify version ownership, schemas and input bounds.
func ValidateAgentNodes(def PipelineDefinition) error {
	for _, node := range def.Nodes {
		if node.Type != "agent" && node.ActivityType != "agent.run" {
			continue
		}
		if node.Type != "agent" || node.ActivityType != "agent.run" {
			return fmt.Errorf("node %s agent type requires activityType agent.run", node.ID)
		}
		if def.Execution != nil && def.Execution.Engine != "" && def.Execution.Engine != "workflow" {
			return fmt.Errorf("node %s agent nodes require the workflow execution engine", node.ID)
		}
		body, err := json.Marshal(node.Config)
		if err != nil {
			return fmt.Errorf("node %s invalid agent config", node.ID)
		}
		// encoding/json matches struct tags case-insensitively. Validate the raw
		// object keys first so accepted maps also satisfy the TypeScript contract.
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(body, &fields); err != nil || len(fields) != 3 || fields["agentId"] == nil || fields["agentVersion"] == nil || fields["inputBinding"] == nil {
			return fmt.Errorf("node %s agent config requires exactly agentId, agentVersion and inputBinding", node.ID)
		}
		var bindingFields map[string]json.RawMessage
		if err := json.Unmarshal(fields["inputBinding"], &bindingFields); err != nil || len(bindingFields) != 3 || bindingFields["mode"] == nil || bindingFields["fields"] == nil || bindingFields["maxRecords"] == nil {
			return fmt.Errorf("node %s inputBinding requires exactly mode, fields and maxRecords", node.ID)
		}
		var config AgentNodeConfig
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&config); err != nil {
			return fmt.Errorf("node %s invalid agent config: %w", node.ID, err)
		}
		// Canonical lowercase form only, so one agent has one spelling in stored definitions.
		if _, err := uuid.Parse(config.AgentID); err != nil || len(config.AgentID) != 36 || config.AgentID != strings.ToLower(config.AgentID) {
			return fmt.Errorf("node %s agentId must be a canonical lowercase UUID", node.ID)
		}
		// JSON numbers cross TypeScript's number boundary before reaching Go.
		if config.AgentVersion <= 0 || config.AgentVersion > 9007199254740991 {
			return fmt.Errorf("node %s agentVersion must be a positive safe integer", node.ID)
		}
		binding := config.InputBinding
		if binding.Mode != "batch" || binding.MaxRecords < 1 || binding.MaxRecords > 100 {
			return fmt.Errorf("node %s inputBinding requires batch mode and maxRecords between 1 and 100", node.ID)
		}
		if len(binding.Fields) == 0 {
			return fmt.Errorf("node %s inputBinding.fields must be nonempty", node.ID)
		}
		seenFields := map[string]bool{}
		for _, field := range binding.Fields {
			if strings.TrimSpace(field) == "" || strings.ContainsAny(field, ".[]{}$\r\n\t") || seenFields[field] {
				return fmt.Errorf("node %s inputBinding.fields must be unique literal top-level keys", node.ID)
			}
			seenFields[field] = true
		}
		incoming := 0
		for _, edge := range def.Edges {
			if edge.Target != node.ID {
				continue
			}
			validSource := false
			for _, source := range def.Nodes {
				if source.ID == edge.Source && source.ID != node.ID {
					validSource = true
					break
				}
			}
			if !validSource {
				return fmt.Errorf("node %s incoming reference must identify another node", node.ID)
			}
			incoming++
		}
		if incoming != 1 {
			return fmt.Errorf("node %s agent nodes require exactly one incoming reference; use a merge for multiple predecessors", node.ID)
		}
	}
	return nil
}

// ValidateAgentAdmission fails closed until the B3 child workflow is implemented.
// Keep this gate separate from wire validation so fixtures can validate the contract.
func ValidateAgentAdmission(def PipelineDefinition) error {
	if err := ValidateAgentNodes(def); err != nil {
		return err
	}
	for _, node := range def.Nodes {
		if node.Type == "agent" {
			return fmt.Errorf("node %s: agent execution is not implemented", node.ID)
		}
	}
	return nil
}
