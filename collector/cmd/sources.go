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
	cleaned := strings.ToLower(strings.TrimSpace(id))
	if cleaned == "" {
		return defaultSourceID
	}
	return cleaned
}

// ensureSourcesRegistered guards against init-order surprises and makes sure all known
// sources are present before we render help or resolve a source at runtime.
func ensureSourcesRegistered() {
	registerSource(newFederalSource())
	registerSource(newVicSource())
	registerSource(newNswSource())
}

func availableSources() []string {
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
