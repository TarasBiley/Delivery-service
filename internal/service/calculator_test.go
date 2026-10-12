package service

import (
	"github.com/shopspring/decimal"
	"testing"
)

func TestCalculator(t *testing.T) {
	for _, tc := range []struct{ name, weight, value, rate, want string }{
		{"ordinary", "2", "100", "90", "180"}, {"round half up", "0.01", "0", "1", "0.01"}, {"round down", "0.001", "0", "1", "0"},
		{"small", "0.001", "0.01", "100", "0.06"}, {"large", "9999999.999", "9999999999.99", "100", "10499999999.94"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := CalculateDeliveryCost(decimal.RequireFromString(tc.weight), decimal.RequireFromString(tc.value), decimal.RequireFromString(tc.rate))
			if !got.Equal(decimal.RequireFromString(tc.want)) {
				t.Fatalf("cost=%s want=%s", got, tc.want)
			}

		})
	}
}
