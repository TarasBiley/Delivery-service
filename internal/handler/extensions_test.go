package handler

import (
	"context"
	"delivery-service/internal/middleware"
	"delivery-service/internal/model"
	"delivery-service/internal/service"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type carrierFunc func(context.Context, string, string, int64) (model.AssignCarrierResponse, error)

func (f carrierFunc) AssignCarrier(c context.Context, s, p string, id int64) (model.AssignCarrierResponse, error) {
	return f(c, s, p, id)
}

type analyticsFunc func(context.Context, string, string) (model.DailySumResponse, error)

func (f analyticsFunc) DailySum(c context.Context, s, d string) (model.DailySumResponse, error) {
	return f(c, s, d)
}
func assertAPIError(t *testing.T, w *httptest.ResponseRecorder, code string) {
	t.Helper()
	var r ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Error.Code != code || r.Error.Message == "" || r.Error.Details == nil {
		t.Fatalf("error=%+v", r)
	}
}
func TestAssignCarrierHandler(t *testing.T) {
	for _, tc := range []struct {
		name, body, id string
		err            error
		status         int
		code           string
	}{
		{name: "success", body: `{"carrier_id":123}`, status: 200},
		{name: "zero", body: `{"carrier_id":0}`, status: 400, code: ErrCodeValidation},
		{name: "negative", body: `{"carrier_id":-1}`, status: 400, code: ErrCodeValidation},
		{name: "missing", body: `{}`, status: 400, code: ErrCodeValidation},
		{name: "null", body: `null`, status: 400, code: ErrCodeValidation},
		{name: "malformed", body: `{`, status: 400, code: ErrCodeInvalidJSON},
		{name: "unknown field", body: `{"carrier_id":1,"other":1}`, status: 400, code: ErrCodeInvalidJSON},
		{name: "trailing JSON", body: `{"carrier_id":1}{}`, status: 400, code: ErrCodeInvalidJSON},
		{name: "fraction", body: `{"carrier_id":1.5}`, status: 400, code: ErrCodeInvalidJSON},
		{name: "overflow", body: `{"carrier_id":9223372036854775808}`, status: 400, code: ErrCodeInvalidJSON},
		{name: "invalid UUID", body: `{"carrier_id":123}`, id: "wrong", status: 400, code: ErrCodeInvalidPackageID},
		{name: "not found", body: `{"carrier_id":123}`, err: service.ErrPackageNotFound, status: 404, code: ErrCodePackageNotFound},
		{name: "conflict", body: `{"carrier_id":123}`, err: service.ErrCarrierAlreadyAssigned, status: 409, code: ErrCodeCarrierAlreadyAssigned},
		{name: "internal", body: `{"carrier_id":123}`, err: errors.New("down"), status: 500, code: ErrCodeInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, session := uuid.NewString(), uuid.NewString()
			if tc.id != "" {
				id = tc.id
			}
			called := false
			h := NewCarrierHandler(carrierFunc(func(_ context.Context, s, p string, n int64) (model.AssignCarrierResponse, error) {
				called = true
				if s != session || p != id || n != 123 {
					t.Fatal("wrong arguments")
				}
				return model.AssignCarrierResponse{CarrierID: n}, tc.err
			}), newTestValidator(t), testLogger())
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			req.SetPathValue("package_id", id)
			req.AddCookie(&http.Cookie{Name: "session_id", Value: session})
			w := httptest.NewRecorder()
			middleware.Session(false)(http.HandlerFunc(h.AssignCarrier)).ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tc.status == 400 && called {
				t.Fatal("invalid request reached service")
			}
			if tc.code != "" {
				assertAPIError(t, w, tc.code)
			} else {
				var got model.AssignCarrierResponse
				if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got.CarrierID != 123 {
					t.Fatal(got)
				}
			}
		})
	}
}
func TestAnalyticsHandler(t *testing.T) {
	for _, tc := range []struct {
		date   string
		err    error
		status int
		code   string
	}{{"2026-10-11", nil, 200, ""}, {"", service.ErrInvalidDate, 400, ErrCodeInvalidQuery}, {"bad", service.ErrInvalidDate, 400, ErrCodeInvalidQuery}, {"2026-10-11", errors.New("down"), 500, ErrCodeInternal}} {
		session := uuid.NewString()
		h := NewAnalyticsHandler(analyticsFunc(func(_ context.Context, s, d string) (model.DailySumResponse, error) {
			if s != session || d != tc.date {
				t.Fatal("arguments")
			}
			return model.DailySumResponse{Date: d, Data: []model.DailySumItem{}}, tc.err
		}), testLogger())
		req := httptest.NewRequest("GET", "/api/v1/analytics/daily-sum?date="+tc.date, nil)
		req.AddCookie(&http.Cookie{Name: "session_id", Value: session})
		w := httptest.NewRecorder()
		middleware.Session(false)(http.HandlerFunc(h.DailySum)).ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatalf("status=%d", w.Code)
		}
		if tc.code != "" {
			assertAPIError(t, w, tc.code)
		}
	}
}
