package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dataflow-poc/workflow-go/internal/connectors"
	"github.com/dataflow-poc/workflow-go/internal/model"
)

type capturedOllamaRequest struct {
	Format   interface{}              `json:"format"`
	Think    bool                     `json:"think"`
	Options  map[string]interface{}   `json:"options"`
	Messages []map[string]interface{} `json:"messages"`
}

func fakeOllama(t *testing.T, responses ...string) (*httptest.Server, *[]capturedOllamaRequest) {
	t.Helper()
	requests := []capturedOllamaRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request capturedOllamaRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode Ollama request: %v", err)
		}
		requests = append(requests, request)
		if len(requests) > len(responses) {
			http.Error(w, "unexpected extra request", http.StatusInternalServerError)
			return
		}
		response := responses[len(requests)-1]
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"message": map[string]string{"content": response}})
	}))
	return server, &requests
}

func TestOllamaJSONSendsSchemaAndDeterministicOptions(t *testing.T) {
	server, requests := fakeOllama(t, `{}`)
	defer server.Close()
	t.Setenv("OLLAMA_URL", server.URL)

	schema := aiPipelineJSONSchema([]string{"http.fetch", "sink.records"})
	var target map[string]interface{}
	api := &Server{HTTP: server.Client()}
	if err := api.ollamaJSON(httptest.NewRequest(http.MethodPost, "/", nil), "system", "user", schema, &target); err != nil {
		t.Fatal(err)
	}
	if len(*requests) != 1 {
		t.Fatalf("requests = %d", len(*requests))
	}
	format, ok := (*requests)[0].Format.(map[string]interface{})
	if !ok || format["type"] != "object" {
		t.Fatalf("format is not a JSON Schema: %#v", (*requests)[0].Format)
	}
	properties := schema["properties"].(map[string]interface{})
	if properties["execution"] == nil {
		t.Fatal("schema omitted execution")
	}
	nodeProperties := properties["nodes"].(map[string]interface{})["items"].(map[string]interface{})["properties"].(map[string]interface{})
	if nodeProperties["ingestion"] == nil || nodeProperties["id"].(map[string]interface{})["pattern"] != "^[A-Za-z][A-Za-z0-9_-]*$" {
		t.Fatalf("node schema omitted ingestion or safe existing IDs: %#v", nodeProperties)
	}
	ingestionProperties := nodeProperties["ingestion"].(map[string]interface{})["properties"].(map[string]interface{})
	modes, err := json.Marshal(ingestionProperties["mode"].(map[string]interface{})["enum"])
	if err != nil || string(modes) != `["incremental","backfill","realtime"]` {
		t.Fatalf("AI ingestion modes must match the shared pipeline contract: %s (%v)", modes, err)
	}
	if (*requests)[0].Options["temperature"] != float64(0) || (*requests)[0].Options["seed"] != float64(42) || (*requests)[0].Options["num_ctx"] != float64(4096) {
		t.Fatalf("options = %#v", (*requests)[0].Options)
	}
	if (*requests)[0].Think {
		t.Fatal("thinking must default off for bounded interactive latency")
	}
}

func TestBuildPipelinePreservesExecutionIngestionAndExistingID(t *testing.T) {
	response := `{
		"status":"ready","reason":"","suggestedName":"Incremental orders","questions":[],"assumptions":[],"warnings":[],
		"trigger":{"type":"manual"},"execution":{"engine":"workflow"},
		"nodes":[
			{"id":"nN-1234","label":"Orders API","activityType":"http.fetch","config":{"url":"https://example.test/orders"},"ingestion":{"mode":"incremental","stateKey":"orders-cursor"}},
			{"id":"sink_1","label":"Store","activityType":"sink.records","config":{"collection":"orders"}}
		],"edges":[{"source":"nN-1234","target":"sink_1"}]
	}`
	server, _ := fakeOllama(t, response)
	defer server.Close()
	t.Setenv("OLLAMA_URL", server.URL)

	result, err := (&Server{HTTP: server.Client()}).buildPipeline(httptest.NewRequest(http.MethodPost, "/", nil), "Preserve my incremental pipeline")
	if err != nil {
		t.Fatal(err)
	}
	definition := result["definition"].(map[string]interface{})
	if definition["execution"].(*model.ExecutionConfig).Engine != "workflow" {
		t.Fatalf("execution = %#v", definition["execution"])
	}
	nodes := definition["nodes"].([]model.Node)
	if nodes[0].ID != "nN-1234" || nodes[0].Ingestion == nil || nodes[0].Ingestion.Mode != "incremental" || nodes[0].Ingestion.StateKey != "orders-cursor" {
		t.Fatalf("nodes = %#v", nodes)
	}
}

