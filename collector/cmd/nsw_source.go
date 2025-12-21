package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/gocolly/colly/v2"
	"github.com/shopspring/decimal"
)

const nswSourceID = "nsw"
const nswSearchURL = "https://buy.nsw.gov.au/notices/search"

var errNswWAF = errors.New("nsw scrape blocked by WAF")

// Chrome-like UA to reduce blocks.
const nswUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// nswSource scrapes buy.nsw.gov.au notice listings (contract awards).
type nswSource struct{}

func newNswSource() Source {
	return nswSource{}
}

func (n nswSource) ID() string { return nswSourceID }

func (n nswSource) Run(ctx context.Context, req SearchRequest) (string, error) {
	lookbackYears := resolveLookbackYears(req.LookbackYears)
	startResolved, endResolved := resolveDates(req.StartDate, req.EndDate, lookbackYears)

	windows := []dateWindow{{start: startResolved, end: endResolved}}
	if req.ShouldFetchWindow != nil {
		windows = splitDateWindows(startResolved, endResolved, maxWindowDays)
	}

	if strings.EqualFold(strings.TrimSpace(os.Getenv("NSW_USE_BROWSER")), "true") {
		return runNswWithBrowser(ctx, req, windows)
	}

	collector := colly.NewCollector(
		colly.AllowedDomains("buy.nsw.gov.au"),
		colly.AllowURLRevisit(),
		colly.UserAgent(nswUserAgent),
		colly.CacheDir(filepath.Join(defaultCacheDir(), "nsw_cookies")),
	)
	collector.WithTransport(&http.Transport{Proxy: http.ProxyFromEnvironment})
	collector.SetRequestTimeout(resolveTimeout())

	collector.OnRequest(func(r *colly.Request) {
		if ctx.Err() != nil {
			r.Abort()
			return
		}
		r.Headers.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		r.Headers.Set("Accept-Language", "en")
		r.Headers.Set("Upgrade-Insecure-Requests", "1")
		r.Headers.Set("Referer", nswSearchURL)
	})

	var scrapeErr error
	collector.OnError(func(_ *colly.Response, err error) {
		scrapeErr = err
	})
	collector.OnResponse(func(r *colly.Response) {
		if r == nil {
			return
		}
		// buy.nsw.gov.au intermittently serves an AWS WAF JS challenge (202 Accepted)
		// which Colly can't solve. Detect it early and fall back to browser automation.
		if r.StatusCode == http.StatusAccepted || r.StatusCode == http.StatusForbidden {
			if isNswWafChallenge(r.Body) {
				scrapeErr = errNswWAF
				r.Request.Abort()
			}
		}
		if scrapeErr == nil && isNswWafChallenge(r.Body) {
			scrapeErr = errNswWAF
			r.Request.Abort()
		}
	})

	total := decimal.Zero
	seen := make(map[string]struct{})
	var mu sync.Mutex

	collector.OnHTML("ul.cards.profiles > li", func(e *colly.HTMLElement) {
		if ctx.Err() != nil {
			return
		}

		title := strings.TrimSpace(e.ChildText("h3 a"))
		noticeHref := strings.TrimSpace(e.ChildAttr("h3 a", "href"))
		noticeURL := ""
		if noticeHref != "" {
			noticeURL = e.Request.AbsoluteURL(noticeHref)
		}
		noticeID := extractNswNoticeID(noticeURL)

		fields := extractNswDetails(e.DOM)
		agency := strings.TrimSpace(fields["agency"])
		supplier := strings.TrimSpace(fields["contractor name"])
		canID := strings.TrimSpace(fields["can id"])

		publishDate := parseNswDate(fields["publish date"])
		periodStart, periodEnd := parseNswContractPeriod(fields["contract period"])

		amount := decimal.Zero
		if rawAmt := fields["estimated amount payable to the contractor (including gst)"]; rawAmt != "" {
			if parsed, err := parseMoneyToDecimal(rawAmt); err == nil {
				amount = parsed
			}
		}

		contractID := canID
		if contractID == "" {
			contractID = noticeID
		}
		if contractID == "" {
			contractID = title
		}

		// De-dupe (pagination sometimes repeats).
		mu.Lock()
		if _, ok := seen[contractID]; ok {
			mu.Unlock()
			return
		}
		seen[contractID] = struct{}{}
		mu.Unlock()

		summary := MatchSummary{
			Source:      nswSourceID,
			ContractID:  contractID,
			ReleaseID:   noticeID,
			OCID:        contractID,
			Supplier:    supplier,
			Agency:      agency,
			Title:       title,
			Amount:      amount,
			ReleaseDate: publishDate,
		}

		if req.OnAnyMatch != nil {
			req.OnAnyMatch(summary)
		}

		// Reuse shared summary filtering logic.
		if !matchesSummaryFilters(req, summary, periodEnd) {
			return
		}
		if !req.StartDate.IsZero() && !periodStart.IsZero() && periodStart.Before(req.StartDate) {
			// Optional: treat contract period start as an additional date signal.
			// (keep this conservative: only apply when periodStart is present)
		}

		if req.OnMatch != nil {
			req.OnMatch(summary)
		}
		mu.Lock()
		total = total.Add(summary.Amount)
		mu.Unlock()
	})

	// Follow next page links.
	collector.OnHTML(".nsw-pagination__item--next-page a.nsw-direction-link.choose-page", func(e *colly.HTMLElement) {
		href := strings.TrimSpace(e.Attr("href"))
		if href == "" {
			return
		}
		nextURL := e.Request.AbsoluteURL(href)
		_ = e.Request.Visit(nextURL)
	})

	completed := 0
	for _, win := range windows {
		if req.ShouldFetchWindow != nil && !req.ShouldFetchWindow(win) {
			completed++
			if req.OnProgress != nil {
				req.OnProgress(completed, len(windows))
			}
			continue
		}
		startURL := buildNswSearchURL(req, 1, win.start, win.end)
		if err := collector.Visit(startURL); err != nil {
			return "", err
		}
		collector.Wait()
		if scrapeErr != nil {
			if errors.Is(scrapeErr, errNswWAF) {
				return runNswWithBrowser(ctx, req, windows)
			}
			return "", scrapeErr
		}
		completed++
		if req.OnProgress != nil {
			req.OnProgress(completed, len(windows))
		}
	}

	mu.Lock()
	out := formatMoneyDecimal(total)
	mu.Unlock()
	return out, nil
}

