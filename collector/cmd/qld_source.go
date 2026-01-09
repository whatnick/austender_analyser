package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/extrame/xls"
	"github.com/shopspring/decimal"
	"github.com/xuri/excelize/v2"
)

const qldSourceID = "qld"

const qldCKANBaseURL = "https://www.data.qld.gov.au"
const qldCKANSearchQuery = "contract+disclosure"

// qldUserAgent for API requests.
const qldUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

var (
	errQldNoResults              = errors.New("qld ckan returned no resources")
	errQldMissingRequiredColumns = errors.New("qld missing required columns")
)

// --- CKAN API types ---

// CKANResponse wraps the standard CKAN Action API response envelope.
type CKANResponse[T any] struct {
	Success bool `json:"success"`
	Result  T    `json:"result"`
	Error   *struct {
		Message string `json:"message"`
		Type    string `json:"__type"`
	} `json:"error,omitempty"`
}

// CKANPackageSearchResult is the result payload for package_search.
type CKANPackageSearchResult struct {
	Count   int           `json:"count"`
	Results []CKANPackage `json:"results"`
}

// CKANPackage represents a dataset in CKAN.
type CKANPackage struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Title            string         `json:"title"`
	Organization     *CKANOrg       `json:"organization,omitempty"`
	MetadataModified string         `json:"metadata_modified"`
	Resources        []CKANResource `json:"resources"`
}

// CKANOrg is the organization owning a dataset.
type CKANOrg struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Title string `json:"title"`
}

// CKANResource is an individual file/link within a dataset.
type CKANResource struct {
	ID           string `json:"id"`
	PackageID    string `json:"package_id"`
	Name         string `json:"name"`
	URL          string `json:"url"`
	Format       string `json:"format"`
	LastModified string `json:"last_modified"`
	Created      string `json:"created"`
	Size         any    `json:"size,omitempty"` // Can be int or string in CKAN API
}

// CKANClient wraps HTTP calls to a CKAN Action API endpoint.
type CKANClient struct {
	baseURL    string
	httpClient *http.Client
	token      string // optional; from QLD_ODATA_TOKEN
}

// NewCKANClient creates a client for the given CKAN instance.
func NewCKANClient(baseURL, token string) *CKANClient {
	return &CKANClient{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		token: token,
	}
}

// PackageSearch calls package_search with the given query and pagination.
func (c *CKANClient) PackageSearch(ctx context.Context, query string, rows, start int) (*CKANPackageSearchResult, error) {
	endpoint := fmt.Sprintf("%s/api/3/action/package_search?q=%s&rows=%d&start=%d", c.baseURL, url.QueryEscape(query), rows, start)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", qldUserAgent)
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ckan package_search: status %d: %s", resp.StatusCode, string(body))
	}

	var ckanResp CKANResponse[CKANPackageSearchResult]
	if err := json.NewDecoder(resp.Body).Decode(&ckanResp); err != nil {
		return nil, fmt.Errorf("ckan package_search decode: %w", err)
	}
	if !ckanResp.Success {
		errMsg := "unknown error"
		if ckanResp.Error != nil {
			errMsg = ckanResp.Error.Message
		}
		return nil, fmt.Errorf("ckan package_search failed: %s", errMsg)
	}
	return &ckanResp.Result, nil
}

type qldSource struct {
	baseURL    string
	ckanClient *CKANClient
}

func newQldSource() Source {
	token := os.Getenv("QLD_ODATA_TOKEN")
	return qldSource{
		baseURL:    qldCKANBaseURL,
		ckanClient: NewCKANClient(qldCKANBaseURL, token),
	}
}

func newQldSourceForTests(baseURL, _ string) Source {
	return qldSource{
		baseURL:    baseURL,
		ckanClient: NewCKANClient(baseURL, ""),
	}
}

func (q qldSource) ID() string { return qldSourceID }

// qldDownloadJob describes a downloadable resource from CKAN.
type qldDownloadJob struct {
	resourceID    string
	downloadURL   string
	format        string
	datasetTitle  string
	resourceTitle string
	lastModified  time.Time
	organization  string
}

