package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

type lifecycleWorkerFake struct {
	starts           atomic.Int32
	started, stopped chan struct{}
	fatal            error
	drain            bool
}

func (w *lifecycleWorkerFake) Run(ctx context.Context) error {
	w.starts.Add(1)
	close(w.started)
	if w.fatal != nil {
		return w.fatal
	}
	if w.drain {
		<-ctx.Done()
	} else {
		select {
		case <-w.stopped:
		case <-ctx.Done():
		}
	}
	return nil
}
func (w *lifecycleWorkerFake) Stop() { close(w.stopped) }

type lifecycleHTTPFake struct{ stopped chan struct{} }

func (s *lifecycleHTTPFake) Start() <-chan error            { return make(chan error) }
func (s *lifecycleHTTPFake) Shutdown(context.Context) error { close(s.stopped); return nil }
func TestLifecycle(t *testing.T) {
	for _, mode := range []string{"signal", "fatal", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w := &lifecycleWorkerFake{started: make(chan struct{}), stopped: make(chan struct{}), drain: mode == "deadline"}
			if mode == "fatal" {
				w.fatal = errors.New("lost consumer")
			}
			s := &lifecycleHTTPFake{stopped: make(chan struct{})}
			forced := false
			result := make(chan int, 1)
			go func() {
				result <- runLifecycle(ctx, 20*time.Millisecond, s, w, func() { forced = true }, slog.New(slog.NewTextHandler(io.Discard, nil)))
			}()
			<-w.started
			if mode != "fatal" {
				cancel()
			}
			select {
			case code := <-result:
				want := 0
				if mode == "fatal" {
					want = 1
				}
				if code != want || w.starts.Load() != 1 || forced != (mode == "deadline") {
					t.Fatalf("code=%d starts=%d forced=%v", code, w.starts.Load(), forced)
				}
			case <-time.After(time.Second):
				t.Fatal("lifecycle did not exit")
			}
			select {
			case <-s.stopped:
			default:
				t.Fatal("HTTP not stopped")
			}
			select {
			case <-w.stopped:
			default:
				t.Fatal("consumer not stopped")
			}
		})
	}
}
