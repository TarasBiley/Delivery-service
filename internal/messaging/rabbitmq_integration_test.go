//go:build integration

package messaging

import (
	"context"
	"errors"
	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"os"
	"sync"
	"testing"
	"time"
)

func rabbitFixture(t *testing.T) (*amqp.Connection, Topology, *Publisher) {
	t.Helper()
	url := os.Getenv("TEST_RABBITMQ_URL")
	if url == "" {
		t.Skip("set TEST_RABBITMQ_URL for RabbitMQ integration tests")
	}
	conn, err := NewRabbitMQ(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	suffix := uuid.NewString()
	top := Topology{Main: "test.packages." + suffix, Retry: "test.retry." + suffix, DLQ: "test.dlq." + suffix, RetryDelay: 50 * time.Millisecond}
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	if err = top.Declare(ch); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, err := conn.Channel()
		if err != nil {
			t.Error(err)
			return
		}
		defer cleanup.Close()
		for _, q := range []string{top.Main, top.Retry, top.DLQ} {
			if _, err = cleanup.QueueDelete(q, false, false, false); err != nil {
				t.Error(err)
			}
		}
	})
	pub, err := NewPublisher(conn, top)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pub.Close() })
	return conn, top, pub
}
func TestRabbitPublisherIntegration(t *testing.T) {
	conn, top, pub := rabbitFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m := validMessage()
	if err := pub.Publish(ctx, m); err != nil {
		t.Fatal(err)
	}
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	if err = top.Declare(ch); err != nil {
		t.Fatal(err)
	}
	d, ok, err := ch.Get(top.Main, true)
	if err != nil || !ok {
		t.Fatalf("message missing: %v", err)
	}
	if d.DeliveryMode != amqp.Persistent || d.ContentType != "application/json" || d.MessageId != m.PackageID || d.Timestamp.IsZero() {
		t.Fatalf("properties=%+v", d)
	}
	if err = pub.PublishRaw(ctx, "missing."+uuid.NewString(), amqp.Publishing{Body: []byte("test")}); !errors.Is(err, ErrUnroutable) {
		t.Fatalf("unroutable=%v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := pub.Publish(ctx, validMessage()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	for i := 0; i < 10; i++ {
		if _, ok, err = ch.Get(top.Main, true); err != nil || !ok {
			t.Fatalf("concurrent publish missing: %v", err)
		}
	}
}
func TestRabbitPublisherConnectionLoss(t *testing.T) {
	url := os.Getenv("TEST_RABBITMQ_URL")
	if url == "" {
		t.Skip("set TEST_RABBITMQ_URL")
	}
	conn, err := NewRabbitMQ(url)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := NewPublisher(conn, DefaultTopology())
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err = pub.Publish(context.Background(), validMessage()); err == nil {
		t.Fatal("closed connection publication succeeded")
	}
}
