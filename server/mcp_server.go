package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	mcp "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	collector "github.com/whatnick/austender_analyser/collector/cmd"
)

const (
	mcpServerName    = "Austender Aggregator MCP"
	mcpServerVersion = "1.0.0"
)

var (
	mcpHandlerOnce sync.Once
	mcpHandler     http.Handler
)

func buildMCPHTTPHandler() http.Handler {
	mcpHandlerOnce.Do(func() {
		srv := mcpserver.NewMCPServer(
			mcpServerName,
			mcpServerVersion,
			mcpserver.WithToolCapabilities(true),
		)

		registerMCPTools(srv)

		transport := mcpserver.NewStreamableHTTPServer(srv)
		mcpHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			setCORSHeaders(w)
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			transport.ServeHTTP(w, r)
		})
	})
	return mcpHandler
}

func registerMCPTools(srv *mcpserver.MCPServer) {
	aggregateTool := mcp.NewTool(
		"aggregate_contracts",
		mcp.WithDescription("Run the Austender aggregator and return the formatted total for supplied filters."),
		mcp.WithInputSchema[aggregateContractsArgs](),
		mcp.WithOutputSchema[aggregateContractsResult](),
	)
	srv.AddTool(aggregateTool, mcp.NewStructuredToolHandler(handleAggregateContracts))

	identifyTool := mcp.NewTool(
		"identify_jurisdiction",
		mcp.WithDescription("Identify the canonical jurisdiction/source from free-form text and return a short evidence string."),
		mcp.WithInputSchema[identifyJurisdictionArgs](),
		mcp.WithOutputSchema[identifyJurisdictionResult](),
	)
	srv.AddTool(identifyTool, mcp.NewStructuredToolHandler(handleIdentifyJurisdiction))

	findAgenciesTool := mcp.NewTool(
		"find_agencies",
		mcp.WithDescription("Find likely spending agencies from the local catalog (optionally scoped by source)."),
		mcp.WithInputSchema[findEntitiesArgs](),
		mcp.WithOutputSchema[findEntitiesResult](),
	)
	srv.AddTool(findAgenciesTool, mcp.NewStructuredToolHandler(handleFindAgencies))

	findCompaniesTool := mcp.NewTool(
		"find_companies",
		mcp.WithDescription("Find likely supplier companies from the local catalog (optionally scoped by source)."),
		mcp.WithInputSchema[findEntitiesArgs](),
		mcp.WithOutputSchema[findEntitiesResult](),
	)
	srv.AddTool(findCompaniesTool, mcp.NewStructuredToolHandler(handleFindCompanies))

	proxyTool := mcp.NewTool(
		"proxy_ocds",
		mcp.WithDescription("Proxy an OCDS findByDates request and return the upstream payload."),
		mcp.WithInputSchema[ocdsProxyArgs](),
		mcp.WithOutputSchema[ocdsProxyResult](),
	)
	srv.AddTool(proxyTool, mcp.NewStructuredToolHandler(handleProxyOCDS))
}

type findEntitiesArgs struct {
	Source string `json:"source,omitempty" jsonschema_description:"Jurisdiction/source identifier (e.g. federal, vic, nsw, qld, sa, wa). When omitted, searches across all sources."`
	Query  string `json:"query,omitempty" jsonschema_description:"Substring query for narrowing results (optional)."`
	Limit  int    `json:"limit,omitempty" jsonschema_description:"Maximum candidates to return (default 10, max 50)."`
}

type findEntitiesResult struct {
	CatalogAvailable bool   `json:"catalogAvailable" jsonschema_description:"Whether a local clickhouse-index.json file was available for lookup."`
	Evidence         string `json:"evidence,omitempty" jsonschema_description:"Short explanation of how candidates were selected."`
	Candidates       []struct {
		Source string `json:"source,omitempty" jsonschema_description:"Canonical source ID for the candidate."`
		Name   string `json:"name" jsonschema_description:"Display name for the entity (best effort)."`
		Key    string `json:"key,omitempty" jsonschema_description:"Normalized catalog key for the entity."`
		Rows   int64  `json:"rows,omitempty" jsonschema_description:"Approximate row volume backing the candidate (used for ranking)."`
	} `json:"candidates"`
}

