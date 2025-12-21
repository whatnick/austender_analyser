package cmd

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBuildSaSearchURL(t *testing.T) {
	req := SearchRequest{Keyword: "KPMG"}
	from := time.Date(2010, time.January, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, time.December, 21, 0, 0, 0, 0, time.UTC)
	u := buildSaSearchURL(req, 1, from, to)
	require.Contains(t, u, "tenders.sa.gov.au/contract/search")
	require.Contains(t, u, "keywords=KPMG")
	require.Contains(t, u, "startDateFrom=01%2F01%2F2010")
	require.Contains(t, u, "startDateTo=21%2F12%2F2025")
	require.Contains(t, u, "page=1")
	require.Contains(t, u, "browse=false")
	require.Contains(t, u, "desc=true")
	require.Contains(t, u, "orderBy=startDate")
}

func TestBuildSaSearchURLBuyerID(t *testing.T) {
	req := SearchRequest{Keyword: "KPMG", Agency: "12345"}
	from := time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2020, time.February, 1, 0, 0, 0, 0, time.UTC)
	u := buildSaSearchURL(req, 2, from, to)
	require.Contains(t, u, "buyerId=12345")
	require.Contains(t, u, "page=2")
}

func TestParseSaDate(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Time
	}{
		{"21/12/2025", time.Date(2025, time.December, 21, 0, 0, 0, 0, time.UTC)},
		{"29 Sept 2025", time.Date(2025, time.September, 29, 0, 0, 0, 0, time.UTC)},
		{"23 July 2024", time.Date(2024, time.July, 23, 0, 0, 0, 0, time.UTC)},
		{"1 Dec 2024", time.Date(2024, time.December, 1, 0, 0, 0, 0, time.UTC)},
		{"02 Jan 2006", time.Date(2006, time.January, 2, 0, 0, 0, 0, time.UTC)},
	}

	for _, tt := range tests {
		d := parseSaDate(tt.input)
		require.Equal(t, tt.expected, d, "failed for input: %s", tt.input)
	}
}
