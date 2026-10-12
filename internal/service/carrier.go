package service

import (
	"context"
	"delivery-service/internal/model"
	"delivery-service/internal/repository"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
)

// ErrCarrierAlreadyAssigned reports a conflict with an existing assignment.
var ErrCarrierAlreadyAssigned = errors.New("carrier already assigned")

type carrierRepository interface {
	AssignCarrier(context.Context, string, string, int64) (int64, error)
}

// CarrierService controls session-scoped carrier claims.
type CarrierService struct{ packages carrierRepository }

// NewCarrierService creates a carrier service.
func NewCarrierService(packages carrierRepository) *CarrierService {
	return &CarrierService{packages: packages}
}

// AssignCarrier preserves the first assignment, including on repeated identical requests.
func (s *CarrierService) AssignCarrier(ctx context.Context, sessionID, packageID string, carrierID int64) (model.AssignCarrierResponse, error) {
	id, err := s.packages.AssignCarrier(ctx, sessionID, packageID, carrierID)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.AssignCarrierResponse{}, ErrPackageNotFound
	}
	if errors.Is(err, repository.ErrCarrierAlreadyAssigned) {
		return model.AssignCarrierResponse{}, ErrCarrierAlreadyAssigned
	}
	if err != nil {
		return model.AssignCarrierResponse{}, fmt.Errorf("claim package: %w", err)
	}
	return model.AssignCarrierResponse{CarrierID: id}, nil
}
