// Package outage warns on Telegram when Frigate or one of its cameras stops, and
// when it is back.
package outage

import (
	"encoding/json"
	"html"
	"strings"
	"sync"
	"time"

	"frigate-telegram-enhanced/internal/i18n"
)

// Grace is how long an outage must last before it is reported: a Frigate restart
// or a camera reconnecting goes unnoticed.
const Grace = 2 * time.Minute

type Watcher struct {
	prefix string
	lang   i18n.Lang
	loc    *time.Location
	send   func(text string) // must not block: called under the lock
	now    func() time.Time
	after  func(d time.Duration, f func()) // default: time.AfterFunc

	mu       sync.Mutex
	frigate  down
	cameras  map[string]*down // cameras sending no images
	disabled map[string]bool  // cameras turned off in Frigate: no images expected
}

type down struct {
	since    time.Time // zero: up
	reported bool
}

func New(prefix string, lang i18n.Lang, loc *time.Location, send func(text string)) *Watcher {
	return &Watcher{
		prefix: prefix, lang: lang, loc: loc, send: send, now: time.Now,
		after:   func(d time.Duration, f func()) { time.AfterFunc(d, f) },
		cameras: map[string]*down{}, disabled: map[string]bool{},
	}
}

// Topics returns the MQTT topics the watcher reads.
func (w *Watcher) Topics() []string {
	return []string{w.prefix + "/available", w.prefix + "/stats", w.prefix + "/+/enabled/state"}
}

// Handle handles a message of one of Topics and reports whether it was one.
func (w *Watcher) Handle(topic string, payload []byte) bool {
	rest, ok := strings.CutPrefix(topic, w.prefix+"/")
	if !ok {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	switch {
	case rest == "available":
		w.available(string(payload))
	case rest == "stats":
		w.stats(payload)
	case strings.HasSuffix(rest, "/enabled/state") && strings.Count(rest, "/") == 2:
		camera := strings.TrimSuffix(rest, "/enabled/state")
		w.disabled[camera] = string(payload) == "OFF"
		if w.disabled[camera] {
			delete(w.cameras, camera)
		}
	default:
		return false
	}
	return true
}

// available handles "online", "offline" (Frigate disconnected, MQTT will) or "stopped".
func (w *Watcher) available(v string) {
	if v == "online" {
		if w.frigate.reported {
			w.send(w.lang.Tf("🟢 Frigate is back (down since %s)", w.clock(w.frigate.since)))
		}
		w.frigate = down{}
		return
	}
	if !w.frigate.since.IsZero() {
		return
	}
	w.frigate.since = w.now()
	since := w.frigate.since
	w.after(Grace, func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.frigate.since.Equal(since) && !w.frigate.reported {
			w.frigate.reported = true
			w.send(w.lang.T("🔴 Frigate is not responding: no notifications until it is back"))
		}
	})
	// A camera's outage started while Frigate was up; Frigate down says it all.
	w.cameras = map[string]*down{}
}

// stats reads the frame rate of each camera from Frigate's periodic stats (every
// minute by default): a camera at 0 fps receives no images.
func (w *Watcher) stats(payload []byte) {
	if !w.frigate.since.IsZero() {
		return
	}
	var s struct {
		Cameras map[string]struct {
			FPS float64 `json:"camera_fps"`
		} `json:"cameras"`
	}
	if json.Unmarshal(payload, &s) != nil {
		return
	}
	now := w.now()
	for name, c := range s.Cameras {
		d := w.cameras[name]
		switch {
		case w.disabled[name]:
		case c.FPS > 0:
			if d != nil && d.reported {
				w.send(w.lang.Tf("🟢 Camera %s is back (down since %s)", html.EscapeString(name), w.clock(d.since)))
			}
			delete(w.cameras, name)
		case d == nil:
			w.cameras[name] = &down{since: now}
		case !d.reported && now.Sub(d.since) >= Grace:
			d.reported = true
			w.send(w.lang.Tf("🔴 Camera %s sends no images", html.EscapeString(name)))
		}
	}
}

// clock is when an outage began: the time alone today, with the date for an outage
// that began on another day.
func (w *Watcher) clock(t time.Time) string {
	at, now := t.In(w.loc), w.now().In(w.loc)
	if at.Year() == now.Year() && at.YearDay() == now.YearDay() {
		return at.Format("15:04")
	}
	return at.Format(w.lang.DateTimeShort())
}
