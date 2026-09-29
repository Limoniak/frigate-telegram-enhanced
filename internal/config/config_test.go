package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func env(vars map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := vars[k]
		return v, ok
	}
}

const minimal = `
frigate:
  url: http://frigate:5000/
mqtt:
  broker: tcp://mqtt:1883
telegram:
  token: ${TG_TOKEN}
  admins: [42]
  chats:
    moi: 42
    famille: -100
`

var testEnv = env(map[string]string{"TG_TOKEN": "abc"})

func mustParse(t *testing.T, raw string) *Config {
	t.Helper()
	c, err := Parse([]byte(raw), testEnv)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return c
}

func TestParseMinimalAppliesDefaults(t *testing.T) {
	c := mustParse(t, minimal)
	if c.Telegram.Token != "abc" {
		t.Errorf("token = %q", c.Telegram.Token)
	}
	if c.Frigate.URL != "http://frigate:5000" {
		t.Errorf("url = %q, le / final doit être retiré", c.Frigate.URL)
	}
	if c.Mode != ModeEvents || c.Location.String() != "UTC" {
		t.Errorf("mode=%q location=%q", c.Mode, c.Location)
	}
	if c.MQTT.ClientID != "frigate-telegram" || c.MQTT.TopicPrefix != "frigate" {
		t.Errorf("mqtt = %+v", c.MQTT)
	}
	if c.StateFile != "/data/state.json" || c.HTTPListen != ":8080" || c.LogLevel != "info" {
		t.Errorf("défauts = %q %q %q", c.StateFile, c.HTTPListen, c.LogLevel)
	}
	n := c.Global()
	if !n.Enabled || !n.Snapshot || !n.Clip || n.GIF || !n.GenAIDescription || !n.IgnoreStationary {
		t.Errorf("booléens par défaut incorrects : %+v", n)
	}
	if n.Cooldown != time.Minute || n.ClipDelay != 5*time.Second {
		t.Errorf("durées = %v %v", n.Cooldown, n.ClipDelay)
	}
	if !reflect.DeepEqual(n.Chats, []string{"famille", "moi"}) {
		t.Errorf("chats = %v, attendu tous les chats triés", n.Chats)
	}
	if !reflect.DeepEqual(n.Severity, []string{"alert"}) {
		t.Errorf("severity = %v", n.Severity)
	}
	if !c.IsAdmin(42) || c.IsAdmin(7) {
		t.Error("IsAdmin incorrect")
	}
	if c.ChatID("famille") != -100 {
		t.Error("ChatID incorrect")
	}
}

func TestMissingEnvVarIsAnError(t *testing.T) {
	_, err := Parse([]byte(minimal), env(nil))
	if err == nil || !strings.Contains(err.Error(), "TG_TOKEN") {
		t.Fatalf("erreur attendue mentionnant TG_TOKEN, obtenu %v", err)
	}
}

