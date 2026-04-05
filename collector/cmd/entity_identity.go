package cmd

import (
	"sort"
	"strings"
)

type indexedEntity struct {
	displayName  string
	partitionKey string
	identifier   string
	tokens       []string
}

type compiledEntityFilter struct {
	raw          string
	lower        string
	partitionKey string
	identifier   string
	tokens       []string
}

var entityTokenAliases = map[string]string{
	"dept":  "department",
	"depts": "department",
	"govt":  "government",
	"svc":   "services",
	"svcs":  "services",
	"tech":  "technology",
}

var commonEntityIgnoredTokens = map[string]struct{}{
	"the": {},
	"of":  {},
	"and": {},
	"for": {},
	"to":  {},
}

var companyIgnoredTokens = map[string]struct{}{
	"pty":         {},
	"ltd":         {},
	"limited":     {},
	"proprietary": {},
	"inc":         {},
	"llc":         {},
	"plc":         {},
	"corp":        {},
	"corporation": {},
}

func compileEntityFilter(kind, query string) compiledEntityFilter {
	query = strings.TrimSpace(query)
	return compiledEntityFilter{
		raw:          query,
		lower:        strings.ToLower(query),
		partitionKey: sanitizePartitionComponent(query),
		identifier:   canonicalEntityIdentifier(kind, query),
		tokens:       entityTokens(kind, query),
	}
}

func (f compiledEntityFilter) active() bool {
	return f.raw != ""
}

func (f compiledEntityFilter) matches(entity indexedEntity) bool {
	if !f.active() {
		return true
	}
	if f.identifier != "" && entity.identifier != "" && f.identifier == entity.identifier {
		return true
	}
	if f.lower != "" && entity.displayName != "" && strings.Contains(strings.ToLower(entity.displayName), f.lower) {
		return true
	}
	if f.partitionKey != "" && entity.partitionKey != "" && strings.Contains(entity.partitionKey, f.partitionKey) {
		return true
	}
	if len(f.tokens) == 0 || len(entity.tokens) == 0 {
		return false
	}
	entityTokens := make(map[string]struct{}, len(entity.tokens))
	for _, tok := range entity.tokens {
		entityTokens[tok] = struct{}{}
	}
	for _, tok := range f.tokens {
		if _, ok := entityTokens[tok]; !ok {
			return false
		}
	}
	return true
}

func (f compiledEntityFilter) score(entity indexedEntity) int {
	if !f.active() {
		return 0
	}
	score := 0
	if f.identifier != "" && entity.identifier != "" && f.identifier == entity.identifier {
		score += 1000
	}
	if f.lower != "" && entity.displayName != "" {
		display := strings.ToLower(strings.TrimSpace(entity.displayName))
		switch {
		case display == f.lower:
			score += 300
		case strings.Contains(display, f.lower):
			score += 120
		}
	}
	if f.partitionKey != "" && entity.partitionKey != "" {
		switch {
		case entity.partitionKey == f.partitionKey:
			score += 180
		case strings.Contains(entity.partitionKey, f.partitionKey):
			score += 80
		}
	}
	if len(f.tokens) > 0 && len(entity.tokens) > 0 {
		entityTokens := make(map[string]struct{}, len(entity.tokens))
		for _, tok := range entity.tokens {
			entityTokens[tok] = struct{}{}
		}
		matched := 0
		for _, tok := range f.tokens {
			if _, ok := entityTokens[tok]; ok {
				matched++
			}
		}
		if matched == len(f.tokens) {
			score += 200 + (matched * 10)
		} else {
			score += matched * 5
		}
	}
	return score
}

func canonicalEntityIdentifier(kind, value string) string {
	tokens := entityTokens(kind, value)
	if len(tokens) == 0 {
		key := sanitizePartitionComponent(value)
		if key == "unknown" {
			return ""
		}
		return key
	}
	ordered := append([]string(nil), tokens...)
	sort.Strings(ordered)
	return strings.Join(ordered, "_")
}

func entityTokens(kind, value string) []string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return nil
	}
	value = strings.NewReplacer("&", " and ", "@", " at ").Replace(value)
	rawTokens := tokenize(value)
	if len(rawTokens) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(rawTokens))
	out := make([]string, 0, len(rawTokens))
	for _, raw := range rawTokens {
		tok := normalizeEntityToken(raw)
		if tok == "" {
			continue
		}
		if _, ignored := commonEntityIgnoredTokens[tok]; ignored {
			continue
		}
		if kind == "company" && len(rawTokens) > 1 {
			if _, ignored := companyIgnoredTokens[tok]; ignored {
				continue
			}
		}
		if _, exists := seen[tok]; exists {
			continue
		}
		seen[tok] = struct{}{}
		out = append(out, tok)
	}
	if len(out) > 0 {
		return out
	}
	fallback := sanitizePartitionComponent(value)
	if fallback == "" || fallback == "unknown" {
		return nil
	}
	parts := strings.FieldsFunc(fallback, func(r rune) bool {
		return r == '_' || r == '-'
	})
	for _, part := range parts {
		if part == "" {
			continue
		}
		if _, exists := seen[part]; exists {
			continue
		}
		seen[part] = struct{}{}
		out = append(out, part)
	}
	return out
}

func normalizeEntityToken(tok string) string {
	tok = strings.TrimSpace(strings.ToLower(tok))
	if tok == "" {
		return ""
	}
	if mapped, ok := entityTokenAliases[tok]; ok {
		return mapped
	}
	return tok
}

func resolveIndexedEntity(kind, displayName, partitionKey, identifier string, tokens []string) indexedEntity {
	displayName = strings.TrimSpace(displayName)
	partitionKey = strings.TrimSpace(partitionKey)
	base := firstNonEmpty(displayName, partitionKey)
	if partitionKey == "" {
		partitionKey = sanitizePartitionComponent(base)
		if partitionKey == "unknown" {
			partitionKey = ""
		}
	}
	if len(tokens) == 0 {
		tokens = entityTokens(kind, base)
	}
	if identifier == "" {
		identifier = canonicalEntityIdentifier(kind, base)
	}
	if displayName == "" {
		displayName = partitionKey
	}
	return indexedEntity{displayName: displayName, partitionKey: partitionKey, identifier: identifier, tokens: tokens}
}

func preferredEntityName(current, candidate string) string {
	current = strings.TrimSpace(current)
	candidate = strings.TrimSpace(candidate)
	switch {
	case current == "":
		return candidate
	case candidate == "":
		return current
	case looksLikePartitionKey(current) && !looksLikePartitionKey(candidate):
		return candidate
	case !looksLikePartitionKey(current) && looksLikePartitionKey(candidate):
		return current
	case len(candidate) > len(current):
		return candidate
	default:
		return current
	}
}

func looksLikePartitionKey(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	return v == sanitizePartitionComponent(v) && !strings.Contains(v, " ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
