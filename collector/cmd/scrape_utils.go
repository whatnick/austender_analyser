package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/leekchan/accounting"
	"github.com/shopspring/decimal"
)

const (
	defaultBaseURL        = "https://api.tenders.gov.au/ocds"
	defaultDateType       = "contractPublished"
	defaultLookbackDays   = 365
	defaultRequestTimeout = 0
	maxWindowDays         = 31
	defaultMaxConcurrency = 4
)

var defaultHTTPClient = &http.Client{Timeout: 30 * time.Second}

// SearchRequest defines all supported filters when querying the OCDS API.
type SearchRequest struct {
	Keyword   string
	Company   string
	Agency    string
	StartDate time.Time
	EndDate   time.Time
	DateType  string
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

type ocdsClient struct {
	baseURL       string
	dateType      string
	httpClient    *http.Client
	maxConcurrent int
}

// RunSearch queries the OCDS API and returns the formatted contract sum.
func RunSearch(ctx context.Context, req SearchRequest) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := resolveTimeout()
	cancel := func() {}
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	start, end := resolveDates(req.StartDate, req.EndDate)
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

	releases, err := client.fetchAll(ctx, start, end)
	if err != nil {
		return "", err
	}

	total := aggregateReleases(releases, req)
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

func (c *ocdsClient) fetchAll(ctx context.Context, start, end time.Time) ([]ocdsRelease, error) {
	windows := splitDateWindows(start, end, maxWindowDays)
	if len(windows) == 0 {
		return nil, nil
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

	for idx, window := range windows {
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

	batches := make([][]ocdsRelease, len(windows))
	for res := range resCh {
		if res.err != nil && !errors.Is(res.err, context.Canceled) {
			return nil, res.err
		}
		batches[res.idx] = res.rel
	}

	var combined []ocdsRelease
	for _, rels := range batches {
		combined = append(combined, rels...)
	}
	return combined, nil
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ocds api returned %s", resp.Status)
	}
	var decoded ocdsResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	if decoded.ErrorCode != 0 {
		return nil, fmt.Errorf("ocds api error %d: %s", decoded.ErrorCode, decoded.Message)
	}
	return &decoded, nil
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

func aggregateReleases(releases []ocdsRelease, req SearchRequest) decimal.Decimal {
	if len(releases) == 0 {
		return decimal.Zero
	}
	aggregates := make(map[string]contractAggregate)
	for _, rel := range releases {
		if !isContractRelease(rel) {
			continue
		}
		if !matchesFilters(rel, req) {
			continue
		}
		contractID, ok := canonicalContractID(rel)
		if !ok {
			continue
		}
		amount, ok := releaseValue(rel)
		if !ok || amount.LessThanOrEqual(decimal.Zero) {
			continue
		}
		updatedAt := parseReleaseTime(rel.Date)
		agg, exists := aggregates[contractID]
		if !exists || updatedAt.After(agg.UpdatedAt) {
			agg.Value = amount
			agg.UpdatedAt = updatedAt
			aggregates[contractID] = agg
		}
	}
	total := decimal.Zero
	for _, agg := range aggregates {
		total = total.Add(agg.Value)
	}
	return total
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

func resolveDates(start, end time.Time) (time.Time, time.Time) {
	endUTC := end
	if endUTC.IsZero() {
		endUTC = time.Now().UTC()
	}
	startUTC := start
	if startUTC.IsZero() {
		startUTC = endUTC.AddDate(0, 0, -defaultLookbackDays)
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

func scrapeAncap(keywordVal, companyName, agencyVal string, start, end time.Time, dateType string) {
	result, err := RunSearch(context.Background(), SearchRequest{
		Keyword:   keywordVal,
		Company:   companyName,
		Agency:    agencyVal,
		StartDate: start,
		EndDate:   end,
		DateType:  dateType,
	})
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	fmt.Println("Total Contract:" + result)
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
