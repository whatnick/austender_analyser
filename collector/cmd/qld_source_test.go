package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestQldDiscoverResourcesViaCKAN(t *testing.T) {
	// Mock CKAN API response.
	mockResponse := CKANResponse[CKANPackageSearchResult]{
		Success: true,
		Result: CKANPackageSearchResult{
			Count: 2,
			Results: []CKANPackage{
				{
					ID:    "pkg-1",
					Name:  "agency-a-contracts",
					Title: "Agency A Contract Disclosure",
					Organization: &CKANOrg{
						ID:    "org-1",
						Name:  "agency-a",
						Title: "Agency A",
					},
					Resources: []CKANResource{
						{
							ID:           "res-1",
							PackageID:    "pkg-1",
							Name:         "FY 2024-25 Contracts",
							URL:          "https://example.test/contracts-2024.csv",
							Format:       "CSV",
							LastModified: "2024-07-15T10:00:00",
						},
						{
							ID:           "res-2",
							PackageID:    "pkg-1",
							Name:         "FY 2023-24 Contracts",
							URL:          "https://example.test/contracts-2023.xlsx",
							Format:       "XLSX",
							LastModified: "2023-08-01T12:00:00",
						},
					},
				},
				{
					ID:    "pkg-2",
					Name:  "agency-b-contracts",
					Title: "Agency B Contract Disclosure",
					Organization: &CKANOrg{
						ID:    "org-2",
						Name:  "agency-b",
						Title: "Agency B",
					},
					Resources: []CKANResource{
						{
							ID:           "res-3",
							PackageID:    "pkg-2",
							Name:         "Contract Register",
							URL:          "https://example.test/register.xls",
							Format:       "XLS",
							LastModified: "2024-06-01T09:00:00",
						},
						{
							ID:           "res-4",
							PackageID:    "pkg-2",
							Name:         "PDF Report",
							URL:          "https://example.test/report.pdf",
							Format:       "PDF",
							LastModified: "2024-06-01T09:00:00",
						},
					},
				},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/3/action/package_search" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mockResponse)
	}))
	defer srv.Close()

	client := NewCKANClient(srv.URL, "")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	jobs, err := qldDiscoverResourcesViaCKAN(ctx, client)
	require.NoError(t, err)
	// Should only include CSV, XLSX, XLS - not PDF.
	require.Len(t, jobs, 3)

	// Verify job details.
	jobByID := make(map[string]qldDownloadJob)
	for _, job := range jobs {
		jobByID[job.resourceID] = job
	}

	require.Contains(t, jobByID, "res-1")
	require.Equal(t, "csv", jobByID["res-1"].format)
	require.Equal(t, "Agency A", jobByID["res-1"].organization)

	require.Contains(t, jobByID, "res-2")
	require.Equal(t, "xlsx", jobByID["res-2"].format)

	require.Contains(t, jobByID, "res-3")
	require.Equal(t, "xls", jobByID["res-3"].format)
	require.Equal(t, "Agency B", jobByID["res-3"].organization)

	// PDF should be excluded.
	require.NotContains(t, jobByID, "res-4")
}

