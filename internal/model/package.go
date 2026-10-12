package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// CreatePackageRequest retains JSON decimal tokens until exact normalization.
type CreatePackageRequest struct {
	Name            string          `json:"name" validate:"required"`
	WeightKG        RequestDecimal  `json:"weight_kg" swaggertype:"number" validate:"required,decimal_gte=0.001,decimal_lte=9999999.999,max_decimal=3"`
	PackageTypeID   int             `json:"package_type_id" validate:"required,gt=0"`
	ContentValueUSD *RequestDecimal `json:"content_value_usd" swaggertype:"number" validate:"required,decimal_gte=0,decimal_lte=9999999999.99,max_decimal=2"`
	IdempotencyKey  string          `json:"-" swaggerignore:"true" validate:"-"`
}

type Package struct {
	CarrierID       *int64
	CalculatedAt    *time.Time
	USDToRUBRate    *decimal.Decimal
	PackageID       string
	SessionID       string
	Name            string
	WeightKG        float64
	PackageTypeID   int
	ContentValueUSD float64
	DeliveryCostRUB *float64
	Status          string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type CreatePackageResponse struct {
	PackageID string    `json:"package_id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type PackageListItem struct {
	CarrierID       *int64           `json:"carrier_id"`
	CalculatedAt    *time.Time       `json:"-"`
	USDToRUBRate    *decimal.Decimal `json:"usd_to_rub_rate" swaggertype:"string"`
	PackageID       string           `json:"package_id"`
	Name            string           `json:"name"`
	WeightKG        float64          `json:"weight_kg"`
	PackageType     PackageType      `json:"package_type"`
	ContentValueUSD float64          `json:"content_value_usd"`
	DeliveryCostRUB *float64         `json:"delivery_cost_rub"`
	Status          string           `json:"status"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
}

type PackageListFilter struct {
	Page          int
	Limit         int
	PackageTypeID *int
	Calculated    *bool
}

type Pagination struct {
	Page       int `json:"page"`
	Limit      int `json:"limit"`
	Total      int `json:"total"`
	TotalPages int `json:"total_pages"`
}

type PackageListResponse struct {
	Data       []PackageListItem `json:"data"`
	Pagination Pagination        `json:"pagination"`
}

type PendingPackage struct {
	PackageID       string
	WeightKG        decimal.Decimal
	ContentValueUSD decimal.Decimal
}

// AssignCarrierRequest names the first carrier to claim a package.
type AssignCarrierRequest struct {
	CarrierID int64 `json:"carrier_id" validate:"required,gt=0"`
}

// AssignCarrierResponse returns the winning carrier.
type AssignCarrierResponse struct {
	CarrierID int64 `json:"carrier_id"`
}
