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
