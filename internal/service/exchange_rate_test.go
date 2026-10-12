package service

import (
	"context"
	"errors"
	"github.com/shopspring/decimal"
	"io"
	"log/slog"
	"testing"
)

type rateStub struct {
	rate  decimal.Decimal
	err   error
	calls int
}

func (f *rateStub) GetUSDRUB(context.Context) (decimal.Decimal, error) {
	f.calls++
	return f.rate, f.err
}

type rateCacheStub struct {
	fresh, last             decimal.Decimal
	found, lastFound        bool
	getErr, lastErr, setErr error
	sets, lastCalls         int
	saved                   decimal.Decimal
}

func (f *rateCacheStub) Get(context.Context) (decimal.Decimal, bool, error) {
	return f.fresh, f.found, f.getErr
}
func (f *rateCacheStub) GetLast(context.Context) (decimal.Decimal, bool, error) {
	f.lastCalls++
	return f.last, f.lastFound, f.lastErr
}
func (f *rateCacheStub) Set(_ context.Context, d decimal.Decimal) error {
	f.sets++
	f.saved = d
	return f.setErr
}
func TestExchangeRateService(t *testing.T) {
	failure := errors.New("dependency unavailable")
	for _, tc := range []struct {
		name                   string
		cache                  rateCacheStub
		clientErr              error
		want                   string
		calls, sets, lastCalls int
		wantErr                error
		anyErr                 bool
	}{
		{name: "fresh cache", cache: rateCacheStub{fresh: decimal.NewFromInt(90), found: true}, want: "90"},
		{name: "cache miss", want: "95.1234", calls: 1, sets: 1},
		{name: "cache read error", cache: rateCacheStub{getErr: failure}, want: "95.1234", calls: 1, sets: 1},
		{name: "cache write error", cache: rateCacheStub{setErr: failure}, want: "95.1234", calls: 1, sets: 1},
		{name: "fallback", cache: rateCacheStub{last: decimal.NewFromInt(85), lastFound: true}, clientErr: failure, want: "85", calls: 1, lastCalls: 1},
		{name: "no fallback", clientErr: failure, calls: 1, lastCalls: 1, anyErr: true},
		{name: "fallback cache error", cache: rateCacheStub{lastErr: failure}, clientErr: failure, calls: 1, lastCalls: 1, wantErr: failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := tc.cache
			client := &rateStub{rate: decimal.RequireFromString("95.1234"), err: tc.clientErr}
			got, err := NewExchangeRateService(&cache, client, slog.New(slog.NewTextHandler(io.Discard, nil))).GetUSDRUB(context.Background())
			if tc.anyErr {
				if err == nil {
					t.Fatal("expected unavailable error")
				}
			} else if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error=%v", err)
			}
			if err == nil && !got.Equal(decimal.RequireFromString(tc.want)) {
				t.Fatalf("rate=%s", got)
			}
			if client.calls != tc.calls || cache.sets != tc.sets || cache.lastCalls != tc.lastCalls {
				t.Fatalf("calls: client=%d set=%d last=%d", client.calls, cache.sets, cache.lastCalls)
			}
			if cache.sets > 0 && !cache.saved.Equal(client.rate) {
				t.Fatal("wrong cached rate")
			}
		})
	}
}
func TestExchangeRateServiceCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cache := &rateCacheStub{}
	client := &rateStub{err: context.Canceled}
	_, err := NewExchangeRateService(cache, client, slog.New(slog.NewTextHandler(io.Discard, nil))).GetUSDRUB(ctx)
	if !errors.Is(err, context.Canceled) || cache.lastCalls != 0 {
		t.Fatalf("error=%v fallback calls=%d", err, cache.lastCalls)
	}
}
