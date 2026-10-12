package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"delivery-service/internal/model"
	"delivery-service/internal/service"
	"delivery-service/internal/validation"

	"github.com/go-playground/validator/v10"
	"github.com/shopspring/decimal"
)

func testLogger() *slog.Logger {
	return slog.New(
		slog.NewTextHandler(io.Discard, nil),
	)
}

func newTestValidator(t *testing.T) *validator.Validate {
	t.Helper()

	validate := validator.New()

	if err := validation.Register(validate); err != nil {
		t.Fatal(err)
	}

	return validate
}

func decodeErrorResponse(
	t *testing.T,
	rec *httptest.ResponseRecorder,
) ErrorResponse {
	t.Helper()

	var response ErrorResponse

	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf(
			"decode response: %v",
			err,
		)
	}

	return response
}

// ----------------------------------------------------
// FAKE SERVICE
// ----------------------------------------------------

type fakePackageService struct {
	createFn func(
		context.Context,
		string,
		model.CreatePackageRequest,
	) (model.CreatePackageResponse, error)

	getAllFn func(
		context.Context,
		string,
		model.PackageListFilter,
	) (model.PackageListResponse, error)

	getByIDFn func(
		context.Context,
		string,
		string,
	) (model.PackageListItem, error)
}

func (f *fakePackageService) Create(
	ctx context.Context,
	sessionID string,
	req model.CreatePackageRequest,
) (model.CreatePackageResponse, error) {
	if f.createFn == nil {
		panic("unexpected Create call")
	}

	return f.createFn(
		ctx,
		sessionID,
		req,
	)
}

func (f *fakePackageService) GetAll(
	ctx context.Context,
	sessionID string,
	filter model.PackageListFilter,
) (model.PackageListResponse, error) {
	if f.getAllFn == nil {
		panic("unexpected GetAll call")
	}

	return f.getAllFn(
		ctx,
		sessionID,
		filter,
	)
}

func (f *fakePackageService) GetByID(
	ctx context.Context,
	sessionID string,
	packageID string,
) (model.PackageListItem, error) {
	if f.getByIDFn == nil {
		panic("unexpected GetByID call")
	}

	return f.getByIDFn(
		ctx,
		sessionID,
		packageID,
	)
}

// ----------------------------------------------------
// CREATE — INVALID JSON
// ----------------------------------------------------

func TestPackageHandler_Create_InvalidJSON(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "empty body",
			body: "",
		},
		{
			name: "broken json",
			body: `{"name":`,
		},
		{
			name: "unknown field",
			body: `{
				"name": "Phone",
				"weight_kg": 1.5,
				"package_type_id": 1,
				"content_value_usd": 100,
				"unknown": "value"
			}`,
		},
		{
			name: "multiple json objects",
			body: `{"name":"first"} {"name":"second"}`,
		},
	}

	handler := &PackageHandler{
		logger: testLogger(),
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/packages",
				strings.NewReader(tt.body),
			)

			rec := httptest.NewRecorder()

			handler.Create(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf(
					"expected status %d, got %d",
					http.StatusBadRequest,
					rec.Code,
				)
			}

			response := decodeErrorResponse(t, rec)

			if response.Error.Code != "INVALID_JSON" {
				t.Errorf(
					"expected code INVALID_JSON, got %s",
					response.Error.Code,
				)
			}
		})
	}
}

// ----------------------------------------------------
// CREATE — REQUIRED
// ----------------------------------------------------

func TestPackageHandler_Create_ValidationRequired(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		expectedField string
	}{
		{
			name: "name is required",
			body: `{
				"weight_kg": 1.5,
				"package_type_id": 1,
				"content_value_usd": 100
			}`,
			expectedField: "name",
		},
		{
			name: "weight is required",
			body: `{
				"name": "Phone",
				"package_type_id": 1,
				"content_value_usd": 100
			}`,
			expectedField: "weight_kg",
		},
		{
			name: "package type is required",
			body: `{
				"name": "Phone",
				"weight_kg": 1.5,
				"content_value_usd": 100
			}`,
			expectedField: "package_type_id",
		},
		{
			name: "content value is required",
			body: `{
				"name": "Phone",
				"weight_kg": 1.5,
				"package_type_id": 1
			}`,
			expectedField: "content_value_usd",
		},
	}

	handler := &PackageHandler{
		validate: newTestValidator(t),
		logger:   testLogger(),
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/packages",
				strings.NewReader(tt.body),
			)

			rec := httptest.NewRecorder()

			handler.Create(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf(
					"expected status %d, got %d",
					http.StatusBadRequest,
					rec.Code,
				)
			}

			response := decodeErrorResponse(t, rec)

			if response.Error.Code != "VALIDATION_ERROR" {
				t.Fatalf(
					"expected code VALIDATION_ERROR, got %s",
					response.Error.Code,
				)
			}

			if _, ok := response.Error.Details[tt.expectedField]; !ok {
				t.Errorf(
					"expected validation error for %q, got %v",
					tt.expectedField,
					response.Error.Details,
				)
			}
		})
	}
}

