package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/require"
)

func TestIsSaCloudflareBlocked(t *testing.T) {
	require.False(t, isSaCloudflareBlocked("normal page"))
	require.True(t, isSaCloudflareBlocked("Attention Required - Cloudflare"))
	require.True(t, isSaCloudflareBlocked("__cf_chl page"))
}

func TestParseSaDateVariants(t *testing.T) {
	require.Equal(t, time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC), parseSaDate("01/09/2024"))
	require.True(t, parseSaDate("invalid").IsZero())
}

func TestFindSaResultsTable(t *testing.T) {
	html := `
    <table>
      <thead><tr><th>Contract Reference</th><th>Buyer</th><th>Value</th></tr></thead>
      <tbody>
        <tr><td>CN1</td><td>Agency</td><td>$100</td></tr>
        <tr><td>CN2</td><td>Agency</td><td>$200</td></tr>
      </tbody>
    </table>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	require.NoError(t, err)

	table, idx := findSaResultsTable(doc)
	require.NotNil(t, table)
	require.Equal(t, 2, table.Find("tbody tr").Length())
	require.Contains(t, idx, "contract")
	require.Contains(t, idx, "buyer")
}
