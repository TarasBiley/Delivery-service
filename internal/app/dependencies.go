package app

import (
	"context"
	"delivery-service/internal/cache"
	"delivery-service/internal/config"
	"delivery-service/internal/database"
	"delivery-service/internal/exchange"
	"delivery-service/internal/handler"
	"delivery-service/internal/messaging"
	"delivery-service/internal/repository"
	"delivery-service/internal/service"
	"delivery-service/internal/validation"
	"delivery-service/internal/worker"
	"errors"
	"fmt"
	"github.com/go-playground/validator/v10"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"log/slog"
	"time"
)

type dependencies struct {
	db              *pgxpool.Pool
	redisClient     *redis.Client
	mongoClient     *mongo.Client
	rabbitConn      *amqp.Connection
	publisher       *messaging.Publisher
	consumer        *worker.Consumer
	handlers        routerHandlers
	shutdownTimeout time.Duration
}

func newDependencies(ctx context.Context, cfg config.Config, logger *slog.Logger) (result *dependencies, err error) {
	d := &dependencies{shutdownTimeout: cfg.HTTPShutdownTimeout}
	defer func() {
		if err != nil {
			d.Close()
		}
	}()
	validate := validator.New()
	if err = validation.Register(validate); err != nil {
		return nil, fmt.Errorf("register validation: %w", err)
	}
	if d.db, err = database.NewPostgres(ctx, cfg.DatabaseURL); err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	if d.redisClient, err = cache.NewRedis(ctx, cfg.RedisAddr); err != nil {
		return nil, fmt.Errorf("connect redis: %w", err)
	}
	if d.mongoClient, err = database.NewMongo(ctx, cfg.MongoURI); err != nil {
		return nil, fmt.Errorf("connect mongo: %w", err)
	}
	if d.rabbitConn, err = messaging.NewRabbitMQ(cfg.RabbitMQURL); err != nil {
		return nil, fmt.Errorf("connect rabbitmq: %w", err)
	}
	topology := messaging.DefaultTopology()
	ch, err := d.rabbitConn.Channel()
	if err != nil {
		return nil, fmt.Errorf("open topology channel: %w", err)
	}
	err = topology.Declare(ch)
	closeErr := ch.Close()
	if err != nil {
		return nil, fmt.Errorf("declare topology: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close topology channel: %w", closeErr)
	}
	packages := repository.NewPackageRepository(d.db)
	types := repository.NewPackageTypeRepository(d.db)
	logs := repository.NewCalculationLogRepository(d.mongoClient.Database(cfg.MongoDB))
	indexCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = logs.EnsureIndexes(indexCtx)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("ensure calculation indexes: %w", err)
	}
	rates := service.NewExchangeRateService(cache.NewExchangeRateCache(d.redisClient), exchange.NewClient(cfg.CBRURL), logger)
	if d.publisher, err = messaging.NewPublisher(d.rabbitConn, topology); err != nil {
		return nil, err
	}
	processor, err := service.NewWorkerService(packages, rates, logs)
	if err != nil {
		return nil, err
	}
	if d.consumer, err = worker.NewConsumer(d.rabbitConn, processor, d.publisher, topology, messaging.DefaultMaxRetries, logger); err != nil {
		return nil, err
	}
	packageService := service.NewPackageService(packages, types, d.publisher, repository.NewIdempotencyRepository(d.db))
	recovery := service.NewCalculationService(packages, processor)
	health := handler.NewHealthHandler(map[string]handler.HealthCheck{
		"postgres": d.db.Ping,
		"redis":    func(ctx context.Context) error { return d.redisClient.Ping(ctx).Err() },
		"mongo":    func(ctx context.Context) error { return d.mongoClient.Ping(ctx, readpref.Primary()) },
		"rabbitmq": func(context.Context) error {
			if d.rabbitConn.IsClosed() {
				return amqp.ErrClosed
			}
			return nil
		},
		"consumer": func(context.Context) error {
			if !d.consumer.Ready() {
				return errors.New("consumer is not ready")
			}
			return nil
		},
	}, logger)
	d.handlers = routerHandlers{
		carrierHandler:     handler.NewCarrierHandler(service.NewCarrierService(packages), validate, logger),
		analyticsHandler:   handler.NewAnalyticsHandler(service.NewAnalyticsService(logs), logger),
		packageHandler:     handler.NewPackageHandler(packageService, validate, logger),
		packageTypeHandler: handler.NewPackageTypeHandler(service.NewPackageTypeService(types, cache.NewPackageTypeCache()), logger),
		adminHandler:       handler.NewAdminHandler(recovery, logger),
		systemHandler:      handler.NewSystemHandler(logger), healthHandler: health,
	}
	logger.Info("dependencies initialized; RabbitMQ topology and Mongo indexes ready")
	return d, nil
}
func (d *dependencies) Close() {
	if d.publisher != nil {
		_ = d.publisher.Close()
	}
	if d.rabbitConn != nil {
		_ = d.rabbitConn.CloseDeadline(time.Now().Add(d.shutdownTimeout))
	}
	if d.mongoClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), d.shutdownTimeout)
		_ = d.mongoClient.Disconnect(ctx)
		cancel()
	}
	if d.redisClient != nil {
		_ = d.redisClient.Close()
	}
	if d.db != nil {
		d.db.Close()
	}
}