// ----------------------------------------------------
// CREATE — VALUES
// ----------------------------------------------------

func TestPackageHandler_Create_ValidationValues(t *testing.T) {
	tests := []struct {
		name            string
		body            string
		expectedField   string
		expectedMessage string
	}{
		{
			name: "weight below minimum",
			body: `{
				"name": "Phone",
				"weight_kg": 0.0009,
				"package_type_id": 1,
				"content_value_usd": 100
			}`,
			expectedField:   "weight_kg",
			expectedMessage: "must be greater than or equal to 0.001",
		},
		{
			name: "weight above maximum",
			body: `{
				"name": "Phone",
				"weight_kg": 10000000,
				"package_type_id": 1,
				"content_value_usd": 100
			}`,
			expectedField:   "weight_kg",
			expectedMessage: "must be less than or equal to 9999999.999",
		},
		{
			name: "weight too many decimals",
			body: `{
				"name": "Phone",
				"weight_kg": 1.2345,
				"package_type_id": 1,
				"content_value_usd": 100
			}`,
			expectedField:   "weight_kg",
			expectedMessage: "must have no more than 3 decimal places",
		},
		{
			name: "package type below zero",
			body: `{
				"name": "Phone",
				"weight_kg": 1.5,
				"package_type_id": -1,
				"content_value_usd": 100
			}`,
			expectedField:   "package_type_id",
			expectedMessage: "must be greater than 0",
		},
		{
			name: "content value below minimum",
			body: `{
				"name": "Phone",
				"weight_kg": 1.5,
				"package_type_id": 1,
				"content_value_usd": -1
			}`,
			expectedField:   "content_value_usd",
			expectedMessage: "must be greater than or equal to 0",
		},
		{
			name: "content value above maximum",
			body: `{
				"name": "Phone",
				"weight_kg": 1.5,
				"package_type_id": 1,
				"content_value_usd": 10000000000
			}`,
			expectedField:   "content_value_usd",
			expectedMessage: "must be less than or equal to 9999999999.99",
		},
		{
			name: "content value too many decimals",
			body: `{
				"name": "Phone",
				"weight_kg": 1.5,
				"package_type_id": 1,
				"content_value_usd": 12.345
			}`,
			expectedField:   "content_value_usd",
			expectedMessage: "must have at most 2 decimal places",
		},
	}

	handler := &PackageHandler{
		validate: newTestValidator(t),
		logger:   testLogger(),
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/packages",
				strings.NewReader(tt.body),
			)

			rec := httptest.NewRecorder()

			handler.Create(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf(
					"expected status %d, got %d",
					http.StatusBadRequest,
					rec.Code,
				)
			}

			response := decodeErrorResponse(t, rec)

			if response.Error.Code != "VALIDATION_ERROR" {
				t.Fatalf(
					"expected VALIDATION_ERROR, got %s",
					response.Error.Code,
				)
			}

			value, ok := response.Error.Details[tt.expectedField]
			if !ok {
				t.Fatalf(
					"expected error for %q, got %v",
					tt.expectedField,
					response.Error.Details,
				)
			}

			if value != tt.expectedMessage {
				t.Errorf(
					"expected message %q, got %q",
					tt.expectedMessage,
					value,
				)
			}
		})
	}
}

// ----------------------------------------------------
// CREATE — SERVICE
// ----------------------------------------------------

