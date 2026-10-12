package repository

import (
	"context"
	"delivery-service/internal/model"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
)

var ErrCalculationIdentityConflict = errors.New("package registration does not match existing record")

// GetCalculation reads exact decimals and canonical result metadata for idempotent processing.
func (r *PackageRepository) GetCalculation(ctx context.Context, id string) (model.CalculationRecord, error) {
	var p model.CalculationRecord
	err := r.db.QueryRow(ctx, `SELECT p.package_id,p.session_id,p.name,p.package_type_id,pt.name,
 p.weight_kg::text,p.content_value_usd::text,p.status,p.delivery_cost_rub::text,
 p.usd_to_rub_rate::text,p.created_at,p.calculated_at
 FROM packages p JOIN package_types pt ON pt.id=p.package_type_id WHERE p.package_id=$1`, id).Scan(
		&p.PackageID, &p.SessionID, &p.Name, &p.PackageTypeID, &p.PackageTypeName, &p.WeightKG, &p.ContentValueUSD,
		&p.Status, &p.DeliveryCostRUB, &p.USDToRUBRate, &p.CreatedAt, &p.CalculatedAt)
	if err != nil {
		return model.CalculationRecord{}, fmt.Errorf("get canonical calculation: %w", err)
	}
	return p, nil
}

// SaveCalculation never overwrites a calculated result. Statements use separate
// READ COMMITTED snapshots, so after a concurrent INSERT the winner is visible.
// No transaction spans a rate request or a MongoDB operation.
func (r *PackageRepository) SaveCalculation(ctx context.Context, p model.CalculationRecord) (model.CalculationRecord, error) {
	if p.DeliveryCostRUB == nil || p.USDToRUBRate == nil || p.CalculatedAt == nil {
		return model.CalculationRecord{}, errors.New("missing calculation result metadata")
	}
	var id string
	err := r.db.QueryRow(ctx, `INSERT INTO packages
 (package_id,session_id,name,weight_kg,package_type_id,content_value_usd,status,delivery_cost_rub,usd_to_rub_rate,created_at,calculated_at)
 VALUES($1,$2,$3,$4::numeric,$5,$6::numeric,'calculated',$7::numeric,$8::numeric,$9,$10)
 ON CONFLICT(package_id) DO NOTHING RETURNING package_id`, p.PackageID, p.SessionID, p.Name, p.WeightKG.String(), p.PackageTypeID, p.ContentValueUSD.String(), p.DeliveryCostRUB.String(), p.USDToRUBRate.String(), p.CreatedAt, p.CalculatedAt).Scan(&id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return model.CalculationRecord{}, fmt.Errorf("insert calculated package: %w", err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = r.db.Exec(ctx, `UPDATE packages SET status='calculated',delivery_cost_rub=$2::numeric,usd_to_rub_rate=$3::numeric,calculated_at=$4,updated_at=NOW()
   WHERE package_id=$1 AND status='calculation_pending'
   AND session_id=$5 AND name=$6 AND weight_kg=$7::numeric AND package_type_id=$8 AND content_value_usd=$9::numeric`, p.PackageID, p.DeliveryCostRUB.String(), p.USDToRUBRate.String(), p.CalculatedAt, p.SessionID, p.Name, p.WeightKG.String(), p.PackageTypeID, p.ContentValueUSD.String())
		if err != nil {
			return model.CalculationRecord{}, fmt.Errorf("finish pending package: %w", err)
		}
	}
	saved, err := r.GetCalculation(ctx, p.PackageID)
	if err != nil {
		return model.CalculationRecord{}, err
	}
	if saved.SessionID != p.SessionID || saved.Name != p.Name || saved.PackageTypeID != p.PackageTypeID || !saved.WeightKG.Equal(p.WeightKG) || !saved.ContentValueUSD.Equal(p.ContentValueUSD) {
		return model.CalculationRecord{}, ErrCalculationIdentityConflict
	}
	return saved, nil
}

// GetRecoveryBatch uses keyset pagination and no locks across external calls.
// Include complete calculated records so a failed Mongo write can be retried.
func (r *PackageRepository) GetRecoveryBatch(ctx context.Context, after string, limit int) ([]model.CalculationRecord, error) {
	rows, err := r.db.Query(ctx, `SELECT p.package_id,p.session_id,p.name,p.package_type_id,pt.name,
 p.weight_kg::text,p.content_value_usd::text,p.status,p.delivery_cost_rub::text,
 p.usd_to_rub_rate::text,p.created_at,p.calculated_at
 FROM packages p JOIN package_types pt ON pt.id=p.package_type_id
 WHERE ($1::text='' OR p.package_id>NULLIF($1,'')::uuid)
 AND (p.status='calculation_pending' OR (p.status='calculated' AND p.calculated_at IS NOT NULL AND p.usd_to_rub_rate IS NOT NULL AND p.delivery_cost_rub IS NOT NULL))
 ORDER BY p.package_id LIMIT $2`, after, limit)
	if err != nil {
		return nil, fmt.Errorf("query recovery packages: %w", err)
	}
	defer rows.Close()
	result := []model.CalculationRecord{}
	for rows.Next() {
		var p model.CalculationRecord
		if err := rows.Scan(&p.PackageID, &p.SessionID, &p.Name, &p.PackageTypeID, &p.PackageTypeName, &p.WeightKG, &p.ContentValueUSD, &p.Status, &p.DeliveryCostRUB, &p.USDToRUBRate, &p.CreatedAt, &p.CalculatedAt); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}
