package cache

import (
	"context"
	"errors"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
	"net"
	"testing"
	"time"
)

// Intercepts commands before network I/O; preserves the real client's command encoding.
type redisStub struct {
	values    map[string]string
	ttls      map[string]time.Duration
	err       error
	pipelines int
}

func (f *redisStub) DialHook(redis.DialHook) redis.DialHook {
	return func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("unexpected Redis connection")
	}
}
func (f *redisStub) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		if f.err != nil {
			cmd.SetErr(f.err)
			return f.err
		}
		value, ok := f.values[cmd.Args()[1].(string)]
		c := cmd.(*redis.StringCmd)
		if !ok {
			c.SetErr(redis.Nil)
			return redis.Nil
		}
		c.SetVal(value)
		return nil
	}
}
func (f *redisStub) ProcessPipelineHook(redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(_ context.Context, cmds []redis.Cmder) error {
		f.pipelines++
		if f.err != nil {
			for _, c := range cmds {
				c.SetErr(f.err)
			}
			return f.err
		}
		if cmds[0].Name() != "multi" || cmds[len(cmds)-1].Name() != "exec" {
			return errors.New("expected transaction")
		}
		for _, c := range cmds[1 : len(cmds)-1] {
			a := c.Args()
			key := a[1].(string)
			f.values[key] = a[2].(string)
			f.ttls[key] = 0
			if len(a) > 3 {
				if a[3] != "ex" {
					return errors.New("unexpected TTL unit")
				}
				f.ttls[key] = time.Duration(a[4].(int64)) * time.Second
			}
		}
		return nil
	}
}
func newRedisCache(t *testing.T) (*ExchangeRateCache, *redisStub) {
	t.Helper()
	stub := &redisStub{values: map[string]string{}, ttls: map[string]time.Duration{}}
	client := redis.NewClient(&redis.Options{})
	client.AddHook(stub)
	t.Cleanup(func() { _ = client.Close() })
	return NewExchangeRateCache(client), stub
}
func TestExchangeRateCacheRoundTrip(t *testing.T) {
	cache, stub := newRedisCache(t)
	rate := decimal.RequireFromString("95.1234567890123456789")
	ctx := context.Background()
	if err := cache.Set(ctx, rate); err != nil {
		t.Fatal(err)
	}
	for _, get := range []func(context.Context) (decimal.Decimal, bool, error){cache.Get, cache.GetLast} {
		got, found, err := get(ctx)
		if err != nil || !found || !got.Equal(rate) {
			t.Fatalf("got=%s found=%v err=%v", got, found, err)
		}
	}
	if stub.ttls[usdRubKey] != time.Hour || stub.ttls[usdRubLastKey] != 0 || stub.pipelines != 1 {
		t.Fatalf("TTL=%v pipelines=%d", stub.ttls, stub.pipelines)
	}
	delete(stub.values, usdRubKey)
	_, found, err := cache.Get(ctx)
	if err != nil || found {
		t.Fatalf("missing fresh: found=%v err=%v", found, err)
	}
	_, found, err = cache.GetLast(ctx)
	if err != nil || !found {
		t.Fatal("last rate lost")
	}
}
func TestExchangeRateCacheReadErrors(t *testing.T) {
	for _, last := range []bool{false, true} {
		for _, value := range []string{"missing", "bad", "NaN", "Infinity", "0", "-1", "redis-error"} {
			t.Run(map[bool]string{false: "fresh/", true: "last/"}[last]+value, func(t *testing.T) {
				cache, stub := newRedisCache(t)
				key := usdRubKey
				get := cache.Get
				if last {
					key = usdRubLastKey
					get = cache.GetLast
				}
				failure := errors.New("redis error")
				if value == "redis-error" {
					stub.err = failure
				} else if value != "missing" {
					stub.values[key] = value
				}
				_, found, err := get(context.Background())
				if found {
					t.Fatal("invalid rate accepted")
				}
				if value == "missing" {
					if err != nil {
						t.Fatal(err)
					}
				} else if err == nil {
					t.Fatal("expected error")
				}
				if value == "redis-error" && !errors.Is(err, failure) {
					t.Fatal(err)
				}
			})
		}
	}
}
func TestExchangeRateCacheSetErrors(t *testing.T) {
	for _, value := range []string{"0", "-1", "90"} {
		t.Run(value, func(t *testing.T) {
			cache, stub := newRedisCache(t)
			failure := errors.New("redis error")
			if value == "90" {
				stub.err = failure
			}
			err := cache.Set(context.Background(), decimal.RequireFromString(value))
			if err == nil {
				t.Fatal("expected error")
			}
			if value == "90" {
				if !errors.Is(err, failure) {
					t.Fatal(err)
				}
			} else if stub.pipelines != 0 {
				t.Fatal("invalid rate sent to Redis")
			}
		})
	}
}
