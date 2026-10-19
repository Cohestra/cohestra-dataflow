package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	APIPort                      string
	AppURL                       string
	DatabaseURL                  string
	AppDatabaseURL               string
	RedisURL                     string
	TemporalAddress              string
	TemporalNamespace            string
	TaskQueue                    string
	JWTAccessSecret              string
	SMTPHost                     string
	SMTPPort                     string
	SMTPFrom                     string
	SMTPUser                     string
	SMTPPass                     string
	ClickHouseURL                string
	ClickHouseUser               string
	ClickHousePassword           string
	ClickHouseDB                 string
	ConnectorsDir                string
	PayloadBucket                string
	PayloadRegion                string
	PayloadEndpoint              string
	PayloadForcePathStyle        bool
	PayloadAccessKeyID           string
	PayloadSecretAccessKey       string
	TemporalPayloadEncryptionKey string
	OAuthTokenEncryptionKey      string
	WorkerPrivateKeyPath         string
	OpenLineageURL               string
	OpenLineageAPIKey            string
	Edition                      string
	InternalDemoFeatures         bool
	// Production is NODE_ENV=production (the Helm chart's runtime.production).
	Production bool
	// AppDatabaseURLSet records whether APP_DATABASE_URL was given explicitly;
	// without it the API falls back to DATABASE_URL and RLS does not apply.
	AppDatabaseURLSet bool
	// MailConfigured is true when SMTP_HOST or SMTP_USER is set, so the API
	// can send invitation and verification email.
	MailConfigured bool
	// BillingEnabled turns on the monthly execution quota and paid top-ups.
	// Off by default: self-hosted installs are never metered.
	BillingEnabled           bool
	AuditRetentionDays       int
	ExecutionRetentionDays   int
	NodeRunRetentionDays     int
	PayloadRetentionDays     int
	OutboxRetentionDays      int
	BackfillDispatchInterval time.Duration
	MaxPipelineListPageSize  int
	MaxPipelineNodes         int
	MaxPipelineEdges         int
	MaxPipelineConfigBytes   int
	MaxPipelineFanout        int
	MaxPipelineMaxParallel   int
	MaxPipelineNodePageSize  int
	MaxBackfillPartitions    int
	MaxPayloadBytes          int64
	MaxMergeInMemoryBytes    int64
}

