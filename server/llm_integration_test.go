//go:build llama_integration
// +build llama_integration

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/llamacpp"
)

// This test is opt-in (build tag `llama_integration`) and exercises the /api/llm endpoint
// using a local CPU-only llamacpp model. It stubs prefetching via prefetch=false and
// overrides the LLM constructor.
func TestLLMHandlerWithLlamaCPP(t *testing.T) {
	if os.Getenv("LOCAL_LLM") == "" {
		t.Skip("LOCAL_LLM not set; skipping CPU-only llamacpp integration test")
	}

	modelPath := os.Getenv("LOCAL_LLM_MODEL")
	if modelPath == "" {
		modelPath = filepath.Join("./models", "tinyllama-1.1b-chat.gguf")
	}
	if _, err := os.Stat(modelPath); err != nil {
		t.Skipf("model not present at %s; skipping", modelPath)
	}

	// Override LLM factory to use local llamacpp.
	oldFactory := newLLMClient
	newLLMClient = func(_ string) (llms.Model, error) {
		return llamacpp.New(
			llamacpp.WithModelPath(modelPath),
			llamacpp.WithPredictTokens(32),
			llamacpp.WithTemperature(0.1),
		)
	}
	defer func() { newLLMClient = oldFactory }()

	srv := httptest.NewServer(http.HandlerFunc(llmHandler))
	defer srv.Close()

	payload := map[string]any{
		"prompt":   "Hello from llamacpp",
		"prefetch": false, // disable collector prefetch
	}
	body, _ := json.Marshal(payload)

	resp, err := http.Post(srv.URL+"/api/llm", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out LLMResponse
	dec := json.NewDecoder(resp.Body)
	require.NoError(t, dec.Decode(&out))
	require.NotEmpty(t, out.Result)
}