func TestPackageHandler_Create_ServiceResults(t *testing.T) {
	tests := []struct {
		name       string
		serviceErr error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "success",
			serviceErr: nil,
			wantStatus: http.StatusCreated,
		},
		{
			name:       "package type not found",
			serviceErr: service.ErrPackageTypeNotFound,
			wantStatus: http.StatusBadRequest,
			wantCode:   "VALIDATION_ERROR",
		},
		{
			name:       "internal error",
			serviceErr: errors.New("database error"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false

			fake := &fakePackageService{
				createFn: func(
					ctx context.Context,
					sessionID string,
					req model.CreatePackageRequest,
				) (model.CreatePackageResponse, error) {
					called = true

					if req.Name != "Phone" {
						t.Errorf(
							"expected name Phone, got %s",
							req.Name,
						)
					}

					if req.WeightKG.String() != "1.5" {
						t.Errorf(
							"expected weight 1.5, got %v",
							req.WeightKG,
						)
					}

					if req.PackageTypeID != 1 {
						t.Errorf(
							"expected package type 1, got %d",
							req.PackageTypeID,
						)
					}

					if req.ContentValueUSD == nil {
						t.Fatal(
							"expected content_value_usd",
						)
					}

					if req.ContentValueUSD.String() != "100" {
						t.Errorf(
							"expected content value 100, got %v",
							*req.ContentValueUSD,
						)
					}

					return model.CreatePackageResponse{},
						tt.serviceErr
				},
			}

			handler := NewPackageHandler(
				fake,
				newTestValidator(t),
				testLogger(),
			)

			req := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/packages",
				strings.NewReader(`{
					"name": "Phone",
					"weight_kg": 1.5,
					"package_type_id": 1,
					"content_value_usd": 100
				}`),
			)

			rec := httptest.NewRecorder()

			handler.Create(rec, req)

			if !called {
				t.Fatal(
					"expected service Create to be called",
				)
			}

			if rec.Code != tt.wantStatus {
				t.Fatalf(
					"expected status %d, got %d",
					tt.wantStatus,
					rec.Code,
				)
			}

			if tt.wantCode != "" {
				response := decodeErrorResponse(
					t,
					rec,
				)

				if response.Error.Code != tt.wantCode {
					t.Errorf(
						"expected code %s, got %s",
						tt.wantCode,
						response.Error.Code,
					)
				}
			}
		})
	}
}

// ----------------------------------------------------
// GET ALL — INVALID QUERY
// ----------------------------------------------------

func TestPackageHandler_GetAll_InvalidQuery(t *testing.T) {
	tests := []struct {
		name          string
		query         string
		expectedField string
	}{
		{
			name:          "page zero",
			query:         "?page=0",
			expectedField: "page",
		},
		{
			name:          "page negative",
			query:         "?page=-1",
			expectedField: "page",
		},
		{
			name:          "page not number",
			query:         "?page=abc",
			expectedField: "page",
		},
		{
			name:          "limit zero",
			query:         "?limit=0",
			expectedField: "limit",
		},
		{
			name:          "limit negative",
			query:         "?limit=-1",
			expectedField: "limit",
		},
		{
			name:          "limit greater than 100",
			query:         "?limit=101",
			expectedField: "limit",
		},
		{
			name:          "limit not number",
			query:         "?limit=abc",
			expectedField: "limit",
		},
		{
			name:          "package type zero",
			query:         "?package_type_id=0",
			expectedField: "package_type_id",
		},
		{
			name:          "package type negative",
			query:         "?package_type_id=-1",
			expectedField: "package_type_id",
		},
		{
			name:          "package type not number",
			query:         "?package_type_id=abc",
			expectedField: "package_type_id",
		},
		{
			name:          "calculated invalid",
			query:         "?calculated=abc",
			expectedField: "calculated",
		},
		{
			name: "page overflow",
			query: "?page=" +
				strconv.Itoa(math.MaxInt) +
				"&limit=100",
			expectedField: "page",
		},
	}

	handler := &PackageHandler{
		logger: testLogger(),
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(
				http.MethodGet,
				"/api/v1/packages"+tt.query,
				nil,
			)

			rec := httptest.NewRecorder()

			handler.GetAll(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf(
					"expected status %d, got %d",
					http.StatusBadRequest,
					rec.Code,
				)
			}

			response := decodeErrorResponse(t, rec)

			if response.Error.Code != "INVALID_QUERY" {
				t.Errorf(
					"expected INVALID_QUERY, got %s",
					response.Error.Code,
				)
			}

			if _, ok := response.Error.Details[tt.expectedField]; !ok {
				t.Errorf(
					"expected error for %q, got %v",
					tt.expectedField,
					response.Error.Details,
				)
			}
		})
	}
}

// ----------------------------------------------------
// GET ALL — DEFAULTS
// ----------------------------------------------------

