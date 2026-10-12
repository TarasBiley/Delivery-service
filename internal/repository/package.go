package repository

import (
	"context"
	"errors"
	"fmt"

	"delivery-service/internal/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// PackageRepository stores and reads PostgreSQL packages.
type PackageRepository struct {
	db *pgxpool.Pool
}

// NewPackageRepository creates a repository using the shared pool.
func NewPackageRepository(db *pgxpool.Pool) *PackageRepository {
	return &PackageRepository{
		db: db,
	}
}

// Create inserts a legacy pending record; the registration HTTP route does not use it.
func (r *PackageRepository) Create(
	ctx context.Context,
	pkg model.Package,
) (model.Package, error) {
	err := r.db.QueryRow(
		ctx,
		`
		INSERT INTO packages (
			package_id,
			session_id,
			name,
			weight_kg,
			package_type_id,
			content_value_usd,
			status
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING created_at, updated_at
		`,
		pkg.PackageID,
		pkg.SessionID,
		pkg.Name,
		pkg.WeightKG,
		pkg.PackageTypeID,
		pkg.ContentValueUSD,
		pkg.Status,
	).Scan(
		&pkg.CreatedAt,
		&pkg.UpdatedAt,
	)

	if err != nil {
		return model.Package{}, fmt.Errorf(
			"create package: %w",
			err,
		)
	}

	return pkg, nil
}

// GetAll returns a session-scoped filtered page and total.
func (r *PackageRepository) GetAll(
	ctx context.Context,
	sessionID string,
	filter model.PackageListFilter,
) ([]model.PackageListItem, int, error) {
	offset := (filter.Page - 1) * filter.Limit

	rows, err := r.db.Query(
		ctx,
		`
		SELECT
			p.package_id,
			p.name,
			p.weight_kg,
			pt.id,
			pt.name,
			pt.name_ru,
			p.content_value_usd,
			p.delivery_cost_rub,
			p.status,
			p.created_at,
			p.updated_at,
 p.carrier_id,
 p.calculated_at,
 p.usd_to_rub_rate::text
		FROM packages p
		JOIN package_types pt
			ON pt.id = p.package_type_id
		WHERE p.session_id = $1
		  AND ($2::integer IS NULL OR p.package_type_id = $2)
		  AND (
			  $3::boolean IS NULL
			  OR ($3 = TRUE AND p.delivery_cost_rub IS NOT NULL)
			  OR ($3 = FALSE AND p.delivery_cost_rub IS NULL)
		  )
		ORDER BY p.created_at DESC, p.package_id DESC
		LIMIT $4 OFFSET $5
		`,
		sessionID,
		filter.PackageTypeID,
		filter.Calculated,
		filter.Limit,
		offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("get packages: %w", err)
	}
	defer rows.Close()

	packages := make([]model.PackageListItem, 0)

	for rows.Next() {
		var item model.PackageListItem

		if err := rows.Scan(
			&item.PackageID,
			&item.Name,
			&item.WeightKG,
			&item.PackageType.ID,
			&item.PackageType.Name,
			&item.PackageType.NameRU,
			&item.ContentValueUSD,
			&item.DeliveryCostRUB,
			&item.Status,
			&item.CreatedAt,
			&item.UpdatedAt,
			&item.CarrierID,
			&item.CalculatedAt,
			&item.USDToRUBRate,
		); err != nil {
			return nil, 0, fmt.Errorf("scan package: %w", err)
		}

		packages = append(packages, item)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate packages: %w", err)
	}

	var total int

	err = r.db.QueryRow(
		ctx,
		`
		SELECT COUNT(*)
		FROM packages
		WHERE session_id = $1
		  AND ($2::integer IS NULL OR package_type_id = $2)
		  AND (
			  $3::boolean IS NULL
			  OR ($3 = TRUE AND delivery_cost_rub IS NOT NULL)
			  OR ($3 = FALSE AND delivery_cost_rub IS NULL)
		  )
		`,
		sessionID,
		filter.PackageTypeID,
		filter.Calculated,
	).Scan(&total)

	if err != nil {
		return nil, 0, fmt.Errorf("count packages: %w", err)
	}

	return packages, total, nil
}

// GetByID reads one package within its owner session.
func (r *PackageRepository) GetByID(
	ctx context.Context,
	sessionID string,
	packageID string,
) (model.PackageListItem, error) {
	var item model.PackageListItem

	err := r.db.QueryRow(
		ctx,
		`
		SELECT
			p.package_id,
			p.name,
			p.weight_kg,
			pt.id,
			pt.name,
			pt.name_ru,
			p.content_value_usd,
			p.delivery_cost_rub,
			p.status,
			p.created_at,
			p.updated_at,
 p.carrier_id,
 p.calculated_at,
 p.usd_to_rub_rate::text
		FROM packages p
		JOIN package_types pt
			ON pt.id = p.package_type_id
		WHERE p.package_id = $1
		  AND p.session_id = $2
		`,
		packageID,
		sessionID,
	).Scan(
		&item.PackageID,
		&item.Name,
		&item.WeightKG,
		&item.PackageType.ID,
		&item.PackageType.Name,
		&item.PackageType.NameRU,
		&item.ContentValueUSD,
		&item.DeliveryCostRUB,
		&item.Status,
		&item.CreatedAt,
		&item.UpdatedAt,
		&item.CarrierID,
		&item.CalculatedAt,
		&item.USDToRUBRate,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return model.PackageListItem{}, pgx.ErrNoRows
		}

		return model.PackageListItem{}, fmt.Errorf(
			"get package by id: %w",
			err,
		)
	}

	return item, nil
}

