package service

import (
	"bytes"
	"context"
	"delivery-service/internal/messaging"
	"delivery-service/internal/model"
	"delivery-service/internal/tracing"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"sync"
	"testing"
)

type reservationFake struct {
	mu      sync.Mutex
	records map[string]model.PackageRegistration
	err     error
}

func (r *reservationFake) Reserve(_ context.Context, p model.PackageRegistration) (model.PackageRegistration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return model.PackageRegistration{}, r.err
	}
	if r.records == nil {
		r.records = map[string]model.PackageRegistration{}
	}
	key := p.SessionID + ":" + p.IdempotencyKey
	if saved, ok := r.records[key]; ok {
		if !bytes.Equal(saved.RequestHash, p.RequestHash) {
			return model.PackageRegistration{}, ErrIdempotencyKeyConflict
		}
		return saved, nil
	}
	r.records[key] = p
	return p, nil
}
func TestCreateIdempotency(t *testing.T) {
	reserve := &reservationFake{}
	var messages []messaging.PackageMessage
	service := NewPackageService(packageStub{}, typeStub(func(context.Context, int) (bool, error) { return true, nil }), publishFunc(func(_ context.Context, m messaging.PackageMessage) error { messages = append(messages, m); return nil }), reserve)
	ctx := tracing.WithID(context.Background(), uuid.NewString())
	session := uuid.NewString()
	var request, normal model.CreatePackageRequest
	for data, p := range map[string]*model.CreatePackageRequest{`{"name":"Phone","weight_kg":1.500,"package_type_id":2,"content_value_usd":100.00}`: &request, `{"content_value_usd":1e2,"package_type_id":2,"weight_kg":1.5,"name":"Phone"}`: &normal} {
		if err := json.Unmarshal([]byte(data), p); err != nil {
			t.Fatal(err)
		}
		p.IdempotencyKey = "key"
	}
	first, err := service.Create(ctx, session, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Create(ctx, session, normal)
	if err != nil || first != second {
		t.Fatalf("first=%+v second=%+v err=%v", first, second, err)
	}
	if len(messages) != 2 || messages[0].PackageID != messages[1].PackageID || !messages[0].CreatedAt.Equal(messages[1].CreatedAt) || messages[1].WeightKG != "1.5" || messages[1].ContentValueUSD != "100" {
		t.Fatal("replay not canonical")
	}
	before := len(messages)
	for _, field := range []string{"name", "weight", "type", "content"} {
		changed := normal
		switch field {
		case "name":
			changed.Name = "other"
		case "weight":
			changed.WeightKG = "1.501"
		case "type":
			changed.PackageTypeID = 1
		case "content":
			v := model.RequestDecimal("100.01")
			changed.ContentValueUSD = &v
		}
		if _, err := service.Create(ctx, session, changed); !errors.Is(err, ErrIdempotencyKeyConflict) {
			t.Fatalf("%s: %v", field, err)
		}
	}
	if len(messages) != before {
		t.Fatal("conflicting key published")
	}
	independent, err := service.Create(ctx, uuid.NewString(), normal)
	if err != nil || independent.PackageID == first.PackageID {
		t.Fatal("sessions not independent")
	}
	normal.IdempotencyKey = ""
	one, err := service.Create(ctx, session, normal)
	if err != nil {
		t.Fatal(err)
	}
	two, err := service.Create(ctx, session, normal)
	if err != nil || one.PackageID == two.PackageID {
		t.Fatal("optional key changed old behavior")
	}
}
func TestCreateReservationAndPublishFailure(t *testing.T) {
	value := model.RequestDecimal("10")
	req := model.CreatePackageRequest{Name: "test", WeightKG: "1", PackageTypeID: 1, ContentValueUSD: &value, IdempotencyKey: "key"}
	failure := errors.New("failed")
	reserve := &reservationFake{}
	failPublish := true
	var messages []messaging.PackageMessage
	svc := NewPackageService(packageStub{}, typeStub(func(context.Context, int) (bool, error) { return true, nil }), publishFunc(func(_ context.Context, m messaging.PackageMessage) error {
		messages = append(messages, m)
		if failPublish {
			return failure
		}
		return nil
	}), reserve)
	ctx := tracing.WithID(context.Background(), uuid.NewString())
	session := uuid.NewString()
	if _, err := svc.Create(ctx, session, req); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	failPublish = false
	retry, err := svc.Create(ctx, session, req)
	if err != nil || retry.PackageID != messages[0].PackageID || !retry.CreatedAt.Equal(messages[0].CreatedAt) {
		t.Fatal("reservation lost after publication failure")
	}
	reserve.err = failure
	before := len(messages)
	if _, err := svc.Create(ctx, session, req); !errors.Is(err, failure) || len(messages) != before {
		t.Fatal("reservation error ignored")
	}
}
