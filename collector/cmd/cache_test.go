package cmd

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestRunSearchWithCacheShortCircuitsWhenWindowsCached(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AUSTENDER_CACHE_DIR", dir)
	t.Setenv("AUSTENDER_USE_CACHE", "true")

	cache, err := newCacheManager(dir)
	require.NoError(t, err)
	defer cache.close()

	now := time.Now().UTC()
	baseTime := time.Date(now.Year(), now.Month(), 15, 0, 0, 0, 0, time.UTC)

	pool := newLakeWriterPool(cache.lake)
	summary := MatchSummary{
		ContractID:  "CN1",
		ReleaseID:   "rel-1",
		OCID:        "ocds-1",
		Supplier:    "KPMG",
		Agency:      "ATO",
		Title:       "Consulting",
		Amount:      decimal.NewFromInt(100),
		ReleaseDate: baseTime,
	}
	require.NoError(t, pool.write(summary))
	pool.closeAll()

	var path string
	require.NoError(t, cache.db.QueryRow("SELECT path FROM parquet_files LIMIT 1").Scan(&path))
	require.True(t, strings.HasPrefix(path, dir))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Greater(t, info.Size(), int64(0))

	var companyKey string
	require.NoError(t, cache.db.QueryRow("SELECT company_key FROM parquet_files LIMIT 1").Scan(&companyKey))
	require.Equal(t, "kpmg", companyKey)

	var fy string
	require.NoError(t, cache.db.QueryRow("SELECT fy FROM parquet_files LIMIT 1").Scan(&fy))
	minFy := financialYearLabel(time.Now().AddDate(-1, 0, 0))
	var rowCount int
	require.NoError(t, cache.db.QueryRow("SELECT COUNT(*) FROM parquet_files WHERE company_key = ? AND fy >= ?", companyKey, minFy).Scan(&rowCount))
	require.Greater(t, rowCount, 0)

	dec, hit, err := sumParquetFile(path, SearchRequest{Company: "KPMG"})
	require.NoError(t, err)
	require.True(t, hit)
	require.True(t, dec.Equal(decimal.NewFromInt(100)))

	// Verify the lake lookup sees the cached row.
	sumResult, matchedDirect, err := cache.lake.queryTotals(context.Background(), SearchRequest{Company: "KPMG"})
	require.NoError(t, err)
	require.True(t, matchedDirect)
	require.True(t, sumResult.total.Equal(decimal.NewFromInt(100)))

	noLbTotal, matchedNoLb, err := cache.queryCache(SearchRequest{Company: "KPMG"})
	require.NoError(t, err)
	require.True(t, matchedNoLb)
	require.True(t, noLbTotal.Equal(decimal.NewFromInt(100)))

	total, matched, err := cache.queryCache(SearchRequest{Company: "KPMG", LookbackYears: 1})
	require.NoError(t, err)
	require.True(t, matched)
	require.True(t, total.Equal(decimal.NewFromInt(100)))

	cacheCopy, err := newCacheManager(dir)
	require.NoError(t, err)
	defer cacheCopy.close()

	totalCopy, matchedCopy, err := cacheCopy.queryCache(SearchRequest{Company: "KPMG", LookbackYears: 1})
	require.NoError(t, err)
	require.True(t, matchedCopy)
	require.True(t, totalCopy.Equal(decimal.NewFromInt(100)))

	checkpoint := baseTime.Add(12 * time.Hour)
	require.NoError(t, cache.saveCheckpoint(cacheKey("", "KPMG", "", defaultDateType), checkpoint))
	resume, err := cache.loadCheckpoint(cacheKey("", "KPMG", "", defaultDateType))
	require.NoError(t, err)
	startResolved, endResolved := resolveDates(resume, time.Time{}, resolveLookbackYears(1))
	require.Equal(t, dir, cache.lake.baseDir)
	require.True(t, cache.lake.hasMonthPartition(startResolved))
	require.True(t, windowsCached(cache.lake, startResolved, endResolved))

	called := false
	oldRun := runSearchFunc
	runSearchFunc = func(ctx context.Context, req SearchRequest) (string, error) {
		called = true
		return "$0.00", nil
	}
	defer func() { runSearchFunc = oldRun }()

	res, hit, err := RunSearchWithCache(context.Background(), SearchRequest{
		Company:       "KPMG",
		LookbackYears: 1,
		DateType:      defaultDateType,
	})
	require.NoError(t, err)
	require.True(t, hit)
	require.Equal(t, "$100.00", res)
	require.True(t, called, "expected incremental scan when some windows are missing")
}
