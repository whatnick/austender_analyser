//go:build vic_integration
// +build vic_integration

package cmd

import (
	"context"
	"os"
	"testing"
)

// Run with: VIC_INTEGRATION=1 VIC_USE_BROWSER=true go test -tags vic_integration ./cmd -run TestVicIntegrationLive
func TestVicIntegrationLive(t *testing.T) {
	if os.Getenv("VIC_INTEGRATION") == "" {
		t.Skip("set VIC_INTEGRATION=1 to run live VIC scrape")
	}
	ctx := context.Background()
	_, err := RunSearch(ctx, SearchRequest{Source: vicSourceID, Keyword: "Deloitte"})
	if err != nil {
		t.Fatalf("vic scrape failed: %v", err)
	}
}
