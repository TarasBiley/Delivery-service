//go:build integration

package repository

import (
	"context"
	"delivery-service/internal/model"
	"errors"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"os"
	"reflect"
	"testing"
	"time"
)

func mongoTestRepository(t *testing.T) (*CalculationLogRepository, context.Context) {
	t.Helper()
	uri := os.Getenv("TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set TEST_MONGO_URI for MongoDB integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Disconnect(ctx); err != nil {
			t.Error(err)
		}
	})
	if err = client.Ping(ctx, nil); err != nil {
		t.Fatal(err)
	}
	db := client.Database("test_" + uuid.NewString())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := db.Drop(ctx); err != nil {
			t.Error(err)
		}
	})
	repo := NewCalculationLogRepository(db)
	if err = repo.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	if err = repo.EnsureIndexes(ctx); err != nil {
		t.Fatalf("idempotent indexes: %v", err)
	}
	return repo, ctx
}
func TestCalculationLogMongoIntegration(t *testing.T) {
	repo, ctx := mongoTestRepository(t)
	log := model.CalculationLog{Package: workerRecord(), TraceID: uuid.NewString()}
	log.Package.PackageTypeName = "electronics"
	if err := repo.Upsert(ctx, log); err != nil {
		t.Fatal(err)
	}
	filter := bson.M{"_id": log.Package.PackageID}
	var first calculationLogDocument
	if err := repo.collection.FindOne(ctx, filter).Decode(&first); err != nil {
		t.Fatal(err)
	}
	if first.ID != log.Package.PackageID || first.PackageID != first.ID || first.SessionID != log.Package.SessionID || first.PackageTypeName != "electronics" || first.TraceID != log.TraceID || !first.CreatedAt.Equal(log.Package.CreatedAt) || !first.CalculatedAt.Equal(*log.Package.CalculatedAt) {
		t.Fatalf("document=%+v", first)
	}
	for _, field := range []struct {
		got  bson.Decimal128
		want string
	}{{first.WeightKG, "9999999.999"}, {first.ContentValueUSD, "9999999999.99"}, {first.USDToRUBRate, "100.1234567890123456789"}, {first.DeliveryCostRUB, "10499999999.94"}} {
		if field.got.String() != field.want {
			t.Fatalf("decimal=%s want=%s", field.got, field.want)
		}
	}
	log.Package.DeliveryCostRUB = decimalPtr("1")
	later := log.Package.CalculatedAt.Add(time.Hour)
	log.Package.CalculatedAt = &later
	log.TraceID = uuid.NewString()
	if err := repo.Upsert(ctx, log); !errors.Is(err, ErrCalculationLogConflict) {
		t.Fatalf("expected conflict: %v", err)
	}
	var second calculationLogDocument
	if err := repo.collection.FindOne(ctx, filter).Decode(&second); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("redelivery changed document")
	}
	count, err := repo.collection.CountDocuments(ctx, bson.M{"package_id": first.PackageID})
	if err != nil || count != 1 {
		t.Fatalf("count=%d error=%v", count, err)
	}
	if _, err = repo.collection.InsertOne(ctx, first); !mongo.IsDuplicateKeyError(err) {
		t.Fatalf("duplicate _id accepted: %v", err)
	}
	t.Run("concurrent upsert", func(t *testing.T) {
		log.Package.PackageID = uuid.NewString()
		errs := make(chan error, 4)
		start := make(chan struct{})
		for i := 0; i < 4; i++ {
			go func() { <-start; errs <- repo.Upsert(ctx, log) }()
		}
		close(start)
		for i := 0; i < 4; i++ {
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
		}
		count, err := repo.collection.CountDocuments(ctx, bson.M{"_id": log.Package.PackageID})
		if err != nil || count != 1 {
			t.Fatalf("count=%d error=%v", count, err)
		}
	})
}