// qldDiscoverResourcesViaCKAN uses the CKAN Action API to find contract disclosure resources.
func qldDiscoverResourcesViaCKAN(ctx context.Context, client *CKANClient) ([]qldDownloadJob, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	const pageSize = 100
	var allJobs []qldDownloadJob
	seen := make(map[string]struct{})
	start := 0

	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		result, err := client.PackageSearch(ctx, qldCKANSearchQuery, pageSize, start)
		if err != nil {
			return nil, fmt.Errorf("ckan discovery: %w", err)
		}

		for _, pkg := range result.Results {
			org := ""
			if pkg.Organization != nil {
				org = pkg.Organization.Title
			}

			for _, res := range pkg.Resources {
				// Filter by format - only CSV, XLSX, XLS.
				format := strings.ToLower(strings.TrimSpace(res.Format))
				if format != "csv" && format != "xlsx" && format != "xls" {
					continue
				}

				// Validate URL - skip malformed/relative URLs.
				resURL := strings.TrimSpace(res.URL)
				if resURL == "" {
					continue
				}
				parsedURL, err := url.Parse(resURL)
				if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
					// Skip invalid or relative URLs.
					continue
				}
				if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
					continue
				}
				// Skip URLs where "host" looks like a filename (contains . but no domain structure).
				if !strings.Contains(parsedURL.Host, ".") || strings.HasSuffix(strings.ToLower(parsedURL.Host), ".xlsx") ||
					strings.HasSuffix(strings.ToLower(parsedURL.Host), ".xls") ||
					strings.HasSuffix(strings.ToLower(parsedURL.Host), ".csv") {
					continue
				}

				if _, ok := seen[res.ID]; ok {
					continue
				}
				seen[res.ID] = struct{}{}

				lastMod := parseQldCKANTime(res.LastModified)
				if lastMod.IsZero() {
					lastMod = parseQldCKANTime(res.Created)
				}

				allJobs = append(allJobs, qldDownloadJob{
					resourceID:    res.ID,
					downloadURL:   resURL,
					format:        format,
					datasetTitle:  pkg.Title,
					resourceTitle: res.Name,
					lastModified:  lastMod,
					organization:  org,
				})
			}
		}

		start += len(result.Results)
		if start >= result.Count || len(result.Results) == 0 {
			break
		}
	}

	// Sort for deterministic ordering.
	sort.Slice(allJobs, func(i, j int) bool {
		return allJobs[i].downloadURL < allJobs[j].downloadURL
	})

	return allJobs, nil
}

// parseQldCKANTime parses CKAN timestamp formats.
func parseQldCKANTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05.999999",
		"2006-01-02T15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// qldCacheDir returns the directory for caching QLD downloaded files.
func qldCacheDir() string {
	return filepath.Join(defaultCacheDir(), "qld_ckan")
}

// qldCachedFilePath returns the cache path for a resource.
func qldCachedFilePath(resourceID, format string) string {
	return filepath.Join(qldCacheDir(), fmt.Sprintf("%s.%s", resourceID, format))
}

// qldCacheMetaPath returns the metadata cache path.
func qldCacheMetaPath(resourceID string) string {
	return filepath.Join(qldCacheDir(), fmt.Sprintf("%s.meta.json", resourceID))
}

// qldCacheMeta stores metadata about a cached file.
type qldCacheMeta struct {
	ResourceID   string    `json:"resource_id"`
	URL          string    `json:"url"`
	Format       string    `json:"format"`
	LastModified time.Time `json:"last_modified"`
	CachedAt     time.Time `json:"cached_at"`
	ETag         string    `json:"etag,omitempty"`
	ContentHash  string    `json:"content_hash"`
}

