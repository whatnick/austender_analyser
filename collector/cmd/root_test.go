package cmd

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseDateFlag(t *testing.T) {
	date, err := parseDateFlag("2024-01-02")
	require.NoError(t, err)
	require.Equal(t, time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), date)
}
