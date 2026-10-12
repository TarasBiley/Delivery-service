package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/shopspring/decimal"
)

type exchangeRateCache interface {
	Get(context.Context) (decimal.Decimal, bool, error)
	GetLast(context.Context) (decimal.Decimal, bool, error)
	Set(context.Context, decimal.Decimal) error
}

// ExchangeRateService uses Redis, CBR and the last known rate in that order.
type ExchangeRateService struct {
	cache  exchangeRateCache
	client exchangeRateProvider
	logger *slog.Logger
}

// NewExchangeRateService injects the rate cache and external provider.
func NewExchangeRateService(
	cache exchangeRateCache,
	client exchangeRateProvider,
	logger *slog.Logger,
) *ExchangeRateService {
	return &ExchangeRateService{
		cache:  cache,
		client: client,
		logger: logger,
	}
}

// GetUSDRUB returns a positive USD/RUB rate, falling back to the last known value.
func (s *ExchangeRateService) GetUSDRUB(
	ctx context.Context,
) (decimal.Decimal, error) {
	rate, found, err := s.cache.Get(ctx)
	if err != nil {
		s.logger.Warn(
			"failed to get USD/RUB rate from cache",
			"error", err,
		)
	}

	if found {
		return rate, nil
	}

	rate, err = s.client.GetUSDRUB(ctx)
	if err == nil {
		if cacheErr := s.cache.Set(ctx, rate); cacheErr != nil {
			s.logger.Warn(
				"failed to cache USD/RUB rate",
				"error", cacheErr,
			)
		}

		return rate, nil
	}

	// Если приложение завершается, это не ошибка внешнего сервиса.
	if ctx.Err() != nil {
		return decimal.Zero, ctx.Err()
	}

	s.logger.Warn(
		"failed to get USD/RUB rate from external source",
		"error", err,
	)

	lastRate, found, lastErr := s.cache.GetLast(ctx)
	if lastErr != nil {
		return decimal.Zero, fmt.Errorf(
			"get last known USD/RUB rate: %w",
			lastErr,
		)
	}

	if found {
		s.logger.Warn(
			"using last known USD/RUB rate",
			"rate", lastRate.String(),
		)

		return lastRate, nil
	}

	return decimal.Zero, fmt.Errorf(
		"USD/RUB rate is unavailable",
	)
}
