package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/ollama"
	"github.com/tmc/langchaingo/llms/openai"

	collector "github.com/whatnick/austender_analyser/collector/cmd"
)

// newLLMClient builds the LLM used by the handler. Overridden in integration tests.
var newLLMClient = func(modelName, backendOverride string) (llms.Model, error) {
	backend := selectBackend(modelName, backendOverride)
	if backend == llmBackendOllama {
		opts := []ollama.Option{ollama.WithModel(modelName)}
		if base := strings.TrimSpace(os.Getenv("OLLAMA_HOST")); base != "" {
			opts = append(opts, ollama.WithServerURL(normalizeOllamaURL(base)))
		}
		if system := strings.TrimSpace(os.Getenv("OLLAMA_SYSTEM_PROMPT")); system != "" {
			opts = append(opts, ollama.WithSystemPrompt(system))
		}
		return ollama.New(opts...)
	}
	return openai.New(openai.WithModel(modelName))
}

// generateFromPrompt is overridable in tests.
var generateFromPrompt = func(ctx context.Context, client llms.Model, prompt string) (string, error) {
	return llms.GenerateFromSinglePrompt(ctx, client, prompt)
}

// runSearchWithCache is overridable in tests.
var runSearchWithCache = collector.RunSearchWithCache

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
		sendJSONError(w, "invalid request body", http.StatusBadRequest)
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
	if len(req.MCPConfig) == 0 {
		req.MCPConfig = defaultMCPConfig()
	}
	if strings.TrimSpace(req.Prompt) == "" {
		sendJSONError(w, "prompt is required", http.StatusBadRequest)
		return
	}

	modelName, backendOverride, err := resolveModelName(strings.TrimSpace(req.Model))
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}

	mcpContext := strings.TrimSpace(string(req.MCPConfig))
	basePrompt := req.Prompt

	var prefetchedContext string
	if directResp, directContext, ok := directSpendResponse(r.Context(), req.Prompt, strings.TrimSpace(req.Source), lookback, useCache); ok {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(LLMResponse{Result: directResp, Context: directContext})
		return
	}

	// If the prompt looks like a spend query, prefetch using the collector cache
	// and inject context.
	if pre, err := maybePrefetchSpend(r.Context(), req.Prompt, strings.TrimSpace(req.Source), lookback); err == nil && pre != "" {
		prefetchedContext = pre
		basePrompt = pre + "\n\n" + basePrompt
	}

	client, err := newLLMClient(modelName, backendOverride)
	if err != nil {
		msg := fmt.Sprintf("llm init failed: %v", err)
		if strings.Contains(msg, "OPENAI_API_KEY") || strings.Contains(msg, "no API key") {
			msg = "LLM initialization failed: OPENAI_API_KEY is not set in the environment. Please set it to use the chat feature."
		}
		if strings.Contains(msg, "OLLAMA_HOST") {
			msg = "LLM initialization failed: OLLAMA_HOST is not reachable. Ensure the Ollama server is running and the host is correct."
		}
		sendJSONError(w, msg, http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	// Basic timeout to keep API responsive.
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	// Agent mode: always-on tool-calling loop that executes local MCP-equivalent tools.
	agent := true

	resp := ""
	agentErr := error(nil)
	if agent {
		resp, agentErr = runLLMAgent(ctx, client, basePrompt, strings.TrimSpace(req.Source), lookback, useCache, mcpContext)
	}
	if !agent || agentErr != nil {
		fullPrompt := basePrompt
		if mcpContext != "" {
			// Keep backwards compatibility: include MCP config as plain context.
			fullPrompt = fmt.Sprintf("MCP config (for tool-aware agents): %s\n\n%s", mcpContext, basePrompt)
		}
		resp, err = generateFromPrompt(ctx, client, fullPrompt)
		if err != nil {
			if fallback, ok := directAggregateResponse(basePrompt, strings.TrimSpace(req.Source), lookback, useCache); ok {
				resp = fallback
				err = nil
			} else {
				sendJSONError(w, fmt.Sprintf("llm error: %v", err), http.StatusInternalServerError)
				return
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(LLMResponse{Result: resp, Context: prefetchedContext})
}

// Patterns:
// 1) "how much did {agency} spend on {company}"
// 2) "how much was spent on {company} by {agency}" (agency optional)
// 3) "how much was spent by {agency}" (agency only)
// 4) "how much did {agency} spend" (agency only)
var (
	spendAgencyRe     = regexp.MustCompile(`(?i)how\s+much\s+did\s+(.+?)\s+spend\s+on\s+([\w\s&\-\.]+)\s*\??\s*$`)           // agency, company
	spendOnRe         = regexp.MustCompile(`(?i)how\s+much\s+was\s+spent\s+on\s+([\w\s&\-\.]+)(?:\s+by\s+(.+?))?\s*\??\s*$`) // company, optional agency
	spendByAgencyRe   = regexp.MustCompile(`(?i)how\s+much\s+was\s+spent\s+by\s+(.+?)\s*\??\s*$`)                            // agency only
	spendAgencyOnlyRe = regexp.MustCompile(`(?i)how\s+much\s+did\s+(.+?)\s+spend\s*\??\s*$`)                                 // agency only
)

// maybePrefetchSpend tries to answer spend questions by querying the collector
// cache and injecting the result.
func maybePrefetchSpend(ctx context.Context, prompt, sourceHint string, lookback int) (string, error) {
	req, company, agency, ok := buildSpendQueryRequest(prompt, sourceHint, lookback)
	if !ok {
		return "", nil
	}
	res, _, err := runSearchWithCache(ctx, req)
	if err != nil {
		return "", err
	}
	return formatSpendPrefetchContext(req.Source, req.LookbackPeriod, res, company, agency), nil
}

func directSpendResponse(ctx context.Context, prompt, sourceHint string, lookback int, useCache bool) (string, string, bool) {
	req, company, agency, ok := buildSpendQueryRequest(prompt, sourceHint, lookback)
	if !ok {
		return "", "", false
	}

	var (
		res string
		err error
	)
	if useCache {
		res, _, err = runSearchWithCache(ctx, req)
	} else {
		res, err = runScrape(ctx, req)
	}
	if err != nil || strings.TrimSpace(res) == "" {
		return "", "", false
	}

	contextText := formatSpendPrefetchContext(req.Source, req.LookbackPeriod, res, company, agency)
	scope := ""
	if strings.TrimSpace(req.Source) != "" {
		scope = fmt.Sprintf(" (%s)", strings.ToUpper(req.Source))
	}

	target := ""
	switch {
	case company != "" && agency != "":
		target = fmt.Sprintf("Total spend on %s by %s%s over the past %d years: %s", company, agency, scope, req.LookbackPeriod, res)
	case company != "":
		target = fmt.Sprintf("Total spend on %s%s over the past %d years: %s", company, scope, req.LookbackPeriod, res)
	case agency != "":
		target = fmt.Sprintf("Total spend by %s%s over the past %d years: %s", agency, scope, req.LookbackPeriod, res)
	default:
		return "", "", false
	}

	return target, contextText, true
}

func buildSpendQueryRequest(prompt, sourceHint string, lookback int) (collector.SearchRequest, string, string, bool) {
	company, agency := parseSpendQuery(prompt)
	if company == "" && agency == "" {
		return collector.SearchRequest{}, "", "", false
	}
	if lookback <= 0 {
		lookback = 20
	}
	source := strings.TrimSpace(sourceHint)
	if source != "" {
		source = collector.CanonicalSourceID(source)
	}
	return collector.SearchRequest{
		Company:        company,
		Agency:         agency,
		Source:         source,
		LookbackPeriod: lookback,
	}, company, agency, true
}

func formatSpendPrefetchContext(source string, lookback int, total, company, agency string) string {
	scope := ""
	if strings.TrimSpace(source) != "" {
		scope = fmt.Sprintf(" (%s)", strings.ToUpper(source))
	}
	parts := []string{fmt.Sprintf("Prefetched spend%s over the last %d years: %s", scope, lookback, total)}
	if company != "" {
		parts = append(parts, fmt.Sprintf("company=%s", company))
	}
	if agency != "" {
		parts = append(parts, fmt.Sprintf("agency=%s", agency))
	}
	return strings.Join(parts, " | ")
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
	v = strings.TrimSpace(stripEdgeQuotes(v))
	v = strings.Trim(v, " ?!.\t\r\n")
	v = strings.Join(strings.Fields(v), " ")
	if v == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(v), "the ") {
		v = strings.TrimSpace(v[4:])
	}
	return v
}

var spendOnRegex = regexp.MustCompile(`(?i)(?:how\s+much\s+was\s+)?(?:spent|spend|spending)\s+(?:on|with)\s+([^?.!\n]+)`) // covers common spend phrasing

func extractPrefetchKeyword(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return ""
	}
	if matches := spendOnRegex.FindStringSubmatch(prompt); len(matches) == 2 {
		candidate := strings.TrimSpace(matches[1])
		candidate = strings.TrimSpace(stripEdgeQuotes(candidate))
		candidate = strings.TrimSuffix(candidate, "?")
		candidate = strings.TrimSpace(candidate)
		if candidate != "" {
			return candidate
		}
	}
	// fallback: if prompt short, try entire prompt without punctuation
	if len(prompt) <= 80 {
		cleaned := strings.Trim(prompt, " ?!.\t\r\n")
		if cleaned != "" {
			return cleaned
		}
	}
	return ""
}

func stripEdgeQuotes(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		switch r {
		case '"', '\'', '`', '“', '”', '‘', '’':
			return true
		}
		return false
	})
}

