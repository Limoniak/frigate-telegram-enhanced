package config

import (
	"reflect"
	"strings"
	"testing"

	"frigate-telegram/internal/i18n"
)

func TestFromEnvMinimal(t *testing.T) {
	c, err := FromEnv(env(map[string]string{
		"TELEGRAM_TOKEN":   "123:abc",
		"TELEGRAM_CHAT_ID": "123456789",
		"FRIGATE_URL":      "http://192.168.1.10:5000/",
		"MQTT_BROKER":      "192.168.1.10",
		"TZ":               "Europe/Paris",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Source != "environment" || c.Telegram.Token != "123:abc" || c.Frigate.URL != "http://192.168.1.10:5000" {
		t.Errorf("config = %+v", c)
	}
	if c.MQTT.Broker != "tcp://192.168.1.10:1883" || c.MQTT.TopicPrefix != "frigate" {
		t.Errorf("mqtt = %+v", c.MQTT)
	}
	if !reflect.DeepEqual(c.Telegram.Chats, map[string]int64{"123456789": 123456789}) || !reflect.DeepEqual(c.Telegram.Admins, []int64{123456789}) {
		t.Errorf("telegram = %+v", c.Telegram)
	}
	if c.Location.String() != "Europe/Paris" || !c.Web.Enabled || c.StateFile != "/data/state.json" {
		t.Errorf("défauts = %v %+v %q", c.Location, c.Web, c.StateFile)
	}
	if n := c.Global(); !reflect.DeepEqual(n.Chats, []string{"123456789"}) || !n.Enabled {
		t.Errorf("notify = %+v", n)
	}
}

func TestFromEnvFull(t *testing.T) {
	c, err := FromEnv(env(map[string]string{
		"TELEGRAM_TOKEN":      "123:abc",
		"TELEGRAM_CHAT_ID":    "moi=111, famille=-1001234567890",
		"TELEGRAM_ADMINS":     "111,222",
		"FRIGATE_URL":         "https://frigate:8971",
		"FRIGATE_USERNAME":    "admin",
		"FRIGATE_PASSWORD":    "pw",
		"MQTT_BROKER":         "ssl://mqtt.lan:8883",
		"MODE":                "reviews",
		"WEB_PASSWORD":        "secret",
		"WEB_ALLOWED_HOSTS":   "nas.lan, frigate.lan",
		"WEB_PROTECT_METRICS": "true",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Telegram.Chats, map[string]int64{"moi": 111, "famille": -1001234567890}) || !reflect.DeepEqual(c.Telegram.Admins, []int64{111, 222}) {
		t.Errorf("telegram = %+v", c.Telegram)
	}
	if c.MQTT.Broker != "ssl://mqtt.lan:8883" || c.Mode != ModeReviews || c.Frigate.Username != "admin" {
		t.Errorf("config = %+v", c)
	}
	if !reflect.DeepEqual(c.Web.AllowedHosts, []string{"nas.lan", "frigate.lan"}) || !c.Web.ProtectMetrics || c.Web.Password != "secret" {
		t.Errorf("web = %+v", c.Web)
	}
}

func TestFromEnvErrors(t *testing.T) {
	base := map[string]string{
		"TELEGRAM_TOKEN": "123:abc", "TELEGRAM_CHAT_ID": "111",
		"FRIGATE_URL": "http://f:5000", "MQTT_BROKER": "mqtt",
	}
	with := func(k, v string) map[string]string {
		m := map[string]string{}
		for a, b := range base {
			m[a] = b
		}
		m[k] = v
		return m
	}
	for _, tc := range []struct {
		name string
		vars map[string]string
		want string
	}{
		{"rien", map[string]string{}, "TELEGRAM_TOKEN, TELEGRAM_CHAT_ID, FRIGATE_URL, MQTT_BROKER"},
		{"chat invalide", with("TELEGRAM_CHAT_ID", "moi"), "TELEGRAM_CHAT_ID"},
		{"groupe sans admin", with("TELEGRAM_CHAT_ID", "-1001234567890"), "TELEGRAM_ADMINS is required"},
		{"admin négatif", with("TELEGRAM_ADMINS", "-5"), "TELEGRAM_ADMINS"},
		{"booléen", with("WEB_ENABLED", "peut-être"), "WEB_ENABLED"},
		{"mode", with("MODE", "tout"), "mode"},
		{"fuseau", with("TZ", "Mars/Olympus"), "timezone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := FromEnv(env(tc.vars))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("erreur attendue contenant %q, obtenu %v", tc.want, err)
			}
		})
	}
}

func TestLoadFallsBackToEnvironment(t *testing.T) {
	t.Setenv("TELEGRAM_TOKEN", "123:abc")
	t.Setenv("TELEGRAM_CHAT_ID", "111")
	t.Setenv("FRIGATE_URL", "http://f:5000")
	t.Setenv("MQTT_BROKER", "mqtt")
	c, err := Load(t.TempDir() + "/absent.yml")
	if err != nil {
		t.Fatal(err)
	}
	if c.Source != "environment" {
		t.Errorf("source = %q", c.Source)
	}
}

func TestLanguage(t *testing.T) {
	base := map[string]string{
		"TELEGRAM_TOKEN": "123:abc", "TELEGRAM_CHAT_ID": "111",
		"FRIGATE_URL": "http://f:5000", "MQTT_BROKER": "mqtt",
	}
	c, err := FromEnv(env(base))
	if err != nil || c.Language != i18n.EN {
		t.Fatalf("langue par défaut = %q, %v ; attendu en", c.Language, err)
	}
	base["LANGUAGE"] = "fr"
	if c, err = FromEnv(env(base)); err != nil || c.Language != i18n.FR {
		t.Fatalf("LANGUAGE=fr : %q, %v", c.Language, err)
	}
	base["TELEGRAM_CHAT_ID"] = "-1001234567890"
	if _, err := FromEnv(env(base)); err == nil || !strings.Contains(err.Error(), "TELEGRAM_ADMINS est obligatoire") {
		t.Errorf("erreur en français attendue, obtenu %v", err)
	}
	base["LANGUAGE"] = "de"
	base["TELEGRAM_CHAT_ID"] = "111"
	if _, err := FromEnv(env(base)); err == nil || !strings.Contains(err.Error(), "unsupported language") {
		t.Errorf("langue inconnue : %v", err)
	}
	if _, err := FromEnv(env(map[string]string{"LANGUAGE": "fr"})); err == nil || !strings.Contains(err.Error(), "obligatoires manquantes") {
		t.Errorf("variables manquantes en français : %v", err)
	}
}

func TestFileLanguageKey(t *testing.T) {
	c := mustParse(t, minimal+"language: fr\n")
	if c.Language != i18n.FR {
		t.Errorf("language: fr → %q", c.Language)
	}
	_, err := Parse([]byte(minimal+"language: fr\nnotify:\n  chats: [nope]\n"), testEnv)
	if err == nil || !strings.Contains(err.Error(), `chat "nope" inconnu`) {
		t.Errorf("erreur en français attendue : %v", err)
	}
}
