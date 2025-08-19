package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	collector "github.com/whatnick/austender_analyser/collector/cmd"
)

type ScrapeRequest struct {
	Keyword     string `json:"keyword"`
	Company     string `json:"company,omitempty"`
	CompanyName string `json:"companyName,omitempty"`
	Agency      string `json:"agency,omitempty"`
}

type ScrapeResponse struct {
	Result string `json:"result"`
}

// function indirection for easier testing
var runScrape = collector.RunScrape

func scrapeHandler(w http.ResponseWriter, r *http.Request) {
	// Basic CORS headers for browser requests (including file:// origins)
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	start := time.Now()
	var req ScrapeRequest
	decErr := json.NewDecoder(r.Body).Decode(&req)
	if decErr != nil || req.Keyword == "" {
		// Fallback to form values (e.g., if frontend posted URL-encoded)
		_ = r.ParseForm()
		if req.Keyword == "" {
			req.Keyword = r.Form.Get("keyword")
		}
		if req.Company == "" {
			req.Company = r.Form.Get("company")
		}
		if req.CompanyName == "" {
			req.CompanyName = r.Form.Get("companyName")
		}
		if req.Agency == "" {
			req.Agency = r.Form.Get("agency")
		}
		if req.Keyword == "" {
			log.Printf("bad request: decodeErr=%v method=%s path=%s", decErr, r.Method, r.URL.Path)
			http.Error(w, "Invalid request", http.StatusBadRequest)
			return
		}
	}

	company := req.Company
	if company == "" {
		company = req.CompanyName
	}

	// Reuse collector logic directly (indirection for testability)
	total, err := runScrape(req.Keyword, company, req.Agency)
	if err != nil {
		log.Printf("collector error: keyword=%q company=%q agency=%q err=%v", req.Keyword, company, req.Agency, err)
		http.Error(w, "Error running collector", http.StatusInternalServerError)
		return
	}

	resp := ScrapeResponse{Result: total}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)

	log.Printf("%s %s -> 200 in %s (keyword=%q company=%q agency=%q)", r.Method, r.URL.Path, time.Since(start), req.Keyword, company, req.Agency)
}

func RegisterHandlers() {
	http.HandleFunc("/api/scrape", scrapeHandler)
}
