package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReadiness(t *testing.T) {
	for _, failed := range []string{"", "postgres", "redis", "mongo", "rabbitmq", "consumer"} {
		t.Run("failure="+failed, func(t *testing.T) {
			checks := map[string]HealthCheck{}
			for _, name := range []string{"postgres", "redis", "mongo", "rabbitmq", "consumer"} {
				checks[name] = func(ctx context.Context) error {
					if _, ok := ctx.Deadline(); !ok {
						t.Fatal("unbounded health")
					}
					if name == failed {
						return errors.New("unavailable")
					}
					return nil
				}
			}
			h := NewHealthHandler(checks, slog.New(slog.NewTextHandler(io.Discard, nil)))
			w := httptest.NewRecorder()
			h.Check(w, httptest.NewRequest(http.MethodGet, "/health", nil))
			want := 200
			if failed != "" {
				want = 503
			}
			if w.Code != want {
				t.Fatalf("status=%d", w.Code)
			}
			var response healthResponse
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Checks) != 5 {
				t.Fatal("missing readiness checks")
			}
			for name, status := range response.Checks {
				want := "ok"
				if name == failed {
					want = "error"
				}
				if status != want {
					t.Fatalf("%s=%s", name, status)
				}
			}
		})
	}
}
