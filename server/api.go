package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	collector "github.com/whatnick/austender_analyser/collector/cmd"
)

type ScrapeRequest struct {
	Keyword        string `json:"keyword"`
	Company        string `json:"company,omitempty"`
	CompanyName    string `json:"companyName,omitempty"`
	Agency         string `json:"agency,omitempty"`
	StartDate      string `json:"startDate,omitempty"`
	EndDate        string `json:"endDate,omitempty"`
	DateType       string `json:"dateType,omitempty"`
	LookbackPeriod int    `json:"lookbackPeriod,omitempty"`
	UseCache       *bool  `json:"useCache,omitempty"`
}

type ScrapeResponse struct {
	Result string `json:"result"`
}

const defaultOCDSBaseURL = "https://api.tenders.gov.au/ocds"

var mcpHTTPClient = &http.Client{Timeout: 30 * time.Second}

type ocdsProxyParams struct {
	DateType  string `json:"dateType,omitempty"`
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
}

// function indirection for easier testing
var runScrape = func(ctx context.Context, req collector.SearchRequest) (string, error) {
	res, _, err := collector.RunSearchWithCache(ctx, req)
	return res, err
}

func scrapeHandler(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	start := time.Now()
	var req ScrapeRequest
	decErr := json.NewDecoder(r.Body).Decode(&req)
	if decErr != nil {
		// Fallback to form values (e.g., if frontend posted URL-encoded)
		_ = r.ParseForm()
		req.Keyword = r.Form.Get("keyword")
		if req.Company == "" {
			req.Company = r.Form.Get("company")
		}
		if req.CompanyName == "" {
			req.CompanyName = r.Form.Get("companyName")
		}
		if req.Agency == "" {
			req.Agency = r.Form.Get("agency")
		}
	}

	// If keyword still missing, allow empty to support full-lake prime queries.
	if req.Keyword == "" {
		req.Keyword = r.Form.Get("keyword")
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
	if req.UseCache == nil {
		if raw := r.Form.Get("useCache"); raw != "" {
			if v, err := strconv.ParseBool(raw); err == nil {
				req.UseCache = &v
			}
		}
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

	useCache := true
	if req.UseCache != nil {
		useCache = *req.UseCache
	}

	searchReq := collector.SearchRequest{
		Keyword:        req.Keyword,
		Company:        company,
		Agency:         req.Agency,
		StartDate:      start,
		EndDate:        end,
		DateType:       req.DateType,
		LookbackPeriod: req.LookbackPeriod,
	}

	var total string
	if useCache {
		total, err = runScrape(r.Context(), searchReq)
	} else {
		total, err = collector.RunSearch(r.Context(), searchReq)
	}
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

func setCORSHeaders(w http.ResponseWriter) {
	// Basic CORS headers for browser requests (including file:// origins)
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Mcp-Session-Id, Mcp-Protocol-Version")
}

func proxyOCDSRequest(ctx context.Context, params ocdsProxyParams, start, end time.Time) (json.RawMessage, error) {
	dateType := params.DateType
	if dateType == "" {
		dateType = "contractPublished"
	}
	base := resolveOCDSBaseURL()
	endpoint := fmt.Sprintf("%s/findByDates/%s/%s/%s",
		base,
		url.PathEscape(dateType),
		start.Format(time.RFC3339),
		end.Format(time.RFC3339),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := mcpHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ocds proxy returned %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(body), nil
}

func resolveOCDSBaseURL() string {
	base := strings.TrimSpace(os.Getenv("AUSTENDER_OCDS_BASE_URL"))
	if base == "" {
		return defaultOCDSBaseURL
	}
	return strings.TrimSuffix(base, "/")
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
	mcpHandler := buildMCPHTTPHandler()
	http.Handle("/api/mcp", mcpHandler)
	http.Handle("/api/mcp/", mcpHandler)
	http.HandleFunc("/api/llm", llmHandler)
}