// qldLoadCacheMeta loads metadata for a cached resource.
func qldLoadCacheMeta(resourceID string) (*qldCacheMeta, error) {
	data, err := os.ReadFile(qldCacheMetaPath(resourceID))
	if err != nil {
		return nil, err
	}
	var meta qldCacheMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// qldSaveCacheMeta saves metadata for a cached resource.
func qldSaveCacheMeta(meta *qldCacheMeta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(qldCacheMetaPath(meta.ResourceID), data, 0o644)
}

// qldIsCacheValid checks if a cached file is still valid.
func qldIsCacheValid(job qldDownloadJob) bool {
	meta, err := qldLoadCacheMeta(job.resourceID)
	if err != nil {
		return false
	}

	// Check if the file exists.
	cachedPath := qldCachedFilePath(job.resourceID, job.format)
	if _, err := os.Stat(cachedPath); os.IsNotExist(err) {
		return false
	}

	// If CKAN reports a newer last_modified, cache is stale.
	if !job.lastModified.IsZero() && meta.LastModified.Before(job.lastModified) {
		return false
	}

	return true
}

// QLD source uses CKAN API for discovery.
//
//go:nocover
func (q qldSource) Run(ctx context.Context, req SearchRequest) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	lookback := resolveLookbackPeriod(req.LookbackPeriod)
	startResolved, endResolved := resolveDates(req.StartDate, req.EndDate, lookback)
	windows := splitDateWindowsByMonth(startResolved, endResolved)
	if len(windows) == 0 {
		return formatMoneyDecimal(decimal.Zero), nil
	}

	// Identify which month windows we actually need to process.
	needed := make(map[string]dateWindow)
	for _, win := range windows {
		if req.ShouldFetchWindow != nil && !req.ShouldFetchWindow(win) {
			continue
		}
		needed[monthKey(win.start)] = win
	}
	if len(needed) == 0 {
		if req.OnProgress != nil {
			req.OnProgress(len(windows), len(windows))
		}
		return formatMoneyDecimal(decimal.Zero), nil
	}

	// Discover resources via CKAN API.
	jobs, err := qldDiscoverResourcesViaCKAN(ctx, q.ckanClient)
	if err != nil {
		return "", err
	}
	if len(jobs) == 0 {
		return "", errQldNoResults
	}

	// Download and parse resources with caching.
	buckets, err := qldDownloadAndParseCached(ctx, jobs, needed, req)
	if err != nil {
		return "", err
	}

	maxConc := resolveMaxConcurrency()
	if maxConc <= 0 {
		maxConc = 1
	}
	if maxConc > len(windows) {
		maxConc = len(windows)
	}

	sem := make(chan struct{}, maxConc)
	var wg sync.WaitGroup

	var completed int32
	totalWindows := len(windows)
	notifyProgress := func() {
		if req.OnProgress != nil {
			req.OnProgress(int(atomic.LoadInt32(&completed)), totalWindows)
		}
	}

	var total decimal.Decimal
	var totalMu sync.Mutex
	var cbMu sync.Mutex

	for _, win := range windows {
		win := win
		if req.ShouldFetchWindow != nil && !req.ShouldFetchWindow(win) {
			atomic.AddInt32(&completed, 1)
			notifyProgress()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			winKey := monthKey(win.start)
			summaries := buckets[winKey]
			for _, summary := range summaries {
				if ctx.Err() != nil {
					break
				}
				if summary.ReleaseDate.Before(win.start) || summary.ReleaseDate.After(win.end) {
					continue
				}

				cbMu.Lock()
				if req.OnAnyMatch != nil {
					req.OnAnyMatch(summary)
				}
				cbMu.Unlock()

				if !matchesSummaryFilters(req, summary, time.Time{}) {
					continue
				}
				cbMu.Lock()
				// When verbose output is enabled, QLD streams matches during tabular parsing.
				// Avoid double-calling OnMatch here.
				if req.OnMatch != nil && !req.Verbose {
					req.OnMatch(summary)
				}
				cbMu.Unlock()

				totalMu.Lock()
				total = total.Add(summary.Amount)
				totalMu.Unlock()
			}

			atomic.AddInt32(&completed, 1)
			notifyProgress()
		}()
	}

	wg.Wait()
	return formatMoneyDecimal(total), nil
}

