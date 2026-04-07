package cmd

import (
	"context"
	"os"
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
	require.Contains(t, url, "supplierName=")
	require.Contains(t, url, "orderBy=startDate")
	require.Contains(t, url, "browse=false")
	require.Contains(t, url, "page=")
}

func TestVicRunBrowserFallbackPreservesMonthWindows(t *testing.T) {
	t.Setenv("VIC_USE_BROWSER", "true")
	original := runVicWithBrowserFunc
	defer func() {
		runVicWithBrowserFunc = original
	}()

	var capturedReq SearchRequest
	var capturedWindows []dateWindow
	runVicWithBrowserFunc = func(ctx context.Context, req SearchRequest, windows []dateWindow) (string, error) {
		capturedReq = req
		capturedWindows = append([]dateWindow(nil), windows...)
		return "$0.00", nil
	}

	start := time.Date(2024, time.January, 15, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, time.March, 20, 0, 0, 0, 0, time.UTC)

	res, err := newVicSource().Run(context.Background(), SearchRequest{
		Source:    vicSourceID,
		StartDate: start,
		EndDate:   end,
	})
	require.NoError(t, err)
	require.Equal(t, "$0.00", res)
	require.Equal(t, start, capturedReq.StartDate)
	require.Equal(t, end, capturedReq.EndDate)
	require.Len(t, capturedWindows, 3)
	require.Equal(t, time.Date(2024, time.January, 15, 0, 0, 0, 0, time.UTC), capturedWindows[0].start)
	require.Equal(t, time.Date(2024, time.January, 31, 0, 0, 0, 0, time.UTC), capturedWindows[0].end)
	require.Equal(t, time.Date(2024, time.February, 1, 0, 0, 0, 0, time.UTC), capturedWindows[1].start)
	require.Equal(t, time.Date(2024, time.February, 29, 0, 0, 0, 0, time.UTC), capturedWindows[1].end)
	require.Equal(t, time.Date(2024, time.March, 1, 0, 0, 0, 0, time.UTC), capturedWindows[2].start)
	require.Equal(t, time.Date(2024, time.March, 20, 0, 0, 0, 0, time.UTC), capturedWindows[2].end)
	_, _ = os.LookupEnv("VIC_USE_BROWSER")
}
