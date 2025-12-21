package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/gocolly/colly/v2"
	"github.com/shopspring/decimal"
)

const waSourceID = "wa"
const waSupplierSearchURL = "https://www.tenders.wa.gov.au/watenders/rest/business/searchBySupplier"
const waContractSearchURL = "https://www.tenders.wa.gov.au/watenders/contract/list.action"

type waSource struct{}

func newWaSource() Source {
	return waSource{}
}

func (w waSource) ID() string { return waSourceID }

type waSupplier struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func (w waSource) Run(ctx context.Context, req SearchRequest) (string, error) {
	// Determine supplier search term
	supplierSearchTerm := req.Company
	if supplierSearchTerm == "" && req.Keyword != "" {
		// If no company is specified, use keyword as a fallback for supplier search
		// but only if we don't have an agency. If we have an agency and a keyword,
		// we might just want to search that agency by keyword.
		if req.Agency == "" {
			supplierSearchTerm = req.Keyword
		}
	}

	var suppliers []waSupplier
	if supplierSearchTerm != "" {
		var err error
		suppliers, err = w.findSuppliers(supplierSearchTerm)
		if err != nil {
			return "", fmt.Errorf("failed to find suppliers: %w", err)
		}
	}

	lookbackPeriod := resolveLookbackPeriod(req.LookbackPeriod)
	startResolved, endResolved := resolveDates(req.StartDate, req.EndDate, lookbackPeriod)

	total := decimal.Zero
	seen := make(map[string]struct{})
	var currentSupplier string

	c := colly.NewCollector(
		colly.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
	)

	c.OnHTML("#contractTable tbody tr", func(e *colly.HTMLElement) {
		ref := strings.TrimSpace(e.ChildText("td:nth-child(2)"))
		if ref == "" {
			return
		}

		if _, ok := seen[ref]; ok {
			return
		}

		title := strings.TrimSpace(e.ChildText("td:nth-child(3)"))
		agency := strings.TrimSpace(e.ChildText("td:nth-child(4)"))

		// Filter by agency if requested
		if req.Agency != "" && !strings.Contains(strings.ToLower(agency), strings.ToLower(req.Agency)) {
			return
		}

		seen[ref] = struct{}{}

		awardDateStr := strings.TrimSpace(e.ChildText("td:nth-child(5)"))
		valueStr := strings.TrimSpace(e.ChildText("td:nth-child(7)"))

		supplier := currentSupplier
		// Always try to get the exact supplier name from the detail page.
		// This is necessary because:
		// 1. The search results table doesn't show the supplier.
		// 2. The WA site sometimes ignores the supplier filter when combined with agency.
		detailURL := e.ChildAttr("td:nth-child(2) a", "href")
		if detailURL != "" {
			if !strings.HasPrefix(detailURL, "http") {
				detailURL = "https://www.tenders.wa.gov.au" + detailURL
			}
			fetched, err := w.fetchSupplier(detailURL)
			if err == nil && fetched != "" {
				supplier = fetched
			}
		}

		// If we are searching for a specific company, ensure the result matches.
		if req.Company != "" {
			if !strings.Contains(strings.ToLower(supplier), strings.ToLower(req.Company)) {
				return
			}
		}

		val, err := parseWaMoney(valueStr)
		if err == nil {
			total = total.Add(val)
		}

		if req.OnMatch != nil {
			// Try multiple date formats
			var awardDate time.Time
			for _, fmtStr := range []string{"2006-01-02", "02/01/2006"} {
				if t, err := time.Parse(fmtStr, awardDateStr); err == nil {
					awardDate = t
					break
				}
			}

			req.OnMatch(MatchSummary{
				ContractID:  ref,
				Source:      waSourceID,
				Supplier:    supplier,
				Agency:      agency,
				Title:       title,
				Amount:      val,
				ReleaseDate: awardDate,
			})
		}
	})

	// Build base query parameters
	baseParams := url.Values{}
	baseParams.Set("action", "contract-search-submit")
	baseParams.Set("noreset", "yes")
	baseParams.Set("maxResults", "1000")

	if req.Agency != "" {
		baseParams.Set("publicAuthority", req.Agency)
	}

	// Use keyword if provided, otherwise use company name as a keyword to help filtering
	if req.Keyword != "" {
		baseParams.Set("keywords", req.Keyword)
	} else if req.Company != "" {
		baseParams.Set("keywords", req.Company)
	}

	windows := splitDateWindows(startResolved, endResolved, maxWindowDays)

	if len(suppliers) > 0 {
		for i, s := range suppliers {
			currentSupplier = s.Name
			if req.OnProgress != nil {
				req.OnProgress(i, len(suppliers))
			}

			// Filter suppliers by name if we searched by name
			if supplierSearchTerm != "" {
				isNumeric := regexp.MustCompile(`^[0-9\s]+$`).MatchString(supplierSearchTerm)
				if !isNumeric && !strings.Contains(strings.ToLower(s.Name), strings.ToLower(supplierSearchTerm)) {
					continue
				}
			}

			for _, win := range windows {
				if req.ShouldFetchWindow != nil && !req.ShouldFetchWindow(win) {
					continue
				}

				params := url.Values{}
				for k, v := range baseParams {
					params[k] = v
				}
				params.Set("bySupplierId", fmt.Sprintf("%d", s.ID))
				params.Set("awardDateFromString", win.start.Format("02/01/2006"))
				params.Set("awardDateToString", win.end.Format("02/01/2006"))

				searchURL := fmt.Sprintf("%s?%s", waContractSearchURL, params.Encode())
				err := c.Visit(searchURL)
				if err != nil {
					continue
				}
			}
		}
	} else if req.Agency != "" || req.Keyword != "" {
		// Search by agency or keyword only
		currentSupplier = "Various"
		for i, win := range windows {
			if req.ShouldFetchWindow != nil && !req.ShouldFetchWindow(win) {
				if req.OnProgress != nil {
					req.OnProgress(i+1, len(windows))
				}
				continue
			}

			params := url.Values{}
			for k, v := range baseParams {
				params[k] = v
			}
			params.Set("awardDateFromString", win.start.Format("02/01/2006"))
			params.Set("awardDateToString", win.end.Format("02/01/2006"))

			searchURL := fmt.Sprintf("%s?%s", waContractSearchURL, params.Encode())
			err := c.Visit(searchURL)
			if err != nil {
				continue
			}

			if req.OnProgress != nil {
				req.OnProgress(i+1, len(windows))
			}
		}
	}

	if req.OnProgress != nil {
		totalSuppliers := len(suppliers)
		if totalSuppliers == 0 {
			totalSuppliers = 1
		}
		req.OnProgress(totalSuppliers, totalSuppliers)
	}

	return formatMoneyDecimal(total), nil
}

