package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
)

const (
	usdRubKey     = "exchange:usd:rub"
	usdRubLastKey = "exchange:usd:rub:last"
)

type ExchangeRateCache struct {
	client *redis.Client
}

func NewExchangeRateCache(
	client *redis.Client,
) *ExchangeRateCache {
	return &ExchangeRateCache{
		client: client,
	}
}

func (c *ExchangeRateCache) Get(
	ctx context.Context,
) (decimal.Decimal, bool, error) {
	value, err := c.client.Get(
		ctx,
		usdRubKey,
	).Result()

	if err != nil {
		if err == redis.Nil {
			return decimal.Zero, false, nil
		}

		return decimal.Zero, false, fmt.Errorf(
			"get USD/RUB rate from redis: %w",
			err,
		)
	}

	rate, err := decimal.NewFromString(value)
	if err != nil {
		return decimal.Zero, false, fmt.Errorf(
			"parse USD/RUB rate from redis: %w",
			err,
		)
	}

	if rate.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero, false, fmt.Errorf(
			"invalid USD/RUB rate in redis",
		)
	}

	return rate, true, nil
}

func (c *ExchangeRateCache) GetLast(
	ctx context.Context,
) (decimal.Decimal, bool, error) {
	value, err := c.client.Get(
		ctx,
		usdRubLastKey,
	).Result()

	if err != nil {
		if err == redis.Nil {
			return decimal.Zero, false, nil
		}

		return decimal.Zero, false, fmt.Errorf(
			"get last USD/RUB rate from redis: %w",
			err,
		)
	}

	rate, err := decimal.NewFromString(value)
	if err != nil {
		return decimal.Zero, false, fmt.Errorf(
			"parse last USD/RUB rate from redis: %w",
			err,
		)
	}

	if rate.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero, false, fmt.Errorf(
			"invalid last USD/RUB rate in redis",
		)
	}

	return rate, true, nil
}

func (c *ExchangeRateCache) Set(
	ctx context.Context,
	rate decimal.Decimal,
) error {
	if rate.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("USD/RUB rate must be greater than 0")
	}

	value := rate.String()

	pipe := c.client.TxPipeline()

	pipe.Set(
		ctx,
		usdRubKey,
		value,
		time.Hour,
	)

	pipe.Set(
		ctx,
		usdRubLastKey,
		value,
		0,
	)

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf(
			"save USD/RUB rate to redis: %w",
			err,
		)
	}

	return nil
}
