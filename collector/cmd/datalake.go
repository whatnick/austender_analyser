package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/snappy"
	"github.com/shopspring/decimal"
)

// dataLake tracks parquet files in a partitioned layout plus a lightweight
// ClickHouse-friendly JSON index for fast discovery.
// for fast discovery. Partitions are organized as source=<id>/fy=YYYY-YY/month=YYYY-MM/agency=<key>/company=<key>.
type dataLake struct {
	baseDir string
	index   *columnarIndex
}

func newDataLake(baseDir string, index *columnarIndex) *dataLake {
	return &dataLake{baseDir: baseDir, index: index}
}

func (l *dataLake) ensureSchema() error {
	if l == nil || l.index == nil {
		return fmt.Errorf("columnar index not initialized")
	}
	return nil
}

type lakeSink struct {
	mu            sync.Mutex
	w             *parquet.GenericWriter[parquetRow]
	file          *os.File
	lake          *dataLake
	sourceKey     string
	fy            string
	month         string
	agencyKey     string
	agencyID      string
	agencyName    string
	agencyTokens  []string
	companyKey    string
	companyID     string
	companyName   string
	companyTokens []string
	rows          int64
}

// lakeWriterPool lazily opens sinks per partition derived from match content.
type lakeWriterPool struct {
	lake  *dataLake
	mu    sync.Mutex
	sinks map[string]*lakeSink
}

func newLakeWriterPool(l *dataLake) *lakeWriterPool {
	return &lakeWriterPool{lake: l, sinks: make(map[string]*lakeSink)}
}

func (l *dataLake) newSink(source string, ts time.Time, agency, company string) (*lakeSink, error) {
	fy := strings.TrimPrefix(financialYearLabel(ts), "fy=")
	month := monthLabel(ts)
	sourceKey := sanitizePartitionComponent(normalizeSourceID(source))
	if sourceKey == "" {
		sourceKey = sanitizePartitionComponent(defaultSourceID)
	}
	ag := sanitizePartitionComponent(agency)
	if ag == "" {
		ag = "unknown_agency"
	}
	co := sanitizePartitionComponent(company)
	if co == "" {
		co = "unknown_company"
	}
	agencyEntity := resolveIndexedEntity("agency", agency, ag, "", nil)
	companyEntity := resolveIndexedEntity("company", company, co, "", nil)
	dir := filepath.Join(l.baseDir, "lake", fmt.Sprintf("source=%s", sourceKey), financialYearLabel(ts), month, fmt.Sprintf("agency=%s", ag), fmt.Sprintf("company=%s", co))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, fmt.Sprintf("part-%d.parquet", time.Now().UnixNano()))
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	w := parquet.NewGenericWriter[parquetRow](f, parquet.Compression(&snappy.Codec{}))
	return &lakeSink{
		w:             w,
		file:          f,
		lake:          l,
		sourceKey:     sourceKey,
		fy:            fy,
		month:         month,
		agencyKey:     ag,
		agencyID:      agencyEntity.identifier,
		agencyName:    strings.TrimSpace(agency),
		agencyTokens:  agencyEntity.tokens,
		companyKey:    co,
		companyID:     companyEntity.identifier,
		companyName:   strings.TrimSpace(company),
		companyTokens: companyEntity.tokens,
	}, nil
}

func (s *lakeSink) write(ms MatchSummary) {
	s.mu.Lock()
	defer s.mu.Unlock()

	row := parquetRow{
		Partition:     partitionKeyLake(ms.ReleaseDate, ms.Source, ms.Agency, ms.Supplier),
		Source:        normalizeSourceID(ms.Source),
		FinancialYear: strings.TrimPrefix(financialYearLabel(ms.ReleaseDate), "fy="),
		AgencyKey:     sanitizePartitionComponent(ms.Agency),
		CompanyKey:    sanitizePartitionComponent(ms.Supplier),
		ContractID:    ms.ContractID,
		ReleaseID:     ms.ReleaseID,
		OCID:          ms.OCID,
		Supplier:      ms.Supplier,
		Agency:        ms.Agency,
		Title:         ms.Title,
		Amount:        ms.Amount.InexactFloat64(),
		ReleaseEpoch:  ms.ReleaseDate.UnixMilli(),
		IsUpdate:      ms.IsUpdate,
	}
	_, _ = s.w.Write([]parquetRow{row})
	s.rows++
}

