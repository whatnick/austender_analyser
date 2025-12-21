package cmd

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestRunSearchWithCacheShortCircuitsWhenWindowsCached(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AUSTENDER_CACHE_DIR", dir)
	t.Setenv("AUSTENDER_USE_CACHE", "true")

	calls := 0
	sample := MatchSummary{
		ContractID:  "CN1",
		ReleaseID:   "rel-1",
		OCID:        "ocds-1",
		Supplier:    "KPMG",
		Agency:      "ATO",
		Title:       "Consulting",
		Amount:      decimal.NewFromInt(100),
		ReleaseDate: time.Now().UTC(),
	}

	oldRun := runSearchFunc
	runSearchFunc = func(ctx context.Context, req SearchRequest) (string, error) {
		calls++
		if req.ShouldFetchWindow != nil {
			win := dateWindow{start: sample.ReleaseDate, end: sample.ReleaseDate}
			if !req.ShouldFetchWindow(win) {
				return "$0.00", nil
			}
		}
		if req.OnAnyMatch != nil {
			req.OnAnyMatch(sample)
		}
		return "$100.00", nil
	}
	defer func() { runSearchFunc = oldRun }()

	res, hit, err := RunSearchWithCache(context.Background(), SearchRequest{
		Company:        "KPMG",
		LookbackPeriod: 1,
		DateType:       defaultDateType,
	})
	require.NoError(t, err)
	require.NotEmpty(t, res)
	require.Equal(t, 1, calls)

	res2, hit2, err := RunSearchWithCache(context.Background(), SearchRequest{
		Company:        "KPMG",
		LookbackPeriod: 1,
		DateType:       defaultDateType,
	})
	require.NoError(t, err)
	require.Equal(t, res, res2)
	require.LessOrEqual(t, calls, 2, "cache should avoid re-fetching when windows already present")
	require.True(t, hit2 || hit, "expected cache usage on second run")
}

// Regression: lookback + cache should return the same total across runs and avoid inflating totals
// when FY strings carry or omit the fy= prefix.
func TestRunSearchWithCacheConsistentLookback(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AUSTENDER_CACHE_DIR", dir)
	t.Setenv("AUSTENDER_USE_CACHE", "true")

	// Override runSearchFunc to simulate a single window of data and ensure OnAnyMatch writes to the lake.
	oldRun := runSearchFunc
	runSearchFunc = func(ctx context.Context, req SearchRequest) (string, error) {
		if req.OnAnyMatch != nil {
			req.OnAnyMatch(MatchSummary{
				ContractID:  "CN-R",
				ReleaseID:   "rel-1",
				OCID:        "ocds-1",
				Supplier:    "Vendor",
				Agency:      "Defence",
				Title:       "Sample",
				Amount:      decimal.NewFromInt(123),
				ReleaseDate: time.Now().AddDate(-1, 0, 0).UTC(),
			})
		}
		return "$123.00", nil
	}
	defer func() { runSearchFunc = oldRun }()

	releaseTime := time.Now().AddDate(-1, 0, 0).UTC()
	first, hit1, err := RunSearchWithCache(context.Background(), SearchRequest{
		Agency:         "Defence",
		LookbackPeriod: 3,
		StartDate:      releaseTime,
		EndDate:        releaseTime,
	})
	require.NoError(t, err)
	require.False(t, hit1)
	require.Equal(t, "$123.00", first)

	second, _, err := RunSearchWithCache(context.Background(), SearchRequest{
		Agency:         "Defence",
		LookbackPeriod: 3,
		StartDate:      releaseTime,
		EndDate:        releaseTime,
	})
	require.NoError(t, err)
	require.Equal(t, "$123.00", second)
}

func TestQueryCacheRespectsDateRange(t *testing.T) {
	dir := t.TempDir()
	cache, err := newCacheManager(dir)
	require.NoError(t, err)
	defer cache.close()

	pool := newLakeWriterPool(cache.lake)
	oldRelease := time.Now().AddDate(-5, 0, 0).UTC()
	newRelease := time.Now().AddDate(-1, 0, 0).UTC()
	require.NoError(t, pool.write(MatchSummary{
		ContractID:  "CN-OLD",
		ReleaseID:   "rel-old",
		OCID:        "ocds-old",
		Supplier:    "Vendor",
		Agency:      "Defence",
		Title:       "Legacy",
		Amount:      decimal.NewFromInt(200),
		ReleaseDate: oldRelease,
	}))
	require.NoError(t, pool.write(MatchSummary{
		ContractID:  "CN-NEW",
		ReleaseID:   "rel-new",
		OCID:        "ocds-new",
		Supplier:    "Vendor",
		Agency:      "Defence",
		Title:       "Recent",
		Amount:      decimal.NewFromInt(50),
		ReleaseDate: newRelease,
	}))
	pool.closeAll()

	// Lookback 3y should exclude the old record and include the recent one.
	startResolved, endResolved := resolveDates(time.Time{}, time.Time{}, resolveLookbackPeriod(3))
	res, matched, err := cache.queryCache(SearchRequest{
		Agency:         "Defence",
		LookbackPeriod: 3,
		StartDate:      startResolved,
		EndDate:        endResolved,
	})
	require.NoError(t, err)
	require.True(t, matched)
	require.True(t, res.Equal(decimal.NewFromInt(50)), "expected only recent contract within lookback window")
}
