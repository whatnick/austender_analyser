package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// cacheQueryCmd executes ClickHouse-local queries against cached parquet files.
// It shells out to the local clickhouse binary to avoid adding a long-running server
// requirement to the collector path.
var cacheQueryCmd = &cobra.Command{
	Use:   "query",
	Short: "Run ClickHouse query over cached parquet files",
	RunE: func(cmd *cobra.Command, args []string) error {
		cacheDir, _ := cmd.Flags().GetString("cache-dir")
		sql, _ := cmd.Flags().GetString("sql")
		limit, _ := cmd.Flags().GetInt("limit")

		if sql == "" {
			sql = defaultAnalyticsSQL(cacheDir, limit)
		}

		return runClickHouseLocalQuery(cmd.Context(), cacheDir, sql)
	},
}

func init() {
	cacheCmd.AddCommand(cacheQueryCmd)
	cacheQueryCmd.Flags().String("cache-dir", defaultCacheDir(), "Cache directory containing lake/ and clickhouse-index.json")
	cacheQueryCmd.Flags().String("sql", "", "Custom ClickHouse SQL to run; defaults to agency/year aggregation")
	cacheQueryCmd.Flags().Int("limit", 20, "Row limit for default aggregation")
}

// Requires clickhouse-local CLI; skip coverage in unit tests.
//
//go:nocover
func runClickHouseLocalQuery(ctx context.Context, cacheDir, sql string) error {
	if cacheDir == "" {
		cacheDir = defaultCacheDir()
	}
	lakeDir := filepath.Join(cacheDir, "lake")
	if _, err := os.Stat(lakeDir); err != nil {
		return fmt.Errorf("lake not found; run `austender cache` or `task collector:prime-lake` first: %w", err)
	}
	clickhousePath, err := exec.LookPath(envOrDefault("AUSTENDER_CLICKHOUSE_LOCAL_BIN", "clickhouse-local"))
	if err != nil {
		return fmt.Errorf("clickhouse-local CLI not found in PATH; install ClickHouse local tools or set AUSTENDER_CLICKHOUSE_LOCAL_BIN")
	}

	parquetGlob := clickHouseLakeGlob(cacheDir)
	sql = strings.ReplaceAll(sql, "{{PARQUET_GLOB}}", parquetGlob)

	cmd := exec.CommandContext(ctx, clickhousePath, "--query", sql, "--format", "JSONEachRow")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func defaultAnalyticsSQL(cacheDir string, limit int) string {
	return fmt.Sprintf(
		"\nwith data as (\n"+
			"  select\n"+
			"    `name=financial_year` as financial_year,\n"+
			"    `name=agency` as agency,\n"+
			"    sum(`name=amount`) as total_amount,\n"+
			"    count(*) as records\n"+
			"  from file('{{PARQUET_GLOB}}', Parquet)\n"+
			"  group by 1,2\n"+
			")\n"+
			"select * from data order by total_amount desc limit %d;",
		limit,
	)
}

// ClickHouseManifest points client-side analytics tooling at the cached parquet lake.
type ClickHouseManifest struct {
	GeneratedAt time.Time `json:"generated_at"`
	ParquetGlob string    `json:"parquet_glob"`
	Format      string    `json:"format"`
	Notes       string    `json:"notes,omitempty"`
}

func BuildClickHouseManifest(cacheDir string) ([]byte, error) {
	if cacheDir == "" {
		cacheDir = defaultCacheDir()
	}
	m := ClickHouseManifest{
		GeneratedAt: time.Now().UTC(),
		ParquetGlob: clickHouseLakeGlob(cacheDir),
		Format:      "Parquet",
		Notes:       "Use parquet_glob with clickhouse-local file(..., Parquet) for client-side analytics.",
	}
	return json.MarshalIndent(m, "", "  ")
}

func clickHouseLakeGlob(cacheDir string) string {
	return filepath.Join(cacheDir, "lake", "source=*", "fy=*", "month=*", "agency=*", "company=*", "*.parquet")
}