func buildSearchRequest(ctx context.Context, keyword, sourceHint string, lookback int) (collector.SearchRequest, bool) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return collector.SearchRequest{}, false
	}
	source := collector.CanonicalSourceID(strings.TrimSpace(sourceHint))
	if source == "" {
		source = collector.CanonicalSourceID(collector.DetectSourceFromText(keyword))
	}
	if lookback <= 0 {
		lookback = 20
	}
	search := collector.SearchRequest{
		Keyword:        keyword,
		Source:         source,
		LookbackPeriod: lookback,
	}
	opts := collector.EntityLookupOptions{Source: search.Source, Query: keyword, Limit: 1}
	if res, err := collector.FindCompaniesFromCatalog(ctx, opts); err == nil && len(res.Candidates) > 0 {
		top := res.Candidates[0]
		company := strings.TrimSpace(top.Name)
		if company == "" {
			company = strings.TrimSpace(top.Key)
		}
		search.Company = company
		if strings.TrimSpace(top.Source) != "" {
			search.Source = collector.CanonicalSourceID(top.Source)
		}
	}
	return search, true
}

func directAggregateResponse(prompt, sourceHint string, lookback int, useCache bool) (string, bool) {
	keyword := extractPrefetchKeyword(prompt)
	if keyword == "" {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	search, ok := buildSearchRequest(ctx, keyword, sourceHint, lookback)
	if !ok {
		return "", false
	}
	var (
		total string
		err   error
	)
	if useCache {
		total, err = runScrapeCached(ctx, search)
	} else {
		total, err = runScrape(ctx, search)
	}
	if err != nil || strings.TrimSpace(total) == "" {
		return "", false
	}
	target := keyword
	if strings.TrimSpace(search.Company) != "" {
		target = search.Company
	}
	scope := ""
	if search.Source != "" {
		scope = fmt.Sprintf(" (%s)", strings.ToUpper(search.Source))
	}
	result := fmt.Sprintf("Total spend on %s%s over the past %d years: %s", target, scope, search.LookbackPeriod, total)
	return result, true
}

type LLMRequest struct {
	Prompt         string          `json:"prompt"`
	Model          string          `json:"model,omitempty"`
	Source         string          `json:"source,omitempty"`
	Agent          *bool           `json:"agent,omitempty"`
	MCPConfig      json.RawMessage `json:"mcpConfig,omitempty"`
	LookbackPeriod int             `json:"lookbackPeriod,omitempty"`
	UseCache       *bool           `json:"useCache,omitempty"`
}

type LLMResponse struct {
	Result  string `json:"result"`
	Context string `json:"context,omitempty"`
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

type agentDirective struct {
	Tool      string         `json:"tool,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Final     string         `json:"final,omitempty"`
}

func runLLMAgent(ctx context.Context, client llms.Model, userPrompt, sourceHint string, lookback int, useCache bool, mcpContext string) (string, error) {
	state := map[string]any{}

	source := strings.TrimSpace(sourceHint)
	if source == "" {
		source = collector.DetectSourceFromText(userPrompt)
	}
	source = collector.CanonicalSourceID(source)
	if strings.TrimSpace(sourceHint) == "" {
		// If we didn't get an explicit hint and no match, keep it empty.
		if collector.DetectSourceFromText(userPrompt) == "" {
			source = ""
		}
	}
	state["source"] = source

	maxSteps := 8
	transcript := strings.Builder{}
	transcript.WriteString("User prompt:\n")
	transcript.WriteString(userPrompt)
	transcript.WriteString("\n\n")
	if mcpContext != "" {
		transcript.WriteString("MCP config (informational; tools are executed locally by the server):\n")
		transcript.WriteString(mcpContext)
		transcript.WriteString("\n\n")
	}

	toolsDescription := agentToolsDescription(lookback)

	for step := 0; step < maxSteps; step++ {
		prompt := fmt.Sprintf("%s\n\nCurrent state (JSON): %s\n\nConversation so far:\n%s\n\nReturn ONLY a JSON object for the next action. Either {\"tool\":\"...\",\"arguments\":{...}} or {\"final\":\"...\"}. No markdown.", toolsDescription, mustJSON(state), transcript.String())
		raw, err := generateFromPrompt(ctx, client, prompt)
		if err != nil {
			return "", err
		}
		dir, parseErr := parseAgentDirective(raw)
		if parseErr != nil {
			// Give the model one corrective attempt.
			transcript.WriteString("Agent parse error: ")
			transcript.WriteString(parseErr.Error())
			transcript.WriteString("\n")
			continue
		}
		if strings.TrimSpace(dir.Final) != "" {
			return strings.TrimSpace(dir.Final), nil
		}
		tool := strings.TrimSpace(dir.Tool)
		if tool == "" {
			transcript.WriteString("Agent error: missing tool name\n")
			continue
		}
		out, err := executeAgentTool(ctx, tool, dir.Arguments, lookback, useCache, state)
		if err != nil {
			transcript.WriteString(fmt.Sprintf("Tool %s error: %v\n", tool, err))
			continue
		}
		transcript.WriteString("Tool call: ")
		transcript.WriteString(tool)
		transcript.WriteString("\nArguments: ")
		transcript.WriteString(mustJSON(dir.Arguments))
		transcript.WriteString("\nResult: ")
		transcript.WriteString(mustJSON(out))
		transcript.WriteString("\n\n")
	}

	return "", fmt.Errorf("agent exceeded max steps")
}

func agentToolsDescription(defaultLookback int) string {
	return fmt.Sprintf(strings.TrimSpace(`You are a tool-using agent for Australian government contract spend analysis.

Available tools:

1) identify_jurisdiction
  - arguments: {"text": string}
  - returns: {"source": string, "evidence": string}
	- source is one of: federal|nsw|vic|qld|sa|wa (or empty)

2) find_companies
  - arguments: {"source": string (optional), "query": string (optional), "limit": int (optional)}
  - returns: {"catalogAvailable": bool, "evidence": string, "candidates": [{"source": string, "name": string, "key": string, "rows": number}]}

3) find_agencies
  - arguments: {"source": string (optional), "query": string (optional), "limit": int (optional)}
  - returns: same shape as find_companies

