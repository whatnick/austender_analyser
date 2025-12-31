package cmd

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type EntityCandidate struct {
	Source string `json:"source,omitempty"`
	Name   string `json:"name"`
	Key    string `json:"key,omitempty"`
	Rows   int64  `json:"rows,omitempty"`
}

type EntityLookupOptions struct {
	Source string
	Query  string
	Limit  int
}

type EntityLookupResult struct {
	CatalogAvailable bool              `json:"catalogAvailable"`
	Evidence         string            `json:"evidence,omitempty"`
	Candidates       []EntityCandidate `json:"candidates"`
}

func FindAgenciesFromCatalog(ctx context.Context, opts EntityLookupOptions) (EntityLookupResult, error) {
	return findEntitiesFromCatalog(ctx, "agency", opts)
}

func FindCompaniesFromCatalog(ctx context.Context, opts EntityLookupOptions) (EntityLookupResult, error) {
	return findEntitiesFromCatalog(ctx, "company", opts)
}

func findEntitiesFromCatalog(ctx context.Context, kind string, opts EntityLookupOptions) (EntityLookupResult, error) {
	cacheDir := defaultCacheDir()
	dbPath := filepath.Join(cacheDir, "catalog.sqlite")
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return EntityLookupResult{CatalogAvailable: false, Evidence: fmt.Sprintf("catalog.sqlite not found in %s", cacheDir), Candidates: []EntityCandidate{}}, nil
		}
		return EntityLookupResult{}, err
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return EntityLookupResult{}, err
	}
	defer db.Close()

	res, err := queryEntities(ctx, db, kind, opts.Source, opts.Query, limit, true)
	if err == nil {
		res.CatalogAvailable = true
		return res, nil
	}

	// Legacy fallback: older catalogs may not have the *_name columns.
	fallback, fbErr := queryEntities(ctx, db, kind, opts.Source, opts.Query, limit, false)
	if fbErr == nil {
		fallback.CatalogAvailable = true
		fallback.Evidence = strings.TrimSpace(strings.Join([]string{fallback.Evidence, "legacy schema fallback"}, "; "))
		return fallback, nil
	}
	return EntityLookupResult{}, err
}

func queryEntities(ctx context.Context, db *sql.DB, kind, source, q string, limit int, withNames bool) (EntityLookupResult, error) {
	var keyCol, nameCol string
	switch kind {
	case "agency":
		keyCol = "agency_key"
		nameCol = "agency_name"
	case "company":
		keyCol = "company_key"
		nameCol = "company_name"
	default:
		return EntityLookupResult{}, fmt.Errorf("unknown entity kind: %s", kind)
	}

	var nameExpr string
	if withNames {
		nameExpr = fmt.Sprintf("COALESCE(NULLIF(%s, ''), %s)", nameCol, keyCol)
	} else {
		nameExpr = keyCol
	}

	var args []any
	var clauses []string

	// Optional source filtering.
	if strings.TrimSpace(source) != "" {
		sourceKey := sanitizePartitionComponent(normalizeSourceID(source))
		clauses = append(clauses, "source = ?")
		args = append(args, sourceKey)
	}

	// Optional query filtering.
	query := strings.ToLower(strings.TrimSpace(q))
	if query != "" {
		namePattern := "%" + query + "%"
		keyPattern := "%" + sanitizePartitionComponent(query) + "%"
		clauses = append(clauses, fmt.Sprintf("(LOWER(%s) LIKE ? OR %s LIKE ?)", nameExpr, keyCol))
		args = append(args, namePattern, keyPattern)
	}

	where := ""
	if len(clauses) > 0 {
		where = "WHERE " + strings.Join(clauses, " AND ")
	}

	selectCols := fmt.Sprintf("source, %s as name, %s as entity_key, SUM(row_count) as rows", nameExpr, keyCol)
	groupBy := "GROUP BY source, name, entity_key"
	orderBy := "ORDER BY rows DESC"
	querySQL := fmt.Sprintf("SELECT %s FROM parquet_files %s %s %s LIMIT ?", selectCols, where, groupBy, orderBy)
	args = append(args, limit)

	rows, err := db.QueryContext(ctx, querySQL, args...)
	if err != nil {
		return EntityLookupResult{}, err
	}
	defer rows.Close()

	out := EntityLookupResult{Candidates: []EntityCandidate{}}
	if query != "" {
		out.Evidence = fmt.Sprintf("substring match: %q", query)
	} else {
		out.Evidence = "top entities from catalog"
	}

	for rows.Next() {
		var src, name, key string
		var n int64
		if scanErr := rows.Scan(&src, &name, &key, &n); scanErr != nil {
			return EntityLookupResult{}, scanErr
		}
		name = strings.TrimSpace(name)
		if name == "" {
			name = key
		}
		out.Candidates = append(out.Candidates, EntityCandidate{Source: src, Name: name, Key: key, Rows: n})
	}

	return out, nil
}
