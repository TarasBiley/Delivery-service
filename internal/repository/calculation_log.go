package repository

import (
	"context"
	"delivery-service/internal/model"
	"errors"
	"fmt"
	"github.com/shopspring/decimal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"time"
)

// CalculationLogRepository stores immutable calculation logs keyed by package UUID.
type CalculationLogRepository struct{ collection *mongo.Collection }

// NewCalculationLogRepository uses the calculation_logs collection.
func NewCalculationLogRepository(db *mongo.Database) *CalculationLogRepository {
	return &CalculationLogRepository{collection: db.Collection("calculation_logs")}
}

// EnsureIndexes idempotently creates the per-session daily analytics index.
// MongoDB already provides the unique _id index.
func (r *CalculationLogRepository) EnsureIndexes(ctx context.Context) error {
	_, err := r.collection.Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "session_id", Value: 1}, {Key: "calculated_at", Value: 1}}})
	if err != nil {
		return fmt.Errorf("create calculation log index: %w", err)
	}
	return nil
}

type calculationLogDocument struct {
	ID              string          `bson:"_id"`
	PackageID       string          `bson:"package_id"`
	SessionID       string          `bson:"session_id"`
	PackageTypeID   int             `bson:"package_type_id"`
	PackageTypeName string          `bson:"package_type_name"`
	WeightKG        bson.Decimal128 `bson:"weight_kg"`
	ContentValueUSD bson.Decimal128 `bson:"content_value_usd"`
	USDToRUBRate    bson.Decimal128 `bson:"usd_to_rub_rate"`
	DeliveryCostRUB bson.Decimal128 `bson:"delivery_cost_rub"`
	CreatedAt       time.Time       `bson:"created_at"`
	CalculatedAt    time.Time       `bson:"calculated_at"`
	TraceID         string          `bson:"trace_id"`
}

func logDocument(log model.CalculationLog) (calculationLogDocument, error) {
	p := log.Package
	if p.DeliveryCostRUB == nil || p.USDToRUBRate == nil || p.CalculatedAt == nil {
		return calculationLogDocument{}, errors.New("incomplete calculation log")
	}
	d := calculationLogDocument{ID: p.PackageID, PackageID: p.PackageID, SessionID: p.SessionID, PackageTypeID: p.PackageTypeID, PackageTypeName: p.PackageTypeName, CreatedAt: p.CreatedAt, CalculatedAt: *p.CalculatedAt, TraceID: log.TraceID}
	for _, field := range []struct {
		name  string
		value decimal.Decimal
		dest  *bson.Decimal128
	}{
		{"weight_kg", p.WeightKG, &d.WeightKG}, {"content_value_usd", p.ContentValueUSD, &d.ContentValueUSD},
		{"usd_to_rub_rate", *p.USDToRUBRate, &d.USDToRUBRate}, {"delivery_cost_rub", *p.DeliveryCostRUB, &d.DeliveryCostRUB},
	} {
		value, err := bson.ParseDecimal128(field.value.String())
		if err != nil {
			return calculationLogDocument{}, fmt.Errorf("convert %s to Decimal128: %w", field.name, err)
		}
		*field.dest = value
	}
	return d, nil
}

// ErrCalculationLogConflict reports a mismatch with the canonical PostgreSQL result.
var ErrCalculationLogConflict = errors.New("calculation log conflicts with canonical result")

// Upsert inserts once, then verifies existing canonical business data without overwriting.
func (r *CalculationLogRepository) Upsert(ctx context.Context, log model.CalculationLog) error {
	doc, err := logDocument(log)
	if err != nil {
		return err
	}
	filter := bson.D{{Key: "_id", Value: doc.ID}}
	update := bson.D{{Key: "$setOnInsert", Value: doc}}
	result, err := r.collection.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true))
	// Two simultaneous upserts can collide on _id. The winner already exists;
	// retry without insertion, preserving its original fields.
	if mongo.IsDuplicateKeyError(err) {
		result, err = r.collection.UpdateOne(ctx, filter, update)
		if err == nil && result.MatchedCount != 1 {
			return errors.New("calculation log disappeared after concurrent upsert")
		}
	}
	if err != nil {
		return fmt.Errorf("upsert calculation log: %w", err)
	}
	if result.UpsertedCount == 1 {
		return nil
	}
	var raw bson.Raw
	if err := r.collection.FindOne(ctx, filter).Decode(&raw); err != nil {
		return fmt.Errorf("read existing calculation log: %w", err)
	}
	for _, field := range []string{"_id", "package_id", "session_id", "package_type_id", "weight_kg", "content_value_usd", "usd_to_rub_rate", "delivery_cost_rub", "created_at", "calculated_at"} {
		if value := raw.Lookup(field); value.Type == 0 || value.Type == bson.TypeNull {
			return fmt.Errorf("%w: package_id=%s missing %s", ErrCalculationLogConflict, doc.ID, field)
		}
	}
	var existing calculationLogDocument
	if err := bson.Unmarshal(raw, &existing); err != nil {
		return fmt.Errorf("%w: package_id=%s malformed document: %v", ErrCalculationLogConflict, doc.ID, err)
	}
	return verifyCalculationLog(existing, doc)
}

