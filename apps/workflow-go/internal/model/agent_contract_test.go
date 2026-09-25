package model

import (
	"encoding/json"
	"os"
	"testing"
)

func agentFixture(t *testing.T) PipelineDefinition {
	t.Helper()
	body, err := os.ReadFile("../../../../tests/contracts/agent-pipeline.json")
	if err != nil {
		t.Fatal(err)
	}
	var def PipelineDefinition
	if err := json.Unmarshal(body, &def); err != nil {
		t.Fatal(err)
	}
	return def
}

func TestAgentContractWire(t *testing.T) {
	def := agentFixture(t)
	if err := ValidateAgentNodes(def); err != nil {
		t.Fatal(err)
	}
	if err := ValidateNodePolicies(def); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	var roundtrip PipelineDefinition
	if err := json.Unmarshal(encoded, &roundtrip); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAgentNodes(roundtrip); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAgentAdmission(def); err == nil {
		t.Fatal("reserved contract must not execute")
	}
	for _, engine := range []string{"", "workflow", "stream-direct", "spark-sql", "flink-sql"} {
		def.Execution.Engine = engine
		err := ValidateAgentNodes(def)
		if (err == nil) != (engine == "" || engine == "workflow") {
			t.Fatalf("engine %q: %v", engine, err)
		}
	}
}

func TestAgentContractRejectsMalformedNodes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*PipelineDefinition)
	}{
		{"wrong activity", func(d *PipelineDefinition) { d.Nodes[1].ActivityType = "http.fetch" }},
		{"disguised activity", func(d *PipelineDefinition) { d.Nodes[1].Type = "transform" }},
		{"missing input", func(d *PipelineDefinition) { d.Edges = d.Edges[1:] }},
		{"multiple inputs", func(d *PipelineDefinition) { d.Edges = append(d.Edges, d.Edges[0]) }},
		{"unknown input", func(d *PipelineDefinition) { d.Edges[0].Source = "missing" }},
		{"self input", func(d *PipelineDefinition) { d.Edges[0].Source = "classifyTickets" }},
		{"missing config", func(d *PipelineDefinition) { d.Nodes[1].Config = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			def := agentFixture(t)
			tc.change(&def)
			if err := ValidateAgentNodes(def); err == nil {
				t.Fatal("accepted malformed agent")
			}
		})
	}
	for _, tc := range []struct {
		key   string
		value interface{}
	}{
		{"agentId", "agt_triage"}, {"agentId", nil}, {"agentId", 42},
		{"agentId", "CBB40A16-242B-4D78-87DE-36A941635F48"}, {"agentId", "{cbb40a16-242b-4d78-87de-36a941635f48}"},
		{"agentVersion", 0}, {"agentVersion", -1}, {"agentVersion", 1.5}, {"agentVersion", "3"}, {"agentVersion", nil}, {"agentVersion", 9007199254740992.0},
		{"instructions", "secret instructions"}, {"model", "mutable-tag"}, {"credentials", map[string]interface{}{"token": "secret"}},
		{"inputBinding", nil}, {"inputBinding", "batch"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			def := agentFixture(t)
			def.Nodes[1].Config[tc.key] = tc.value
			if err := ValidateAgentNodes(def); err == nil {
				t.Fatalf("accepted %s=%v", tc.key, tc.value)
			}
		})
	}
	for _, tc := range []struct {
		key   string
		value interface{}
	}{
		{"mode", "record"}, {"mode", nil},
		{"maxRecords", 0}, {"maxRecords", -1}, {"maxRecords", 101}, {"maxRecords", 1.5}, {"maxRecords", "100"},
		{"fields", []string{}}, {"fields", nil}, {"fields", "subject"}, {"fields", []int{1}},
		{"fields", []string{"body", "body"}}, {"fields", []string{""}}, {"fields", []string{"  "}},
		{"fields", []string{"ticket.subject"}}, {"fields", []string{"ticket[0]"}}, {"fields", []string{"${subject}"}},
		{"expression", "r.subject"},
	} {
		t.Run("binding/"+tc.key, func(t *testing.T) {
			def := agentFixture(t)
			def.Nodes[1].Config["inputBinding"].(map[string]interface{})[tc.key] = tc.value
			if err := ValidateAgentNodes(def); err == nil {
				t.Fatalf("accepted %s=%v", tc.key, tc.value)
			}
		})
	}
}

func TestAgentAdmissionPreservesDataOnly(t *testing.T) {
	def := agentFixture(t)
	def.Nodes = append(def.Nodes[:1], def.Nodes[2:]...)
	def.Edges = []Edge{{ID: "data", Source: "tickets", Target: "output"}}
	if err := ValidateAgentAdmission(def); err != nil {
		t.Fatal(err)
	}
}

func TestAgentContractRequiresCanonicalKeys(t *testing.T) {
	for _, tc := range []struct {
		key, alias string
		binding    bool
	}{
		{"agentId", "agentid", false}, {"agentId", "AgentID", false},
		{"agentVersion", "agentversion", false}, {"inputBinding", "InputBinding", false},
		{"mode", "Mode", true}, {"fields", "Fields", true}, {"maxRecords", "maxrecords", true},
	} {
		for _, collision := range []bool{false, true} {
			name := tc.key + "/" + tc.alias
			if collision {
				name += "/collision"
			}
			t.Run(name, func(t *testing.T) {
				def := agentFixture(t)
				fields := def.Nodes[1].Config
				if tc.binding {
					fields = fields["inputBinding"].(map[string]interface{})
				}
				fields[tc.alias] = fields[tc.key]
				if !collision {
					delete(fields, tc.key)
				}
				// Exercise the same stored-definition map round trip as API admission.
				body, err := json.Marshal(def)
				if err != nil {
					t.Fatal(err)
				}
				var stored PipelineDefinition
				if err := json.Unmarshal(body, &stored); err != nil {
					t.Fatal(err)
				}
				if err := ValidateAgentNodes(stored); err == nil {
					t.Fatal("accepted noncanonical keys")
				}
			})
		}
	}
	for _, tc := range []struct {
		key     string
		binding bool
	}{
		{"agentId", false}, {"agentVersion", false}, {"inputBinding", false},
		{"mode", true}, {"fields", true}, {"maxRecords", true},
	} {
		t.Run("missing/"+tc.key, func(t *testing.T) {
			def := agentFixture(t)
			fields := def.Nodes[1].Config
			if tc.binding {
				fields = fields["inputBinding"].(map[string]interface{})
			}
			delete(fields, tc.key)
			if err := ValidateAgentNodes(def); err == nil {
				t.Fatal("accepted missing required key")
			}
		})
	}
}
