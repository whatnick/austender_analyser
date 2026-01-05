package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	defaultOpenAIModel = "gpt-4o-mini"
	llmBackendOpenAI   = "openai"
	llmBackendOllama   = "ollama"
)

// LLMModelInfo describes a model that can be selected via /api/llm/models.
type LLMModelInfo struct {
	Name       string `json:"name"`
	Provider   string `json:"provider"`
	Display    string `json:"display"`
	Default    bool   `json:"default"`
	SizeBytes  int64  `json:"sizeBytes,omitempty"`
	ModifiedAt string `json:"modifiedAt,omitempty"`
}

// LLMModelsResponse wraps the available models for the frontend selector.
type LLMModelsResponse struct {
	Backend      string         `json:"backend"`
	Models       []LLMModelInfo `json:"models"`
	Endpoint     string         `json:"endpoint,omitempty"`
	DefaultModel string         `json:"defaultModel,omitempty"`
}

var fetchOllamaModelsFunc = fetchOllamaModels

func preferOpenAI() bool {
	return strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != ""
}

func isOllamaConfigured() bool {
	return strings.TrimSpace(os.Getenv("OLLAMA_HOST")) != ""
}

func defaultOpenAIModelName() string {
	if envDefault := strings.TrimSpace(os.Getenv("OPENAI_DEFAULT_MODEL")); envDefault != "" {
		return envDefault
	}
	return defaultOpenAIModel
}

func openAIModelInfo() []LLMModelInfo {
	name := defaultOpenAIModelName()
	return []LLMModelInfo{{
		Name:     name,
		Display:  name,
		Provider: llmBackendOpenAI,
		Default:  true,
	}}
}

func currentLLMBackend() string {
	if preferOpenAI() {
		return llmBackendOpenAI
	}
	if isOllamaConfigured() {
		return llmBackendOllama
	}
	return llmBackendOpenAI
}

func parseModelRequest(raw string) (backendOverride, model string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	lowerRaw := strings.ToLower(raw)
	if strings.HasPrefix(lowerRaw, llmBackendOpenAI+":") || strings.HasPrefix(lowerRaw, llmBackendOllama+":") {
		parts := strings.SplitN(raw, ":", 2)
		if len(parts) == 2 {
			backend := strings.ToLower(strings.TrimSpace(parts[0]))
			name := strings.TrimSpace(parts[1])
			if name != "" {
				return backend, name
			}
		}
		return "", ""
	}
	return "", raw
}

func resolveModelName(requested string) (modelName string, backendOverride string, err error) {
	backendOverride, requested = parseModelRequest(requested)
	if requested != "" {
		return requested, backendOverride, nil
	}

	switch currentLLMBackend() {
	case llmBackendOllama:
		if envDefault := strings.TrimSpace(os.Getenv("OLLAMA_DEFAULT_MODEL")); envDefault != "" {
			return envDefault, backendOverride, nil
		}
		models, err := getOllamaModels(context.Background())
		if err != nil {
			if preferOpenAI() {
				return defaultOpenAIModelName(), llmBackendOpenAI, nil
			}
			return "", backendOverride, fmt.Errorf("failed to detect Ollama models: %w", err)
		}
		for _, m := range models {
			if name := m.Name(); name != "" {
				return name, backendOverride, nil
			}
		}
		if preferOpenAI() {
			return defaultOpenAIModelName(), llmBackendOpenAI, nil
		}
		return "", backendOverride, fmt.Errorf("no Ollama models available; run `ollama list` to install one")
	default:
		return defaultOpenAIModelName(), backendOverride, nil
	}
}

func selectBackend(modelName, backendOverride string) string {
	switch strings.ToLower(strings.TrimSpace(backendOverride)) {
	case llmBackendOpenAI:
		if preferOpenAI() {
			return llmBackendOpenAI
		}
		if isOllamaConfigured() {
			return llmBackendOllama
		}
		return llmBackendOpenAI
	case llmBackendOllama:
		if isOllamaConfigured() {
			return llmBackendOllama
		}
		// Fall back to default if Ollama not available
		if preferOpenAI() {
			return llmBackendOpenAI
		}
		return llmBackendOpenAI
	}

	if preferOpenAI() {
		return llmBackendOpenAI
	}
	if isOllamaConfigured() {
		return llmBackendOllama
	}
	return llmBackendOpenAI
}

