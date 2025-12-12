package cmd

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestResolveDates(t *testing.T) {
	now := time.Date(2025, time.December, 12, 0, 0, 0, 0, time.UTC)
	t.Run("defaults to lookback when empty", func(t *testing.T) {
		start, end := resolveDates(time.Time{}, time.Time{}, 3)
		require.WithinDuration(t, time.Now().UTC(), end, 2*time.Second)
		require.Equal(t, end.AddDate(-3, 0, 0).Unix(), start.Unix())
	})

	t.Run("swaps inverted range", func(t *testing.T) {
		start := now
		end := now.AddDate(-1, 0, 0)
		gotStart, gotEnd := resolveDates(start, end, 0)
		require.True(t, gotStart.Before(gotEnd))
		require.Equal(t, gotEnd, start.UTC())
		require.Equal(t, gotStart, end.UTC())
	})
}

func TestWindowsCached(t *testing.T) {
	dir := t.TempDir()
	mgr, err := newCacheManager(dir)
	require.NoError(t, err)
	defer mgr.close()

	now := time.Date(2024, time.July, 15, 0, 0, 0, 0, time.UTC)
	summary := MatchSummary{
		ContractID:  "CN-test",
		ReleaseID:   "rel-test",
		OCID:        "ocds-test",
		Supplier:    "Acme",
		Agency:      "ATO",
		Title:       "Consulting",
		Amount:      parseDecimal(t, "10"),
		ReleaseDate: now,
	}
	pool := newLakeWriterPool(mgr.lake)
	require.NoError(t, pool.write(summary))
	pool.closeAll()

	start := now.AddDate(-1, 0, 0)
	end := now.AddDate(0, 1, 0)
	require.False(t, mgr.lake.shouldFetchWindow(defaultSourceID, dateWindow{start: now, end: now.AddDate(0, 0, maxWindowDays)}))
	// windowsCached expects full coverage of every window; ensure we query only the month we wrote.
	require.True(t, windowsCached(mgr.lake, defaultSourceID, now, now))
	require.False(t, windowsCached(mgr.lake, "vic", now, now))
	require.False(t, windowsCached(mgr.lake, defaultSourceID, start, end))
}

func parseDecimal(t *testing.T, v string) decimal.Decimal {
	d, err := decimal.NewFromString(v)
	require.NoError(t, err)
	return d
}
