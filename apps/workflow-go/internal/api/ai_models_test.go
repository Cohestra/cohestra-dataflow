package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAIModelsListsDefaultFirstWithLabels(t *testing.T) {
	t.Setenv("OLLAMA_MODEL", "qwen3:8b")
	t.Setenv("OLLAMA_MODELS", "qwen3:4b-instruct-2507, qwen3:8b")
	mux := http.NewServeMux()
	(&Server{}).registerAI(mux)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/ai/models", nil))
	var body struct {
		Default string          `json:"default"`
		Models  []aiModelOption `json:"models"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body)
	}
	if body.Default != "qwen3:8b" || len(body.Models) != 2 || body.Models[0].ID != "qwen3:8b" || body.Models[1].Label != "Qwen3 4B" {
		t.Fatalf("models = %+v", body)
	}
}

func TestAIGenerateUsesRequestedAllowedModelOnly(t *testing.T) {
	needsInput := `{"status":"needs_input","reason":"","suggestedName":"","questions":["Which connection?"],"assumptions":[],"warnings":[],"trigger":{"type":"manual"},"nodes":[],"edges":[]}`
	server, requests := fakeOllama(t, needsInput, needsInput)
	defer server.Close()
	t.Setenv("OLLAMA_URL", server.URL)
	t.Setenv("OLLAMA_MODEL", "qwen3:8b")
	t.Setenv("OLLAMA_MODELS", "qwen3:8b,qwen3:4b-instruct-2507")
	mux := http.NewServeMux()
	(&Server{HTTP: server.Client()}).registerAI(mux)
	post := func(path, body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		return recorder
	}
	if got := post("/api/ai/generate", `{"prompt":"copy orders","model":"qwen3:4b-instruct-2507"}`); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"model":"qwen3:4b-instruct-2507"`) {
		t.Fatalf("generate: %d %s", got.Code, got.Body)
	}
	if got := post("/api/ai/refine", `{"prompt":"add a filter","definition":{"nodes":[],"edges":[]}}`); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"model":"qwen3:8b"`) {
		t.Fatalf("refine default: %d %s", got.Code, got.Body)
	}
	if len(*requests) != 2 || (*requests)[0].Model != "qwen3:4b-instruct-2507" || (*requests)[1].Model != "qwen3:8b" {
		t.Fatalf("Ollama received %+v, want the chosen model then the default", *requests)
	}
	if got := post("/api/ai/generate", `{"prompt":"copy orders","model":"llama3:70b"}`); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "unsupported AI model") {
		t.Fatalf("unlisted model: %d %s", got.Code, got.Body)
	}
	if len(*requests) != 2 {
		t.Fatal("an unlisted model must never reach Ollama")
	}
}