func TestDailySumMongoIntegration(t *testing.T) {
	repo, ctx := mongoTestRepository(t)
	start := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 1)
	session, foreign := uuid.NewString(), uuid.NewString()
	var replay model.CalculationLog
	for i, fixture := range []struct {
		session    string
		typeID     int
		name, cost string
		at         time.Time
	}{
		{session, 1, "old clothing", "0.10", start},
		{session, 1, "new clothing", "0.20", start.Add(time.Hour)},
		{session, 2, "electronics", "9999999999999.99", end.Add(-time.Millisecond)},
		{session, 1, "before", "50.00", start.Add(-time.Millisecond)},
		{session, 1, "after", "60.00", end},
		{foreign, 1, "foreign", "70.00", start},
	} {
		p := workerRecord()
		p.PackageID = uuid.NewString()
		p.SessionID = fixture.session
		p.PackageTypeID = fixture.typeID
		p.PackageTypeName = fixture.name
		p.CalculatedAt = &fixture.at
		value := decimal.RequireFromString(fixture.cost)
		p.DeliveryCostRUB = &value
		log := model.CalculationLog{Package: p, TraceID: uuid.NewString()}
		if err := repo.Upsert(ctx, log); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			replay = log
		}
	}
	if err := repo.Upsert(ctx, replay); err != nil {
		t.Fatal(err)
	}
	got, err := repo.DailySum(ctx, session, start, end)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.DailySumItem{{PackageTypeID: 1, PackageTypeName: "new clothing", PackageCount: 2, DeliveryCostRUB: "0.30"}, {PackageTypeID: 2, PackageTypeName: "electronics", PackageCount: 1, DeliveryCostRUB: "9999999999999.99"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%+v want=%+v", got, want)
	}
	empty, err := repo.DailySum(ctx, uuid.NewString(), start, end)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty=%v err=%v", empty, err)
	}
}

func TestCalculationLogConsistencyMongoIntegration(t *testing.T) {
	repo, ctx := mongoTestRepository(t)
	base := model.CalculationLog{Package: workerRecord(), TraceID: uuid.NewString()}
	base.Package.PackageTypeName = "electronics"
	// Millisecond storage and alternate trace/name are legitimate replay changes.
	base.Package.CreatedAt = time.Date(2026, 10, 11, 0, 0, 0, 123456789, time.UTC)
	at := base.Package.CreatedAt.Add(time.Hour)
	base.Package.CalculatedAt = &at
	if err := repo.Upsert(ctx, base); err != nil {
		t.Fatal(err)
	}
	if err := repo.Upsert(ctx, base); err != nil {
		t.Fatal(err)
	}
	var first calculationLogDocument
	filter := bson.M{"_id": base.Package.PackageID}
	if err := repo.collection.FindOne(ctx, filter).Decode(&first); err != nil {
		t.Fatal(err)
	}
	replay := base
	replay.TraceID = uuid.NewString()
	replay.Package.PackageTypeName = "renamed"
	replay.Package.CreatedAt = replay.Package.CreatedAt.Truncate(time.Millisecond)
	rounded := replay.Package.CalculatedAt.Truncate(time.Millisecond)
	replay.Package.CalculatedAt = &rounded
	if err := repo.Upsert(ctx, replay); err != nil {
		t.Fatalf("equivalent timestamp/trace/name: %v", err)
	}
	// Equivalent Decimal128 scale must compare numerically, not by binary encoding.
	equivalent, err := bson.ParseDecimal128(base.Package.USDToRUBRate.String() + "0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.collection.UpdateOne(ctx, filter, bson.M{"$set": bson.M{"usd_to_rub_rate": equivalent}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Upsert(ctx, base); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.collection.UpdateOne(ctx, filter, bson.M{"$set": bson.M{"usd_to_rub_rate": first.USDToRUBRate}}); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"cost", "rate", "session", "weight", "content", "type", "created", "calculated"} {
		t.Run(field, func(t *testing.T) {
			changed := base
			switch field {
			case "cost":
				changed.Package.DeliveryCostRUB = decimalPtr("1")
			case "rate":
				changed.Package.USDToRUBRate = decimalPtr("100.1234567890123456788")
			case "session":
				changed.Package.SessionID = uuid.NewString()
			case "weight":
				changed.Package.WeightKG = decimal.RequireFromString("1.001")
			case "content":
				changed.Package.ContentValueUSD = decimal.RequireFromString("1.01")
			case "type":
				changed.Package.PackageTypeID++
			case "created":
				changed.Package.CreatedAt = base.Package.CreatedAt.Add(time.Millisecond)
			case "calculated":
				v := base.Package.CalculatedAt.Add(time.Millisecond)
				changed.Package.CalculatedAt = &v
			}
			if err := repo.Upsert(ctx, changed); !errors.Is(err, ErrCalculationLogConflict) {
				t.Fatalf("conflict=%v", err)
			}
			var saved calculationLogDocument
			if err := repo.collection.FindOne(ctx, filter).Decode(&saved); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(saved, first) {
				t.Fatal("conflict overwrote existing document")
			}
		})
	}
}
