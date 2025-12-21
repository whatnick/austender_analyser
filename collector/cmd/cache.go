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
	"time"

	_ "modernc.org/sqlite"

	"github.com/leekchan/accounting"
	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"
)

const indexRebuildInterval = 24 * time.Hour

// runSearchFunc is overridable for tests to assert cache short-circuits.
var runSearchFunc = RunSearch

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
		source, _ := cmd.Flags().GetString("source")
		lookbackPeriod, _ := cmd.Flags().GetInt("lookback-period")
		cacheDir, _ := cmd.Flags().GetString("cache-dir")
		noCache, _ := cmd.Flags().GetBool("no-cache")
		startRaw, _ := cmd.Flags().GetString("start-date")
		endRaw, _ := cmd.Flags().GetString("end-date")

		source = normalizeSourceID(source)

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
				Keyword:        keyword,
				Company:        company,
				Agency:         agency,
				Source:         source,
				StartDate:      start,
				EndDate:        end,
				DateType:       dateType,
				LookbackPeriod: lookbackPeriod,
			})
			return err
		}

		cache, err := newCacheManager(cacheDir)
		if err != nil {
			return err
		}
		defer cache.close()

		cachedTotal, cacheHit, err := cache.queryCache(SearchRequest{Keyword: keyword, Company: company, Agency: agency, Source: source})
		if err != nil {
			return err
		}
		if cacheHit {
			fmt.Printf("Cache result: %s (before refresh)\n", formatMoneyDecimal(cachedTotal))
		}

		checkpointKey := cacheKey(keyword, company, agency, dateType, source)
		resumeFrom, _ := cache.loadCheckpoint(checkpointKey)
		if !resumeFrom.IsZero() && (start.IsZero() || resumeFrom.After(start)) {
			start = resumeFrom
		}

		pool := newLakeWriterPool(cache.lake)
		_, err = RunSearch(context.Background(), SearchRequest{
			Keyword:        keyword,
			Company:        company,
			Agency:         agency,
			Source:         source,
			StartDate:      start,
			EndDate:        end,
			DateType:       dateType,
			LookbackPeriod: lookbackPeriod,
			OnAnyMatch: func(ms MatchSummary) {
				_ = pool.write(ms)
			},
			ShouldFetchWindow: func(win dateWindow) bool {
				return cache.lake.shouldFetchWindow(source, win)
			},
		})
		pool.closeAll()
		if cache.shouldReindex() {
			if err := cache.lake.rebuildIndex(context.Background()); err != nil {
				return err
			}
			cache.markReindexed()
		}
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
// indicates whether a cache hit was used. Callers can supply OnMatch/OnProgress in req;
// they will be invoked for fresh scans and results will also be written to the lake.
func RunSearchWithCache(ctx context.Context, req SearchRequest) (string, bool, error) {
	useCache := strings.ToLower(strings.TrimSpace(os.Getenv("AUSTENDER_USE_CACHE")))
	if useCache == "false" || useCache == "0" {
		req.Source = normalizeSourceID(req.Source)
		res, err := runSearchFunc(ctx, req)
		return res, false, err
	}

	resolvedSource := normalizeSourceID(req.Source)

	cache, err := newCacheManager(defaultCacheDir())
	if err != nil {
		return "", false, err
	}
	defer cache.close()

	checkpointKey := cacheKey(req.Keyword, req.Company, req.Agency, req.DateType, resolvedSource)
	lastRun, _ := cache.loadCheckpoint(checkpointKey)
	resolvedLookback := resolveLookbackPeriod(req.LookbackPeriod)
	startResolved, endResolved := resolveDates(req.StartDate, req.EndDate, resolvedLookback)

	workingReq := req
	workingReq.Source = resolvedSource
	workingReq.StartDate = startResolved
	workingReq.EndDate = endResolved
	workingReq.LookbackPeriod = resolvedLookback

	cachedTotal, cacheHit, err := cache.queryCache(workingReq)
	if err != nil {
		return "", false, err
	}

	// If every window in range already exists in the lake, rely on the cached total.
	if cacheHit && cache.lake != nil && windowsCached(cache.lake, resolvedSource, startResolved, endResolved) {
		return formatMoneyDecimal(cachedTotal), true, nil
	}

	// Adjust search start to resume from checkpoint if it's within the requested range.
	searchStart := startResolved
	if !lastRun.IsZero() && lastRun.After(searchStart) && lastRun.Before(endResolved) {
		searchStart = lastRun
	}

	pool := newLakeWriterPool(cache.lake)
	userOnMatch := req.OnMatch
	mergedOnMatch := func(summary MatchSummary) {
		if summary.Source == "" {
			summary.Source = resolvedSource
		}
		if userOnMatch != nil {
			userOnMatch(summary)
		}
		_ = pool.write(summary)
	}

	incStr, err := runSearchFunc(ctx, SearchRequest{
		Keyword:        req.Keyword,
		Company:        req.Company,
		Agency:         req.Agency,
		Source:         resolvedSource,
		StartDate:      searchStart,
		EndDate:        endResolved,
		DateType:       req.DateType,
		LookbackPeriod: resolvedLookback,
		OnMatch:        mergedOnMatch,
		OnAnyMatch: func(ms MatchSummary) {
			if ms.Source == "" {
				ms.Source = resolvedSource
			}
			_ = pool.write(ms)
		},
		OnProgress: req.OnProgress,
		ShouldFetchWindow: func(win dateWindow) bool {
			return cache.lake.shouldFetchWindow(resolvedSource, win)
		},
	})
	if err != nil {
		return "", cacheHit, err
	}

	pool.closeAll()
	if cache.shouldReindex() {
		if err := cache.lake.rebuildIndex(ctx); err != nil {
			return "", cacheHit, err
		}
		cache.markReindexed()
	}

	incDec, err := parseMoneyToDecimal(incStr)
	if err != nil {
		return "", cacheHit, err
	}
	combined := cachedTotal.Add(incDec)

	finalCheckpoint := endResolved
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

// windowsCached returns true when every date window between start and end already has a lake partition for the given source.
func windowsCached(l *dataLake, source string, start, end time.Time) bool {
	if l == nil {
		return false
	}
	source = normalizeSourceID(source)
	for _, win := range splitDateWindows(start, end, maxWindowDays) {
		if l.shouldFetchWindow(source, win) {
			return false
		}
	}
	return true
}

func init() {
	rootCmd.AddCommand(cacheCmd)
	cacheCmd.Flags().String("keyword", "", "Keyword to scan (optional; empty primes cache/lake)")
	cacheCmd.Flags().String("company", "", "Company filter (optional)")
	cacheCmd.Flags().String("agency", "", "Agency filter (optional)")
	cacheCmd.Flags().String("source", defaultSourceID, "Data source identifier (e.g., federal)")
	cacheCmd.Flags().String("date-type", defaultDateType, "OCDS date field: contractPublished, contractStart, contractEnd, contractLastModified")
	cacheCmd.Flags().Int("lookback-period", defaultLookbackPeriod, "Default window when start not specified")
	cacheCmd.Flags().String("cache-dir", defaultCacheDir(), "Directory for parquet files and sqlite catalog")
	cacheCmd.Flags().Bool("no-cache", false, "Bypass cache and run a full scrape (does not write parquet)")
	cacheCmd.Flags().String("start-date", "", "Optional start date (YYYY-MM-DD or RFC3339)")
	cacheCmd.Flags().String("end-date", "", "Optional end date (YYYY-MM-DD or RFC3339)")
}

type cacheManager struct {
	baseDir string
	db      *sql.DB
	lake    *dataLake
}

func (m *cacheManager) indexMarkerPath() string {
	return filepath.Join(m.baseDir, "index.last")
}

func (m *cacheManager) shouldReindex() bool {
	info, err := os.Stat(m.indexMarkerPath())
	if err != nil {
		return true
	}
	return time.Since(info.ModTime()) >= indexRebuildInterval
}

func (m *cacheManager) markReindexed() {
	_ = os.WriteFile(m.indexMarkerPath(), []byte(time.Now().UTC().Format(time.RFC3339)), 0o644)
}

func formatMoneyDecimal(v decimal.Decimal) string {
	ac := accounting.Accounting{Symbol: "$", Precision: 2}
	return ac.FormatMoney(v)
}

func parseMoneyToDecimal(v string) (decimal.Decimal, error) {
	clean := strings.TrimSpace(v)
	clean = strings.ReplaceAll(clean, ",", "")
	if clean == "" {
		return decimal.Zero, nil
	}

	// Be lenient: values may include currency prefixes like "A$", "AUD", NBSPs, etc.
	// Extract the first numeric token and parse that.
	num := regexp.MustCompile(`-?\d+(?:\.\d+)?`).FindString(clean)
	if num == "" {
		return decimal.Zero, fmt.Errorf("no numeric value in %q", v)
	}
	return decimal.NewFromString(num)
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
	mgr.lake = newDataLake(baseDir, db)
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
	if _, err := m.db.Exec(schema); err != nil {
		return err
	}
	return m.lake.ensureSchema()
}

func (m *cacheManager) close() {
	if m.db != nil {
		_ = m.db.Close()
	}
}

func cacheKey(keyword, company, agency, dateType, source string) string {
	normalizedSource := normalizeSourceID(source)
	return fmt.Sprintf("s=%s|k=%s|c=%s|a=%s|d=%s", normalizedSource, keyword, company, agency, dateType)
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

// partitionKeyLake builds a richer partition including source and company for the lake layout.
func partitionKeyLake(ts time.Time, source, agency, company string) string {
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	fy := financialYearLabel(ts)
	month := monthLabel(ts)
	src := sanitizePartitionComponent(normalizeSourceID(source))
	if src == "" {
		src = sanitizePartitionComponent(defaultSourceID)
	}
	ag := sanitizePartitionComponent(agency)
	if ag == "" {
		ag = "unknown_agency"
	}
	co := sanitizePartitionComponent(company)
	if co == "" {
		co = "unknown_company"
	}
	return filepath.Join(fmt.Sprintf("source=%s", src), fy, month, fmt.Sprintf("agency=%s", ag), fmt.Sprintf("company=%s", co))
}

func monthLabel(ts time.Time) string {
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	return fmt.Sprintf("month=%04d-%02d", ts.Year(), ts.Month())
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

func (m *cacheManager) newParquetSink(source string, ts time.Time, agency, company string) (*lakeSink, error) {
	return m.lake.newSink(source, ts, agency, company)
}

func (m *cacheManager) queryCache(filters SearchRequest) (decimal.Decimal, bool, error) {
	filters.Source = normalizeSourceID(filters.Source)
	res, matched, err := m.lake.queryTotals(context.Background(), filters)
	if err != nil {
		return decimal.Zero, false, err
	}
	return res.total, matched, nil
}

func rowMatches(row parquetRow, filters SearchRequest) bool {
	if normalized := strings.TrimSpace(filters.Source); normalized != "" {
		rowSource := row.Source
		if rowSource == "" {
			rowSource = defaultSourceID
		}
		if normalizeSourceID(normalized) != normalizeSourceID(rowSource) {
			return false
		}
	}
	if !filters.StartDate.IsZero() {
		rowTime := time.Unix(0, row.ReleaseEpoch*int64(time.Millisecond)).UTC()
		if rowTime.Before(filters.StartDate.UTC()) {
			return false
		}
	}
	if !filters.EndDate.IsZero() {
		rowTime := time.Unix(0, row.ReleaseEpoch*int64(time.Millisecond)).UTC()
		if rowTime.After(filters.EndDate.UTC()) {
			return false
		}
	}

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
	Source        string  `parquet:"name=source, type=BYTE_ARRAY, convertedtype=UTF8"`
	FinancialYear string  `parquet:"name=financial_year, type=BYTE_ARRAY, convertedtype=UTF8"`
	AgencyKey     string  `parquet:"name=agency_key, type=BYTE_ARRAY, convertedtype=UTF8"`
	CompanyKey    string  `parquet:"name=company_key, type=BYTE_ARRAY, convertedtype=UTF8"`
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
