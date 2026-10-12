package handler

import (
	"context"
	"delivery-service/internal/model"
	"delivery-service/internal/service"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIdempotencyHeader(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers []string
		status  int
		code    string
		err     error
	}{
		{name: "absent", status: 201}, {name: "valid", headers: []string{"key"}, status: 201}, {name: "max", headers: []string{strings.Repeat("a", 128)}, status: 201},
		{name: "empty", headers: []string{""}, status: 400, code: ErrCodeIdempotencyKeyInvalid}, {name: "long", headers: []string{strings.Repeat("a", 129)}, status: 400, code: ErrCodeIdempotencyKeyInvalid},
		{name: "multiple", headers: []string{"a", "b"}, status: 400, code: ErrCodeIdempotencyKeyInvalid}, {name: "unicode", headers: []string{"ключ"}, status: 400, code: ErrCodeIdempotencyKeyInvalid},
		{name: "conflict", headers: []string{"key"}, err: service.ErrIdempotencyKeyConflict, status: 409, code: ErrCodeIdempotencyKeyConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			stub := &fakePackageService{createFn: func(_ context.Context, _ string, req model.CreatePackageRequest) (model.CreatePackageResponse, error) {
				called = true
				want := ""
				if len(tc.headers) == 1 {
					want = tc.headers[0]
				}
				if req.IdempotencyKey != want {
					t.Fatal("key not propagated")
				}
				return model.CreatePackageResponse{PackageID: "id", Status: "registered"}, tc.err
			}}
			h := NewPackageHandler(stub, newTestValidator(t), testLogger())
			req := httptest.NewRequest(http.MethodPost, "/api/v1/packages", strings.NewReader(`{"name":"test","weight_kg":1,"package_type_id":1,"content_value_usd":0}`))
			for _, value := range tc.headers {
				req.Header.Add("Idempotency-Key", value)
			}
			w := httptest.NewRecorder()
			h.Create(w, req)
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tc.status == 400 && called {
				t.Fatal("invalid key called service")
			}
			if tc.code != "" {
				assertAPIError(t, w, tc.code)
			}
		})
	}
}

func TestCreateRejectsQuotedDecimals(t *testing.T) {
	h := NewPackageHandler(&fakePackageService{}, newTestValidator(t), testLogger())
	for _, body := range []string{`{"name":"test","weight_kg":"1","package_type_id":1,"content_value_usd":0}`, `{"name":"test","weight_kg":1,"package_type_id":1,"content_value_usd":"0"}`} {
		w := httptest.NewRecorder()
		h.Create(w, httptest.NewRequest("POST", "/api/v1/packages", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatalf("quoted decimal status=%d", w.Code)
		}
		assertAPIError(t, w, ErrCodeInvalidJSON)
	}
}
