package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveModelName_DefaultOpenAI(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "")
	t.Setenv("OPENAI_DEFAULT_MODEL", "")
	resetOllamaModelCache()

	got, err := resolveModelName("")
	if err != nil {
		t.Fatalf("resolveModelName returned error: %v", err)
	}
	if got != defaultOpenAIModel {
		t.Fatalf("expected default model %q, got %q", defaultOpenAIModel, got)
	}
}

func TestResolveModelName_OpenAIEnvOverride(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "")
	t.Setenv("OPENAI_DEFAULT_MODEL", "gpt-test")
	resetOllamaModelCache()

	got, err := resolveModelName("")
	if err != nil {
		t.Fatalf("resolveModelName returned error: %v", err)
	}
	if got != "gpt-test" {
		t.Fatalf("expected override model, got %q", got)
	}
}

func TestResolveModelName_Ollama(t *testing.T) {
	t.Setenv("OPENAI_DEFAULT_MODEL", "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{
				{"name": "mistral", "model": "mistral", "size": 1},
				{"name": "llama3", "model": "llama3"},
			},
		})
	}))
	defer srv.Close()

	t.Setenv("OLLAMA_HOST", srv.URL)
	resetOllamaModelCache()

	got, err := resolveModelName("")
	if err != nil {
		t.Fatalf("resolveModelName returned error: %v", err)
	}
	if got != "mistral" {
		t.Fatalf("expected mistral, got %q", got)
	}

	models, err := getAvailableLLMModels(context.Background())
	if err != nil {
		t.Fatalf("getAvailableLLMModels error: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].Name != "mistral" {
		t.Fatalf("unexpected first model %q", models[0].Name)
	}
	defaultCount := 0
	for _, m := range models {
		if m.Default {
			defaultCount++
		}
	}
	if defaultCount != 1 {
		t.Fatalf("expected exactly one default model, found %d", defaultCount)
	}
	if models[0].Provider != llmBackendOllama {
		t.Fatalf("expected provider %s, got %s", llmBackendOllama, models[0].Provider)
	}

	resetOllamaModelCache()
	req := httptest.NewRequest(http.MethodGet, "/api/llm/models", nil)
	w := httptest.NewRecorder()
	llmModelsHandler(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp LLMModelsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode models response: %v", err)
	}
	if resp.Backend != llmBackendOllama {
		t.Fatalf("expected backend %s, got %s", llmBackendOllama, resp.Backend)
	}
	if resp.Endpoint != srv.URL {
		t.Fatalf("expected endpoint %s, got %s", srv.URL, resp.Endpoint)
	}
	if resp.DefaultModel != "mistral" {
		t.Fatalf("expected default model mistral, got %s", resp.DefaultModel)
	}
}
