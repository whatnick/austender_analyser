package cmd

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/leekchan/accounting"
	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/snappy"
	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"
)

// cacheCmd wires an incremental ETL that writes OCDS matches into parquet files
// and tracks checkpoints in a lightweight SQLite catalog. Subsequent runs resume
// from the last completed window while full scrapes remain available via the
// existing root command.
var cacheCmd = &cobra.Command{
	Use:   "cache",
	Short: "Incremental ETL to local parquet cache backed by SQLite checkpoints",
	RunE: func(cmd *cobra.Command, args []string) error {
		keyword, _ := cmd.Flags().GetString("keyword")
		company, _ := cmd.Flags().GetString("company")
		agency, _ := cmd.Flags().GetString("agency")
		dateType, _ := cmd.Flags().GetString("date-type")
		lookbackYears, _ := cmd.Flags().GetInt("lookback-years")
		cacheDir, _ := cmd.Flags().GetString("cache-dir")
		noCache, _ := cmd.Flags().GetBool("no-cache")
		startRaw, _ := cmd.Flags().GetString("start-date")
		endRaw, _ := cmd.Flags().GetString("end-date")

		if keyword == "" {
			return errors.New("keyword is required")
		}

		start, err := parseDateFlag(startRaw)
		if err != nil {
			return err
		}
		end, err := parseDateFlag(endRaw)
		if err != nil {
			return err
		}
		if err := validateDateOrder(start, end); err != nil {
			return err
		}

		if noCache {
			_, err := RunSearch(context.Background(), SearchRequest{
				Keyword:       keyword,
				Company:       company,
				Agency:        agency,
				StartDate:     start,
				EndDate:       end,
				DateType:      dateType,
				LookbackYears: lookbackYears,
			})
			return err
		}

		cache, err := newCacheManager(cacheDir)
		if err != nil {
			return err
		}
		defer cache.close()

		cachedTotal, cacheHit, err := cache.queryCache(SearchRequest{Keyword: keyword, Company: company, Agency: agency})
		if err != nil {
			return err
		}
		if cacheHit {
			fmt.Printf("Cache result: %s (before refresh)\n", formatMoneyDecimal(cachedTotal))
		}

		checkpointKey := cacheKey(keyword, company, agency, dateType)
		resumeFrom, _ := cache.loadCheckpoint(checkpointKey)
		if !resumeFrom.IsZero() && (start.IsZero() || resumeFrom.After(start)) {
			start = resumeFrom
		}

		sink, err := cache.newParquetSink(partitionKey(time.Now(), agency))
		if err != nil {
			return err
		}
		defer sink.close()

		_, err = RunSearch(context.Background(), SearchRequest{
			Keyword:       keyword,
			Company:       company,
			Agency:        agency,
			StartDate:     start,
			EndDate:       end,
			DateType:      dateType,
			LookbackYears: lookbackYears,
			OnMatch:       sink.write,
		})
		if err != nil {
			return err
		}

		finalCheckpoint := end
		if finalCheckpoint.IsZero() {
			finalCheckpoint = time.Now().UTC()
		}
		return cache.saveCheckpoint(checkpointKey, finalCheckpoint)
	},
}

// RunSearchWithCache prefers cached totals when available, then fetches and appends
// new data beyond the stored checkpoint. It returns the combined formatted total and
// indicates whether a cache hit was used.
func RunSearchWithCache(ctx context.Context, req SearchRequest) (string, bool, error) {
	useCache := strings.ToLower(strings.TrimSpace(os.Getenv("AUSTENDER_USE_CACHE")))
	if useCache == "false" || useCache == "0" {
		res, err := RunSearch(ctx, req)
		return res, false, err
	}

	cache, err := newCacheManager(defaultCacheDir())
	if err != nil {
		return "", false, err
	}
	defer cache.close()

	checkpointKey := cacheKey(req.Keyword, req.Company, req.Agency, req.DateType)
	resumeFrom, _ := cache.loadCheckpoint(checkpointKey)
	start := req.StartDate
	end := req.EndDate
	if !resumeFrom.IsZero() && (start.IsZero() || resumeFrom.After(start)) {
		start = resumeFrom
	}

	cachedTotal, cacheHit, err := cache.queryCache(req)
	if err != nil {
		return "", false, err
	}

	// If we have a cache hit and a recent checkpoint, short-circuit to avoid unnecessary fetches.
	if cacheHit && !resumeFrom.IsZero() && time.Since(resumeFrom) < 24*time.Hour && start.Equal(resumeFrom) && end.IsZero() {
		return formatMoneyDecimal(cachedTotal), true, nil
	}

	sink, err := cache.newParquetSink(partitionKey(time.Now(), req.Agency))
	if err != nil {
		return "", cacheHit, err
	}
	defer sink.close()

	incStr, err := RunSearch(ctx, SearchRequest{
		Keyword:       req.Keyword,
		Company:       req.Company,
		Agency:        req.Agency,
		StartDate:     start,
		EndDate:       end,
		DateType:      req.DateType,
		LookbackYears: req.LookbackYears,
		OnMatch:       sink.write,
	})
	if err != nil {
		return "", cacheHit, err
	}

	incDec, err := parseMoneyToDecimal(incStr)
	if err != nil {
		return "", cacheHit, err
	}
	combined := cachedTotal.Add(incDec)

	finalCheckpoint := end
	if finalCheckpoint.IsZero() {
		finalCheckpoint = time.Now().UTC()
	}
	_ = cache.saveCheckpoint(checkpointKey, finalCheckpoint)

	return formatMoneyDecimal(combined), cacheHit, nil
}

