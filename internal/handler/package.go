package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"

	"delivery-service/internal/middleware"
	"delivery-service/internal/model"
	"delivery-service/internal/service"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
)

type packageService interface {
	Create(
		ctx context.Context,
		sessionID string,
		req model.CreatePackageRequest,
	) (model.CreatePackageResponse, error)

	GetAll(
		ctx context.Context,
		sessionID string,
		filter model.PackageListFilter,
	) (model.PackageListResponse, error)

	GetByID(
		ctx context.Context,
		sessionID string,
		packageID string,
	) (model.PackageListItem, error)
}

// PackageHandler validates and serves session-scoped package requests.
type PackageHandler struct {
	service  packageService
	validate *validator.Validate
	logger   *slog.Logger
}

// NewPackageHandler injects the package service and request validator.
func NewPackageHandler(
	service packageService,
	validate *validator.Validate,
	logger *slog.Logger,
) *PackageHandler {
	return &PackageHandler{
		service:  service,
		validate: validate,
		logger:   logger,
	}
}

// Create godoc
// @Summary Зарегистрировать посылку
// @Description После 201 registered посылка подтверждённо передана в RabbitMQ. Worker асинхронно рассчитывает её и сохраняет PostgreSQL/MongoDB; GET может временно вернуть 404. session_id cookie изолирует данные пользователя.
// @Tags packages
// @Accept json
// @Produce json
// @Param Idempotency-Key header string false "1..128 printable ASCII: same session/key/normalized request returns same package_id and created_at; changed payload gives 409"
// @Param package body model.CreatePackageRequest true "Данные посылки"
// @Success 201 {object} model.CreatePackageResponse
// @Failure 409 {object} ErrorResponse
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/packages [post]
func (h *PackageHandler) Create(
	w http.ResponseWriter,
	r *http.Request,
) {
	var req model.CreatePackageRequest

	r.Body = http.MaxBytesReader(
		w,
		r.Body,
		1<<20, // 1 MB
	)

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&req); err != nil {
		h.respondError(
			w,
			r,
			http.StatusBadRequest,
			ErrCodeInvalidJSON,
			"Invalid request body",
			nil,
		)
		return
	}

	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		h.respondError(
			w,
			r,
			http.StatusBadRequest,
			ErrCodeInvalidJSON,
			"Request body must contain a single JSON object",
			nil,
		)
		return
	}

	if err := h.validate.Struct(req); err != nil {
		details := make(map[string]any)

		if validationErrors, ok := err.(validator.ValidationErrors); ok {
			for _, validationErr := range validationErrors {
				switch validationErr.Field() {
				case "Name":
					details["name"] = "required"

				case "WeightKG":
					switch validationErr.Tag() {
					case "required":
						details["weight_kg"] = "required"
					case "gte", "decimal_gte":
						details["weight_kg"] = "must be greater than or equal to 0.001"
					case "lte", "decimal_lte":
						details["weight_kg"] = "must be less than or equal to 9999999.999"
					case "max_decimal":
						details["weight_kg"] = "must have no more than 3 decimal places"
					}
				case "PackageTypeID":
					switch validationErr.Tag() {
					case "required":
						details["package_type_id"] = "required"
					case "gt":
						details["package_type_id"] = "must be greater than 0"
					}
				case "ContentValueUSD":
					switch validationErr.Tag() {
					case "required":
						details["content_value_usd"] = "required"

					case "gte", "decimal_gte":
						details["content_value_usd"] =
							"must be greater than or equal to 0"

					case "lte", "decimal_lte":
						details["content_value_usd"] =
							"must be less than or equal to 9999999999.99"

					case "max_decimal":
						details["content_value_usd"] =
							"must have at most 2 decimal places"
					}
				}
			}
		}

		h.respondError(
			w,
			r,
			http.StatusBadRequest,
			ErrCodeValidation,
			"Validation failed",
			details,
		)
		return
	}

	if keys, present := r.Header["Idempotency-Key"]; present {
		if len(keys) != 1 || h.validate.Var(keys[0], "min=1,max=128,printascii") != nil {
			h.respondError(w, r, http.StatusBadRequest, ErrCodeIdempotencyKeyInvalid, "Invalid Idempotency-Key", map[string]any{"Idempotency-Key": "one header value, 1..128 printable ASCII characters"})
			return
		}
		req.IdempotencyKey = keys[0]
	}
	sessionID := middleware.SessionID(r.Context())

	response, err := h.service.Create(
		r.Context(),
		sessionID,
		req,
	)
	if err != nil {
		if errors.Is(err, service.ErrIdempotencyKeyConflict) {
			h.respondError(w, r, http.StatusConflict, ErrCodeIdempotencyKeyConflict, "Idempotency-Key already used with a different payload", nil)
			return
		}
		if errors.Is(err, service.ErrPackageTypeNotFound) {
			h.respondError(
				w,
				r,
				http.StatusBadRequest,
				ErrCodeValidation,
				"Validation failed",
				map[string]any{
					"package_type_id": "package type does not exist",
				},
			)
			return
		}

		h.logger.Error(
			"failed to create package",
			"trace_id", middleware.TraceID(r.Context()),
			"error", err,
		)

		h.respondError(
			w,
			r,
			http.StatusInternalServerError,
			ErrCodeInternal,
			"Internal server error",
			nil,
		)
		return
	}

	h.respondJSON(
		w,
		r,
		http.StatusCreated,
		response,
	)
}