func handleFindAgencies(ctx context.Context, _ mcp.CallToolRequest, args findEntitiesArgs) (findEntitiesResult, error) {
	source := collector.CanonicalSourceID(strings.TrimSpace(args.Source))
	if strings.TrimSpace(args.Source) == "" {
		source = ""
	}

	res, err := collector.FindAgenciesFromCatalog(ctx, collector.EntityLookupOptions{Source: source, Query: strings.TrimSpace(args.Query), Limit: args.Limit})
	if err != nil {
		return findEntitiesResult{}, err
	}

	out := findEntitiesResult{CatalogAvailable: res.CatalogAvailable, Evidence: res.Evidence}
	for _, c := range res.Candidates {
		out.Candidates = append(out.Candidates, struct {
			Source string `json:"source,omitempty" jsonschema_description:"Canonical source ID for the candidate."`
			Name   string `json:"name" jsonschema_description:"Display name for the entity (best effort)."`
			Key    string `json:"key,omitempty" jsonschema_description:"Normalized catalog key for the entity."`
			Rows   int64  `json:"rows,omitempty" jsonschema_description:"Approximate row volume backing the candidate (used for ranking)."`
		}{Source: c.Source, Name: c.Name, Key: c.Key, Rows: c.Rows})
	}
	return out, nil
}

func handleFindCompanies(ctx context.Context, _ mcp.CallToolRequest, args findEntitiesArgs) (findEntitiesResult, error) {
	source := collector.CanonicalSourceID(strings.TrimSpace(args.Source))
	if strings.TrimSpace(args.Source) == "" {
		source = ""
	}

	res, err := collector.FindCompaniesFromCatalog(ctx, collector.EntityLookupOptions{Source: source, Query: strings.TrimSpace(args.Query), Limit: args.Limit})
	if err != nil {
		return findEntitiesResult{}, err
	}

	out := findEntitiesResult{CatalogAvailable: res.CatalogAvailable, Evidence: res.Evidence}
	for _, c := range res.Candidates {
		out.Candidates = append(out.Candidates, struct {
			Source string `json:"source,omitempty" jsonschema_description:"Canonical source ID for the candidate."`
			Name   string `json:"name" jsonschema_description:"Display name for the entity (best effort)."`
			Key    string `json:"key,omitempty" jsonschema_description:"Normalized catalog key for the entity."`
			Rows   int64  `json:"rows,omitempty" jsonschema_description:"Approximate row volume backing the candidate (used for ranking)."`
		}{Source: c.Source, Name: c.Name, Key: c.Key, Rows: c.Rows})
	}
	return out, nil
}

type identifyJurisdictionArgs struct {
	Text string `json:"text" jsonschema:"required" jsonschema_description:"Free-form text to analyze"`
}

type identifyJurisdictionResult struct {
	Source   string `json:"source" jsonschema_description:"Canonical source ID (federal|nsw|vic|qld|sa|wa) or empty when unknown"`
	Evidence string `json:"evidence" jsonschema_description:"Short explanation of how the source was detected"`
}

func handleIdentifyJurisdiction(_ context.Context, _ mcp.CallToolRequest, args identifyJurisdictionArgs) (identifyJurisdictionResult, error) {
	text := strings.TrimSpace(args.Text)
	if text == "" {
		return identifyJurisdictionResult{}, fmt.Errorf("text is required")
	}
	source, evidence := collector.DetectSourceFromTextWithEvidence(text)
	return identifyJurisdictionResult{Source: source, Evidence: evidence}, nil
}

