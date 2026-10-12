package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	amqp "github.com/rabbitmq/amqp091-go"
	"sync"
	"time"
)

var ErrUnroutable = errors.New("RabbitMQ message is unroutable")
var ErrNegativeConfirm = errors.New("RabbitMQ rejected publication")
var ErrPublisherClosed = errors.New("RabbitMQ publisher is closed")

type publishChannel interface {
	Confirm(bool) error
	NotifyPublish(chan amqp.Confirmation) chan amqp.Confirmation
	NotifyReturn(chan amqp.Return) chan amqp.Return
	PublishWithContext(context.Context, string, string, bool, bool, amqp.Publishing) error
	Close() error
}

// Publisher gives each call its own AMQP channel and notifications. No channel or confirm
// sequence is shared by concurrent HTTP requests or retry publications.
type Publisher struct {
	open      func() (publishChannel, error)
	queue     string
	timeout   time.Duration
	done      chan struct{}
	closeOnce sync.Once
}

// NewPublisher creates a confirmed mandatory publisher.
func NewPublisher(conn *amqp.Connection, t Topology) (*Publisher, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	return &Publisher{open: func() (publishChannel, error) { return conn.Channel() }, queue: t.Main, timeout: DefaultConfirmTimeout, done: make(chan struct{})}, nil
}

// Close stops new publications and releases confirmation waiters.
func (p *Publisher) Close() error { p.closeOnce.Do(func() { close(p.done) }); return nil }

// Publish publishes one persistent registration and waits for routing and confirmation.
func (p *Publisher) Publish(ctx context.Context, m PackageMessage) error {
	body, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal package message: %w", err)
	}
	return p.PublishRaw(ctx, p.queue, amqp.Publishing{ContentType: "application/json", DeliveryMode: amqp.Persistent, MessageId: m.PackageID, Timestamp: time.Now().UTC(), Body: body, Headers: amqp.Table{"trace_id": m.TraceID}})
}

// PublishRaw also forwards original delivery properties/body for retry and DLQ.
// A positive broker confirm alone is insufficient: mandatory returns are checked.
func (p *Publisher) PublishRaw(ctx context.Context, queue string, msg amqp.Publishing) error {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return ErrPublisherClosed
	default:
	}
	ch, err := p.open()
	if err != nil {
		return fmt.Errorf("open publisher channel: %w", err)
	}
	defer func() { _ = ch.Close() }()
	if err = ch.Confirm(false); err != nil {
		return fmt.Errorf("enable publisher confirms: %w", err)
	}
	confirms := ch.NotifyPublish(make(chan amqp.Confirmation, 1))
	returns := ch.NotifyReturn(make(chan amqp.Return, 1))
	if err = ch.PublishWithContext(ctx, "", queue, true, false, msg); err != nil {
		return fmt.Errorf("publish to %s: %w", queue, err)
	}
	var returned *amqp.Return
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait publisher confirm: %w", ctx.Err())
		case <-p.done:
			return ErrPublisherClosed
		case ret, ok := <-returns:
			if !ok {
				returns = nil
				continue
			}
			returned = &ret
		case confirm, ok := <-confirms:
			if !ok {
				return fmt.Errorf("publisher channel closed before confirm: %w", amqp.ErrClosed)
			}
			// RabbitMQ sends basic.return before basic.ack. The library dispatches the
			// return into our buffered listener before delivering the confirmation.
			if returns != nil {
				select {
				case ret, ok := <-returns:
					if ok {
						returned = &ret
					}
				default:
				}
			}
			if returned != nil {
				return fmt.Errorf("%w: %d %s (queue %s)", ErrUnroutable, returned.ReplyCode, returned.ReplyText, queue)
			}
			if !confirm.Ack {
				return ErrNegativeConfirm
			}
			return nil
		}
	}
}
