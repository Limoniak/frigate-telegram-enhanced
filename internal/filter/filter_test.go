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
`), func(string) (string, bool) { return "", false })
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
		t.Errorf("entree (défaut skip) = %+v, attendu refus home", d)
	}
	if d := e.Evaluate(in("jardin"), noon); !d.Notify || d.Silent {
		t.Errorf("jardin (notify) = %+v", d)
	}
	if d := e.Evaluate(in("garage"), noon); !d.Notify || !d.Silent {
		t.Errorf("garage (silent) = %+v", d)
	}
	if d := New(cfg, &fakeState{}).Evaluate(in("entree"), noon); !d.Notify {
		t.Errorf("personne à la maison : entree = %+v", d)
	}
	// Sans topics de présence configurés, when_home n'a aucun effet.
	cfg2, _ := config.Parse([]byte(cfgYAML), func(string) (string, bool) { return "", false })
	if d := New(cfg2, home).Evaluate(in("entree"), noon); !d.Notify {
		t.Errorf("présence non configurée : %+v", d)
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
`), func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	e := New(cfg, &fakeState{})
	at := func(h int) time.Time { return time.Date(2026, 9, 28, h, 0, 0, 0, cfg.Location) }
	in := func(label string) Input {
		return Input{Camera: "entree", Labels: []string{label}, Score: 0.9, HasScore: true}
	}

	if d := e.Evaluate(in("person"), at(15)); !reflect.DeepEqual(d.Chats, []string{"moi"}) {
		t.Errorf("15 h, personne : chats = %v (la famille ne reçoit que la nuit)", d.Chats)
	}
	if d := e.Evaluate(in("person"), at(22)); !reflect.DeepEqual(d.Chats, []string{"famille", "moi"}) || d.SilentFor("famille") {
		t.Errorf("22 h, personne : %+v", d)
	}
	if d := e.Evaluate(in("car"), at(22)); !reflect.DeepEqual(d.Chats, []string{"moi"}) {
		t.Errorf("22 h, voiture : chats = %v (la famille ne reçoit que les personnes)", d.Chats)
	}
	if d := e.Evaluate(in("person"), at(23)); !d.SilentFor("famille") || d.SilentFor("moi") {
		t.Errorf("23 h : sans son pour la famille seulement, obtenu %+v", d)
	}
	// Personne ne reçoit : refus explicite.
	cfg.Recipient("moi") // accès concurrent-sûr
	o := cfg.CurrentOverlay()
	o.Recipients["moi"] = config.Recipient{Labels: []string{"dog"}}
	if err := cfg.ApplyOverlay(&o); err != nil {
		t.Fatal(err)
	}
	if d := e.Evaluate(in("car"), at(15)); d.Notify || d.Reason != ReasonRecipients {
		t.Errorf("aucun destinataire : %+v", d)
	}
}