4) aggregate_contracts
  - arguments: {"keyword": string, "company": string (optional), "agency": string (optional), "source": string (optional), "startDate": string (optional), "endDate": string (optional), "dateType": string (optional), "lookbackPeriod": int (optional)}
  - returns: {"total": string, "source": string}

Rules:
- Always use JSON only.
- Prefer calling identify_jurisdiction first when source is not clear.
- Use find_companies/find_agencies to resolve ambiguous names before aggregating.
- When calling aggregate_contracts, use lookbackPeriod default %d if not specified.
`), defaultLookback)
}

func executeAgentTool(ctx context.Context, name string, args map[string]any, defaultLookback int, useCache bool, state map[string]any) (map[string]any, error) {
	if args == nil {
		args = map[string]any{}
	}

	getString := func(key string) string {
		v, ok := args[key]
		if !ok || v == nil {
			return ""
		}
		s, ok := v.(string)
		if ok {
			return strings.TrimSpace(s)
		}
		return strings.TrimSpace(fmt.Sprintf("%v", v))
	}
	getInt := func(key string, fallback int) int {
		v, ok := args[key]
		if !ok || v == nil {
			return fallback
		}
		switch t := v.(type) {
		case float64:
			return int(t)
		case int:
			return t
		case string:
			parsed, err := strconv.Atoi(strings.TrimSpace(t))
			if err == nil {
				return parsed
			}
		}
		return fallback
	}

	// Allow tools to default to previously detected source.
	stateSource, _ := state["source"].(string)
	defaultSource := strings.TrimSpace(stateSource)

	switch name {
	case "identify_jurisdiction":
		text := getString("text")
		if text == "" {
			return nil, fmt.Errorf("text is required")
		}
		src, evidence := collector.DetectSourceFromTextWithEvidence(text)
		if src != "" {
			state["source"] = src
		}
		return map[string]any{"source": src, "evidence": evidence}, nil
	case "find_companies":
		src := getString("source")
		if src == "" {
			src = defaultSource
		}
		res, err := collector.FindCompaniesFromCatalog(ctx, collector.EntityLookupOptions{Source: src, Query: getString("query"), Limit: getInt("limit", 10)})
		if err != nil {
			return nil, err
		}
		return map[string]any{"catalogAvailable": res.CatalogAvailable, "evidence": res.Evidence, "candidates": res.Candidates}, nil
	case "find_agencies":
		src := getString("source")
		if src == "" {
			src = defaultSource
		}
		res, err := collector.FindAgenciesFromCatalog(ctx, collector.EntityLookupOptions{Source: src, Query: getString("query"), Limit: getInt("limit", 10)})
		if err != nil {
			return nil, err
		}
		return map[string]any{"catalogAvailable": res.CatalogAvailable, "evidence": res.Evidence, "candidates": res.Candidates}, nil
	case "aggregate_contracts":
		keyword := getString("keyword")
		if keyword == "" {
			return nil, fmt.Errorf("keyword is required")
		}
		src := getString("source")
		if src == "" {
			src = defaultSource
		}
		src = collector.CanonicalSourceID(src)
		if strings.TrimSpace(getString("source")) == "" {
			// If omitted and we had no detected value, allow empty to mean "auto" for collector.
			if defaultSource == "" {
				src = ""
			}
		}

		start, err := parseRequestDate(getString("startDate"))
		if err != nil {
			return nil, fmt.Errorf("invalid startDate: %w", err)
		}
		end, err := parseRequestDate(getString("endDate"))
		if err != nil {
			return nil, fmt.Errorf("invalid endDate: %w", err)
		}

		lb := getInt("lookbackPeriod", defaultLookback)
		if lb <= 0 {
			lb = defaultLookback
		}

		search := collector.SearchRequest{
			Keyword:        keyword,
			Company:        getString("company"),
			Agency:         getString("agency"),
			Source:         src,
			StartDate:      start,
			EndDate:        end,
			DateType:       getString("dateType"),
			LookbackPeriod: lb,
		}

		var total string
		if useCache {
			total, err = runScrapeCached(ctx, search)
		} else {
			total, err = runScrape(ctx, search)
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"total": total, "source": src}, nil
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

func parseAgentDirective(modelText string) (agentDirective, error) {
	trimmed := strings.TrimSpace(modelText)
	if trimmed == "" {
		return agentDirective{}, fmt.Errorf("empty model response")
	}
	obj, err := extractFirstJSONObject(trimmed)
	if err != nil {
		return agentDirective{}, err
	}
	var dir agentDirective
	if err := json.Unmarshal([]byte(obj), &dir); err != nil {
		return agentDirective{}, err
	}
	return dir, nil
}

func extractFirstJSONObject(s string) (string, error) {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", fmt.Errorf("no json object found")
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			continue
		}
		if c == '{' {
			depth++
		}
		if c == '}' {
			depth--
			if depth == 0 {
				return s[start : i+1], nil
			}
		}
	}
	return "", fmt.Errorf("unterminated json object")
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