// DailySum aggregates exact Decimal128 costs for one session and UTC interval.
// The latest stored name represents each type, even if it was renamed.
func (r *CalculationLogRepository) DailySum(ctx context.Context, sessionID string, start, end time.Time) ([]model.DailySumItem, error) {
	pipeline := mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.D{{Key: "session_id", Value: sessionID}, {Key: "calculated_at", Value: bson.D{{Key: "$gte", Value: start}, {Key: "$lt", Value: end}}}}}},
		bson.D{{Key: "$sort", Value: bson.D{{Key: "calculated_at", Value: 1}, {Key: "_id", Value: 1}}}},
		bson.D{{Key: "$group", Value: bson.D{{Key: "_id", Value: "$package_type_id"}, {Key: "package_type_name", Value: bson.D{{Key: "$last", Value: "$package_type_name"}}}, {Key: "delivery_cost_rub", Value: bson.D{{Key: "$sum", Value: "$delivery_cost_rub"}}}, {Key: "package_count", Value: bson.D{{Key: "$sum", Value: 1}}}}}},
		bson.D{{Key: "$sort", Value: bson.D{{Key: "_id", Value: 1}}}},
	}
	cursor, err := r.collection.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, fmt.Errorf("aggregate daily sum: %w", err)
	}
	defer func() { _ = cursor.Close(ctx) }()
	items := []model.DailySumItem{}
	for cursor.Next(ctx) {
		var row struct {
			ID    int             `bson:"_id"`
			Name  string          `bson:"package_type_name"`
			Count int64           `bson:"package_count"`
			Sum   bson.Decimal128 `bson:"delivery_cost_rub"`
		}
		if err := cursor.Decode(&row); err != nil {
			return nil, fmt.Errorf("decode daily sum: %w", err)
		}
		sum, err := decimal.NewFromString(row.Sum.String())
		if err != nil {
			return nil, fmt.Errorf("decode exact sum: %w", err)
		}
		items = append(items, model.DailySumItem{PackageTypeID: row.ID, PackageTypeName: row.Name, PackageCount: row.Count, DeliveryCostRUB: sum.StringFixed(2)})
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("read daily sum: %w", err)
	}
	return items, nil
}

// Trace belongs to an attempt, not the business result; recovery may have another
// trace. Type names are mutable dictionary values read from PostgreSQL, so keep
// the first log's name snapshot rather than reject a legitimate type rename.
func verifyCalculationLog(existing, expected calculationLogDocument) error {
	mismatch := func(field string) error {
		return fmt.Errorf("%w: package_id=%s mismatch %s", ErrCalculationLogConflict, expected.ID, field)
	}
	for _, field := range []struct{ name, got, want string }{{"_id", existing.ID, expected.ID}, {"package_id", existing.PackageID, expected.PackageID}, {"session_id", existing.SessionID, expected.SessionID}} {
		if field.got != field.want {
			return mismatch(field.name)
		}
	}
	if existing.PackageTypeID != expected.PackageTypeID {
		return mismatch("package_type_id")
	}
	for _, field := range []struct {
		name      string
		got, want bson.Decimal128
	}{
		{"weight_kg", existing.WeightKG, expected.WeightKG}, {"content_value_usd", existing.ContentValueUSD, expected.ContentValueUSD},
		{"usd_to_rub_rate", existing.USDToRUBRate, expected.USDToRUBRate}, {"delivery_cost_rub", existing.DeliveryCostRUB, expected.DeliveryCostRUB},
	} {
		got, err := decimal.NewFromString(field.got.String())
		if err != nil {
			return mismatch(field.name)
		}
		want, err := decimal.NewFromString(field.want.String())
		if err != nil || !got.Equal(want) {
			return mismatch(field.name)
		}
	}
	if existing.CreatedAt.UnixMilli() != expected.CreatedAt.UnixMilli() {
		return mismatch("created_at")
	}
	if existing.CalculatedAt.UnixMilli() != expected.CalculatedAt.UnixMilli() {
		return mismatch("calculated_at")
	}
	return nil
}
