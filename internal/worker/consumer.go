package worker

import (
	"context"
	"delivery-service/internal/messaging"
	"delivery-service/internal/repository"
	"delivery-service/internal/service"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-playground/validator/v10"
	"github.com/jackc/pgx/v5/pgconn"
	amqp "github.com/rabbitmq/amqp091-go"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
)

type processor interface {
	Process(context.Context, messaging.PackageMessage) error
}
type publisher interface {
	PublishRaw(context.Context, string, amqp.Publishing) error
}
type consumerChannel interface {
	Qos(int, int, bool) error
	Consume(string, string, bool, bool, bool, bool, amqp.Table) (<-chan amqp.Delivery, error)
	Close() error
}

// Consumer processes deliveries with prefetch one, manual ACK, retries and DLQ.
type Consumer struct {
	open       func() (consumerChannel, error)
	processor  processor
	publisher  publisher
	topology   messaging.Topology
	maxRetries int
	validate   *validator.Validate
	logger     *slog.Logger
	mu         sync.Mutex
	running    bool
	ready      atomic.Bool
	stopping   chan struct{}
	stopOnce   sync.Once
}

// NewConsumer creates a managed consumer without starting it.
func NewConsumer(conn *amqp.Connection, p processor, pub publisher, t messaging.Topology, maxRetries int, logger *slog.Logger) (*Consumer, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	if maxRetries < 0 {
		return nil, errors.New("max retries must not be negative")
	}
	v, err := messaging.NewPackageMessageValidator()
	if err != nil {
		return nil, err
	}
	return &Consumer{stopping: make(chan struct{}), open: func() (consumerChannel, error) { return conn.Channel() }, processor: p, publisher: pub, topology: t, maxRetries: maxRetries, validate: v, logger: logger}, nil
}

// Ready reports an active subscription, without publishing probe messages.
func (c *Consumer) Ready() bool { return c.ready.Load() }

// Stop stops processing new deliveries while allowing the current operation to
// finish. The Run context supplies the deadline for that operation.
func (c *Consumer) Stop() { c.ready.Store(false); c.stopOnce.Do(func() { close(c.stopping) }) }

// Run is blocking. The owner cancels ctx and waits for Run before closing the
// publisher/connection. Any fatal error is returned to that owner; no hot reconnect.
func (c *Consumer) Run(ctx context.Context) (err error) {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return errors.New("consumer already running")
	}
	c.running = true
	c.mu.Unlock()
	defer c.ready.Store(false)
	defer func() { c.mu.Lock(); c.running = false; c.mu.Unlock() }()
	if ctx.Err() != nil {
		return nil
	}
	ch, err := c.open()
	if err != nil {
		return fmt.Errorf("open consumer channel: %w", err)
	}
	defer func() {
		c.ready.Store(false)
		closeErr := ch.Close()
		if err == nil && ctx.Err() == nil && closeErr != nil && !errors.Is(closeErr, amqp.ErrClosed) {
			err = fmt.Errorf("close consumer channel: %w", closeErr)
		}
	}()
	if err = ch.Qos(1, 0, false); err != nil {
		return fmt.Errorf("set consumer prefetch: %w", err)
	}
	// Channel ownership includes cancellation. ConsumeWithContext would run a
	// competing Cancel RPC during Close; use one shutdown path instead.
	deliveries, err := ch.Consume(c.topology.Main, "", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("start consumer: %w", err)
	}
	select {
	case <-c.stopping:
		return nil
	default:
	}
	c.ready.Store(true)
	for {
		select {
		case <-c.stopping:
			return nil
		case <-ctx.Done():
			return nil
		case d, ok := <-deliveries:
			if !ok {
				if ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("consumer deliveries closed: %w", amqp.ErrClosed)
			}
			if ctx.Err() != nil {
				return nil
			}
			select {
			case <-c.stopping:
				return nil
			default:
			}
			if err = c.handle(ctx, d); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		}
	}
}

