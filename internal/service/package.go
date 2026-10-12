package service

import (
	"context"
	"crypto/sha256"
	"delivery-service/internal/messaging"
	"delivery-service/internal/repository"
	"delivery-service/internal/tracing"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/shopspring/decimal"
	"time"

	"delivery-service/internal/model"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrPackageNotFound = errors.New("package not found")

// ErrIdempotencyKeyConflict reports reuse with a different normalized payload.
var ErrIdempotencyKeyConflict = repository.ErrIdempotencyKeyConflict

var ErrPackageTypeNotFound = errors.New("package type not found")

type packageRepository interface {
	GetAll(context.Context, string, model.PackageListFilter) ([]model.PackageListItem, int, error)
	GetByID(context.Context, string, string) (model.PackageListItem, error)
}
type packageTypeRepository interface {
	Exists(context.Context, int) (bool, error)
}

type packagePublisher interface {
	Publish(context.Context, messaging.PackageMessage) error
}

// PackageService registers packages via RabbitMQ and reads session-scoped records.
type idempotencyRepository interface {
	Reserve(context.Context, model.PackageRegistration) (model.PackageRegistration, error)
}

type PackageService struct {
	idempotency     idempotencyRepository
	publisher       packagePublisher
	repository      packageRepository
	packageTypeRepo packageTypeRepository
}

// NewPackageService injects the read repository, type check and confirmed publisher.
func NewPackageService(
	repository packageRepository,
	packageTypeRepo packageTypeRepository,
	publisher packagePublisher,
	idempotency idempotencyRepository,
) *PackageService {
	return &PackageService{
		idempotency:     idempotency,
		publisher:       publisher,
		repository:      repository,
		packageTypeRepo: packageTypeRepo,
	}
}

// Create publishes a registration; it never inserts the package into PostgreSQL.
func (s *PackageService) Create(
	ctx context.Context,
	sessionID string,
	req model.CreatePackageRequest,
) (model.CreatePackageResponse, error) {

	exists, err := s.packageTypeRepo.Exists(
		ctx,
		req.PackageTypeID,
	)
	if err != nil {
		return model.CreatePackageResponse{}, fmt.Errorf(
			"check package type: %w",
			err,
		)
	}

	if !exists {
		return model.CreatePackageResponse{}, ErrPackageTypeNotFound
	}

	id := uuid.NewString()
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	weight, err := decimal.NewFromString(req.WeightKG.String())
	if err != nil {
		return model.CreatePackageResponse{}, fmt.Errorf("parse weight: %w", err)
	}
	if req.ContentValueUSD == nil {
		return model.CreatePackageResponse{}, errors.New("missing content value")
	}
	value, err := decimal.NewFromString(req.ContentValueUSD.String())
	if err != nil {
		return model.CreatePackageResponse{}, fmt.Errorf("parse content value: %w", err)
	}
	if req.IdempotencyKey != "" {
		canonical := struct {
			Name            string
			WeightKG        string
			PackageTypeID   int
			ContentValueUSD string
		}{req.Name, weight.String(), req.PackageTypeID, value.String()}
		body, err := json.Marshal(canonical)
		if err != nil {
			return model.CreatePackageResponse{}, fmt.Errorf("hash registration: %w", err)
		}
		hash := sha256.Sum256(body)
		registration, err := s.idempotency.Reserve(ctx, model.PackageRegistration{SessionID: sessionID, IdempotencyKey: req.IdempotencyKey, RequestHash: hash[:], PackageID: id, CreatedAt: createdAt})
		if err != nil {
			return model.CreatePackageResponse{}, fmt.Errorf("reserve idempotency key: %w", err)
		}
		id, createdAt = registration.PackageID, registration.CreatedAt
	}
	msg := messaging.PackageMessage{
		SchemaVersion: messaging.PackageMessageVersion, PackageID: id, SessionID: sessionID,
		Name: req.Name, WeightKG: weight.String(),
		PackageTypeID: req.PackageTypeID, ContentValueUSD: value.String(),
		CreatedAt: createdAt, TraceID: tracing.ID(ctx),
	}
	if err := s.publisher.Publish(ctx, msg); err != nil {
		return model.CreatePackageResponse{}, fmt.Errorf("publish package registration: %w", err)
	}
	// The confirmed registration becomes readable after asynchronous processing.
	return model.CreatePackageResponse{PackageID: id, Status: "registered", CreatedAt: createdAt}, nil
}

// GetAll returns a filtered page belonging to the current session.
func (s *PackageService) GetAll(
	ctx context.Context,
	sessionID string,
	filter model.PackageListFilter,
) (model.PackageListResponse, error) {
	packages, total, err := s.repository.GetAll(
		ctx,
		sessionID,
		filter,
	)
	if err != nil {
		return model.PackageListResponse{}, fmt.Errorf(
			"get packages: %w",
			err,
		)
	}

	totalPages := 0

	if total > 0 {
		totalPages = (total + filter.Limit - 1) / filter.Limit
	}

	return model.PackageListResponse{
		Data: packages,
		Pagination: model.Pagination{
			Page:       filter.Page,
			Limit:      filter.Limit,
			Total:      total,
			TotalPages: totalPages,
		},
	}, nil
}

// GetByID returns a package only when it belongs to the current session.
func (s *PackageService) GetByID(
	ctx context.Context,
	sessionID string,
	packageID string,
) (model.PackageListItem, error) {
	item, err := s.repository.GetByID(
		ctx,
		sessionID,
		packageID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return model.PackageListItem{}, ErrPackageNotFound
		}

		return model.PackageListItem{}, fmt.Errorf(
			"get package by id: %w",
			err,
		)
	}

	return item, nil
}
