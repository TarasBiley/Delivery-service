package repository

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
)

// ErrCarrierAlreadyAssigned means the package has already been claimed.
var ErrCarrierAlreadyAssigned = errors.New("carrier already assigned")

// AssignCarrier atomically claims an unassigned package in the owner's session.
func (r *PackageRepository) AssignCarrier(ctx context.Context, sessionID, packageID string, carrierID int64) (int64, error) {
	var assigned int64
	err := r.db.QueryRow(ctx, `UPDATE packages SET carrier_id=$1,updated_at=NOW()
 WHERE package_id=$2 AND session_id=$3 AND carrier_id IS NULL RETURNING carrier_id`, carrierID, packageID, sessionID).Scan(&assigned)
	if err == nil {
		return assigned, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("assign carrier: %w", err)
	}
	var exists bool
	err = r.db.QueryRow(ctx, `SELECT true FROM packages WHERE package_id=$1 AND session_id=$2`, packageID, sessionID).Scan(&exists)
	if err != nil {
		return 0, fmt.Errorf("find package after claim: %w", err)
	}
	return 0, ErrCarrierAlreadyAssigned
}
