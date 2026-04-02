package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeEntityLookupIndex(t *testing.T, dir string, files map[string]columnarFileMeta) {
	t.Helper()
	state := columnarIndexState{Version: columnarIndexVersion, Files: files}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("marshal index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, columnarIndexFileName), data, 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
}

func TestFindAgenciesFromCatalog_NewSchema(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AUSTENDER_CACHE_DIR", dir)

	writeEntityLookupIndex(t, dir, map[string]columnarFileMeta{
		"p1": {Path: "p1", Source: "federal", FinancialYear: "2024-25", AgencyKey: "department_of_defence", AgencyName: "Department of Defence", CompanyKey: "acme", CompanyName: "Acme Pty Ltd", RowCount: 100},
		"p2": {Path: "p2", Source: "federal", FinancialYear: "2024-25", AgencyKey: "ato", AgencyName: "Australian Taxation Office", CompanyKey: "acme", CompanyName: "Acme Pty Ltd", RowCount: 10},
	})

	res, err := FindAgenciesFromCatalog(context.Background(), EntityLookupOptions{Source: "federal", Query: "defence", Limit: 10})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !res.CatalogAvailable {
		t.Fatalf("expected catalog available")
	}
	if len(res.Candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(res.Candidates))
	}
	if res.Candidates[0].Name != "Department of Defence" {
		t.Fatalf("unexpected candidate: %+v", res.Candidates[0])
	}
}

func TestFindCompaniesFromCatalog_LegacySchemaFallback(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AUSTENDER_CACHE_DIR", dir)

	writeEntityLookupIndex(t, dir, map[string]columnarFileMeta{
		"p1": {Path: "p1", Source: "vic", FinancialYear: "2024-25", AgencyKey: "dept_health", CompanyKey: "kpmg", RowCount: 50},
		"p2": {Path: "p2", Source: "vic", FinancialYear: "2024-25", AgencyKey: "dept_health", CompanyKey: "acn_123", RowCount: 10},
	})

	res, err := FindCompaniesFromCatalog(context.Background(), EntityLookupOptions{Source: "vic", Query: "kpmg", Limit: 10})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !res.CatalogAvailable {
		t.Fatalf("expected catalog available")
	}
	if len(res.Candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(res.Candidates))
	}
	if res.Candidates[0].Name != "kpmg" {
		t.Fatalf("unexpected candidate: %+v", res.Candidates[0])
	}
}
