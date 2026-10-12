package messaging

import (
	"fmt"
	amqp "github.com/rabbitmq/amqp091-go"
	"time"
)

const (
	PackageQueue          = "packages.to_calculate"
	DefaultMaxRetries     = 3
	DefaultRetryDelay     = 5 * time.Second
	DefaultConfirmTimeout = 5 * time.Second
	RetryAttemptHeader    = "x-retry-attempt"
)

// Topology describes durable processing, delayed retry and dead-letter queues.
type Topology struct {
	Main, Retry, DLQ string
	RetryDelay       time.Duration
}

// DefaultTopology returns the application queues and retry delay.
func DefaultTopology() Topology {
	return Topology{Main: PackageQueue, Retry: PackageQueue + ".retry", DLQ: PackageQueue + ".dlq", RetryDelay: DefaultRetryDelay}
}

type topologyChannel interface {
	QueueDeclare(string, bool, bool, bool, bool, amqp.Table) (amqp.Queue, error)
}

func (t Topology) Validate() error {
	if t.Main == "" || t.Retry == "" || t.DLQ == "" || t.Main == t.Retry || t.Main == t.DLQ || t.Retry == t.DLQ {
		return fmt.Errorf("topology needs three distinct queue names")
	}
	if t.RetryDelay < time.Millisecond {
		return fmt.Errorf("retry delay must be at least 1ms")
	}
	return nil
}

// Declare idempotently creates all queues with the configured durability and routing.
func (t Topology) Declare(ch topologyChannel) error {
	if err := t.Validate(); err != nil {
		return err
	}
	for _, q := range []struct {
		name string
		args amqp.Table
	}{
		{t.Main, nil}, {t.DLQ, nil}, {t.Retry, amqp.Table{"x-message-ttl": int64(t.RetryDelay / time.Millisecond), "x-dead-letter-exchange": "", "x-dead-letter-routing-key": t.Main}},
	} {
		if _, err := ch.QueueDeclare(q.name, true, false, false, false, q.args); err != nil {
			return fmt.Errorf("declare %s: %w", q.name, err)
		}
	}
	return nil
}
