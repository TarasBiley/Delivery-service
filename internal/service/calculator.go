package service

import "github.com/shopspring/decimal"

// CalculateDeliveryCost is shared by worker and pending recovery; it has no I/O.
func CalculateDeliveryCost(weight, contentValue, rate decimal.Decimal) decimal.Decimal {
	return weight.Mul(decimal.New(5, -1)).Add(contentValue.Mul(decimal.New(1, -2))).Mul(rate).Round(2)
}
