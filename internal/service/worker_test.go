package service

import (
	"context"
	"delivery-service/internal/messaging"
	"delivery-service/internal/model"
	"delivery-service/internal/repository"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
	"sync"
	"testing"
	"time"
)

type workerPackagesFake struct {
	mu              sync.Mutex
	record          *model.CalculationRecord
	getErr, saveErr error
	saves           int
}

func (f *workerPackagesFake) GetCalculation(context.Context, string) (model.CalculationRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return model.CalculationRecord{}, f.getErr
	}
	if f.record == nil {
		return model.CalculationRecord{}, pgx.ErrNoRows
	}
	return *f.record, nil
}
func (f *workerPackagesFake) SaveCalculation(_ context.Context, p model.CalculationRecord) (model.CalculationRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saves++
	if f.saveErr != nil {
		return model.CalculationRecord{}, f.saveErr
	}
	if f.record == nil || f.record.Status != "calculated" {
		p.PackageTypeName = "electronics"
		f.record = &p
	}
	return *f.record, nil
}

type workerLogsFake struct {
	mu    sync.Mutex
	err   error
	calls int
	log   *model.CalculationLog
}

func (f *workerLogsFake) Upsert(_ context.Context, l model.CalculationLog) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return f.err
	}
	if f.log == nil {
		f.log = &l
	}
	return nil
}
func workerMessage() messaging.PackageMessage {
	return messaging.PackageMessage{SchemaVersion: 1, PackageID: uuid.NewString(), SessionID: uuid.NewString(), Name: "Phone", WeightKG: "2", PackageTypeID: 2, ContentValueUSD: "100", CreatedAt: time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC), TraceID: uuid.NewString()}
}
func newTestWorker(t *testing.T, p workerPackageRepository, r exchangeRateProvider, l calculationLogRepository) *WorkerService {
	t.Helper()
	s, err := NewWorkerService(p, r, l)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestWorkerNewAndRedelivery(t *testing.T) {
	m := workerMessage()
	packages := &workerPackagesFake{}
	rates := &rateStub{rate: decimal.NewFromInt(90)}
	logs := &workerLogsFake{}
	s := newTestWorker(t, packages, rates, logs)
	if err := s.Process(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	first := *packages.record
	if first.Status != "calculated" || !first.CreatedAt.Equal(m.CreatedAt) || !first.DeliveryCostRUB.Equal(decimal.NewFromInt(180)) || logs.log.Package.PackageTypeName != "electronics" {
		t.Fatalf("record=%+v", first)
	}
	rates.rate = decimal.NewFromInt(100)
	if err := s.Process(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if rates.calls != 1 || packages.saves != 1 || !packages.record.USDToRUBRate.Equal(*first.USDToRUBRate) || !packages.record.CalculatedAt.Equal(*first.CalculatedAt) || !packages.record.DeliveryCostRUB.Equal(*first.DeliveryCostRUB) {
		t.Fatal("redelivery recalculated result")
	}
}
func TestWorkerMongoFailureRetriesOnlyLog(t *testing.T) {
	failure := errors.New("mongo offline")
	m := workerMessage()
	p := &workerPackagesFake{}
	r := &rateStub{rate: decimal.NewFromInt(90)}
	l := &workerLogsFake{err: failure}
	s := newTestWorker(t, p, r, l)
	if err := s.Process(context.Background(), m); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	first := *p.record
	l.err = nil
	r.err = errors.New("rate now unavailable")
	if err := s.Process(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if r.calls != 1 || p.saves != 1 || l.calls != 2 || !l.log.Package.CalculatedAt.Equal(*first.CalculatedAt) {
		t.Fatal("retry did not reuse canonical result")
	}
}
func TestWorkerFailures(t *testing.T) {
	failure := errors.New("dependency failure")
	for _, stage := range []string{"validation", "lookup", "rate", "save", "mongo", "identity", "legacy metadata"} {
		t.Run(stage, func(t *testing.T) {
			m := workerMessage()
			p := &workerPackagesFake{}
			r := &rateStub{rate: decimal.NewFromInt(90)}
			l := &workerLogsFake{}
			want := failure
			switch stage {
			case "validation":
				m.PackageID = "bad"
				want = ErrInvalidPackageMessage
			case "lookup":
				p.getErr = failure
			case "rate":
				r.err = failure
			case "save":
				p.saveErr = failure
			case "mongo":
				l.err = failure
			case "identity", "legacy metadata":
				p.record = &model.CalculationRecord{PackageID: m.PackageID, SessionID: m.SessionID, Name: m.Name, PackageTypeID: 2, WeightKG: decimal.NewFromInt(2), ContentValueUSD: decimal.NewFromInt(100), Status: "calculated"}
				if stage == "identity" {
					p.record.SessionID = uuid.NewString()
					want = ErrPackageIdentityConflict
				} else {
					want = ErrIncompleteCalculation
				}
			}
			err := newTestWorker(t, p, r, l).Process(context.Background(), m)
			if !errors.Is(err, want) {
				t.Fatalf("error=%v want=%v", err, want)
			}
			if stage == "identity" || stage == "legacy metadata" || stage == "validation" || stage == "lookup" {
				if r.calls != 0 || p.saves != 0 || l.calls != 0 {
					t.Fatal("failure caused side effects")
				}
			}
		})
	}
}

// A barrier makes both callers observe the absent package before either saves.
type barrierRate struct {
	mu    sync.Mutex
	calls int
	ready chan struct{}
}

func (f *barrierRate) GetUSDRUB(ctx context.Context) (decimal.Decimal, error) {
	f.mu.Lock()
	f.calls++
	n := f.calls
	if n == 2 {
		close(f.ready)
	}
	f.mu.Unlock()
	select {
	case <-f.ready:
		return decimal.NewFromInt(int64(89 + n)), nil
	case <-ctx.Done():
		return decimal.Zero, ctx.Err()
	}
}
func TestWorkerConcurrentCallsUseOneCanonicalResult(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m := workerMessage()
	p := &workerPackagesFake{}
	r := &barrierRate{ready: make(chan struct{})}
	l := &workerLogsFake{}
	s := newTestWorker(t, p, r, l)
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- s.Process(ctx, m) }()
	}
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if p.saves != 2 || l.calls != 2 || !l.log.Package.DeliveryCostRUB.Equal(*p.record.DeliveryCostRUB) || !l.log.Package.USDToRUBRate.Equal(*p.record.USDToRUBRate) || !l.log.Package.CalculatedAt.Equal(*p.record.CalculatedAt) {
		t.Fatal("different canonical results")
	}
}

func TestWorkerPropagatesMongoConflict(t *testing.T) {
	m := workerMessage()
	packages := &workerPackagesFake{}
	rates := &rateStub{rate: decimal.NewFromInt(90)}
	logs := &workerLogsFake{err: repository.ErrCalculationLogConflict}
	err := newTestWorker(t, packages, rates, logs).Process(context.Background(), m)
	if !errors.Is(err, repository.ErrCalculationLogConflict) || packages.record == nil {
		t.Fatalf("canonical result/conflict lost: %v", err)
	}
}
