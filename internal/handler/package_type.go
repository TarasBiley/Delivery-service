package handler

import (
	"log/slog"
	"net/http"

	"delivery-service/internal/middleware"
	"delivery-service/internal/model"
	"delivery-service/internal/service"
)

// PackageTypeHandler serves the package type dictionary.
type PackageTypeHandler struct {
	service *service.PackageTypeService
	logger  *slog.Logger
}

// NewPackageTypeHandler injects the package type service and logger.
func NewPackageTypeHandler(
	service *service.PackageTypeService,
	logger *slog.Logger,
) *PackageTypeHandler {
	return &PackageTypeHandler{
		service: service,
		logger:  logger,
	}
}

type packageTypesResponse struct {
	Data []model.PackageType `json:"data"`
}

// GetAll godoc
// @Summary Получить типы посылок
// @Description Возвращает список доступных типов посылок
// @Tags package-types
// @Produce json
// @Success 200 {object} packageTypesResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/package-types [get]
func (h *PackageTypeHandler) GetAll(
	w http.ResponseWriter,
	r *http.Request,
) {
	traceID := middleware.TraceID(r.Context())

	packageTypes, err := h.service.GetAll(r.Context())
	if err != nil {
		h.logger.Error(
			"failed to get package types",
			"error", err,
			"trace_id", traceID,
		)

		if err := writeError(
			w,
			http.StatusInternalServerError,
			ErrCodeInternal,
			"Internal server error",
			map[string]any{
				"trace_id": traceID,
			},
		); err != nil {
			h.logger.Error(
				"failed to write error response",
				"error", err,
				"trace_id", traceID,
			)
		}

		return
	}

	if err := writeJSON(
		w,
		http.StatusOK,
		packageTypesResponse{
			Data: packageTypes,
		},
	); err != nil {
		h.logger.Error(
			"failed to write package types response",
			"error", err,
			"trace_id", traceID,
		)
	}
}
