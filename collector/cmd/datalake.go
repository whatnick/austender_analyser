package cmd

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/snappy"
	"github.com/shopspring/decimal"
)

// dataLake tracks parquet files in a partitioned layout plus a SQLite index
// for fast discovery. Partitions are organized as fy=YYYY-YY/agency=<key>/company=<key>.
type dataLake struct {
	baseDir string
	db      *sql.DB
}

func newDataLake(baseDir string, db *sql.DB) *dataLake {
	return &dataLake{baseDir: baseDir, db: db}
}

func (l *dataLake) ensureSchema() error {
	const schema = `
    CREATE TABLE IF NOT EXISTS parquet_files (
        path TEXT PRIMARY KEY,
        fy TEXT NOT NULL,
        agency_key TEXT NOT NULL,
        company_key TEXT NOT NULL,
        row_count INTEGER NOT NULL,
        created_at TEXT NOT NULL
    );
    CREATE INDEX IF NOT EXISTS idx_parquet_files_keys ON parquet_files(fy, agency_key, company_key);
    `
	_, err := l.db.Exec(schema)
	return err
}

type lakeSink struct {
	w          *parquet.GenericWriter[parquetRow]
	file       *os.File
	lake       *dataLake
	fy         string
	agencyKey  string
	companyKey string
	rows       int64
}

// lakeWriterPool lazily opens sinks per partition derived from match content.
type lakeWriterPool struct {
	lake  *dataLake
	sinks map[string]*lakeSink
}

func newLakeWriterPool(l *dataLake) *lakeWriterPool {
	return &lakeWriterPool{lake: l, sinks: make(map[string]*lakeSink)}
}

func (l *dataLake) newSink(ts time.Time, agency, company string) (*lakeSink, error) {
	fy := financialYearLabel(ts)
	month := monthLabel(ts)
	ag := sanitizePartitionComponent(agency)
	co := sanitizePartitionComponent(company)
	if co == "" {
		co = "unknown_company"
	}
	dir := filepath.Join(l.baseDir, "lake", fy, month, fmt.Sprintf("agency=%s", ag), fmt.Sprintf("company=%s", co))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, fmt.Sprintf("part-%d.parquet", time.Now().Unix()))
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	w := parquet.NewGenericWriter[parquetRow](f, parquet.Compression(&snappy.Codec{}))
	return &lakeSink{w: w, file: f, lake: l, fy: fy, agencyKey: ag, companyKey: co}, nil
}

func (s *lakeSink) write(ms MatchSummary) {
	row := parquetRow{
		Partition:     partitionKeyLake(ms.ReleaseDate, ms.Agency, ms.Supplier),
		FinancialYear: financialYearLabel(ms.ReleaseDate),
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

func (s *lakeSink) close() {
	if s.w != nil {
		_ = s.w.Close()
	}
	if s.file != nil {
		_ = s.file.Close()
	}
	if s.lake != nil && s.rows > 0 {
		_, _ = s.lake.db.Exec("INSERT OR REPLACE INTO parquet_files(path, fy, agency_key, company_key, row_count, created_at) VALUES(?, ?, ?, ?, ?, ?)", s.file.Name(), s.fy, s.agencyKey, s.companyKey, s.rows, time.Now().UTC().Format(time.RFC3339))
	}
}

// write routes a match summary to the correct partition sink based on its content.
func (p *lakeWriterPool) write(ms MatchSummary) error {
	if p == nil || p.lake == nil {
		return fmt.Errorf("lake writer pool not initialized")
	}
	partition := partitionKeyLake(ms.ReleaseDate, ms.Agency, ms.Supplier)
	sink, ok := p.sinks[partition]
	if !ok {
		var err error
		sink, err = p.lake.newSink(ms.ReleaseDate, ms.Agency, ms.Supplier)
		if err != nil {
			return err
		}
		p.sinks[partition] = sink
	}
	sink.write(ms)
	return nil
}

func (p *lakeWriterPool) closeAll() {
	for _, s := range p.sinks {
		s.close()
	}
}

// rebuildIndex scans the lake directory and rebuilds the parquet_files index.
func (l *dataLake) rebuildIndex(ctx context.Context) error {
	if err := l.ensureSchema(); err != nil {
		return err
	}
	_, _ = l.db.ExecContext(ctx, "DELETE FROM parquet_files")
	root := filepath.Join(l.baseDir, "lake")
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".parquet") {
			return nil
		}
		fy, ag, co := parseLakePartition(path)
		rowCount, countErr := countRows(path)
		if countErr != nil {
			return nil
		}
		_, _ = l.db.ExecContext(ctx, "INSERT OR REPLACE INTO parquet_files(path, fy, agency_key, company_key, row_count, created_at) VALUES(?, ?, ?, ?, ?, ?)", path, fy, ag, co, rowCount, time.Now().UTC().Format(time.RFC3339))
		return nil
	})
}

// queryTotals returns sum of matching rows using the lake index to pick files.
func (l *dataLake) queryTotals(ctx context.Context, filters SearchRequest) (decimalSum decimalSumResult, matched bool, err error) {
	// Collect candidate files via index filtering.
	agencyKey := sanitizePartitionComponent(filters.Agency)
	companyKey := sanitizePartitionComponent(filters.Company)

	var args []any
	var clauses []string
	if agencyKey != "" {
		clauses = append(clauses, "agency_key = ?")
		args = append(args, agencyKey)
	}
	if companyKey != "" {
		clauses = append(clauses, "company_key = ?")
		args = append(args, companyKey)
	}

	// Lookback by FY if specified
	if filters.LookbackYears > 0 {
		minFy := financialYearLabel(time.Now().AddDate(-filters.LookbackYears, 0, 0))
		clauses = append(clauses, "fy >= ?")
		args = append(args, minFy)
	}

	where := ""
	if len(clauses) > 0 {
		where = "WHERE " + strings.Join(clauses, " AND ")
	}
	query := fmt.Sprintf("SELECT path FROM parquet_files %s", where)
	rows, err := l.db.QueryContext(ctx, query, args...)
	if err != nil {
		return decimalSumResult{}, false, err
	}
	defer rows.Close()

	total := decimalSumResult{}
	for rows.Next() {
		var path string
		if scanErr := rows.Scan(&path); scanErr != nil {
			return decimalSumResult{}, false, scanErr
		}
		inc, hit, scanErr := sumParquetFile(path, filters)
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
				if rowMatches(row, filters) {
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

// hasMonthPartition returns true if a month partition already contains parquet files.
func (l *dataLake) hasMonthPartition(ts time.Time) bool {
	root := filepath.Join(l.baseDir, "lake", financialYearLabel(ts), monthLabel(ts))
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			sub, err := os.ReadDir(filepath.Join(root, e.Name()))
			if err != nil {
				continue
			}
			for _, f := range sub {
				if !f.IsDir() && strings.HasSuffix(strings.ToLower(f.Name()), ".parquet") {
					return true
				}
			}
		}
	}
	return false
}

// shouldFetchWindow reports whether a date window should be fetched based on existing partitions.
func (l *dataLake) shouldFetchWindow(win dateWindow) bool {
	return !l.hasMonthPartition(win.start)
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

// parseLakePartition extracts fy, agency, and company keys from a lake file path.
func parseLakePartition(path string) (string, string, string) {
	parts := strings.Split(filepath.ToSlash(path), "/")
	var fy, ag, co string
	for _, p := range parts {
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
	return fy, ag, co
}
