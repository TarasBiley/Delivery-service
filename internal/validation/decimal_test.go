package validation

import (
	"encoding/json"
	"github.com/go-playground/validator/v10"
	"testing"
)

func TestMaxDecimal(t *testing.T) {
	v := validator.New()
	if err := Register(v); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		value float64
		tag   string
		ok    bool
	}{
		{"weight integer", 1, "max_decimal=3", true}, {"weight one", 1.2, "max_decimal=3", true}, {"weight three", 1.234, "max_decimal=3", true}, {"weight four", 1.2345, "max_decimal=3", false},
		{"minimum weight", .001, "max_decimal=3", true}, {"below precision", .0001, "max_decimal=3", false}, {"maximum weight", 9999999.999, "max_decimal=3", true},
		{"money zero", 0, "max_decimal=2", true}, {"money one", 1.2, "max_decimal=2", true}, {"money two", 1.23, "max_decimal=2", true}, {"money three", 1.234, "max_decimal=2", false},
		{"cent", .01, "max_decimal=2", true}, {"subcent", .001, "max_decimal=2", false}, {"maximum money", 9999999999.99, "max_decimal=2", true}, {"negative precision", -1.234, "max_decimal=2", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := v.Var(tc.value, tc.tag)
			if (err == nil) != tc.ok {
				t.Fatalf("value=%v error=%v want valid=%v", tc.value, err, tc.ok)
			}
		})
	}
}

func TestExactRequestDecimals(t *testing.T) {
	v := validator.New()
	if err := Register(v); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		value, tag string
		valid      bool
	}{
		{"9999999999.99", "decimal_gte=0,decimal_lte=9999999999.99,max_decimal=2", true},
		{"9999999999.990000000000001", "decimal_gte=0,decimal_lte=9999999999.99,max_decimal=2", false},
		{"1.5000", "decimal_gte=0.001,decimal_lte=9999999.999,max_decimal=3", true},
		{"1.5000000000000001", "max_decimal=3", false},
		{"1e1000000000", "decimal_lte=9999999.999", false}, {"1e-1000000000", "max_decimal=3", false},
		{"1e0", "decimal_gte=0.001,max_decimal=3", true},
	} {
		if err := v.Var(json.Number(tc.value), tc.tag); (err == nil) != tc.valid {
			t.Fatalf("%s: %v", tc.value, err)
		}
	}
}
