package handler

import (
	"context"
	"delivery-service/internal/middleware"
	"delivery-service/internal/model"
	"delivery-service/internal/service"
	"encoding/json"
	"errors"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"io"
	"log/slog"
	"net/http"
)

type carrierService interface {
	AssignCarrier(context.Context, string, string, int64) (model.AssignCarrierResponse, error)
}

// CarrierHandler serves session-scoped carrier assignment requests.
type CarrierHandler struct {
	service  carrierService
	validate *validator.Validate
	logger   *slog.Logger
}

// NewCarrierHandler creates a carrier HTTP handler.
func NewCarrierHandler(s carrierService, v *validator.Validate, l *slog.Logger) *CarrierHandler {
	return &CarrierHandler{service: s, validate: v, logger: l}
}

// AssignCarrier atomically assigns the first carrier to a package.
// @Summary Назначить перевозчика
// @Description Первая попытка выигрывает. Повторное назначение, даже того же carrier_id, возвращает 409. Только текущая session cookie.
// @Tags packages
// @Accept json
// @Produce json
// @Param package_id path string true "UUID посылки"
// @Param request body model.AssignCarrierRequest true "Положительный carrier_id"
// @Success 200 {object} model.AssignCarrierResponse
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 409 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/packages/{package_id}/assign-carrier [post]
func (h *CarrierHandler) AssignCarrier(w http.ResponseWriter, r *http.Request) {
	fail := func(status int, code, message string, details map[string]any) {
		if err := writeError(w, status, code, message, details); err != nil {
			h.logger.Error("write carrier error", "trace_id", middleware.TraceID(r.Context()), "error", err)
		}
	}
	id, err := uuid.Parse(r.PathValue("package_id"))
	if err != nil {
		fail(400, ErrCodeInvalidPackageID, "Invalid package UUID", nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var req model.AssignCarrierRequest
	if err := decoder.Decode(&req); err != nil {
		fail(400, ErrCodeInvalidJSON, "Invalid request body", nil)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		fail(400, ErrCodeInvalidJSON, "Request must contain one JSON object", nil)
		return
	}
	if err := h.validate.Struct(req); err != nil {
		fail(400, ErrCodeValidation, "Invalid carrier ID", map[string]any{"carrier_id": "must be a positive integer"})
		return
	}
	result, err := h.service.AssignCarrier(r.Context(), middleware.SessionID(r.Context()), id.String(), req.CarrierID)
	switch {
	case errors.Is(err, service.ErrPackageNotFound):
		fail(404, ErrCodePackageNotFound, "Package not found", nil)
	case errors.Is(err, service.ErrCarrierAlreadyAssigned):
		fail(409, ErrCodeCarrierAlreadyAssigned, "Carrier already assigned", nil)
	case err != nil:
		h.logger.Error("assign carrier failed", "trace_id", middleware.TraceID(r.Context()), "error", err)
		fail(500, ErrCodeInternal, "Failed to assign carrier", nil)
	default:
		if err := writeJSON(w, 200, result); err != nil {
			h.logger.Error("write carrier response", "trace_id", middleware.TraceID(r.Context()), "error", err)
		}
	}
}
