package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/dataflow-poc/workflow-go/internal/activities"
	"github.com/dataflow-poc/workflow-go/internal/codec"
	"github.com/dataflow-poc/workflow-go/internal/config"
	"github.com/dataflow-poc/workflow-go/internal/connectors"
	"github.com/dataflow-poc/workflow-go/internal/database"
	"github.com/dataflow-poc/workflow-go/internal/dispatchers"
	"github.com/dataflow-poc/workflow-go/internal/objectstore"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	ctx := context.Background()
	cfg := config.Load()
	db, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	store, err := objectstore.New(ctx, objectstore.Config{Bucket: cfg.PayloadBucket, Region: cfg.PayloadRegion, Endpoint: cfg.PayloadEndpoint, ForcePathStyle: cfg.PayloadForcePathStyle, AccessKeyID: cfg.PayloadAccessKeyID, SecretAccessKey: cfg.PayloadSecretAccessKey})
	if err != nil {
		slog.Error("object store initialization failed", "error", err)
		os.Exit(1)
	}
	platformKey, err := activities.DecodePlatformKey(cfg.TemporalPayloadEncryptionKey)
	if err != nil {
		slog.Error("payload key invalid", "error", err)
		os.Exit(1)
	}
	registry := connectors.Load(cfg.ConnectorsDir)
	runtime := connectors.NewRuntime(db, store, cfg, &http.Client{Timeout: 30 * time.Second}, registry)
	defer runtime.CloseConnectorPools()
	dataConverter, err := codec.NewDataConverterFromEnv()
	if err != nil {
		slog.Error("data converter failed", "error", err)
		os.Exit(1)
	}
	temporal, err := client.Dial(client.Options{HostPort: cfg.TemporalAddress, Namespace: cfg.TemporalNamespace, DataConverter: dataConverter})
	if err != nil {
		slog.Error("Temporal connection failed", "error", err)
		os.Exit(1)
	}
	defer temporal.Close()
	w := worker.New(temporal, cfg.TaskQueue, worker.Options{MaxConcurrentActivityExecutionSize: 20, MaxHeartbeatThrottleInterval: time.Second})
	activities.Register(w, &activities.Activities{DB: db, Payloads: &activities.Payloads{DB: db, Store: store, PlatformKey: platformKey, MaxPayloadBytes: cfg.MaxPayloadBytes}, Runtime: runtime, PrivateKeyPath: cfg.WorkerPrivateKeyPath, MaxMergeInMemoryBytes: cfg.MaxMergeInMemoryBytes})
	group, err := dispatchers.Start(ctx, db, runtime, cfg)
	if err != nil {
		slog.Error("dispatchers failed", "error", err)
		os.Exit(1)
	}
	defer group.Stop()
	group.StartExecutionControls(db, temporal, cfg.TemporalNamespace)
	slog.Info("activity worker started", "namespace", cfg.TemporalNamespace, "taskQueue", cfg.TaskQueue)
	if err = w.Run(worker.InterruptCh()); err != nil {
		slog.Error("activity worker stopped", "error", err)
		os.Exit(1)
	}
}
