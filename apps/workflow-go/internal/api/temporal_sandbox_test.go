package api

// These opt-in tests use real PostgreSQL, Temporal history, workers, activities,
// HTTP routing and payload persistence. Authentication context and deterministic
// connector boundaries are injected only here; no production transport policy
// or connector implementation is weakened.
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dataflow-poc/workflow-go/internal/activities"
	"github.com/dataflow-poc/workflow-go/internal/config"
	"github.com/dataflow-poc/workflow-go/internal/connectors"
	"github.com/dataflow-poc/workflow-go/internal/dispatchers"
	"github.com/dataflow-poc/workflow-go/internal/model"
	"github.com/dataflow-poc/workflow-go/internal/workflows"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"google.golang.org/protobuf/encoding/protojson"
)

type temporalSandbox struct {
	dispatchErrorMu   sync.Mutex
	lastDispatchError string
	t                 *testing.T
	fixture           controlFixture
	ctx               context.Context
	cancel            context.CancelFunc
	client            client.Client
	runtime           *connectors.Runtime
	payloads          *activities.Payloads
	activityWorker    worker.Worker
	workflowWorker    worker.Worker
	http              *httptest.Server
	run               client.WorkflowRun
	dispatchDone      chan struct{}
	summary           map[string]interface{}
}

func newTemporalSandbox(t *testing.T) *temporalSandbox {
	t.Helper()
	address, namespace := os.Getenv("SANDBOX_TEMPORAL_ADDRESS"), os.Getenv("SANDBOX_TEMPORAL_NAMESPACE")
	if address == "" || namespace == "" || os.Getenv("CONTROL_TEST_DATABASE_URL") == "" {
		t.Skip("real sandbox requires SANDBOX_TEMPORAL_ADDRESS, SANDBOX_TEMPORAL_NAMESPACE and CONTROL_TEST_DATABASE_URL")
	}
	f := newControlFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	b := &temporalSandbox{t: t, fixture: f, ctx: ctx, cancel: cancel, summary: map[string]interface{}{"test": t.Name(), "authContextInjected": true, "connectorBoundaryInjected": true, "realTemporal": true, "realPostgres": true}}
	t.Cleanup(b.close)
	c, err := client.DialContext(ctx, client.Options{HostPort: address, Namespace: namespace, Identity: "cohestra-synthetic-sandbox"})
	if err != nil {
		t.Fatal(err)
	}
	b.client = c
	// The bytes are a public synthetic test key, never an environment credential.
	b.payloads = &activities.Payloads{DB: f.db, PlatformKey: bytes.Repeat([]byte{0x42}, 32), MaxPayloadBytes: 1 << 20}
	b.runtime = connectors.NewRuntime(f.db, nil, config.Config{}, &http.Client{Timeout: 5 * time.Second}, &connectors.Registry{Manifests: map[string]model.ConnectorManifest{}})
	s := &Server{DB: f.app, Temporal: map[string]client.Client{"test": c}, Config: config.Config{}}
	mux := http.NewServeMux()
	mux.Handle("POST /api/pipelines/{rowId}/run", s.pipelineAccess("editor", handle(s.pipelineRun)))
	mux.HandleFunc("POST /api/executions/{id}/{action}", handle(s.executionSignal))
	mux.HandleFunc("GET /api/executions/{id}/status", handle(s.executionStatus))
	actor := model.TenantContext{TenantID: f.tenant, UserID: f.user, Role: "owner", EmailVerified: true}
	b.http = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Deliberate auth injection: route/ACL/RLS behavior is real; JWT/OIDC login
		// is outside these Temporal scenarios and covered separately.
		mux.ServeHTTP(w, withTenant(r, actor))
	}))
	b.dispatchDone = make(chan struct{})
	go func() {
		defer close(b.dispatchDone)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				callCtx, stop := context.WithTimeout(ctx, 10*time.Second)
				err := dispatchers.DispatchExecutionControls(callCtx, f.db, c, "test")
				b.dispatchErrorMu.Lock()
				if err != nil {
					b.lastDispatchError = err.Error()
				} else {
					b.lastDispatchError = ""
				}
				b.dispatchErrorMu.Unlock()
				stop()
			}
		}
	}()
	return b
}

