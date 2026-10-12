package repository

import (
	"context"
	"fmt"

	"delivery-service/internal/model"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PackageTypeRepository reads the PostgreSQL package type dictionary.
type PackageTypeRepository struct {
	db *pgxpool.Pool
}

// NewPackageTypeRepository creates a repository using the shared PostgreSQL pool.
func NewPackageTypeRepository(db *pgxpool.Pool) *PackageTypeRepository {
	return &PackageTypeRepository{
		db: db,
	}
}

func (r *PackageTypeRepository) GetAll(
	ctx context.Context,
) ([]model.PackageType, error) {
	rows, err := r.db.Query(
		ctx,
		`
		SELECT id, name, name_ru
		FROM package_types
		ORDER BY id
		`,
	)
	if err != nil {
		return nil, fmt.Errorf("get package types: %w", err)
	}
	defer rows.Close()

	var packageTypes []model.PackageType

	for rows.Next() {
		var packageType model.PackageType

		if err := rows.Scan(
			&packageType.ID,
			&packageType.Name,
			&packageType.NameRU,
		); err != nil {
			return nil, fmt.Errorf("scan package type: %w", err)
		}

		packageTypes = append(packageTypes, packageType)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate package types: %w", err)
	}

	return packageTypes, nil
}

func (r *PackageTypeRepository) Exists(
	ctx context.Context,
	id int,
) (bool, error) {
	var exists bool

	err := r.db.QueryRow(
		ctx,
		`
		SELECT EXISTS (
			SELECT 1
			FROM package_types
			WHERE id = $1
		)
		`,
		id,
	).Scan(&exists)

	if err != nil {
		return false, fmt.Errorf(
			"check package type exists: %w",
			err,
		)
	}

	return exists, nil
}
