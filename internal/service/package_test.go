package service

import (
	"context"
	"delivery-service/internal/messaging"
	"delivery-service/internal/model"
	"delivery-service/internal/tracing"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"reflect"
	"testing"
	"time"
)

type packageStub struct {
	create func(context.Context, model.Package) (model.Package, error)
	all    func(context.Context, string, model.PackageListFilter) ([]model.PackageListItem, int, error)
	byID   func(context.Context, string, string) (model.PackageListItem, error)
}

func (f packageStub) Create(c context.Context, p model.Package) (model.Package, error) {
	return f.create(c, p)
}
func (f packageStub) GetAll(c context.Context, s string, q model.PackageListFilter) ([]model.PackageListItem, int, error) {
	return f.all(c, s, q)
}
func (f packageStub) GetByID(c context.Context, s, id string) (model.PackageListItem, error) {
	return f.byID(c, s, id)
}

type typeStub func(context.Context, int) (bool, error)

func (f typeStub) Exists(c context.Context, id int) (bool, error) { return f(c, id) }

type publishFunc func(context.Context, messaging.PackageMessage) error

func (f publishFunc) Publish(ctx context.Context, m messaging.PackageMessage) error { return f(ctx, m) }
func TestPackageServiceCreate(t *testing.T) {
	failure := errors.New("publisher failure")
	for _, tc := range []struct {
		name                         string
		exists                       bool
		typeErr, publishErr, wantErr error
	}{
		{name: "success", exists: true}, {name: "missing type", wantErr: ErrPackageTypeNotFound},
		{name: "type failure", typeErr: failure, wantErr: failure},
		{name: "publisher error", exists: true, publishErr: failure, wantErr: failure},
		{name: "routing failure", exists: true, publishErr: messaging.ErrUnroutable, wantErr: messaging.ErrUnroutable},
		{name: "negative confirm", exists: true, publishErr: messaging.ErrNegativeConfirm, wantErr: messaging.ErrNegativeConfirm},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := model.RequestDecimal("100")
			req := model.CreatePackageRequest{Name: "Phone", WeightKG: model.RequestDecimal("1.5"), PackageTypeID: 3, ContentValueUSD: &value}
			trace, session := uuid.NewString(), uuid.NewString()
			ctx := tracing.WithID(context.Background(), trace)
			checked, published := false, false
			var message messaging.PackageMessage
			repo := packageStub{create: func(context.Context, model.Package) (model.Package, error) {
				t.Fatal("POST must not INSERT")
				return model.Package{}, nil
			}}
			svc := NewPackageService(repo, typeStub(func(_ context.Context, id int) (bool, error) {
				checked = true
				if id != 3 {
					t.Fatalf("type=%d", id)
				}
				return tc.exists, tc.typeErr
			}), publishFunc(func(c context.Context, m messaging.PackageMessage) error {
				published = true
				message = m
				if !checked || c != ctx {
					t.Fatal("publish before validation or wrong context")
				}
				id, err := uuid.Parse(m.PackageID)
				if err != nil || id.Version() != 4 {
					t.Fatalf("invalid UUID: %s", m.PackageID)
				}
				if m.SchemaVersion != 1 || m.TraceID != trace || m.SessionID != session || m.Name != req.Name || m.WeightKG != "1.5" || m.ContentValueUSD != "100" || m.PackageTypeID != 3 || m.CreatedAt.IsZero() {
					t.Fatalf("message=%+v", m)
				}
				return tc.publishErr
			}), nil)
			before := time.Now().Truncate(time.Microsecond)
			got, err := svc.Create(ctx, session, req)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error=%v want=%v", err, tc.wantErr)
			}
			if !checked || published != (tc.exists && tc.typeErr == nil) {
				t.Fatal("dependency calls")
			}
			if err == nil && (got.Status != "registered" || got.PackageID != message.PackageID || !got.CreatedAt.Equal(message.CreatedAt) || got.CreatedAt.Before(before) || got.CreatedAt.After(time.Now())) {
				t.Fatalf("response=%+v message=%+v", got, message)
			}
		})
	}
}
func TestPackageServiceGetAll(t *testing.T) {
	failure := errors.New("list failure")
	typeID := 3
	calculated := false
	filter := model.PackageListFilter{Page: 2, Limit: 20, PackageTypeID: &typeID, Calculated: &calculated}
	for _, tc := range []struct {
		name         string
		total, pages int
		err          error
	}{{"empty", 0, 0, nil}, {"partial page", 21, 2, nil}, {"exact page", 40, 2, nil}, {"error", 0, 0, failure}} {
		t.Run(tc.name, func(t *testing.T) {
			items := []model.PackageListItem{}
			if tc.total > 0 {
				items = append(items, model.PackageListItem{PackageID: "id"})
			}
			repo := packageStub{all: func(_ context.Context, s string, q model.PackageListFilter) ([]model.PackageListItem, int, error) {
				if s != "session" || !reflect.DeepEqual(q, filter) {
					t.Fatalf("arguments=%s %+v", s, q)
				}
				return items, tc.total, tc.err
			}}
			got, err := NewPackageService(repo, nil, nil, nil).GetAll(context.Background(), "session", filter)
			if !errors.Is(err, tc.err) {
				t.Fatal(err)
			}
			if err == nil && (!reflect.DeepEqual(got.Data, items) || got.Pagination != (model.Pagination{Page: 2, Limit: 20, Total: tc.total, TotalPages: tc.pages})) {
				t.Fatalf("response=%+v", got)
			}
		})
	}
}
func TestPackageServiceGetByID(t *testing.T) {
	failure := errors.New("lookup failure")
	for _, tc := range []struct {
		name             string
		repoErr, wantErr error
	}{{"success", nil, nil}, {"not found", fmt.Errorf("lookup: %w", pgx.ErrNoRows), ErrPackageNotFound}, {"error", failure, failure}} {
		t.Run(tc.name, func(t *testing.T) {
			item := model.PackageListItem{PackageID: "package", Name: "Phone"}
			repo := packageStub{byID: func(_ context.Context, s, id string) (model.PackageListItem, error) {
				if s != "owner" || id != "package" {
					t.Fatalf("arguments=%s %s", s, id)
				}
				return item, tc.repoErr
			}}
			got, err := NewPackageService(repo, nil, nil, nil).GetByID(context.Background(), "owner", "package")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error=%v", err)
			}
			if err == nil && !reflect.DeepEqual(got, item) {
				t.Fatalf("item=%+v", got)
			}
		})
	}
}