// GetAll godoc
// @Summary Получить список посылок
// @Description Возвращает посылки текущей сессии с пагинацией и фильтрацией
// @Tags packages
// @Produce json
// @Param page query int false "Номер страницы" default(1) minimum(1)
// @Param limit query int false "Количество элементов" default(20) minimum(1) maximum(100)
// @Param package_type_id query int false "ID типа посылки" minimum(1)
// @Param calculated query bool false "Фильтр по наличию рассчитанной стоимости"
// @Success 200 {object} model.PackageListResponse
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/packages [get]
func (h *PackageHandler) GetAll(
	w http.ResponseWriter,
	r *http.Request,
) {
	query := r.URL.Query()

	page := 1
	limit := 20

	if value := query.Get("page"); value != "" {
		parsedPage, err := strconv.Atoi(value)
		if err != nil || parsedPage < 1 {
			h.respondError(
				w,
				r,
				http.StatusBadRequest,
				ErrCodeInvalidQuery,
				"Invalid page parameter",
				map[string]any{
					"page": "must be a positive integer",
				},
			)
			return
		}

		page = parsedPage
	}

	if value := query.Get("limit"); value != "" {
		parsedLimit, err := strconv.Atoi(value)
		if err != nil || parsedLimit < 1 || parsedLimit > 100 {
			h.respondError(
				w,
				r,
				http.StatusBadRequest,
				ErrCodeInvalidQuery,
				"Invalid limit parameter",
				map[string]any{
					"limit": "must be between 1 and 100",
				},
			)
			return
		}

		limit = parsedLimit
	}

	if page > 1 && page-1 > math.MaxInt/limit {
		h.respondError(
			w,
			r,
			http.StatusBadRequest,
			ErrCodeInvalidQuery,
			"Invalid page parameter",
			map[string]any{
				"page": "page is too large",
			},
		)
		return
	}

	var packageTypeID *int

	if value := query.Get("package_type_id"); value != "" {
		parsedPackageTypeID, err := strconv.Atoi(value)
		if err != nil || parsedPackageTypeID < 1 {
			h.respondError(
				w,
				r,
				http.StatusBadRequest,
				ErrCodeInvalidQuery,
				"Invalid package_type_id parameter",
				map[string]any{
					"package_type_id": "must be a positive integer",
				},
			)
			return
		}

		packageTypeID = &parsedPackageTypeID
	}

	var calculated *bool

	if value := query.Get("calculated"); value != "" {
		parsedCalculated, err := strconv.ParseBool(value)
		if err != nil {
			h.respondError(
				w,
				r,
				http.StatusBadRequest,
				ErrCodeInvalidQuery,
				"Invalid calculated parameter",
				map[string]any{
					"calculated": "must be true or false",
				},
			)
			return
		}

		calculated = &parsedCalculated
	}

	filter := model.PackageListFilter{
		Page:          page,
		Limit:         limit,
		PackageTypeID: packageTypeID,
		Calculated:    calculated,
	}

	sessionID := middleware.SessionID(r.Context())

	response, err := h.service.GetAll(
		r.Context(),
		sessionID,
		filter,
	)
	if err != nil {
		h.logger.Error(
			"failed to get packages",
			"trace_id", middleware.TraceID(r.Context()),
			"error", err,
		)

		h.respondError(
			w,
			r,
			http.StatusInternalServerError,
			ErrCodeInternal,
			"Internal server error",
			nil,
		)
		return
	}

	h.respondJSON(
		w,
		r,
		http.StatusOK,
		response,
	)
}

// GetByID godoc
// @Summary Получить посылку
// @Description Возвращает посылку текущей сессии по package_id
// @Tags packages
// @Produce json
// @Param package_id path string true "UUID посылки"
// @Success 200 {object} model.PackageListItem
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/packages/{package_id} [get]
func (h *PackageHandler) GetByID(
	w http.ResponseWriter,
	r *http.Request,
) {
	packageID := r.PathValue("package_id")

	if _, err := uuid.Parse(packageID); err != nil {
		h.respondError(
			w,
			r,
			http.StatusBadRequest,
			ErrCodeInvalidPackageID,
			"Invalid package ID",
			nil,
		)
		return
	}

	sessionID := middleware.SessionID(r.Context())

	response, err := h.service.GetByID(
		r.Context(),
		sessionID,
		packageID,
	)
	if err != nil {
		if errors.Is(err, service.ErrPackageNotFound) {
			h.respondError(
				w,
				r,
				http.StatusNotFound,
				ErrCodePackageNotFound,
				"Package not found",
				nil,
			)
			return
		}

		h.logger.Error(
			"failed to get package",
			"trace_id", middleware.TraceID(r.Context()),
			"error", err,
		)

		h.respondError(
			w,
			r,
			http.StatusInternalServerError,
			ErrCodeInternal,
			"Internal server error",
			nil,
		)
		return
	}

	h.respondJSON(
		w,
		r,
		http.StatusOK,
		response,
	)
}

func (h *PackageHandler) respondJSON(
	w http.ResponseWriter,
	r *http.Request,
	status int,
	data any,
) {
	if err := writeJSON(
		w,
		status,
		data,
	); err != nil {
		h.logger.Error(
			"failed to write JSON response",
			"trace_id", middleware.TraceID(r.Context()),
			"error", err,
		)
	}
}

func (h *PackageHandler) respondError(
	w http.ResponseWriter,
	r *http.Request,
	status int,
	code string,
	message string,
	details map[string]any,
) {
	if err := writeError(
		w,
		status,
		code,
		message,
		details,
	); err != nil {
		h.logger.Error(
			"failed to write error response",
			"trace_id", middleware.TraceID(r.Context()),
			"error", err,
		)
	}
}
