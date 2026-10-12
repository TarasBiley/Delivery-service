package service

import (
	"context"
	"delivery-service/internal/repository"
	"errors"
	"github.com/jackc/pgx/v5"
	"testing"
)

type claimFunc func(context.Context, string, string, int64) (int64, error)

func (f claimFunc) AssignCarrier(c context.Context, s, p string, id int64) (int64, error) {
	return f(c, s, p, id)
}
func TestCarrierService(t *testing.T) {
	failure := errors.New("database failure")
	for _, tc := range []struct{ repo, want error }{{nil, nil}, {pgx.ErrNoRows, ErrPackageNotFound}, {repository.ErrCarrierAlreadyAssigned, ErrCarrierAlreadyAssigned}, {failure, failure}} {
		svc := NewCarrierService(claimFunc(func(_ context.Context, s, p string, id int64) (int64, error) {
			if s != "owner" || p != "package" || id != 123 {
				t.Fatal("wrong claim arguments")
			}
			return id, tc.repo
		}))
		got, err := svc.AssignCarrier(context.Background(), "owner", "package", 123)
		if !errors.Is(err, tc.want) || err == nil && got.CarrierID != 123 {
			t.Fatalf("got=%+v err=%v", got, err)
		}
	}
}
