package api

import (
	"fmt"
	"strings"

	"github.com/dataflow-poc/workflow-go/internal/enterprise"
	"github.com/dataflow-poc/workflow-go/internal/model"
	"github.com/google/uuid"
)

func validatePipeline(def model.PipelineDefinition) error {
	if strings.TrimSpace(def.Name) == "" {
		return fmt.Errorf("pipeline name is required")
	}
	if len(def.Nodes) == 0 {
		return fmt.Errorf("pipeline must contain at least one node")
	}
	if def.Trigger.Type == "" {
		return fmt.Errorf("trigger.type is required")
	}
	if def.Execution != nil && !map[string]bool{"": true, "workflow": true, "stream-direct": true, "spark-sql": true, "flink-sql": true}[def.Execution.Engine] {
		return fmt.Errorf("unsupported execution engine %q", def.Execution.Engine)
	}
	if err := model.ValidateNodePolicies(def); err != nil {
		return err
	}
	validTypes := map[string]bool{"source": true, "transform": true, "sink": true, "fork": true, "merge": true}
	ids := map[string]bool{}
	indegree := map[string]int{}
	outgoing := map[string][]string{}
	for _, node := range def.Nodes {
		if node.ID == "" {
			return fmt.Errorf("node id is required")
		}
		if ids[node.ID] {
			return fmt.Errorf("duplicate node id %q", node.ID)
		}
		if !validTypes[node.Type] {
			return fmt.Errorf("node %s has invalid type %q", node.ID, node.Type)
		}
		if node.Type != "fork" && node.Type != "merge" && node.ActivityType == "" {
			return fmt.Errorf("node %s activityType is required", node.ID)
		}
		if layer, ok := node.Config["layer"]; ok {
			validLayers := map[string]bool{"bronze": true, "silver": true, "gold": true}
			if name, isString := layer.(string); !isString || !validLayers[name] {
				return fmt.Errorf("node %s has invalid layer %v (must be bronze, silver, or gold)", node.ID, layer)
			}
		}
		ids[node.ID] = true
		indegree[node.ID] = 0
	}
	for _, edge := range def.Edges {
		if !ids[edge.Source] || !ids[edge.Target] {
			return fmt.Errorf("edge %s references an unknown node", edge.ID)
		}
		indegree[edge.Target]++
		outgoing[edge.Source] = append(outgoing[edge.Source], edge.Target)
	}
	queue := []string{}
	for id, degree := range indegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}
	visited := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		visited++
		for _, target := range outgoing[id] {
			indegree[target]--
			if indegree[target] == 0 {
				queue = append(queue, target)
			}
		}
	}
	if visited != len(def.Nodes) {
		return fmt.Errorf("pipeline contains a cycle")
	}
	if def.Execution != nil && def.Execution.Engine == "stream-direct" {
		if err := validateStreamDirect(def); err != nil {
			return err
		}
	}
	if def.Execution != nil && def.Execution.Engine == "spark-sql" {
		if err := validateSparkSQL(def); err != nil {
			return err
		}
	}
	if def.Execution != nil && def.Execution.Engine == "flink-sql" {
		if err := validateFlinkSQL(def); err != nil {
			return err
		}
	}
	return nil
}

func validateFlinkSQL(def model.PipelineDefinition) error {
	if err := enterprise.ValidateFlinkSelect(def.Execution.TransformSQL); err != nil {
		return err
	}
	sources, sinks := 0, 0
	for _, node := range def.Nodes {
		if node.Type == "source" {
			sources++
			if node.ActivityType != "kafka.fetch" {
				return fmt.Errorf("flink-sql requires Kafka/CDC input")
			}
		}
		if node.Type == "sink" {
			sinks++
			if node.ActivityType != "sink.clickhouse" {
				return fmt.Errorf("flink-sql requires ClickHouse output")
			}
		}
		if node.Type == "transform" {
			return fmt.Errorf("flink-sql uses execution.transformSql instead of transform nodes")
		}
	}
	if sources != 1 || sinks != 1 {
		return fmt.Errorf("flink-sql requires one source and one sink")
	}
	return enterprise.ValidateFlinkDeployment(def, "validation")
}

func validateSparkSQL(def model.PipelineDefinition) error {
	if err := enterprise.ValidateSparkSelect(def.Execution.TransformSQL); err != nil {
		return err
	}
	sources, sinks := 0, 0
	for _, node := range def.Nodes {
		if node.Type == "source" {
			sources++
			if !map[string]bool{"s3.fetch": true, "iceberg.fetch": true}[node.ActivityType] {
				return fmt.Errorf("spark-sql does not support source %s", node.ActivityType)
			}
		}
		if node.Type == "sink" {
			sinks++
			if !map[string]bool{"sink.iceberg": true, "sink.clickhouse": true}[node.ActivityType] {
				return fmt.Errorf("spark-sql does not support sink %s", node.ActivityType)
			}
		}
		if node.Type == "transform" {
			return fmt.Errorf("spark-sql uses execution.transformSql instead of transform nodes")
		}
	}
	if sources != 1 || sinks != 1 {
		return fmt.Errorf("spark-sql requires one source and one sink")
	}
	return nil
}

func validateStreamDirect(def model.PipelineDefinition) error {
	allowed := map[string]bool{"kafka.fetch": true, "transform.map": true, "transform.filter": true, "transform.rename": true, "transform.parse": true, "transform.contract": true, "sink.postgres": true, "sink.mysql": true, "sink.mongodb": true, "sink.clickhouse": true}
	sources, sinks := 0, 0
	for _, node := range def.Nodes {
		if !allowed[node.ActivityType] {
			return fmt.Errorf("stream-direct does not support %s", node.ActivityType)
		}
		if node.Type == "source" {
			sources++
		}
		if node.Type == "sink" {
			sinks++
		}
	}
	if sources != 1 || sinks != 1 || len(def.Edges) != len(def.Nodes)-1 {
		return fmt.Errorf("stream-direct requires one linear source-to-sink graph")
	}
	return nil
}

func pipelineFeatures(def model.PipelineDefinition) map[string]bool {
	features := map[string]bool{}
	if def.Execution != nil && def.Execution.Engine == "stream-direct" {
		features["realtime"] = true
	}
	if def.Execution != nil && def.Execution.Engine == "spark-sql" {
		features["sparkSql"] = true
	}
	if def.Execution != nil && def.Execution.Engine == "flink-sql" {
		features["realtime"], features["flinkSql"] = true, true
	}
	for _, node := range def.Nodes {
		if node.ActivityType == "kafka.fetch" || node.ActivityType == "sink.kafka" || node.Config["syncMode"] == "cdc" || node.Config["writeMode"] == "apply-cdc" {
			features["realtime"] = true
		}
		if node.ActivityType == "transform.dedupe" && node.Config["scope"] == "pipeline" {
			features["statefulProcessing"] = true
		}
		if map[string]bool{"sftp.fetch": true, "sink.sftp": true, "snowflake.fetch": true, "sink.snowflake": true, "iceberg.fetch": true, "sink.iceberg": true}[node.ActivityType] {
			features["advancedConnectors"] = true
		}
	}
	return features
}

func pipelineID(value string) (string, error) {
	if value == "" {
		return uuid.NewString(), nil
	}
	if _, err := uuid.Parse(value); err != nil {
		return "", fmt.Errorf("pipeline id must be a UUID")
	}
	return value, nil
}