func (s *lakeSink) close() *columnarFileMeta {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.w != nil {
		_ = s.w.Close()
	}
	if s.file != nil {
		_ = s.file.Close()
	}
	if s.lake == nil || s.file == nil || s.rows == 0 {
		return nil
	}
	return &columnarFileMeta{
		Path:          s.file.Name(),
		Source:        s.sourceKey,
		FinancialYear: s.fy,
		Month:         s.month,
		AgencyKey:     s.agencyKey,
		AgencyName:    s.agencyName,
		AgencyID:      s.agencyID,
		AgencyTokens:  s.agencyTokens,
		CompanyKey:    s.companyKey,
		CompanyName:   s.companyName,
		CompanyID:     s.companyID,
		CompanyTokens: s.companyTokens,
		RowCount:      s.rows,
		CreatedAt:     time.Now().UTC(),
	}
}

// write routes a match summary to the correct partition sink based on its content.
func (p *lakeWriterPool) write(ms MatchSummary) error {
	if p == nil || p.lake == nil {
		return fmt.Errorf("lake writer pool not initialized")
	}
	partition := partitionKeyLake(ms.ReleaseDate, ms.Source, ms.Agency, ms.Supplier)
	p.mu.Lock()
	sink, ok := p.sinks[partition]
	if !ok {
		var err error
		sink, err = p.lake.newSink(ms.Source, ms.ReleaseDate, ms.Agency, ms.Supplier)
		if err != nil {
			p.mu.Unlock()
			return err
		}
		p.sinks[partition] = sink
	}
	p.mu.Unlock()
	sink.write(ms)
	return nil
}

func (p *lakeWriterPool) closeAll() {
	p.mu.Lock()
	sinks := make([]*lakeSink, 0, len(p.sinks))
	for _, s := range p.sinks {
		sinks = append(sinks, s)
	}
	p.mu.Unlock()

	metas := make([]columnarFileMeta, 0, len(sinks))
	for _, s := range sinks {
		if meta := s.close(); meta != nil {
			metas = append(metas, *meta)
		}
	}
	if len(metas) > 0 && p.lake != nil && p.lake.index != nil {
		_ = p.lake.index.upsertFiles(metas)
	}
}

// rebuildIndex scans the lake directory and rebuilds the columnar file index.
func (l *dataLake) rebuildIndex(ctx context.Context) error {
	if err := l.ensureSchema(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	root := filepath.Join(l.baseDir, "lake")
	var files []columnarFileMeta
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".parquet") {
			return nil
		}
		src, fy, ag, co := parseLakePartition(path)
		rowCount, countErr := countRows(path)
		if countErr != nil {
			return nil
		}
		files = append(files, columnarFileMeta{
			Path:          path,
			Source:        src,
			FinancialYear: fy,
			Month:         monthLabelFromPath(path),
			AgencyKey:     ag,
			AgencyName:    ag,
			AgencyID:      canonicalEntityIdentifier("agency", ag),
			AgencyTokens:  entityTokens("agency", ag),
			CompanyKey:    co,
			CompanyName:   co,
			CompanyID:     canonicalEntityIdentifier("company", co),
			CompanyTokens: entityTokens("company", co),
			RowCount:      rowCount,
			CreatedAt:     time.Now().UTC(),
		})
		return nil
	}); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return l.index.replaceFiles(nil)
		}
		return err
	}
	if len(files) == 0 {
		return l.index.replaceFiles(nil)
	}
	if err := l.index.replaceFiles(files); err != nil {
		return err
	}
	return nil
}

