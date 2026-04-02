package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const columnarIndexFileName = "clickhouse-index.json"
const columnarIndexVersion = 1

type columnarFileMeta struct {
	Path          string    `json:"path"`
	Source        string    `json:"source"`
	FinancialYear string    `json:"financialYear"`
	Month         string    `json:"month"`
	AgencyKey     string    `json:"agencyKey"`
	AgencyName    string    `json:"agencyName,omitempty"`
	CompanyKey    string    `json:"companyKey"`
	CompanyName   string    `json:"companyName,omitempty"`
	RowCount      int64     `json:"rowCount"`
	CreatedAt     time.Time `json:"createdAt"`
}

type columnarIndexState struct {
	Version       int                         `json:"version"`
	UpdatedAt     time.Time                   `json:"updatedAt"`
	Files         map[string]columnarFileMeta `json:"files"`
	Checkpoints   map[string]string           `json:"checkpoints,omitempty"`
	CoveredMonths map[string]string           `json:"coveredMonths,omitempty"`
}

type columnarIndex struct {
	path  string
	mu    sync.RWMutex
	state columnarIndexState
}

func newColumnarIndex(baseDir string) (*columnarIndex, error) {
	idx := &columnarIndex{path: filepath.Join(baseDir, columnarIndexFileName)}
	if err := idx.load(); err != nil {
		return nil, err
	}
	return idx, nil
}

func (i *columnarIndex) load() error {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.ensureStateLocked()

	data, err := os.ReadFile(i.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	var state columnarIndexState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}

	i.state = state
	i.ensureStateLocked()
	return nil
}

func (i *columnarIndex) ensureStateLocked() {
	if i.state.Version == 0 {
		i.state.Version = columnarIndexVersion
	}
	if i.state.Files == nil {
		i.state.Files = make(map[string]columnarFileMeta)
	}
	if i.state.Checkpoints == nil {
		i.state.Checkpoints = make(map[string]string)
	}
	if i.state.CoveredMonths == nil {
		i.state.CoveredMonths = make(map[string]string)
	}
}

func (i *columnarIndex) exists() bool {
	_, err := os.Stat(i.path)
	return err == nil
}

func (i *columnarIndex) saveLocked() error {
	i.ensureStateLocked()
	i.state.Version = columnarIndexVersion
	i.state.UpdatedAt = time.Now().UTC()

	data, err := json.MarshalIndent(i.state, "", "  ")
	if err != nil {
		return err
	}

	tmpPath := i.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, i.path)
}

func (i *columnarIndex) replaceFiles(files []columnarFileMeta) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.state.Files = make(map[string]columnarFileMeta, len(files))
	i.state.CoveredMonths = make(map[string]string, len(files))
	for _, meta := range files {
		if meta.Path == "" {
			continue
		}
		meta.Source = normalizeSourceID(meta.Source)
		i.state.Files[meta.Path] = meta
		i.state.CoveredMonths[monthCoverageKey(meta.Source, meta.Month)] = meta.Path
	}
	return i.saveLocked()
}

func (i *columnarIndex) upsertFiles(files []columnarFileMeta) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.ensureStateLocked()
	for _, meta := range files {
		if meta.Path == "" {
			continue
		}
		meta.Source = normalizeSourceID(meta.Source)
		i.state.Files[meta.Path] = meta
		i.state.CoveredMonths[monthCoverageKey(meta.Source, meta.Month)] = meta.Path
	}
	return i.saveLocked()
}

func (i *columnarIndex) loadCheckpoint(key string) (time.Time, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()

	raw, ok := i.state.Checkpoints[key]
	if !ok || raw == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, raw)
}

func (i *columnarIndex) saveCheckpoint(key string, t time.Time) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.ensureStateLocked()
	i.state.Checkpoints[key] = t.UTC().Format(time.RFC3339)
	return i.saveLocked()
}

func (i *columnarIndex) hasMonthPartition(source string, ts time.Time) bool {
	i.mu.RLock()
	defer i.mu.RUnlock()

	_, ok := i.state.CoveredMonths[monthCoverageKey(source, monthLabel(ts))]
	return ok
}

func (i *columnarIndex) filesMatching(filters SearchRequest) []columnarFileMeta {
	i.mu.RLock()
	defer i.mu.RUnlock()

	var out []columnarFileMeta
	sourceKey := ""
	if strings.TrimSpace(filters.Source) != "" {
		sourceKey = normalizeSourceID(filters.Source)
	}
	agencyKey := sanitizePartitionComponent(filters.Agency)
	agencyName := strings.ToLower(strings.TrimSpace(filters.Agency))
	companyKey := sanitizePartitionComponent(filters.Company)
	companyName := strings.ToLower(strings.TrimSpace(filters.Company))
	minFy := ""
	if filters.LookbackPeriod > 0 {
		minFy = strings.TrimPrefix(financialYearLabel(time.Now().AddDate(-filters.LookbackPeriod, 0, 0)), "fy=")
	}

	for _, meta := range i.state.Files {
		if sourceKey != "" && normalizeSourceID(meta.Source) != sourceKey {
			continue
		}
		if minFy != "" && meta.FinancialYear < minFy {
			continue
		}
		if agencyName != "" && !strings.Contains(meta.AgencyKey, agencyKey) && !strings.Contains(strings.ToLower(meta.AgencyName), agencyName) {
			continue
		}
		if companyName != "" && !strings.Contains(meta.CompanyKey, companyKey) && !strings.Contains(strings.ToLower(meta.CompanyName), companyName) {
			continue
		}
		out = append(out, meta)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Path < out[j].Path
	})
	return out
}

func (i *columnarIndex) allFiles() []columnarFileMeta {
	return i.filesMatching(SearchRequest{})
}

func columnarIndexPath(baseDir string) string {
	return filepath.Join(baseDir, columnarIndexFileName)
}

func monthCoverageKey(source, month string) string {
	return normalizeSourceID(source) + "|" + strings.TrimSpace(month)
}
