package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
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
	Supplier_Name   string
}

func main() {
	company := flag.String("c", "", "Company to scan")
	department := flag.String("d", "", "Department to scan")
	keyword := flag.String("k", "", "Keywords to scan")

	flag.Parse()

	companyName := *company
	keywordVal := *keyword
	agencyVal := *department

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

	// FIXME: No more need for initial load with Colly
	//initialLoad(requestURL)

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
			case "Supplier Name:":
				c.Supplier_Name = el.ChildText(".list-desc-inner")
			}
		})
		if c.Contract_Value.GreaterThan(decimal.New(0, 0)) {
			fmt.Println(c)
			if strings.Contains(c.Agency, agencyVal) {
				fmt.Println(strings.Index(c.Agency, agencyVal))
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

func initialLoad(requestURL string) {
	client := &http.Client{}
	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.9")
	req.Header.Set("Accept-Language", "en-AU,en;q=0.9,en-GB;q=0.8,en-US;q=0.7,km;q=0.6")
	req.Header.Set("Cache-Control", "max-age=0")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Cookie", "UR_BCF=true; __RequestVerificationToken=y_2OBF6jPK2-BQgpTRUEv_E-Bj9b29Cya5FsOyDpJgQgKbuJdz4_S2NbWRjg_wCL8K_wCF2ClgfXe48o-dGo7RVeJhLo8f3UBwkRLv8bf4iV8KxD8-MKqFu2JNIxeXje5m2bf_IR6Ub365GKPZr3PA2; _ga=GA1.3.1997799152.1674359813; _gid=GA1.3.467396865.1674359813")
	req.Header.Set("DNT", "1")
	req.Header.Set("Referer", "https://www.tenders.gov.au/cn/search")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/108.0.0.0 Safari/537.36")
	req.Header.Set("sec-ch-ua", `"Not?A_Brand";v="8", "Chromium";v="108", "Google Chrome";v="108"`)
	req.Header.Set("sec-ch-ua-mobile", "?0")
	req.Header.Set("sec-ch-ua-platform", `"Windows"`)
	resp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	bodyText, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatal(err)
	}
	// TODO: Parse body for total result count using goquery on text directly
	fmt.Printf("%s\n", bodyText)
}

func cleanNum(s string) decimal.Decimal {
	r := regexp.MustCompile(`[^0-9-. ]`) // Remove anything thats not a number,space or decimal
	num := r.ReplaceAllString(s, "${1}")
	num = strings.Trim(num, " ")
	v, _ := decimal.NewFromString(num)
	return v
}

// TODO: Parse first page and return page counts to parse
func countResultPages(s string) int {
	return 1
}