// queryTotals returns sum of matching rows using the lake index to pick files.
func (l *dataLake) queryTotals(ctx context.Context, filters SearchRequest) (decimalSum decimalSumResult, matched bool, err error) {
	total := decimalSumResult{}
	for _, meta := range l.index.filesMatching(filters) {
		inc, hit, scanErr := sumParquetFile(meta.Path, filters)
		if scanErr != nil {
			continue
		}
		if hit {
			matched = true
			total.total = total.total.Add(inc)
		}
	}
	return total, matched, nil
}

type decimalSumResult struct {
	total decimal.Decimal
}

// sumParquetFile sums amounts in a parquet file that match filters.
func sumParquetFile(path string, filters SearchRequest) (decimal.Decimal, bool, error) {
	matcher := compileSearchMatcher(filters)
	f, err := os.Open(path)
	if err != nil {
		return decimal.Zero, false, err
	}
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		_ = f.Close()
		return decimal.Zero, false, err
	}
	var r *parquet.GenericReader[parquetRow]
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				r = nil
			}
		}()
		r = parquet.NewGenericReader[parquetRow](f)
	}()
	if r == nil {
		_ = f.Close()
		return decimal.Zero, false, fmt.Errorf("parquet reader init failed")
	}
	matched := false
	total := decimal.Zero
	batch := make([]parquetRow, 1024)
	for {
		n, readErr := r.Read(batch)
		if n > 0 {
			for _, row := range batch[:n] {
				if matcher.matches(row) {
					matched = true
					total = total.Add(decimal.NewFromFloat(row.Amount))
				}
			}
		}
		if readErr != nil {
			break
		}
	}
	_ = r.Close()
	_ = f.Close()
	return total, matched, nil
}

// ContractRecord is a single contract row returned by QueryContracts.
type ContractRecord struct {
	ContractID    string  `json:"contractId"`
	ReleaseID     string  `json:"releaseId,omitempty"`
	OCID          string  `json:"ocid,omitempty"`
	Source        string  `json:"source"`
	Supplier      string  `json:"supplier"`
	Agency        string  `json:"agency"`
	Title         string  `json:"title"`
	Amount        float64 `json:"amount"`
	FinancialYear string  `json:"financialYear,omitempty"`
	ReleaseDate   string  `json:"releaseDate,omitempty"`
	IsUpdate      bool    `json:"isUpdate,omitempty"`
}

// ContractSearchResult holds structured search output.
type ContractSearchResult struct {
	Contracts  []ContractRecord `json:"contracts"`
	TotalSpend float64          `json:"totalSpend"`
	Count      int              `json:"count"`
}

// QueryContracts returns individual contract records from the Parquet lake.
func QueryContracts(ctx context.Context, filters SearchRequest, limit int) (ContractSearchResult, error) {
	cacheDir := defaultCacheDir()
	index, err := newColumnarIndex(cacheDir)
	if err != nil {
		return ContractSearchResult{}, err
	}

	lake := newDataLake(filepath.Join(cacheDir), index)
	if schemaErr := lake.ensureSchema(); schemaErr != nil {
		return ContractSearchResult{}, schemaErr
	}

	return lake.queryContracts(ctx, filters, limit)
}

// queryContracts reads individual rows from candidate parquet files.
func (l *dataLake) queryContracts(ctx context.Context, filters SearchRequest, limit int) (ContractSearchResult, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	var result ContractSearchResult
	totalSpend := decimal.Zero
	seen := make(map[string]struct{})

	for _, meta := range l.index.filesMatching(filters) {
		if len(result.Contracts) >= limit {
			break
		}
		records, spend, scanErr := readParquetContracts(meta.Path, filters, limit-len(result.Contracts), seen)
		if scanErr != nil {
			continue
		}
		result.Contracts = append(result.Contracts, records...)
		totalSpend = totalSpend.Add(spend)
	}

	result.Count = len(result.Contracts)
	result.TotalSpend, _ = totalSpend.Float64()
	return result, nil
}

