package api

import (
	"context"
	"net/http"
	"strings"
)

// The AI builder offers a short, operator-approved list of Ollama models
// (OLLAMA_MODELS, comma-separated; OLLAMA_MODEL is the default). Requests may
// pick one of them and nothing else, so a client cannot make the server load
// arbitrary models.

const defaultAIModel = "qwen3:8b"

type aiModelOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// Labels for models measured in docs/evals/cpu-bakeoff-20261016.
var aiModelLabels = map[string]aiModelOption{
	"qwen3:8b":               {Label: "Qwen3 8B", Description: "Best quality: 14/17 lakehouse cases with few-shot"},
	"qwen3:4b-instruct-2507": {Label: "Qwen3 4B", Description: "Faster: about 1.7x quicker, 11/17 lakehouse cases"},
}

func aiDefaultModel() string {
	return strings.TrimSpace(env("OLLAMA_MODEL", defaultAIModel))
}

// aiModelOptions lists the selectable models, default first.
func aiModelOptions() []aiModelOption {
	ids := []string{aiDefaultModel()}
	for _, id := range strings.Split(env("OLLAMA_MODELS", "qwen3:8b,qwen3:4b-instruct-2507"), ",") {
		if id = strings.TrimSpace(id); id != "" && !containsString(ids, id) {
			ids = append(ids, id)
		}
	}
	options := make([]aiModelOption, len(ids))
	for i, id := range ids {
		option := aiModelLabels[id]
		if option.Label == "" {
			option.Label = id
		}
		option.ID = id
		options[i] = option
	}
	return options
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// resolveAIModel maps a requested model to an allowed one ("" = default).
func resolveAIModel(requested string) (string, bool) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return aiDefaultModel(), true
	}
	for _, option := range aiModelOptions() {
		if option.ID == requested {
			return requested, true
		}
	}
	return "", false
}

type aiModelKey struct{}

func withAIModel(r *http.Request, model string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), aiModelKey{}, model))
}

func aiModelFrom(r *http.Request) string {
	if model, ok := r.Context().Value(aiModelKey{}).(string); ok && model != "" {
		return model
	}
	return aiDefaultModel()
}

func (s *Server) aiModels(w http.ResponseWriter, r *http.Request) error {
	jsonResponse(w, http.StatusOK, map[string]interface{}{"default": aiDefaultModel(), "models": aiModelOptions()})
	return nil
}