// qldDownloadAndParseCached downloads and parses resources with file caching.
func qldDownloadAndParseCached(ctx context.Context, jobs []qldDownloadJob, needed map[string]dateWindow, req SearchRequest) (map[string][]MatchSummary, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(jobs) == 0 {
		return map[string][]MatchSummary{}, nil
	}

	// Ensure cache directory exists.
	cacheDir := qldCacheDir()
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("create qld cache dir: %w", err)
	}

	maxWorkers := resolveMaxConcurrency()
	if maxWorkers <= 0 {
		maxWorkers = 1
	}
	if maxWorkers > len(jobs) {
		maxWorkers = len(jobs)
	}
	// Be conservative; QLD portal resources can be large.
	if maxWorkers > 5 {
		maxWorkers = 5
	}

	client := &http.Client{Timeout: 60 * time.Second}
	jobsCh := make(chan qldDownloadJob)
	var wg sync.WaitGroup

	buckets := make(map[string][]MatchSummary)
	seen := make(map[string]struct{})
	var mu sync.Mutex
	var cbMu sync.Mutex

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 1)

	for i := 0; i < maxWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobsCh {
				if ctx.Err() != nil {
					return
				}

				body, err := qldDownloadCached(ctx, client, job)
				if err != nil {
					// Log download errors but continue - some URLs may be stale.
					fmt.Fprintf(os.Stderr, "qld download warning: %s: %v\n", job.downloadURL, err)
					continue
				}

				summaries, err := qldParseTabular(job, body, needed)
				if err != nil {
					if errors.Is(err, errQldMissingRequiredColumns) {
						// Skip non-data/template sheets while continuing.
						continue
					}
					// Log parse errors but continue - some files may be corrupt or wrong format.
					fmt.Fprintf(os.Stderr, "qld parse warning: %s (%s): %v\n", job.downloadURL, job.format, err)
					continue
				}

				var toStream []MatchSummary
				mu.Lock()
				for _, summary := range summaries {
					// Use organization from CKAN as agency fallback.
					if summary.Agency == "" && job.organization != "" {
						summary.Agency = job.organization
					}
					key := summary.ContractID + "|" + summary.ReleaseDate.Format("2006-01-02") + "|" + summary.Amount.StringFixed(2)
					if _, ok := seen[key]; ok {
						continue
					}
					seen[key] = struct{}{}
					mk := monthKey(summary.ReleaseDate)
					buckets[mk] = append(buckets[mk], summary)

					// In verbose mode, stream matches as rows are parsed (per file),
					// rather than waiting for window aggregation.
					if req.Verbose && req.OnMatch != nil && matchesSummaryFilters(req, summary, time.Time{}) {
						toStream = append(toStream, summary)
					}
				}
				mu.Unlock()

				if len(toStream) > 0 {
					cbMu.Lock()
					for _, summary := range toStream {
						req.OnMatch(summary)
					}
					cbMu.Unlock()
				}
			}
		}()
	}

	go func() {
		defer close(jobsCh)
		for _, job := range jobs {
			select {
			case <-ctx.Done():
				return
			case jobsCh <- job:
			}
		}
	}()

	wg.Wait()
	select {
	case err := <-errCh:
		if err != nil {
			return nil, err
		}
	default:
	}

	// Ensure consistent ordering.
	for k := range buckets {
		slice := buckets[k]
		sort.Slice(slice, func(i, j int) bool {
			if slice[i].ReleaseDate.Equal(slice[j].ReleaseDate) {
				return slice[i].ContractID < slice[j].ContractID
			}
			return slice[i].ReleaseDate.Before(slice[j].ReleaseDate)
		})
		buckets[k] = slice
	}

	return buckets, nil
}

