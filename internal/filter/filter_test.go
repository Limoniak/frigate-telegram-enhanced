package filter

import (
	"reflect"
	"testing"
	"time"

	"frigate-telegram/internal/config"
)

type fakeState struct {
	paused bool
	muted  map[string]bool
	last   map[string]time.Time
}

func (f *fakeState) IsPaused(time.Time) bool            { return f.paused }
func (f *fakeState) IsMuted(c string, _ time.Time) bool { return f.muted[c] }
func (f *fakeState) LastNotified(k string) time.Time    { return f.last[k] }

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
	cfg, err := config.Parse([]byte(cfgYAML), func(string) (string, bool) { return "", false })
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
		reason string // "" = notification attendue
		silent bool
		label  string
	}{
		{name: "ok", in: person("garage", 0.9), now: at(15, 0), label: "person"},
		{name: "caméra désactivée", in: person("salon", 0.9), now: at(15, 0), reason: ReasonCameraDisabled},
		{name: "faux positif", in: Input{Camera: "garage", Labels: []string{"person"}, Score: 0.9, HasScore: true, FalsePositive: true}, now: at(15, 0), reason: ReasonFalsePositive},
		{name: "immobile", in: Input{Camera: "garage", Labels: []string{"person"}, Score: 0.9, HasScore: true, Stationary: true}, now: at(15, 0), reason: ReasonStationary},
		{name: "label", in: Input{Camera: "garage", Labels: []string{"dog"}, Score: 0.9, HasScore: true}, now: at(15, 0), reason: ReasonLabel},
		{name: "score global", in: person("garage", 0.6), now: at(15, 0), reason: ReasonScore},
		{name: "score par label", in: Input{Camera: "garage", Labels: []string{"car"}, Score: 0.75, HasScore: true}, now: at(15, 0), reason: ReasonScore},
		{name: "sans score (reviews)", in: Input{Camera: "garage", Labels: []string{"person"}}, now: at(15, 0), label: "person"},
		{name: "zone manquante", in: person("jardin", 0.9), now: at(15, 0), reason: ReasonZone},
		{name: "zone ok", in: Input{Camera: "jardin", Labels: []string{"person"}, Score: 0.9, HasScore: true, Zones: []string{"rue", "allee"}}, now: at(15, 0), label: "person"},
		{name: "pause", in: person("garage", 0.9), now: at(15, 0), st: fakeState{paused: true}, reason: ReasonPaused},
		{name: "caméra coupée", in: person("garage", 0.9), now: at(15, 0), st: fakeState{muted: map[string]bool{"garage": true}}, reason: ReasonMuted},
		{name: "autre caméra coupée", in: person("garage", 0.9), now: at(15, 0), st: fakeState{muted: map[string]bool{"jardin": true}}, label: "person"},
		{name: "off_hours", in: person("garage", 0.9), now: at(12, 30), reason: ReasonOffHours},
		{name: "quiet_hours", in: person("garage", 0.9), now: at(23, 0), silent: true, label: "person"},
		{name: "cooldown actif", in: person("garage", 0.9), now: at(15, 0), st: fakeState{last: map[string]time.Time{"garage/person": at(15, 0).Add(-30 * time.Second)}}, reason: ReasonCooldown},
		{name: "cooldown écoulé", in: person("garage", 0.9), now: at(15, 0), st: fakeState{last: map[string]time.Time{"garage/person": at(15, 0).Add(-2 * time.Minute)}}, label: "person"},
		{name: "severity refusée", in: Input{Camera: "garage", Labels: []string{"person"}, Severity: "detection"}, now: at(15, 0), reason: ReasonSeverity},
		{name: "severity acceptée", in: Input{Camera: "garage", Labels: []string{"person"}, Severity: "alert"}, now: at(15, 0), label: "person"},
		{name: "plusieurs labels", in: Input{Camera: "garage", Labels: []string{"dog", "person"}}, now: at(15, 0), label: "person"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := tc.st
			d := New(cfg, &st).Evaluate(tc.in, tc.now.UTC())
			if tc.reason != "" {
				if d.Notify || d.Reason != tc.reason {
					t.Fatalf("décision = %+v, attendu refus %q", d, tc.reason)
				}
				return
			}
			if !d.Notify {
				t.Fatalf("refus inattendu : %q", d.Reason)
			}
			if d.Silent != tc.silent || d.Label != tc.label || !reflect.DeepEqual(d.Chats, []string{"moi"}) {
				t.Errorf("décision = %+v", d)
			}
		})
	}
}

func TestCooldownKey(t *testing.T) {
	if CooldownKey("jardin", "person") != "jardin/person" {
		t.Error("clé inattendue")
	}
}
