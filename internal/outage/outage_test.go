package outage

import (
	"slices"
	"testing"
	"time"

	"frigate-telegram-enhanced/internal/i18n"
)

type fake struct {
	w      *Watcher
	now    time.Time
	sent   []string
	timers []func()
}

func newFake() *fake {
	f := &fake{now: time.Date(2026, 10, 18, 14, 2, 0, 0, time.UTC)}
	f.w = New("frigate", i18n.EN, time.UTC, func(s string) { f.sent = append(f.sent, s) })
	f.w.now = func() time.Time { return f.now }
	f.w.after = func(_ time.Duration, fn func()) { f.timers = append(f.timers, fn) }
	return f
}

func (f *fake) msg(topic, payload string) {
	if !f.w.Handle(topic, []byte(payload)) {
		panic("not handled: " + topic)
	}
}

// fire runs the pending timers, as if Grace had elapsed.
func (f *fake) fire() {
	f.now = f.now.Add(Grace)
	ts := f.timers
	f.timers = nil
	for _, fn := range ts {
		fn()
	}
}

func (f *fake) expect(t *testing.T, want ...string) {
	t.Helper()
	if !slices.Equal(f.sent, want) {
		t.Fatalf("sent %q, want %q", f.sent, want)
	}
	f.sent = nil
}

func TestFrigateDownThenBack(t *testing.T) {
	f := newFake()
	f.msg("frigate/available", "offline")
	f.expect(t)
	f.fire()
	f.expect(t, "🔴 Frigate is not responding: no notifications until it is back")
	f.msg("frigate/available", "online")
	f.expect(t, "🟢 Frigate is back (down since 14:02)")
}

// An outage that began on another day gives its date too.
func TestFrigateBackTheNextDay(t *testing.T) {
	f := newFake()
	f.msg("frigate/available", "offline")
	f.fire()
	f.expect(t, "🔴 Frigate is not responding: no notifications until it is back")
	f.now = f.now.Add(20 * time.Hour)
	f.msg("frigate/available", "online")
	f.expect(t, "🟢 Frigate is back (down since Oct 18 14:02)")
}

func TestShortFrigateRestartIsSilent(t *testing.T) {
	f := newFake()
	f.msg("frigate/available", "stopped")
	f.msg("frigate/available", "online")
	f.fire()
	f.msg("frigate/available", "online") // republished on every broker reconnection
	f.expect(t)
}

func TestCameraDownThenBack(t *testing.T) {
	f := newFake()
	stats := func(fps string) {
		f.msg("frigate/stats", `{"cameras":{"jardin":{"camera_fps":`+fps+`},"garage":{"camera_fps":5}}}`)
	}
	stats("0")
	f.now = f.now.Add(time.Minute)
	stats("0")
	f.expect(t) // under Grace
	f.now = f.now.Add(time.Minute)
	stats("0")
	f.expect(t, "🔴 Camera jardin sends no images")
	stats("0")
	f.expect(t) // reported once
	stats("5")
	f.expect(t, "🟢 Camera jardin is back (down since 14:02)")
}

func TestDisabledCameraIsNotReported(t *testing.T) {
	f := newFake()
	f.msg("frigate/jardin/enabled/state", "OFF")
	for range 3 {
		f.msg("frigate/stats", `{"cameras":{"jardin":{"camera_fps":0}}}`)
		f.now = f.now.Add(Grace)
	}
	f.expect(t)
}

func TestOtherTopicsAreLeft(t *testing.T) {
	f := newFake()
	for _, topic := range []string{"frigate/reviews", "frigate/jardin/detect/state", "other/available"} {
		if f.w.Handle(topic, nil) {
			t.Errorf("%s handled", topic)
		}
	}
}
