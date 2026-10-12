package service

import (
	"context"
	"fmt"

	"delivery-service/internal/cache"
	"delivery-service/internal/model"
	"delivery-service/internal/repository"
)

// PackageTypeService caches the available package types in memory.
type PackageTypeService struct {
	repository *repository.PackageTypeRepository
	cache      *cache.PackageTypeCache
}

// NewPackageTypeService creates the package type service.
func NewPackageTypeService(
	repository *repository.PackageTypeRepository,
	cache *cache.PackageTypeCache,
) *PackageTypeService {
	return &PackageTypeService{
		repository: repository,
		cache:      cache,
	}
}

// GetAll returns all package types from cache or PostgreSQL.
func (s *PackageTypeService) GetAll(
	ctx context.Context,
) ([]model.PackageType, error) {

	if packageTypes, ok := s.cache.GetAll(); ok {
		return packageTypes, nil
	}

	packageTypes, err := s.repository.GetAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("get package types: %w", err)
	}

	s.cache.SetAll(packageTypes)

	return packageTypes, nil
}
