package cmd

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlaceholderSource(t *testing.T) {
	src := newPlaceholderSource(" VIC ")
	require.Equal(t, "vic", src.ID())
	_, err := src.Run(context.Background(), SearchRequest{})
	require.Error(t, err)
}
