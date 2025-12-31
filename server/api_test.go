package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"
	collector "github.com/whatnick/austender_analyser/collector/cmd"
)

type reqBody struct {
	Keyword        string `json:"keyword"`
	Company        string `json:"company,omitempty"`
	CompanyName    string `json:"companyName,omitempty"`
	Agency         string `json:"agency,omitempty"`
	Source         string `json:"source,omitempty"`
	StartDate      string `json:"startDate,omitempty"`
	EndDate        string `json:"endDate,omitempty"`
	DateType       string `json:"dateType,omitempty"`
	LookbackPeriod int    `json:"lookbackPeriod,omitempty"`
}

func resetScrapeDailyCacheForTests() {
	scrapeCache.mu.Lock()
	defer scrapeCache.mu.Unlock()
	scrapeCache.items = nil
}

func TestScrapeHandler_OK(t *testing.T) {
	// stub the runScrape function
	old := runScrape
	runScrape = func(ctx context.Context, req collector.SearchRequest) (string, error) {
		if req.Keyword != "KPMG" {
			t.Fatalf("unexpected keyword: %s", req.Keyword)
		}
		return "$123.45", nil
	}
	defer func() { runScrape = old }()

	RegisterHandlers()
	w := httptest.NewRecorder()
	b, _ := json.Marshal(reqBody{Keyword: "KPMG"})
	r := httptest.NewRequest(http.MethodPost, "/api/scrape", bytes.NewReader(b))

	scrapeHandler(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}
	var resp ScrapeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp.Result != "$123.45" {
		t.Fatalf("unexpected result: %s", resp.Result)
	}
}

func TestScrapeHandler_SameDayCacheHit(t *testing.T) {
	t.Setenv("AUSTENDER_CACHE_TZ", "UTC")
	resetScrapeDailyCacheForTests()
	oldNow := nowFunc
	nowFunc = func() time.Time { return time.Date(2025, 12, 31, 10, 0, 0, 0, time.UTC) }
	defer func() { nowFunc = oldNow }()

	calls := 0
	old := runScrape
	runScrape = func(ctx context.Context, req collector.SearchRequest) (string, error) {
		calls++
		return "$9.99", nil
	}
	defer func() { runScrape = old }()

	body, _ := json.Marshal(reqBody{Keyword: "KPMG", Company: "KPMG"})

	// First call populates cache.
	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest(http.MethodPost, "/api/scrape", bytes.NewReader(body))
	scrapeHandler(w1, r1)
	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w1.Code)
	}

	// Second call should hit cache and not invoke runScrape again.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPost, "/api/scrape", bytes.NewReader(body))
	scrapeHandler(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w2.Code)
	}

	if calls != 1 {
		t.Fatalf("expected 1 underlying scrape call, got %d", calls)
	}
}

func TestScrapeHandler_SameDayCacheKeyIncludesSource(t *testing.T) {
	t.Setenv("AUSTENDER_CACHE_TZ", "UTC")
	resetScrapeDailyCacheForTests()
	oldNow := nowFunc
	nowFunc = func() time.Time { return time.Date(2025, 12, 31, 10, 0, 0, 0, time.UTC) }
	defer func() { nowFunc = oldNow }()

	calls := 0
	old := runScrape
	runScrape = func(ctx context.Context, req collector.SearchRequest) (string, error) {
		calls++
		return "$1.00", nil
	}
	defer func() { runScrape = old }()

	// Same company, different source should be a cache miss.
	first, _ := json.Marshal(reqBody{Keyword: "Test", Company: "Acme", Source: "austender"})
	second, _ := json.Marshal(reqBody{Keyword: "Test", Company: "Acme", Source: "nsw"})

	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest(http.MethodPost, "/api/scrape", bytes.NewReader(first))
	scrapeHandler(w1, r1)
	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w1.Code)
	}

	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPost, "/api/scrape", bytes.NewReader(second))
	scrapeHandler(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w2.Code)
	}

	if calls != 2 {
		t.Fatalf("expected 2 underlying scrape calls, got %d", calls)
	}
}

func TestScrapeHandler_CompanyAgencyForward(t *testing.T) {
	old := runScrape
	runScrape = func(ctx context.Context, req collector.SearchRequest) (string, error) {
		if req.Keyword != "EY" || req.Company != "Ernst & Young" || req.Agency != "ATO" {
			t.Fatalf("unexpected params: %s, %s, %s", req.Keyword, req.Company, req.Agency)
		}
		return "$1.00", nil
	}
	defer func() { runScrape = old }()

	w := httptest.NewRecorder()
	b, _ := json.Marshal(reqBody{Keyword: "EY", Company: "Ernst & Young", Agency: "ATO"})
	r := httptest.NewRequest(http.MethodPost, "/api/scrape", bytes.NewReader(b))

	scrapeHandler(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}
}

