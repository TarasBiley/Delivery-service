package repository

import (
	"bytes"
	"context"
	"delivery-service/internal/model"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrIdempotencyKeyConflict means the reserved key has a different business payload.
var ErrIdempotencyKeyConflict = errors.New("idempotency key payload conflict")

// IdempotencyRepository durably reserves package registrations before publication.
type IdempotencyRepository struct{ db *pgxpool.Pool }

// NewIdempotencyRepository uses the shared PostgreSQL pool.
func NewIdempotencyRepository(db *pgxpool.Pool) *IdempotencyRepository {
	return &IdempotencyRepository{db: db}
}

// Reserve returns the unique session/key registration. Concurrent INSERT waits
// inside PostgreSQL; the following statement sees the committed winner. No
// transaction remains open when this method returns to the RabbitMQ publisher.
func (r *IdempotencyRepository) Reserve(ctx context.Context, p model.PackageRegistration) (model.PackageRegistration, error) {
	_, err := r.db.Exec(ctx, `INSERT INTO package_idempotency(session_id,idempotency_key,request_hash,package_id,created_at)
 VALUES($1,$2,$3,$4,$5) ON CONFLICT(session_id,idempotency_key) DO NOTHING`, p.SessionID, p.IdempotencyKey, p.RequestHash, p.PackageID, p.CreatedAt)
	if err != nil {
		return model.PackageRegistration{}, fmt.Errorf("reserve registration: %w", err)
	}
	var saved model.PackageRegistration
	err = r.db.QueryRow(ctx, `SELECT session_id,idempotency_key,request_hash,package_id,created_at
 FROM package_idempotency WHERE session_id=$1 AND idempotency_key=$2`, p.SessionID, p.IdempotencyKey).Scan(&saved.SessionID, &saved.IdempotencyKey, &saved.RequestHash, &saved.PackageID, &saved.CreatedAt)
	if err != nil {
		return model.PackageRegistration{}, fmt.Errorf("read reserved registration: %w", err)
	}
	if !bytes.Equal(saved.RequestHash, p.RequestHash) {
		return model.PackageRegistration{}, ErrIdempotencyKeyConflict
	}
	return saved, nil
}