func TestPackageHandler_GetAll_DefaultPagination(t *testing.T) {
	called := false

	fake := &fakePackageService{
		getAllFn: func(
			ctx context.Context,
			sessionID string,
			filter model.PackageListFilter,
		) (model.PackageListResponse, error) {
			called = true

			if filter.Page != 1 {
				t.Errorf(
					"expected page 1, got %d",
					filter.Page,
				)
			}

			if filter.Limit != 20 {
				t.Errorf(
					"expected limit 20, got %d",
					filter.Limit,
				)
			}

			if filter.PackageTypeID != nil {
				t.Errorf(
					"expected nil package_type_id",
				)
			}

			if filter.Calculated != nil {
				t.Errorf(
					"expected nil calculated",
				)
			}

			return model.PackageListResponse{}, nil
		},
	}

	handler := NewPackageHandler(
		fake,
		nil,
		testLogger(),
	)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/packages",
		nil,
	)

	rec := httptest.NewRecorder()

	handler.GetAll(rec, req)

	if !called {
		t.Fatal(
			"expected service GetAll to be called",
		)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusOK,
			rec.Code,
		)
	}
}

// ----------------------------------------------------
// GET ALL — FILTERS
// ----------------------------------------------------

func TestPackageHandler_GetAll_Filters(t *testing.T) {
	fake := &fakePackageService{
		getAllFn: func(
			ctx context.Context,
			sessionID string,
			filter model.PackageListFilter,
		) (model.PackageListResponse, error) {
			if filter.Page != 2 {
				t.Errorf(
					"expected page 2, got %d",
					filter.Page,
				)
			}

			if filter.Limit != 10 {
				t.Errorf(
					"expected limit 10, got %d",
					filter.Limit,
				)
			}

			if filter.PackageTypeID == nil {
				t.Fatal(
					"expected package_type_id",
				)
			}

			if *filter.PackageTypeID != 3 {
				t.Errorf(
					"expected package type 3, got %d",
					*filter.PackageTypeID,
				)
			}

			if filter.Calculated == nil {
				t.Fatal(
					"expected calculated filter",
				)
			}

			if !*filter.Calculated {
				t.Error(
					"expected calculated=true",
				)
			}

			return model.PackageListResponse{}, nil
		},
	}

	handler := NewPackageHandler(
		fake,
		nil,
		testLogger(),
	)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/packages?page=2&limit=10&package_type_id=3&calculated=true",
		nil,
	)

	rec := httptest.NewRecorder()

	handler.GetAll(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusOK,
			rec.Code,
		)
	}
}

// ----------------------------------------------------
// GET ALL — SERVICE ERROR
// ----------------------------------------------------

func TestPackageHandler_GetAll_ServiceError(t *testing.T) {
	fake := &fakePackageService{
		getAllFn: func(
			context.Context,
			string,
			model.PackageListFilter,
		) (model.PackageListResponse, error) {
			return model.PackageListResponse{},
				errors.New("database error")
		},
	}

	handler := NewPackageHandler(
		fake,
		nil,
		testLogger(),
	)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/packages",
		nil,
	)

	rec := httptest.NewRecorder()

	handler.GetAll(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusInternalServerError,
			rec.Code,
		)
	}

	response := decodeErrorResponse(t, rec)

	if response.Error.Code != "INTERNAL_ERROR" {
		t.Errorf(
			"expected INTERNAL_ERROR, got %s",
			response.Error.Code,
		)
	}
}

// ----------------------------------------------------
// GET BY ID — INVALID UUID
// ----------------------------------------------------

func TestPackageHandler_GetByID_InvalidUUID(t *testing.T) {
	handler := &PackageHandler{
		logger: testLogger(),
	}

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/packages/not-a-uuid",
		nil,
	)

	req.SetPathValue(
		"package_id",
		"not-a-uuid",
	)

	rec := httptest.NewRecorder()

	handler.GetByID(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusBadRequest,
			rec.Code,
		)
	}

	response := decodeErrorResponse(t, rec)

	if response.Error.Code != "INVALID_PACKAGE_ID" {
		t.Errorf(
			"expected INVALID_PACKAGE_ID, got %s",
			response.Error.Code,
		)
	}
}

// ----------------------------------------------------
// GET BY ID — SERVICE
// ----------------------------------------------------