func getAvailableLLMModels(ctx context.Context) ([]LLMModelInfo, error) {
	switch currentLLMBackend() {
	case llmBackendOllama:
		models, err := getOllamaModels(ctx)
		if err != nil {
			if preferOpenAI() {
				return openAIModelInfo(), nil
			}
			return nil, err
		}
		defaultName := strings.TrimSpace(os.Getenv("OLLAMA_DEFAULT_MODEL"))
		infos := make([]LLMModelInfo, 0, len(models))
		for _, m := range models {
			name := m.Name()
			if name == "" {
				continue
			}
			infos = append(infos, LLMModelInfo{
				Name:       name,
				Display:    name,
				Provider:   llmBackendOllama,
				SizeBytes:  m.Size,
				ModifiedAt: m.ModifiedAt,
			})
		}
		if len(infos) == 0 {
			if preferOpenAI() {
				return openAIModelInfo(), nil
			}
			return infos, nil
		}
		if defaultName == "" {
			defaultName = infos[0].Name
		}
		defaultAssigned := false
		for i := range infos {
			if infos[i].Name == defaultName {
				infos[i].Default = true
				defaultAssigned = true
			}
		}
		if !defaultAssigned {
			infos[0].Default = true
		}
		return infos, nil
	default:
		return openAIModelInfo(), nil
	}
}

func llmModelsHandler(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	backend := currentLLMBackend()
	models, err := getAvailableLLMModels(r.Context())
	if err != nil {
		if preferOpenAI() {
			backend = llmBackendOpenAI
			models = openAIModelInfo()
		} else {
			sendJSONError(w, fmt.Sprintf("failed to list models: %v", err), http.StatusInternalServerError)
			return
		}
	}
	defaultModel := ""
	for _, m := range models {
		if m.Default {
			defaultModel = m.Name
			break
		}
	}
	if defaultModel == "" && len(models) > 0 {
		defaultModel = models[0].Name
	}
	resp := LLMModelsResponse{Backend: backend, Models: models, DefaultModel: defaultModel}
	if backend == llmBackendOllama {
		resp.Endpoint = strings.TrimSpace(os.Getenv("OLLAMA_HOST"))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// normalizeOllamaURL ensures WithServerURL receives a URL with scheme and no trailing slash.
func normalizeOllamaURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return strings.TrimRight(raw, "/")
	}
	if strings.HasPrefix(raw, "//") {
		raw = raw[2:]
	}
	host := raw
	if !strings.Contains(host, ":") {
		host = host + ":11434"
	}
	return "http://" + strings.TrimRight(host, "/")
}

type ollamaModel struct {
	Model      string `json:"model"`
	NameField  string `json:"name"`
	Digest     string `json:"digest"`
	ModifiedAt string `json:"modified_at"`
	Size       int64  `json:"size"`
}

func (m ollamaModel) Name() string {
	if strings.TrimSpace(m.NameField) != "" {
		return strings.TrimSpace(m.NameField)
	}
	return strings.TrimSpace(m.Model)
}

var ollamaModelCache struct {
	mu      sync.Mutex
	models  []ollamaModel
	expires time.Time
	err     error
}

func getOllamaModels(ctx context.Context) ([]ollamaModel, error) {
	host := strings.TrimSpace(os.Getenv("OLLAMA_HOST"))
	if host == "" {
		return nil, fmt.Errorf("OLLAMA_HOST is not set")
	}

	now := time.Now()
	ollamaModelCache.mu.Lock()
	if now.Before(ollamaModelCache.expires) && ollamaModelCache.models != nil {
		cached := make([]ollamaModel, len(ollamaModelCache.models))
		copy(cached, ollamaModelCache.models)
		err := ollamaModelCache.err
		ollamaModelCache.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return cached, nil
	}
	ollamaModelCache.mu.Unlock()

	models, err := fetchOllamaModelsFunc(ctx, host)
	ttl := 30 * time.Second
	if err != nil {
		ttl = 10 * time.Second
	}

	ollamaModelCache.mu.Lock()
	if err == nil {
		ollamaModelCache.models = make([]ollamaModel, len(models))
		copy(ollamaModelCache.models, models)
	} else {
		ollamaModelCache.models = nil
	}
	ollamaModelCache.err = err
	ollamaModelCache.expires = time.Now().Add(ttl)
	ollamaModelCache.mu.Unlock()

	if err != nil {
		return nil, err
	}
	return models, nil
}

func fetchOllamaModels(ctx context.Context, rawHost string) ([]ollamaModel, error) {
	baseURL, err := url.Parse(normalizeOllamaURL(rawHost))
	if err != nil {
		return nil, fmt.Errorf("invalid OLLAMA_HOST: %w", err)
	}
	endpoint := baseURL.ResolveReference(&url.URL{Path: "/api/tags"})

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	client := mcpHTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned %s", resp.Status)
	}

	var payload struct {
		Models []ollamaModel `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Models, nil
}

// resetOllamaModelCache clears the cache; intended for tests.
func resetOllamaModelCache() {
	ollamaModelCache.mu.Lock()
	defer ollamaModelCache.mu.Unlock()
	ollamaModelCache.models = nil
	ollamaModelCache.err = nil
	ollamaModelCache.expires = time.Time{}
}