// qldDownloadCached downloads a resource, using cache when valid.
func qldDownloadCached(ctx context.Context, client *http.Client, job qldDownloadJob) ([]byte, error) {
	cachedPath := qldCachedFilePath(job.resourceID, job.format)

	// Check if cache is valid.
	if qldIsCacheValid(job) {
		data, err := os.ReadFile(cachedPath)
		if err == nil {
			return data, nil
		}
		// Cache read failed, proceed to download.
	}

	// Download fresh.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, job.downloadURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", qldUserAgent)
	req.Header.Set("Accept", "*/*")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("qld download %s: status %d", job.downloadURL, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Write to cache.
	if err := os.WriteFile(cachedPath, body, 0o644); err != nil {
		// Log but don't fail - caching is best-effort.
		fmt.Fprintf(os.Stderr, "qld cache write warning: %v\n", err)
	} else {
		// Save metadata.
		hash := sha256.Sum256(body)
		meta := &qldCacheMeta{
			ResourceID:   job.resourceID,
			URL:          job.downloadURL,
			Format:       job.format,
			LastModified: job.lastModified,
			CachedAt:     time.Now().UTC(),
			ETag:         resp.Header.Get("ETag"),
			ContentHash:  hex.EncodeToString(hash[:]),
		}
		if err := qldSaveCacheMeta(meta); err != nil {
			fmt.Fprintf(os.Stderr, "qld cache meta write warning: %v\n", err)
		}
	}

	return body, nil
}

func qldParseTabular(job qldDownloadJob, body []byte, needed map[string]dateWindow) ([]MatchSummary, error) {
	ext := strings.ToLower(job.format)

	// Try the declared format first.
	var results []MatchSummary
	var err error

	switch ext {
	case "csv":
		results, err = qldParseCSV(job, body, needed)
	case "xlsx":
		results, err = qldParseXLSX(job, body, needed)
	case "xls":
		results, err = qldParseXLS(job, body, needed)
	default:
		return nil, fmt.Errorf("unsupported qld format: %s", job.format)
	}

	if err == nil {
		return results, nil
	}

	// If declared format fails, try other formats as fallback.
	// This handles cases where CKAN metadata is wrong.
	fallbackFormats := []string{"xlsx", "xls", "csv"}
	for _, fallback := range fallbackFormats {
		if fallback == ext {
			continue // Already tried.
		}
		var fallbackErr error
		switch fallback {
		case "csv":
			results, fallbackErr = qldParseCSV(job, body, needed)
		case "xlsx":
			results, fallbackErr = qldParseXLSX(job, body, needed)
		case "xls":
			results, fallbackErr = qldParseXLS(job, body, needed)
		}
		if fallbackErr == nil {
			return results, nil
		}
	}

	// All formats failed, return original error.
	return nil, err
}

func qldParseCSV(job qldDownloadJob, body []byte, needed map[string]dateWindow) ([]MatchSummary, error) {
	// Sniff delimiter based on first record.
	comma := ','
	probe := csv.NewReader(bytes.NewReader(body))
	probe.FieldsPerRecord = -1
	probe.LazyQuotes = true
	first, err := probe.Read()
	if err != nil {
		return nil, err
	}
	if len(first) == 1 {
		if strings.Contains(first[0], ";") {
			comma = ';'
		} else if strings.Contains(first[0], "\t") {
			comma = '\t'
		}
	}

	r := csv.NewReader(bytes.NewReader(body))
	r.Comma = comma
	r.FieldsPerRecord = -1
	r.LazyQuotes = true

	// Some QLD resources include a preamble before the actual header.
	// Scan the first N records for a plausible header row.
	const maxHeaderScan = 25
	var scanned [][]string
	var headers []string
	startFrom := 0
	for i := 0; i < maxHeaderScan; i++ {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		scanned = append(scanned, rec)
		col := qldHeaderIndex(rec)
		if col.awardDate >= 0 && col.value >= 0 {
			headers = rec
			startFrom = i + 1
			break
		}
	}
	if headers == nil {
		var samples []string
		for i := 0; i < len(scanned) && i < 3; i++ {
			samples = append(samples, strings.Join(scanned[i], " | "))
		}
		return nil, fmt.Errorf("%w: qld csv missing required columns (award date/value); delimiter=%q; first rows: %s", errQldMissingRequiredColumns, string(comma), strings.Join(samples, " || "))
	}

	col := qldHeaderIndex(headers)

	var out []MatchSummary
	rowNum := startFrom
	// Process any already-read records after the header.
	for i := startFrom; i < len(scanned); i++ {
		rowNum++
		summary, ok := qldRowToSummary(job, scanned[i], col, rowNum)
		if !ok {
			continue
		}

		mk := monthKey(summary.ReleaseDate)
		win, ok := needed[mk]
		if !ok {
			continue
		}
		if summary.ReleaseDate.Before(win.start) || summary.ReleaseDate.After(win.end) {
			continue
		}
		out = append(out, summary)
	}

	for {
		rowNum++
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		summary, ok := qldRowToSummary(job, rec, col, rowNum)
		if !ok {
			continue
		}

		mk := monthKey(summary.ReleaseDate)
		win, ok := needed[mk]
		if !ok {
			continue
		}
		if summary.ReleaseDate.Before(win.start) || summary.ReleaseDate.After(win.end) {
			continue
		}
		out = append(out, summary)
	}
	return out, nil
}

func qldParseXLSX(job qldDownloadJob, body []byte, needed map[string]dateWindow) ([]MatchSummary, error) {
	f, err := excelize.OpenReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, errors.New("qld xlsx has no sheets")
	}

	// Search across all sheets; many QLD reports use a cover/preamble section first.
	// Bound the scan so we don't walk huge sheets indefinitely.
	const maxHeaderScan = 2000
	for _, sheet := range sheets {
		rows, err := f.GetRows(sheet)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			continue
		}

		headerRow := -1
		var col qldColumnIndex
		for i := 0; i < len(rows) && i < maxHeaderScan; i++ {
			cand := rows[i]
			idx := qldHeaderIndex(cand)
			if idx.awardDate >= 0 && idx.value >= 0 {
				headerRow = i
				col = idx
				break
			}
		}
		if headerRow < 0 {
			continue
		}

		var out []MatchSummary
		for i := headerRow + 1; i < len(rows); i++ {
			rowNum := i + 1
			summary, ok := qldRowToSummary(job, rows[i], col, rowNum)
			if !ok {
				continue
			}
			mk := monthKey(summary.ReleaseDate)
			win, ok := needed[mk]
			if !ok {
				continue
			}
			if summary.ReleaseDate.Before(win.start) || summary.ReleaseDate.After(win.end) {
				continue
			}
			out = append(out, summary)
		}
		return out, nil
	}

	// If we couldn't find a header row on any sheet, return an error with samples.
	var sampleSheet string
	var sampleRows []string
	for _, sheet := range sheets {
		rows, err := f.GetRows(sheet)
		if err != nil {
			continue
		}
		if len(rows) == 0 {
			continue
		}
		sampleSheet = sheet
		for i := 0; i < len(rows) && i < 3; i++ {
			sampleRows = append(sampleRows, strings.Join(rows[i], " | "))
		}
		break
	}
	if sampleSheet == "" {
		return nil, fmt.Errorf("%w: qld xlsx missing required columns (award date/value); no non-empty sheets", errQldMissingRequiredColumns)
	}
	return nil, fmt.Errorf("%w: qld xlsx missing required columns (award date/value); sheets=%s; sample sheet=%q first rows: %s", errQldMissingRequiredColumns, strings.Join(sheets, ", "), sampleSheet, strings.Join(sampleRows, " || "))
}