func (b *temporalSandbox) startWorkers() {
	b.t.Helper()
	// SDK sticky cache is shared across workers in this process. Disable it
	// before starting any real worker so restart must reconstruct from history.
	worker.SetStickyWorkflowCacheSize(0)
	b.activityWorker = worker.New(b.client, "dynamic-activities-test", worker.Options{DisableWorkflowWorker: true, Identity: "synthetic-activity-worker", MaxConcurrentActivityExecutionSize: 4, MaxHeartbeatThrottleInterval: time.Second, WorkerStopTimeout: 2 * time.Second})
	activities.Register(b.activityWorker, &activities.Activities{DB: b.fixture.db, Payloads: b.payloads, Runtime: b.runtime})
	if err := b.activityWorker.Start(); err != nil {
		b.t.Fatal(err)
	}
	b.startWorkflowWorker()
}

func (b *temporalSandbox) startWorkflowWorker() {
	b.workflowWorker = worker.New(b.client, "dynamic-dag-test", worker.Options{LocalActivityWorkerOnly: true, Identity: "synthetic-workflow-worker-" + uuid.NewString(), WorkerStopTimeout: time.Second, StickyScheduleToStartTimeout: time.Second})
	b.workflowWorker.RegisterWorkflow(workflows.DynamicDAGWorkflow)
	if err := b.workflowWorker.Start(); err != nil {
		b.t.Fatal(err)
	}
}

func (b *temporalSandbox) close() {
	// On assertion failure, terminate before stopping workers and before the
	// fixture's tenant cleanup. Capture only synthetic workflow evidence.
	if b.client != nil && b.run != nil {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		desc, err := b.client.DescribeWorkflowExecution(cleanup, b.run.GetID(), b.run.GetRunID())
		if err == nil && desc.WorkflowExecutionInfo.Status == enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING {
			_ = b.client.TerminateWorkflow(cleanup, b.run.GetID(), b.run.GetRunID(), "synthetic sandbox cleanup")
		}
		b.writeArtifacts(cleanup)
		stop()
	}
	b.cancel()
	if b.dispatchDone != nil {
		select {
		case <-b.dispatchDone:
		case <-time.After(3 * time.Second):
			b.t.Error("sandbox dispatcher did not stop")
		}
	}
	if b.workflowWorker != nil {
		b.workflowWorker.Stop()
	}
	if b.activityWorker != nil {
		b.activityWorker.Stop()
	}
	if b.http != nil {
		b.http.Close()
	}
	if b.runtime != nil {
		b.runtime.CloseConnectorPools()
	}
	if b.run != nil {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		if _, err := b.fixture.db.Pool.Exec(cleanup, `DELETE FROM node_payloads WHERE tenant_id=$1 AND execution_id=$2`, b.fixture.tenant, b.run.GetID()); err != nil {
			b.t.Error(err)
		}
		stop()
	}
	if b.client != nil {
		b.client.Close()
	}
}

func (b *temporalSandbox) request(method, path string) map[string]interface{} {
	b.t.Helper()
	ctx, stop := context.WithTimeout(b.ctx, 10*time.Second)
	defer stop()
	req, err := http.NewRequestWithContext(ctx, method, b.http.URL+path, strings.NewReader("{}"))
	if err != nil {
		b.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := b.http.Client().Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		b.t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		b.t.Fatalf("%s %s: %d %s", method, path, res.StatusCode, body)
	}
	var out map[string]interface{}
	if err = json.Unmarshal(body, &out); err != nil {
		b.t.Fatal(err)
	}
	return out
}

