package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	indexPath := columnarIndexPath(cacheDir)
	if _, err := os.Stat(indexPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return EntityLookupResult{CatalogAvailable: false, Evidence: fmt.Sprintf("%s not found in %s", filepath.Base(indexPath), cacheDir), Candidates: []EntityCandidate{}}, nil
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

	index, err := newColumnarIndex(cacheDir)
	if err != nil {
		return EntityLookupResult{}, err
	}

	res := queryEntities(kind, index.allFiles(), opts.Source, opts.Query, limit)
	res.CatalogAvailable = true
	return res, nil
}

func queryEntities(kind string, files []columnarFileMeta, source, q string, limit int) EntityLookupResult {
	var keyCol, nameCol string
	switch kind {
	case "agency":
		keyCol = "agency"
		nameCol = "agencyName"
	case "company":
		keyCol = "company"
		nameCol = "companyName"
	default:
		return EntityLookupResult{Candidates: []EntityCandidate{}, Evidence: fmt.Sprintf("unknown entity kind: %s", kind)}
	}

	type aggregate struct {
		source string
		name   string
		key    string
		rows   int64
	}
	agg := make(map[string]*aggregate)

	query := strings.ToLower(strings.TrimSpace(q))
	sourceKey := ""
	if strings.TrimSpace(source) != "" {
		sourceKey = normalizeSourceID(source)
	}

	out := EntityLookupResult{Candidates: []EntityCandidate{}}
	if query != "" {
		out.Evidence = fmt.Sprintf("substring match: %q", query)
	} else {
		out.Evidence = "top entities from columnar index"
	}

	for _, meta := range files {
		if sourceKey != "" && normalizeSourceID(meta.Source) != sourceKey {
			continue
		}

		var key, name string
		switch keyCol {
		case "agency":
			key = meta.AgencyKey
			name = strings.TrimSpace(meta.AgencyName)
		case "company":
			key = meta.CompanyKey
			name = strings.TrimSpace(meta.CompanyName)
		}
		if name == "" {
			name = key
		}
		if query != "" {
			if !strings.Contains(strings.ToLower(name), query) && !strings.Contains(strings.ToLower(key), sanitizePartitionComponent(query)) {
				continue
			}
		}
		aggKey := normalizeSourceID(meta.Source) + "|" + key + "|" + name + "|" + nameCol
		item := agg[aggKey]
		if item == nil {
			item = &aggregate{source: normalizeSourceID(meta.Source), name: name, key: key}
			agg[aggKey] = item
		}
		item.rows += meta.RowCount
	}

	ranked := make([]aggregate, 0, len(agg))
	for _, item := range agg {
		ranked = append(ranked, *item)
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].rows == ranked[j].rows {
			if ranked[i].name == ranked[j].name {
				return ranked[i].source < ranked[j].source
			}
			return ranked[i].name < ranked[j].name
		}
		return ranked[i].rows > ranked[j].rows
	})
	for _, item := range ranked {
		if len(out.Candidates) >= limit {
			break
		}
		out.Candidates = append(out.Candidates, EntityCandidate{Source: item.source, Name: item.name, Key: item.key, Rows: item.rows})
	}
	return out
}
