package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/leekchan/accounting"
	"github.com/shopspring/decimal"
)

const (
	defaultBaseURL        = "https://api.tenders.gov.au/ocds"
	defaultDateType       = "contractPublished"
	defaultLookbackPeriod = 20
	defaultRequestTimeout = 0
	maxWindowDays         = 31
	requestMaxRetries     = 4
	initialRetryDelay     = time.Second
)

var defaultMaxConcurrency = determineDefaultConcurrency()

var defaultHTTPClient = &http.Client{Timeout: 30 * time.Second}

// SearchRequest defines all supported filters when querying the OCDS API.
type SearchRequest struct {
	Keyword           string
	Company           string
	Agency            string
	Source            string
	StartDate         time.Time
	EndDate           time.Time
	DateType          string
	LookbackPeriod    int
	Verbose           bool
	OnMatch           MatchHandler
	OnProgress        ProgressHandler
	OnAnyMatch        MatchHandler              // called for every valued release, regardless of filters
	ShouldFetchWindow func(win dateWindow) bool // optional gate to skip a date window
}

// MatchHandler streams each matching contract summary when verbose output is enabled.
type MatchHandler func(MatchSummary)

// ProgressHandler reports batch progress as windows finish processing.
type ProgressHandler func(completed, total int)

// MatchSummary captures the key fields printed for each matching contract.
type MatchSummary struct {
	ContractID  string
	ReleaseID   string
	OCID        string
	Source      string
	Supplier    string
	Agency      string
	Title       string
	Amount      decimal.Decimal
	ReleaseDate time.Time
	IsUpdate    bool
}

type ocdsResponse struct {
	Releases  []ocdsRelease `json:"releases"`
	Links     ocdsLinks     `json:"links"`
	ErrorCode int           `json:"errorCode"`
	Message   string        `json:"message"`
}

type ocdsLinks struct {
	Next string `json:"next"`
}

type ocdsRelease struct {
	ID        string         `json:"id"`
	OCID      string         `json:"ocid"`
	Date      string         `json:"date"`
	Tag       []string       `json:"tag"`
	Parties   []ocdsParty    `json:"parties"`
	Contracts []ocdsContract `json:"contracts"`
	Tender    *ocdsTender    `json:"tender"`
}

type ocdsParty struct {
	Name  string   `json:"name"`
	Roles []string `json:"roles"`
}

type ocdsTender struct {
	Description              string `json:"description"`
	ProcurementMethodDetails string `json:"procurementMethodDetails"`
}

type ocdsContract struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Value       *ocdsValue      `json:"value"`
	Amendments  []ocdsAmendment `json:"amendments"`
}

type ocdsValue struct {
	Amount decimal.Decimal `json:"amount"`
}

type ocdsAmendment struct {
	ID                     string          `json:"id"`
	AmendedValue           decimal.Decimal `json:"amendedvalue"`
	ContractAmendmentValue decimal.Decimal `json:"contractamendmentvalue"`
}

type contractAggregate struct {
	Value     decimal.Decimal
	UpdatedAt time.Time
}

type contractAggregator struct {
	filters    SearchRequest
	aggregates map[string]contractAggregate
	sink       MatchHandler
}

func newContractAggregator(req SearchRequest, sink MatchHandler) *contractAggregator {
	normalized := req
	normalized.Source = normalizeSourceID(req.Source)
	return &contractAggregator{
		filters:    normalized,
		aggregates: make(map[string]contractAggregate),
		sink:       sink,
	}
}

