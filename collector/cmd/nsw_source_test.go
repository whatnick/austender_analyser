package cmd

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBuildNswSearchURL(t *testing.T) {
	req := SearchRequest{Keyword: "Deloitte"}
	from := time.Date(1986, time.January, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, time.September, 16, 0, 0, 0, 0, time.UTC)
	url := buildNswSearchURL(req, 1, from, to)
	require.Contains(t, url, "buy.nsw.gov.au/notices/search")
	require.Contains(t, url, "mode=advanced")
	require.Contains(t, url, "noticeTypes=can")
	require.Contains(t, url, "query=Deloitte")
	require.Contains(t, url, "dateFrom=1986-01-01")
	require.Contains(t, url, "dateTo=2025-09-16")
	require.Contains(t, url, "page=1")
}

func TestBuildNswSearchURLWithAgenciesUUID(t *testing.T) {
	req := SearchRequest{Agency: "7b7678cd-ae5c-4a0a-9a7c-57970cfb4b31"}
	from := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, time.February, 1, 0, 0, 0, 0, time.UTC)
	url := buildNswSearchURL(req, 1, from, to)
	require.Contains(t, url, "agencies=7b7678cd-ae5c-4a0a-9a7c-57970cfb4b31")
}

func TestExtractNswNoticeID(t *testing.T) {
	noticeID := extractNswNoticeID("https://buy.nsw.gov.au/notices/7B7678CD-AE5C-4A0A-9A7C57970CFB4B31")
	require.Equal(t, "7B7678CD-AE5C-4A0A-9A7C57970CFB4B31", noticeID)
}

func TestParseNswDate(t *testing.T) {
	d := parseNswDate("29-Jul-2025")
	require.Equal(t, time.Date(2025, time.July, 29, 0, 0, 0, 0, time.UTC), d)
}

func TestParseNswContractPeriod(t *testing.T) {
	start, end := parseNswContractPeriod("9-Dec-2022 to 31-Dec-2025")
	require.Equal(t, time.Date(2022, time.December, 9, 0, 0, 0, 0, time.UTC), start)
	require.Equal(t, time.Date(2025, time.December, 31, 0, 0, 0, 0, time.UTC), end)
}
