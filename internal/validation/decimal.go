package validation

import (
	"github.com/shopspring/decimal"
	"reflect"
	"strconv"
	"strings"

	"github.com/go-playground/validator/v10"
)

func Register(validate *validator.Validate) error {
	rules := map[string]validator.Func{
		"max_decimal": maxDecimalPlaces,
		"decimal_gte": func(fl validator.FieldLevel) bool {
			d, ok := exactDecimal(fl)
			bound, err := decimal.NewFromString(fl.Param())
			return ok && err == nil && d.GreaterThanOrEqual(bound)
		},
		"decimal_lte": func(fl validator.FieldLevel) bool {
			d, ok := exactDecimal(fl)
			bound, err := decimal.NewFromString(fl.Param())
			return ok && err == nil && d.LessThanOrEqual(bound)
		},
	}
	for name, rule := range rules {
		if err := validate.RegisterValidation(name, rule); err != nil {
			return err
		}
	}
	return nil
}

func maxDecimalPlaces(
	fl validator.FieldLevel,
) bool {
	maxPlaces, err := strconv.Atoi(fl.Param())
	if err != nil {
		return false
	}

	if fl.Field().Kind() == reflect.String {
		d, ok := exactDecimal(fl)
		return ok && d.Equal(d.Round(int32(maxPlaces)))
	}
	value := fl.Field().Float()

	text := strconv.FormatFloat(
		value,
		'f',
		-1,
		64,
	)

	dot := strings.IndexByte(text, '.')
	if dot == -1 {
		return true
	}

	decimalPlaces := len(text) - dot - 1

	return decimalPlaces <= maxPlaces
}

// exactDecimal bounds parsing and decimal exponents before arithmetic.
func exactDecimal(fl validator.FieldLevel) (decimal.Decimal, bool) {
	text := fl.Field().String()
	if len(text) > 64 {
		return decimal.Zero, false
	}
	if i := strings.IndexAny(text, "eE"); i >= 0 {
		exponent, err := strconv.Atoi(text[i+1:])
		if err != nil || exponent < -100 || exponent > 100 {
			return decimal.Zero, false
		}
	}
	d, err := decimal.NewFromString(text)
	return d, err == nil
}