func TestBuildPipelineReturnsNeedsInputWithoutRepair(t *testing.T) {
	server, requests := fakeOllama(t, `{
		"status":"needs_input","suggestedName":"","questions":["Which S3 bucket should receive the data?"],
		"assumptions":[],"warnings":["No destination was selected"],"trigger":{"type":"manual"},"nodes":[],"edges":[]
	}`)
	defer server.Close()
	t.Setenv("OLLAMA_URL", server.URL)

	result, err := (&Server{HTTP: server.Client()}).buildPipeline(httptest.NewRequest(http.MethodPost, "/", nil), "Export orders to S3")
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != aiPipelineNeedsInput || len(result["questions"].([]string)) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if result["metrics"].(map[string]int)["repairCount"] != 0 {
		t.Fatalf("metrics = %#v", result["metrics"])
	}
	if _, exists := result["definition"]; exists {
		t.Fatalf("needs_input result contains a definition: %#v", result)
	}
	if len(*requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(*requests))
	}
}

func TestBuildPipelineRepairsOnceWithValidationError(t *testing.T) {
	invalid := `{
		"status":"ready","suggestedName":"Orders","questions":[],"assumptions":[],"warnings":[],
		"trigger":{"type":"manual"},"nodes":[{"id":"n1","label":"Bad","activityType":"made.up","config":{}}],"edges":[]
	}`
	valid := `{
		"status":"ready","suggestedName":"Orders","questions":[],"assumptions":["Manual run"],"warnings":[],
		"trigger":{"type":"manual"},"nodes":[{"id":"n1","label":"Orders API","activityType":"http.fetch","config":{"url":"https://example.test/orders"}},{"id":"n2","label":"Store","activityType":"sink.records","config":{"collection":"orders"}}],
		"edges":[{"source":"n1","target":"n2"}]
	}`
	server, requests := fakeOllama(t, invalid, valid)
	defer server.Close()
	t.Setenv("OLLAMA_URL", server.URL)

	result, err := (&Server{HTTP: server.Client()}).buildPipeline(httptest.NewRequest(http.MethodPost, "/", nil), "Fetch orders and store them")
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != aiPipelineReady || result["definition"] == nil {
		t.Fatalf("result = %#v", result)
	}
	if result["metrics"].(map[string]int)["repairCount"] != 1 {
		t.Fatalf("metrics = %#v", result["metrics"])
	}
	if len(*requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(*requests))
	}
	repairPrompt, _ := (*requests)[1].Messages[1]["content"].(string)
	if !strings.Contains(repairPrompt, "Validation error: unknown activityType made.up") {
		t.Fatalf("repair prompt = %q", repairPrompt)
	}
}

func TestBuildPipelineStopsAfterOneRepair(t *testing.T) {
	server, requests := fakeOllama(t, `not json`, `still not json`)
	defer server.Close()
	t.Setenv("OLLAMA_URL", server.URL)

	_, err := (&Server{HTTP: server.Client()}).buildPipeline(httptest.NewRequest(http.MethodPost, "/", nil), "Fetch orders")
	if err == nil || !strings.Contains(err.Error(), "after one repair") {
		t.Fatalf("error = %v", err)
	}
	if len(*requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(*requests))
	}
	repairPrompt, _ := (*requests)[1].Messages[1]["content"].(string)
	if !strings.Contains(repairPrompt, "invalid character") {
		t.Fatalf("repair prompt omitted parse error: %q", repairPrompt)
	}
}

