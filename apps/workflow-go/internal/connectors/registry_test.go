package connectors

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/dataflow-poc/workflow-go/internal/config"
	"github.com/dataflow-poc/workflow-go/internal/model"
)

func TestManifestAdmissionMatchesSupportedDirection(t *testing.T) {
	dir := t.TempDir()
	for _, kind := range []string{"source", "sink"} {
		body, err := json.Marshal(model.ConnectorManifest{ActivityType: "fixture." + kind, Label: kind, Kind: kind, URL: "https://fixture.example/records"})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, kind+".manifest.json"), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	registry := Load(dir)
	if len(registry.Manifests) != 1 || registry.Manifests["fixture.source"].Kind != "source" {
		t.Fatalf("admitted=%v", registry.Manifests)
	}
	// Even a manually assembled registry must not advertise or fetch a sink.
	registry.Manifests["fixture.sink"] = model.ConnectorManifest{ActivityType: "fixture.sink", Kind: "sink"}
	catalog := registry.Catalog()
	if len(catalog) != 1 || catalog[0].NodeType != "source" || catalog[0].ActivityType != "fixture.source" {
		t.Fatalf("catalog=%v", catalog)
	}
	runtime := NewRuntime(nil, nil, config.Config{}, http.DefaultClient, registry)
	if _, err := runtime.Fetch(context.Background(), "fixture.sink", SourceParams{}); err == nil {
		t.Fatal("sink accepted as a manifest source")
	}
	if _, _, err := runtime.Handle(context.Background(), "fixture.sink", nil, nil, HandlerContext{}); err == nil {
		t.Fatal("unsupported sink accepted")
	}
	if runtime.Handlers["sink.postgres"] == nil || runtime.Sources["http.fetch"] == nil {
		t.Fatal("coded connector registrations lost")
	}
}
