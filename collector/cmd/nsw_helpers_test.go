package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/require"
)

func TestIsNswWafChallenge(t *testing.T) {
	require.False(t, isNswWafChallenge(nil))
	require.True(t, isNswWafChallenge([]byte(`<script>awswafcookiedomainlist</script>`)))
	require.True(t, isNswWafChallenge([]byte(`{"goKuProps":true}`)))
}

func TestExtractNswDetails(t *testing.T) {
	html := `
    <div class="card">
      <dl class="details">
        <dt>Agency</dt>
        <dd> NSW Health </dd>
        <dt>Contractor Name</dt>
        <dd>Example Pty Ltd</dd>
        <dt>Publish Date</dt>
        <dd>01-Jan-2024</dd>
      </dl>
    </div>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	require.NoError(t, err)

	got := extractNswDetails(doc.Selection)
	require.Equal(t, "NSW Health", got["agency"])
	require.Equal(t, "Example Pty Ltd", got["contractor name"])
	require.Equal(t, "01-Jan-2024", got["publish date"])
}

func TestParseNswContractPeriodRange(t *testing.T) {
	start, end := parseNswContractPeriod("01-Jan-2024 to 15-Jan-2024")
	require.Equal(t, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), start)
	require.Equal(t, time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), end)

	s, e := parseNswContractPeriod("")
	require.True(t, s.IsZero())
	require.True(t, e.IsZero())
}