func (a *contractAggregator) process(rel ocdsRelease) {
	if !isContractRelease(rel) || !matchesFilters(rel, a.filters) {
		return
	}
	contractID, ok := canonicalContractID(rel)
	if !ok {
		return
	}
	amount, ok := releaseValue(rel)
	if !ok || amount.LessThanOrEqual(decimal.Zero) {
		return
	}
	releaseTime := parseReleaseTime(rel.Date)
	summary := MatchSummary{
		Source:      normalizeSourceID(a.filters.Source),
		ContractID:  contractID,
		ReleaseID:   rel.ID,
		OCID:        rel.OCID,
		Supplier:    primarySupplier(rel),
		Agency:      primaryAgency(rel),
		Title:       contractTitle(rel),
		Amount:      amount,
		ReleaseDate: releaseTime,
	}

	// Always write to sink for cache/lake population regardless of user filters.
	if a.sink != nil {
		a.sink(summary)
	}

	if !matchesFilters(rel, a.filters) {
		return
	}

	entry, exists := a.aggregates[contractID]
	if exists && !releaseTime.After(entry.UpdatedAt) {
		return
	}
	a.aggregates[contractID] = contractAggregate{Value: amount, UpdatedAt: releaseTime}
	if a.filters.OnMatch != nil {
		summary.IsUpdate = exists
		a.filters.OnMatch(summary)
	}
}

func (a *contractAggregator) total() decimal.Decimal {
	total := decimal.Zero
	for _, agg := range a.aggregates {
		total = total.Add(agg.Value)
	}
	return total
}

type ocdsClient struct {
	baseURL       string
	dateType      string
	httpClient    *http.Client
	maxConcurrent int
}

// RunSearch dispatches to the requested source, defaulting to the federal OCDS API.
func RunSearch(ctx context.Context, req SearchRequest) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ensureSourcesRegistered()
	req.Source = normalizeSourceID(req.Source)
	src, err := resolveSource(req.Source)
	if err != nil {
		return "", err
	}
	req.Source = src.ID()
	return src.Run(ctx, req)
}

type federalSource struct{}

func newFederalSource() Source {
	return federalSource{}
}

func (f federalSource) ID() string { return defaultSourceID }

func (f federalSource) Run(ctx context.Context, req SearchRequest) (string, error) {
	return runFederalSearch(ctx, req)
}

func runFederalSearch(ctx context.Context, req SearchRequest) (string, error) {
	timeout := resolveTimeout()
	cancel := func() {}
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	lookbackPeriod := resolveLookbackPeriod(req.LookbackPeriod)
	start, end := resolveDates(req.StartDate, req.EndDate, lookbackPeriod)
	dateType := req.DateType
	if dateType == "" {
		dateType = defaultDateType
	}

	baseURL := strings.TrimSuffix(envOrDefault("AUSTENDER_OCDS_BASE_URL", defaultBaseURL), "/")
	client := &ocdsClient{
		baseURL:       baseURL,
		dateType:      dateType,
		httpClient:    defaultHTTPClient,
		maxConcurrent: defaultMaxConcurrency,
	}

	req.Source = normalizeSourceID(req.Source)
	agg := newContractAggregator(req, req.OnAnyMatch)
	if err := client.fetchAll(ctx, start, end, agg.process, req.OnProgress, req.ShouldFetchWindow); err != nil {
		return "", err
	}

	total := agg.total()
	ac := accounting.Accounting{Symbol: "$", Precision: 2}
	return ac.FormatMoney(total), nil
}

// RunScrape keeps the original signature for compatibility but now calls RunSearch.
func RunScrape(keywordVal, companyName, agencyVal string) (string, error) {
	return RunSearch(context.Background(), SearchRequest{
		Keyword: keywordVal,
		Company: companyName,
		Agency:  agencyVal,
	})
}

func (c *ocdsClient) fetchAll(ctx context.Context, start, end time.Time, consume func(ocdsRelease), onProgress ProgressHandler, shouldFetch func(dateWindow) bool) error {
	windows := splitDateWindows(start, end, maxWindowDays)
	if len(windows) == 0 {
		return nil
	}
	totalWindows := len(windows)
	notifyProgress := func(completed int) {
		if onProgress != nil {
			onProgress(completed, totalWindows)
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		idx int
		rel []ocdsRelease
		err error
	}

	resCh := make(chan result, len(windows))
	sem := make(chan struct{}, c.concurrencyLimit())
	var wg sync.WaitGroup
	completed := 0

	for idx, window := range windows {
		if shouldFetch != nil && !shouldFetch(window) {
			completed++
			notifyProgress(completed)
			continue
		}
		wg.Add(1)
		go func(i int, win dateWindow) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				resCh <- result{idx: i, err: ctx.Err()}
				return
			}
			defer func() { <-sem }()
			rels, err := c.fetchWindow(ctx, win.start, win.end)
			if err != nil {
				cancel()
			}
			resCh <- result{idx: i, rel: rels, err: err}
		}(idx, window)
	}

	go func() {
		wg.Wait()
		close(resCh)
	}()
	for res := range resCh {
		if res.err != nil && !errors.Is(res.err, context.Canceled) {
			return res.err
		}
		if res.err == nil {
			for _, rel := range res.rel {
				consume(rel)
			}
			completed++
			notifyProgress(completed)
		}
	}
	return nil
}

