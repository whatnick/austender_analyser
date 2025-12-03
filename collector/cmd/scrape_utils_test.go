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
	start, resolvedEnd := resolveDates(time.Time{}, end)
	require.Equal(t, end, resolvedEnd)
	require.Equal(t, end.AddDate(0, 0, -defaultLookbackDays), start)
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

	total := aggregateReleases(releases, SearchRequest{})
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
