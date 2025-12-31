package cmd

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestFindAgenciesFromCatalog_NewSchema(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AUSTENDER_CACHE_DIR", dir)

	db, err := sql.Open("sqlite", filepath.Join(dir, "catalog.sqlite"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE parquet_files (
			path TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			fy TEXT NOT NULL,
			agency_key TEXT NOT NULL,
			agency_name TEXT NOT NULL,
			company_key TEXT NOT NULL,
			company_name TEXT NOT NULL,
			row_count INTEGER NOT NULL,
			created_at TEXT NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	// Insert two agencies, one with more rows.
	_, err = db.Exec(`
		INSERT INTO parquet_files(path, source, fy, agency_key, agency_name, company_key, company_name, row_count, created_at)
		VALUES
		('p1', 'federal', '2024-25', 'department_of_defence', 'Department of Defence', 'acme', 'Acme Pty Ltd', 100, 'now'),
		('p2', 'federal', '2024-25', 'ato', 'Australian Taxation Office', 'acme', 'Acme Pty Ltd', 10, 'now');
	`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

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

	db, err := sql.Open("sqlite", filepath.Join(dir, "catalog.sqlite"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	// No *_name columns.
	_, err = db.Exec(`
		CREATE TABLE parquet_files (
			path TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			fy TEXT NOT NULL,
			agency_key TEXT NOT NULL,
			company_key TEXT NOT NULL,
			row_count INTEGER NOT NULL,
			created_at TEXT NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	_, err = db.Exec(`
		INSERT INTO parquet_files(path, source, fy, agency_key, company_key, row_count, created_at)
		VALUES
		('p1', 'vic', '2024-25', 'dept_health', 'kpmg', 50, 'now'),
		('p2', 'vic', '2024-25', 'dept_health', 'acn_123', 10, 'now');
	`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

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
