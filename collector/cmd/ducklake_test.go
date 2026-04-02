package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

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