func (r *PackageRepository) BeginTx(
	ctx context.Context,
) (pgx.Tx, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}

	return tx, nil
}

func (r *PackageRepository) GetPendingForUpdate(
	ctx context.Context,
	tx pgx.Tx,
	limit int,
) ([]model.PendingPackage, error) {
	rows, err := tx.Query(
		ctx,
		`
		SELECT
			package_id,
			weight_kg::text,
			content_value_usd::text
		FROM packages
		WHERE status = 'calculation_pending'
		ORDER BY created_at, package_id
		LIMIT $1
		FOR UPDATE SKIP LOCKED
		`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"get pending packages for update: %w",
			err,
		)
	}
	defer rows.Close()

	packages := make([]model.PendingPackage, 0)

	for rows.Next() {
		var pkg model.PendingPackage
		var weightKG string
		var contentValueUSD string

		if err := rows.Scan(
			&pkg.PackageID,
			&weightKG,
			&contentValueUSD,
		); err != nil {
			return nil, fmt.Errorf(
				"scan pending package: %w",
				err,
			)
		}

		pkg.WeightKG, err = decimal.NewFromString(weightKG)
		if err != nil {
			return nil, fmt.Errorf(
				"parse weight_kg for package %s: %w",
				pkg.PackageID,
				err,
			)
		}

		pkg.ContentValueUSD, err = decimal.NewFromString(contentValueUSD)
		if err != nil {
			return nil, fmt.Errorf(
				"parse content_value_usd for package %s: %w",
				pkg.PackageID,
				err,
			)
		}

		packages = append(packages, pkg)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"iterate pending packages: %w",
			err,
		)
	}

	return packages, nil
}

func (r *PackageRepository) UpdateCalculation(
	ctx context.Context,
	tx pgx.Tx,
	packageID string,
	deliveryCostRUB decimal.Decimal,
) error {
	tag, err := tx.Exec(
		ctx,
		`
		UPDATE packages
		SET
			delivery_cost_rub = $1::numeric,
			status = 'calculated',
			updated_at = NOW()
		WHERE package_id = $2
		`,
		deliveryCostRUB.StringFixed(2),
		packageID,
	)
	if err != nil {
		return fmt.Errorf(
			"update package calculation: %w",
			err,
		)
	}

	if tag.RowsAffected() != 1 {
		return fmt.Errorf(
			"update package calculation: expected 1 affected row, got %d for package %s",
			tag.RowsAffected(),
			packageID,
		)
	}

	return nil
}

func (r *PackageRepository) HasPending(
	ctx context.Context,
) (bool, error) {
	var exists bool

	err := r.db.QueryRow(
		ctx,
		`
		SELECT EXISTS (
			SELECT 1
			FROM packages
			WHERE status = 'calculation_pending'
		)
		`,
	).Scan(&exists)

	if err != nil {
		return false, fmt.Errorf(
			"check pending packages: %w",
			err,
		)
	}

	return exists, nil
}
