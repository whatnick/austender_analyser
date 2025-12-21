package cmd

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestParseWaMoney(t *testing.T) {
	tests := []struct {
		input    string
		expected decimal.Decimal
	}{
		{"$239,285", decimal.NewFromInt(239285)},
		{"$1,234,567.89", decimal.NewFromFloat(1234567.89)},
		{" 1000 ", decimal.NewFromInt(1000)},
		{"", decimal.Zero},
	}

	for _, tt := range tests {
		val, err := parseWaMoney(tt.input)
		require.NoError(t, err)
		require.True(t, tt.expected.Equal(val), "expected %v, got %v", tt.expected, val)
	}
}