// RunSearchPreferCache adapts RunSearchWithCache to the existing signature.
func RunSearchPreferCache(ctx context.Context, req SearchRequest) (string, error) {
	res, _, err := RunSearchWithCache(ctx, req)
	return res, err
}

func init() {
	rootCmd.AddCommand(cacheCmd)
	cacheCmd.Flags().String("keyword", "", "Keyword to scan (required)")
	cacheCmd.Flags().String("company", "", "Company to scan")
	cacheCmd.Flags().String("agency", "", "Agency to scan")
	cacheCmd.Flags().String("date-type", defaultDateType, "OCDS date field: contractPublished, contractStart, contractEnd, contractLastModified")
	cacheCmd.Flags().Int("lookback-years", defaultLookbackYears, "Default window when start not specified")
	cacheCmd.Flags().String("cache-dir", defaultCacheDir(), "Directory for parquet files and sqlite catalog")
	cacheCmd.Flags().Bool("no-cache", false, "Bypass cache and run a full scrape (does not write parquet)")
	cacheCmd.Flags().String("start-date", "", "Optional start date (YYYY-MM-DD or RFC3339)")
	cacheCmd.Flags().String("end-date", "", "Optional end date (YYYY-MM-DD or RFC3339)")
}

type cacheManager struct {
	baseDir string
	db      *sql.DB
}

func formatMoneyDecimal(v decimal.Decimal) string {
	ac := accounting.Accounting{Symbol: "$", Precision: 2}
	return ac.FormatMoney(v)
}

func parseMoneyToDecimal(v string) (decimal.Decimal, error) {
	clean := strings.ReplaceAll(v, "$", "")
	clean = strings.ReplaceAll(clean, ",", "")
	clean = strings.TrimSpace(clean)
	if clean == "" {
		return decimal.Zero, nil
	}
	return decimal.NewFromString(clean)
}

func newCacheManager(baseDir string) (*cacheManager, error) {
	if baseDir == "" {
		baseDir = defaultCacheDir()
	}
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(baseDir, "catalog.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	mgr := &cacheManager{baseDir: baseDir, db: db}
	if err := mgr.ensureSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return mgr, nil
}

func (m *cacheManager) ensureSchema() error {
	const schema = `
	CREATE TABLE IF NOT EXISTS checkpoints (
		key TEXT PRIMARY KEY,
		last_run TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS partitions (
		partition_key TEXT NOT NULL,
		path TEXT PRIMARY KEY,
		created_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_partitions_key ON partitions(partition_key);
	`
	_, err := m.db.Exec(schema)
	return err
}

func (m *cacheManager) close() {
	if m.db != nil {
		_ = m.db.Close()
	}
}

func cacheKey(keyword, company, agency, dateType string) string {
	return fmt.Sprintf("k=%s|c=%s|a=%s|d=%s", keyword, company, agency, dateType)
}

func (m *cacheManager) loadCheckpoint(key string) (time.Time, error) {
	row := m.db.QueryRow("SELECT last_run FROM checkpoints WHERE key = ?", key)
	var ts string
	if err := row.Scan(&ts); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return time.Time{}, err
	}
	return t, nil
}

func (m *cacheManager) saveCheckpoint(key string, t time.Time) error {
	_, err := m.db.Exec("INSERT INTO checkpoints(key, last_run) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET last_run = excluded.last_run", key, t.UTC().Format(time.RFC3339))
	return err
}

func partitionKey(ts time.Time, agency string) string {
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	fy := financialYearLabel(ts)
	ag := sanitizePartitionComponent(agency)
	if ag == "" {
		ag = "unknown_agency"
	}
	return filepath.Join(fy, fmt.Sprintf("agency=%s", ag))
}

func financialYearLabel(ts time.Time) string {
	year := ts.Year()
	if ts.Month() < time.July {
		year--
	}
	return fmt.Sprintf("fy=%d-%02d", year, (year+1)%100)
}

