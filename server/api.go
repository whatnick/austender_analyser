package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	collector "github.com/whatnick/austender_analyser/collector/cmd"
)

type ScrapeRequest struct {
	Keyword     string `json:"keyword"`
	Company     string `json:"company,omitempty"`
	CompanyName string `json:"companyName,omitempty"`
	Agency      string `json:"agency,omitempty"`
	StartDate   string `json:"startDate,omitempty"`
	EndDate     string `json:"endDate,omitempty"`
	DateType    string `json:"dateType,omitempty"`
}

type ScrapeResponse struct {
	Result string `json:"result"`
}

// function indirection for easier testing
var runScrape = collector.RunSearch

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

	if req.StartDate == "" {
		req.StartDate = r.Form.Get("startDate")
	}
	if req.EndDate == "" {
		req.EndDate = r.Form.Get("endDate")
	}
	if req.DateType == "" {
		req.DateType = r.Form.Get("dateType")
	}

	company := req.Company
	if company == "" {
		company = req.CompanyName
	}

	start, err := parseRequestDate(req.StartDate)
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid startDate: %v", err), http.StatusBadRequest)
		return
	}
	end, err := parseRequestDate(req.EndDate)
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid endDate: %v", err), http.StatusBadRequest)
		return
	}

	// Reuse collector logic directly (indirection for testability)
	total, err := runScrape(r.Context(), collector.SearchRequest{
		Keyword:   req.Keyword,
		Company:   company,
		Agency:    req.Agency,
		StartDate: start,
		EndDate:   end,
		DateType:  req.DateType,
	})
	if err != nil {
		log.Printf("collector error: keyword=%q company=%q agency=%q start=%q end=%q err=%v", req.Keyword, company, req.Agency, req.StartDate, req.EndDate, err)
		http.Error(w, "Error running collector", http.StatusInternalServerError)
		return
	}

	resp := ScrapeResponse{Result: total}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)

	log.Printf("%s %s -> 200 in %s (keyword=%q company=%q agency=%q start=%q end=%q)", r.Method, r.URL.Path, time.Since(start), req.Keyword, company, req.Agency, req.StartDate, req.EndDate)
}

func parseRequestDate(raw string) (time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("expected RFC3339 or YYYY-MM-DD")
}

func RegisterHandlers() {
	http.HandleFunc("/api/scrape", scrapeHandler)
}
