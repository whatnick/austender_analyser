package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	collector "github.com/whatnick/austender_analyser/collector/cmd"
)

// SearchRequest is the JSON body for /api/search.
type SearchHandlerRequest struct {
	Keyword   string `json:"keyword,omitempty"`
	Company   string `json:"company,omitempty"`
	Agency    string `json:"agency,omitempty"`
	Source    string `json:"source,omitempty"`
	StartDate string `json:"startDate,omitempty"`
	EndDate   string `json:"endDate,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

func searchHandler(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	requestStart := time.Now()

	var req SearchHandlerRequest
	if r.Method == http.MethodGet {
		req.Keyword = r.URL.Query().Get("keyword")
		req.Company = r.URL.Query().Get("company")
		req.Agency = r.URL.Query().Get("agency")
		req.Source = r.URL.Query().Get("source")
		req.StartDate = r.URL.Query().Get("startDate")
		req.EndDate = r.URL.Query().Get("endDate")
		if v := r.URL.Query().Get("limit"); v != "" {
			req.Limit, _ = strconv.Atoi(v)
		}
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			_ = r.ParseForm()
			req.Keyword = r.Form.Get("keyword")
			req.Company = r.Form.Get("company")
			req.Agency = r.Form.Get("agency")
			req.Source = r.Form.Get("source")
			req.StartDate = r.Form.Get("startDate")
			req.EndDate = r.Form.Get("endDate")
			if v := r.Form.Get("limit"); v != "" {
				req.Limit, _ = strconv.Atoi(v)
			}
		}
	}

	if req.Keyword == "" && req.Company == "" && req.Agency == "" {
		sendJSONError(w, "at least one of keyword, company, or agency is required", http.StatusBadRequest)
		return
	}

	startDate, err := parseRequestDate(req.StartDate)
	if err != nil {
		sendJSONError(w, fmt.Sprintf("invalid startDate: %v", err), http.StatusBadRequest)
		return
	}
	endDate, err := parseRequestDate(req.EndDate)
	if err != nil {
		sendJSONError(w, fmt.Sprintf("invalid endDate: %v", err), http.StatusBadRequest)
		return
	}

	filters := collector.SearchRequest{
		Keyword:   req.Keyword,
		Company:   req.Company,
		Agency:    req.Agency,
		Source:    req.Source,
		StartDate: startDate,
		EndDate:   endDate,
	}

	result, err := collector.QueryContracts(r.Context(), filters, req.Limit)
	if err != nil {
		log.Printf("search error: %v", err)
		sendJSONError(w, "Error querying contracts", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)

	log.Printf("%s %s -> 200 (%d contracts) in %s (keyword=%q company=%q agency=%q source=%q)",
		r.Method, r.URL.Path, result.Count, time.Since(requestStart),
		req.Keyword, req.Company, req.Agency, req.Source)
}
