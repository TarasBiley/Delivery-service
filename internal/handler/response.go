package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
)

const (
	ErrCodeIdempotencyKeyInvalid  = "IDEMPOTENCY_KEY_INVALID"
	ErrCodeIdempotencyKeyConflict = "IDEMPOTENCY_KEY_CONFLICT"
	ErrCodeInvalidJSON            = "INVALID_JSON"
	ErrCodeValidation             = "VALIDATION_ERROR"
	ErrCodeInvalidQuery           = "INVALID_QUERY"
	ErrCodeInvalidPackageID       = "INVALID_PACKAGE_ID"
	ErrCodePackageNotFound        = "PACKAGE_NOT_FOUND"
	ErrCodeInternal               = "INTERNAL_ERROR"
	ErrCodeCarrierAlreadyAssigned = "CARRIER_ALREADY_ASSIGNED"
	ErrCodeCalculation            = "CALCULATION_ERROR"
	ErrCodeNotFound               = "NOT_FOUND"
	ErrCodeMethodNotAllowed       = "METHOD_NOT_ALLOWED"
)

type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

type ErrorDetail struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

func writeJSON(
	w http.ResponseWriter,
	status int,
	data any,
) error {
	body, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf(
			"marshal JSON response: %w",
			err,
		)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if _, err := w.Write(body); err != nil {
		return fmt.Errorf(
			"write JSON response: %w",
			err,
		)
	}

	return nil
}

func writeError(
	w http.ResponseWriter,
	status int,
	code string,
	message string,
	details map[string]any,
) error {
	if details == nil {
		details = map[string]any{}
	}
	return writeJSON(
		w,
		status,
		ErrorResponse{
			Error: ErrorDetail{
				Code:    code,
				Message: message,
				Details: details,
			},
		},
	)
}
