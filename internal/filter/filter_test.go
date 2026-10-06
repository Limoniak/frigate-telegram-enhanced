package filter

import (
	"reflect"
	"testing"
	"time"

	"frigate-telegram-enhanced/internal/config"
)

type fakeState struct {
	paused bool
	home   bool
	muted  map[string]bool
	last   map[string]time.Time
}

func (f *fakeState) IsPaused(time.Time) bool            { return f.paused }
func (f *fakeState) IsMuted(c string, _ time.Time) bool { return f.muted[c] }
func (f *fakeState) LastNotified(k string) time.Time    { return f.last[k] }
func (f *fakeState) SomeoneHome() bool                  { return f.home }

const cfgYAML = `
timezone: Europe/Paris
frigate: {url: "http://f"}
mqtt: {broker: "tcp://m:1883"}
telegram: {token: t, admins: [1], chats: {moi: 1}}
notify:
  labels: [person, car]
  min_score: {person: 0.7, car: 0.8}
  cooldown: 1m
  quiet_hours: [{from: "22:00", to: "07:00"}]
  off_hours: [{from: "12:00", to: "13:00"}]
cameras:
  jardin:
    zones: [allee]
  salon:
    enabled: false
`

func TestEvaluate(t *testing.T) {
	cfg, err := config.Parse([]byte(cfgYAML))
	if err != nil {
		t.Fatal(err)
	}
	paris := cfg.Location
	at := func(h, m int) time.Time { return time.Date(2026, 9, 28, h, m, 0, 0, paris) }
	person := func(cam string, score float64) Input {
		return Input{Camera: cam, Labels: []string{"person"}, Score: score, HasScore: true}
	}

	cases := []struct {
		name   string
		in     Input
		now    time.Time
		st     fakeState
		reason string // "" = want a notification
		silent bool
		label  string
	}{
		{name: "ok", in: person("garage", 0.9), now: at(15, 0), label: "person"},
		{name: "camera disabled", in: person("salon", 0.9), now: at(15, 0), reason: ReasonCameraDisabled},
		{name: "faux positif", in: Input{Camera: "garage", Labels: []string{"person"}, Score: 0.9, HasScore: true, FalsePositive: true}, now: at(15, 0), reason: ReasonFalsePositive},
		{name: "immobile", in: Input{Camera: "garage", Labels: []string{"person"}, Score: 0.9, HasScore: true, Stationary: true}, now: at(15, 0), reason: ReasonStationary},
		{name: "label", in: Input{Camera: "garage", Labels: []string{"dog"}, Score: 0.9, HasScore: true}, now: at(15, 0), reason: ReasonLabel},
		{name: "score global", in: person("garage", 0.6), now: at(15, 0), reason: ReasonScore},
		{name: "score per label", in: Input{Camera: "garage", Labels: []string{"car"}, Score: 0.75, HasScore: true}, now: at(15, 0), reason: ReasonScore},
		{name: "no score (reviews)", in: Input{Camera: "garage", Labels: []string{"person"}}, now: at(15, 0), label: "person"},
		{name: "zone missing", in: person("jardin", 0.9), now: at(15, 0), reason: ReasonZone},
		{name: "zone ok", in: Input{Camera: "jardin", Labels: []string{"person"}, Score: 0.9, HasScore: true, Zones: []string{"rue", "allee"}}, now: at(15, 0), label: "person"},
		{name: "pause", in: person("garage", 0.9), now: at(15, 0), st: fakeState{paused: true}, reason: ReasonPaused},
		{name: "camera muted", in: person("garage", 0.9), now: at(15, 0), st: fakeState{muted: map[string]bool{"garage": true}}, reason: ReasonMuted},
		{name: "another camera muted", in: person("garage", 0.9), now: at(15, 0), st: fakeState{muted: map[string]bool{"jardin": true}}, label: "person"},
		{name: "off_hours", in: person("garage", 0.9), now: at(12, 30), reason: ReasonOffHours},
		{name: "quiet_hours", in: person("garage", 0.9), now: at(23, 0), silent: true, label: "person"},
		{name: "cooldown actif", in: person("garage", 0.9), now: at(15, 0), st: fakeState{last: map[string]time.Time{"garage/person": at(15, 0).Add(-30 * time.Second)}}, reason: ReasonCooldown},
		{name: "cooldown over", in: person("garage", 0.9), now: at(15, 0), st: fakeState{last: map[string]time.Time{"garage/person": at(15, 0).Add(-2 * time.Minute)}}, label: "person"},
		{name: "severity refused", in: Input{Camera: "garage", Labels: []string{"person"}, Severity: "detection"}, now: at(15, 0), reason: ReasonSeverity},
		{name: "severity accepted", in: Input{Camera: "garage", Labels: []string{"person"}, Severity: "alert"}, now: at(15, 0), label: "person"},
		{name: "plusieurs labels", in: Input{Camera: "garage", Labels: []string{"dog", "person"}}, now: at(15, 0), label: "person"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := tc.st
			d := New(cfg, &st).Evaluate(tc.in, tc.now.UTC())
			if tc.reason != "" {
				if d.Notify || d.Reason != tc.reason {
					t.Fatalf("decision = %+v, want a refusal %q", d, tc.reason)
				}
				return
			}
			if !d.Notify {
				t.Fatalf("unexpected refusal: %q", d.Reason)
			}
			if d.Silent != tc.silent || d.Label != tc.label || !reflect.DeepEqual(d.Chats, []string{"moi"}) {
				t.Errorf("decision = %+v", d)
			}
		})
	}
}