func (c *ocdsClient) fetchWindow(ctx context.Context, start, end time.Time) ([]ocdsRelease, error) {
	var all []ocdsRelease
	nextURL := c.initialURLRange(start, end)
	for nextURL != "" {
		resp, err := c.doRequest(ctx, nextURL)
		if err != nil {
			return nil, err
		}
		all = append(all, resp.Releases...)
		nextURL = resp.Links.Next
	}
	return all, nil
}

func (c *ocdsClient) doRequest(ctx context.Context, target string) (*ocdsResponse, error) {
	var lastErr error
	backoff := initialRetryDelay
	if backoff <= 0 {
		backoff = time.Second
	}
	for attempt := 0; attempt <= requestMaxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		resp, err := c.httpClient.Do(req)
		if err == nil {
			if resp.StatusCode == http.StatusOK {
				var decoded ocdsResponse
				decodeErr := json.NewDecoder(resp.Body).Decode(&decoded)
				resp.Body.Close()
				if decodeErr != nil {
					return nil, decodeErr
				}
				if decoded.ErrorCode != 0 {
					return nil, fmt.Errorf("ocds api error %d: %s", decoded.ErrorCode, decoded.Message)
				}
				return &decoded, nil
			}
			err = fmt.Errorf("ocds api returned %s", resp.Status)
			resp.Body.Close()
			if !shouldRetryStatus(resp.StatusCode) {
				return nil, err
			}
		}
		lastErr = err
		if attempt == requestMaxRetries {
			break
		}
		if sleepErr := sleepWithContext(ctx, backoff); sleepErr != nil {
			return nil, sleepErr
		}
		backoff *= 2
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("failed to contact ocds api after retries")
	}
	return nil, lastErr
}

func (c *ocdsClient) concurrencyLimit() int {
	if c.maxConcurrent <= 0 {
		return 1
	}
	return c.maxConcurrent
}

func (c *ocdsClient) initialURLRange(start, end time.Time) string {
	return fmt.Sprintf("%s/findByDates/%s/%s/%s",
		c.baseURL,
		url.PathEscape(c.dateType),
		start.Format(time.RFC3339),
		end.Format(time.RFC3339))
}

func isContractRelease(rel ocdsRelease) bool {
	for _, tag := range rel.Tag {
		if tag == "contract" || tag == "contractAmendment" {
			return true
		}
	}
	return false
}

func canonicalContractID(rel ocdsRelease) (string, bool) {
	if len(rel.Contracts) == 0 {
		return "", false
	}
	id := rel.Contracts[0].ID
	if id == "" && len(rel.Contracts[0].Amendments) > 0 {
		id = rel.Contracts[0].Amendments[0].ID
	}
	if id == "" {
		return "", false
	}
	if idx := strings.Index(id, "-A"); idx > 0 {
		return id[:idx], true
	}
	return id, true
}

func releaseValue(rel ocdsRelease) (decimal.Decimal, bool) {
	if len(rel.Contracts) == 0 {
		return decimal.Zero, false
	}
	contract := rel.Contracts[0]
	isAmendment := false
	for _, tag := range rel.Tag {
		if tag == "contractAmendment" {
			isAmendment = true
			break
		}
	}
	if isAmendment && len(contract.Amendments) > 0 {
		amend := contract.Amendments[0]
		if amend.AmendedValue.GreaterThan(decimal.Zero) {
			return amend.AmendedValue, true
		}
		if amend.ContractAmendmentValue.GreaterThan(decimal.Zero) {
			base := decimal.Zero
			if contract.Value != nil {
				base = contract.Value.Amount
			}
			return base.Add(amend.ContractAmendmentValue), true
		}
	}
	if contract.Value == nil {
		return decimal.Zero, false
	}
	return contract.Value.Amount, true
}

