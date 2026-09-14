package connectors

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/dataflow-poc/workflow-go/internal/model"
)

type Registry struct {
	Manifests map[string]model.ConnectorManifest
}

func Load(dirs ...string) *Registry {
	r := &Registry{Manifests: map[string]model.ConnectorManifest{}}
	for _, dir := range dirs {
		entries, _ := os.ReadDir(dir)
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".manifest.json") {
				continue
			}
			body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				continue
			}
			var manifest model.ConnectorManifest
			if json.Unmarshal(body, &manifest) == nil && manifest.ActivityType != "" && manifest.Label != "" && manifest.Kind == "source" && manifest.URL != "" {
				r.Manifests[manifest.ActivityType] = manifest
			}
		}
	}
	return r
}
func (r *Registry) Catalog() []model.CatalogEntry {
	out := make([]model.CatalogEntry, 0, len(r.Manifests))
	for _, m := range r.Manifests {
		// Manifest execution implements fetching records only; writes require a coded handler.
		if m.Kind != "source" {
			continue
		}
		color := m.Color
		if color == "" {
			color = "#1D9E75"
		}
		ingestion := true
		if m.SupportsIngestion != nil {
			ingestion = *m.SupportsIngestion
		}
		out = append(out, model.CatalogEntry{ActivityType: m.ActivityType, NodeType: "source", Label: m.Label, Color: color, SupportsIngestion: ingestion, Fields: m.Fields})
	}
	return out
}
