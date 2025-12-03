package cmd

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestParseDateInput(t *testing.T) {
	date, err := parseDateInput("2024-02-03")
	require.NoError(t, err)
	require.Equal(t, time.Date(2024, 2, 3, 0, 0, 0, 0, time.UTC), date)

	_, err = parseDateInput("03/02/2024")
	require.Error(t, err)
}

func TestResolveDatesDefaultLookback(t *testing.T) {
	end := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)
	start, resolvedEnd := resolveDates(time.Time{}, end, defaultLookbackYears)
	require.Equal(t, end, resolvedEnd)
	require.Equal(t, end.AddDate(-defaultLookbackYears, 0, 0), start)
}

func TestResolveDatesCustomLookback(t *testing.T) {
	end := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)
	start, _ := resolveDates(time.Time{}, end, 3)
	require.Equal(t, end.AddDate(-3, 0, 0), start)
}

func TestAggregateReleasesDedupesContracts(t *testing.T) {
	baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	releases := []ocdsRelease{
		{
			ID:   "rel-1",
			Date: baseTime.Format(time.RFC3339),
			Tag:  []string{"contract"},
			Parties: []ocdsParty{
				{Name: "Acme Pty Ltd", Roles: []string{"supplier"}},
				{Name: "ATO", Roles: []string{"buyer"}},
			},
			Contracts: []ocdsContract{
				{ID: "CN123", Value: &ocdsValue{Amount: decimal.NewFromInt(100)}},
			},
		},
		{
			ID:   "rel-2",
			Date: baseTime.Add(24 * time.Hour).Format(time.RFC3339),
			Tag:  []string{"contractAmendment"},
			Parties: []ocdsParty{
				{Name: "Acme Pty Ltd", Roles: []string{"supplier"}},
			},
			Contracts: []ocdsContract{
				{
					ID: "CN123-A1",
					Amendments: []ocdsAmendment{
						{ID: "CN123", AmendedValue: decimal.NewFromInt(150)},
					},
				},
			},
		},
	}

	agg := newContractAggregator(SearchRequest{})
	for _, rel := range releases {
		agg.process(rel)
	}
	total := agg.total()
	require.Equal(t, decimal.NewFromInt(150), total)
}

func TestMatchesFilters(t *testing.T) {
	rel := ocdsRelease{
		ID:   "CN123",
		OCID: "ocds-1",
		Tag:  []string{"contract"},
		Parties: []ocdsParty{
			{Name: "Acme Pty Ltd", Roles: []string{"supplier"}},
			{Name: "ATO", Roles: []string{"buyer"}},
		},
		Contracts: []ocdsContract{{ID: "CN123", Title: "Audit", Description: "consulting"}},
	}
	req := SearchRequest{Keyword: "CN123", Company: "acme", Agency: "ato"}
	require.True(t, matchesFilters(rel, req))
}

func TestMatchHandlerReceivesStreamingUpdates(t *testing.T) {
	baseTime := time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC)
	var summaries []MatchSummary
	req := SearchRequest{
		OnMatch: func(summary MatchSummary) {
			summaries = append(summaries, summary)
		},
	}
	agg := newContractAggregator(req)
	rel := ocdsRelease{
		ID:   "rel-1",
		Date: baseTime.Format(time.RFC3339),
		Tag:  []string{"contract"},
		Parties: []ocdsParty{
			{Name: "Vendor", Roles: []string{"supplier"}},
			{Name: "ATO", Roles: []string{"buyer"}},
		},
		Contracts: []ocdsContract{{ID: "CN1", Title: "Audit", Value: &ocdsValue{Amount: decimal.NewFromInt(100)}}},
	}
	update := rel
	update.ID = "rel-2"
	update.Date = baseTime.Add(24 * time.Hour).Format(time.RFC3339)
	update.Contracts[0].Amendments = []ocdsAmendment{{ID: "CN1", AmendedValue: decimal.NewFromInt(150)}}
	update.Tag = []string{"contractAmendment"}
	for _, r := range []ocdsRelease{rel, update} {
		agg.process(r)
	}
	require.Len(t, summaries, 2)
	require.False(t, summaries[0].IsUpdate)
	require.True(t, summaries[1].IsUpdate)
	require.Equal(t, "CN1", summaries[0].ContractID)
	require.Equal(t, baseTime, summaries[0].ReleaseDate)
	require.Equal(t, baseTime.Add(24*time.Hour), summaries[1].ReleaseDate)
	require.Equal(t, decimal.NewFromInt(150), summaries[1].Amount)
}

func TestResolveLookbackYears(t *testing.T) {
	require.Equal(t, 5, resolveLookbackYears(5))
	t.Setenv("AUSTENDER_LOOKBACK_YEARS", "7")
	require.Equal(t, 7, resolveLookbackYears(0))
	t.Setenv("AUSTENDER_LOOKBACK_YEARS", "invalid")
	require.Equal(t, defaultLookbackYears, resolveLookbackYears(0))
}