func (b *temporalSandbox) start(nodes []model.Node, edges ...model.Edge) {
	b.t.Helper()
	def := model.PipelineDefinition{ID: b.fixture.pipeline, Version: 1, TenantID: b.fixture.tenant, Name: "synthetic sandbox", Trigger: model.Trigger{Type: "manual"}, Nodes: nodes, Edges: edges}
	if err := validatePipeline(def); err != nil {
		b.t.Fatal(err)
	}
	// The golden definition is stored before the actual API run handler loads it.
	err := b.fixture.app.TenantTx(b.ctx, b.fixture.tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(b.ctx, `UPDATE pipelines SET definition=$3 WHERE tenant_id=$1 AND id=$2`, b.fixture.tenant, b.fixture.pipeline, def)
		return err
	})
	if err != nil {
		b.t.Fatal(err)
	}
	b.startWorkers()
	response := b.request(http.MethodPost, "/api/pipelines/"+b.fixture.pipeline+"/run")
	id, ok := response["executionId"].(string)
	if !ok || id == "" {
		b.t.Fatalf("run response=%v", response)
	}
	var workflowID, runID string
	if err := b.fixture.db.Pool.QueryRow(b.ctx, `SELECT workflow_id,run_id FROM executions WHERE tenant_id=$1 AND id=$2`, b.fixture.tenant, id).Scan(&workflowID, &runID); err != nil {
		b.t.Fatal(err)
	}
	b.run = b.client.GetWorkflow(b.ctx, workflowID, runID)
	b.summary["executionId"] = id
}

func (b *temporalSandbox) control(action string) {
	b.t.Helper()
	response := b.request(http.MethodPost, "/api/executions/"+b.run.GetID()+"/"+action)
	if response["ok"] != true {
		b.t.Fatalf("control response=%v", response)
	}
	revision, ok := response["controlRevision"].(float64)
	if !ok || revision < 1 {
		b.t.Fatalf("control response=%v", response)
	}
	b.waitControlDelivery(action, int64(revision))
}

func (b *temporalSandbox) waitControlDelivery(action string, revision int64) {
	b.t.Helper()
	ctx, stop := context.WithTimeout(b.ctx, 10*time.Second)
	defer stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var delivered int64
	signalSeen := false
	for {
		err := b.fixture.db.Pool.QueryRow(ctx, `SELECT control_delivered_revision FROM executions WHERE tenant_id=$1 AND id=$2`, b.fixture.tenant, b.run.GetID()).Scan(&delivered)
		if err == nil && delivered >= revision {
			history, historyErr := b.history(ctx)
			if historyErr == nil {
				for _, event := range history.Events {
					attrs := event.GetWorkflowExecutionSignaledEventAttributes()
					if attrs == nil || attrs.SignalName != action {
						continue
					}
					var hint model.ExecutionControl
					if converter.GetDefaultDataConverter().FromPayloads(attrs.Input, &hint) == nil && hint.Revision == revision {
						signalSeen = true
						break
					}
				}
			}
		}
		if delivered >= revision && signalSeen {
			b.summary[action+"DeliveredRevision"] = revision
			return
		}
		select {
		case <-ctx.Done():
			b.dispatchErrorMu.Lock()
			lastError := b.lastDispatchError
			b.dispatchErrorMu.Unlock()
			b.t.Fatalf("%s delivery did not reach real Temporal within10s: requested=%d delivered=%d signaled=%v dispatcherError=%s", action, revision, delivered, signalSeen, lastError)
		case <-ticker.C:
		}
	}
}

func (b *temporalSandbox) waitFor(label string, condition func() bool) {
	b.t.Helper()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-b.ctx.Done():
			b.t.Fatalf("waiting for %s: %v", label, b.ctx.Err())
		case <-ticker.C:
		}
	}
}

func (b *temporalSandbox) waitSignal(ch <-chan struct{}, label string) {
	b.t.Helper()
	select {
	case <-ch:
	case <-b.ctx.Done():
		b.t.Fatalf("waiting for %s: %v", label, b.ctx.Err())
	}
}

