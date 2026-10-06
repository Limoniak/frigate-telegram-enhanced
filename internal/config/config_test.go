package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

const minimal = `
frigate:
  url: http://frigate:5000/
mqtt:
  broker: tcp://mqtt:1883
telegram:
  token: abc
  admins: [42]
  chats:
    moi: 42
    famille: -100
`

func mustParse(t *testing.T, raw string) *Config {
	t.Helper()
	c, err := Parse([]byte(raw))
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
		t.Errorf("url = %q, the trailing / must be removed", c.Frigate.URL)
	}
	if c.Mode != ModeEvents || c.Location.String() != "UTC" {
		t.Errorf("mode=%q location=%q", c.Mode, c.Location)
	}
	if c.MQTT.ClientID != "frigate-telegram-enhanced" || c.MQTT.TopicPrefix != "frigate" {
		t.Errorf("mqtt = %+v", c.MQTT)
	}
	if c.StateFile != "/data/state.json" || c.HTTPListen != ":8431" || c.LogLevel != "info" {
		t.Errorf("defaults = %q %q %q", c.StateFile, c.HTTPListen, c.LogLevel)
	}
	n := c.Global()
	if !n.Enabled || !n.Snapshot || !n.Clip || n.GIF || !n.GenAIDescription || !n.IgnoreStationary {
		t.Errorf("wrong default booleans: %+v", n)
	}
	if n.Cooldown != time.Minute || n.ClipDelay != 5*time.Second {
		t.Errorf("durations = %v %v", n.Cooldown, n.ClipDelay)
	}
	if !reflect.DeepEqual(n.Chats, []string{"famille", "moi"}) {
		t.Errorf("chats = %v, want every chat, sorted", n.Chats)
	}
	if !reflect.DeepEqual(n.Severity, []string{"alert"}) {
		t.Errorf("severity = %v", n.Severity)
	}
	if !c.IsAdmin(42) || c.IsAdmin(7) {
		t.Error("IsAdmin wrong")
	}
	if c.ChatID("famille") != -100 {
		t.Error("ChatID wrong")
	}
}

func TestEnvVarIsNotExpanded(t *testing.T) {
	raw := strings.Replace(minimal, "token: abc", `token: "${TG_TOKEN}"`, 1)
	t.Setenv("TG_TOKEN", "from-env")
	c, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if c.Telegram.Token != "${TG_TOKEN}" {
		t.Errorf("token = %q, the environment must not be read", c.Telegram.Token)
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
		t.Error("salon must be disabled")
	}
	if other := c.ForCamera("inconnue"); !reflect.DeepEqual(other.Chats, []string{"famille", "moi"}) {
		t.Errorf("camera not listed: chats = %v", other.Chats)
	}
}

func TestValidationErrors(t *testing.T) {
	cases := []struct{ name, extra, want string }{
		{"mode", "mode: foo\n", "mode"},
		{"chat inconnu", "notify:\n  chats: [nope]\n", `unknown chat "nope"`},
		{"severity", "notify:\n  severity: [urgent]\n", "severity"},
		{"score", "notify:\n  min_score: 1.5\n", "min_score"},
		{"timezone", "timezone: Mars/Olympus\n", "timezone"},
		{"unknown field", "notfy:\n  labels: [x]\n", "notfy"},
		{"hour", "notify:\n  quiet_hours: [{from: \"25:00\", to: \"07:00\"}]\n", "25:00"},
		{"log level", "log_level: verbose\n", "log_level"},
		{"metrics without password", "web:\n  protect_metrics: true\n", "protect_metrics"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(minimal + tc.extra))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want an error containing %q, got %v", tc.want, err)
			}
		})
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
			t.Errorf("%+v.Contains(%d) = %v, want %v", tc.r, tc.hhmm, got, tc.want)
		}
	}
}

func TestInRanges(t *testing.T) {
	rs := []TimeRange{{From: 22 * 60, To: 7 * 60}}
	if !InRanges(rs, time.Date(2026, 1, 1, 23, 30, 0, 0, time.UTC)) {
		t.Error("23:30 must be in the range")
	}
	if InRanges(rs, time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)) {
		t.Error("12:00 must not be in the range")
	}
}

func TestAdminsAreOptional(t *testing.T) {
	c := mustParse(t, strings.Replace(minimal, "  admins: [42]\n", "", 1))
	if len(c.Telegram.Admins) != 0 {
		t.Fatalf("admins = %v", c.Telegram.Admins)
	}
	// Without admins, whoever writes in a recipient chat controls the bot.
	for _, tc := range []struct {
		user, chat int64
		want       bool
	}{
		{7, -100, true},  // a member of the famille group
		{42, 42, true},   // the private chat moi
		{7, -999, false}, // a group that is not a recipient
		{7, 7, false},    // a private chat that is not a recipient
	} {
		if got := c.CanControl(tc.user, tc.chat); got != tc.want {
			t.Errorf("no admins: CanControl(%d, %d) = %v, want %v", tc.user, tc.chat, got, tc.want)
		}
	}

	// With admins, only they do, wherever they write.
	c = mustParse(t, minimal)
	if !c.CanControl(42, -999) || c.CanControl(7, -100) {
		t.Errorf("admins [42]: CanControl(42, -999) = %v, CanControl(7, -100) = %v", c.CanControl(42, -999), c.CanControl(7, -100))
	}
}