func (w waSource) findSuppliers(keyword string) ([]waSupplier, error) {
	u, _ := url.Parse(waSupplierSearchURL)
	q := u.Query()

	// Check if keyword is ABN (11 digits) or ACN (9 digits)
	isNumeric := regexp.MustCompile(`^[0-9\s]+$`).MatchString(keyword)
	cleanNumeric := regexp.MustCompile(`[0-9]`).FindAllString(keyword, -1)
	numericStr := strings.Join(cleanNumeric, "")

	if isNumeric && len(numericStr) == 11 {
		q.Set("abn", numericStr)
		q.Set("name", "")
	} else if isNumeric && len(numericStr) == 9 {
		q.Set("acn", numericStr)
		q.Set("name", "")
	} else {
		q.Set("name", keyword)
		q.Set("abn", "")
	}

	q.Set("acn", "")
	q.Set("type", "contract")
	q.Set("maxResults", "250")
	q.Set("_", fmt.Sprintf("%d", time.Now().UnixMilli()))
	u.RawQuery = q.Encode()

	resp, err := http.Get(u.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var suppliers []waSupplier
	if err := json.NewDecoder(resp.Body).Decode(&suppliers); err != nil {
		return nil, err
	}

	return suppliers, nil
}

func parseWaMoney(s string) (decimal.Decimal, error) {
	// Remove $, commas, and whitespace
	s = strings.ReplaceAll(s, "$", "")
	s = strings.ReplaceAll(s, ",", "")
	s = strings.TrimSpace(s)
	if s == "" {
		return decimal.Zero, nil
	}
	return decimal.NewFromString(s)
}

func (w waSource) fetchSupplier(url string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "", err
	}

	var suppliers []string
	doc.Find("td").Each(func(_ int, s *goquery.Selection) {
		txt := strings.TrimSpace(s.Text())
		// Look for labels like "1)", "2)", etc.
		if regexp.MustCompile(`^\d+\)$`).MatchString(txt) {
			name := strings.TrimSpace(s.Next().Find("div").First().Text())
			if name != "" {
				suppliers = append(suppliers, name)
			}
		}
	})

	if len(suppliers) > 0 {
		return strings.Join(suppliers, ", "), nil
	}

	return "", nil
}
