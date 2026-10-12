package handler

import (
	"context"
	"delivery-service/internal/middleware"
	"delivery-service/internal/model"
	"delivery-service/internal/service"
	"errors"
	"log/slog"
	"net/http"
)

type analyticsService interface {
	DailySum(context.Context, string, string) (model.DailySumResponse, error)
}

// AnalyticsHandler serves exact session-scoped daily totals.
type AnalyticsHandler struct {
	service analyticsService
	logger  *slog.Logger
}

// NewAnalyticsHandler creates the analytics handler.
func NewAnalyticsHandler(s analyticsService, l *slog.Logger) *AnalyticsHandler {
	return &AnalyticsHandler{service: s, logger: l}
}

// DailySum reports one mandatory UTC calendar date.
// @Summary Сумма доставки за день по типам посылок
// @Description Только текущая session cookie. День в UTC, суммы — точные десятичные строки. Пустой день возвращает data: [].
// @Tags analytics
// @Produce json
// @Param date query string true "Дата UTC: YYYY-MM-DD"
// @Success 200 {object} model.DailySumResponse
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/analytics/daily-sum [get]
func (h *AnalyticsHandler) DailySum(w http.ResponseWriter, r *http.Request) {
	dates := r.URL.Query()["date"]
	date := ""
	if len(dates) == 1 {
		date = dates[0]
	}
	result, err := h.service.DailySum(r.Context(), middleware.SessionID(r.Context()), date)
	var writeErr error
	switch {
	case errors.Is(err, service.ErrInvalidDate):
		writeErr = writeError(w, 400, ErrCodeInvalidQuery, "date must be YYYY-MM-DD (UTC)", map[string]any{"date": "required real calendar date, exactly once"})
	case err != nil:
		h.logger.Error("daily analytics failed", "trace_id", middleware.TraceID(r.Context()), "error", err)
		writeErr = writeError(w, 500, ErrCodeInternal, "Failed to load daily analytics", nil)
	default:
		writeErr = writeJSON(w, 200, result)
	}
	if writeErr != nil {
		h.logger.Error("write analytics response", "trace_id", middleware.TraceID(r.Context()), "error", writeErr)
	}
}
