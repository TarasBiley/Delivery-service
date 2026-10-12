package service

import (
	"context"
	"delivery-service/internal/messaging"
	"delivery-service/internal/model"
	"delivery-service/internal/tracing"
	"errors"
	"fmt"
	"github.com/shopspring/decimal"
	"testing"
)

type recoveryStub struct {
	records []model.CalculationRecord
	err     error
	pages   int
}

func (r *recoveryStub) GetRecoveryBatch(_ context.Context, after string, n int) ([]model.CalculationRecord, error) {
	r.pages++
	if r.err != nil {
		return nil, r.err
	}
	if len(r.records) == 0 {
		return nil, nil
	}
	if after != "" && after >= r.records[0].PackageID {
		return nil, errors.New("invalid pagination")
	}
	if n > len(r.records) {
		n = len(r.records)
	}
	result := r.records[:n]
	r.records = r.records[n:]
	return result, nil
}

type recoveryProcessFunc func(context.Context, messaging.PackageMessage) error

func (f recoveryProcessFunc) Process(c context.Context, m messaging.PackageMessage) error {
	return f(c, m)
}
func TestRecoveryEntireBacklog(t *testing.T) {
	r := &recoveryStub{}
	for i := 0; i < 205; i++ {
		r.records = append(r.records, model.CalculationRecord{PackageID: fmt.Sprintf("%03d", i), WeightKG: decimal.NewFromInt(1), Status: "calculation_pending"})
	}
	// Complete calculated records also replay Mongo writes, but do not inflate count.
	r.records = append(r.records, model.CalculationRecord{PackageID: "999", Status: "calculated"})
	calls := 0
	svc := NewCalculationService(r, recoveryProcessFunc(func(ctx context.Context, m messaging.PackageMessage) error {
		calls++
		if m.TraceID != "trace" {
			t.Fatal("trace lost")
		}
		return nil
	}))
	n, err := svc.CalculatePending(tracing.WithID(context.Background(), "trace"))
	if err != nil || n != 205 || calls != 206 || r.pages != 4 {
		t.Fatalf("n=%d calls=%d pages=%d err=%v", n, calls, r.pages, err)
	}
}
func TestRecoveryFailures(t *testing.T) {
	failure := errors.New("failure")
	for _, stage := range []string{"read", "process", "cancel", "empty"} {
		t.Run(stage, func(t *testing.T) {
			r := &recoveryStub{records: []model.CalculationRecord{{PackageID: "id"}}}
			ctx := context.Background()
			want := failure
			calls := 0
			switch stage {
			case "read":
				r.err = failure
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
			case "empty":
				r.records = nil
				want = nil
			}
			svc := NewCalculationService(r, recoveryProcessFunc(func(context.Context, messaging.PackageMessage) error { calls++; return failure }))
			n, err := svc.CalculatePending(ctx)
			if n != 0 || !errors.Is(err, want) {
				t.Fatalf("n=%d err=%v", n, err)
			}
			if stage != "process" && calls != 0 {
				t.Fatal("unexpected worker call")
			}
		})
	}
}

func TestRecoveryRepairsMongoWithoutRecalculation(t *testing.T) {
	m := workerMessage()
	record := model.CalculationRecord{PackageID: m.PackageID, SessionID: m.SessionID, Name: m.Name, PackageTypeID: m.PackageTypeID, WeightKG: decimal.RequireFromString(m.WeightKG), ContentValueUSD: decimal.RequireFromString(m.ContentValueUSD), CreatedAt: m.CreatedAt, Status: "calculation_pending"}
	packages := &workerPackagesFake{record: &record}
	rates := &rateStub{rate: decimal.NewFromInt(90)}
	failure := errors.New("mongo unavailable")
	logs := &workerLogsFake{err: failure}
	worker := newTestWorker(t, packages, rates, logs)
	ctx := tracing.WithID(context.Background(), m.TraceID)
	n, err := NewCalculationService(&recoveryStub{records: []model.CalculationRecord{record}}, worker).CalculatePending(ctx)
	if n != 0 || !errors.Is(err, failure) {
		t.Fatalf("n=%d err=%v", n, err)
	}
	canonical := *packages.record
	logs.err = nil
	n, err = NewCalculationService(&recoveryStub{records: []model.CalculationRecord{canonical}}, worker).CalculatePending(ctx)
	if err != nil || n != 0 || rates.calls != 1 || packages.saves != 1 || logs.log == nil || !logs.log.Package.CalculatedAt.Equal(*canonical.CalculatedAt) {
		t.Fatalf("repair recalculated/lost log: n=%d err=%v", n, err)
	}
}
