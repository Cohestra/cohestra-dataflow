package api

import (
	"context"
	"strings"
	"testing"

	"github.com/dataflow-poc/workflow-go/internal/enterprise"
	"github.com/dataflow-poc/workflow-go/internal/model"
)

func TestInvalidNodePolicyRejectedBeforeAdmission(t *testing.T) {
	zero := 0
	def := model.PipelineDefinition{Name: "invalid policy", Trigger: model.Trigger{Type: "cron", Schedule: "0 * * * *"},
		Nodes: []model.Node{{ID: "source", Type: "source", ActivityType: "http.fetch", TimeoutSec: &zero}}}
	if err := validatePipeline(def); err == nil || !strings.Contains(err.Error(), "timeoutSec") {
		t.Fatalf("save validation: %v", err)
	}
	// No DB or Temporal client: validation must precede quota use and scheduling.
	s := &Server{}
	if _, err := s.fireExecution(context.Background(), def, "fixture", "manual", model.EnvironmentTest, nil, "", "", ""); err == nil || !strings.Contains(err.Error(), "timeoutSec") {
		t.Fatalf("execution validation: %v", err)
	}
	if err := s.syncSchedule(context.Background(), def, "fixture", model.EnvironmentTest); err == nil || !strings.Contains(err.Error(), "timeoutSec") {
		t.Fatalf("schedule validation: %v", err)
	}
}

func TestStreamDirectValidationAndEntitlement(t *testing.T) {
	def := model.PipelineDefinition{Name: "cdc", Trigger: model.Trigger{Type: "manual"}, Execution: &model.ExecutionConfig{Engine: "stream-direct"}, Nodes: []model.Node{
		{ID: "source", Type: "source", ActivityType: "kafka.fetch"},
		{ID: "sink", Type: "sink", ActivityType: "sink.clickhouse"},
	}, Edges: []model.Edge{{ID: "edge", Source: "source", Target: "sink"}}}
	if err := validatePipeline(def); err != nil {
		t.Fatal(err)
	}
	if !pipelineFeatures(def)["realtime"] {
		t.Fatal("stream-direct must require realtime")
	}
	def.Nodes[1].ActivityType = "sink.iceberg"
	if err := validatePipeline(def); err == nil {
		t.Fatal("expected unsupported sink error")
	}
	def.Execution.Engine = "unknown"
	if err := validatePipeline(def); err == nil {
		t.Fatal("expected unsupported engine error")
	}
}

func TestValidatePipelineRejectsInvalidLakehouseLayer(t *testing.T) {
	def := model.PipelineDefinition{Name: "layered", Trigger: model.Trigger{Type: "manual"}, Nodes: []model.Node{
		{ID: "source", Type: "source", ActivityType: "s3.fetch", Config: map[string]interface{}{"layer": "bronze"}},
		{ID: "sink", Type: "sink", ActivityType: "sink.clickhouse", Config: map[string]interface{}{"layer": "platinum"}},
	}, Edges: []model.Edge{{ID: "edge", Source: "source", Target: "sink"}}}
	if err := validatePipeline(def); err == nil {
		t.Fatal("expected invalid layer error")
	}
	def.Nodes[1].Config["layer"] = "gold"
	if err := validatePipeline(def); err != nil {
		t.Fatalf("expected valid lakehouse layers, got %v", err)
	}
}

func TestSparkSQLValidationAndEntitlement(t *testing.T) {
	if !enterprise.Build {
		t.Skip("requires the enterprise build (-tags ee)")
	}
	def := model.PipelineDefinition{Name: "spark", Trigger: model.Trigger{Type: "manual"}, Execution: &model.ExecutionConfig{Engine: "spark-sql", TransformSQL: "SELECT id FROM source"}, Nodes: []model.Node{
		{ID: "source", Type: "source", ActivityType: "s3.fetch"}, {ID: "sink", Type: "sink", ActivityType: "sink.iceberg"},
	}, Edges: []model.Edge{{ID: "edge", Source: "source", Target: "sink"}}}
	if err := validatePipeline(def); err != nil {
		t.Fatal(err)
	}
	if !pipelineFeatures(def)["sparkSql"] {
		t.Fatal("spark-sql must require sparkSql")
	}
	def.Execution.TransformSQL = "DROP TABLE source"
	if err := validatePipeline(def); err == nil {
		t.Fatal("expected unsafe SQL error")
	}
}

func TestFlinkSQLValidationAndEntitlements(t *testing.T) {
	if !enterprise.Build {
		t.Skip("requires the enterprise build (-tags ee)")
	}
	columns := []interface{}{map[string]interface{}{"name": "id", "type": "BIGINT"}}
	def := model.PipelineDefinition{Name: "flink", Trigger: model.Trigger{Type: "manual"}, Execution: &model.ExecutionConfig{Engine: "flink-sql", TransformSQL: "SELECT id FROM source"}, Nodes: []model.Node{{ID: "source", Type: "source", ActivityType: "kafka.fetch", Config: map[string]interface{}{"topic": "db.orders", "columns": columns}}, {ID: "sink", Type: "sink", ActivityType: "sink.clickhouse", Config: map[string]interface{}{"collection": "orders", "columns": columns}}}, Edges: []model.Edge{{ID: "edge", Source: "source", Target: "sink"}}}
	if err := validatePipeline(def); err != nil {
		t.Fatal(err)
	}
	features := pipelineFeatures(def)
	if !features["realtime"] || !features["flinkSql"] {
		t.Fatal("flink-sql entitlements missing")
	}
}