type aggregateContractsArgs struct {
	Keyword        string `json:"keyword" jsonschema:"required" jsonschema_description:"Keyword or entity to search across contracts"`
	Company        string `json:"company,omitempty" jsonschema_description:"Supplier filter (optional)"`
	CompanyName    string `json:"companyName,omitempty" jsonschema_description:"Alias supplier filter"`
	Agency         string `json:"agency,omitempty" jsonschema_description:"Agency filter"`
	Source         string `json:"source,omitempty" jsonschema_description:"Jurisdiction/source identifier (e.g. federal, vic, nsw, qld, sa, wa)"`
	StartDate      string `json:"startDate,omitempty" jsonschema_description:"Start date (YYYY-MM-DD or RFC3339)"`
	EndDate        string `json:"endDate,omitempty" jsonschema_description:"End date (YYYY-MM-DD or RFC3339)"`
	DateType       string `json:"dateType,omitempty" jsonschema_description:"OCDS date bucket"`
	LookbackPeriod int    `json:"lookbackPeriod,omitempty" jsonschema_description:"Fallback lookback horizon when no start date is supplied"`
}

type aggregateContractsResult struct {
	Total  string `json:"total" jsonschema_description:"Formatted total returned by the collector"`
	Source string `json:"source,omitempty" jsonschema_description:"Canonical source ID used for the aggregation"`
}

func handleAggregateContracts(ctx context.Context, _ mcp.CallToolRequest, args aggregateContractsArgs) (aggregateContractsResult, error) {
	keyword := strings.TrimSpace(args.Keyword)
	if keyword == "" {
		return aggregateContractsResult{}, fmt.Errorf("keyword is required")
	}

	company := strings.TrimSpace(args.Company)
	if company == "" {
		company = strings.TrimSpace(args.CompanyName)
	}

	start, err := parseRequestDate(args.StartDate)
	if err != nil {
		return aggregateContractsResult{}, fmt.Errorf("invalid startDate: %w", err)
	}
	end, err := parseRequestDate(args.EndDate)
	if err != nil {
		return aggregateContractsResult{}, fmt.Errorf("invalid endDate: %w", err)
	}

	source := collector.CanonicalSourceID(strings.TrimSpace(args.Source))
	if strings.TrimSpace(args.Source) == "" {
		source = ""
	}

	total, err := runScrapeCached(ctx, collector.SearchRequest{
		Keyword:        keyword,
		Company:        company,
		Agency:         strings.TrimSpace(args.Agency),
		Source:         source,
		StartDate:      start,
		EndDate:        end,
		DateType:       strings.TrimSpace(args.DateType),
		LookbackPeriod: args.LookbackPeriod,
	})
	if err != nil {
		return aggregateContractsResult{}, fmt.Errorf("aggregate_contracts failed: %w", err)
	}

	return aggregateContractsResult{Total: total, Source: source}, nil
}

type ocdsProxyArgs struct {
	DateType  string `json:"dateType,omitempty" jsonschema_description:"OCDS date bucket (defaults to contractPublished)"`
	StartDate string `json:"startDate" jsonschema:"required" jsonschema_description:"Start date (YYYY-MM-DD or RFC3339)"`
	EndDate   string `json:"endDate" jsonschema:"required" jsonschema_description:"End date (YYYY-MM-DD or RFC3339)"`
}

type ocdsProxyResult struct {
	Response json.RawMessage `json:"response" jsonschema_description:"Raw OCDS response"`
}

func handleProxyOCDS(ctx context.Context, _ mcp.CallToolRequest, args ocdsProxyArgs) (ocdsProxyResult, error) {
	start, err := parseRequestDate(args.StartDate)
	if err != nil || start.IsZero() {
		return ocdsProxyResult{}, fmt.Errorf("valid startDate is required")
	}
	end, err := parseRequestDate(args.EndDate)
	if err != nil || end.IsZero() {
		return ocdsProxyResult{}, fmt.Errorf("valid endDate is required")
	}

	payload, err := proxyOCDSRequest(ctx, ocdsProxyParams{
		DateType:  strings.TrimSpace(args.DateType),
		StartDate: args.StartDate,
		EndDate:   args.EndDate,
	}, start, end)
	if err != nil {
		return ocdsProxyResult{}, err
	}

	return ocdsProxyResult{Response: payload}, nil
}