func isNswWafChallenge(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	s := strings.ToLower(string(body))
	// Observed markers in the JS challenge response.
	if strings.Contains(s, "awswafcookiedomainlist") {
		return true
	}
	if strings.Contains(s, "gokuprops") {
		return true
	}
	return false
}

func runNswWithBrowser(ctx context.Context, req SearchRequest, windows []dateWindow) (string, error) {
	allocCtx, cancel := chromedp.NewExecAllocator(ctx,
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.UserAgent(nswUserAgent),
	)
	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	defer cancelCtx()
	defer cancel()

	// Best-effort: reduce headless detection used by some bot protections.
	_ = chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		_, err := page.AddScriptToEvaluateOnNewDocument(`
Object.defineProperty(navigator, 'webdriver', { get: () => undefined });
window.chrome = window.chrome || { runtime: {} };
`).Do(ctx)
		return err
	}))

	total := decimal.Zero
	seen := make(map[string]struct{})

	completed := 0
	for _, win := range windows {
		if req.ShouldFetchWindow != nil && !req.ShouldFetchWindow(win) {
			completed++
			if req.OnProgress != nil {
				req.OnProgress(completed, len(windows))
			}
			continue
		}

		currentURL := buildNswSearchURL(req, 1, win.start, win.end)
		for page := 0; page < 200; page++ {
			var pageHTML string
			if err := chromedp.Run(ctx,
				chromedp.Navigate(currentURL),
				chromedp.WaitReady("body", chromedp.ByQuery),
			); err != nil {
				return "", err
			}

			// Allow time for AWS WAF JS challenge / async results to complete.
			_ = waitForNswCards(ctx, 12*time.Second)

			if err := chromedp.Run(ctx,
				chromedp.OuterHTML("html", &pageHTML, chromedp.ByQuery),
			); err != nil {
				return "", err
			}

			lower := strings.ToLower(pageHTML)
			if strings.Contains(lower, "awswafcookiedomainlist") || strings.Contains(lower, "gokuprops") {
				// Give the challenge a bit more time to complete in-browser, then re-read once.
				if err := chromedp.Run(ctx,
					chromedp.Sleep(4*time.Second),
					chromedp.OuterHTML("html", &pageHTML, chromedp.ByQuery),
				); err != nil {
					return "", err
				}
			}

			doc, err := goquery.NewDocumentFromReader(strings.NewReader(pageHTML))
			if err != nil {
				return "", err
			}

			cards := doc.Find("ul.cards.profiles > li")
			cards.Each(func(_ int, s *goquery.Selection) {
				title := strings.TrimSpace(s.Find("h3 a").First().Text())
				noticeHref, _ := s.Find("h3 a").First().Attr("href")
				noticeURL := strings.TrimSpace(noticeHref)
				if strings.HasPrefix(noticeURL, "/") {
					noticeURL = "https://buy.nsw.gov.au" + noticeURL
				}
				noticeID := extractNswNoticeID(noticeURL)

				fields := extractNswDetails(s)
				agency := strings.TrimSpace(fields["agency"])
				supplier := strings.TrimSpace(fields["contractor name"])
				canID := strings.TrimSpace(fields["can id"])

				publishDate := parseNswDate(fields["publish date"])
				periodStart, periodEnd := parseNswContractPeriod(fields["contract period"])

				amount := decimal.Zero
				if rawAmt := fields["estimated amount payable to the contractor (including gst)"]; rawAmt != "" {
					if parsed, err := parseMoneyToDecimal(rawAmt); err == nil {
						amount = parsed
					}
				}

				contractID := canID
				if contractID == "" {
					contractID = noticeID
				}
				if contractID == "" {
					contractID = title
				}
				if contractID == "" {
					return
				}
				if _, ok := seen[contractID]; ok {
					return
				}
				seen[contractID] = struct{}{}

				summary := MatchSummary{
					Source:      nswSourceID,
					ContractID:  contractID,
					ReleaseID:   noticeID,
					OCID:        contractID,
					Supplier:    supplier,
					Agency:      agency,
					Title:       title,
					Amount:      amount,
					ReleaseDate: publishDate,
				}

				if req.OnAnyMatch != nil {
					req.OnAnyMatch(summary)
				}
				if !matchesSummaryFilters(req, summary, periodEnd) {
					return
				}
				if !req.StartDate.IsZero() && !periodStart.IsZero() && periodStart.Before(req.StartDate) {
					// keep conservative
				}
				if req.OnMatch != nil {
					req.OnMatch(summary)
				}
				total = total.Add(summary.Amount)
			})

			nextHref := strings.TrimSpace(doc.Find(".nsw-pagination__item--next-page a.nsw-direction-link.choose-page").First().AttrOr("href", ""))
			if nextHref == "" {
				break
			}
			if strings.HasPrefix(strings.ToLower(nextHref), "javascript:") {
				break
			}
			baseURL, err := url.Parse(currentURL)
			if err != nil {
				break
			}
			refURL, err := url.Parse(nextHref)
			if err != nil {
				break
			}
			currentURL = baseURL.ResolveReference(refURL).String()
		}

		completed++
		if req.OnProgress != nil {
			req.OnProgress(completed, len(windows))
		}
	}

	return formatMoneyDecimal(total), nil
}