func matchesFilters(rel ocdsRelease, req SearchRequest) bool {
	keyword := strings.TrimSpace(req.Keyword)
	if keyword != "" && !releaseContainsKeyword(rel, keyword) {
		return false
	}
	company := strings.TrimSpace(req.Company)
	if company != "" && !strings.Contains(strings.ToLower(primarySupplier(rel)), strings.ToLower(company)) {
		return false
	}
	agency := strings.TrimSpace(req.Agency)
	if agency != "" && !strings.Contains(strings.ToLower(primaryAgency(rel)), strings.ToLower(agency)) {
		return false
	}
	return true
}

func releaseContainsKeyword(rel ocdsRelease, keyword string) bool {
	needle := strings.ToLower(keyword)
	for _, text := range []string{
		rel.ID,
		rel.OCID,
		rel.ContractsText(),
		rel.TenderText(),
		primarySupplier(rel),
	} {
		if text != "" && strings.Contains(strings.ToLower(text), needle) {
			return true
		}
	}
	return false
}

func (rel ocdsRelease) ContractsText() string {
	if len(rel.Contracts) == 0 {
		return ""
	}
	contract := rel.Contracts[0]
	return strings.Join([]string{contract.Title, contract.Description}, " ")
}

func contractTitle(rel ocdsRelease) string {
	if len(rel.Contracts) == 0 {
		return ""
	}
	return rel.Contracts[0].Title
}

func (rel ocdsRelease) TenderText() string {
	if rel.Tender == nil {
		return ""
	}
	return strings.Join([]string{rel.Tender.Description, rel.Tender.ProcurementMethodDetails}, " ")
}

func primarySupplier(rel ocdsRelease) string {
	for _, party := range rel.Parties {
		for _, role := range party.Roles {
			if strings.EqualFold(role, "supplier") {
				return party.Name
			}
		}
	}
	if len(rel.Parties) > 0 {
		return rel.Parties[0].Name
	}
	return ""
}

func primaryAgency(rel ocdsRelease) string {
	for _, party := range rel.Parties {
		for _, role := range party.Roles {
			if strings.EqualFold(role, "procuringEntity") || strings.EqualFold(role, "buyer") {
				return party.Name
			}
		}
	}
	return ""
}

func parseReleaseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t
	}
	return time.Time{}
}

func resolveDates(start, end time.Time, lookbackPeriod int) (time.Time, time.Time) {
	if lookbackPeriod <= 0 {
		lookbackPeriod = defaultLookbackPeriod
	}
	endUTC := end
	if endUTC.IsZero() {
		endUTC = time.Now().UTC()
	}
	startUTC := start
	if startUTC.IsZero() {
		startUTC = endUTC.AddDate(-lookbackPeriod, 0, 0)
	}
	if startUTC.After(endUTC) {
		startUTC, endUTC = endUTC, startUTC
	}
	return startUTC.UTC(), endUTC.UTC()
}

func envOrDefault(key, fallback string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return fallback
}

func parseDateInput(raw string) (time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return time.Time{}, nil
	}
	formats := []string{time.RFC3339, "2006-01-02"}
	for _, layout := range formats {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid date %q", raw)
}

