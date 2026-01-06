package cmd

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/extrame/xls"
	"github.com/gocolly/colly/v2"
	"github.com/shopspring/decimal"
	"github.com/xuri/excelize/v2"
)

const qldSourceID = "qld"

const qldDefaultBaseURL = "https://www.data.qld.gov.au"
const qldDefaultSearchURL = "https://www.data.qld.gov.au/dataset/?q=contract+disclosure&page=1"

// Chrome-like UA to reduce blocks.
const qldUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

var (
	errQldNoResults              = errors.New("qld scrape returned no resources")
	errQldMissingRequiredColumns = errors.New("qld missing required columns")
	reQldDataURL                 = regexp.MustCompile(`(?i)\.(csv|xlsx|xls)(?:\?|$)`) // used on hrefs
)

type qldSource struct {
	baseURL   string
	searchURL string
}

func newQldSource() Source {
	return qldSource{baseURL: qldDefaultBaseURL, searchURL: qldDefaultSearchURL}
}

func newQldSourceForTests(baseURL, searchURL string) Source {
	return qldSource{baseURL: baseURL, searchURL: searchURL}
}

func (q qldSource) ID() string { return qldSourceID }

// QLD scraping depends on live data.qld.gov.au pages.
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

	jobs, err := qldDiscoverDownloadJobs(ctx, q.baseURL, q.searchURL)
	if err != nil {
		return "", err
	}
	if len(jobs) == 0 {
		return "", errQldNoResults
	}

	buckets, err := qldDownloadAndParse(ctx, jobs, needed)
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
				if req.OnMatch != nil {
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

type qldDownloadJob struct {
	resourcePageURL string
	downloadURL     string
	format          string
	datasetTitle    string
	resourceTitle   string
}

func qldDiscoverDownloadJobs(ctx context.Context, baseURL string, searchURL string) ([]qldDownloadJob, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if baseURL == "" {
		baseURL = qldDefaultBaseURL
	}
	if searchURL == "" {
		searchURL = qldDefaultSearchURL
	}

	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	allowedDomain := base.Hostname()

	timeout := resolveTimeout()

	// Crawl search result pages to find dataset URLs.
	searchCollector := colly.NewCollector(
		colly.AllowedDomains(allowedDomain),
		colly.AllowURLRevisit(),
		colly.UserAgent(qldUserAgent),
		colly.Async(true),
	)
	searchCollector.WithTransport(&http.Transport{Proxy: http.ProxyFromEnvironment})
	searchCollector.SetRequestTimeout(timeout)

	parallelism := minInt(resolveMaxConcurrency(), 2)
	delay := 2 * time.Second
	randomDelay := 1500 * time.Millisecond
	// Keep tests (httptest/localhost) fast and deterministic.
	if allowedDomain != "data.qld.gov.au" && allowedDomain != "www.data.qld.gov.au" {
		parallelism = minInt(resolveMaxConcurrency(), 8)
		delay = 0
		randomDelay = 0
	}
	_ = searchCollector.Limit(&colly.LimitRule{
		DomainGlob:  "*",
		Parallelism: parallelism,
		Delay:       delay,
		RandomDelay: randomDelay,
	})

	datasetCollector := searchCollector.Clone()
	resourceCollector := searchCollector.Clone()

	datasetURLs := make(map[string]struct{})
	resourcePageURLs := make(map[string]qldDownloadJob)
	jobsByDownload := make(map[string]qldDownloadJob)

	var mu sync.Mutex
	var scrapeErr error

	searchCollector.OnRequest(func(r *colly.Request) {
		if ctx.Err() != nil {
			r.Abort()
			return
		}
		r.Headers.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		r.Headers.Set("Accept-Language", "en")
		r.Headers.Set("Upgrade-Insecure-Requests", "1")
	})
	searchCollector.OnError(func(_ *colly.Response, err error) {
		mu.Lock()
		if scrapeErr == nil {
			scrapeErr = err
		}
		mu.Unlock()
	})

	// CKAN theme usually uses h3.dataset-heading a; we also accept any /dataset/<slug> links.
	searchCollector.OnHTML("a[href]", func(e *colly.HTMLElement) {
		if ctx.Err() != nil {
			return
		}
		href := strings.TrimSpace(e.Attr("href"))
		if href == "" {
			return
		}
		abs := e.Request.AbsoluteURL(href)
		u, err := url.Parse(abs)
		if err != nil {
			return
		}

		// Follow pagination: keep it scoped to /dataset/ with the same search query.
		if u.Path == "/dataset/" {
			q := u.Query().Get("q")
			page := u.Query().Get("page")
			if page == "" {
				return
			}
			// Only follow the contract disclosure search.
			if strings.Contains(strings.ToLower(q), "contract") {
				mu.Lock()
				if _, ok := datasetURLs[abs]; !ok {
					datasetURLs[abs] = struct{}{}
					_ = searchCollector.Visit(abs)
				}
				mu.Unlock()
			}
			return
		}

		// Follow pagination: keep it scoped to /dataset/ with the same search query.
		if strings.HasPrefix(u.Path, "/dataset/") {
			// Avoid resource pages here.
			if strings.Contains(u.Path, "/resource/") {
				return
			}
			mu.Lock()
			if _, ok := datasetURLs[abs]; !ok {
				datasetURLs[abs] = struct{}{}
				_ = datasetCollector.Visit(abs)
			}
			mu.Unlock()
			return
		}
	})

	datasetCollector.OnError(func(_ *colly.Response, err error) {
		mu.Lock()
		if scrapeErr == nil {
			scrapeErr = err
		}
		mu.Unlock()
	})

	datasetCollector.OnHTML("title", func(e *colly.HTMLElement) {
		// track dataset title via request context if needed
		_ = e
	})

	// Resource links on dataset pages.
	datasetCollector.OnHTML("a[href]", func(e *colly.HTMLElement) {
		if ctx.Err() != nil {
			return
		}
		href := strings.TrimSpace(e.Attr("href"))
		if href == "" {
			return
		}
		abs := e.Request.AbsoluteURL(href)
		u, err := url.Parse(abs)
		if err != nil {
			return
		}
		if !strings.Contains(u.Path, "/resource/") {
			return
		}
		if !strings.HasPrefix(u.Path, "/dataset/") {
			return
		}

		mu.Lock()
		if _, ok := resourcePageURLs[abs]; ok {
			mu.Unlock()
			return
		}
		resourcePageURLs[abs] = qldDownloadJob{resourcePageURL: abs}
		mu.Unlock()
		_ = resourceCollector.Visit(abs)
	})

	resourceCollector.OnError(func(_ *colly.Response, err error) {
		mu.Lock()
		if scrapeErr == nil {
			scrapeErr = err
		}
		mu.Unlock()
	})

	resourceCollector.OnHTML("h1", func(e *colly.HTMLElement) {
		// best-effort resource title
		mu.Lock()
		job := resourcePageURLs[e.Request.URL.String()]
		if job.resourceTitle == "" {
			job.resourceTitle = strings.TrimSpace(e.Text)
			resourcePageURLs[e.Request.URL.String()] = job
		}
		mu.Unlock()
	})

	resourceCollector.OnHTML("a[href]", func(e *colly.HTMLElement) {
		if ctx.Err() != nil {
			return
		}
		href := strings.TrimSpace(e.Attr("href"))
		if href == "" {
			return
		}
		if !strings.Contains(href, "/download/") {
			return
		}
		if !reQldDataURL.MatchString(href) {
			return
		}
		dl := e.Request.AbsoluteURL(href)
		ext := qldFileExt(dl)
		if ext == "" {
			return
		}

		mu.Lock()
		job := resourcePageURLs[e.Request.URL.String()]
		job.downloadURL = dl
		job.format = ext
		resourcePageURLs[e.Request.URL.String()] = job
		// de-dupe by download URL
		if _, ok := jobsByDownload[dl]; !ok {
			jobsByDownload[dl] = job
		}
		mu.Unlock()
	})

	if err := searchCollector.Visit(searchURL); err != nil {
		return nil, err
	}
	searchCollector.Wait()
	datasetCollector.Wait()
	resourceCollector.Wait()

	mu.Lock()
	err = scrapeErr
	mu.Unlock()
	if err != nil {
		return nil, err
	}

	var jobs []qldDownloadJob
	for _, job := range jobsByDownload {
		if job.downloadURL == "" {
			continue
		}
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].downloadURL < jobs[j].downloadURL
	})
	return jobs, nil
}