func (b *temporalSandbox) result(want string) model.ExecutionStatus {
	b.t.Helper()
	var result model.ExecutionStatus
	if err := b.run.Get(b.ctx, &result); err != nil {
		b.t.Fatal(err)
	}
	if result.Phase != want {
		b.t.Fatalf("phase=%s want=%s nodes=%+v", result.Phase, want, result.NodeResults)
	}
	var phase string
	if err := b.fixture.db.Pool.QueryRow(b.ctx, `SELECT phase FROM executions WHERE tenant_id=$1 AND id=$2`, b.fixture.tenant, b.run.GetID()).Scan(&phase); err != nil {
		b.t.Fatal(err)
	}
	if phase != want {
		b.t.Fatalf("stored phase=%s want=%s", phase, want)
	}
	b.summary["phase"] = phase
	return result
}

func (b *temporalSandbox) history(ctx context.Context) (*historypb.History, error) {
	history := &historypb.History{}
	it := b.client.GetWorkflowHistory(ctx, b.run.GetID(), b.run.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for it.HasNext() {
		event, err := it.Next()
		if err != nil {
			return nil, err
		}
		history.Events = append(history.Events, event)
	}
	return history, nil
}

func (b *temporalSandbox) writeArtifacts(ctx context.Context) {
	dir := os.Getenv("SANDBOX_ARTIFACT_DIR")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		b.t.Error(err)
		return
	}
	name := strings.ReplaceAll(b.t.Name(), "/", "_")
	history, err := b.history(ctx)
	if err != nil {
		b.t.Error(err)
		return
	}
	body, err := protojson.MarshalOptions{Indent: "  "}.Marshal(history)
	if err != nil {
		b.t.Error(err)
		return
	}
	if err = os.WriteFile(filepath.Join(dir, name+".history.json"), body, 0600); err != nil {
		b.t.Error(err)
	}
	b.summary["historyEvents"] = len(history.Events)
	b.summary["testFailed"] = b.t.Failed()
	body, err = json.MarshalIndent(b.summary, "", "  ")
	if err != nil {
		b.t.Error(err)
		return
	}
	if err = os.WriteFile(filepath.Join(dir, name+".summary.json"), body, 0600); err != nil {
		b.t.Error(err)
	}
}

func TestTemporalSandboxStoredGoldenOutput(t *testing.T) {
	b := newTemporalSandbox(t)
	padding := strings.Repeat("synthetic-", 350)
	input := []interface{}{map[string]interface{}{"id": float64(1), "name": "alpha", "padding": padding}, map[string]interface{}{"id": float64(2), "name": "beta", "padding": padding}}
	b.runtime.Sources["sandbox.fetch"] = func(context.Context, connectors.SourceParams) (connectors.SourceResult, error) {
		return connectors.SourceResult{Records: input}, nil
	}
	var sinkCalls atomic.Int32
	b.runtime.Handlers["sandbox.store"] = func(_ context.Context, input interface{}, _ map[string]interface{}, _ connectors.HandlerContext) (interface{}, map[string]interface{}, error) {
		sinkCalls.Add(1)
		return input, nil, nil
	}
	b.start([]model.Node{
		{ID: "source", Type: "source", ActivityType: "sandbox.fetch"},
		{ID: "map", Type: "transform", ActivityType: "transform.map", Config: map[string]interface{}{"expression": "{id: r.id, name: upper(r.name), padding: r.padding}"}},
		{ID: "sink", Type: "sink", ActivityType: "sandbox.store"},
	}, model.Edge{Source: "source", Target: "map"}, model.Edge{Source: "map", Target: "sink"})
	result := b.result("completed")
	ref := result.NodeResults["sink"].OutputRef
	if ref == nil || ref.Type != "pg" || !ref.Encrypted {
		t.Fatalf("sink output was not encrypted PostgreSQL payload: %+v", ref)
	}
	actual, err := b.payloads.Read(b.ctx, ref, nil)
	if err != nil {
		t.Fatal(err)
	}
	expected := []interface{}{map[string]interface{}{"id": float64(1), "name": "ALPHA", "padding": padding}, map[string]interface{}{"id": float64(2), "name": "BETA", "padding": padding}}
	if !reflect.DeepEqual(actual, expected) || sinkCalls.Load() != 1 {
		t.Fatalf("golden output mismatch or sink calls=%d", sinkCalls.Load())
	}
	var count int
	if err = b.fixture.db.Pool.QueryRow(b.ctx, `SELECT count(*) FROM node_payloads WHERE tenant_id=$1 AND execution_id=$2 AND encrypted`, b.fixture.tenant, b.run.GetID()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("stored source/map/sink payloads=%d", count)
	}
	b.summary["gates"] = []string{"S01"}
	b.summary["goldenMatched"] = true
	b.summary["storedPayloads"] = count
	b.summary["sinkCalls"] = sinkCalls.Load()
}