func TestEnvDefaultValue(t *testing.T) {
	raw := strings.Replace(minimal, "${TG_TOKEN}", "${TG_TOKEN:-fallback}", 1)
	c, err := Parse([]byte(raw), env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Telegram.Token != "fallback" {
		t.Errorf("token = %q", c.Telegram.Token)
	}
}

func TestCameraOverridesGlobal(t *testing.T) {
	c := mustParse(t, minimal+`
notify:
  labels: [person, car]
  min_score: {person: 0.6, car: 0.8}
  cooldown: 2m
  quiet_hours: [{from: "22:00", to: "07:00"}]
cameras:
  jardin:
    zones: [allee]
    chats: [moi]
  salon:
    enabled: false
`)
	j := c.ForCamera("jardin")
	if !reflect.DeepEqual(j.Labels, []string{"person", "car"}) || !reflect.DeepEqual(j.Zones, []string{"allee"}) {
		t.Errorf("jardin labels=%v zones=%v", j.Labels, j.Zones)
	}
	if !reflect.DeepEqual(j.Chats, []string{"moi"}) || j.Cooldown != 2*time.Minute {
		t.Errorf("jardin chats=%v cooldown=%v", j.Chats, j.Cooldown)
	}
	if j.MinScore.For("car") != 0.8 || j.MinScore.For("dog") != 0 {
		t.Errorf("min_score car=%v dog=%v", j.MinScore.For("car"), j.MinScore.For("dog"))
	}
	if !reflect.DeepEqual(j.QuietHours, []TimeRange{{From: 22 * 60, To: 7 * 60}}) {
		t.Errorf("quiet_hours = %v", j.QuietHours)
	}
	if c.ForCamera("salon").Enabled {
		t.Error("salon doit être désactivée")
	}
	if other := c.ForCamera("inconnue"); !reflect.DeepEqual(other.Chats, []string{"famille", "moi"}) {
		t.Errorf("caméra non listée : chats = %v", other.Chats)
	}
}

func TestValidationErrors(t *testing.T) {
	cases := []struct{ name, extra, want string }{
		{"mode", "mode: foo\n", "mode"},
		{"chat inconnu", "notify:\n  chats: [nope]\n", `chat "nope" inconnu`},
		{"severity", "notify:\n  severity: [urgent]\n", "severity"},
		{"score", "notify:\n  min_score: 1.5\n", "min_score"},
		{"timezone", "timezone: Mars/Olympus\n", "timezone"},
		{"champ inconnu", "notfy:\n  labels: [x]\n", "notfy"},
		{"heure", "notify:\n  quiet_hours: [{from: \"25:00\", to: \"07:00\"}]\n", "25:00"},
		{"log level", "log_level: verbose\n", "log_level"},
		{"metrics sans mot de passe", "web:\n  protect_metrics: true\n", "protect_metrics"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(minimal+tc.extra), testEnv)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("erreur attendue contenant %q, obtenu %v", tc.want, err)
			}
		})
	}
}

func TestEnvVarInCommentIsIgnored(t *testing.T) {
	raw := minimal + "# adresse de secours ${NOT_SET}\n"
	if _, err := Parse([]byte(raw), testEnv); err != nil {
		t.Fatalf("Parse: %v, un ${VAR} en commentaire ne doit pas provoquer d'erreur", err)
	}
}

func TestEnvVarPreservesSpecialCharacters(t *testing.T) {
	raw := strings.Replace(minimal, "token: ${TG_TOKEN}", `token: "${TG_TOKEN}"`, 1)
	value := `a\nb"c#d`
	c, err := Parse([]byte(raw), env(map[string]string{"TG_TOKEN": value}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Telegram.Token != value {
		t.Errorf("token = %q, attendu %q (identique octet pour octet)", c.Telegram.Token, value)
	}
}

func TestTimeRangeContains(t *testing.T) {
	night := TimeRange{From: 22 * 60, To: 7 * 60}
	day := TimeRange{From: 9 * 60, To: 17 * 60}
	cases := []struct {
		r    TimeRange
		hhmm int
		want bool
	}{
		{night, 23 * 60, true}, {night, 6*60 + 59, true}, {night, 7 * 60, false}, {night, 12 * 60, false},
		{day, 9 * 60, true}, {day, 16*60 + 59, true}, {day, 17 * 60, false}, {day, 8 * 60, false},
	}
	for _, tc := range cases {
		if got := tc.r.Contains(tc.hhmm); got != tc.want {
			t.Errorf("%+v.Contains(%d) = %v, attendu %v", tc.r, tc.hhmm, got, tc.want)
		}
	}
}

func TestInRanges(t *testing.T) {
	rs := []TimeRange{{From: 22 * 60, To: 7 * 60}}
	if !InRanges(rs, time.Date(2026, 1, 1, 23, 30, 0, 0, time.UTC)) {
		t.Error("23:30 doit être dans la plage")
	}
	if InRanges(rs, time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)) {
		t.Error("12:00 ne doit pas être dans la plage")
	}
}
