package model

import (
	"github.com/shopspring/decimal"
	"time"
)

// CalculationRecord is the exact PostgreSQL representation used by the worker.
// Nullable result metadata also represents pre-worker historical records.
type CalculationRecord struct {
	PackageID       string
	SessionID       string
	Name            string
	PackageTypeID   int
	PackageTypeName string
	WeightKG        decimal.Decimal
	ContentValueUSD decimal.Decimal
	Status          string
	DeliveryCostRUB *decimal.Decimal
	USDToRUBRate    *decimal.Decimal
	CreatedAt       time.Time
	CalculatedAt    *time.Time
}

// CalculationLog couples a canonical result with the originating trace ID.
type CalculationLog struct {
	Package CalculationRecord
	TraceID string
}
