package telegram

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLimiterSpacesMessagesPerChat(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewLimiterWith(0, time.Second, 3*time.Second)
	l.now = func() time.Time { return now }
	steps := []struct {
		chat int64
		want time.Duration
	}{
		{42, 0}, {42, time.Second}, {7, 0}, {-100, 0}, {-100, 3 * time.Second},
	}
	for i, s := range steps {
		if got := l.reserveChat(s.chat); got != s.want {
			t.Errorf("step %d (chat %d): %v, want %v", i, s.chat, got, s.want)
		}
	}
}

func TestLimiterGlobal(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewLimiterWith(100*time.Millisecond, 0, 0)
	l.now = func() time.Time { return now }
	for i, want := range []time.Duration{0, 100 * time.Millisecond, 200 * time.Millisecond} {
		if got := l.reserveGlobal(); got != want {
			t.Errorf("reservation %d: %v, want %v", i, got, want)
		}
	}
}

func TestWaitHonoursContext(t *testing.T) {
	l := NewLimiterWith(0, time.Hour, time.Hour)
	if err := l.Wait(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.Wait(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