func scrapeAncap(keywordVal, companyName, agencyVal, sourceVal string, start, end time.Time, dateType string, lookbackPeriod int, verbose bool) {
	var onMatch MatchHandler
	if verbose {
		onMatch = func(summary MatchSummary) {
			dateText := ""
			if !summary.ReleaseDate.IsZero() {
				dateText = summary.ReleaseDate.Format("2006-01-02")
			}
			fmt.Printf("[match] %s | %s | %s | %s | %s | %s\n",
				dateText,
				summary.ContractID,
				summary.Supplier,
				summary.Agency,
				summary.Amount.StringFixed(2),
				summary.Title,
			)
		}
	}
	var onProgress ProgressHandler
	var progressWriter *progressPrinter
	if !verbose {
		progressWriter = newProgressPrinter(28)
		onProgress = func(done, total int) {
			progressWriter.Update(done, total)
		}
	}
	if progressWriter != nil {
		defer progressWriter.Finish()
	}
	result, cacheHit, err := RunSearchWithCache(context.Background(), SearchRequest{
		Keyword:        keywordVal,
		Company:        companyName,
		Agency:         agencyVal,
		Source:         sourceVal,
		StartDate:      start,
		EndDate:        end,
		DateType:       dateType,
		LookbackPeriod: lookbackPeriod,
		Verbose:        verbose,
		OnMatch:        onMatch,
		OnProgress:     onProgress,
	})
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	totalStyle := color.New(color.FgRed, color.Bold)
	if cacheHit {
		fmt.Printf("Total Contract (cache): %s\n", totalStyle.Sprint(result))
		return
	}
	fmt.Printf("Total Contract: %s\n", totalStyle.Sprint(result))
}

// validateDateOrder helps CLI inputs provide user-friendly errors.
func validateDateOrder(start, end time.Time) error {
	if !start.IsZero() && !end.IsZero() && start.After(end) {
		return errors.New("start date cannot be after end date")
	}
	return nil
}

type dateWindow struct {
	start time.Time
	end   time.Time
}

func splitDateWindows(start, end time.Time, windowDays int) []dateWindow {
	if windowDays <= 0 {
		windowDays = maxWindowDays
	}
	if end.Before(start) {
		return []dateWindow{{start: start, end: end}}
	}
	if start.Equal(end) {
		return []dateWindow{{start: start, end: end}}
	}
	var windows []dateWindow
	current := start
	for current.Before(end) {
		next := current.AddDate(0, 0, windowDays)
		if next.After(end) {
			next = end
		}
		windows = append(windows, dateWindow{start: current, end: next})
		if !next.After(current) {
			break
		}
		current = next
	}
	if len(windows) == 0 {
		windows = append(windows, dateWindow{start: start, end: end})
	}
	return windows
}

func resolveTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("AUSTENDER_REQUEST_TIMEOUT"))
	if raw == "" {
		return defaultRequestTimeout
	}
	dur, err := time.ParseDuration(raw)
	if err != nil || dur <= 0 {
		return defaultRequestTimeout
	}
	return dur
}

func determineDefaultConcurrency() int {
	cores := runtime.NumCPU()
	if cores <= 1 {
		return 1
	}
	return cores - 1
}

func resolveLookbackPeriod(override int) int {
	if override > 0 {
		return override
	}
	raw := strings.TrimSpace(os.Getenv("AUSTENDER_LOOKBACK_PERIOD"))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("AUSTENDER_LOOKBACK_YEARS"))
	}
	if raw != "" {
		if yrs, err := strconv.Atoi(raw); err == nil && yrs > 0 {
			return yrs
		}
	}
	return defaultLookbackPeriod
}

func shouldRetryStatus(code int) bool {
	if code == http.StatusTooManyRequests {
		return true
	}
	return code >= 500 && code < 600
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type progressPrinter struct {
	width    int
	printed  bool
	finished bool
}

func newProgressPrinter(width int) *progressPrinter {
	if width <= 0 {
		width = 20
	}
	return &progressPrinter{width: width}
}

func (p *progressPrinter) Update(done, total int) {
	if total <= 0 {
		return
	}
	if done < 0 {
		done = 0
	}
	if done > total {
		done = total
	}
	filled := done * p.width / total
	if filled > p.width {
		filled = p.width
	}
	bar := strings.Repeat("#", filled) + strings.Repeat("-", p.width-filled)
	fmt.Printf("\rProgress [%s] %d/%d", bar, done, total)
	p.printed = true
	if done == total {
		p.finishLine()
	}
}

func (p *progressPrinter) Finish() {
	if !p.printed || p.finished {
		return
	}
	p.finishLine()
}

func (p *progressPrinter) finishLine() {
	if p.finished {
		return
	}
	fmt.Print("\n")
	p.finished = true
}