func qldParseXLS(job qldDownloadJob, body []byte, needed map[string]dateWindow) ([]MatchSummary, error) {
	// extrame/xls reads from a file path.
	tmpDir := defaultCacheDir()
	_ = os.MkdirAll(tmpDir, 0o755)
	tmpPath := filepath.Join(tmpDir, fmt.Sprintf("qld_%d.xls", time.Now().UnixNano()))
	if err := os.WriteFile(tmpPath, body, 0o600); err != nil {
		return nil, err
	}
	defer os.Remove(tmpPath)

	workbook, err := xls.Open(tmpPath, "utf-8")
	if err != nil {
		return nil, err
	}
	if workbook == nil {
		return nil, errors.New("qld xls open returned nil workbook")
	}
	sheet := workbook.GetSheet(0)
	if sheet == nil {
		return nil, nil
	}

	// Extract header row.
	if sheet.MaxRow == 0 {
		return nil, nil
	}
	headers := make([]string, 0)
	headerRow := sheet.Row(0)
	for i := 0; i < headerRow.LastCol(); i++ {
		headers = append(headers, headerRow.Col(i))
	}
	col := qldHeaderIndex(headers)
	if col.awardDate < 0 || col.value < 0 {
		return nil, fmt.Errorf("%w: qld xls missing required columns (award date/value)", errQldMissingRequiredColumns)
	}

	var out []MatchSummary
	for r := 1; r <= int(sheet.MaxRow); r++ {
		row := sheet.Row(r)
		if row == nil {
			continue
		}
		cells := make([]string, headerRow.LastCol())
		for c := 0; c < len(cells); c++ {
			cells[c] = row.Col(c)
		}
		rowNum := r + 1
		summary, ok := qldRowToSummary(job, cells, col, rowNum)
		if !ok {
			continue
		}
		mk := monthKey(summary.ReleaseDate)
		win, ok := needed[mk]
		if !ok {
			continue
		}
		if summary.ReleaseDate.Before(win.start) || summary.ReleaseDate.After(win.end) {
			continue
		}
		out = append(out, summary)
	}
	return out, nil
}

type qldColumnIndex struct {
	agency    int
	supplier  int
	awardDate int
	value     int
	reference int
	title     int
}

