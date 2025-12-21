package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/shopspring/decimal"
)

const saSourceID = "sa"
const saSearchURL = "https://www.tenders.sa.gov.au/contract/search"

var errSaBlocked = errors.New("sa scrape blocked")

// Chrome-like UA to reduce blocks.
const saUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// saSource scrapes South Australia contract awards via tenders.sa.gov.au search.
type saSource struct{}

func newSaSource() Source {
	return saSource{}
}

func (s saSource) ID() string { return saSourceID }

func (s saSource) Run(ctx context.Context, req SearchRequest) (string, error) {
	lookbackPeriod := resolveLookbackPeriod(req.LookbackPeriod)
	startResolved, endResolved := resolveDates(req.StartDate, req.EndDate, lookbackPeriod)

	windows := []dateWindow{{start: startResolved, end: endResolved}}
	if req.ShouldFetchWindow != nil {
		windows = splitDateWindows(startResolved, endResolved, maxWindowDays)
	}

	return runSaWithBrowser(ctx, req, windows)
}

func runSaWithBrowser(ctx context.Context, req SearchRequest, windows []dateWindow) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	allocCtx, cancel := chromedp.NewExecAllocator(ctx,
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.UserAgent(saUserAgent),
	)
	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	defer cancelCtx()
	defer cancel()

	// Best-effort: reduce headless detection used by bot protections.
	_ = chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		params := page.AddScriptToEvaluateOnNewDocument(`
Object.defineProperty(navigator, 'webdriver', { get: () => undefined });
window.chrome = window.chrome || { runtime: {} };
`)
		_, err := params.Do(ctx)
		return err
	}))

	total := decimal.Zero
	seen := make(map[string]struct{})
	var mu sync.Mutex

	completed := 0
	for _, win := range windows {
		if req.ShouldFetchWindow != nil && !req.ShouldFetchWindow(win) {
			completed++
			if req.OnProgress != nil {
				req.OnProgress(completed, len(windows))
			}
			continue
		}

		newCount := 0
		for pageNum := 1; pageNum <= 250; pageNum++ {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}

			target := buildSaSearchURL(req, pageNum, win.start, win.end)
			var pageHTML string
			if err := chromedp.Run(ctx,
				chromedp.Navigate(target),
				chromedp.WaitReady("body", chromedp.ByQuery),
				chromedp.Sleep(1200*time.Millisecond),
				chromedp.OuterHTML("html", &pageHTML, chromedp.ByQuery),
			); err != nil {
				return "", err
			}

			// Cloudflare may present a JS challenge.
			if isSaCloudflareBlocked(pageHTML) {
				// Give it a moment to complete, then re-read once.
				if err := chromedp.Run(ctx,
					chromedp.Sleep(4*time.Second),
					chromedp.OuterHTML("html", &pageHTML, chromedp.ByQuery),
				); err != nil {
					return "", err
				}
				if isSaCloudflareBlocked(pageHTML) {
					return "", errSaBlocked
				}
			}

			doc, err := goquery.NewDocumentFromReader(strings.NewReader(pageHTML))
			if err != nil {
				return "", err
			}

			if strings.EqualFold(strings.TrimSpace(os.Getenv("SA_DEBUG_HTML")), "true") {
				_ = os.WriteFile(fmt.Sprintf("/tmp/sa_page_%d.html", pageNum), []byte(pageHTML), 0o600)
			}

			table, colIdx := findSaResultsTable(doc)
			if table == nil {
				// No results or layout changed.
				break
			}

			rows := table.Find("tbody tr")
			if rows.Length() == 0 {
				rows = table.Find("tr") // Fallback for headerless tables
			}
			if rows.Length() == 0 {
				break
			}

			pageMatches := 0
			rows.Each(func(i int, tr *goquery.Selection) {
				cells := tr.Find("td")
				if cells.Length() == 0 {
					return
				}

				get := func(i int) string {
					if i < 0 || i >= cells.Length() {
						return ""
					}
					cell := cells.Eq(i).Clone()
					cell.Find(".tablesaw-cell-label").Remove()
					return strings.TrimSpace(strings.Join(strings.Fields(cell.Text()), " "))
				}

				contractID := get(firstIndex(colIdx, "reference", "code", "contract", "id"))
				title := get(firstIndex(colIdx, "description", "title"))
				buyer := get(firstIndex(colIdx, "buyer", "agency"))
				supplier := get(firstIndex(colIdx, "supplier", "contractor"))
				startDate := parseSaDate(get(firstIndex(colIdx, "start date", "start")))
				awardDate := parseSaDate(get(firstIndex(colIdx, "awarded date", "awarded")))

				amount := decimal.Zero
				if val := get(firstIndex(colIdx, "value", "amount", "cost", "total cost")); val != "" {
					if parsed, err := parseMoneyToDecimal(val); err == nil {
						amount = parsed
					}
				}

				if contractID == "" {
					contractID = title
				}
				if contractID == "" {
					return
				}

				// Heuristic: if supplier/agency are missing from the table but we searched for them,
				// populate them so they pass filters and provide some context.
				if supplier == "" && req.Keyword != "" {
					supplier = req.Keyword
				}
				if supplier == "" && req.Company != "" {
					supplier = req.Company
				}
				if buyer == "" && req.Agency != "" {
					buyer = req.Agency
				}

				mu.Lock()
				if _, ok := seen[contractID]; ok {
					mu.Unlock()
					return
				}
				seen[contractID] = struct{}{}
				mu.Unlock()

				releaseDate := awardDate
				if releaseDate.IsZero() {
					releaseDate = startDate
				}

				summary := MatchSummary{
					Source:      saSourceID,
					ContractID:  contractID,
					ReleaseID:   contractID,
					OCID:        contractID,
					Supplier:    supplier,
					Agency:      buyer,
					Title:       title,
					Amount:      amount,
					ReleaseDate: releaseDate,
				}

				if req.OnAnyMatch != nil {
					req.OnAnyMatch(summary)
				}
				if !matchesSummaryFilters(req, summary, time.Time{}) {
					return
				}
				if req.OnMatch != nil {
					req.OnMatch(summary)
				}

				mu.Lock()
				total = total.Add(summary.Amount)
				mu.Unlock()
				pageMatches++
			})

			newCount += pageMatches
			// Check if there is a next page link in the paging div
			hasNext := false
			doc.Find(".paging a").Each(func(_ int, s *goquery.Selection) {
				if strings.Contains(strings.ToLower(s.AttrOr("title", "")), "go to page") {
					// If the page number in the link is greater than current pageNum, we have a next page
					href := s.AttrOr("href", "")
					if strings.Contains(href, fmt.Sprintf("page.value=%d", pageNum+1)) {
						hasNext = true
					}
				}
			})

			if !hasNext {
				break
			}
		}

		_ = newCount
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

