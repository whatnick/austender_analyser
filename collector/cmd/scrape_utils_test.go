package cmd

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestCleanNum(t *testing.T) {
	zero, _ := decimal.NewFromString("")
	assert.Equal(t, cleanNum("BlahBlah"), zero, "Arbitrary strings parse to zero")
}
