package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newJSONRecorder() *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Type", "application/json")
	return rec
}

func TestRunFederalSearchAggregatesPages(t *testing.T) {
	start := time.Date(2024, 8, 10, 0, 0, 0, 0, time.UTC)

	var nextURL string
	var callMu sync.Mutex
	callCount := 0

	originalClient := defaultHTTPClient
	originalConcurrency := defaultMaxConcurrency
	defer func() {
		defaultHTTPClient = originalClient
		defaultMaxConcurrency = originalConcurrency
	}()

	defaultHTTPClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		callMu.Lock()
		defer callMu.Unlock()
		callCount++

		w := newJSONRecorder()
		switch callCount {
		case 1:
			nextURL = r.URL.Scheme + "://" + r.URL.Host + "/page2"
			resp := ocdsResponse{
				Releases: []ocdsRelease{
					{
						ID:   "rel-1",
						OCID: "ocid-1",
						Date: start.Format(time.RFC3339),
						Tag:  []string{"contract"},
						Parties: []ocdsParty{
							{Name: "Acme Pty Ltd", Roles: []string{"supplier"}},
							{Name: "ATO", Roles: []string{"buyer"}},
						},
						Contracts: []ocdsContract{
							{ID: "CN-1", Title: "Audit", Value: &ocdsValue{Amount: decimal.NewFromInt(100)}},
						},
					},
				},
				Links: ocdsLinks{Next: nextURL},
			}
			require.NoError(t, json.NewEncoder(w).Encode(resp))
		case 2:
			resp := ocdsResponse{
				Releases: []ocdsRelease{
					{
						ID:   "rel-2",
						OCID: "ocid-2",
						Date: start.Add(12 * time.Hour).Format(time.RFC3339),
						Tag:  []string{"contract"},
						Parties: []ocdsParty{
							{Name: "Beta Pty Ltd", Roles: []string{"supplier"}},
							{Name: "ATO", Roles: []string{"buyer"}},
						},
						Contracts: []ocdsContract{
							{ID: "CN-2", Title: "Security", Value: &ocdsValue{Amount: decimal.NewFromInt(50)}},
						},
					},
				},
			}
			require.NoError(t, json.NewEncoder(w).Encode(resp))
		default:
			return nil, errors.New("unexpected call count")
		}
		return w.Result(), nil
	})}
	defaultMaxConcurrency = 1
	t.Setenv("AUSTENDER_OCDS_BASE_URL", "http://fake")

	var seen []MatchSummary
	var progressCalls []int
	var aggregated decimal.Decimal
	res, err := runFederalSearch(context.Background(), SearchRequest{
		Keyword:   "",
		StartDate: start,
		EndDate:   start,
		OnAnyMatch: func(ms MatchSummary) {
			seen = append(seen, ms)
		},
		OnProgress: func(done, total int) {
			progressCalls = append(progressCalls, done)
		},
		OnMatch: func(ms MatchSummary) {
			aggregated = aggregated.Add(ms.Amount)
		},
	})
	require.NoError(t, err)
	require.Len(t, seen, 2)
	require.Equal(t, "CN-1", seen[0].ContractID)
	require.Equal(t, "CN-2", seen[1].ContractID)
	require.Equal(t, decimal.NewFromInt(100), seen[0].Amount)
	require.Equal(t, decimal.NewFromInt(50), seen[1].Amount)
	require.Equal(t, decimal.NewFromInt(150), aggregated)
	require.True(t, strings.HasPrefix(res, "$"))
	require.Equal(t, "Acme Pty Ltd", seen[0].Supplier)
	require.Equal(t, "Beta Pty Ltd", seen[1].Supplier)
	require.Equal(t, []int{1}, progressCalls)
	require.Equal(t, 2, callCount)
}

func TestFetchAllSkipsWindowsWhenGateReturnsFalse(t *testing.T) {
	client := &ocdsClient{
		baseURL:       "http://example",
		dateType:      defaultDateType,
		httpClient:    http.DefaultClient,
		maxConcurrent: 2,
	}

	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 2)

	var consumed int
	err := client.fetchAll(context.Background(), start, end, func(ocdsRelease) {
		consumed++
	}, nil, func(dateWindow) bool { return false })
	require.NoError(t, err)
	require.Zero(t, consumed)
}

type errorRoundTripper struct{}

func (errorRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("boom")
}

func TestDoRequestReturnsContextError(t *testing.T) {
	client := &ocdsClient{
		httpClient: &http.Client{Transport: errorRoundTripper{}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.doRequest(ctx, "http://example.invalid")
	require.Error(t, err)
	require.Equal(t, context.Canceled, err)
}

func TestSleepWithContext(t *testing.T) {
	require.NoError(t, sleepWithContext(context.Background(), 0))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, sleepWithContext(ctx, time.Second), context.Canceled)
}

func TestEnvOrDefault(t *testing.T) {
	key := "AUSTENDER_TEST_ENV_OR_DEFAULT"
	t.Setenv(key, "value")
	require.Equal(t, "value", envOrDefault(key, "fallback"))

	os.Unsetenv(key)
	require.Equal(t, "fallback", envOrDefault(key, "fallback"))
}

func TestValidateDateOrder(t *testing.T) {
	now := time.Now()
	require.NoError(t, validateDateOrder(now, now.Add(time.Hour)))
	require.Error(t, validateDateOrder(now.Add(time.Hour), now))
}

func TestProgressPrinter(t *testing.T) {
	buf := &bytes.Buffer{}
	originalStdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	pp := newProgressPrinter(10)
	pp.Update(3, 5)
	pp.Finish()

	require.NoError(t, w.Close())
	os.Stdout = originalStdout

	_, err = buf.ReadFrom(r)
	require.NoError(t, err)
	require.Contains(t, buf.String(), "Progress [######----] 3/5")
}
