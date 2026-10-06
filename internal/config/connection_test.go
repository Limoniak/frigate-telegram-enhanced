package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func validConnection() Connection {
	return Connection{
		Frigate:  Frigate{URL: "http://frigate:5000/"},
		MQTT:     MQTT{Broker: "mosquitto"},
		Telegram: Telegram{Token: "123:abc", Chats: map[string]int64{"Alice": 111, "Family": -1001}},
	}
}

func TestConnectionNormalize(t *testing.T) {
	c := validConnection()
	c.Normalize()
	if c.Frigate.URL != "http://frigate:5000" {
		t.Errorf("frigate url = %q", c.Frigate.URL)
	}
	if c.MQTT.Broker != "tcp://mosquitto:1883" {
		t.Errorf("broker = %q", c.MQTT.Broker)
	}
	// The admins are the people of the private chats, not the groups.
	if !slices.Equal(c.Telegram.Admins, []int64{111}) {
		t.Errorf("admins = %v", c.Telegram.Admins)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestConnectionValidateReportsWhatIsMissing(t *testing.T) {
	c := validConnection()
	c.Normalize()
	c.Telegram.Token = ""
	if err := c.Validate(); err == nil {
		t.Error("a connection without a token is accepted")
	}
}

func TestLoadReadsTheSavedConnection(t *testing.T) {
	dir := t.TempDir()
	conn := filepath.Join(dir, "connection.yml")
	if _, err := Load(filepath.Join(dir, "absent.yml"), conn); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("nothing configured: err = %v, want ErrNotConfigured", err)
	}

	c := validConnection()
	c.Normalize()
	c.Language, c.Timezone = "fr", "Europe/Paris"
	c.MQTT.Password = "pa${ss}" // taken as it is: the page knows nothing of ${VAR}
	if err := SaveConnection(conn, c); err != nil {
		t.Fatal(err)
	}
	// Windows has no Unix permissions: the file is read-write for all there.
	if fi, err := os.Stat(conn); runtime.GOOS != "windows" && (err != nil || fi.Mode().Perm() != 0o600) {
		t.Errorf("connection file mode = %v, %v; want 0600", fi.Mode().Perm(), err)
	}
	cfg, err := Load(filepath.Join(dir, "absent.yml"), conn)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Source != conn || cfg.MQTT.Password != "pa${ss}" || cfg.Language != "fr" || cfg.Timezone != "Europe/Paris" {
		t.Errorf("loaded: source %q, mqtt password %q, language %q, timezone %q", cfg.Source, cfg.MQTT.Password, cfg.Language, cfg.Timezone)
	}
	if cfg.ChatID("Family") != -1001 || !cfg.IsAdmin(111) || cfg.IsAdmin(-1001) {
		t.Errorf("chats %v, admins %v", cfg.Telegram.Chats, cfg.Telegram.Admins)
	}
	back, err := LoadConnection(conn)
	if err != nil {
		t.Fatal(err)
	}
	if back.MQTT.Password != c.MQTT.Password || back.Telegram.Token != c.Telegram.Token {
		t.Errorf("LoadConnection = %+v", back)
	}
}