func TestTemporalSandboxRetryAndTimeout(t *testing.T) {
	t.Run("retry", func(t *testing.T) {
		b := newTemporalSandbox(t)
		var calls atomic.Int32
		var mu sync.Mutex
		attempts := []int32{}
		b.runtime.Handlers["sandbox.retry"] = func(ctx context.Context, _ interface{}, _ map[string]interface{}, _ connectors.HandlerContext) (interface{}, map[string]interface{}, error) {
			info := activity.GetInfo(ctx)
			mu.Lock()
			attempts = append(attempts, info.Attempt)
			mu.Unlock()
			if calls.Add(1) < 3 {
				return nil, nil, errors.New("synthetic retryable failure")
			}
			return []interface{}{map[string]interface{}{"ok": true}}, nil, nil
		}
		limit, timeout := 3, 5
		b.start([]model.Node{{ID: "retry", Type: "transform", ActivityType: "sandbox.retry", TimeoutSec: &timeout, Retry: &model.RetryConfig{MaximumAttempts: &limit}}})
		b.result("completed")
		mu.Lock()
		got := append([]int32(nil), attempts...)
		mu.Unlock()
		if !reflect.DeepEqual(got, []int32{1, 2, 3}) {
			t.Fatalf("server retry attempts=%v", got)
		}
		b.summary["gates"] = []string{"S03"}
		b.summary["actualAttempts"] = got
	})
	t.Run("timeout", func(t *testing.T) {
		b := newTemporalSandbox(t)
		var calls atomic.Int32
		bodyStopped := make(chan struct{})
		b.runtime.Handlers["sandbox.timeout"] = func(ctx context.Context, _ interface{}, _ map[string]interface{}, _ connectors.HandlerContext) (interface{}, map[string]interface{}, error) {
			calls.Add(1)
			defer close(bodyStopped)
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-b.ctx.Done():
				return nil, nil, b.ctx.Err()
			}
		}
		limit, timeout := 1, 1
		b.start([]model.Node{{ID: "timeout", Type: "transform", ActivityType: "sandbox.timeout", TimeoutSec: &timeout, Retry: &model.RetryConfig{MaximumAttempts: &limit}}})
		b.result("failed")
		b.waitSignal(bodyStopped, "timed-out activity context")
		history, err := b.history(b.ctx)
		if err != nil {
			t.Fatal(err)
		}
		timedOut := false
		for _, event := range history.Events {
			if attrs := event.GetActivityTaskTimedOutEventAttributes(); attrs != nil && attrs.Failure.GetTimeoutFailureInfo().GetTimeoutType() == enumspb.TIMEOUT_TYPE_START_TO_CLOSE {
				timedOut = true
			}
		}
		if !timedOut || calls.Load() != 1 {
			t.Fatalf("real start-to-close event=%v calls=%d", timedOut, calls.Load())
		}
		b.summary["gates"] = []string{"S03"}
		b.summary["startToCloseTimedOut"] = true
		b.summary["actualAttempts"] = calls.Load()
	})
}

