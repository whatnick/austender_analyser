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

	proxyTool := mcp.NewTool(
		"proxy_ocds",
		mcp.WithDescription("Proxy an OCDS findByDates request and return the upstream payload."),
		mcp.WithInputSchema[ocdsProxyArgs](),
		mcp.WithOutputSchema[ocdsProxyResult](),
	)
	srv.AddTool(proxyTool, mcp.NewStructuredToolHandler(handleProxyOCDS))
}

type aggregateContractsArgs struct {
	Keyword        string `json:"keyword" jsonschema:"required" jsonschema_description:"Keyword or entity to search across contracts"`
	Company        string `json:"company,omitempty" jsonschema_description:"Supplier filter (optional)"`
	CompanyName    string `json:"companyName,omitempty" jsonschema_description:"Alias supplier filter"`
	Agency         string `json:"agency,omitempty" jsonschema_description:"Agency filter"`
	StartDate      string `json:"startDate,omitempty" jsonschema_description:"Start date (YYYY-MM-DD or RFC3339)"`
	EndDate        string `json:"endDate,omitempty" jsonschema_description:"End date (YYYY-MM-DD or RFC3339)"`
	DateType       string `json:"dateType,omitempty" jsonschema_description:"OCDS date bucket"`
	LookbackPeriod int    `json:"lookbackPeriod,omitempty" jsonschema_description:"Fallback lookback horizon when no start date is supplied"`
}

type aggregateContractsResult struct {
	Total string `json:"total" jsonschema_description:"Formatted total returned by the collector"`
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

	total, err := runScrape(ctx, collector.SearchRequest{
		Keyword:        keyword,
		Company:        company,
		Agency:         strings.TrimSpace(args.Agency),
		StartDate:      start,
		EndDate:        end,
		DateType:       strings.TrimSpace(args.DateType),
		LookbackPeriod: args.LookbackPeriod,
	})
	if err != nil {
		return aggregateContractsResult{}, fmt.Errorf("aggregate_contracts failed: %w", err)
	}

	return aggregateContractsResult{Total: total}, nil
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
