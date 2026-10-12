package messaging

import (
	"errors"
	amqp "github.com/rabbitmq/amqp091-go"
	"testing"
)

type topologyFake struct {
	names []string
	args  []amqp.Table
	err   error
}

func (f *topologyFake) QueueDeclare(name string, durable, autoDelete, exclusive, noWait bool, args amqp.Table) (amqp.Queue, error) {
	if !durable || autoDelete || exclusive || noWait {
		return amqp.Queue{}, errors.New("wrong queue flags")
	}
	f.names = append(f.names, name)
	f.args = append(f.args, args)
	return amqp.Queue{}, f.err
}
func TestTopologyDurableRetryRouting(t *testing.T) {
	top := DefaultTopology()
	f := &topologyFake{}
	if err := top.Declare(f); err != nil {
		t.Fatal(err)
	}
	if len(f.names) != 3 {
		t.Fatal(f.names)
	}
	retry := f.args[2]
	if retry["x-message-ttl"] != int64(5000) || retry["x-dead-letter-exchange"] != "" || retry["x-dead-letter-routing-key"] != top.Main {
		t.Fatalf("retry=%v", retry)
	}
	failure := errors.New("broker unavailable")
	f.err = failure
	if err := top.Declare(f); !errors.Is(err, failure) {
		t.Fatal(err)
	}
}
