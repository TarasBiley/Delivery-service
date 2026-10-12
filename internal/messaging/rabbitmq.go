package messaging

import (
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

// NewRabbitMQ opens and verifies a connection; reconnect is owned by app lifecycle.
func NewRabbitMQ(
	url string,
) (*amqp.Connection, error) {

	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf(
			"connect rabbitmq: %w",
			err,
		)
	}

	// Проверяем, что соединение реально позволяет открыть AMQP channel.
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()

		return nil, fmt.Errorf(
			"open rabbitmq channel: %w",
			err,
		)
	}

	_ = ch.Close()

	return conn, nil
}
