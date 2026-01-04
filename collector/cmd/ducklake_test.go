package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultAnalyticsSQL(t *testing.T) {
	sql := defaultAnalyticsSQL("/tmp/cache", 25)
	require.Contains(t, sql, "parquet_scan")
	require.Contains(t, sql, "limit 25")
}

func TestBuildDucklakeManifest(t *testing.T) {
	raw, err := BuildDucklakeManifest("/var/cache")
	require.NoError(t, err)

	var manifest DucklakeManifest
	require.NoError(t, json.Unmarshal(raw, &manifest))
	require.True(t, strings.HasSuffix(manifest.ParquetGlob, "parquet/**/*.parquet"))
	require.NotZero(t, manifest.GeneratedAt.Unix())
}