func qldDownloadAndParse(ctx context.Context, jobs []qldDownloadJob, needed map[string]dateWindow) (map[string][]MatchSummary, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(jobs) == 0 {
		return map[string][]MatchSummary{}, nil
	}

	maxWorkers := resolveMaxConcurrency()
	if maxWorkers <= 0 {
		maxWorkers = 1
	}
	if maxWorkers > len(jobs) {
		maxWorkers = len(jobs)
	}
	// Be conservative; QLD portal has crawl delay and resources can be large.
	if maxWorkers > 3 {
		maxWorkers = 3
	}

	client := &http.Client{Timeout: 30 * time.Second}
	jobsCh := make(chan qldDownloadJob)
	var wg sync.WaitGroup

	buckets := make(map[string][]MatchSummary)
	seen := make(map[string]struct{})
	var mu sync.Mutex

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
				body, err := qldDownload(ctx, client, job.downloadURL)
				if err != nil {
					err = fmt.Errorf("qld download %s: %w", job.downloadURL, err)
					select {
					case errCh <- err:
					default:
					}
					cancel()
					return
				}

				summaries, err := qldParseTabular(job, body, needed)
				if err != nil {
					if errors.Is(err, errQldMissingRequiredColumns) {
						// Skip non-data/template sheets while continuing the scrape.
						continue
					}
					err = fmt.Errorf("qld parse %s (%s): %w", job.downloadURL, job.format, err)
					select {
					case errCh <- err:
					default:
					}
					cancel()
					return
				}

				mu.Lock()
				for _, summary := range summaries {
					key := summary.ContractID + "|" + summary.ReleaseDate.Format("2006-01-02") + "|" + summary.Amount.StringFixed(2)
					if _, ok := seen[key]; ok {
						continue
					}
					seen[key] = struct{}{}
					mk := monthKey(summary.ReleaseDate)
					buckets[mk] = append(buckets[mk], summary)
				}
				mu.Unlock()
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

func qldDownload(ctx context.Context, client *http.Client, downloadURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
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
		return nil, fmt.Errorf("qld download %s: status %d", downloadURL, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func qldParseTabular(job qldDownloadJob, body []byte, needed map[string]dateWindow) ([]MatchSummary, error) {
	ext := strings.ToLower(job.format)
	switch ext {
	case "csv":
		return qldParseCSV(job, body, needed)
	case "xlsx":
		return qldParseXLSX(job, body, needed)
	case "xls":
		return qldParseXLS(job, body, needed)
	default:
		return nil, fmt.Errorf("unsupported qld format: %s", job.format)
	}
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

func qldFileExt(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(parsed.Path), "."))
	if ext == "" {
		return ""
	}
	switch ext {
	case "csv", "xlsx", "xls":
		return ext
	default:
		return ""
	}
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
