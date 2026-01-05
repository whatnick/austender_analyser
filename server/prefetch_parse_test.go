package main

import "testing"

func TestParseSpendQuery_SpentOnCompany(t *testing.T) {
	company, agency := parseSpendQuery("How much was spent on KPMG ?")
	if company == "" {
		t.Fatalf("expected company, got empty")
	}
	if agency != "" {
		t.Fatalf("expected empty agency, got %q", agency)
	}
}

func TestParseSpendQuery_AgencySpendOnCompany(t *testing.T) {
	company, agency := parseSpendQuery("How much did Department of Defence spend on KPMG?")
	if company == "" || agency == "" {
		t.Fatalf("expected company and agency, got company=%q agency=%q", company, agency)
	}
}