func TestPackageHandler_GetByID_ServiceResults(t *testing.T) {
	const packageID = "550e8400-e29b-41d4-a716-446655440000"

	tests := []struct {
		name       string
		serviceErr error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "success",
			serviceErr: nil,
			wantStatus: http.StatusOK,
		},
		{
			name:       "package not found",
			serviceErr: service.ErrPackageNotFound,
			wantStatus: http.StatusNotFound,
			wantCode:   "PACKAGE_NOT_FOUND",
		},
		{
			name:       "internal error",
			serviceErr: errors.New("database error"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false

			fake := &fakePackageService{
				getByIDFn: func(
					ctx context.Context,
					sessionID string,
					gotPackageID string,
				) (model.PackageListItem, error) {
					called = true

					if gotPackageID != packageID {
						t.Errorf(
							"expected package ID %s, got %s",
							packageID,
							gotPackageID,
						)
					}

					return model.PackageListItem{},
						tt.serviceErr
				},
			}

			handler := NewPackageHandler(
				fake,
				nil,
				testLogger(),
			)

			req := httptest.NewRequest(
				http.MethodGet,
				"/api/v1/packages/"+packageID,
				nil,
			)

			req.SetPathValue(
				"package_id",
				packageID,
			)

			rec := httptest.NewRecorder()

			handler.GetByID(rec, req)

			if !called {
				t.Fatal(
					"expected service GetByID to be called",
				)
			}

			if rec.Code != tt.wantStatus {
				t.Fatalf(
					"expected status %d, got %d",
					tt.wantStatus,
					rec.Code,
				)
			}

			if tt.wantCode != "" {
				response := decodeErrorResponse(
					t,
					rec,
				)

				if response.Error.Code != tt.wantCode {
					t.Errorf(
						"expected code %s, got %s",
						tt.wantCode,
						response.Error.Code,
					)
				}
			}
		})
	}
}

func TestWriteErrorDetailsAlwaysPresent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		details map[string]any
	}{
		{"nil", nil}, {"empty", map[string]any{}}, {"field validation", map[string]any{"weight_kg": "invalid"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			if err := writeError(rec, http.StatusBadRequest, ErrCodeValidation, "Validation failed", tc.details); err != nil {
				t.Fatal(err)
			}
			var body struct {
				Error map[string]json.RawMessage `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"code", "message", "details"} {
				if _, ok := body.Error[key]; !ok {
					t.Fatalf("missing %s: %s", key, rec.Body.String())
				}
			}
			var details map[string]any
			if err := json.Unmarshal(body.Error["details"], &details); err != nil || details == nil {
				t.Fatalf("details must be object: %s", body.Error["details"])
			}
			if len(details) != len(tc.details) {
				t.Fatalf("details=%v", details)
			}
			for k, v := range tc.details {
				if details[k] != v {
					t.Fatalf("details=%v", details)
				}
			}
			if rec.Code != http.StatusBadRequest || rec.Header().Get("Content-Type") != "application/json" {
				t.Fatal("wrong HTTP response")
			}
		})
	}
}

func TestWriteJSONMarshalFailureDoesNotCommitResponse(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := writeJSON(rec, http.StatusCreated, make(chan int)); err == nil {
		t.Fatal("expected marshal error")
	}
	if rec.Body.Len() != 0 || rec.Header().Get("Content-Type") != "" {
		t.Fatal("response committed before serialization")
	}
}

type failingResponseWriter struct {
	header http.Header
	err    error
}

func (w *failingResponseWriter) Header() http.Header       { return w.header }
func (w *failingResponseWriter) WriteHeader(int)           {}
func (w *failingResponseWriter) Write([]byte) (int, error) { return 0, w.err }
func TestWriteJSONPreservesWriteError(t *testing.T) {
	failure := errors.New("connection closed")
	err := writeJSON(&failingResponseWriter{header: make(http.Header), err: failure}, http.StatusOK, map[string]int{"processed": 1})
	if !errors.Is(err, failure) {
		t.Fatalf("error=%v", err)
	}
}

func TestSavedRateJSON(t *testing.T) {
	exact := decimal.RequireFromString("100.1234567890123456789")
	for _, rate := range []*decimal.Decimal{nil, &exact} {
		w := httptest.NewRecorder()
		if err := writeJSON(w, 200, model.PackageListItem{USDToRUBRate: rate}); err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		value, present := body["usd_to_rub_rate"]
		if !present {
			t.Fatal("missing nullable rate")
		}
		if rate == nil && value != nil || rate != nil && value != exact.String() {
			t.Fatalf("rate=%v", value)
		}
	}
}
