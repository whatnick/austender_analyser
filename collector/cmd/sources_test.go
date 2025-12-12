package cmd

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveSourceDefaults(t *testing.T) {
	src, err := resolveSource("")
	require.NoError(t, err)
	require.Equal(t, defaultSourceID, src.ID())
}

func TestResolveSourceUnknown(t *testing.T) {
	_, err := resolveSource("unknown-source")
	require.Error(t, err)
}

func TestPlaceholderSource(t *testing.T) {
	src, err := resolveSource("vic")
	require.NoError(t, err)

	_, runErr := src.Run(context.Background(), SearchRequest{Source: "vic"})
	require.Error(t, runErr)
}
