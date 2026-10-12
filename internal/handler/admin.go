package handler

import (
	"log/slog"
	"net/http"

	"delivery-service/internal/middleware"
	"delivery-service/internal/service"
)

// AdminHandler serves manual recovery when debug routing is enabled.
type AdminHandler struct {
	calculationService *service.CalculationService
	logger             *slog.Logger
}

// NewAdminHandler creates the debug recovery handler.
func NewAdminHandler(
	calculationService *service.CalculationService,
	logger *slog.Logger,
) *AdminHandler {
	return &AdminHandler{
		calculationService: calculationService,
		logger:             logger,
	}
}

// CalculatePending godoc
// @Summary Запустить расчёт вручную
// @Description Вручную восстанавливает старые pending через WorkerService и дописывает отсутствующие Mongo-логи готовых расчётов без пересчёта. processed — количество pending. Только DEBUG_MODE=true.
// @Tags admin
// @Produce json
// @Success 200 {object} map[string]int
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/admin/calculate-pending [post]
func (h *AdminHandler) CalculatePending(
	w http.ResponseWriter,
	r *http.Request,
) {

	processed, err := h.calculationService.CalculatePending(
		r.Context(),
	)
	if err != nil {
		h.logger.Error(
			"failed to calculate pending packages",
			"trace_id", middleware.TraceID(r.Context()),
			"error", err,
		)

		if err := writeError(
			w,
			http.StatusInternalServerError,
			ErrCodeCalculation,
			"Failed to calculate pending packages",
			nil,
		); err != nil {
			h.logger.Error("failed to write error response", "trace_id", middleware.TraceID(r.Context()), "error", err)
		}
		return
	}

	if err := writeJSON(
		w,
		http.StatusOK,
		map[string]any{
			"processed": processed,
		},
	); err != nil {
		h.logger.Error("failed to write calculation response", "trace_id", middleware.TraceID(r.Context()), "error", err)
	}
}
