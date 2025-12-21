package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/chromedp"
	"github.com/gocolly/colly/v2"
	"github.com/leekchan/accounting"
	"github.com/shopspring/decimal"
)

const vicSourceID = "vic"
const vicSearchURL = "https://www.tenders.vic.gov.au/contract/search"

var errVicForbidden = errors.New("vic scrape forbidden")

// Chrome-like UA to reduce blocks.
const vicUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// vicSource scrapes Buying for Victoria contract listings.
type vicSource struct{}

func newVicSource() Source {
	return vicSource{}
}

func (v vicSource) ID() string { return vicSourceID }

func (v vicSource) Run(ctx context.Context, req SearchRequest) (string, error) {
	lookbackPeriod := resolveLookbackPeriod(req.LookbackPeriod)
	startResolved, endResolved := resolveDates(req.StartDate, req.EndDate, lookbackPeriod)
	req.StartDate = startResolved
	req.EndDate = endResolved

	target := buildVicSearchURL(req)
	if strings.EqualFold(os.Getenv("VIC_USE_BROWSER"), "true") {
		return runVicWithBrowser(ctx, target, req)
	}

	collector := colly.NewCollector(
		colly.AllowedDomains("www.tenders.vic.gov.au", "tenders.vic.gov.au"),
		colly.AllowURLRevisit(),
		colly.UserAgent(vicUserAgent),
		colly.CacheDir(filepath.Join(defaultCacheDir(), "vic_cookies")),
	)
	collector.WithTransport(&http.Transport{Proxy: http.ProxyFromEnvironment})
	_ = collector.Limit(&colly.LimitRule{DomainGlob: "*tenders.vic.gov.au*", Parallelism: 2, RandomDelay: 500 * time.Millisecond})
	collector.SetRequestTimeout(resolveTimeout())

	collector.OnRequest(func(r *colly.Request) {
		if ctx.Err() != nil {
			r.Abort()
		}
		r.Headers.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		r.Headers.Set("Accept-Language", "en")
		r.Headers.Set("Referer", vicSearchURL)
		r.Headers.Set("Upgrade-Insecure-Requests", "1")
	})

	var scrapeErr error
	total := decimal.Zero
	observedRows := 0
	var totalsMu sync.Mutex

	collector.OnError(func(_ *colly.Response, err error) {
		scrapeErr = err
	})

	collector.OnResponse(func(r *colly.Response) {
		// Buying for Victoria intermittently blocks non-browser clients.
		// Treat 403 as a signal to fall back to the headless browser flow.
		if r != nil && r.StatusCode == http.StatusForbidden {
			scrapeErr = errVicForbidden
			r.Request.Abort()
		}
	})

	collector.OnHTML("table", func(e *colly.HTMLElement) {
		if !isVicResultsTable(e.DOM) {
			return
		}
		e.DOM.Find("tbody tr").Each(func(_ int, row *goquery.Selection) {
			totalsMu.Lock()
			observedRows++
			totalsMu.Unlock()
			cells := row.Find("td")
			if cells.Length() < 6 {
				return
			}
			getText := func(idx int) string {
				return strings.TrimSpace(cells.Eq(idx).Text())
			}
			contractID := getText(0)
			if !isLikelyVicContractID(contractID) {
				return
			}
			title := getText(1)
			status := getText(2)
			startDate := parseVicDate(getText(3))
			if startDate.IsZero() {
				return
			}
			endDate := parseVicDate(getText(4))
			amount := parseVicAmount(getText(5))

			agency := ""
			supplier := ""
			if cells.Length() > 6 {
				agency = getText(6)
			}
			if cells.Length() > 7 {
				supplier = getText(7)
			}

			detailLink := ""
			if href, ok := row.Find("a").First().Attr("href"); ok {
				detailLink = strings.TrimSpace(href)
			}
			if detailLink != "" {
				detailLink = e.Request.AbsoluteURL(detailLink)
			}
			if (agency == "" || supplier == "") && detailLink != "" && ctx.Err() == nil {
				detailAgency, detailSupplier, detailErr := fetchVicDetail(ctx, detailLink)
				if detailErr == nil {
					if agency == "" {
						agency = detailAgency
					}
					if supplier == "" {
						supplier = detailSupplier
					}
				}
			}

			summary := MatchSummary{
				Source:      vicSourceID,
				ContractID:  contractID,
				ReleaseID:   contractID,
				OCID:        contractID,
				Supplier:    supplier,
				Agency:      agency,
				Title:       buildVicTitle(title, status),
				Amount:      amount,
				ReleaseDate: startDate,
			}

			if req.OnAnyMatch != nil {
				req.OnAnyMatch(summary)
			}

			if !matchesSummaryFilters(req, summary, endDate) {
				return
			}

			if req.OnMatch != nil {
				req.OnMatch(summary)
			}
			totalsMu.Lock()
			total = total.Add(summary.Amount)
			totalsMu.Unlock()
		})
	})

	collector.OnHTML("a[aria-label='Next']:not(.disabled)", func(e *colly.HTMLElement) {
		href := strings.TrimSpace(e.Attr("href"))
		if href == "" {
			return
		}
		nextURL := e.Request.AbsoluteURL(href)
		_ = e.Request.Visit(nextURL)
	})

	// Pre-warm session to pick up cookies before hitting search results.
	_ = collector.Request("GET", vicSearchURL, nil, nil, http.Header{
		"Accept":          []string{"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"},
		"Accept-Language": []string{"en"},
		"Referer":         []string{vicSearchURL},
	})

	if err := collector.Visit(target); err != nil {
		// Some blocks surface as a Visit error.
		if errors.Is(err, errVicForbidden) || strings.Contains(strings.ToLower(err.Error()), "forbidden") {
			return runVicWithBrowser(ctx, target, req)
		}
		return "", err
	}
	collector.Wait()
	if scrapeErr != nil {
		if errors.Is(scrapeErr, errVicForbidden) || strings.Contains(strings.ToLower(scrapeErr.Error()), "forbidden") {
			return runVicWithBrowser(ctx, target, req)
		}
		return "", scrapeErr
	}

	// The VIC search page is frequently rendered client-side. In those cases Colly sees an empty
	// table despite a 200 response. If we didn't observe any result rows, retry with headless Chrome.
	totalsMu.Lock()
	rows := observedRows
	totalsMu.Unlock()
	if rows == 0 && ctx.Err() == nil {
		return runVicWithBrowser(ctx, target, req)
	}

	return formatMoneyDecimal(total), nil
}

