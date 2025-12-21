package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"

	collector "github.com/whatnick/austender_analyser/collector/cmd"
)

// newLLMClient builds the LLM used by the handler. Overridden in integration tests.
var newLLMClient = func(modelName string) (llms.Model, error) {
	return openai.New(openai.WithModel(modelName))
}

// llmHandler accepts plain-text prompts and optional MCP server config to give the LLM
// more structured context. It relies on langchaingo so any supported backend can be
// swapped by changing the model name and env credentials (e.g., OpenAI-compatible APIs).
func llmHandler(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	var req LLMRequest
	decErr := json.NewDecoder(r.Body).Decode(&req)
	if decErr != nil {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return
	}
	lookback := req.LookbackPeriod
	if lookback <= 0 {
		lookback = 20
	}
	useCache := true
	if req.UseCache != nil {
		useCache = *req.UseCache
	}
	prefetchAllowed := true
	if req.Prefetch != nil {
		prefetchAllowed = *req.Prefetch
	}
	if len(req.MCPConfig) == 0 {
		req.MCPConfig = defaultMCPConfig()
	}
	if strings.TrimSpace(req.Prompt) == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return
	}

	modelName := strings.TrimSpace(req.Model)
	if modelName == "" {
		// Default to a widely available model; callers can override.
		modelName = "gpt-4o-mini"
	}

	mcpContext := strings.TrimSpace(string(req.MCPConfig))
	basePrompt := req.Prompt

	var prefetchedContext string
	// If allowed and the prompt looks like a spend query, prefetch using the collector cache and inject context.
	if prefetchAllowed {
		if pre, err := maybePrefetchComparison(r.Context(), req.Prompt, lookback, useCache); err == nil && pre != "" {
			prefetchedContext = pre
			basePrompt = pre + "\n\n" + basePrompt
		} else if pre, err := maybePrefetchSpend(r.Context(), req.Prompt, lookback, useCache); err == nil && pre != "" {
			prefetchedContext = pre
			basePrompt = pre + "\n\n" + basePrompt
		}
	}
	fullPrompt := basePrompt
	if mcpContext != "" {
		fullPrompt = fmt.Sprintf("You can call MCP servers described by this JSON config (pass along to your agent tooling): %s\n\n%s", mcpContext, basePrompt)
	}

	client, err := newLLMClient(modelName)
	if err != nil {
		http.Error(w, fmt.Sprintf("llm init failed: %v", err), http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	// Basic timeout to keep API responsive.
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	resp, err := llms.GenerateFromSinglePrompt(ctx, client, fullPrompt)
	if err != nil {
		http.Error(w, fmt.Sprintf("llm error: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(LLMResponse{Result: resp, Context: prefetchedContext})
}

type LLMRequest struct {
	Prompt         string          `json:"prompt"`
	Model          string          `json:"model,omitempty"`
	MCPConfig      json.RawMessage `json:"mcpConfig,omitempty"`
	Prefetch       *bool           `json:"prefetch,omitempty"`
	LookbackPeriod int             `json:"lookbackPeriod,omitempty"`
	UseCache       *bool           `json:"useCache,omitempty"`
}

type LLMResponse struct {
	Result  string `json:"result"`
	Context string `json:"context,omitempty"`
}

// Patterns:
// 1) "how much did {agency} spend on {company}"
// 2) "how much was spent on {company} by {agency}" (agency optional)
// 3) "how much was spent by {agency}" (agency only)
// 4) "how much did {agency} spend" (agency only)
var (
	spendAgencyRe     = regexp.MustCompile(`(?i)how\s+much\s+did\s+(.+?)\s+spend\s+on\s+([\w\s&\-\.]+)`)            // agency, company
	spendOnRe         = regexp.MustCompile(`(?i)how\s+much\s+was\s+spent\s+on\s+([\w\s&\-\.]+)(?:\s+by\s+(.+?))?$`) // company, optional agency
	spendByAgencyRe   = regexp.MustCompile(`(?i)how\s+much\s+was\s+spent\s+by\s+(.+?)\??$`)                         // agency only
	spendAgencyOnlyRe = regexp.MustCompile(`(?i)how\s+much\s+did\s+(.+?)\s+spend\??$`)                              // agency only
)

// maybePrefetchSpend tries to answer spend questions by querying the collector.
func maybePrefetchSpend(ctx context.Context, prompt string, lookbackPeriod int, useCache bool) (string, error) {
	if lookbackPeriod <= 0 {
		lookbackPeriod = 20
	}
	company, agency := parseSpendQuery(prompt)
	if company == "" && agency == "" {
		return "", nil
	}
	req := collector.SearchRequest{
		Company:        company,
		Agency:         agency,
		LookbackPeriod: lookbackPeriod,
	}
	var res string
	var err error
	if useCache {
		res, _, err = collector.RunSearchWithCache(ctx, req)
	} else {
		res, err = collector.RunSearch(ctx, req)
	}
	if err != nil {
		return "", err
	}
	parts := []string{fmt.Sprintf("Prefetched spend over the last %d years: %s", lookbackPeriod, res)}
	if company != "" {
		parts = append(parts, fmt.Sprintf("company=%s", company))
	}
	if agency != "" {
		parts = append(parts, fmt.Sprintf("agency=%s", agency))
	}
	return strings.Join(parts, " | "), nil
}

// maybePrefetchComparison handles prompts asking to compare spend between two agencies or two companies.
func maybePrefetchComparison(ctx context.Context, prompt string, lookbackPeriod int, useCache bool) (string, error) {
	if lookbackPeriod <= 0 {
		lookbackPeriod = 20
	}
	leftAgency, rightAgency := parseCompareAgencies(prompt)
	leftCo, rightCo := parseCompareCompanies(prompt)

	switch {
	case leftAgency != "" && rightAgency != "":
		return prefetchTwo(ctx, collector.SearchRequest{Agency: leftAgency, LookbackPeriod: lookbackPeriod}, collector.SearchRequest{Agency: rightAgency, LookbackPeriod: lookbackPeriod}, useCache)
	case leftCo != "" && rightCo != "":
		return prefetchTwo(ctx, collector.SearchRequest{Company: leftCo, LookbackPeriod: lookbackPeriod}, collector.SearchRequest{Company: rightCo, LookbackPeriod: lookbackPeriod}, useCache)
	default:
		return "", nil
	}
}

func prefetchTwo(ctx context.Context, leftReq, rightReq collector.SearchRequest, useCache bool) (string, error) {
	lookback := leftReq.LookbackPeriod
	if lookback <= 0 {
		lookback = 20
	}
	leftRes, err := runSearchMaybeCache(ctx, leftReq, useCache)
	if err != nil {
		return "", err
	}
	rightRes, err := runSearchMaybeCache(ctx, rightReq, useCache)
	if err != nil {
		return "", err
	}
	leftLabel := labelForReq(leftReq)
	rightLabel := labelForReq(rightReq)
	return fmt.Sprintf("Prefetched comparison over the last %d years: %s=%s | %s=%s", lookback, leftLabel, leftRes, rightLabel, rightRes), nil
}

func runSearchMaybeCache(ctx context.Context, req collector.SearchRequest, useCache bool) (string, error) {
	if useCache {
		res, _, err := collector.RunSearchWithCache(ctx, req)
		return res, err
	}
	return collector.RunSearch(ctx, req)
}

func labelForReq(r collector.SearchRequest) string {
	switch {
	case r.Agency != "":
		return r.Agency
	case r.Company != "":
		return r.Company
	default:
		return ""
	}
}

// parseSpendQuery extracts company and agency from common spend prompts.
func parseSpendQuery(prompt string) (company string, agency string) {
	p := strings.TrimSpace(prompt)
	if p == "" {
		return
	}
	if m := spendAgencyRe.FindStringSubmatch(p); len(m) == 3 {
		agency = normalizeEntity(m[1])
		company = normalizeEntity(m[2])
		return
	}
	if m := spendOnRe.FindStringSubmatch(p); len(m) >= 2 {
		company = normalizeEntity(m[1])
		if len(m) >= 3 {
			agency = normalizeEntity(m[2])
		}
		return
	}
	if m := spendByAgencyRe.FindStringSubmatch(p); len(m) == 2 {
		agency = normalizeEntity(m[1])
		return
	}
	if m := spendAgencyOnlyRe.FindStringSubmatch(p); len(m) == 2 {
		agency = normalizeEntity(m[1])
	}
	return
}

func normalizeEntity(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimSuffix(v, "?")
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	return strings.Title(strings.ToLower(v))
}

// parseCompareAgencies tries to extract two agencies from comparison phrasing without regex.
// Example: "Compare how much was spent by Home Affairs with how much was spent by Department of Defence".
func parseCompareAgencies(prompt string) (string, string) {
	p := strings.TrimSpace(prompt)
	if p == "" {
		return "", ""
	}
	lower := strings.ToLower(p)
	if !strings.Contains(lower, "compare") || !strings.Contains(lower, "with") || !strings.Contains(lower, "spent by") {
		return "", ""
	}
	parts := strings.SplitN(p, " with ", 2)
	if len(parts) != 2 {
		return "", ""
	}
	left := extractAfter(parts[0], "spent by")
	right := extractAfter(parts[1], "spent by")
	return normalizeEntity(left), normalizeEntity(right)
}

// parseCompareCompanies tries to extract two companies from comparison phrasing.
// Example: "Compare how much was spent on KPMG with how much was spent on Deloitte".
func parseCompareCompanies(prompt string) (string, string) {
	p := strings.TrimSpace(prompt)
	if p == "" {
		return "", ""
	}
	lower := strings.ToLower(p)
	if !strings.Contains(lower, "compare") || !strings.Contains(lower, "with") || !strings.Contains(lower, "spent on") {
		return "", ""
	}
	parts := strings.SplitN(p, " with ", 2)
	if len(parts) != 2 {
		return "", ""
	}
	left := extractAfter(parts[0], "spent on")
	right := extractAfter(parts[1], "spent on")
	return normalizeEntity(left), normalizeEntity(right)
}

// extractAfter returns the substring after the last occurrence of marker.
func extractAfter(s, marker string) string {
	lower := strings.ToLower(s)
	idx := strings.LastIndex(lower, marker)
	if idx == -1 {
		return ""
	}
	return strings.TrimSpace(s[idx+len(marker):])
}

// Helper to detect available MCP config path via env for defaults.
func defaultMCPConfig() json.RawMessage {
	path := strings.TrimSpace(os.Getenv("AUSTENDER_MCP_CONFIG"))
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return b
}