func waitForNswCards(ctx context.Context, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var count int
		_ = chromedp.Run(ctx,
			chromedp.Evaluate(`document.querySelectorAll('ul.cards.profiles > li').length`, &count),
		)
		if count > 0 {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}

var nswUUIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func buildNswSearchURL(req SearchRequest, pageNum int, dateFrom, dateTo time.Time) string {
	params := url.Values{}
	params.Set("mode", "advanced")

	query := strings.TrimSpace(req.Keyword)
	if query == "" {
		query = strings.TrimSpace(req.Company)
	}
	if query != "" {
		params.Set("query", query)
	}

	if agencyID := strings.TrimSpace(req.Agency); nswUUIDPattern.MatchString(agencyID) {
		params.Set("agencies", agencyID)
	}

	if !dateFrom.IsZero() {
		params.Set("dateFrom", dateFrom.UTC().Format("2006-01-02"))
	}
	if !dateTo.IsZero() {
		params.Set("dateTo", dateTo.UTC().Format("2006-01-02"))
	}

	// Default to contract awards (CAN) to match the provided URL.
	params.Set("noticeTypes", "can")
	params.Set("categories", "")
	params.Set("sort", "")

	if pageNum > 0 {
		params.Set("page", strconv.Itoa(pageNum))
	}

	return fmt.Sprintf("%s?%s", nswSearchURL, params.Encode())
}

func extractNswNoticeID(noticeURL string) string {
	u, err := url.Parse(strings.TrimSpace(noticeURL))
	if err != nil {
		return ""
	}
	base := path.Base(u.Path)
	if base == "" || base == "/" || strings.EqualFold(base, "notices") {
		return ""
	}
	return base
}

func extractNswDetails(root *goquery.Selection) map[string]string {
	out := make(map[string]string)
	dl := root.Find("dl.details").First()
	if dl.Length() == 0 {
		return out
	}

	// The page uses dt/dd pairs.
	var lastKey string
	dl.Children().Each(func(_ int, s *goquery.Selection) {
		switch strings.ToLower(goquery.NodeName(s)) {
		case "dt":
			lastKey = strings.ToLower(strings.TrimSpace(s.Text()))
		case "dd":
			if lastKey == "" {
				return
			}
			val := strings.TrimSpace(strings.Join(strings.Fields(s.Text()), " "))
			out[lastKey] = val
			lastKey = ""
		}
	})
	return out
}

func parseNswDate(raw string) time.Time {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		return time.Time{}
	}
	layouts := []string{"2-Jan-2006", "02-Jan-2006", "2-Jan-06", "02-Jan-06"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, cleaned); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func parseNswContractPeriod(raw string) (time.Time, time.Time) {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		return time.Time{}, time.Time{}
	}
	parts := strings.Split(cleaned, " to ")
	if len(parts) != 2 {
		return time.Time{}, time.Time{}
	}
	start := parseNswDate(parts[0])
	end := parseNswDate(parts[1])
	return start, end
}