func buildVicSearchURL(req SearchRequest) string {
	params := url.Values{}
	params.Set("keywords", strings.TrimSpace(req.Keyword))
	params.Set("title", "")
	params.Set("code", "")
	params.Set("buyerId", "")
	params.Set("supplierName", strings.TrimSpace(req.Company))
	params.Set("minCost", "")
	params.Set("expiryDateFrom", "")
	params.Set("expiryDateTo", "")
	params.Set("contractStatus", "")
	params.Set("awardedDateFrom", "")
	params.Set("page", "")
	params.Set("preset", "")
	params.Set("browse", "false")
	params.Set("desc", "true")
	params.Set("orderBy", "startDate")

	if agency := strings.TrimSpace(req.Agency); agency != "" {
		// If it looks like a numeric ID, use buyerId, otherwise keywords
		if _, err := strconv.Atoi(agency); err == nil {
			params.Set("buyerId", agency)
		} else {
			// Append to keywords if not already there
			k := params.Get("keywords")
			if k == "" {
				params.Set("keywords", agency)
			} else if !strings.Contains(strings.ToLower(k), strings.ToLower(agency)) {
				params.Set("keywords", k+" "+agency)
			}
		}
	}

	if !req.StartDate.IsZero() {
		params.Set("startDateFrom", req.StartDate.Format("02/01/2006"))
	}
	if !req.EndDate.IsZero() {
		params.Set("startDateTo", req.EndDate.Format("02/01/2006"))
	}

	return fmt.Sprintf("%s?%s", vicSearchURL, params.Encode())
}

func parseVicDate(raw string) time.Time {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		return time.Time{}
	}
	layouts := []string{"2 Jan 2006", "02 Jan 2006", "2 January 2006", "02 January 2006"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, cleaned); err == nil {
			return t
		}
	}
	return time.Time{}
}

func parseVicAmount(raw string) decimal.Decimal {
	cleaned := strings.ReplaceAll(raw, "\u00a0", " ")
	d, err := parseMoneyToDecimal(cleaned)
	if err != nil {
		return decimal.Zero
	}
	return d
}

