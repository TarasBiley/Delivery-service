package messaging

import (
	"context"
	"errors"
	amqp "github.com/rabbitmq/amqp091-go"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type publishFake struct {
	confirms           chan amqp.Confirmation
	returns            chan amqp.Return
	pubErr, confirmErr error
	ack                bool
	returned           bool
	noConfirm          bool
	sent               chan struct{}
	msg                amqp.Publishing
	queue              string
	closed             bool
}

func (f *publishFake) Confirm(bool) error { return f.confirmErr }
func (f *publishFake) NotifyPublish(ch chan amqp.Confirmation) chan amqp.Confirmation {
	f.confirms = ch
	return ch
}
func (f *publishFake) NotifyReturn(ch chan amqp.Return) chan amqp.Return { f.returns = ch; return ch }
func (f *publishFake) PublishWithContext(_ context.Context, exchange, queue string, mandatory, immediate bool, m amqp.Publishing) error {
	if exchange != "" || !mandatory || immediate {
		return errors.New("wrong publish flags")
	}
	f.msg = m
	f.queue = queue
	if f.sent != nil {
		close(f.sent)
	}
	if f.pubErr != nil {
		return f.pubErr
	}
	if f.returned {
		f.returns <- amqp.Return{ReplyCode: 312, ReplyText: "NO_ROUTE"}
	}
	if !f.noConfirm {
		f.confirms <- amqp.Confirmation{DeliveryTag: 1, Ack: f.ack}
	}
	return nil
}
func (f *publishFake) Close() error { f.closed = true; return nil }
func testPublisher(f *publishFake) *Publisher {
	return &Publisher{open: func() (publishChannel, error) { return f, nil }, queue: PackageQueue, timeout: time.Second, done: make(chan struct{})}
}
func TestPublisherResults(t *testing.T) {
	failure := errors.New("publish failed")
	for _, tc := range []struct {
		name                        string
		pubErr, confirmErr, wantErr error
		ack, returned               bool
	}{
		{name: "confirmed", ack: true}, {name: "publish error", pubErr: failure, wantErr: failure},
		{name: "confirm setup error", confirmErr: failure, wantErr: failure}, {name: "negative confirm", wantErr: ErrNegativeConfirm},
		{name: "unroutable positive confirm", ack: true, returned: true, wantErr: ErrUnroutable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &publishFake{pubErr: tc.pubErr, confirmErr: tc.confirmErr, ack: tc.ack, returned: tc.returned}
			m := validMessage()
			err := testPublisher(f).Publish(context.Background(), m)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error=%v want=%v", err, tc.wantErr)
			}
			if !f.closed {
				t.Fatal("channel leaked")
			}
			if tc.confirmErr == nil && (f.msg.MessageId != m.PackageID || f.msg.ContentType != "application/json" || f.msg.DeliveryMode != amqp.Persistent || f.msg.Timestamp.IsZero() || f.queue != PackageQueue) {
				t.Fatalf("properties=%+v", f.msg)
			}
		})
	}
}
func TestPublisherMarshalError(t *testing.T) {
	f := &publishFake{}
	m := validMessage()
	m.CreatedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := testPublisher(f).Publish(context.Background(), m); err == nil {
		t.Fatal("expected marshal error")
	}
	if f.confirms != nil {
		t.Fatal("published invalid JSON")
	}
}
func TestPublisherCancellationAndClose(t *testing.T) {
	for _, stop := range []string{"context", "close"} {
		t.Run(stop, func(t *testing.T) {
			f := &publishFake{noConfirm: true, sent: make(chan struct{})}
			p := testPublisher(f)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- p.Publish(ctx, validMessage()) }()
			<-f.sent
			want := context.Canceled
			if stop == "context" {
				cancel()
			} else {
				_ = p.Close()
				want = ErrPublisherClosed
			}
			if err := <-result; !errors.Is(err, want) {
				t.Fatal(err)
			}
			if !f.closed {
				t.Fatal("channel leaked")
			}
		})
	}
}
func TestPublisherClosedConfirmationAndOpenFailure(t *testing.T) {
	failure := errors.New("connection lost")
	p := testPublisher(&publishFake{})
	p.open = func() (publishChannel, error) { return nil, failure }
	if err := p.Publish(context.Background(), validMessage()); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	f := &publishFake{noConfirm: true, sent: make(chan struct{})}
	p = testPublisher(f)
	result := make(chan error, 1)
	go func() { result <- p.Publish(context.Background(), validMessage()) }()
	<-f.sent
	close(f.confirms)
	if err := <-result; !errors.Is(err, amqp.ErrClosed) {
		t.Fatal(err)
	}
}
func TestPublisherConcurrentCallsOwnChannels(t *testing.T) {
	var count atomic.Int32
	p := testPublisher(nil)
	p.open = func() (publishChannel, error) { count.Add(1); return &publishFake{ack: true}, nil }
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := p.Publish(context.Background(), validMessage()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if count.Load() != 20 {
		t.Fatal("channels shared")
	}
}