func TestScrapeHandler_CompanyNameAlias(t *testing.T) {
	old := runScrape
	runScrape = func(ctx context.Context, req collector.SearchRequest) (string, error) {
		if req.Keyword != "EY" || req.Company != "EY Pty Ltd" || req.Agency != "" {
			t.Fatalf("unexpected params: %s, %s, %s", req.Keyword, req.Company, req.Agency)
		}
		return "$2.00", nil
	}
	defer func() { runScrape = old }()

	w := httptest.NewRecorder()
	b, _ := json.Marshal(reqBody{Keyword: "EY", CompanyName: "EY Pty Ltd"})
	r := httptest.NewRequest(http.MethodPost, "/api/scrape", bytes.NewReader(b))

	scrapeHandler(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}
}

func TestScrapeHandler_DatesForwarded(t *testing.T) {
	old := runScrape
	runScrape = func(ctx context.Context, req collector.SearchRequest) (string, error) {
		if req.StartDate.IsZero() {
			t.Fatalf("expected start date to be parsed")
		}
		if req.StartDate.Format("2006-01-02") != "2024-01-01" {
			t.Fatalf("unexpected start date: %s", req.StartDate)
		}
		if !req.EndDate.IsZero() {
			t.Fatalf("expected end date to be zero")
		}
		if req.DateType != "publish" {
			t.Fatalf("unexpected date type: %s", req.DateType)
		}
		return "$3.00", nil
	}
	defer func() { runScrape = old }()

	w := httptest.NewRecorder()
	b, _ := json.Marshal(reqBody{Keyword: "Test", StartDate: "2024-01-01", DateType: "publish"})
	r := httptest.NewRequest(http.MethodPost, "/api/scrape", bytes.NewReader(b))

	scrapeHandler(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}
}