func runVicWithBrowser(ctx context.Context, target string, req SearchRequest) (string, error) {
	// Headless Chrome fallback for anti-bot protections.
	allocCtx, cancel := chromedp.NewExecAllocator(ctx,
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.UserAgent(vicUserAgent),
	)
	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	defer cancelCtx()
	defer cancel()

	if err := chromedp.Run(ctx,
		chromedp.Navigate(target),
		chromedp.WaitVisible(`table`, chromedp.ByQuery),
	); err != nil {
		return "", err
	}

	// The results table is often populated asynchronously.
	if err := waitForVicResultRows(ctx, 10*time.Second); err != nil {
		return "", err
	}

	total := decimal.Zero
	seen := make(map[string]struct{})
	const maxPages = 50

	for page := 0; page < maxPages; page++ {
		var pageHTML string
		if err := chromedp.Run(ctx,
			chromedp.WaitVisible(`table`, chromedp.ByQuery),
			chromedp.OuterHTML(`html`, &pageHTML, chromedp.ByQuery),
		); err != nil {
			return "", err
		}

		doc, err := goquery.NewDocumentFromReader(strings.NewReader(pageHTML))
		if err != nil {
			return "", err
		}

		resultsTable := doc.Find("table").FilterFunction(func(_ int, s *goquery.Selection) bool {
			return isVicResultsTable(s)
		}).First()
		if resultsTable.Length() == 0 {
			break
		}

		resultsTable.Find("tbody tr").Each(func(_ int, s *goquery.Selection) {
			cells := s.Find("td")
			if cells.Length() < 6 {
				return
			}
			getText := func(idx int) string {
				return strings.TrimSpace(cells.Eq(idx).Text())
			}
			contractID := getText(0)
			if !isLikelyVicContractID(contractID) {
				return
			}
			if contractID == "" {
				return
			}
			if _, ok := seen[contractID]; ok {
				return
			}
			seen[contractID] = struct{}{}

			title := getText(1)
			status := getText(2)
			startDate := parseVicDate(getText(3))
			if startDate.IsZero() {
				return
			}
			endDate := parseVicDate(getText(4))
			amount := parseVicAmount(getText(5))
			agency := ""
			supplier := ""
			if cells.Length() > 6 {
				agency = getText(6)
			}
			if cells.Length() > 7 {
				supplier = getText(7)
			}
			summary := MatchSummary{
				Source:      vicSourceID,
				ContractID:  contractID,
				ReleaseID:   contractID,
				OCID:        contractID,
				Supplier:    supplier,
				Agency:      agency,
				Title:       buildVicTitle(title, status),
				Amount:      amount,
				ReleaseDate: startDate,
			}
			if req.OnAnyMatch != nil {
				req.OnAnyMatch(summary)
			}
			if matchesSummaryFilters(req, summary, endDate) {
				if req.OnMatch != nil {
					req.OnMatch(summary)
				}
				total = total.Add(summary.Amount)
			}
		})

		var nextHref string
		if err := chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(
				`(() => { const a = document.querySelector("a[aria-label='Next']:not(.disabled)"); return a ? a.getAttribute('href') : ""; })()`,
				&nextHref,
			),
		); err != nil {
			return "", err
		}
		nextHref = strings.TrimSpace(nextHref)
		if nextHref == "" {
			break
		}
		nextURL, err := url.Parse(nextHref)
		if err != nil {
			break
		}
		base, err := url.Parse(vicSearchURL)
		if err != nil {
			break
		}
		resolved := base.ResolveReference(nextURL)
		if err := chromedp.Run(ctx,
			chromedp.Navigate(resolved.String()),
			chromedp.WaitVisible(`table`, chromedp.ByQuery),
		); err != nil {
			return "", err
		}
		if err := waitForVicResultRows(ctx, 10*time.Second); err != nil {
			return "", err
		}
	}

	ac := accounting.Accounting{Symbol: "$", Precision: 2}
	return ac.FormatMoney(total), nil
}

func waitForVicResultRows(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var rowCount int
		err := chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`document.querySelectorAll("table a[href*='/contract/view']").length`, &rowCount),
		)
		if err == nil && rowCount > 0 {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return nil
}

