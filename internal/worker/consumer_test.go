package worker

import (
	"context"
	"delivery-service/internal/messaging"
	"delivery-service/internal/repository"
	"delivery-service/internal/service"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"io"
	"log/slog"
	"testing"
	"time"
)

type processFunc func(context.Context, messaging.PackageMessage) error

func (f processFunc) Process(ctx context.Context, m messaging.PackageMessage) error { return f(ctx, m) }

type forwardFake struct {
	err    error
	queue  string
	msg    amqp.Publishing
	called bool
}

func (f *forwardFake) PublishRaw(_ context.Context, q string, m amqp.Publishing) error {
	f.called = true
	f.queue = q
	f.msg = m
	return f.err
}

type ackFake struct {
	acks   int
	err    error
	before func()
}

func (f *ackFake) Ack(_ uint64, multiple bool) error {
	if multiple {
		return errors.New("multiple ACK")
	}
	if f.before != nil {
		f.before()
	}
	f.acks++
	return f.err
}
func (f *ackFake) Nack(uint64, bool, bool) error { return errors.New("unexpected NACK") }
func (f *ackFake) Reject(uint64, bool) error     { return errors.New("unexpected reject") }
func message() messaging.PackageMessage {
	return messaging.PackageMessage{SchemaVersion: 1, PackageID: uuid.NewString(), SessionID: uuid.NewString(), Name: "Phone", WeightKG: "1", PackageTypeID: 1, ContentValueUSD: "10", CreatedAt: time.Now().UTC(), TraceID: uuid.NewString()}
}
func testConsumer(t *testing.T, p processor, pub publisher) *Consumer {
	t.Helper()
	c, err := NewConsumer(nil, p, pub, messaging.DefaultTopology(), 2, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestConsumerRoutingAndACK(t *testing.T) {
	failure := errors.New("temporary failure")
	for _, tc := range []struct {
		name                   string
		body                   string
		attempt                int32
		processErr, forwardErr error
		want                   string
		wantAck                int
	}{
		{name: "success", wantAck: 1}, {name: "temporary", processErr: failure, want: "retry", wantAck: 1},
		{name: "retry limit", attempt: 2, processErr: failure, want: "dlq", wantAck: 1},
		{name: "Mongo conflict", processErr: repository.ErrCalculationLogConflict, want: "dlq", wantAck: 1},
		{name: "permanent", processErr: service.ErrPackageIdentityConflict, want: "dlq", wantAck: 1},
		{name: "invalid JSON", body: "{", want: "dlq", wantAck: 1},
		{name: "invalid message", body: `{"schema_version":2}`, want: "dlq", wantAck: 1},
		{name: "retry publish failure", processErr: failure, forwardErr: failure, want: "retry"},
		{name: "DLQ publish failure", body: "{", forwardErr: failure, want: "dlq"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := message()
			body, _ := json.Marshal(m)
			if tc.body != "" {
				body = []byte(tc.body)
			}
			pub := &forwardFake{err: tc.forwardErr}
			called := false
			c := testConsumer(t, processFunc(func(_ context.Context, got messaging.PackageMessage) error {
				called = true
				if got.PackageID != m.PackageID {
					t.Fatal("wrong message")
				}
				return tc.processErr
			}), pub)
			ack := &ackFake{before: func() {
				if tc.want != "" && !pub.called {
					t.Fatal("ACK before confirmed forwarding")
				}
			}}
			d := amqp.Delivery{Acknowledger: ack, DeliveryTag: 1, Body: body, MessageId: m.PackageID, ContentType: "application/json", Headers: amqp.Table{messaging.RetryAttemptHeader: tc.attempt, "trace_id": m.TraceID}}
			err := c.handle(context.Background(), d)
			if !errors.Is(err, tc.forwardErr) {
				t.Fatalf("error=%v", err)
			}
			if ack.acks != tc.wantAck {
				t.Fatalf("ACK count=%d", ack.acks)
			}
			if tc.body != "" && called {
				t.Fatal("invalid message reached worker")
			}
			if tc.want != "" {
				want := c.topology.Retry
				if tc.want == "dlq" {
					want = c.topology.DLQ
				}
				if pub.queue != want || string(pub.msg.Body) != string(body) || pub.msg.DeliveryMode != amqp.Persistent || pub.msg.MessageId != m.PackageID {
					t.Fatalf("forward=%s %+v", pub.queue, pub.msg)
				}
				if tc.want == "retry" && pub.msg.Headers[messaging.RetryAttemptHeader] != tc.attempt+1 {
					t.Fatal("attempt not incremented")
				}
			}
		})
	}
}

type channelFake struct {
	deliveries chan amqp.Delivery
	closed     bool
	prefetch   int
	err        error
}

func (f *channelFake) Qos(n, size int, global bool) error { f.prefetch = n; return f.err }
func (f *channelFake) Consume(_ string, _ string, autoAck, exclusive, noLocal, noWait bool, _ amqp.Table) (<-chan amqp.Delivery, error) {
	if autoAck {
		return nil, errors.New("autoAck enabled")
	}
	return f.deliveries, f.err
}
func (f *channelFake) Close() error { f.closed = true; return nil }
func TestConsumerCancellationLeavesUnfinishedUnacked(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	pub := &forwardFake{}
	c := testConsumer(t, processFunc(func(ctx context.Context, _ messaging.PackageMessage) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}), pub)
	m := message()
	body, _ := json.Marshal(m)
	ack := &ackFake{}
	ch := &channelFake{deliveries: make(chan amqp.Delivery, 1)}
	ch.deliveries <- amqp.Delivery{Acknowledger: ack, Body: body}
	c.open = func() (consumerChannel, error) { return ch, nil }
	result := make(chan error, 1)
	go func() { result <- c.Run(ctx) }()
	<-entered
	cancel()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if !ch.closed || ack.acks != 0 || pub.called || ch.prefetch != 1 {
		t.Fatal("unfinished delivery acknowledged or channel leaked")
	}
}
func TestConsumerChannelLossAndACKFailure(t *testing.T) {
	c := testConsumer(t, processFunc(func(context.Context, messaging.PackageMessage) error { return nil }), &forwardFake{})
	ch := &channelFake{deliveries: make(chan amqp.Delivery)}
	close(ch.deliveries)
	c.open = func() (consumerChannel, error) { return ch, nil }
	if err := c.Run(context.Background()); !errors.Is(err, amqp.ErrClosed) {
		t.Fatal(err)
	}
	failure := errors.New("ACK failed")
	body, _ := json.Marshal(message())
	if err := c.handle(context.Background(), amqp.Delivery{Acknowledger: &ackFake{err: failure}, Body: body}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
}

func TestConsumerPermanentClassification(t *testing.T) {
	for _, err := range []error{service.ErrInvalidPackageMessage, service.ErrPackageIdentityConflict, service.ErrIncompleteCalculation} {
		if !IsPermanent(err) {
			t.Fatalf("not permanent: %v", err)
		}
	}
	if IsPermanent(errors.New("mongo unavailable")) {
		t.Fatal("infrastructure error classified permanent")
	}
}
func TestConsumerInvalidRetryHeaderIsPermanent(t *testing.T) {
	pub := &forwardFake{}
	c := testConsumer(t, processFunc(func(context.Context, messaging.PackageMessage) error {
		t.Fatal("bad header reached worker")
		return nil
	}), pub)
	body, _ := json.Marshal(message())
	ack := &ackFake{}
	if err := c.handle(context.Background(), amqp.Delivery{Acknowledger: ack, Body: body, Headers: amqp.Table{messaging.RetryAttemptHeader: "bad"}}); err != nil {
		t.Fatal(err)
	}
	if pub.queue != c.topology.DLQ || ack.acks != 1 {
		t.Fatal("invalid header not dead-lettered")
	}
}

func TestConsumerStopDrainsCurrentDelivery(t *testing.T) {
	entered, finish := make(chan struct{}), make(chan struct{})
	c := testConsumer(t, processFunc(func(ctx context.Context, _ messaging.PackageMessage) error {
		close(entered)
		<-finish
		if ctx.Err() != nil {
			t.Error("graceful stop canceled active work")
		}
		return nil
	}), &forwardFake{})
	body, _ := json.Marshal(message())
	first, second := &ackFake{}, &ackFake{}
	ch := &channelFake{deliveries: make(chan amqp.Delivery, 2)}
	ch.deliveries <- amqp.Delivery{Acknowledger: first, Body: body}
	ch.deliveries <- amqp.Delivery{Acknowledger: second, Body: body}
	c.open = func() (consumerChannel, error) { return ch, nil }
	result := make(chan error, 1)
	go func() { result <- c.Run(context.Background()) }()
	<-entered
	if !c.Ready() {
		t.Fatal("active consumer not ready")
	}
	if err := c.Run(context.Background()); err == nil {
		t.Fatal("duplicate consumer Run succeeded")
	}
	c.Stop()
	c.Stop()
	if c.Ready() {
		t.Fatal("stopping consumer still ready")
	}
	close(finish)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if first.acks != 1 || second.acks != 0 || !ch.closed || c.Ready() {
		t.Fatal("wrong drain/ACK/readiness")
	}
}
