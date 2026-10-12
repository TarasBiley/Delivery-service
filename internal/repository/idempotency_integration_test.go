//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"delivery-service/internal/model"
	"errors"
	"github.com/google/uuid"
	"os"
	"sync"
	"testing"
	"time"
)

func TestIdempotencyPostgresIntegration(t *testing.T) {
	pool := integrationPool(t)
	repo := NewIdempotencyRepository(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	hash := sha256.Sum256([]byte("canonical"))
	request := model.PackageRegistration{SessionID: uuid.NewString(), IdempotencyKey: "same", RequestHash: hash[:], PackageID: uuid.NewString(), CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	first, err := repo.Reserve(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	candidate := request
	candidate.PackageID = uuid.NewString()
	candidate.CreatedAt = time.Now().UTC().Truncate(time.Microsecond)
	second, err := repo.Reserve(ctx, candidate)
	if err != nil || second.PackageID != first.PackageID || !second.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	changed := sha256.Sum256([]byte("changed"))
	candidate.RequestHash = changed[:]
	if _, err := repo.Reserve(ctx, candidate); !errors.Is(err, ErrIdempotencyKeyConflict) {
		t.Fatal(err)
	}
	candidate = request
	candidate.SessionID = uuid.NewString()
	candidate.PackageID = uuid.NewString()
	independent, err := repo.Reserve(ctx, candidate)
	if err != nil || independent.PackageID == first.PackageID {
		t.Fatalf("foreign=%+v err=%v", independent, err)
	}
	request.IdempotencyKey = "concurrent"
	const n = 10
	results := make(chan model.PackageRegistration, n)
	errs := make(chan error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := request
			p.PackageID = uuid.NewString()
			<-start
			result, err := repo.Reserve(ctx, p)
			results <- result
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var winner model.PackageRegistration
	for result := range results {
		if winner.PackageID == "" {
			winner = result
		}
		if result.PackageID != winner.PackageID || !result.CreatedAt.Equal(winner.CreatedAt) {
			t.Fatal("multiple logical registrations")
		}
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM package_idempotency WHERE session_id=$1 AND idempotency_key=$2", request.SessionID, request.IdempotencyKey).Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	// Reservation metadata must not create a package row.
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM packages").Scan(&count); err != nil || count != 0 {
		t.Fatalf("packages=%d err=%v", count, err)
	}
	migration, err := os.ReadFile("../../migrations/3_package_idempotency.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
}
