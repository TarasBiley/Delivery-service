package service

import (
	"context"
	"delivery-service/internal/model"
	"errors"
	"testing"
	"time"
)

type sumFunc func(context.Context, string, time.Time, time.Time) ([]model.DailySumItem, error)

func (f sumFunc) DailySum(c context.Context, s string, a, b time.Time) ([]model.DailySumItem, error) {
	return f(c, s, a, b)
}
func TestAnalyticsDailySum(t *testing.T) {
	for _, date := range []string{"", "2026-02-30", "2026-1-01", "2026-10-11T00:00:00Z", "2026-10-11 "} {
		svc := NewAnalyticsService(sumFunc(func(context.Context, string, time.Time, time.Time) ([]model.DailySumItem, error) {
			t.Fatal("invalid date called repository")
			return nil, nil
		}))
		if _, err := svc.DailySum(context.Background(), "session", date); !errors.Is(err, ErrInvalidDate) {
			t.Fatalf("date=%q err=%v", date, err)
		}
	}
	for _, date := range []string{"2026-10-11", "2024-02-29", "2026-12-31"} {
		svc := NewAnalyticsService(sumFunc(func(_ context.Context, session string, start, end time.Time) ([]model.DailySumItem, error) {
			if session != "session" || start.Location() != time.UTC || start.Format("2006-01-02") != date || end.Sub(start) != 24*time.Hour || start.Hour() != 0 {
				t.Fatalf("boundaries=%v..%v session=%s", start, end, session)
			}
			return nil, nil
		}))
		result, err := svc.DailySum(context.Background(), "session", date)
		if err != nil || result.Date != date || result.Data == nil || len(result.Data) != 0 {
			t.Fatalf("response=%+v err=%v", result, err)
		}
	}
	failure := errors.New("mongo down")
	svc := NewAnalyticsService(sumFunc(func(context.Context, string, time.Time, time.Time) ([]model.DailySumItem, error) { return nil, failure }))
	if _, err := svc.DailySum(context.Background(), "session", "2026-10-11"); !errors.Is(err, failure) {
		t.Fatal(err)
	}
}
