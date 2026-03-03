//go:build ignore
// +build ignore

// This file is excluded from normal builds because the llamacpp package was
// removed from langchaingo v0.1.14+. It is kept as a reference for future
// local-LLM integration tests. To re-enable, replace the llamacpp import with
// an available local-model provider and change the build tag.

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLLMHandlerWithLocalModel is a placeholder for future local-LLM integration tests.
func TestLLMHandlerWithLocalModel(t *testing.T) {
	if os.Getenv("LOCAL_LLM") == "" {
		t.Skip("LOCAL_LLM not set; skipping local-model integration test")
	}

	srv := httptest.NewServer(http.HandlerFunc(llmHandler))
	defer srv.Close()

	payload := map[string]any{
		"prompt": "Hello from local model",
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
