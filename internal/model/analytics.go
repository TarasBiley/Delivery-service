package model

// DailySumItem is an exact daily total grouped by package type.
type DailySumItem struct {
	PackageTypeID   int    `json:"package_type_id"`
	PackageTypeName string `json:"package_type_name"`
	PackageCount    int64  `json:"package_count"`
	DeliveryCostRUB string `json:"delivery_cost_rub"`
}

// DailySumResponse reports one UTC day for the current session.
type DailySumResponse struct {
	Date string         `json:"date"`
	Data []DailySumItem `json:"data"`
}
