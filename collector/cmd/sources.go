package cmd

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

const defaultSourceID = "federal"

// Source represents a provider capable of fulfilling a SearchRequest.
type Source interface {
	ID() string
	Run(ctx context.Context, req SearchRequest) (string, error)
}

var sourceRegistry = make(map[string]Source)

func registerSource(src Source) {
	if src == nil {
		return
	}
	id := normalizeSourceID(src.ID())
	if id == "" {
		return
	}
	sourceRegistry[id] = src
}

func resolveSource(id string) (Source, error) {
	normalized := normalizeSourceID(id)
	if normalized == "" {
		normalized = defaultSourceID
	}
	src, ok := sourceRegistry[normalized]
	if !ok {
		var keys []string
		for key := range sourceRegistry {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		return nil, fmt.Errorf("unknown source %q; available: %s", normalized, strings.Join(keys, ", "))
	}
	return src, nil
}

func normalizeSourceID(id string) string {
	cleaned := normalizeLooseSourceText(id)
	if cleaned == "" {
		return defaultSourceID
	}
	if mapped, ok := sourceSynonyms[cleaned]; ok {
		return mapped
	}
	return cleaned
}

// CanonicalSourceID maps loose jurisdiction identifiers (e.g. "Victoria", "Federal Government")
// onto the canonical source IDs used by the collector (e.g. "vic", "federal").
//
// It is safe to call this for request inputs; unknown values are returned normalized.
func CanonicalSourceID(id string) string {
	return normalizeSourceID(id)
}

// DetectSourceFromText attempts to identify a source/jurisdiction from free-form text.
//
// This is a deterministic heuristic (no regex): it prefers longer phrase matches first,
// then falls back to exact token matches for short IDs.
func DetectSourceFromText(text string) string {
	lower := strings.ToLower(text)
	for _, phrase := range sourcePhraseMatches {
		if strings.Contains(lower, phrase.phrase) {
			return phrase.sourceID
		}
	}
	for _, tok := range tokenize(lower) {
		if mapped, ok := sourceSynonyms[tok]; ok {
			return mapped
		}
	}
	return ""
}

// DetectSourceFromTextWithEvidence returns the detected canonical source ID and a short
// explanation string describing how it was detected.
//
// Evidence formats:
// - "phrase matched: <phrase>"
// - "token matched: <token>"
// - "no match"
func DetectSourceFromTextWithEvidence(text string) (sourceID string, evidence string) {
	lower := strings.ToLower(text)
	for _, phrase := range sourcePhraseMatches {
		if strings.Contains(lower, phrase.phrase) {
			return phrase.sourceID, fmt.Sprintf("phrase matched: %s", phrase.phrase)
		}
	}
	for _, tok := range tokenize(lower) {
		if mapped, ok := sourceSynonyms[tok]; ok {
			return mapped, fmt.Sprintf("token matched: %s", tok)
		}
	}
	return "", "no match"
}

type sourcePhraseMatch struct {
	phrase   string
	sourceID string
}

// Longer phrases first to avoid partial/ambiguous matches.
var sourcePhraseMatches = []sourcePhraseMatch{
	{phrase: "western australian government", sourceID: "wa"},
	{phrase: "western australia", sourceID: "wa"},
	{phrase: "south australian government", sourceID: "sa"},
	{phrase: "south australia", sourceID: "sa"},
	{phrase: "victorian government", sourceID: "vic"},
	{phrase: "victoria", sourceID: "vic"},
	{phrase: "new south wales", sourceID: "nsw"},
	{phrase: "nsw government", sourceID: "nsw"},
	{phrase: "australian government", sourceID: "federal"},
	{phrase: "federal government", sourceID: "federal"},
	{phrase: "commonwealth", sourceID: "federal"},
	{phrase: "austender", sourceID: "federal"},
}

// sourceSynonyms maps normalized identifiers to canonical source IDs.
var sourceSynonyms = map[string]string{
	"federal":               "federal",
	"commonwealth":          "federal",
	"austender":             "federal",
	"australian government": "federal",
	"federal government":    "federal",

	"nsw":             "nsw",
	"new south wales": "nsw",
	"nsw government":  "nsw",

	"vic":                  "vic",
	"victoria":             "vic",
	"victorian government": "vic",

	"sa":              "sa",
	"south australia": "sa",
	"sa government":   "sa",

	"wa":                            "wa",
	"western australia":             "wa",
	"wa government":                 "wa",
	"western australian government": "wa",
}

func normalizeLooseSourceText(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return ""
	}
	// Replace common separators with spaces.
	var b strings.Builder
	b.Grow(len(v))
	lastSpace := false
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			lastSpace = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			lastSpace = false
		case r == ' ' || r == '\t' || r == '\n' || r == '_' || r == '-' || r == '/' || r == '.':
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		default:
			// drop other punctuation
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		}
	}
	out := strings.TrimSpace(b.String())
	out = strings.Join(strings.Fields(out), " ")
	return out
}

func tokenize(v string) []string {
	v = strings.ToLower(v)
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		out = append(out, b.String())
		b.Reset()
	}
	for _, r := range v {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return out
}

// ensureSourcesRegistered guards against init-order surprises and makes sure all known
// sources are present before we render help or resolve a source at runtime.
func ensureSourcesRegistered() {
	registerSource(newFederalSource())
	registerSource(newVicSource())
	registerSource(newNswSource())
	registerSource(newSaSource())
	registerSource(newWaSource())
}

func AvailableSources() []string {
	ensureSourcesRegistered()
	var keys []string
	for key := range sourceRegistry {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func init() {
	ensureSourcesRegistered()
}

type placeholderSource struct {
	id string
}

func (p placeholderSource) ID() string { return p.id }

func (p placeholderSource) Run(ctx context.Context, _ SearchRequest) (string, error) {
	return "", fmt.Errorf("source %s not implemented yet", p.id)
}

func newPlaceholderSource(id string) Source {
	return placeholderSource{id: normalizeSourceID(id)}
}
