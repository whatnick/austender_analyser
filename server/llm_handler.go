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
	// If the prompt looks like a spend query, prefetch using the collector cache and inject context.
	if pre, err := maybePrefetchSpend(r.Context(), req.Prompt); err == nil && pre != "" {
		prefetchedContext = pre
		basePrompt = pre + "\n\n" + basePrompt
	}
	fullPrompt := basePrompt
	if mcpContext != "" {
		fullPrompt = fmt.Sprintf("You can call MCP servers described by this JSON config (pass along to your agent tooling): %s\n\n%s", mcpContext, basePrompt)
	}

	client, err := openai.New(openai.WithModel(modelName))
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
	Prompt    string          `json:"prompt"`
	Model     string          `json:"model,omitempty"`
	MCPConfig json.RawMessage `json:"mcpConfig,omitempty"`
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

// maybePrefetchSpend tries to answer spend questions by querying the collector cache (20-year lookback).
func maybePrefetchSpend(ctx context.Context, prompt string) (string, error) {
	company, agency := parseSpendQuery(prompt)
	if company == "" && agency == "" {
		return "", nil
	}
	req := collector.SearchRequest{
		Company:       company,
		Agency:        agency,
		LookbackYears: 20,
	}
	res, _, err := collector.RunSearchWithCache(ctx, req)
	if err != nil {
		return "", err
	}
	parts := []string{fmt.Sprintf("Prefetched spend over the last 20 years: %s", res)}
	if company != "" {
		parts = append(parts, fmt.Sprintf("company=%s", company))
	}
	if agency != "" {
		parts = append(parts, fmt.Sprintf("agency=%s", agency))
	}
	return strings.Join(parts, " | "), nil
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
