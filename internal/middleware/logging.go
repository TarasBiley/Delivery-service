package middleware

import (
	"context"
	"delivery-service/internal/tracing"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}

	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}

	return w.ResponseWriter.Write(data)
}

func Logging(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			startedAt := time.Now()

			traceID := uuid.NewString()

			ctx := tracing.WithID(r.Context(), traceID)

			w.Header().Set("X-Trace-ID", traceID)

			recorder := &responseWriter{
				ResponseWriter: w,
			}

			next.ServeHTTP(
				recorder,
				r.WithContext(ctx),
			)

			status := recorder.status
			if status == 0 {
				status = http.StatusOK
			}

			logger.Info(
				"HTTP request",
				"trace_id", traceID,
				"method", r.Method,
				"path", r.URL.Path,
				"status", status,
				"duration_ms", time.Since(startedAt).Milliseconds(),
			)
		})
	}
}

func TraceID(ctx context.Context) string {
	return tracing.ID(ctx)
}