func Load() Config {
	return Config{
		APIPort:                      env("API_PORT", "4000"),
		AppURL:                       env("APP_URL", "http://localhost:3000"),
		DatabaseURL:                  env("DATABASE_URL", "postgres://dataflow:dataflow@localhost:5432/dataflow"),
		AppDatabaseURL:               env("APP_DATABASE_URL", env("DATABASE_URL", "postgres://dataflow:dataflow@localhost:5432/dataflow")),
		RedisURL:                     env("REDIS_URL", "redis://localhost:6379"),
		TemporalAddress:              env("TEMPORAL_ADDRESS", "localhost:7233"),
		TemporalNamespace:            env("TEMPORAL_NAMESPACE", "default"),
		TaskQueue:                    env("TASK_QUEUE", "dynamic-activities-test"),
		JWTAccessSecret:              env("JWT_ACCESS_SECRET", DefaultJWTAccessSecret),
		SMTPHost:                     env("SMTP_HOST", "email-smtp.us-east-1.amazonaws.com"),
		SMTPPort:                     env("SMTP_PORT", "465"),
		SMTPFrom:                     env("SMTP_FROM", "noreply@dataflow.local"),
		SMTPUser:                     os.Getenv("SMTP_USER"),
		SMTPPass:                     os.Getenv("SMTP_PASS"),
		ClickHouseURL:                env("CLICKHOUSE_URL", "http://localhost:8123"),
		ClickHouseUser:               env("CLICKHOUSE_USER", "dataflow"),
		ClickHousePassword:           env("CLICKHOUSE_PASSWORD", "dataflow"),
		ClickHouseDB:                 env("CLICKHOUSE_DB", "dataflow"),
		ConnectorsDir:                env("CONNECTORS_DIR", "/app/connectors/manifests"),
		PayloadBucket:                os.Getenv("PAYLOAD_S3_BUCKET"),
		PayloadRegion:                env("PAYLOAD_S3_REGION", "us-east-1"),
		PayloadEndpoint:              os.Getenv("PAYLOAD_S3_ENDPOINT"),
		PayloadForcePathStyle:        boolEnv("PAYLOAD_S3_FORCE_PATH_STYLE"),
		PayloadAccessKeyID:           os.Getenv("PAYLOAD_S3_ACCESS_KEY_ID"),
		PayloadSecretAccessKey:       os.Getenv("PAYLOAD_S3_SECRET_ACCESS_KEY"),
		TemporalPayloadEncryptionKey: os.Getenv("TEMPORAL_PAYLOAD_ENCRYPTION_KEY"),
		OAuthTokenEncryptionKey:      os.Getenv("OAUTH_TOKEN_ENCRYPTION_KEY"),
		WorkerPrivateKeyPath:         os.Getenv("WORKER_PRIVATE_KEY_PATH"),
		OpenLineageURL:               os.Getenv("OPENLINEAGE_URL"),
		OpenLineageAPIKey:            os.Getenv("OPENLINEAGE_API_KEY"),
		Edition:                      env("EDITION", "community"),
		InternalDemoFeatures:         boolEnv("INTERNAL_DEMO_FEATURES"),
		BillingEnabled:               boolEnv("BILLING_ENABLED"),
		Production:                   os.Getenv("NODE_ENV") == "production",
		AppDatabaseURLSet:            os.Getenv("APP_DATABASE_URL") != "",
		MailConfigured:               os.Getenv("SMTP_HOST") != "" || os.Getenv("SMTP_USER") != "",
		AuditRetentionDays:           intEnv("AUDIT_RETENTION_DAYS", 90),
		ExecutionRetentionDays:       intEnv("EXECUTION_RETENTION_DAYS", 90),
		NodeRunRetentionDays:         intEnv("NODE_RUN_RETENTION_DAYS", 90),
		PayloadRetentionDays:         intEnv("PAYLOAD_RETENTION_DAYS", 30),
		OutboxRetentionDays:          intEnv("OUTBOX_RETENTION_DAYS", 30),
		BackfillDispatchInterval:     time.Duration(intEnv("BACKFILL_DISPATCH_INTERVAL_MS", 5000)) * time.Millisecond,
		MaxPipelineListPageSize:      intEnv("MAX_PIPELINE_LIST_PAGE_SIZE", 200),
		MaxPipelineNodes:             intEnv("MAX_PIPELINE_NODES", 500),
		MaxPipelineEdges:             intEnv("MAX_PIPELINE_EDGES", 1000),
		MaxPipelineConfigBytes:       intEnv("MAX_PIPELINE_CONFIG_BYTES", 65536),
		MaxPipelineFanout:            intEnv("MAX_FANOUT", 50),
		MaxPipelineMaxParallel:       intEnv("MAX_PIPELINE_MAX_PARALLEL", 100),
		MaxPipelineNodePageSize:      intEnv("MAX_PIPELINE_NODE_PAGE_SIZE", 100000),
		MaxBackfillPartitions:        intEnv("MAX_BACKFILL_PARTITIONS", 10000),
		MaxPayloadBytes:              int64(intEnv("MAX_PAYLOAD_BYTES", 10*1024*1024)),
		MaxMergeInMemoryBytes:        int64(intEnv("MAX_MERGE_INMEMORY_BYTES", 50*1024*1024)),
	}
}

// DefaultJWTAccessSecret is the development fallback; production refuses it.
const DefaultJWTAccessSecret = "dev-access-secret-change-me"

// ValidateAPI checks settings the API must not run with. In production any
// problem is an error; in development it is returned as a warning to log.
func (c Config) ValidateAPI() (warnings []string, err error) {
	var problems []string
	if c.JWTAccessSecret == DefaultJWTAccessSecret || len(c.JWTAccessSecret) < 32 {
		problems = append(problems, "JWT_ACCESS_SECRET must be a random value of at least 32 characters, not the development default")
	}
	if !c.AppDatabaseURLSet || c.AppDatabaseURL == c.DatabaseURL {
		problems = append(problems, "APP_DATABASE_URL must name the RLS-bound dataflow_app role; without it tenant row-level security is not enforced")
	}
	if c.Production {
		if len(problems) > 0 {
			return nil, fmt.Errorf("refusing to start in production: %s", strings.Join(problems, "; "))
		}
		return nil, nil
	}
	return problems, nil
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func boolEnv(name string) bool {
	value, _ := strconv.ParseBool(strings.TrimSpace(os.Getenv(name)))
	return value
}

func intEnv(name string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil {
		return fallback
	}
	return value
}
