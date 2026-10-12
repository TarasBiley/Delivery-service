package app

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"delivery-service/internal/config"
	"delivery-service/internal/middleware"
)

func Run() int {
	logger := slog.New(
		slog.NewJSONHandler(os.Stdout, nil),
	)

	// Config
	cfg, err := config.Load()
	if err != nil {
		logger.Error(
			"failed to load config",
			"error", err,
		)

		return 1
	}

	// Application context
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	// Dependencies
	deps, err := newDependencies(
		ctx,
		cfg,
		logger,
	)
	if err != nil {
		logger.Error(
			"failed to initialize dependencies",
			"error", err,
		)

		return 1
	}
	defer deps.Close()

	// Router
	mux := newRouter(
		deps.handlers,
		cfg.DebugMode,
	)

	if cfg.DebugMode {
		logger.Info("debug admin routes enabled")
	}

	// Middleware
	handlerChain := middleware.Logging(logger)(
		middleware.Session(cfg.CookieSecure)(mux),
	)

	// HTTP server
	server := newHTTPServer(
		cfg,
		handlerChain,
		logger,
	)

	return runLifecycle(ctx, cfg.HTTPShutdownTimeout, server, deps.consumer, func() { _ = deps.rabbitConn.CloseDeadline(time.Now().Add(time.Second)) }, logger)
}

type lifecycleConsumer interface {
	Run(context.Context) error
	Stop()
}
type lifecycleServer interface {
	Start() <-chan error
	Shutdown(context.Context) error
}

func runLifecycle(ctx context.Context, timeout time.Duration, server lifecycleServer, consumer lifecycleConsumer, forceClose func(), logger *slog.Logger) int {
	// Signals stop intake first; active processing gets the shutdown grace period.
	workerCtx, cancelWorker := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelWorker()
	result := make(chan error, 1)
	done := make(chan struct{})
	go func() { defer close(done); result <- consumer.Run(workerCtx) }()
	serverErr := server.Start()
	code := 0
	select {
	case <-ctx.Done():
	case err := <-serverErr:
		logger.Error("HTTP server failed", "error", err)
		code = 1
	case err := <-result:
		logger.Error("consumer terminated unexpectedly", "error", err)
		code = 1
	}
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	httpDone := make(chan error, 1)
	go func() { httpDone <- server.Shutdown(shutdownCtx) }()
	consumer.Stop()
	select {
	case <-done:
	case <-shutdownCtx.Done():
		logger.Warn("consumer grace period expired; leaving unfinished delivery for redelivery")
		cancelWorker()
		forceClose()
		<-done
	}
	if err := <-httpDone; err != nil {
		logger.Error("HTTP shutdown failed", "error", err)
		code = 1
	}
	logger.Info("server stopped")
	return code
}
