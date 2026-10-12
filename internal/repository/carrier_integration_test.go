//go:build integration

package repository

import (
	"context"
	"delivery-service/internal/model"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"sync"
	"testing"
	"time"
)

func TestAssignCarrierConcurrentIntegration(t *testing.T) {
	pool := integrationPool(t)
	repo := NewPackageRepository(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	owner, id := uuid.NewString(), uuid.NewString()
	if _, err := repo.Create(ctx, model.Package{PackageID: id, SessionID: owner, Name: "claim", WeightKG: 1, ContentValueUSD: 10, PackageTypeID: 1, Status: "calculation_pending"}); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{uuid.NewString(), id}, {owner, uuid.NewString()}} {
		if _, err := repo.AssignCarrier(ctx, pair[0], pair[1], 1); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("foreign/missing: %v", err)
		}
	}
	const n = 12
	type result struct {
		id  int64
		err error
	}
	results := make(chan result, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(carrier int64) {
			defer wg.Done()
			<-start
			got, err := repo.AssignCarrier(ctx, owner, id, carrier)
			results <- result{got, err}
		}(int64(i + 1))
	}
	close(start)
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	var winner int64
	for r := range results {
		if r.err == nil {
			wins++
			winner = r.id
		} else if errors.Is(r.err, ErrCarrierAlreadyAssigned) {
			conflicts++
		} else {
			t.Fatal(r.err)
		}
	}
	if wins != 1 || conflicts != n-1 {
		t.Fatalf("wins=%d conflicts=%d", wins, conflicts)
	}
	var saved int64
	if err := pool.QueryRow(ctx, "SELECT carrier_id FROM packages WHERE package_id=$1", id).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	if saved != winner {
		t.Fatalf("saved=%d winner=%d", saved, winner)
	}
	if _, err := repo.AssignCarrier(ctx, owner, id, winner); !errors.Is(err, ErrCarrierAlreadyAssigned) {
		t.Fatalf("same carrier retry=%v", err)
	}
}
