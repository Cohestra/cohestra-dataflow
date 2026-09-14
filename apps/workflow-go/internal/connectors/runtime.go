package connectors

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/dataflow-poc/workflow-go/internal/config"
	"github.com/dataflow-poc/workflow-go/internal/database"
	"github.com/dataflow-poc/workflow-go/internal/model"
	"github.com/dataflow-poc/workflow-go/internal/objectstore"
	"github.com/dataflow-poc/workflow-go/internal/security"
)

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

type SourceParams struct {
	Config, Cursor map[string]interface{}
	Ingestion      *model.IngestionConfig
	TenantID       string
}
type SourceResult struct {
	Records    []interface{}
	NextCursor map[string]interface{}
	HasMore    bool
	LagRecords int64
}
type Source func(context.Context, SourceParams) (SourceResult, error)
type HandlerContext struct {
	TenantID, ExecutionID, NodeID string
	PipelineVersion               int
}
type Handler func(context.Context, interface{}, map[string]interface{}, HandlerContext) (interface{}, map[string]interface{}, error)

type Runtime struct {
	DB     *database.DB
	Store  *objectstore.Store
	Config config.Config
	HTTP   *http.Client
	// SafeHTTP is the shared client for outbound requests to user/tenant
	// supplied URLs (HTTP connector fetch/sink, ClickHouse sink connector
	// instances, alert destinations). It enforces HTTPS and re-validates
	// the resolved IP against the private/metadata denylist at dial time.
	// HTTP requests to platform-owned infra (e.g. the system ClickHouse or
	// SaaS OAuth endpoints) intentionally keep using HTTP, since those are
	// not attacker-influenced and may legitimately live on private ranges.
	SafeHTTP *http.Client
	Registry *Registry
	Sources  map[string]Source
	Handlers map[string]Handler
}

func NewRuntime(db *database.DB, store *objectstore.Store, cfg config.Config, httpClient *http.Client, registry *Registry) *Runtime {
	r := &Runtime{DB: db, Store: store, Config: cfg, HTTP: httpClient, SafeHTTP: security.NewHTTPClient(30 * time.Second), Registry: registry, Sources: map[string]Source{}, Handlers: map[string]Handler{}}
	r.registerTransforms()
	r.registerHTTP()
	r.registerDatabases()
	r.registerFiles()
	r.registerStreams()
	r.registerSaaS()
	return r
}
func (r *Runtime) Fetch(ctx context.Context, name string, p SourceParams) (SourceResult, error) {
	source := r.Sources[name]
	if source == nil {
		if manifest, ok := r.Registry.Manifests[name]; ok && manifest.Kind == "source" {
			return r.fetchManifest(ctx, manifest, p)
		}
		return SourceResult{}, fmt.Errorf("Unknown source: %s", name)
	}
	return source(ctx, p)
}
func (r *Runtime) Handle(ctx context.Context, name string, input interface{}, config map[string]interface{}, handlerCtx HandlerContext) (interface{}, map[string]interface{}, error) {
	handler := r.Handlers[name]
	if handler == nil {
		return nil, nil, fmt.Errorf("Unknown activity: %s", name)
	}
	return handler(ctx, input, config, handlerCtx)
}
