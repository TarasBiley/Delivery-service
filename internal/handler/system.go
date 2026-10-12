package handler

import (
	"log/slog"
	"net/http"

	"delivery-service/internal/middleware"
)

type SystemHandler struct {
	logger *slog.Logger
}

func NewSystemHandler(
	logger *slog.Logger,
) *SystemHandler {
	return &SystemHandler{
		logger: logger,
	}
}

func (h *SystemHandler) NotFound(
	w http.ResponseWriter,
	r *http.Request,
) {
	traceID := middleware.TraceID(r.Context())

	if err := writeError(
		w,
		http.StatusNotFound,
		ErrCodeNotFound,
		"Route not found",
		map[string]any{
			"trace_id": traceID,
		},
	); err != nil {
		h.logger.Error(
			"failed to write not found response",
			"trace_id", traceID,
			"error", err,
		)
	}
}

func (h *SystemHandler) MethodNotAllowed(
	allowed string,
) http.HandlerFunc {
	return func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		traceID := middleware.TraceID(r.Context())

		w.Header().Set("Allow", allowed)

		if err := writeError(
			w,
			http.StatusMethodNotAllowed,
			ErrCodeMethodNotAllowed,
			"Method not allowed",
			map[string]any{
				"trace_id": traceID,
			},
		); err != nil {
			h.logger.Error(
				"failed to write method not allowed response",
				"trace_id", traceID,
				"error", err,
			)
		}
	}
}
