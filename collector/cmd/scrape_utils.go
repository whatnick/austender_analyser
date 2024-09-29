package cmd

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/gocolly/colly"
	"github.com/leekchan/accounting"
	"github.com/shopspring/decimal"
)

// Fields
/*
	CN ID:CN3482539-A2
	Amends:CN3482539
	Agency:Australian National Audit Office
	Publish Date:6-Feb-2018
	Category:Audit services
	Contract Period:22-Jan-2018 to 31-Oct-2023
	Contract Value (AUD):$542,560.00
	ATM ID:2017/1102
	Supplier Name:KPMG Peat Marwick - ACT
*/
type contract struct {
	CN_ID           string
	Amends          string
	Agency          string
	Publish_Date    string
	Category        string
	Contract_Period string
	Contract_Value  decimal.Decimal
	ATM_ID          string
	SON_ID          string
	Supplier_Name   string
}

func cleanNum(s string) decimal.Decimal {
	r := regexp.MustCompile(`[^0-9-. ]`) // Remove anything thats not a number,space or decimal
	num := r.ReplaceAllString(s, "${1}")
	num = strings.Trim(num, " ")
	v, _ := decimal.NewFromString(num)
	return v
}

func scrapeAncap(keywordVal, companyName, agencyVal string) {
	collector := colly.NewCollector(colly.Async(true))
	contracts := []*contract{}
	ac := accounting.Accounting{Symbol: "$", Precision: 2}
	contractSum := decimal.New(0, 0)
	params := url.Values{}
	params.Add("SearchFrom", "CnSearch")
	params.Add("Type", "Cn")
	params.Add("AgencyStatus", "-1")
	params.Add("KeywordTypeSearch", "AllWord")
	params.Add("DateType", "Publish Date")
	params.Add("Keyword", keywordVal)
	params.Add("SupplierName", companyName)
	requestURL := "https://www.tenders.gov.au/Search/CnAdvancedSearch?" + params.Encode()

	collector.OnHTML("a[href]", func(e *colly.HTMLElement) {
		url := e.Attr("href")
		if strings.Contains(url, "SupplierName="+companyName) {
			//fmt.Printf(url)
			// Visit all search bread crumbs
			e.Request.Visit(url)
		}
	})

	collector.OnHTML(".col-sm-8", func(e *colly.HTMLElement) {
		c := &contract{}
		e.ForEach(".list-desc", func(_ int, el *colly.HTMLElement) {
			switch el.ChildText("span") {
			case "CN ID:":
				c.CN_ID = el.ChildText(".list-desc-inner")
			case "Agency:":
				c.Agency = el.ChildText(".list-desc-inner")
			case "Publish Date:":
				c.Publish_Date = el.ChildText(".list-desc-inner")
			case "Category:":
				c.Category = el.ChildText(".list-desc-inner")
			case "Contract Period:":
				c.Contract_Period = el.ChildText(".list-desc-inner")
			case "Contract Value (AUD):":
				c_value_str := el.ChildText(".list-desc-inner")
				c.Contract_Value = cleanNum(c_value_str)
			case "ATM ID:":
				c.ATM_ID = el.ChildText(".list-desc-inner")
			case "SON ID":
				c.SON_ID = el.ChildText(".list-desc-inner")
			case "Supplier Name:":
				c.Supplier_Name = el.ChildText(".list-desc-inner")
			}
		})
		if c.Contract_Value.GreaterThan(decimal.New(0, 0)) {
			if strings.Contains(c.Agency, agencyVal) {
				fmt.Println(c)
				contracts = append(contracts, c)
			}
		}
	})

	collector.Visit(requestURL)
	collector.Wait()
	for _, c := range contracts {
		contractSum = contractSum.Add(c.Contract_Value)
	}
	sumValue := ac.FormatMoney(contractSum)
	fmt.Println("Total Contract:" + sumValue)
}