func TestCooldownKey(t *testing.T) {
	if CooldownKey("jardin", "person") != "jardin/person" {
		t.Error("unexpected key")
	}
}

func TestSomeoneHome(t *testing.T) {
	cfg, err := config.Parse([]byte(`
timezone: Europe/Paris
frigate: {url: "http://f"}
mqtt: {broker: "tcp://m:1883"}
telegram: {token: t, admins: [1], chats: {moi: 1}}
presence: {topics: ["homeassistant/person/+/state"]}
cameras:
  jardin: {when_home: notify}
  garage: {when_home: silent}
`))
	if err != nil {
		t.Fatal(err)
	}
	noon := time.Date(2026, 9, 28, 15, 0, 0, 0, cfg.Location)
	in := func(cam string) Input {
		return Input{Camera: cam, Labels: []string{"person"}, Score: 0.9, HasScore: true}
	}
	home := &fakeState{home: true}
	e := New(cfg, home)
	if d := e.Evaluate(in("entree"), noon); d.Notify || d.Reason != ReasonHome {
		t.Errorf("entree (default skip) = %+v, want a home refusal", d)
	}
	if d := e.Evaluate(in("jardin"), noon); !d.Notify || d.Silent {
		t.Errorf("jardin (notify) = %+v", d)
	}
	if d := e.Evaluate(in("garage"), noon); !d.Notify || !d.Silent {
		t.Errorf("garage (silent) = %+v", d)
	}
	if d := New(cfg, &fakeState{}).Evaluate(in("entree"), noon); !d.Notify {
		t.Errorf("nobody home: entree = %+v", d)
	}
	// Without presence topics configured, when_home has no effect.
	cfg2, _ := config.Parse([]byte(cfgYAML))
	if d := New(cfg2, home).Evaluate(in("entree"), noon); !d.Notify {
		t.Errorf("presence not configured: %+v", d)
	}
}

