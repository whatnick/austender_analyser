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

		val, err := parseWaMoney(valueStr)
		if err == nil {
			total = total.Add(val)
		}

		if req.OnMatch != nil {
			awardDate, _ := time.Parse("2006-01-02", awardDateStr)
			req.OnMatch(MatchSummary{
				ContractID:  ref,
				Source:      waSourceID,
				Supplier:    currentSupplier,
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
	baseParams.Set("awardDateFromString", startResolved.Format("02/01/2006"))
	baseParams.Set("awardDateToString", endResolved.Format("02/01/2006"))
	baseParams.Set("noreset", "yes")
	baseParams.Set("maxResults", "1000")

	if req.Agency != "" {
		baseParams.Set("publicAuthority", req.Agency)
	}

	// If we used Keyword for supplier search, don't use it as a keyword filter
	// unless it was explicitly provided as a keyword and we have a company.
	if req.Keyword != "" && (req.Company != "" || req.Agency != "") {
		baseParams.Set("keywords", req.Keyword)
	}

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

			params := url.Values{}
			for k, v := range baseParams {
				params[k] = v
			}
			params.Set("bySupplierId", fmt.Sprintf("%d", s.ID))

			searchURL := fmt.Sprintf("%s?%s", waContractSearchURL, params.Encode())
			err := c.Visit(searchURL)
			if err != nil {
				continue
			}
		}
	} else if req.Agency != "" || req.Keyword != "" {
		// Search by agency or keyword only
		currentSupplier = "Various"
		searchURL := fmt.Sprintf("%s?%s", waContractSearchURL, baseParams.Encode())
		err := c.Visit(searchURL)
		if err != nil {
			return "", err
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
