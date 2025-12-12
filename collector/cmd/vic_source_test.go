package cmd

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestParseVicDate(t *testing.T) {
	d := parseVicDate("30 Aug 2025")
	require.Equal(t, time.Date(2025, time.August, 30, 0, 0, 0, 0, time.UTC), d)
}

func TestParseVicAmount(t *testing.T) {
	amt := parseVicAmount("$ 1,234.56")
	require.True(t, amt.Equal(decimal.RequireFromString("1234.56")))
}

func TestMatchesSummaryFilters(t *testing.T) {
	summary := MatchSummary{
		Source:      vicSourceID,
		ContractID:  "CN-1",
		Title:       "Splunk License",
		Supplier:    "Splunk Pty Ltd",
		Agency:      "Dept of Justice",
		Amount:      decimal.NewFromInt(100),
		ReleaseDate: time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC),
	}

	req := SearchRequest{Keyword: "splunk", Company: "splunk", Agency: "justice"}
	require.True(t, matchesSummaryFilters(req, summary, time.Time{}))

	req = SearchRequest{Company: "other"}
	require.False(t, matchesSummaryFilters(req, summary, time.Time{}))

	req = SearchRequest{StartDate: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}
	require.False(t, matchesSummaryFilters(req, summary, time.Time{}))
}

func TestBuildVicSearchURL(t *testing.T) {
	req := SearchRequest{Keyword: "Splunk", Company: "Splunk", Agency: "Justice"}
	url := buildVicSearchURL(req)
	require.Contains(t, url, "keywords=Splunk")
	require.Contains(t, url, "supplierName=Splunk")
	require.Contains(t, url, "orderBy=startDate")
	require.Contains(t, url, "browse=false")
	require.Contains(t, url, "page=")
}
