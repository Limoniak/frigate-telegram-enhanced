package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrNotConfigured reports an installation with nothing to start from: no
// config.yml, no environment variables and no connection saved by the setup page.
// The service then only serves the setup page.
var ErrNotConfigured = errors.New("not configured")

// DefaultConnectionPath is where the setup page saves the connection, next to the
// state, in the container's volume.
const DefaultConnectionPath = "/data/connection.yml"

// Connection is what the setup page asks for: how to reach Telegram, Frigate and
// MQTT, and who receives the notifications. Saved by the page, it takes the place
// of the environment variables; the notification settings stay in notify.yml.
type Connection struct {
	Language string        `yaml:"language,omitempty" json:"language"`
	Timezone string        `yaml:"timezone,omitempty" json:"timezone"`
	Mode     string        `yaml:"mode,omitempty" json:"mode"`
	Frigate  Frigate       `yaml:"frigate" json:"frigate"`
	MQTT     MQTT          `yaml:"mqtt" json:"mqtt"`
	Telegram Telegram      `yaml:"telegram" json:"telegram"`
	Web      ConnectionWeb `yaml:"web,omitempty" json:"web"`
}

// ConnectionWeb is the part of the web settings the setup page offers.
type ConnectionWeb struct {
	Password     string   `yaml:"password,omitempty" json:"password"`
	AllowedHosts []string `yaml:"allowed_hosts,omitempty" json:"allowed_hosts"`
}

const connectionHeader = `# Connection saved by the setup page of frigate-telegram-enhanced.
# Change it from the web interface ("Connection" link); it is rewritten on every save.
`

// Normalize completes what the page leaves implicit: a bare broker address
// ("mosquitto", "mosquitto:1883"), trailing slashes, and the admins — by default
// the people of the private chats, as with TELEGRAM_ADMINS.
func (c *Connection) Normalize() {
	c.Frigate.URL = strings.TrimRight(strings.TrimSpace(c.Frigate.URL), "/")
	if b := strings.TrimSpace(c.MQTT.Broker); b != "" {
		c.MQTT.Broker = brokerURL(b)
	}
	if len(c.Telegram.Admins) == 0 {
		for _, id := range c.Telegram.Chats {
			if id > 0 {
				c.Telegram.Admins = append(c.Telegram.Admins, id)
			}
		}
		slices.Sort(c.Telegram.Admins)
	}
}

func (c Connection) file() fileYAML {
	return fileYAML{
		Language: c.Language, Timezone: c.Timezone, Mode: c.Mode,
		Frigate: c.Frigate, MQTT: c.MQTT, Telegram: c.Telegram,
		Web: webYAML{Password: c.Web.Password, AllowedHosts: c.Web.AllowedHosts},
	}
}

// Validate reports whether the connection gives a valid configuration.
func (c Connection) Validate() error {
	_, err := build(c.file())
	return err
}

// LoadConnection reads the connection saved by the setup page; ErrNotConfigured
// when there is none.
func LoadConnection(path string) (*Connection, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotConfigured
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var c Connection
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", path, err)
	}
	return &c, nil
}

// loadConnection builds the configuration from the saved connection. Its values
// are taken as they are: unlike config.yml, a ${VAR} in a password is not a variable.
func loadConnection(path string) (*Config, error) {
	conn, err := LoadConnection(path)
	if err != nil {
		return nil, err
	}
	// The state lives next to the connection, in the same volume.
	f := conn.file()
	f.StateFile = filepath.Join(filepath.Dir(path), "state.json")
	f.HTTPListen = SetupListen()
	c, err := build(f)
	if err != nil {
		return nil, err
	}
	c.Source = path
	return c, nil
}

// SaveConnection writes the connection atomically, readable by the service alone:
// it holds the bot token and the passwords.
func SaveConnection(path string, c Connection) error {
	body, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".connection-*.yml")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no effect after a successful rename
	_, err = f.WriteString(connectionHeader + string(body))
	if err == nil {
		err = f.Chmod(0o600)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// envConfigured reports whether the environment describes the connection: one of
// the required variables is enough, FromEnv then reports the missing ones.
func envConfigured(lookup func(string) (string, bool)) bool {
	for _, name := range []string{"TELEGRAM_TOKEN", "TELEGRAM_CHAT_ID", "FRIGATE_URL", "MQTT_BROKER"} {
		if v, _ := lookup(name); strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}

// SetupListen is the address of the setup page, before any configuration:
// HTTP_LISTEN if set, otherwise the default one.
func SetupListen() string {
	if v := strings.TrimSpace(os.Getenv("HTTP_LISTEN")); v != "" {
		return v
	}
	return DefaultHTTPListen
}