func qldHeaderIndex(headers []string) qldColumnIndex {
	norm := make([]string, len(headers))
	for i, h := range headers {
		clean := strings.ToLower(strings.TrimSpace(h))
		clean = strings.TrimPrefix(clean, "\ufeff")
		clean = strings.Join(strings.Fields(clean), " ")
		norm[i] = clean
	}

	idx := qldColumnIndex{agency: -1, supplier: -1, awardDate: -1, value: -1, reference: -1, title: -1}
	find := func(candidates ...string) int {
		for _, cand := range candidates {
			cand = strings.ToLower(strings.TrimSpace(cand))
			for i, h := range norm {
				if h == cand {
					return i
				}
			}
		}
		// fallback: contains match for messy headers
		for _, cand := range candidates {
			cand = strings.ToLower(strings.TrimSpace(cand))
			for i, h := range norm {
				if strings.Contains(h, cand) {
					return i
				}
			}
		}
		return -1
	}

	idx.agency = find("agency", "agency name", "department", "agency/entity", "entity")
	idx.supplier = find("supplier name", "supplier", "vendor", "contractor", "contract party")
	idx.awardDate = find(
		"award contract date",
		"award date",
		"contract date",
		"date awarded",
		"awarded date",
		"start date",
		"commencement date",
		"effective date",
	)
	idx.value = find(
		"contract value",
		"total contract value",
		"contract amount",
		"amount",
		"revised value",
		"revised value (inc)",
		"revised value (inc)",
		"revised value inc",
		"revised value (incl)",
		"revised value (incl)",
		"value (inc)",
		"value (incl)",
		"value inc",
		"value incl",
	)
	idx.reference = find("contract reference number", "contract number", "reference", "contract reference")
	if idx.reference < 0 {
		// Some QLD report-style XLSX files use a bare "Contract" header for the reference.
		for i, h := range norm {
			if h == "contract" {
				idx.reference = i
				break
			}
		}
	}
	idx.title = find("contract description", "description", "title", "purpose")
	return idx
}

func qldRowToSummary(job qldDownloadJob, rec []string, col qldColumnIndex, rowNum int) (MatchSummary, bool) {
	get := func(i int) string {
		if i < 0 || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}

	releaseDate := parseQldDate(get(col.awardDate))
	if releaseDate.IsZero() {
		return MatchSummary{}, false
	}

	amount := decimal.Zero
	if raw := get(col.value); raw != "" {
		if parsed, err := parseMoneyToDecimal(raw); err == nil {
			amount = parsed
		}
	}
	if amount.LessThanOrEqual(decimal.Zero) {
		return MatchSummary{}, false
	}

	agency := get(col.agency)
	supplier := get(col.supplier)
	title := get(col.title)
	ref := get(col.reference)

	contractID := ref
	if contractID == "" {
		contractID = strings.TrimSpace(strings.Join([]string{agency, supplier, title}, " | "))
	}
	if contractID == "" {
		contractID = fmt.Sprintf("qld-%s-%d", monthKey(releaseDate), rowNum)
	}

	releaseID := fmt.Sprintf("%s#%d", job.downloadURL, rowNum)

	return MatchSummary{
		Source:      qldSourceID,
		ContractID:  contractID,
		ReleaseID:   releaseID,
		OCID:        contractID,
		Supplier:    supplier,
		Agency:      agency,
		Title:       title,
		Amount:      amount,
		ReleaseDate: releaseDate,
	}, true
}

func parseQldDate(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}

	// Some XLSX files use Excel serial dates (days since 1899-12-30).
	// Example: 44942 => 2023-01-16
	if serial, err := strconv.ParseFloat(raw, 64); err == nil {
		// Guard rails to avoid accidentally treating values/IDs as dates.
		if serial >= 20000 && serial <= 80000 {
			base := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
			days := time.Duration(serial*24) * time.Hour
			return base.Add(days).UTC()
		}
	}
	// Some sheets include timestamps; trim common suffixes.
	raw = strings.TrimSpace(strings.TrimSuffix(raw, "00:00:00"))
	raw = strings.TrimSpace(strings.TrimSuffix(raw, "00:00"))

	layouts := []string{
		time.RFC3339,
		"2006-01-02",
		"02/01/2006",
		"2/01/2006",
		"02-01-2006",
		"2-01-2006",
		"02 Jan 2006",
		"2 Jan 2006",
		"02-Jan-2006",
		"2-Jan-2006",
		"Jan 2 2006",
		"January 2 2006",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func monthKey(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
