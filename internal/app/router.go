package app

import (
	"net/http"

	_ "delivery-service/docs"
	"delivery-service/internal/handler"
	"delivery-service/web"

	httpSwagger "github.com/swaggo/http-swagger/v2"
)

type routerHandlers struct {
	carrierHandler     *handler.CarrierHandler
	analyticsHandler   *handler.AnalyticsHandler
	packageHandler     *handler.PackageHandler
	packageTypeHandler *handler.PackageTypeHandler
	adminHandler       *handler.AdminHandler
	systemHandler      *handler.SystemHandler
	healthHandler      *handler.HealthHandler
}

func newRouter(
	handlers routerHandlers,
	debugMode bool,
) *http.ServeMux {
	mux := http.NewServeMux()

	mux.Handle("/swagger/", httpSwagger.WrapHandler)

	// Debug routes
	if debugMode {
		mux.HandleFunc(
			"POST /api/v1/admin/calculate-pending",
			handlers.adminHandler.CalculatePending,
		)

		mux.HandleFunc(
			"/api/v1/admin/calculate-pending",
			handlers.systemHandler.MethodNotAllowed("POST"),
		)

	}

	// Package types
	mux.HandleFunc(
		"GET /api/v1/package-types",
		handlers.packageTypeHandler.GetAll,
	)

	mux.HandleFunc(
		"/api/v1/package-types",
		handlers.systemHandler.MethodNotAllowed("GET, HEAD"),
	)

	// Packages
	mux.HandleFunc(
		"POST /api/v1/packages",
		handlers.packageHandler.Create,
	)

	mux.HandleFunc(
		"GET /api/v1/packages",
		handlers.packageHandler.GetAll,
	)

	mux.HandleFunc(
		"/api/v1/packages",
		handlers.systemHandler.MethodNotAllowed("GET, HEAD, POST"),
	)

	mux.HandleFunc(
		"GET /api/v1/packages/{package_id}",
		handlers.packageHandler.GetByID,
	)

	mux.HandleFunc(
		"/api/v1/packages/{package_id}",
		handlers.systemHandler.MethodNotAllowed("GET, HEAD"),
	)
	mux.HandleFunc("POST /api/v1/packages/{package_id}/assign-carrier", handlers.carrierHandler.AssignCarrier)
	mux.HandleFunc("/api/v1/packages/{package_id}/assign-carrier", handlers.systemHandler.MethodNotAllowed("POST"))
	mux.HandleFunc("GET /api/v1/analytics/daily-sum", handlers.analyticsHandler.DailySum)
	mux.HandleFunc("/api/v1/analytics/daily-sum", handlers.systemHandler.MethodNotAllowed("GET, HEAD"))
	mux.Handle("GET /{$}", web.Handler())
	mux.Handle("GET /static/", http.StripPrefix("/static/", web.Handler()))
	// Health
	mux.HandleFunc(
		"GET /health",
		handlers.healthHandler.Check,
	)

	mux.HandleFunc(
		"/health",
		handlers.systemHandler.MethodNotAllowed("GET, HEAD"),
	)
	// 404
	mux.HandleFunc(
		"/",
		handlers.systemHandler.NotFound,
	)

	return mux
}
