package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dataflow-poc/workflow-go/internal/model"
)

func TestFewShotExamplesPassPlannerValidation(t *testing.T) {
	byType := map[string]model.CatalogEntry{}
	for _, entry := range codedCatalog {
		entry.Fields = codedFields(entry.ActivityType)
		byType[entry.ActivityType] = entry
	}
	for _, example := range aiFewShotExamples {
		raw, err := json.Marshal(example.Output)
		if err != nil {
			t.Fatal(err)
		}
		var output aiPipelineResponse
		if err := json.Unmarshal(raw, &output); err != nil {
			t.Fatalf("%s: example does not match the response contract: %v", example.Request, err)
		}
		if output.Status != aiPipelineReady {
			if output.Status == aiPipelineNeedsInput && len(output.Questions) == 0 {
				t.Fatalf("%s: needs_input example must ask a question", example.Request)
			}
			continue
		}
		def := model.PipelineDefinition{Name: output.SuggestedName, Trigger: output.Trigger}
		for _, node := range output.Nodes {
			var id string
			_ = json.Unmarshal(node.ID, &id)
			def.Nodes = append(def.Nodes, model.Node{ID: id, Type: byType[node.ActivityType].NodeType, ActivityType: node.ActivityType, Config: node.Config})
		}
		for i, rawEdge := range output.Edges {
			var edge model.Edge
			if err := json.Unmarshal(rawEdge, &edge); err != nil {
				t.Fatal(err)
			}
			edge.ID = string(rune('a' + i))
			def.Edges = append(def.Edges, edge)
		}
		if err := validateAIGrounding(def, byType, aiFewShotConnections); err != nil {
			t.Fatalf("%s: %v", example.Category, err)
		}
		if err := validatePipeline(def); err != nil {
			t.Fatalf("%s: %v", example.Category, err)
		}
	}
}

func TestFewShotExamplesStayHeldOutFromEvalCorpora(t *testing.T) {
	paths, err := filepath.Glob("../../../../tests/ai-evals/cases/*.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("eval corpora not found: %v", err)
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		corpus := string(data)
		for _, connection := range aiFewShotConnections {
			if strings.Contains(corpus, connection.Name) {
				t.Fatalf("%s references few-shot connection %s", path, connection.Name)
			}
		}
		for _, example := range aiFewShotExamples {
			if strings.Contains(corpus, example.Request) {
				t.Fatalf("%s contains a few-shot request verbatim", path)
			}
		}
	}
}

func TestFewShotSectionMatchesRequestShape(t *testing.T) {
	cases := map[string]string{
		"Current pipeline JSON:\n{}\n\nApply this change: add a filter": `"id":"orders_src"`,
		"Join orders with refunds on order_id and load them":            `"mergeStrategy":"innerJoin"`,
		"Fork the stream into an archive and an alert":                  `"activityType":"flow.fork"`,
		"Copy invoices into the lake":                                   `"status":"needs_input"`,
	}
	for prompt, want := range cases {
		section := strings.Join(aiFewShotSection(prompt), "\n")
		if strings.Count(section, "Example request:") != 2 || !strings.Contains(section, want) {
			t.Fatalf("%q: section missing %s:\n%s", prompt, want, section)
		}
	}
}

func TestFewShotIsOptInAndContextWindowConfigurable(t *testing.T) {
	ready := `{"status":"needs_input","reason":"","suggestedName":"","questions":["Which connection?"],"assumptions":[],"warnings":[],"trigger":{"type":"manual"},"nodes":[],"edges":[]}`
	for _, enabled := range []bool{false, true} {
		server, requests := fakeOllama(t, ready)
		t.Setenv("OLLAMA_URL", server.URL)
		if enabled {
			t.Setenv("AI_FEW_SHOT", "true")
			t.Setenv("OLLAMA_NUM_CTX", "8192")
		}
		api := &Server{HTTP: server.Client()}
		if _, err := api.buildPipeline(httptest.NewRequest(http.MethodPost, "/", nil), "Copy invoices into the lake"); err != nil {
			t.Fatal(err)
		}
		server.Close()
		system, _ := (*requests)[0].Messages[0]["content"].(string)
		if got := strings.Contains(system, "WORKED EXAMPLES"); got != enabled {
			t.Fatalf("AI_FEW_SHOT=%v: examples present = %v", enabled, got)
		}
		if !strings.HasSuffix(system, "no markdown or explanation.") {
			t.Fatal("output instruction must stay last in the system prompt")
		}
		wantCtx := float64(4096)
		if enabled {
			wantCtx = 8192
		}
		if (*requests)[0].Options["num_ctx"] != wantCtx {
			t.Fatalf("num_ctx = %v, want %v", (*requests)[0].Options["num_ctx"], wantCtx)
		}
	}
}

func TestAICallTimeoutConfigurable(t *testing.T) {
	if aiCallTimeout() != 240*time.Second {
		t.Fatalf("default = %v", aiCallTimeout())
	}
	t.Setenv("OLLAMA_TIMEOUT_SECONDS", "480")
	if aiCallTimeout() != 480*time.Second {
		t.Fatalf("configured = %v", aiCallTimeout())
	}
	t.Setenv("OLLAMA_TIMEOUT_SECONDS", "5")
	if aiCallTimeout() != 240*time.Second {
		t.Fatal("values under 30s fall back to the default")
	}
}
