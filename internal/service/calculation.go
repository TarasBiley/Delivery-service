package service

import (
	"context"
	"delivery-service/internal/messaging"
	"delivery-service/internal/model"
	"delivery-service/internal/tracing"
	"fmt"
	"github.com/shopspring/decimal"
)

const calculationBatchSize = 100

type exchangeRateProvider interface {
	GetUSDRUB(context.Context) (decimal.Decimal, error)
}
type recoveryRepository interface {
	GetRecoveryBatch(context.Context, string, int) ([]model.CalculationRecord, error)
}
type calculationProcessor interface {
	Process(context.Context, messaging.PackageMessage) error
}

// CalculationService provides manual recovery through the same WorkerService.
type CalculationService struct {
	packages recoveryRepository
	worker   calculationProcessor
}

// NewCalculationService creates the debug recovery service.
func NewCalculationService(packages recoveryRepository, worker calculationProcessor) *CalculationService {
	return &CalculationService{packages: packages, worker: worker}
}

// CalculatePending is manual recovery only. Canonical calculated records are
// replayed to repair a failed Mongo write; WorkerService never recalculates them.
// Legacy calculated records without rate/time metadata cannot be reconstructed.
func (s *CalculationService) CalculatePending(ctx context.Context) (int, error) {
	total, after := 0, ""
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		records, err := s.packages.GetRecoveryBatch(ctx, after, calculationBatchSize)
		if err != nil {
			return total, fmt.Errorf("load recovery batch: %w", err)
		}
		if len(records) == 0 {
			return total, nil
		}
		for _, p := range records {
			m := messaging.PackageMessage{SchemaVersion: messaging.PackageMessageVersion, PackageID: p.PackageID, SessionID: p.SessionID, Name: p.Name, WeightKG: p.WeightKG.String(), PackageTypeID: p.PackageTypeID, ContentValueUSD: p.ContentValueUSD.String(), CreatedAt: p.CreatedAt, TraceID: tracing.ID(ctx)}
			if err := s.worker.Process(ctx, m); err != nil {
				return total, fmt.Errorf("recover package %s: %w", p.PackageID, err)
			}
			if p.Status == "calculation_pending" {
				total++
			}
			after = p.PackageID
		}
	}
}