func TestBuildPipelineRepairsMissingRequiredConnectorConfig(t *testing.T) {
	missingConfig := `{
		"status":"ready","suggestedName":"Custom","questions":[],"assumptions":[],"warnings":[],
		"trigger":{"type":"manual"},"nodes":[{"id":"n1","label":"Custom","activityType":"custom.fetch","config":{}}],"edges":[]
	}`
	needsInput := `{
		"status":"needs_input","suggestedName":"","questions":["What account name should the custom source use?"],
		"assumptions":[],"warnings":[],"trigger":{"type":"manual"},"nodes":[],"edges":[]
	}`
	server, requests := fakeOllama(t, missingConfig, needsInput)
	defer server.Close()
	t.Setenv("OLLAMA_URL", server.URL)

	registry := &connectors.Registry{Manifests: map[string]model.ConnectorManifest{
		"custom.fetch": {
			ActivityType: "custom.fetch", Label: "Custom", Kind: "source", URL: "https://example.test",
			Fields: []model.CatalogField{{Key: "account", Label: "Account", Type: "string", Required: true}},
		},
	}}
	result, err := (&Server{HTTP: server.Client(), Connectors: registry}).buildPipeline(httptest.NewRequest(http.MethodPost, "/", nil), "Fetch custom data")
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != aiPipelineNeedsInput {
		t.Fatalf("result = %#v", result)
	}
	repairPrompt, _ := (*requests)[1].Messages[1]["content"].(string)
	if !strings.Contains(repairPrompt, "node n1 (custom.fetch) requires config.account") {
		t.Fatalf("repair prompt omitted required config error: %q", repairPrompt)
	}
}

func TestBuildPipelineRepairsUnknownConnectorIDToNeedsInput(t *testing.T) {
	unknown := `{
		"status":"ready","reason":"","suggestedName":"Custom","questions":[],"assumptions":[],"warnings":[],
		"trigger":{"type":"manual"},"nodes":[{"id":"n1","label":"API","activityType":"http.fetch","config":{"connectionId":"invented-id","url":"https://example.test"}}],"edges":[]
	}`
	needsInput := `{
		"status":"needs_input","reason":"","suggestedName":"","questions":["Which saved HTTP connection should be used?"],
		"assumptions":[],"warnings":[],"trigger":{"type":"manual"},"nodes":[],"edges":[]
	}`
	server, requests := fakeOllama(t, unknown, needsInput)
	defer server.Close()
	t.Setenv("OLLAMA_URL", server.URL)

	result, err := (&Server{HTTP: server.Client()}).buildPipeline(httptest.NewRequest(http.MethodPost, "/", nil), "Fetch from my saved API")
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != aiPipelineNeedsInput || result["metrics"].(map[string]int)["repairCount"] != 1 {
		t.Fatalf("result = %#v", result)
	}
	repairPrompt, _ := (*requests)[1].Messages[1]["content"].(string)
	if !strings.Contains(repairPrompt, `unknown tenant connector instance "invented-id"`) {
		t.Fatalf("repair prompt omitted connector error: %q", repairPrompt)
	}
}

func TestBuildPipelineSupportsRejectedResponse(t *testing.T) {
	rejected := `{
		"status":"rejected","reason":"Unsafe request.","suggestedName":"","questions":[],"assumptions":[],"warnings":["Unsafe request."],
		"trigger":{"type":"manual"},"nodes":[],"edges":[]
	}`
	server, requests := fakeOllama(t, rejected)
	defer server.Close()
	t.Setenv("OLLAMA_URL", server.URL)

	result, err := (&Server{HTTP: server.Client()}).buildPipeline(httptest.NewRequest(http.MethodPost, "/", nil), "unsafe request")
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != aiPipelineRejected || result["reason"] != "Unsafe request." {
		t.Fatalf("result = %#v", result)
	}
	if _, exists := result["definition"]; exists {
		t.Fatalf("rejected result contains a definition: %#v", result)
	}
	if len(*requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(*requests))
	}
}

func TestBuildPipelineRejectsUnsafeModelURL(t *testing.T) {
	unsafe := `{
		"status":"ready","reason":"","suggestedName":"Metadata","questions":[],"assumptions":[],"warnings":[],
		"trigger":{"type":"manual"},"nodes":[{"id":"n1","label":"Metadata","activityType":"http.fetch","config":{"url":"https://169.254.169.254/latest"}}],"edges":[]
	}`
	server, requests := fakeOllama(t, unsafe)
	defer server.Close()
	t.Setenv("OLLAMA_URL", server.URL)

	result, err := (&Server{HTTP: server.Client()}).buildPipeline(httptest.NewRequest(http.MethodPost, "/", nil), "fetch metadata")
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != aiPipelineRejected {
		t.Fatalf("result = %#v", result)
	}
	if len(*requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(*requests))
	}
}

