package api

import (
	"testing"

	"github.com/stellar/go/amount"
	"github.com/stretchr/testify/assert"
)

func TestParse(t *testing.T) {
	testCases := []struct {
		floatStr  string
		wantInt64 int64
	}{
		{"1.0", int64(10000000)},
		{"1.1", int64(11000000)},
		{"1.1234567", int64(11234567)},
		{"0.00000071", int64(71)},
	}

	for _, k := range testCases {
		t.Run(k.floatStr, func(t *testing.T) {
			val, e := amount.ParseInt64(k.floatStr)
			if !assert.NoError(t, e) {
				return
			}
			assert.Equal(t, k.wantInt64, val)
		})
	}
}
