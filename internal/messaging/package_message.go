package messaging

import (
	"github.com/go-playground/validator/v10"
	"github.com/shopspring/decimal"
	"strings"
	"time"
)

const PackageMessageVersion = 1

// PackageMessage carries a versioned registration and the originating trace ID.
// Decimal strings keep the wire representation independent of float64 and
// decimal.MarshalJSONWithoutQuotes, a process-wide option.
type PackageMessage struct {
	SchemaVersion   int       `json:"schema_version" validate:"eq=1"`
	PackageID       string    `json:"package_id" validate:"required,uuid"`
	SessionID       string    `json:"session_id" validate:"required,uuid"`
	Name            string    `json:"name" validate:"required"`
	WeightKG        string    `json:"weight_kg" validate:"required,max=64,package_weight"`
	PackageTypeID   int       `json:"package_type_id" validate:"gt=0,lte=2147483647"`
	ContentValueUSD string    `json:"content_value_usd" validate:"required,max=64,package_value"`
	CreatedAt       time.Time `json:"created_at" validate:"required"`
	TraceID         string    `json:"trace_id" validate:"required,uuid"`
}

// NewPackageMessageValidator registers bounded exact decimal message rules.
func NewPackageMessageValidator() (*validator.Validate, error) {
	v := validator.New()
	rules := []struct {
		name, min, max string
		places         int
	}{
		{"package_weight", "0.001", "9999999.999", 3},
		{"package_value", "0", "9999999999.99", 2},
	}
	for _, rule := range rules {
		min, max := decimal.RequireFromString(rule.min), decimal.RequireFromString(rule.max)
		if err := v.RegisterValidation(rule.name, func(fl validator.FieldLevel) bool {
			text := fl.Field().String()
			// Fixed decimal notation bounds parsing and rejects unbounded exponents.
			if len(text) > 64 || strings.ContainsAny(text, "eE") {
				return false
			}
			d, err := decimal.NewFromString(text)
			return err == nil && d.GreaterThanOrEqual(min) && d.LessThanOrEqual(max) && d.Equal(d.Round(int32(rule.places)))
		}); err != nil {
			return nil, err
		}
	}
	return v, nil
}

func (m PackageMessage) Validate(v *validator.Validate) error { return v.Struct(m) }
