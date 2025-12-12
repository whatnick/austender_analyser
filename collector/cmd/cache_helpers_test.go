package cmd

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRowMatchesFilters(t *testing.T) {
	row := parquetRow{
		Source:       defaultSourceID,
		Supplier:     "Acme Pty Ltd",
		Agency:       "ATO",
		Title:        "Audit and advisory",
		ContractID:   "CN-1",
		ReleaseEpoch: time.Date(2024, time.July, 1, 0, 0, 0, 0, time.UTC).UnixMilli(),
	}

	tests := []struct {
		name    string
		filter  SearchRequest
		expects bool
	}{
		{"no filters", SearchRequest{}, true},
		{"keyword hit", SearchRequest{Keyword: "audit"}, true},
		{"keyword miss", SearchRequest{Keyword: "travel"}, false},
		{"company hit", SearchRequest{Company: "acme"}, true},
		{"company miss", SearchRequest{Company: "other"}, false},
		{"agency hit", SearchRequest{Agency: "ato"}, true},
		{"agency miss", SearchRequest{Agency: "dva"}, false},
		{"source match", SearchRequest{Source: defaultSourceID}, true},
		{"source miss", SearchRequest{Source: "vic"}, false},
		{"before start", SearchRequest{StartDate: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}, false},
		{"after end", SearchRequest{EndDate: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expects, rowMatches(row, tt.filter))
		})
	}
}

func TestPartitionHelpers(t *testing.T) {
	base := time.Date(2024, time.July, 10, 0, 0, 0, 0, time.UTC)
	require.Contains(t, partitionKey(base, "ATO"), "fy=2024-25")
	path := partitionKeyLake(base, defaultSourceID, "ATO", "ACME & Co")
	require.Contains(t, path, "source=federal")
	require.Contains(t, path, "fy=2024-25")
	require.Contains(t, path, "agency=ato")
	require.Contains(t, path, "company=acme__co")
	require.Equal(t, "month=2024-07", monthLabel(base))
}

func TestResolveTimeoutAndRetry(t *testing.T) {
	t.Setenv("AUSTENDER_REQUEST_TIMEOUT", "150ms")
	require.Equal(t, 150*time.Millisecond, resolveTimeout())
	t.Setenv("AUSTENDER_REQUEST_TIMEOUT", "bad")
	require.Equal(t, time.Duration(defaultRequestTimeout), resolveTimeout())

	require.True(t, shouldRetryStatus(500))
	require.True(t, shouldRetryStatus(429))
	require.False(t, shouldRetryStatus(404))
}

func TestCacheKeyIncludesSource(t *testing.T) {
	base := cacheKey("k", "c", "a", "d", defaultSourceID)
	alt := cacheKey("k", "c", "a", "d", "vic")
	require.NotEqual(t, base, alt)
}

func TestSplitDateWindows(t *testing.T) {
	start := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 62)
	windows := splitDateWindows(start, end, 31)
	require.Len(t, windows, 2)
	require.Equal(t, start, windows[0].start)
	require.Equal(t, start.AddDate(0, 0, 31), windows[0].end)
	require.Equal(t, end, windows[1].end)
}
