package cmd

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

type clickHouseAggregationRow struct {
	FinancialYear string  `json:"financial_year"`
	Agency        string  `json:"agency"`
	TotalAmount   float64 `json:"total_amount"`
	Records       int     `json:"records"`
}

func TestDefaultAnalyticsSQL(t *testing.T) {
	sql := defaultAnalyticsSQL("/tmp/cache", 25)
	require.Contains(t, sql, "file('")
	require.Contains(t, sql, "limit 25")
}

func TestBuildClickHouseManifest(t *testing.T) {
	raw, err := BuildClickHouseManifest("/var/cache")
	require.NoError(t, err)

	var manifest ClickHouseManifest
	require.NoError(t, json.Unmarshal(raw, &manifest))
	require.True(t, strings.HasSuffix(manifest.ParquetGlob, "lake/source=*/fy=*/month=*/agency=*/company=*/*.parquet"))
	require.Equal(t, "Parquet", manifest.Format)
	require.NotZero(t, manifest.GeneratedAt.Unix())
}

func TestClickHouseLocalSmoke(t *testing.T) {
	clickhousePath, err := exec.LookPath(envOrDefault("AUSTENDER_CLICKHOUSE_LOCAL_BIN", "clickhouse-local"))
	if err != nil {
		t.Skip("clickhouse-local not installed")
	}

	dir := t.TempDir()
	mgr, err := newCacheManager(dir)
	require.NoError(t, err)
	defer mgr.close()

	releaseDate := time.Date(2024, time.January, 15, 0, 0, 0, 0, time.UTC)
	pool := newLakeWriterPool(mgr.lake)
	require.NoError(t, pool.write(MatchSummary{
		ContractID:  "CN-CLICKHOUSE",
		ReleaseID:   "rel-clickhouse",
		OCID:        "ocds-clickhouse",
		Source:      defaultSourceID,
		Supplier:    "KPMG",
		Agency:      "Department of Defence",
		Title:       "ClickHouse Smoke Test",
		Amount:      decimal.RequireFromString("123.45"),
		ReleaseDate: releaseDate,
	}))
	pool.closeAll()

	sql := strings.ReplaceAll(defaultAnalyticsSQL(dir, 5), "{{PARQUET_GLOB}}", clickHouseLakeGlob(dir))
	cmd := exec.CommandContext(context.Background(), clickhousePath, "--query", sql, "--format", "JSONEachRow")
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "clickhouse-local failed: %s", string(out))

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	require.Len(t, lines, 1)

	var row clickHouseAggregationRow
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &row))
	require.Equal(t, "2023-24", row.FinancialYear)
	require.Equal(t, "Department of Defence", row.Agency)
	require.Equal(t, 1, row.Records)
	require.InDelta(t, 123.45, row.TotalAmount, 0.001)
}
