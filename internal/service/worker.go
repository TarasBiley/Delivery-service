package service

import (
	"context"
	"delivery-service/internal/messaging"
	"delivery-service/internal/model"
	"errors"
	"fmt"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
	"time"
)

var ErrInvalidPackageMessage = errors.New("invalid package message")
var ErrPackageIdentityConflict = errors.New("package message conflicts with stored registration")
var ErrIncompleteCalculation = errors.New("calculated package lacks canonical metadata")

type workerPackageRepository interface {
	GetCalculation(context.Context, string) (model.CalculationRecord, error)
	SaveCalculation(context.Context, model.CalculationRecord) (model.CalculationRecord, error)
}
type calculationLogRepository interface {
	Upsert(context.Context, model.CalculationLog) error
}

// WorkerService calculates each package once and idempotently persists its Mongo log.
type WorkerService struct {
	packages workerPackageRepository
	rates    exchangeRateProvider
	logs     calculationLogRepository
	validate *validator.Validate
}

// NewWorkerService creates the shared message processing service.
func NewWorkerService(packages workerPackageRepository, rates exchangeRateProvider, logs calculationLogRepository) (*WorkerService, error) {
	v, err := messaging.NewPackageMessageValidator()
	if err != nil {
		return nil, fmt.Errorf("register message validation: %w", err)
	}
	return &WorkerService{packages: packages, rates: rates, logs: logs, validate: v}, nil
}

// Process saves a canonical calculation and its log before allowing acknowledgement.
func (s *WorkerService) Process(ctx context.Context, m messaging.PackageMessage) error {
	if err := m.Validate(s.validate); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidPackageMessage, err)
	}
	m.PackageID = uuid.MustParse(m.PackageID).String()
	m.SessionID = uuid.MustParse(m.SessionID).String()
	weight, _ := decimal.NewFromString(m.WeightKG)
	value, _ := decimal.NewFromString(m.ContentValueUSD)
	p, err := s.packages.GetCalculation(ctx, m.PackageID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("lookup package: %w", err)
	}
	if err == nil {
		if err = matchesMessage(p, m, weight, value); err != nil {
			return err
		}
	} else {
		p = model.CalculationRecord{PackageID: m.PackageID, SessionID: m.SessionID, Name: m.Name, PackageTypeID: m.PackageTypeID, WeightKG: weight, ContentValueUSD: value, CreatedAt: m.CreatedAt}
	}
	if p.Status != "calculated" {
		rate, err := s.rates.GetUSDRUB(ctx)
		if err != nil {
			return fmt.Errorf("get worker exchange rate: %w", err)
		}
		if !rate.IsPositive() {
			return errors.New("worker exchange rate must be positive")
		}
		cost := CalculateDeliveryCost(weight, value, rate)
		at := time.Now().UTC()
		p.DeliveryCostRUB = &cost
		p.USDToRUBRate = &rate
		p.CalculatedAt = &at
		p.Status = "calculated"
		p, err = s.packages.SaveCalculation(ctx, p)
		if err != nil {
			return fmt.Errorf("save worker calculation: %w", err)
		}
		if err = matchesMessage(p, m, weight, value); err != nil {
			return err
		}
	}
	if p.Status != "calculated" || p.DeliveryCostRUB == nil || p.USDToRUBRate == nil || p.CalculatedAt == nil || p.PackageTypeName == "" {
		return ErrIncompleteCalculation
	}
	if err = s.logs.Upsert(ctx, model.CalculationLog{Package: p, TraceID: m.TraceID}); err != nil {
		return fmt.Errorf("save calculation log: %w", err)
	}
	return nil
}

func matchesMessage(p model.CalculationRecord, m messaging.PackageMessage, weight, value decimal.Decimal) error {
	if p.PackageID != m.PackageID || p.SessionID != m.SessionID || p.Name != m.Name || p.PackageTypeID != m.PackageTypeID || !p.WeightKG.Equal(weight) || !p.ContentValueUSD.Equal(value) {
		return ErrPackageIdentityConflict
	}
	return nil
}
