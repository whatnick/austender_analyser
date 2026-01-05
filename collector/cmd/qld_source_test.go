package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestQldDiscoverDownloadJobs_FollowsPaginationAndFindsDownloads(t *testing.T) {
	var srv *httptest.Server
	mux := http.NewServeMux()

	mux.HandleFunc("/dataset/", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		page := r.URL.Query().Get("page")
		if !strings.Contains(q, "contract") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<html><body>nope</body></html>`))
			return
		}
		switch page {
		case "1":
			_, _ = w.Write([]byte(`
<html><body>
<a href="/dataset/ds-one">dataset one</a>
<a href="/dataset/?q=contract+disclosure&page=2">next</a>
</body></html>`))
		case "2":
			_, _ = w.Write([]byte(`
<html><body>
<a href="/dataset/ds-two">dataset two</a>
</body></html>`))
		default:
			_, _ = w.Write([]byte(`<html><body></body></html>`))
		}
	})

	mux.HandleFunc("/dataset/ds-one", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`
<html><body>
<a href="/dataset/ds-one/resource/res-1">resource 1</a>
</body></html>`))
	})
	mux.HandleFunc("/dataset/ds-two", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`
<html><body>
<a href="/dataset/ds-two/resource/res-2">resource 2</a>
</body></html>`))
	})

	mux.HandleFunc("/dataset/ds-one/resource/res-1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`
<html><body>
<a href="/dataset/uuid-1/resource/res-1/download/contracts.csv">Download CSV</a>
</body></html>`))
	})
	mux.HandleFunc("/dataset/ds-two/resource/res-2", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`
<html><body>
<a href="/dataset/uuid-2/resource/res-2/download/contracts.xlsx">Download XLSX</a>
</body></html>`))
	})

	srv = httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	jobs, err := qldDiscoverDownloadJobs(ctx, srv.URL, srv.URL+"/dataset/?q=contract+disclosure&page=1")
	require.NoError(t, err)
	require.Len(t, jobs, 2)

	urls := []string{jobs[0].downloadURL, jobs[1].downloadURL}
	require.Contains(t, urls, srv.URL+"/dataset/uuid-1/resource/res-1/download/contracts.csv")
	require.Contains(t, urls, srv.URL+"/dataset/uuid-2/resource/res-2/download/contracts.xlsx")
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