func TestBuildPipelineRejectsNestedSecretOutput(t *testing.T) {
	unsafe := `{
		"status":"ready","reason":"","suggestedName":"Secret","questions":[],"assumptions":[],"warnings":[],
		"trigger":{"type":"manual"},"nodes":[{"id":"n1","label":"API","activityType":"http.fetch","config":{"url":"https://example.test","headers":{"Authorization":"Bearer model-secret"}}}],"edges":[]
	}`
	server, requests := fakeOllama(t, unsafe)
	defer server.Close()
	t.Setenv("OLLAMA_URL", server.URL)

	result, err := (&Server{HTTP: server.Client()}).buildPipeline(httptest.NewRequest(http.MethodPost, "/", nil), "fetch safely")
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != aiPipelineRejected || !strings.Contains(result["reason"].(string), "config.headers.Authorization") {
		t.Fatalf("result = %#v", result)
	}
	if len(*requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(*requests))
	}
}

func TestAIRefineRedactsDefinitionAndHistory(t *testing.T) {
	needsInput := `{
		"status":"needs_input","reason":"","suggestedName":"","questions":["Which destination?"],
		"assumptions":[],"warnings":[],"trigger":{"type":"manual"},"nodes":[],"edges":[]
	}`
	server, requests := fakeOllama(t, needsInput)
	defer server.Close()
	t.Setenv("OLLAMA_URL", server.URL)

	body, _ := json.Marshal(map[string]interface{}{
		"prompt": "Keep the pipeline unchanged",
		"definition": map[string]interface{}{"nodes": []interface{}{map[string]interface{}{
			"config": map[string]interface{}{"clientSecret": "definition-secret", "headers": map[string]interface{}{"Authorization": "Bearer header-secret"}},
		}}},
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "api-key=history-secret"}},
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/ai/refine", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if err := (&Server{HTTP: server.Client()}).aiRefine(recorder, request); err != nil {
		t.Fatal(err)
	}
	modelPrompt, _ := (*requests)[0].Messages[1]["content"].(string)
	for _, secret := range []string{"definition-secret", "header-secret", "history-secret"} {
		if strings.Contains(modelPrompt, secret) {
			t.Fatalf("secret %q reached Ollama: %s", secret, modelPrompt)
		}
	}
	if !strings.Contains(modelPrompt, "[REDACTED]") {
		t.Fatalf("prompt did not retain redaction markers: %s", modelPrompt)
	}
}

func TestAIRequestBounds(t *testing.T) {
	promptBody, _ := json.Marshal(map[string]string{"prompt": strings.Repeat("x", maxAIUserRunes+1)})
	promptErr := (&Server{}).aiGenerate(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/ai/generate", bytes.NewReader(promptBody)))
	if httpErr, ok := promptErr.(*HTTPError); !ok || httpErr.Status != http.StatusBadRequest || httpErr.Message != "prompt is too large" {
		t.Fatalf("prompt error = %#v", promptErr)
	}

	definitionBody, _ := json.Marshal(map[string]interface{}{
		"prompt": "refine", "definition": map[string]interface{}{"padding": strings.Repeat("x", maxAIDefinition)},
	})
	definitionErr := (&Server{}).aiRefine(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/ai/refine", bytes.NewReader(definitionBody)))
	if httpErr, ok := definitionErr.(*HTTPError); !ok || httpErr.Status != http.StatusBadRequest || httpErr.Message != "definition is too large" {
		t.Fatalf("definition error = %#v", definitionErr)
	}

	contextBody, _ := json.Marshal(map[string]interface{}{
		"prompt": strings.Repeat("p", 4_000), "mermaid": strings.Repeat("m", 2_500),
	})
	contextErr := (&Server{}).aiGenerate(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/ai/generate", bytes.NewReader(contextBody)))
	if httpErr, ok := contextErr.(*HTTPError); !ok || httpErr.Status != http.StatusBadRequest || httpErr.Message != "AI context is too large" {
		t.Fatalf("aggregate context error = %#v", contextErr)
	}
}
