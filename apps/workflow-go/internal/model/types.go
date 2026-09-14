package model

import "encoding/json"

type Environment string

const (
	EnvironmentTest Environment = "test"
	EnvironmentProd Environment = "prod"
)

type PipelineDefinition struct {
	ID            string                      `json:"id"`
	Version       int                         `json:"version"`
	Name          string                      `json:"name"`
	TenantID      string                      `json:"tenantId"`
	Trigger       Trigger                     `json:"trigger"`
	Nodes         []Node                      `json:"nodes"`
	Edges         []Edge                      `json:"edges"`
	Concurrency   *Concurrency                `json:"concurrency,omitempty"`
	Metadata      *PipelineMetadata           `json:"metadata,omitempty"`
	SLO           *PipelineSLO                `json:"slo,omitempty"`
	Notifications *PipelineNotificationPolicy `json:"notifications,omitempty"`
	Execution     *ExecutionConfig            `json:"execution,omitempty"`
}

type ExecutionConfig struct {
	Engine       string `json:"engine,omitempty"`
	TransformSQL string `json:"transformSql,omitempty"`
}

type Trigger struct {
	Type     string `json:"type"`
	Schedule string `json:"schedule,omitempty"`
	Path     string `json:"path,omitempty"`
	Secret   string `json:"secret,omitempty"`
	Topic    string `json:"topic,omitempty"`
	AssetURN string `json:"assetUrn,omitempty"`
}

type Concurrency struct {
	MaxParallelNodes int `json:"maxParallelNodes,omitempty"`
}

type PipelineMetadata struct {
	Owner  string   `json:"owner,omitempty"`
	Domain string   `json:"domain,omitempty"`
	Tags   []string `json:"tags,omitempty"`
}

type PipelineSLO struct {
	FreshnessMinutes      *float64 `json:"freshnessMinutes,omitempty"`
	MaxFailureRatePercent *float64 `json:"maxFailureRatePercent,omitempty"`
	MaxDurationMS         *float64 `json:"maxDurationMs,omitempty"`
}

type PipelineNotificationPolicy struct {
	ConnectionID    string `json:"connectionId,omitempty"`
	MinimumSeverity string `json:"minimumSeverity,omitempty"`
}

type Node struct {
	ID            string                 `json:"id"`
	Type          string                 `json:"type"`
	ActivityType  string                 `json:"activityType"`
	Label         string                 `json:"label,omitempty"`
	Config        map[string]interface{} `json:"config"`
	Ingestion     *IngestionConfig       `json:"ingestion,omitempty"`
	TimeoutSec    *int                   `json:"timeoutSec,omitempty"`
	Retry         *RetryConfig           `json:"retry,omitempty"`
	MergeStrategy string                 `json:"mergeStrategy,omitempty"`
	JoinKey       string                 `json:"joinKey,omitempty"`
	InputAssets   []DataAssetRef         `json:"inputAssets,omitempty"`
	OutputAssets  []DataAssetRef         `json:"outputAssets,omitempty"`
}

// AgentNodeConfig pins an immutable definition; it contains no executable instructions or secrets.
type AgentNodeConfig struct {
	AgentID      string            `json:"agentId"`
	AgentVersion int64             `json:"agentVersion"`
	InputBinding AgentInputBinding `json:"inputBinding"`
}

type AgentInputBinding struct {
	Mode       string   `json:"mode"`
	Fields     []string `json:"fields"`
	MaxRecords int      `json:"maxRecords"`
}

type DataAssetRef struct {
	URN       string                 `json:"urn"`
	Platform  string                 `json:"platform"`
	Namespace string                 `json:"namespace"`
	Name      string                 `json:"name"`
	Type      string                 `json:"type"`
	Layer     string                 `json:"layer,omitempty"`
	Schema    map[string]interface{} `json:"schema,omitempty"`
	Owner     string                 `json:"owner,omitempty"`
	Tags      []string               `json:"tags,omitempty"`
}

type IngestionConfig struct {
	Mode          string `json:"mode"`
	BackfillStart string `json:"backfillStart,omitempty"`
	BackfillEnd   string `json:"backfillEnd,omitempty"`
	StateKey      string `json:"stateKey,omitempty"`
	PageSize      int    `json:"pageSize,omitempty"`
}

type RetryConfig struct {
	MaximumAttempts *int `json:"maximumAttempts,omitempty"`
}

type Edge struct {
	ID        string `json:"id"`
	Source    string `json:"source"`
	Target    string `json:"target"`
	Condition string `json:"condition,omitempty"`
}