func TestRecipients(t *testing.T) {
	cfg, err := config.Parse([]byte(`
timezone: Europe/Paris
frigate: {url: "http://f"}
mqtt: {broker: "tcp://m:1883"}
telegram: {token: t, admins: [1], chats: {moi: 1, famille: -100}}
recipients:
  famille:
    labels: [person]
    off_hours: [{from: "07:00", to: "22:00"}]
    quiet_hours: [{from: "23:00", to: "06:00"}]
`))
	if err != nil {
		t.Fatal(err)
	}
	e := New(cfg, &fakeState{})
	at := func(h int) time.Time { return time.Date(2026, 9, 28, h, 0, 0, 0, cfg.Location) }
	in := func(label string) Input {
		return Input{Camera: "entree", Labels: []string{label}, Score: 0.9, HasScore: true}
	}

	if d := e.Evaluate(in("person"), at(15)); !reflect.DeepEqual(d.Chats, []string{"moi"}) {
		t.Errorf("3 pm, person: chats = %v (the family only gets the night)", d.Chats)
	}
	if d := e.Evaluate(in("person"), at(22)); !reflect.DeepEqual(d.Chats, []string{"famille", "moi"}) || d.SilentFor("famille") {
		t.Errorf("10 pm, person: %+v", d)
	}
	if d := e.Evaluate(in("car"), at(22)); !reflect.DeepEqual(d.Chats, []string{"moi"}) {
		t.Errorf("10 pm, car: chats = %v (the family only gets people)", d.Chats)
	}
	if d := e.Evaluate(in("person"), at(23)); !d.SilentFor("famille") || d.SilentFor("moi") {
		t.Errorf("11 pm: silent for the family only, got %+v", d)
	}
	// Nobody receives it: an explicit refusal.
	cfg.Recipient("moi") // concurrency-safe access
	o := cfg.CurrentOverlay()
	o.Recipients["moi"] = config.Recipient{Labels: []string{"dog"}}
	if err := cfg.ApplyOverlay(&o); err != nil {
		t.Fatal(err)
	}
	if d := e.Evaluate(in("car"), at(15)); d.Notify || d.Reason != ReasonRecipients {
		t.Errorf("no recipient: %+v", d)
	}
}

func TestSubLabelFilter(t *testing.T) {
	cfg, err := config.Parse([]byte(`
timezone: Europe/Paris
frigate: {url: "http://f"}
mqtt: {broker: "tcp://m:1883"}
telegram: {token: t, admins: [1], chats: {moi: 1}}
notify: {ignore_sub_labels: ["Clio 3 Océane", ohana]}
cameras:
  salon: {ignore_known: true}
`))
	if err != nil {
		t.Fatal(err)
	}
	e := New(cfg, &fakeState{})
	noon := time.Date(2026, 9, 28, 15, 0, 0, 0, cfg.Location)
	in := func(cam, label string, subs ...string) Input {
		return Input{Camera: cam, Labels: []string{label}, Score: 0.9, HasScore: true, SubLabels: subs}
	}
	for _, tc := range []struct {
		name   string
		in     Input
		reason string
	}{
		{"car ignored (different case)", in("jardin", "car", "clio 3 océane"), ReasonSubLabel},
		{"cat ignored", in("jardin", "cat", "ohana"), ReasonSubLabel},
		{"another known car", in("jardin", "car", "audi a3 baptiste"), ""},
		{"car without a label", in("jardin", "car"), ""},
		{"salon: only unknown objects", in("salon", "cat", "ulysse"), ReasonSubLabel},
		{"salon: unknown", in("salon", "cat"), ""},
		{"review: one ignored and one unknown", in("jardin", "car", "ohana", ""), ""},
		{"review: two ignored", in("jardin", "car", "ohana", "clio 3 océane"), ReasonSubLabel},
	} {
		if d := e.Evaluate(tc.in, noon); d.Reason != tc.reason || d.Notify != (tc.reason == "") {
			t.Errorf("%s: %+v, want reason %q", tc.name, d, tc.reason)
		}
	}
	if !cfg.ForCamera("jardin").FiltersSubLabels() || cfg.Global().SubLabelWait != 5*time.Second {
		t.Errorf("default wait = %v", cfg.Global().SubLabelWait)
	}
}
