package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/langchaingo/llms"
	collector "github.com/whatnick/austender_analyser/collector/cmd"
)

func writeTestCatalogForLLM(t *testing.T, cacheDir string) {
	t.Helper()
	type testIndexFile struct {
		Path          string `json:"path"`
		Source        string `json:"source"`
		FinancialYear string `json:"financialYear"`
		AgencyKey     string `json:"agencyKey"`
		AgencyName    string `json:"agencyName,omitempty"`
		CompanyKey    string `json:"companyKey"`
		CompanyName   string `json:"companyName,omitempty"`
		RowCount      int64  `json:"rowCount"`
	}
	type testIndexState struct {
		Version int                      `json:"version"`
		Files   map[string]testIndexFile `json:"files"`
	}
	data, err := json.MarshalIndent(testIndexState{
		Version: 1,
		Files: map[string]testIndexFile{
			"p1": {Path: "p1", Source: "federal", FinancialYear: "2024-25", AgencyKey: "department_of_defence", AgencyName: "Department of Defence", CompanyKey: "kpmg", CompanyName: "KPMG", RowCount: 100},
			"p2": {Path: "p2", Source: "federal", FinancialYear: "2024-25", AgencyKey: "ato", AgencyName: "Australian Taxation Office", CompanyKey: "acme", CompanyName: "Acme Pty Ltd", RowCount: 10},
		},
	}, "", "  ")
	if err != nil {
		t.Fatalf("marshal index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "clickhouse-index.json"), data, 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
}

type dummyLLM struct{}

func (d dummyLLM) GenerateContent(context.Context, []llms.MessageContent, ...llms.CallOption) (*llms.ContentResponse, error) {
	return &llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: ""}}}, nil
}

func (d dummyLLM) Call(context.Context, string, ...llms.CallOption) (string, error) {
	return "", nil
}

func TestLLMHandler_AgentMode_ToolChain(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("AUSTENDER_CACHE_DIR", cacheDir)
	writeTestCatalogForLLM(t, cacheDir)

	// Stub LLM client construction.
	oldNew := newLLMClient
	newLLMClient = func(modelName, backendOverride string) (llms.Model, error) { return dummyLLM{}, nil }
	defer func() { newLLMClient = oldNew }()

	// Stub tool-driving LLM outputs.
	oldGen := generateFromPrompt
	steps := []string{
		`{"tool":"identify_jurisdiction","arguments":{"text":"Federal government spend on KPMG with Department of Defence"}}`,
		`{"tool":"find_companies","arguments":{"query":"kpmg","limit":5}}`,
		`{"tool":"find_agencies","arguments":{"query":"defence","limit":5}}`,
		`{"tool":"aggregate_contracts","arguments":{"keyword":"KPMG","company":"KPMG","agency":"Department of Defence","source":"federal","lookbackPeriod":5}}`,
		`{"final":"Total spend is $42.00"}`,
	}
	idx := 0
	generateFromPrompt = func(ctx context.Context, client llms.Model, prompt string) (string, error) {
		if idx >= len(steps) {
			return `{"final":"done"}`, nil
		}
		out := steps[idx]
		idx++
		return out, nil
	}
	defer func() { generateFromPrompt = oldGen }()

	// Stub scraping.
	oldRun := runScrape
	runScrape = func(ctx context.Context, req collector.SearchRequest) (string, error) {
		return "$42.00", nil
	}
	defer func() { runScrape = oldRun }()

	w := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]any{
		"prompt":         "Federal government spend on KPMG with Department of Defence",
		"agent":          true,
		"lookbackPeriod": 5,
		"useCache":       true,
	})
	r := httptest.NewRequest(http.MethodPost, "/api/llm", bytes.NewReader(body))

	llmHandler(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Result != "Total spend is $42.00" {
		t.Fatalf("unexpected result: %s", resp.Result)
	}
}

func TestLLMHandler_PrefetchSpend_RespectsSourceAndLookback(t *testing.T) {
	// Ensure the prefetched context uses the request's source (e.g. vic)
	// so the UI doesn't show Federal prefetch when a state is selected.
	cacheDir := t.TempDir()
	t.Setenv("AUSTENDER_CACHE_DIR", cacheDir)

	oldNew := newLLMClient
	newLLMClient = func(modelName, backendOverride string) (llms.Model, error) { return dummyLLM{}, nil }
	defer func() { newLLMClient = oldNew }()

	oldGen := generateFromPrompt
	generateFromPrompt = func(ctx context.Context, client llms.Model, prompt string) (string, error) {
		return `{"final":"ok"}`, nil
	}
	defer func() { generateFromPrompt = oldGen }()

	oldSearch := runSearchWithCache
	var gotReq collector.SearchRequest
	runSearchWithCache = func(ctx context.Context, req collector.SearchRequest) (string, bool, error) {
		gotReq = req
		return "$123.00", true, nil
	}
	defer func() { runSearchWithCache = oldSearch }()

	w := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]any{
		"prompt":         "How much was spent on KPMG ?",
		"agent":          true,
		"source":         "vic",
		"lookbackPeriod": 7,
		"useCache":       true,
	})
	r := httptest.NewRequest(http.MethodPost, "/api/llm", bytes.NewReader(body))

	llmHandler(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if gotReq.Source != "vic" {
		t.Fatalf("expected prefetch source vic, got %q", gotReq.Source)
	}
	if gotReq.LookbackPeriod != 7 {
		t.Fatalf("expected prefetch lookback 7, got %d", gotReq.LookbackPeriod)
	}

	var resp struct {
		Context string `json:"context"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Context == "" {
		t.Fatalf("expected prefetched context to be set")
	}
}
