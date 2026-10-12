//go:build integration

package repository

import (
	"context"
	"delivery-service/internal/model"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"
)

func integrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := "test_" + uuid.New().String()
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = quoted
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, name := range []string{"1_init.up.sql", "2_package_metadata.up.sql", "3_package_idempotency.up.sql"} {
		migration, err := os.ReadFile("../../migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	return pool
}
func TestPackageRepositoryIntegration(t *testing.T) {
	pool := integrationPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo := NewPackageRepository(pool)
	owner, other := uuid.NewString(), uuid.NewString()
	ids := []string{}
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		id := uuid.NewString()
		session := owner
		if i == 3 {
			session = other
		}
		typeID := 1
		if i == 1 {
			typeID = 2
		}
		p, err := repo.Create(ctx, model.Package{PackageID: id, SessionID: session, Name: "fixture", WeightKG: 1.234, ContentValueUSD: 12.34, PackageTypeID: typeID, Status: "calculation_pending"})
		if err != nil {
			t.Fatal(err)
		}
		if p.CreatedAt.IsZero() || p.UpdatedAt.IsZero() {
			t.Fatal("missing timestamps")
		}
		if _, err = pool.Exec(ctx, "UPDATE packages SET created_at=$1 WHERE package_id=$2", stamp, id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	tx, err := repo.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err = repo.UpdateCalculation(ctx, tx, ids[0], decimal.RequireFromString("123.45")); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	expected := append([]string(nil), ids[:3]...)
	sort.Sort(sort.Reverse(sort.StringSlice(expected)))
	typeID := 2
	yes, no := true, false
	for _, tc := range []struct {
		name, session string
		filter        model.PackageListFilter
		want          []string
		total         int
	}{
		{"owner", owner, model.PackageListFilter{Page: 1, Limit: 20}, expected, 3},
		{"other", other, model.PackageListFilter{Page: 1, Limit: 20}, ids[3:], 1},
		{"empty", uuid.NewString(), model.PackageListFilter{Page: 1, Limit: 20}, []string{}, 0},
		{"pagination", owner, model.PackageListFilter{Page: 2, Limit: 1}, expected[1:2], 3},
		{"type", owner, model.PackageListFilter{Page: 1, Limit: 20, PackageTypeID: &typeID}, ids[1:2], 1},
		{"calculated", owner, model.PackageListFilter{Page: 1, Limit: 20, Calculated: &yes}, ids[:1], 1},
		{"pending", owner, model.PackageListFilter{Page: 1, Limit: 20, Calculated: &no}, nil, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items, total, err := repo.GetAll(ctx, tc.session, tc.filter)
			if err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for _, p := range items {
				got = append(got, p.PackageID)
				if tc.name == "pending" && (p.DeliveryCostRUB != nil || p.Status != "calculation_pending") {
					t.Fatal("wrong pending state")
				}
			}
			want := tc.want
			if tc.name == "pending" {
				want = []string{}
				for _, id := range expected {
					if id != ids[0] {
						want = append(want, id)
					}
				}
			}
			if total != tc.total || !reflect.DeepEqual(got, want) {
				t.Fatalf("ids=%v total=%d want=%v/%d", got, total, want, tc.total)
			}
		})
	}
	item, err := repo.GetByID(ctx, owner, ids[0])
	if err != nil || item.Status != "calculated" || item.DeliveryCostRUB == nil || *item.DeliveryCostRUB != 123.45 || item.Name != "fixture" || item.WeightKG != 1.234 || item.ContentValueUSD != 12.34 {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	for _, q := range []struct{ session, id string }{{other, ids[0]}, {owner, uuid.NewString()}} {
		_, err := repo.GetByID(ctx, q.session, q.id)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("expected not found: %v", err)
		}
	}
	has, err := repo.HasPending(ctx)
	if err != nil || !has {
		t.Fatalf("pending=%v error=%v", has, err)
	}
	t.Run("skip locked", func(t *testing.T) {
		first, err := repo.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer first.Rollback(context.Background())
		locked, err := repo.GetPendingForUpdate(ctx, first, 1)
		if err != nil || len(locked) != 1 {
			t.Fatalf("locked=%v err=%v", locked, err)
		}
		pendingIDs := append([]string(nil), ids[1:]...)
		sort.Strings(pendingIDs)
		if locked[0].PackageID != pendingIDs[0] || !locked[0].WeightKG.Equal(decimal.RequireFromString("1.234")) || !locked[0].ContentValueUSD.Equal(decimal.RequireFromString("12.34")) {
			t.Fatalf("wrong pending row: %+v", locked[0])
		}
		second, err := repo.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer second.Rollback(context.Background())
		available, err := repo.GetPendingForUpdate(ctx, second, 100)
		if err != nil || len(available) != 2 {
			t.Fatalf("available=%v err=%v", available, err)
		}
		for i, p := range available {
			if p.PackageID != pendingIDs[i+1] {
				t.Fatal("locked row returned or unstable sort")
			}
		}
		if err = second.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		if err = first.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		third, err := repo.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer third.Rollback(context.Background())
		all, err := repo.GetPendingForUpdate(ctx, third, 100)
		if err != nil || len(all) != 3 {
			t.Fatalf("rollback did not release rows: %v %v", all, err)
		}
		for _, p := range all {
			if err = repo.UpdateCalculation(ctx, third, p.PackageID, decimal.RequireFromString("9999999999999999.99")); err != nil {
				t.Fatal(err)
			}
		}
		if err = third.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		var stored string
		if err = pool.QueryRow(ctx, "SELECT delivery_cost_rub::text FROM packages WHERE package_id=$1", ids[1]).Scan(&stored); err != nil || stored != "9999999999999999.99" {
			t.Fatalf("decimal storage=%s err=%v", stored, err)
		}
		has, err := repo.HasPending(ctx)
		if err != nil || has {
			t.Fatalf("pending remains: %v %v", has, err)
		}
	})
}

func TestPackageMetadataIntegration(t *testing.T) {
	pool := integrationPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	repo := NewPackageRepository(pool)
	session, id := uuid.NewString(), uuid.NewString()
	_, err := repo.Create(ctx, model.Package{PackageID: id, SessionID: session, Name: "metadata", WeightKG: 1, PackageTypeID: 1, Status: "calculation_pending"})
	if err != nil {
		t.Fatal(err)
	}
	check := func(populated bool) {
		t.Helper()
		item, err := repo.GetByID(ctx, session, id)
		if err != nil {
			t.Fatal(err)
		}
		items, total, err := repo.GetAll(ctx, session, model.PackageListFilter{Page: 1, Limit: 20})
		if err != nil || total != 1 || len(items) != 1 {
			t.Fatalf("list=%v total=%d err=%v", items, total, err)
		}
		for _, got := range []model.PackageListItem{item, items[0]} {
			if got.DeliveryCostRUB != nil {
				t.Fatal("pending delivery cost is not null")
			}
			if !populated {
				if got.CarrierID != nil || got.CalculatedAt != nil || got.USDToRUBRate != nil {
					t.Fatalf("metadata not null: %+v", got)
				}
				continue
			}
			wantTime := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
			if got.CarrierID == nil || *got.CarrierID != 42 || got.CalculatedAt == nil || !got.CalculatedAt.Equal(wantTime) || got.USDToRUBRate == nil || !got.USDToRUBRate.Equal(decimal.RequireFromString("95.1234567890123456789")) {
				t.Fatalf("metadata=%+v", got)
			}
		}
	}
	check(false)
	for _, carrier := range []int64{0, -1} {
		if _, err = pool.Exec(ctx, "UPDATE packages SET carrier_id=$1 WHERE package_id=$2", carrier, id); err == nil {
			t.Fatalf("invalid carrier accepted: %d", carrier)
		}
	}
	if _, err = pool.Exec(ctx, "UPDATE packages SET carrier_id=42, calculated_at=$1, usd_to_rub_rate=$2::numeric WHERE package_id=$3", time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC), "95.1234567890123456789", id); err != nil {
		t.Fatal(err)
	}
	check(true)
	down, err := os.ReadFile("../../migrations/2_package_metadata.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(down)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM packages WHERE package_id=$1", id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("down lost existing row: %d %v", count, err)
	}
	var columns int
	if err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='packages' AND column_name IN ('carrier_id','calculated_at','usd_to_rub_rate')").Scan(&columns); err != nil || columns != 0 {
		t.Fatalf("down columns=%d err=%v", columns, err)
	}
	up, err := os.ReadFile("../../migrations/2_package_metadata.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(up)); err != nil {
		t.Fatal(err)
	}
	check(false)
}

