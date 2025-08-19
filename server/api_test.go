package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type reqBody struct {
	Keyword string `json:"keyword"`
	Company string `json:"company,omitempty"`
	CompanyName string `json:"companyName,omitempty"`
	Agency  string `json:"agency,omitempty"`
}

func TestScrapeHandler_OK(t *testing.T) {
	// stub the runScrape function
	old := runScrape
	runScrape = func(keyword, company, agency string) (string, error) {
		if keyword != "KPMG" {
			t.Fatalf("unexpected keyword: %s", keyword)
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
	runScrape = func(keyword, company, agency string) (string, error) {
		if keyword != "EY" || company != "Ernst & Young" || agency != "ATO" {
			t.Fatalf("unexpected params: %s, %s, %s", keyword, company, agency)
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
	runScrape = func(keyword, company, agency string) (string, error) {
		if keyword != "EY" || company != "EY Pty Ltd" || agency != "" {
			t.Fatalf("unexpected params: %s, %s, %s", keyword, company, agency)
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