func isVicResultsTable(table *goquery.Selection) bool {
	headers := make([]string, 0, 8)
	table.Find("thead th").Each(func(_ int, s *goquery.Selection) {
		headers = append(headers, strings.ToLower(strings.TrimSpace(s.Text())))
	})
	if len(headers) == 0 {
		// Fallback when thead is absent.
		table.Find("tr").First().Find("th").Each(func(_ int, s *goquery.Selection) {
			headers = append(headers, strings.ToLower(strings.TrimSpace(s.Text())))
		})
	}
	hay := strings.Join(headers, " | ")
	return strings.Contains(hay, "contract") &&
		strings.Contains(hay, "title") &&
		(strings.Contains(hay, "value") || strings.Contains(hay, "cost"))
}

func isLikelyVicContractID(contractID string) bool {
	contractID = strings.TrimSpace(contractID)
	if len(contractID) < 4 {
		return false
	}
	if strings.IndexFunc(contractID, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r == '\r'
	}) != -1 {
		return false
	}
	hasDigit := false
	for _, r := range contractID {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r == '-' || r == '_':
		default:
			return false
		}
	}
	return hasDigit
}

func fetchVicDetail(ctx context.Context, detailURL string) (string, string, error) {
	collector := colly.NewCollector(
		colly.AllowedDomains("www.tenders.vic.gov.au", "tenders.vic.gov.au"),
		colly.UserAgent(vicUserAgent),
		colly.AllowURLRevisit(),
		colly.CacheDir(filepath.Join(defaultCacheDir(), "vic_cookies")),
	)
	_ = collector.Limit(&colly.LimitRule{DomainGlob: "*tenders.vic.gov.au*", Parallelism: 1, RandomDelay: 400 * time.Millisecond})
	collector.SetRequestTimeout(resolveTimeout())
	collector.OnRequest(func(r *colly.Request) {
		if ctx.Err() != nil {
			r.Abort()
		}
		r.Headers.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		r.Headers.Set("Accept-Language", "en")
		r.Headers.Set("Referer", vicSearchURL)
	})

	var agency, supplier string
	var scrapeErr error
	done := make(chan struct{})

	collector.OnError(func(_ *colly.Response, err error) {
		scrapeErr = err
	})

	collector.OnHTML("table", func(e *colly.HTMLElement) {
		e.ForEach("tr", func(_ int, tr *colly.HTMLElement) {
			label := strings.ToLower(strings.TrimSpace(tr.ChildText("th")))
			val := strings.TrimSpace(tr.ChildText("td"))
			switch label {
			case "issued by":
				agency = val
			case "supplier":
				supplier = val
			}
		})
	})

	collector.OnScraped(func(_ *colly.Response) {
		close(done)
	})

	if err := collector.Visit(detailURL); err != nil {
		return "", "", err
	}

	select {
	case <-done:
	case <-ctx.Done():
		return agency, supplier, ctx.Err()
	}

	if scrapeErr != nil {
		return agency, supplier, scrapeErr
	}
	return agency, supplier, nil
}

func buildVicTitle(title, status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return title
	}
	return fmt.Sprintf("%s (%s)", title, status)
}

func matchesSummaryFilters(req SearchRequest, summary MatchSummary, endDate time.Time) bool {
	keyword := strings.ToLower(strings.TrimSpace(req.Keyword))
	if keyword != "" {
		hay := strings.ToLower(strings.Join([]string{
			summary.ContractID,
			summary.Title,
			summary.Supplier,
			summary.Agency,
		}, " "))
		if !strings.Contains(hay, keyword) {
			return false
		}
	}

	if company := strings.ToLower(strings.TrimSpace(req.Company)); company != "" {
		if !strings.Contains(strings.ToLower(summary.Supplier), company) {
			return false
		}
	}

	if agency := strings.ToLower(strings.TrimSpace(req.Agency)); agency != "" {
		if !strings.Contains(strings.ToLower(summary.Agency), agency) {
			return false
		}
	}

	if !req.StartDate.IsZero() && summary.ReleaseDate.Before(req.StartDate) {
		return false
	}
	if !req.EndDate.IsZero() {
		upper := req.EndDate
		if !endDate.IsZero() {
			upper = endDate
		}
		if summary.ReleaseDate.After(req.EndDate) && (upper.IsZero() || upper.After(req.EndDate)) {
			return false
		}
	}

	return true
}
