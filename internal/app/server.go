package app

import (
	"context"
	"log/slog"
	"net/http"

	"delivery-service/internal/config"
)

type httpServer struct {
	server *http.Server
	logger *slog.Logger
}

func newHTTPServer(
	cfg config.Config,
	handler http.Handler,
	logger *slog.Logger,
) *httpServer {
	return &httpServer{
		server: &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           handler,
			ReadHeaderTimeout: cfg.HTTPReadHeaderTimeout,
			ReadTimeout:       cfg.HTTPReadTimeout,
			WriteTimeout:      cfg.HTTPWriteTimeout,
			IdleTimeout:       cfg.HTTPIdleTimeout,
		},
		logger: logger,
	}
}

func (s *httpServer) Start() <-chan error {
	serverErr := make(chan error, 1)

	go func() {
		s.logger.Info(
			"HTTP server started",
			"addr", s.server.Addr,
		)

		if err := s.server.ListenAndServe(); err != nil &&
			err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	return serverErr
}

func (s *httpServer) Shutdown(
	ctx context.Context,
) error {
	err := s.server.Shutdown(ctx)
	if err != nil {
		_ = s.server.Close()
	}
	return err
}