// IsPermanent identifies validation and canonical identity errors that go directly to DLQ.
func IsPermanent(err error) bool {
	if errors.Is(err, service.ErrInvalidPackageMessage) || errors.Is(err, service.ErrPackageIdentityConflict) || errors.Is(err, service.ErrIncompleteCalculation) || errors.Is(err, repository.ErrCalculationIdentityConflict) || errors.Is(err, repository.ErrCalculationLogConflict) {
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23503", "23514", "22003", "22P02":
			return true
		}
	}
	return false
}
func retryAttempt(headers amqp.Table) (int, error) {
	value, ok := headers[messaging.RetryAttemptHeader]
	if !ok {
		return 0, nil
	}
	var n int64
	switch v := value.(type) {
	case int8:
		n = int64(v)
	case int16:
		n = int64(v)
	case int32:
		n = int64(v)
	case int64:
		n = v
	case int:
		n = int64(v)
	default:
		return 0, errors.New("invalid retry attempt header")
	}
	if n < 0 || n > 2147483647 {
		return 0, errors.New("invalid retry attempt range")
	}
	return int(n), nil
}
func (c *Consumer) handle(ctx context.Context, d amqp.Delivery) error {
	var m messaging.PackageMessage
	err := json.Unmarshal(d.Body, &m)
	attempt, headerErr := retryAttempt(d.Headers)
	if err == nil {
		err = m.Validate(c.validate)
	}
	permanent := err != nil || headerErr != nil
	if err == nil && headerErr != nil {
		err = headerErr
	}
	if err == nil {
		err = c.processor.Process(ctx, m)
		permanent = IsPermanent(err)
	}
	trace, id := m.TraceID, m.PackageID
	if trace == "" {
		trace, _ = d.Headers["trace_id"].(string)
	}
	if id == "" {
		id = d.MessageId
	}
	attrs := []any{"trace_id", trace, "package_id", id, "retry_attempt", attempt}
	if err == nil {
		if ackErr := d.Ack(false); ackErr != nil {
			c.logger.Error("consumer ACK failed", append(attrs, "error", ackErr)...)
			return fmt.Errorf("ack delivery: %w", ackErr)
		}
		c.logger.Info("package processed", attrs...)
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	target := c.topology.Retry
	if permanent || attempt >= c.maxRetries {
		target = c.topology.DLQ
	}
	headers := amqp.Table{}
	for k, v := range d.Headers {
		headers[k] = v
	}
	if target == c.topology.Retry {
		headers[messaging.RetryAttemptHeader] = int32(attempt + 1)
	}
	headers["x-processing-error"] = err.Error()
	msg := amqp.Publishing{Headers: headers, ContentType: d.ContentType, ContentEncoding: d.ContentEncoding, DeliveryMode: amqp.Persistent, Priority: d.Priority, CorrelationId: d.CorrelationId, ReplyTo: d.ReplyTo, MessageId: d.MessageId, Timestamp: d.Timestamp, Type: d.Type, AppId: d.AppId, Body: d.Body}
	// Original expiration is deliberately cleared: retry queue TTL controls delay,
	// while DLQ messages must remain available for inspection/recovery.
	c.logger.Warn("package processing failed", append(attrs, "error", err, "target_queue", target)...)
	if publishErr := c.publisher.PublishRaw(ctx, target, msg); publishErr != nil {
		c.logger.Error("delivery forwarding failed", append(attrs, "error", publishErr, "target_queue", target)...)
		return fmt.Errorf("forward delivery to %s (attempt %s): %w", target, strconv.Itoa(attempt), publishErr)
	}
	if ackErr := d.Ack(false); ackErr != nil {
		c.logger.Error("forwarded delivery ACK failed", append(attrs, "error", ackErr)...)
		return fmt.Errorf("ack forwarded delivery: %w", ackErr)
	}
	return nil
}
