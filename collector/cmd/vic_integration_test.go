//go:build vic_integration
// +build vic_integration

package cmd

import (
	"context"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/shopspring/decimal"
)

// Run with: VIC_INTEGRATION=1 VIC_USE_BROWSER=true go test -tags vic_integration ./cmd -run TestVicIntegrationLive
func TestVicIntegrationLive(t *testing.T) {
	if os.Getenv("VIC_INTEGRATION") == "" {
		t.Skip("set VIC_INTEGRATION=1 to run live VIC scrape")
	}
	ctx := context.Background()

	var matchCount atomic.Int64
	var positiveAmountCount atomic.Int64
	var firstInvalidMu sync.Mutex
	firstInvalid := ""

	total, err := RunSearch(ctx, SearchRequest{
		Source:  vicSourceID,
		Keyword: "Deloitte",
		OnAnyMatch: func(s MatchSummary) {
			matchCount.Add(1)
			if strings.TrimSpace(s.ContractID) == "" {
				firstInvalidMu.Lock()
				if firstInvalid == "" {
					firstInvalid = "empty ContractID"
				}
				firstInvalidMu.Unlock()
				return
			}
			if strings.TrimSpace(s.Title) == "" {
				firstInvalidMu.Lock()
				if firstInvalid == "" {
					firstInvalid = "empty Title"
				}
				firstInvalidMu.Unlock()
				return
			}
			if s.Amount.GreaterThan(decimal.Zero) {
				positiveAmountCount.Add(1)
			}
		},
	})
	if err != nil {
		t.Fatalf("vic scrape failed: %v", err)
	}

	if got := matchCount.Load(); got == 0 {
		t.Fatalf("vic scrape returned 0 matches")
	}
	if got := positiveAmountCount.Load(); got == 0 {
		t.Fatalf("vic scrape returned no positive-value matches (matches=%d)", matchCount.Load())
	}
	if firstInvalid != "" {
		t.Fatalf("vic scrape returned invalid match summary: %s", firstInvalid)
	}
	parsed, err := parseMoneyToDecimal(total)
	if err != nil {
		t.Fatalf("vic scrape returned unparseable total %q: %v", total, err)
	}
	if parsed.LessThanOrEqual(decimal.Zero) {
		t.Fatalf("vic scrape returned non-positive total %q", total)
	}
}