func TestTemporalSandboxPausePagesAndRestartWorkflowWorker(t *testing.T) {
	b := newTemporalSandbox(t)
	var pages, sinks atomic.Int32
	firstPage := make(chan struct{})
	releasePage := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releasePage) })
	b.runtime.Sources["sandbox.pages"] = func(ctx context.Context, _ connectors.SourceParams) (connectors.SourceResult, error) {
		page := pages.Add(1)
		if page == 1 {
			close(firstPage)
			select {
			case <-releasePage:
			case <-ctx.Done():
				return connectors.SourceResult{}, ctx.Err()
			case <-b.ctx.Done():
				return connectors.SourceResult{}, b.ctx.Err()
			}
		}
		return connectors.SourceResult{Records: []interface{}{map[string]interface{}{"page": page}}, NextCursor: map[string]interface{}{"page": page}, HasMore: page == 1}, nil
	}
	b.runtime.Handlers["sandbox.store"] = func(_ context.Context, input interface{}, _ map[string]interface{}, _ connectors.HandlerContext) (interface{}, map[string]interface{}, error) {
		sinks.Add(1)
		return input, nil, nil
	}
	b.start([]model.Node{{ID: "source", Type: "source", ActivityType: "sandbox.pages"}, {ID: "sink", Type: "sink", ActivityType: "sandbox.store"}}, model.Edge{Source: "source", Target: "sink"})
	b.waitSignal(firstPage, "first page start")
	b.control("pause")
	b.waitFor("workflow observes pause", func() bool {
		return b.request(http.MethodGet, "/api/executions/"+b.run.GetID()+"/status")["controlState"] == "paused"
	})
	releaseOnce.Do(func() { close(releasePage) })
	b.waitFor("first page persisted", func() bool {
		var count int
		err := b.fixture.db.Pool.QueryRow(b.ctx, `SELECT count(*) FROM node_runs WHERE tenant_id=$1 AND execution_id=$2 AND node_id='source' AND status='success'`, b.fixture.tenant, b.run.GetID()).Scan(&count)
		return err == nil && count == 1
	})
	runIDBeforeRestart := b.run.GetRunID()
	b.workflowWorker.Stop()
	b.workflowWorker = nil
	if pages.Load() != 1 || sinks.Load() != 0 {
		t.Fatalf("work escaped pause: pages=%d sinks=%d", pages.Load(), sinks.Load())
	}
	b.startWorkflowWorker()
	b.waitFor("restarted workflow remains paused", func() bool {
		return b.request(http.MethodGet, "/api/executions/"+b.run.GetID()+"/status")["controlState"] == "paused"
	})
	if pages.Load() != 1 || sinks.Load() != 0 {
		t.Fatal("worker restart admitted paused work")
	}
	b.control("resume")
	result := b.result("completed")
	if pages.Load() != 2 || sinks.Load() != 1 || result.NodeResults["sink"].OutputRef.RecordCount != 2 {
		t.Fatalf("pages=%d sinks=%d result=%+v", pages.Load(), sinks.Load(), result)
	}
	b.summary["gates"] = []string{"S11", "S13"}
	desc, err := b.client.DescribeWorkflowExecution(b.ctx, b.run.GetID(), b.run.GetRunID())
	if err != nil {
		t.Fatal(err)
	}
	if desc.WorkflowExecutionInfo.Execution.RunId != runIDBeforeRestart {
		t.Fatal("worker restart changed the workflow run")
	}
	b.summary["sameRunAfterRestart"] = true
	b.summary["stickyCacheDisabled"] = true
	b.summary["restartMode"] = "graceful workflow-worker stop/start; activity worker remains running"
	b.summary["workflowWorkerRestarts"] = 1
	b.summary["sourcePages"] = pages.Load()
	b.summary["sinkCalls"] = sinks.Load()
}