// readParquetContracts reads matching rows from a single parquet file up to limit.
// seen tracks already-emitted ContractIDs to avoid duplicates across files.
func readParquetContracts(path string, filters SearchRequest, limit int, seen map[string]struct{}) ([]ContractRecord, decimal.Decimal, error) {
	matcher := compileSearchMatcher(filters)
	f, err := os.Open(path)
	if err != nil {
		return nil, decimal.Zero, err
	}
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		_ = f.Close()
		return nil, decimal.Zero, err
	}
	var r *parquet.GenericReader[parquetRow]
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				r = nil
			}
		}()
		r = parquet.NewGenericReader[parquetRow](f)
	}()
	if r == nil {
		_ = f.Close()
		return nil, decimal.Zero, fmt.Errorf("parquet reader init failed")
	}

	var records []ContractRecord
	spend := decimal.Zero
	batch := make([]parquetRow, 1024)
	for {
		n, readErr := r.Read(batch)
		if n > 0 {
			for _, row := range batch[:n] {
				if matcher.matches(row) {
					// Deduplicate by ContractID+Source to keep latest version
					deduKey := row.ContractID + "|" + row.Source
					if _, dup := seen[deduKey]; dup {
						continue
					}
					seen[deduKey] = struct{}{}
					releaseDate := ""
					if row.ReleaseEpoch > 0 {
						releaseDate = time.Unix(0, row.ReleaseEpoch*int64(time.Millisecond)).UTC().Format("2006-01-02")
					}
					records = append(records, ContractRecord{
						ContractID:    row.ContractID,
						ReleaseID:     row.ReleaseID,
						OCID:          row.OCID,
						Source:        row.Source,
						Supplier:      row.Supplier,
						Agency:        row.Agency,
						Title:         row.Title,
						Amount:        row.Amount,
						FinancialYear: row.FinancialYear,
						ReleaseDate:   releaseDate,
						IsUpdate:      row.IsUpdate,
					})
					spend = spend.Add(decimal.NewFromFloat(row.Amount))
					if len(records) >= limit {
						_ = r.Close()
						_ = f.Close()
						return records, spend, nil
					}
				}
			}
		}
		if readErr != nil {
			break
		}
	}
	_ = r.Close()
	_ = f.Close()
	return records, spend, nil
}

// hasMonthPartition returns true if a month partition already contains parquet files.
func (l *dataLake) hasMonthPartition(source string, ts time.Time) bool {
	if l == nil || l.index == nil {
		return false
	}
	return l.index.hasMonthPartition(source, ts)
}

// shouldFetchWindow reports whether a date window should be fetched based on existing partitions.
func (l *dataLake) shouldFetchWindow(source string, win dateWindow) bool {
	return !l.hasMonthPartition(source, win.start)
}

// countRows returns the number of rows in a parquet file without materializing records.
func countRows(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		_ = f.Close()
		return 0, err
	}

	var gr *parquet.GenericReader[parquetRow]
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				gr = nil
			}
		}()
		gr = parquet.NewGenericReader[parquetRow](f)
	}()
	if gr == nil {
		_ = f.Close()
		return 0, fmt.Errorf("parquet reader init failed")
	}
	defer gr.Close()
	defer f.Close()

	var rows int64
	buf := make([]parquetRow, 1024)
	for {
		n, readErr := gr.Read(buf)
		rows += int64(n)
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return rows, readErr
		}
	}
	return rows, nil
}

// parseLakePartition extracts source, fy, agency, and company keys from a lake file path.
func parseLakePartition(path string) (string, string, string, string) {
	parts := strings.Split(filepath.ToSlash(path), "/")
	var src, fy, ag, co string
	for _, p := range parts {
		if strings.HasPrefix(p, "source=") {
			src = strings.TrimPrefix(p, "source=")
		}
		if strings.HasPrefix(p, "fy=") {
			fy = strings.TrimPrefix(p, "fy=")
		}
		if strings.HasPrefix(p, "agency=") {
			ag = strings.TrimPrefix(p, "agency=")
		}
		if strings.HasPrefix(p, "company=") {
			co = strings.TrimPrefix(p, "company=")
		}
	}
	if src == "" {
		src = sanitizePartitionComponent(defaultSourceID)
	}
	return src, fy, ag, co
}

func monthLabelFromPath(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for _, p := range parts {
		if strings.HasPrefix(p, "month=") {
			return p
		}
	}
	return ""
}