func TestQldCKANClient_Pagination(t *testing.T) {
	// Simulate paginated CKAN API responses.
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/3/action/package_search" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		callCount++
		start := r.URL.Query().Get("start")
		rows := r.URL.Query().Get("rows")
		require.Equal(t, "100", rows)

		var response CKANResponse[CKANPackageSearchResult]
		response.Success = true
		response.Result.Count = 150

		if start == "0" {
			// First page: return 100 packages with unique IDs.
			for i := 0; i < 100; i++ {
				resID := "res-page1-" + string(rune('0'+i/100)) + string(rune('0'+(i/10)%10)) + string(rune('0'+i%10))
				response.Result.Results = append(response.Result.Results, CKANPackage{
					ID:   "pkg-page1-" + resID,
					Name: "dataset-page1-" + resID,
					Resources: []CKANResource{{
						ID:     resID,
						URL:    "https://example.test/file-" + resID + ".csv",
						Format: "CSV",
					}},
				})
			}
		} else if start == "100" {
			// Second page: return 50 packages with unique IDs.
			for i := 0; i < 50; i++ {
				resID := "res-page2-" + string(rune('0'+i/100)) + string(rune('0'+(i/10)%10)) + string(rune('0'+i%10))
				response.Result.Results = append(response.Result.Results, CKANPackage{
					ID:   "pkg-page2-" + resID,
					Name: "dataset-page2-" + resID,
					Resources: []CKANResource{{
						ID:     resID,
						URL:    "https://example.test/file2-" + resID + ".csv",
						Format: "CSV",
					}},
				})
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer srv.Close()

	client := NewCKANClient(srv.URL, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	jobs, err := qldDiscoverResourcesViaCKAN(ctx, client)
	require.NoError(t, err)
	require.Equal(t, 2, callCount, "Should make 2 API calls for pagination")
	require.Len(t, jobs, 150)
}

func TestQldCKANClient_WithToken(t *testing.T) {
	var receivedToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedToken = r.Header.Get("Authorization")
		response := CKANResponse[CKANPackageSearchResult]{
			Success: true,
			Result:  CKANPackageSearchResult{Count: 0, Results: []CKANPackage{}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer srv.Close()

	client := NewCKANClient(srv.URL, "test-token-123")
	ctx := context.Background()

	_, err := client.PackageSearch(ctx, "test", 10, 0)
	require.NoError(t, err)
	require.Equal(t, "test-token-123", receivedToken)
}

func TestParseQldCKANTime(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Time
	}{
		{"2024-07-15T10:30:00.123456", time.Date(2024, 7, 15, 10, 30, 0, 123456000, time.UTC)},
		{"2024-07-15T10:30:00", time.Date(2024, 7, 15, 10, 30, 0, 0, time.UTC)},
		{"2024-07-15", time.Date(2024, 7, 15, 0, 0, 0, 0, time.UTC)},
		{"", time.Time{}},
		{"invalid", time.Time{}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseQldCKANTime(tt.input)
			require.Equal(t, tt.expected, got)
		})
	}
}

func TestQldParseCSV_MapsFieldsAndFiltersByWindow(t *testing.T) {
	csvBody := "Agency,Supplier name,Award contract date,Contract value,Contract reference number,Contract description\n" +
		"Dept A,Acme Pty Ltd,2024-07-10,\"$1,234.56\",ABC-123,Consulting\n" +
		"Dept B,Other Pty Ltd,2024-08-01,$10.00,DEF-456,Something\n"

	needed := map[string]dateWindow{
		"2024-07": {
			start: time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC),
			end:   time.Date(2024, 7, 31, 23, 59, 59, 0, time.UTC),
		},
	}

	job := qldDownloadJob{downloadURL: "https://example.test/file.csv", format: "csv"}
	summaries, err := qldParseCSV(job, []byte(csvBody), needed)
	require.NoError(t, err)
	require.Len(t, summaries, 1)

	got := summaries[0]
	require.Equal(t, qldSourceID, got.Source)
	require.Equal(t, "ABC-123", got.ContractID)
	require.Equal(t, "Acme Pty Ltd", got.Supplier)
	require.Equal(t, "Dept A", got.Agency)
	require.Equal(t, time.Date(2024, 7, 10, 0, 0, 0, 0, time.UTC), got.ReleaseDate)
	require.Equal(t, "1234.56", got.Amount.StringFixed(2))
}

func TestQldHeaderIndex_ReportStyleXlsxHeaders(t *testing.T) {
	// Matches the header row used by at least one real QLD report-style XLSX.
	headers := []string{"LIST", "Contract", "Description", "Contract Party", "ABN", "Contract Type", "Revised Value (inc)", "Start Date", "End Date"}
	idx := qldHeaderIndex(headers)
	require.GreaterOrEqual(t, idx.awardDate, 0)
	require.GreaterOrEqual(t, idx.value, 0)
	require.GreaterOrEqual(t, idx.supplier, 0)
}

func TestParseQldDate_ExcelSerial(t *testing.T) {
	got := parseQldDate("44942")
	require.Equal(t, time.Date(2023, 1, 16, 0, 0, 0, 0, time.UTC), got)
}