func TestTemporalSandboxHeartbeatCancelPreventsSink(t *testing.T) {
	b := newTemporalSandbox(t)
	started := make(chan struct{})
	cancelled := make(chan struct{})
	var sinks atomic.Int32
	b.runtime.Handlers["sandbox.block"] = func(ctx context.Context, _ interface{}, _ map[string]interface{}, _ connectors.HandlerContext) (interface{}, map[string]interface{}, error) {
		close(started)
		select {
		case <-ctx.Done():
			close(cancelled)
			return nil, nil, ctx.Err()
		case <-b.ctx.Done():
			return nil, nil, b.ctx.Err()
		}
	}
	b.runtime.Handlers["sandbox.store"] = func(context.Context, interface{}, map[string]interface{}, connectors.HandlerContext) (interface{}, map[string]interface{}, error) {
		sinks.Add(1)
		return nil, nil, nil
	}
	b.start([]model.Node{{ID: "work", Type: "transform", ActivityType: "sandbox.block"}, {ID: "sink", Type: "sink", ActivityType: "sandbox.store"}}, model.Edge{Source: "work", Target: "sink"})
	b.waitSignal(started, "activity start")
	s := &Server{DB: b.fixture.app, Temporal: map[string]client.Client{"test": b.client}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/executions/{id}/{action}", handle(s.executionSignal))
	cross := model.TenantContext{TenantID: uuid.NewString(), UserID: b.fixture.user, Role: "owner", EmailVerified: true}
	crossHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { mux.ServeHTTP(w, withTenant(r, cross)) }))
	defer crossHTTP.Close()
	req, err := http.NewRequestWithContext(b.ctx, http.MethodPost, crossHTTP.URL+"/api/executions/"+b.run.GetID()+"/cancel", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	denied, err := crossHTTP.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	denied.Body.Close()
	if denied.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant control status=%d", denied.StatusCode)
	}
	b.summary["httpCrossTenantControlDenied"] = true
	b.control("cancel")
	b.result("cancelled")
	// Unlike the SDK test environment, this requires the actual worker's
	// heartbeat RPC to cancel the admitted connector's Go context.
	b.waitSignal(cancelled, "real heartbeat cancellation")
	if sinks.Load() != 0 {
		t.Fatalf("sink calls after cancel=%d", sinks.Load())
	}
	b.summary["gates"] = []string{"S13"}
	b.summary["realActivityContextCancelled"] = true
	b.summary["sinkCalls"] = 0
}

func TestTemporalSandboxRejectsUnsupportedAgentAdmission(t *testing.T) {
	b := newTemporalSandbox(t)
	b.startWorkers()
	id := "sandbox-agent-denial-" + uuid.NewString()
	def := model.PipelineDefinition{Nodes: []model.Node{
		{ID: "source", Type: "source", ActivityType: "sandbox.fetch"},
		{ID: "agent", Type: "agent", ActivityType: "agent.run", Config: map[string]interface{}{"agentId": "7c466825-23de-46a9-8ed5-df95f22ed45b", "agentVersion": 1, "inputBinding": map[string]interface{}{"mode": "batch", "fields": []string{"id"}, "maxRecords": 100}}},
	}, Edges: []model.Edge{{Source: "source", Target: "agent"}}}
	if err := model.ValidateAgentNodes(def); err != nil {
		t.Fatalf("agent fixture is not valid: %v", err)
	}
	run, err := b.client.ExecuteWorkflow(b.ctx, client.StartWorkflowOptions{ID: id, TaskQueue: "dynamic-dag-test", WorkflowExecutionTimeout: time.Minute}, workflows.DynamicDAGWorkflow, model.WorkflowInput{ExecutionID: id, TenantID: b.fixture.tenant, Environment: model.EnvironmentTest, Definition: def})
	if err != nil {
		t.Fatal(err)
	}
	b.run = run
	err = run.Get(b.ctx, nil)
	var admissionError *temporal.ApplicationError
	if !errors.As(err, &admissionError) || admissionError.Type() != "AgentAdmissionRejected" || !strings.Contains(admissionError.Message(), "agent execution is not implemented") {
		t.Fatalf("expected runtime admission denial for valid agent contract, got %v", err)
	}
	history, err := b.history(b.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range history.Events {
		if event.GetActivityTaskScheduledEventAttributes() != nil {
			t.Fatal("unsupported agent scheduled an activity before rejection")
		}
	}
	b.summary["gates"] = []string{"S01-admission"}
	b.summary["unsupportedAgentDeniedBeforeActivity"] = true
}
