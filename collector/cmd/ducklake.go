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

// cacheQueryCmd executes DuckDB (or Duck Lake) queries against cached parquet files.
// It shells out to the local duckdb binary to avoid CGO requirements. If duckdb is
// unavailable, the command explains how to install it and exits with an error.
var cacheQueryCmd = &cobra.Command{
	Use:   "query",
	Short: "Run DuckDB query over cached parquet files",
	RunE: func(cmd *cobra.Command, args []string) error {
		cacheDir, _ := cmd.Flags().GetString("cache-dir")
		sql, _ := cmd.Flags().GetString("sql")
		limit, _ := cmd.Flags().GetInt("limit")

		if sql == "" {
			sql = defaultAnalyticsSQL(cacheDir, limit)
		}

		return runDuckDBQuery(cmd.Context(), cacheDir, sql)
	},
}

func init() {
	cacheCmd.AddCommand(cacheQueryCmd)
	cacheQueryCmd.Flags().String("cache-dir", defaultCacheDir(), "Cache directory containing parquet/ and catalog.sqlite")
	cacheQueryCmd.Flags().String("sql", "", "Custom DuckDB SQL to run; defaults to agency/year aggregation")
	cacheQueryCmd.Flags().Int("limit", 20, "Row limit for default aggregation")
}

func runDuckDBQuery(ctx context.Context, cacheDir, sql string) error {
	if cacheDir == "" {
		cacheDir = defaultCacheDir()
	}
	lakeDir := filepath.Join(cacheDir, "lake")
	if _, err := os.Stat(lakeDir); err != nil {
		return fmt.Errorf("lake not found; run `austender cache` or `task collector:prime-lake` first: %w", err)
	}
	duckPath, err := exec.LookPath("duckdb")
	if err != nil {
		return fmt.Errorf("duckdb CLI not found in PATH; install from https://duckdb.org/docs/installation")
	}

	// DuckDB supports globbing; we scan all parquet parts under the cache.
	parquetGlob := filepath.Join(cacheDir, "lake", "**", "*.parquet")
	sql = strings.ReplaceAll(sql, "{{PARQUET_GLOB}}", parquetGlob)

	cmd := exec.CommandContext(ctx, duckPath, "-json", "-c", sql)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func defaultAnalyticsSQL(cacheDir string, limit int) string {
	return fmt.Sprintf(`
with data as (
  select
		financial_year,
		agency,
    sum(amount) as total_amount,
    count(*) as records
  from parquet_scan('{{PARQUET_GLOB}}')
  group by 1,2
)
select * from data order by total_amount desc limit %d;`, limit)
}

// DuckLake compatibility: produce a JSON manifest of parquet locations for clients
// that want to mount the cache via WASM DuckDB. Useful for MCP agents.
type DucklakeManifest struct {
	GeneratedAt time.Time `json:"generated_at"`
	ParquetGlob string    `json:"parquet_glob"`
	Notes       string    `json:"notes,omitempty"`
}

func BuildDucklakeManifest(cacheDir string) ([]byte, error) {
	if cacheDir == "" {
		cacheDir = defaultCacheDir()
	}
	m := DucklakeManifest{
		GeneratedAt: time.Now().UTC(),
		ParquetGlob: filepath.Join(cacheDir, "parquet", "**", "*.parquet"),
		Notes:       "Mount parquet_glob in DuckDB/DuckLake to run client-side analytics.",
	}
	return json.MarshalIndent(m, "", "  ")
}
