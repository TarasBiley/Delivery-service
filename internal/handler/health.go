package handler

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"delivery-service/internal/middleware"
)

// HealthCheck probes one required dependency without business side effects.
type HealthCheck func(context.Context) error

// HealthHandler reports readiness for all mandatory dependencies.
type HealthHandler struct {
	checks map[string]HealthCheck
	logger *slog.Logger
}

// NewHealthHandler creates readiness probes with a shared request deadline.
func NewHealthHandler(checks map[string]HealthCheck, logger *slog.Logger) *HealthHandler {
	return &HealthHandler{checks: checks, logger: logger}
}

type healthResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

// Check godoc
// @Summary Проверить состояние сервиса
// @Description Проверяет PostgreSQL, Redis, MongoDB, RabbitMQ и consumer
// @Tags system
// @Produce json
// @Success 200 {object} healthResponse
// @Failure 503 {object} healthResponse
// @Router /health [get]
func (h *HealthHandler) Check(
	w http.ResponseWriter,
	r *http.Request,
) {
	traceID := middleware.TraceID(r.Context())

	checks := make(map[string]string, len(h.checks))
	healthy := true
	// A single deadline bounds the whole readiness request.
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	for name, check := range h.checks {
		checks[name] = "ok"
		if err := check(ctx); err != nil {
			checks[name] = "error"
			healthy = false
			h.logger.Error("health dependency unavailable", "dependency", name, "trace_id", traceID, "error", err)
		}
	}

	status := http.StatusOK

	response := healthResponse{
		Status: "ok",
		Checks: checks,
	}

	if !healthy {
		status = http.StatusServiceUnavailable
		response.Status = "unhealthy"
	}

	if err := writeJSON(
		w,
		status,
		response,
	); err != nil {
		h.logger.Error(
			"failed to write health response",
			"trace_id", traceID,
			"error", err,
		)
	}
}