var sanitizeRe = regexp.MustCompile(`[^a-z0-9_-]+`)

func sanitizePartitionComponent(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.ReplaceAll(v, " ", "_")
	v = sanitizeRe.ReplaceAllString(v, "")
	if v == "" {
		return "unknown"
	}
	return v
}

func (m *cacheManager) newParquetSink(partition string) (*parquetSink, error) {
	dir := filepath.Join(m.baseDir, "parquet", partition)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, fmt.Sprintf("part-%d.parquet", time.Now().Unix()))
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	w := parquet.NewGenericWriter[parquetRow](f, parquet.Compression(&snappy.Codec{}))
	if _, err := m.db.Exec("INSERT OR IGNORE INTO partitions(partition_key, path, created_at) VALUES(?, ?, ?)", partition, path, time.Now().UTC().Format(time.RFC3339)); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &parquetSink{w: w, file: f}, nil
}

func (m *cacheManager) queryCache(filters SearchRequest) (decimal.Decimal, bool, error) {
	rows, err := m.db.Query("SELECT path FROM partitions")
	if err != nil {
		return decimal.Zero, false, err
	}
	defer rows.Close()

	var total decimal.Decimal
	matched := false
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return decimal.Zero, false, err
		}
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		info, statErr := f.Stat()
		if statErr != nil || info.Size() == 0 {
			_ = f.Close()
			continue
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
			continue
		}
		batch := make([]parquetRow, 1024)
		for {
			n, err := r.Read(batch)
			if n > 0 {
				for _, row := range batch[:n] {
					if rowMatches(row, filters) {
						matched = true
						total = total.Add(decimal.NewFromFloat(row.Amount))
					}
				}
			}
			if err != nil {
				break
			}
		}
		_ = r.Close()
		_ = f.Close()
	}
	return total, matched, nil
}

type parquetSink struct {
	w    *parquet.GenericWriter[parquetRow]
	file *os.File
	mu   sync.Mutex
}

func (s *parquetSink) write(ms MatchSummary) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := parquetRow{
		Partition:     partitionKey(ms.ReleaseDate, ms.Agency),
		FinancialYear: financialYearLabel(ms.ReleaseDate),
		AgencyKey:     sanitizePartitionComponent(ms.Agency),
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
}

func (s *parquetSink) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.w != nil {
		_ = s.w.Close()
	}
	if s.file != nil {
		_ = s.file.Close()
	}
}

func rowMatches(row parquetRow, filters SearchRequest) bool {
	kw := strings.ToLower(filters.Keyword)
	comp := strings.ToLower(filters.Company)
	agency := strings.ToLower(filters.Agency)

	if kw != "" {
		hay := strings.ToLower(row.Supplier + " " + row.Title + " " + row.Agency + " " + row.ContractID)
		if !strings.Contains(hay, kw) {
			return false
		}
	}
	if comp != "" && !strings.Contains(strings.ToLower(row.Supplier), comp) {
		return false
	}
	if agency != "" && !strings.Contains(strings.ToLower(row.Agency), agency) {
		return false
	}
	return true
}

type parquetRow struct {
	Partition     string  `parquet:"name=partition, type=BYTE_ARRAY, convertedtype=UTF8"`
	FinancialYear string  `parquet:"name=financial_year, type=BYTE_ARRAY, convertedtype=UTF8"`
	AgencyKey     string  `parquet:"name=agency_key, type=BYTE_ARRAY, convertedtype=UTF8"`
	ContractID    string  `parquet:"name=contract_id, type=BYTE_ARRAY, convertedtype=UTF8"`
	ReleaseID     string  `parquet:"name=release_id, type=BYTE_ARRAY, convertedtype=UTF8"`
	OCID          string  `parquet:"name=ocid, type=BYTE_ARRAY, convertedtype=UTF8"`
	Supplier      string  `parquet:"name=supplier, type=BYTE_ARRAY, convertedtype=UTF8"`
	Agency        string  `parquet:"name=agency, type=BYTE_ARRAY, convertedtype=UTF8"`
	Title         string  `parquet:"name=title, type=BYTE_ARRAY, convertedtype=UTF8"`
	Amount        float64 `parquet:"name=amount, type=DOUBLE"`
	ReleaseEpoch  int64   `parquet:"name=release_epoch_ms, type=INT64, logicaltype=TIMESTAMP_MILLIS"`
	IsUpdate      bool    `parquet:"name=is_update, type=BOOLEAN"`
}

func defaultCacheDir() string {
	if dir := os.Getenv("AUSTENDER_CACHE_DIR"); dir != "" {
		return dir
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".cache", "austender")
	}
	return filepath.Join(".cache", "austender")
}
