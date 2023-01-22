package main

import (
	"container/list"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
)

func main() {
	client := &http.Client{}
	companyName := "KPMG"
	params := url.Values{}
	params.Add("SearchFrom", "CnSearch")
	params.Add("Type", "Cn")
	params.Add("AgencyStatus", "-1")
	params.Add("KeywordTypeSearch", "AllWord")
	params.Add("DateType", "Publish Date")
	params.Add("SupplierName", companyName)
	requestURL := "https://www.tenders.gov.au/Search/CnAdvancedSearch?" + params.Encode()
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
	fmt.Printf("%s\n", bodyText)
}

// TODO: Create contract class and return a list
// of contracts/tenders per page
func summarizePage(s string) *list.List {
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
	l := list.New()
	return l
}

// TODO: Parse first page and return page counts to parse
func countResultPages(s string) int {
	return 1
}

// TODO: Summarize spends from list of contracts over time
// to 3 buckets last year, last 5 years, all time
func summarizeSpend(map[string]string) *list.List {
	l := list.New()
	return l
}
