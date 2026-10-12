package messaging

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"testing"
	"time"
)

func validMessage() PackageMessage {
	return PackageMessage{SchemaVersion: 1, PackageID: uuid.NewString(), SessionID: uuid.NewString(), Name: "Phone", WeightKG: "1.234", PackageTypeID: 2, ContentValueUSD: "9999999999.99", CreatedAt: time.Now().UTC(), TraceID: uuid.NewString()}
}
func TestPackageMessageValidation(t *testing.T) {
	v, err := NewPackageMessageValidator()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*PackageMessage)
		valid  bool
	}{
		{"valid", func(*PackageMessage) {}, true}, {"zero content", func(m *PackageMessage) { m.ContentValueUSD = "0" }, true},
		{"unsupported version", func(m *PackageMessage) { m.SchemaVersion = 2 }, false},
		{"package UUID", func(m *PackageMessage) { m.PackageID = "bad" }, false}, {"session UUID", func(m *PackageMessage) { m.SessionID = "bad" }, false}, {"trace UUID", func(m *PackageMessage) { m.TraceID = "bad" }, false},
		{"name", func(m *PackageMessage) { m.Name = "" }, false}, {"created_at", func(m *PackageMessage) { m.CreatedAt = time.Time{} }, false}, {"type", func(m *PackageMessage) { m.PackageTypeID = 0 }, false},
		{"missing weight", func(m *PackageMessage) { m.WeightKG = "" }, false}, {"missing value", func(m *PackageMessage) { m.ContentValueUSD = "" }, false},
		{"invalid decimal", func(m *PackageMessage) { m.ContentValueUSD = "NaN" }, false}, {"negative value", func(m *PackageMessage) { m.ContentValueUSD = "-1" }, false},
		{"weight precision", func(m *PackageMessage) { m.WeightKG = "1.2345" }, false}, {"money precision", func(m *PackageMessage) { m.ContentValueUSD = "1.234" }, false},
		{"weight minimum", func(m *PackageMessage) { m.WeightKG = "0.0001" }, false}, {"weight max", func(m *PackageMessage) { m.WeightKG = "10000000" }, false},
		{"money max", func(m *PackageMessage) { m.ContentValueUSD = "10000000000" }, false}, {"huge exponent", func(m *PackageMessage) { m.WeightKG = "1e999999999" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := validMessage()
			tc.change(&m)
			err := m.Validate(v)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
}
func TestPackageMessageJSONPreservesDecimals(t *testing.T) {
	m := validMessage()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err = json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["weight_kg"] != "1.234" || wire["content_value_usd"] != "9999999999.99" {
		t.Fatalf("wire=%s", data)
	}
	var got PackageMessage
	if err = json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !decimal.RequireFromString(got.ContentValueUSD).Equal(decimal.RequireFromString(m.ContentValueUSD)) || !got.CreatedAt.Equal(m.CreatedAt) {
		t.Fatal("round trip changed message")
	}
	if err = json.Unmarshal([]byte(`{"weight_kg":"invalid"}`), &got); err != nil {
		t.Fatal(err)
	}
	v, _ := NewPackageMessageValidator()
	if got.Validate(v) == nil {
		t.Fatal("invalid decimal accepted")
	}
}
