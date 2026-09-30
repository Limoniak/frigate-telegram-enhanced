package config

import (
	"errors"
	"slices"
	"strconv"
	"strings"

	"frigate-telegram-enhanced/internal/i18n"
)

// FromEnv builds the configuration from the environment variables, for an
// installation with docker-compose alone, without config.yml. Notification
// settings are then made in the web interface.
//
// Required: TELEGRAM_TOKEN, TELEGRAM_CHAT_ID, FRIGATE_URL, MQTT_BROKER.
// Optional: TELEGRAM_ADMINS, FRIGATE_EXTERNAL_URL, FRIGATE_USERNAME,
// FRIGATE_PASSWORD, FRIGATE_INSECURE_SKIP_VERIFY, MQTT_USERNAME, MQTT_PASSWORD,
// MQTT_TOPIC_PREFIX, MQTT_CLIENT_ID, MQTT_INSECURE_SKIP_VERIFY, MODE, TZ,
// WEB_ENABLED, WEB_PASSWORD, WEB_ALLOWED_HOSTS, WEB_PROTECT_METRICS, LOG_LEVEL,
// STATE_FILE, HTTP_LISTEN, LANGUAGE (en by default; see internal/i18n/locales),
// PRESENCE_TOPICS, PRESENCE_HOME_VALUES.
func FromEnv(lookup func(string) (string, bool)) (*Config, error) {
	get := func(name string) string {
		v, _ := lookup(name)
		return strings.TrimSpace(v)
	}
	l, _ := i18n.Parse(get("LANGUAGE")) // an invalid language is reported by build
	var errs []error
	boolean := func(name string, def bool) bool {
		v := get(name)
		if v == "" {
			return def
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, l.Errorf("%s=%q is invalid (true or false)", name, v))
		}
		return b
	}
	var missing []string
	required := func(name string) string {
		v := get(name)
		if v == "" {
			missing = append(missing, name)
		}
		return v
	}

	token := required("TELEGRAM_TOKEN")
	chatsRaw := required("TELEGRAM_CHAT_ID")
	frigateURL := required("FRIGATE_URL")
	broker := required("MQTT_BROKER")
	if len(missing) > 0 {
		return nil, l.Errorf("missing required environment variables: %s "+
			"(see docker-compose.yml, or provide a /config/config.yml file)", strings.Join(missing, ", "))
	}

	chats, err := parseChats(chatsRaw, l)
	if err != nil {
		errs = append(errs, err)
	}
	admins, err := parseAdmins(get("TELEGRAM_ADMINS"), chats, l)
	if err != nil {
		errs = append(errs, err)
	}

	enabled := boolean("WEB_ENABLED", true)
	f := fileYAML{
		Language: get("LANGUAGE"),
		Timezone: get("TZ"),
		Mode:     get("MODE"),
		Frigate: Frigate{
			URL:                frigateURL,
			ExternalURL:        get("FRIGATE_EXTERNAL_URL"),
			Username:           get("FRIGATE_USERNAME"),
			Password:           get("FRIGATE_PASSWORD"),
			InsecureSkipVerify: boolean("FRIGATE_INSECURE_SKIP_VERIFY", false),
		},
		MQTT: MQTT{
			Broker:             brokerURL(broker),
			Username:           get("MQTT_USERNAME"),
			Password:           get("MQTT_PASSWORD"),
			ClientID:           get("MQTT_CLIENT_ID"),
			TopicPrefix:        get("MQTT_TOPIC_PREFIX"),
			InsecureSkipVerify: boolean("MQTT_INSECURE_SKIP_VERIFY", false),
		},
		Telegram: Telegram{Token: token, Admins: admins, Chats: chats},
		Web: webYAML{
			Enabled:        &enabled,
			Password:       get("WEB_PASSWORD"),
			AllowedHosts:   splitList(get("WEB_ALLOWED_HOSTS")),
			ProtectMetrics: boolean("WEB_PROTECT_METRICS", false),
		},
		Presence:   Presence{Topics: splitList(get("PRESENCE_TOPICS")), HomeValues: splitList(get("PRESENCE_HOME_VALUES"))},
		StateFile:  get("STATE_FILE"),
		HTTPListen: get("HTTP_LISTEN"),
		LogLevel:   get("LOG_LEVEL"),
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	c, err := build(f)
	if err != nil {
		return nil, err
	}
	c.Source = "environment"
	return c, nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseChats reads TELEGRAM_CHAT_ID: an ID, or a comma-separated list, each one
// optionally named ("me=123456789,family=-1001234567890"). Without a name, the ID
// itself serves as the name.
func parseChats(s string, l i18n.Lang) (map[string]int64, error) {
	chats := map[string]int64{}
	for _, item := range splitList(s) {
		name, idText, named := strings.Cut(item, "=")
		if !named {
			idText = name
		}
		name, idText = strings.TrimSpace(name), strings.TrimSpace(idText)
		id, err := strconv.ParseInt(idText, 10, 64)
		if err != nil || id == 0 || name == "" {
			return nil, l.Errorf("TELEGRAM_CHAT_ID: %q is invalid (expected 123456789, or name=123456789)", item)
		}
		if _, dup := chats[name]; dup {
			return nil, l.Errorf("TELEGRAM_CHAT_ID: duplicate name %q", name)
		}
		chats[name] = id
	}
	if len(chats) == 0 {
		return nil, l.Errorf("TELEGRAM_CHAT_ID contains no ID")
	}
	return chats, nil
}

// parseAdmins reads TELEGRAM_ADMINS. By default, the positive IDs of
// TELEGRAM_CHAT_ID: a private chat with the bot has the user's ID. A group
// (negative ID) designates nobody: TELEGRAM_ADMINS is then needed.
func parseAdmins(s string, chats map[string]int64, l i18n.Lang) ([]int64, error) {
	var admins []int64
	if s == "" {
		for _, id := range chats {
			if id > 0 {
				admins = append(admins, id)
			}
		}
		slices.Sort(admins)
		if len(admins) == 0 {
			return nil, l.Errorf("TELEGRAM_ADMINS is required when TELEGRAM_CHAT_ID only lists groups: " +
				"give the IDs of the users allowed to control the bot")
		}
		return admins, nil
	}
	for _, item := range splitList(s) {
		id, err := strconv.ParseInt(item, 10, 64)
		if err != nil || id <= 0 {
			return nil, l.Errorf("TELEGRAM_ADMINS: %q is invalid (a user ID, positive)", item)
		}
		admins = append(admins, id)
	}
	return admins, nil
}

// brokerURL accepts "mosquitto", "mosquitto:1883" or a full URL ("tcp://…",
// "ssl://…") and returns a full URL.
func brokerURL(s string) string {
	if strings.Contains(s, "://") {
		return s
	}
	if !strings.Contains(s, ":") {
		s += ":1883"
	}
	return "tcp://" + s
}
