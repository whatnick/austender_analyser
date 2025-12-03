package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	collector "github.com/whatnick/austender_analyser/collector/cmd"
)

type reqBody struct {
	Keyword     string `json:"keyword"`
	Company     string `json:"company,omitempty"`
	CompanyName string `json:"companyName,omitempty"`
	Agency      string `json:"agency,omitempty"`
	StartDate   string `json:"startDate,omitempty"`
	EndDate     string `json:"endDate,omitempty"`
	DateType    string `json:"dateType,omitempty"`
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
