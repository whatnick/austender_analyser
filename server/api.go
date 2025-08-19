package main

import (
	"encoding/json"
	"net/http"

	collector "github.com/whatnick/austender_analyser/collector/cmd"
)

type ScrapeRequest struct {
	Keyword string `json:"keyword"`
	Company string `json:"company,omitempty"`
	CompanyName string `json:"companyName,omitempty"`
	Agency  string `json:"agency,omitempty"`
}

type ScrapeResponse struct {
	Result string `json:"result"`
}

// function indirection for easier testing
var runScrape = collector.RunScrape

func scrapeHandler(w http.ResponseWriter, r *http.Request) {
	var req ScrapeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("Invalid request"))
		return
	}

	company := req.Company
	if company == "" {
		company = req.CompanyName
	}

	// Reuse collector logic directly (indirection for testability)
	total, err := runScrape(req.Keyword, company, req.Agency)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Error running collector"))
		return
	}

	resp := ScrapeResponse{Result: total}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func RegisterHandlers() {
	http.HandleFunc("/api/scrape", scrapeHandler)
}
