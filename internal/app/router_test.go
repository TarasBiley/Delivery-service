package app

import (
	"delivery-service/internal/handler"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBundledWebRoutes(t *testing.T) {
	mux := newRouter(routerHandlers{systemHandler: handler.NewSystemHandler(slog.New(slog.NewTextHandler(io.Discard, nil)))}, false)
	for _, tc := range []struct {
		path     string
		status   int
		contains string
	}{{"/", 200, "Delivery Service"}, {"/static/app.js", 200, "pollPackage"}, {"/static/styles.css", 200, ":root"}, {"/missing", 404, "NOT_FOUND"}, {"/api/v1/admin/calculate-pending", 404, "NOT_FOUND"}} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) {
			t.Fatalf("%s: status=%d body=%s", tc.path, w.Code, w.Body.String())
		}
	}
}