func workerRecord() model.CalculationRecord {
	cost := decimal.RequireFromString("10499999999.94")
	rate := decimal.RequireFromString("100.1234567890123456789")
	at := time.Date(2026, 10, 11, 12, 0, 0, 123000000, time.UTC)
	return model.CalculationRecord{PackageID: uuid.NewString(), SessionID: uuid.NewString(), Name: "worker fixture", PackageTypeID: 2, WeightKG: decimal.RequireFromString("9999999.999"), ContentValueUSD: decimal.RequireFromString("9999999999.99"), Status: "calculated", DeliveryCostRUB: &cost, USDToRUBRate: &rate, CreatedAt: at.Add(-time.Hour), CalculatedAt: &at}
}
func TestWorkerPackageRepositoryIntegration(t *testing.T) {
	pool := integrationPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	repo := NewPackageRepository(pool)
	t.Run("insert and conflict", func(t *testing.T) {
		input := workerRecord()
		first, err := repo.SaveCalculation(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		if first.PackageTypeName != "electronics" || !first.WeightKG.Equal(input.WeightKG) || !first.ContentValueUSD.Equal(input.ContentValueUSD) || !first.DeliveryCostRUB.Equal(*input.DeliveryCostRUB) || !first.USDToRUBRate.Equal(*input.USDToRUBRate) || !first.CreatedAt.Equal(input.CreatedAt) || !first.CalculatedAt.Equal(*input.CalculatedAt) {
			t.Fatalf("record=%+v", first)
		}
		input.DeliveryCostRUB = decimalPtr("1")
		input.USDToRUBRate = decimalPtr("1")
		later := input.CalculatedAt.Add(time.Hour)
		input.CalculatedAt = &later
		second, err := repo.SaveCalculation(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("conflict changed result: %+v", second)
		}
		input.SessionID = uuid.NewString()
		if _, err = repo.SaveCalculation(ctx, input); !errors.Is(err, ErrCalculationIdentityConflict) {
			t.Fatalf("identity conflict=%v", err)
		}
	})
	t.Run("pending transition", func(t *testing.T) {
		input := workerRecord()
		old, err := repo.Create(ctx, model.Package{PackageID: input.PackageID, SessionID: input.SessionID, Name: input.Name, WeightKG: 9999999.999, ContentValueUSD: 9999999999.99, PackageTypeID: 2, Status: "calculation_pending"})
		if err != nil {
			t.Fatal(err)
		}
		before, err := repo.GetCalculation(ctx, input.PackageID)
		if err != nil || before.DeliveryCostRUB != nil || before.USDToRUBRate != nil || before.CalculatedAt != nil {
			t.Fatalf("pending=%+v error=%v", before, err)
		}
		conflicting := input
		conflicting.Name = "different"
		if _, err = repo.SaveCalculation(ctx, conflicting); !errors.Is(err, ErrCalculationIdentityConflict) {
			t.Fatal(err)
		}
		first, err := repo.SaveCalculation(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		if first.Status != "calculated" || !first.CreatedAt.Equal(old.CreatedAt) {
			t.Fatal("pending transition lost creation time")
		}
		input.DeliveryCostRUB = decimalPtr("2")
		again, err := repo.SaveCalculation(ctx, input)
		if err != nil || !reflect.DeepEqual(first, again) {
			t.Fatalf("second update changed calculation: %v", err)
		}
	})
	t.Run("concurrent insert", func(t *testing.T) {
		input := workerRecord()
		other := input
		other.DeliveryCostRUB = decimalPtr("777.77")
		other.USDToRUBRate = decimalPtr("50")
		type result struct {
			p   model.CalculationRecord
			err error
		}
		out := make(chan result, 2)
		start := make(chan struct{})
		for _, p := range []model.CalculationRecord{input, other} {
			go func(p model.CalculationRecord) {
				<-start
				saved, err := repo.SaveCalculation(ctx, p)
				out <- result{saved, err}
			}(p)
		}
		close(start)
		a, b := <-out, <-out
		if a.err != nil || b.err != nil || !reflect.DeepEqual(a.p, b.p) {
			t.Fatalf("different results: %+v %+v", a, b)
		}
		var count int
		if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM packages WHERE package_id=$1", input.PackageID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("count=%d error=%v", count, err)
		}
	})
	t.Run("concurrent pending", func(t *testing.T) {
		input := workerRecord()
		_, err := repo.Create(ctx, model.Package{PackageID: input.PackageID, SessionID: input.SessionID, Name: input.Name, WeightKG: 9999999.999, ContentValueUSD: 9999999999.99, PackageTypeID: 2, Status: "calculation_pending"})
		if err != nil {
			t.Fatal(err)
		}
		other := input
		other.DeliveryCostRUB = decimalPtr("42.42")
		results := make(chan model.CalculationRecord, 2)
		errs := make(chan error, 2)
		start := make(chan struct{})
		for _, p := range []model.CalculationRecord{input, other} {
			go func(p model.CalculationRecord) {
				<-start
				saved, err := repo.SaveCalculation(ctx, p)
				results <- saved
				errs <- err
			}(p)
		}
		close(start)
		a, b := <-results, <-results
		for i := 0; i < 2; i++ {
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
		}
		if !reflect.DeepEqual(a, b) {
			t.Fatal("pending overwritten by second worker")
		}
	})
}
func decimalPtr(s string) *decimal.Decimal { d := decimal.RequireFromString(s); return &d }

func TestRecoveryBatchIntegration(t *testing.T) {
	pool := integrationPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	repo := NewPackageRepository(pool)
	for i := 0; i < 3; i++ {
		p, err := repo.Create(ctx, model.Package{PackageID: uuid.NewString(), SessionID: uuid.NewString(), Name: "recovery", WeightKG: 1, ContentValueUSD: 10, PackageTypeID: 1, Status: "calculation_pending"})
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			record, err := repo.GetCalculation(ctx, p.PackageID)
			if err != nil {
				t.Fatal(err)
			}
			cost, rate, at := decimal.NewFromInt(60), decimal.NewFromInt(100), time.Now().UTC()
			record.DeliveryCostRUB = &cost
			record.USDToRUBRate = &rate
			record.CalculatedAt = &at
			if _, err := repo.SaveCalculation(ctx, record); err != nil {
				t.Fatal(err)
			}
		}
		if i == 2 {
			if _, err := pool.Exec(ctx, "UPDATE packages SET status='calculated',delivery_cost_rub=50 WHERE package_id=$1", p.PackageID); err != nil {
				t.Fatal(err)
			}
		}
	}
	after := ""
	pending, calculated := 0, 0
	for {
		records, err := repo.GetRecoveryBatch(ctx, after, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(records) == 0 {
			break
		}
		p := records[0]
		if p.Status == "calculation_pending" {
			pending++
		} else {
			calculated++
		}
		if after != "" && p.PackageID <= after {
			t.Fatal("keyset order violated")
		}
		after = p.PackageID
	}
	if pending != 1 || calculated != 1 {
		t.Fatalf("pending=%d calculated=%d", pending, calculated)
	}
}