func buildSaSearchURL(req SearchRequest, pageNum int, startDateFrom, startDateTo time.Time) string {
	params := url.Values{}

	keywords := strings.TrimSpace(req.Keyword)
	company := strings.TrimSpace(req.Company)
	if company != "" {
		if keywords == "" {
			keywords = company
		} else if !strings.Contains(strings.ToLower(keywords), strings.ToLower(company)) {
			keywords = keywords + " " + company
		}
	}
	params.Set("keywords", keywords)

	params.Set("code", "")

	buyerID := ""
	agency := strings.TrimSpace(req.Agency)
	if saBuyerIDPattern.MatchString(agency) {
		buyerID = agency
	} else if agency != "" {
		// If agency is a name, add it to keywords if not already there
		if keywords == "" {
			params.Set("keywords", agency)
		} else if !strings.Contains(strings.ToLower(keywords), strings.ToLower(agency)) {
			params.Set("keywords", keywords+" "+agency)
		}
	}
	params.Set("buyerId", buyerID)

	params.Set("minCost", "")
	if !startDateFrom.IsZero() {
		params.Set("startDateFrom", startDateFrom.UTC().Format("02/01/2006"))
	} else {
		params.Set("startDateFrom", "")
	}
	if !startDateTo.IsZero() {
		params.Set("startDateTo", startDateTo.UTC().Format("02/01/2006"))
	} else {
		params.Set("startDateTo", "")
	}
	params.Set("awardedDateFrom", "")

	if pageNum <= 0 {
		pageNum = 1
	}
	params.Set("page", strconv.Itoa(pageNum))

	params.Set("preset", "")
	params.Set("browse", "false")
	params.Set("desc", "true")
	params.Set("orderBy", "startDate")

	return fmt.Sprintf("%s?%s", saSearchURL, params.Encode())
}

var saBuyerIDPattern = regexp.MustCompile(`^[0-9]+$`)

func isSaCloudflareBlocked(html string) bool {
	s := strings.ToLower(html)
	if strings.Contains(s, "attention required") && strings.Contains(s, "cloudflare") {
		return true
	}
	if strings.Contains(s, "cf-browser-verification") || strings.Contains(s, "__cf_chl") {
		return true
	}
	return false
}

func parseSaDate(raw string) time.Time {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		return time.Time{}
	}

	// SA uses "Sept" which Go's "Jan" doesn't like (it wants "Sep").
	// Also handle multiple spaces.
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	normalized := strings.ReplaceAll(cleaned, "Sept", "Sep")

	layouts := []string{
		"02/01/2006",
		"2/1/2006",
		"2006-01-02",
		"2 Jan 2006",
		"02 Jan 2006",
		"2 January 2006",
		"02 January 2006",
		"2-Jan-2006",
		"02-Jan-2006",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, normalized); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func findSaResultsTable(doc *goquery.Document) (*goquery.Selection, map[string]int) {
	if doc == nil {
		return nil, nil
	}

	type candidate struct {
		score int
		t     *goquery.Selection
		idx   map[string]int
	}

	best := candidate{score: -1}
	known := []string{"contract", "code", "reference", "buyer", "agency", "supplier", "contractor", "start", "start date", "awarded", "awarded date", "value", "amount", "cost", "total cost", "description", "title"}

	doc.Find("table").Each(func(_ int, t *goquery.Selection) {
		headers := []string{}
		row := t.Find("thead tr").First()
		if row.Length() == 0 {
			row = t.Find("tr").First()
		}
		row.Find("th").Each(func(i int, th *goquery.Selection) {
			text := strings.ToLower(strings.TrimSpace(strings.Join(strings.Fields(th.Text()), " ")))
			headers = append(headers, text)
		})
		if len(headers) == 0 {
			return
		}

		idx := map[string]int{}
		score := 0
		for i, h := range headers {
			for _, k := range known {
				if strings.Contains(h, k) {
					idx[k] = i
					score++
				}
			}
		}

		rows := t.Find("tbody tr")
		if rows.Length() == 0 {
			rows = t.Find("tr")
		}
		if rows.Length() < 2 {
			return
		}

		if score > best.score {
			best = candidate{score: score, t: t, idx: idx}
		}
	})

	if best.t == nil {
		return nil, nil
	}
	return best.t, best.idx
}

func firstIndex(idx map[string]int, keys ...string) int {
	if idx == nil {
		return -1
	}
	for _, k := range keys {
		if i, ok := idx[k]; ok {
			return i
		}
	}
	return -1
}