func TestScrapeHandler_InvalidDate(t *testing.T) {
	w := httptest.NewRecorder()
	b, _ := json.Marshal(reqBody{Keyword: "Test", StartDate: "01-01-2024"})
	r := httptest.NewRequest(http.MethodPost, "/api/scrape", bytes.NewReader(b))

	scrapeHandler(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestMCPStreamable_ListTools(t *testing.T) {
	handler := buildMCPHTTPHandler()
	sessionID := initializeTestMCPSession(t, handler)
	resp := sendJSONRPCRequest(t, handler, sessionID, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
		"params":  map[string]any{},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
	var body struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid response: %v", err)
	}
	foundAggregate := false
	foundIdentify := false
	foundAgencies := false
	foundCompanies := false
	for _, tool := range body.Result.Tools {
		if tool.Name == "aggregate_contracts" {
			foundAggregate = true
		}
		if tool.Name == "identify_jurisdiction" {
			foundIdentify = true
		}
		if tool.Name == "find_agencies" {
			foundAgencies = true
		}
		if tool.Name == "find_companies" {
			foundCompanies = true
		}
	}
	if !foundAggregate {
		t.Fatalf("aggregate_contracts tool missing from list response")
	}
	if !foundIdentify {
		t.Fatalf("identify_jurisdiction tool missing from list response")
	}
	if !foundAgencies {
		t.Fatalf("find_agencies tool missing from list response")
	}
	if !foundCompanies {
		t.Fatalf("find_companies tool missing from list response")
	}
}

func writeTestCatalog(t *testing.T, cacheDir string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(cacheDir, "catalog.sqlite"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE parquet_files (
			path TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			fy TEXT NOT NULL,
			agency_key TEXT NOT NULL,
			agency_name TEXT NOT NULL,
			company_key TEXT NOT NULL,
			company_name TEXT NOT NULL,
			row_count INTEGER NOT NULL,
			created_at TEXT NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO parquet_files(path, source, fy, agency_key, agency_name, company_key, company_name, row_count, created_at)
		VALUES
		('p1', 'federal', '2024-25', 'department_of_defence', 'Department of Defence', 'acme', 'Acme Pty Ltd', 100, 'now'),
		('p2', 'federal', '2024-25', 'ato', 'Australian Taxation Office', 'kpmg', 'KPMG', 50, 'now');
	`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
}

func TestMCPStreamable_IdentifyJurisdictionCall(t *testing.T) {
	handler := buildMCPHTTPHandler()
	sessionID := initializeTestMCPSession(t, handler)

	resp := sendJSONRPCRequest(t, handler, sessionID, map[string]any{
		"jsonrpc": "2.0",
		"id":      42,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "identify_jurisdiction",
			"arguments": map[string]any{
				"text": "Compare spend in Western Australia",
			},
		},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	var body struct {
		Result struct {
			Structured struct {
				Source   string `json:"source"`
				Evidence string `json:"evidence"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid response: %v", err)
	}
	if body.Result.Structured.Source != "wa" {
		t.Fatalf("unexpected source: %+v", body.Result.Structured)
	}
	if body.Result.Structured.Evidence == "" {
		t.Fatalf("expected evidence to be populated")
	}
}

func TestMCPStreamable_FindAgenciesCall(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("AUSTENDER_CACHE_DIR", cacheDir)
	writeTestCatalog(t, cacheDir)

	handler := buildMCPHTTPHandler()
	sessionID := initializeTestMCPSession(t, handler)

	resp := sendJSONRPCRequest(t, handler, sessionID, map[string]any{
		"jsonrpc": "2.0",
		"id":      43,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "find_agencies",
			"arguments": map[string]any{
				"source": "federal",
				"query":  "defence",
				"limit":  5,
			},
		},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	var body struct {
		Result struct {
			Structured struct {
				CatalogAvailable bool `json:"catalogAvailable"`
				Candidates       []struct {
					Name string `json:"name"`
				} `json:"candidates"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid response: %v", err)
	}
	if !body.Result.Structured.CatalogAvailable {
		t.Fatalf("expected catalogAvailable=true")
	}
	if len(body.Result.Structured.Candidates) == 0 {
		t.Fatalf("expected at least 1 candidate")
	}
	if body.Result.Structured.Candidates[0].Name != "Department of Defence" {
		t.Fatalf("unexpected top candidate: %+v", body.Result.Structured.Candidates[0])
	}
}

func TestMCPStreamable_FindCompaniesCall(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("AUSTENDER_CACHE_DIR", cacheDir)
	writeTestCatalog(t, cacheDir)

	handler := buildMCPHTTPHandler()
	sessionID := initializeTestMCPSession(t, handler)

	resp := sendJSONRPCRequest(t, handler, sessionID, map[string]any{
		"jsonrpc": "2.0",
		"id":      44,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "find_companies",
			"arguments": map[string]any{
				"source": "federal",
				"query":  "kpmg",
				"limit":  5,
			},
		},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	var body struct {
		Result struct {
			Structured struct {
				CatalogAvailable bool `json:"catalogAvailable"`
				Candidates       []struct {
					Name string `json:"name"`
				} `json:"candidates"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid response: %v", err)
	}
	if !body.Result.Structured.CatalogAvailable {
		t.Fatalf("expected catalogAvailable=true")
	}
	if len(body.Result.Structured.Candidates) == 0 {
		t.Fatalf("expected at least 1 candidate")
	}
	if body.Result.Structured.Candidates[0].Name != "KPMG" {
		t.Fatalf("unexpected top candidate: %+v", body.Result.Structured.Candidates[0])
	}
}

func TestMCPStreamable_AggregateCall(t *testing.T) {
	old := runScrape
	runScrape = func(ctx context.Context, req collector.SearchRequest) (string, error) {
		if req.Keyword != "Lockheed" || req.LookbackPeriod != 5 {
			t.Fatalf("unexpected aggregate request: %+v", req)
		}
		return "$42.00", nil
	}
	defer func() { runScrape = old }()

	handler := buildMCPHTTPHandler()
	sessionID := initializeTestMCPSession(t, handler)
	resp := sendJSONRPCRequest(t, handler, sessionID, map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "aggregate_contracts",
			"arguments": map[string]any{
				"keyword":        "Lockheed",
				"lookbackPeriod": 5,
			},
		},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
	var body struct {
		Result struct {
			Structured struct {
				Total string `json:"total"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid response: %v", err)
	}
	if body.Result.Structured.Total != "$42.00" {
		t.Fatalf("unexpected aggregate total: %+v", body.Result.Structured)
	}
}

func TestMCPStreamable_ProxyCall(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/findByDates/") {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"releases":[]}`))
	}))
	defer ts.Close()
	oldClient := mcpHTTPClient
	mcpHTTPClient = ts.Client()
	defer func() { mcpHTTPClient = oldClient }()
	t.Setenv("AUSTENDER_OCDS_BASE_URL", ts.URL)

	handler := buildMCPHTTPHandler()
	sessionID := initializeTestMCPSession(t, handler)
	resp := sendJSONRPCRequest(t, handler, sessionID, map[string]any{
		"jsonrpc": "2.0",
		"id":      4,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "proxy_ocds",
			"arguments": map[string]any{
				"startDate": "2024-01-01",
				"endDate":   "2024-01-31",
			},
		},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
	var body struct {
		Result struct {
			Structured struct {
				Response json.RawMessage `json:"response"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid response: %v", err)
	}
	if len(body.Result.Structured.Response) == 0 {
		t.Fatalf("expected proxy payload in structured response")
	}
}

func initializeTestMCPSession(t *testing.T, handler http.Handler) string {
	t.Helper()
	resp := sendJSONRPCRequest(t, handler, "", map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{},
			"clientInfo": map[string]string{
				"name":    "tests",
				"version": "0.0.1",
			},
		},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("initialize failed: %d %s", resp.Code, resp.Body.String())
	}
	sessionID := resp.Header().Get(mcpserver.HeaderKeySessionID)
	if sessionID == "" {
		t.Fatalf("expected session id in response headers")
	}
	return sessionID
}

func sendJSONRPCRequest(t *testing.T, handler http.Handler, sessionID string, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if sessionID != "" {
		req.Header.Set(mcpserver.HeaderKeySessionID, sessionID)
	}
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	return resp
}
