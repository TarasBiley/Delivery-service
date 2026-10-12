package service

import (
	"context"
	"delivery-service/internal/model"
	"errors"
	"fmt"
	"time"
)

// ErrInvalidDate requires a real UTC calendar date in YYYY-MM-DD format.
var ErrInvalidDate = errors.New("date must be YYYY-MM-DD")

type dailySumRepository interface {
	DailySum(context.Context, string, time.Time, time.Time) ([]model.DailySumItem, error)
}

// AnalyticsService provides session-scoped UTC daily totals.
type AnalyticsService struct{ logs dailySumRepository }

// NewAnalyticsService creates analytics backed by calculation logs.
func NewAnalyticsService(logs dailySumRepository) *AnalyticsService {
	return &AnalyticsService{logs: logs}
}

// DailySum sums the half-open UTC interval [date, date+1 day).
func (s *AnalyticsService) DailySum(ctx context.Context, sessionID, date string) (model.DailySumResponse, error) {
	start, err := time.Parse("2006-01-02", date)
	if err != nil || start.Format("2006-01-02") != date {
		return model.DailySumResponse{}, ErrInvalidDate
	}
	data, err := s.logs.DailySum(ctx, sessionID, start, start.AddDate(0, 0, 1))
	if err != nil {
		return model.DailySumResponse{}, fmt.Errorf("daily sum: %w", err)
	}
	if data == nil {
		data = []model.DailySumItem{}
	}
	return model.DailySumResponse{Date: date, Data: data}, nil
}
