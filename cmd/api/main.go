package main

import (
	"delivery-service/internal/app"
	"os"
)

// @title Delivery Service API
// @version 1.0
// @description API для регистрации посылок и расчёта стоимости международной доставки.
func main() {
	os.Exit(app.Run())
}
