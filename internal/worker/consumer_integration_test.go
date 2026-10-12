//go:build integration

package worker

import (
	"context"
	"delivery-service/internal/messaging"
	"delivery-service/internal/model"
	"delivery-service/internal/service"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/shopspring/decimal"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"
)

type logEvents struct{ events chan string }

func (w *logEvents) Write(b []byte) (int, error) {
	var row struct {
		Message string `json:"msg"`
	}
	_ = json.Unmarshal(b, &row)
	select {
	case w.events <- row.Message:
	default:
	}
	return len(b), nil
}

type rabbitTest struct {
	conn   *amqp.Connection
	top    messaging.Topology
	pub    *messaging.Publisher
	ctx    context.Context
	events *logEvents
	logger *slog.Logger
}

func workerRabbit(t *testing.T) *rabbitTest {
	t.Helper()
	url := os.Getenv("TEST_RABBITMQ_URL")
	if url == "" {
		t.Skip("set TEST_RABBITMQ_URL for consumer integration tests")
	}
	conn, err := messaging.NewRabbitMQ(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	suffix := uuid.NewString()
	top := messaging.Topology{Main: "test.worker." + suffix, Retry: "test.retry." + suffix, DLQ: "test.dlq." + suffix, RetryDelay: 30 * time.Millisecond}
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	if err = top.Declare(ch); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupConn := conn
		if conn.IsClosed() {
			var err error
			cleanupConn, err = messaging.NewRabbitMQ(url)
			if err != nil {
				t.Error(err)
				return
			}
			defer cleanupConn.Close()
		}
		for _, q := range []string{top.Main, top.Retry, top.DLQ} {
			cleanup, err := cleanupConn.Channel()
			if err != nil {
				return
			}
			_, _ = cleanup.QueueDelete(q, false, false, false)
			_ = cleanup.Close()
		}
	})
	pub, err := messaging.NewPublisher(conn, top)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pub.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	events := &logEvents{events: make(chan string, 100)}
	return &rabbitTest{conn: conn, top: top, pub: pub, ctx: ctx, events: events, logger: slog.New(slog.NewJSONHandler(events, nil))}
}
func (r *rabbitTest) start(t *testing.T, p processor, max int) (*Consumer, context.CancelFunc, <-chan error) {
	t.Helper()
	c, err := NewConsumer(r.conn, p, r.pub, r.top, max, r.logger)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(r.ctx)
	result := make(chan error, 1)
	go func() { result <- c.Run(ctx) }()
	t.Cleanup(func() { cancel() })
	return c, cancel, result
}
func (r *rabbitTest) awaitLog(t *testing.T, want string) {
	t.Helper()
	for {
		select {
		case event := <-r.events.events:
			if event == want {
				return
			}
		case <-r.ctx.Done():
			t.Fatalf("timeout waiting for log %s", want)
		}
	}
}
func (r *rabbitTest) receive(t *testing.T, queue string) amqp.Delivery {
	t.Helper()
	ch, err := r.conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	d, err := ch.ConsumeWithContext(r.ctx, queue, "", true, false, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-d:
		return got
	case <-r.ctx.Done():
		t.Fatal("timed out waiting for delivery")
		return amqp.Delivery{}
	}
}
func stopConsumer(t *testing.T, cancel context.CancelFunc, result <-chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("consumer leaked")
	}
}
func TestRabbitConsumerRetriesToDLQ(t *testing.T) {
	r := workerRabbit(t)
	var mu sync.Mutex
	calls := 0
	p := processFunc(func(context.Context, messaging.PackageMessage) error {
		mu.Lock()
		calls++
		mu.Unlock()
		return errors.New("exchange offline")
	})
	_, cancel, result := r.start(t, p, 2)
	m := message()
	if err := r.pub.Publish(r.ctx, m); err != nil {
		t.Fatal(err)
	}
	got := r.receive(t, r.top.DLQ)
	stopConsumer(t, cancel, result)
	mu.Lock()
	defer mu.Unlock()
	if calls != 3 || got.Headers[messaging.RetryAttemptHeader] != int32(2) || got.MessageId != m.PackageID {
		t.Fatalf("calls=%d headers=%v", calls, got.Headers)
	}
}
func TestRabbitConsumerInvalidMessagesToDLQ(t *testing.T) {
	for _, tc := range []struct{ name, body string }{{"JSON", "{"}, {"version", `{"schema_version":2}`}, {"required", "{}"}, {"UUID", `{"schema_version":1,"package_id":"bad"}`}, {"decimal", `{"weight_kg":"bad"}`}} {
		t.Run(tc.name, func(t *testing.T) {
			r := workerRabbit(t)
			_, cancel, result := r.start(t, processFunc(func(context.Context, messaging.PackageMessage) error {
				t.Error("invalid message reached worker")
				return nil
			}), 2)
			if err := r.pub.PublishRaw(r.ctx, r.top.Main, amqp.Publishing{Body: []byte(tc.body), MessageId: "invalid", DeliveryMode: amqp.Persistent}); err != nil {
				t.Fatal(err)
			}
			got := r.receive(t, r.top.DLQ)
			stopConsumer(t, cancel, result)
			if string(got.Body) != tc.body {
				t.Fatal("original body lost")
			}
		})
	}
}
func TestRabbitConsumerForwardFailureLeavesDeliveryUnacked(t *testing.T) {
	for _, target := range []string{"retry", "dlq"} {
		t.Run(target, func(t *testing.T) {
			r := workerRabbit(t)
			ch, err := r.conn.Channel()
			if err != nil {
				t.Fatal(err)
			}
			defer ch.Close()
			queue := r.top.Retry
			if target == "dlq" {
				queue = r.top.DLQ
			}
			if _, err = ch.QueueDelete(queue, false, false, false); err != nil {
				t.Fatal(err)
			}
			_, cancel, result := r.start(t, processFunc(func(context.Context, messaging.PackageMessage) error { return errors.New("temporary") }), 2)
			defer cancel()
			m := message()
			if target == "dlq" {
				if err = r.pub.PublishRaw(r.ctx, r.top.Main, amqp.Publishing{Body: []byte("{"), MessageId: m.PackageID}); err != nil {
					t.Fatal(err)
				}
			} else if err = r.pub.Publish(r.ctx, m); err != nil {
				t.Fatal(err)
			}
			select {
			case err = <-result:
				if !errors.Is(err, messaging.ErrUnroutable) {
					t.Fatalf("forward error=%v", err)
				}
			case <-r.ctx.Done():
				t.Fatal("consumer did not terminate")
			}
			d, ok, err := ch.Get(r.top.Main, true)
			if err != nil || !ok || !d.Redelivered || d.MessageId != m.PackageID {
				t.Fatalf("unacked message lost: ok=%v redelivered=%v err=%v", ok, d.Redelivered, err)
			}
		})
	}
}
func TestRabbitConsumerPrefetchAndSuccessACK(t *testing.T) {
	r := workerRabbit(t)
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	p := processFunc(func(ctx context.Context, _ messaging.PackageMessage) error {
		entered <- struct{}{}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	for i := 0; i < 2; i++ {
		if err := r.pub.Publish(r.ctx, message()); err != nil {
			t.Fatal(err)
		}
	}
	_, cancel, result := r.start(t, p, 2)
	defer cancel()
	select {
	case <-entered:
	case <-r.ctx.Done():
		t.Fatal("consumer did not process")
	}
	ch, err := r.conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	q, err := ch.QueueInspect(r.top.Main)
	if err != nil || q.Messages != 1 {
		t.Fatalf("prefetch: queued=%d err=%v", q.Messages, err)
	}
	close(release)
	r.awaitLog(t, "package processed")
	r.awaitLog(t, "package processed")
	stopConsumer(t, cancel, result)
	if _, ok, err := ch.Get(r.top.Main, true); err != nil || ok {
		t.Fatalf("success was not ACKed: %v", err)
	}
}

// These fakes exercise the real WorkerService over real RabbitMQ retry delivery.
type canonicalPackages struct {
	record *model.CalculationRecord
	saves  int
}

func (f *canonicalPackages) GetCalculation(context.Context, string) (model.CalculationRecord, error) {
	if f.record == nil {
		return model.CalculationRecord{}, pgx.ErrNoRows
	}
	return *f.record, nil
}
func (f *canonicalPackages) SaveCalculation(_ context.Context, p model.CalculationRecord) (model.CalculationRecord, error) {
	f.saves++
	p.PackageTypeName = "clothing"
	f.record = &p
	return p, nil
}

type canonicalRate struct{ calls int }

func (f *canonicalRate) GetUSDRUB(context.Context) (decimal.Decimal, error) {
	f.calls++
	return decimal.NewFromInt(100), nil
}

type failingOnceLog struct {
	calls  int
	record model.CalculationLog
}

func (f *failingOnceLog) Upsert(_ context.Context, l model.CalculationLog) error {
	f.calls++
	if f.calls == 1 {
		return errors.New("mongo briefly offline")
	}
	f.record = l
	return nil
}
func TestRabbitConsumerWorkerServiceRedelivery(t *testing.T) {
	r := workerRabbit(t)
	p := &canonicalPackages{}
	rate := &canonicalRate{}
	logs := &failingOnceLog{}
	s, err := service.NewWorkerService(p, rate, logs)
	if err != nil {
		t.Fatal(err)
	}
	_, cancel, result := r.start(t, s, 2)
	if err = r.pub.Publish(r.ctx, message()); err != nil {
		t.Fatal(err)
	}
	r.awaitLog(t, "package processed")
	stopConsumer(t, cancel, result)
	if rate.calls != 1 || p.saves != 1 || logs.calls != 2 || !logs.record.Package.DeliveryCostRUB.Equal(*p.record.DeliveryCostRUB) || !logs.record.Package.CalculatedAt.Equal(*p.record.CalculatedAt) {
		t.Fatal("retry recalculated canonical result")
	}
}

func TestRabbitConsumerConnectionLoss(t *testing.T) {
	r := workerRabbit(t)
	_, cancel, result := r.start(t, processFunc(func(context.Context, messaging.PackageMessage) error { return nil }), 2)
	defer cancel()
	if err := r.conn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("connection loss not reported")
		}
	case <-r.ctx.Done():
		t.Fatal("consumer leaked after connection loss")
	}
}