type DataRef struct {
	Type        string `json:"type"`
	Key         string `json:"key"`
	Bucket      string `json:"bucket,omitempty"`
	TenantID    string `json:"tenantId"`
	SizeBytes   int    `json:"sizeBytes"`
	RecordCount int    `json:"recordCount,omitempty"`
	Encrypted   bool   `json:"encrypted,omitempty"`
	IV          string `json:"iv,omitempty"`
}

type NodeResult struct {
	NodeID    string                 `json:"nodeId"`
	Status    string                 `json:"status"`
	OutputRef *DataRef               `json:"outputRef,omitempty"`
	Meta      map[string]interface{} `json:"meta"`
	Error     string                 `json:"error,omitempty"`
}

type ExecutionStatus struct {
	ExecutionID string                `json:"executionId"`
	Phase       string                `json:"phase"`
	NodeResults map[string]NodeResult `json:"nodeResults"`
	StartedAt   string                `json:"startedAt"`
	CompletedAt string                `json:"completedAt,omitempty"`
	Stream      *StreamStatus         `json:"stream,omitempty"`
}

type StreamStatus struct {
	Batches          int     `json:"batches"`
	Records          int     `json:"records"`
	Errors           int     `json:"errors"`
	LagRecords       int64   `json:"lagRecords"`
	ThroughputPerSec float64 `json:"throughputPerSec"`
	LastHeartbeat    string  `json:"lastHeartbeat"`
}

type TriggerInput struct {
	Type       string   `json:"type"`
	PayloadRef *DataRef `json:"payloadRef,omitempty"`
	FiredAt    string   `json:"firedAt"`
}

type WorkflowInput struct {
	Definition        PipelineDefinition  `json:"definition"`
	TenantID          string              `json:"tenantId"`
	ExecutionID       string              `json:"executionId"`
	PipelineRowID     string              `json:"pipelineRowId,omitempty"`
	Environment       Environment         `json:"environment,omitempty"`
	ExecutionPrepared bool                `json:"executionPrepared,omitempty"`
	Trigger           TriggerInput        `json:"trigger"`
	TraceID           string              `json:"traceId,omitempty"`
	EncryptedDEK      string              `json:"encryptedDek,omitempty"`
	DEKIV             string              `json:"dekIv,omitempty"`
	Stream            *StreamStatus       `json:"stream,omitempty"`
	Flink             *FlinkWorkflowState `json:"flink,omitempty"`
}

type FlinkWorkflowState struct{ CohestraID, DesiredState, LastError, Checkpoint string }

type TenantContext struct {
	TenantID      string `json:"tenantId"`
	UserID        string `json:"userId"`
	Email         string `json:"email"`
	Role          string `json:"role"`
	EmailVerified bool   `json:"emailVerified"`
}

type CatalogField struct {
	Key         string        `json:"key"`
	Label       string        `json:"label"`
	Type        string        `json:"type"`
	Required    bool          `json:"required,omitempty"`
	Placeholder string        `json:"placeholder,omitempty"`
	Options     []interface{} `json:"options,omitempty"`
}

type CatalogEntry struct {
	ActivityType      string         `json:"activityType"`
	NodeType          string         `json:"nodeType"`
	Label             string         `json:"label"`
	Color             string         `json:"color"`
	SupportsIngestion bool           `json:"supportsIngestion,omitempty"`
	Fields            []CatalogField `json:"fields,omitempty"`
}

type ConnectorManifest struct {
	ActivityType      string                 `json:"activityType"`
	Label             string                 `json:"label"`
	Kind              string                 `json:"kind"`
	Color             string                 `json:"color,omitempty"`
	SupportsIngestion *bool                  `json:"supportsIngestion,omitempty"`
	URL               string                 `json:"url"`
	Method            string                 `json:"method,omitempty"`
	RecordsPath       string                 `json:"recordsPath,omitempty"`
	Headers           map[string]string      `json:"headers,omitempty"`
	Auth              map[string]interface{} `json:"auth,omitempty"`
	Pagination        map[string]interface{} `json:"pagination,omitempty"`
	Incremental       map[string]interface{} `json:"incremental,omitempty"`
	Fields            []CatalogField         `json:"fields,omitempty"`
}

// JSONMap preserves the loose JSON contracts used by connectors without
// inventing a second schema language.
type JSONMap map[string]interface{}

func CloneJSON[T any](value T) (T, error) {
	var cloned T
	b, err := json.Marshal(value)
	if err != nil {
		return cloned, err
	}
	err = json.Unmarshal(b, &cloned)
	return cloned, err
}
