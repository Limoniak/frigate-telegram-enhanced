// Package config loads, completes and validates the YAML configuration of the service.
package config

import (
	"bytes"
	"errors"
	"io"
	"maps"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"frigate-telegram-enhanced/internal/i18n"
)

const (
	ModeEvents  = "events"
	ModeReviews = "reviews"
)

// Behavior of a camera when someone is home (see Presence).
const (
	HomeNotify = "notify" // notifier normalement
	HomeSilent = "silent" // notify without sound
	HomeSkip   = "skip"   // send nothing
)

// DefaultHomeValues are the values of a presence topic that mean "home": those of
// Home Assistant (person, device_tracker) and of binary sensors.
var DefaultHomeValues = []string{"home", "on", "true", "1", "present"}

// DefaultHTTPListen is the default listening address of the web interface, /healthz
// and /metrics. 8431: a port no common service uses.
const DefaultHTTPListen = ":8431"

type Frigate struct {
	URL                string `yaml:"url" json:"url"`
	ExternalURL        string `yaml:"external_url,omitempty" json:"-"`
	Username           string `yaml:"username,omitempty" json:"username"`
	Password           string `yaml:"password,omitempty" json:"password"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify,omitempty" json:"insecure_skip_verify"`
}

type MQTT struct {
	Broker             string `yaml:"broker" json:"broker"`
	Username           string `yaml:"username,omitempty" json:"username"`
	Password           string `yaml:"password,omitempty" json:"password"`
	ClientID           string `yaml:"client_id,omitempty" json:"-"`
	TopicPrefix        string `yaml:"topic_prefix,omitempty" json:"topic_prefix"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify,omitempty" json:"insecure_skip_verify"`
}

type Telegram struct {
	Token  string           `yaml:"token" json:"token"`
	Admins []int64          `yaml:"admins" json:"admins"`
	Chats  map[string]int64 `yaml:"chats" json:"chats"`
}

// Recipient restricts what a recipient receives, on top of the cameras' settings:
// only some objects, not at some hours, silently at others. Empty, it receives
// everything the cameras send it.
type Recipient struct {
	Labels     []string    `yaml:"labels,omitempty" json:"labels"`           // empty = every object
	QuietHours []TimeRange `yaml:"quiet_hours,omitempty" json:"quiet_hours"` // silent for this recipient
	OffHours   []TimeRange `yaml:"off_hours,omitempty" json:"off_hours"`     // nothing for this recipient
}

// IsZero reports a recipient without restrictions.
func (r Recipient) IsZero() bool {
	return len(r.Labels) == 0 && len(r.QuietHours) == 0 && len(r.OffHours) == 0
}

// Presence describes who is home, read from MQTT topics (Home Assistant, a
// sensor…). Someone is home as soon as one of the topics carries one of the
// HomeValues. Topics accept the MQTT wildcards + and #.
type Presence struct {
	Topics     []string `yaml:"topics"`
	HomeValues []string `yaml:"home_values"`
}

// Enabled reports whether presence is tracked.
func (p Presence) Enabled() bool { return len(p.Topics) > 0 }

// Web configures the notification settings interface, served by the same HTTP
// server as /healthz and /metrics.
type Web struct {
	Enabled  bool
	Password string // empty = no authentication
	// AllowedHosts lists the host names accepted besides IP addresses and localhost
	// when no password is set (protection against DNS rebinding).
	AllowedHosts []string
	// ProtectMetrics puts /metrics behind the interface password too.
	ProtectMetrics bool
}

// Notify is the effective notification configuration of a camera.
type Notify struct {
	Enabled          bool
	Chats            []string
	Labels           []string
	Zones            []string
	MinScore         MinScore
	Cooldown         time.Duration
	IgnoreStationary bool
	Severity         []string
	Snapshot         bool
	Crop             bool // image cropped on the object rather than the wide shot
	Clip             bool
	// MediaInPlace: at the end of the event, the video (or GIF) replaces the image in
	// the notification message, instead of arriving as a reply.
	MediaInPlace     bool
	GIF              bool
	GenAIDescription bool
	ClipDelay        time.Duration
	QuietHours       []TimeRange
	OffHours         []TimeRange
	WhenHome         string // HomeNotify, HomeSilent or HomeSkip
	// Group groups bursts: for this long after a notification, the following
	// detections are added to its message instead of sending a new one.
	// Global setting (the one of notify); 0 = disabled.
	Group time.Duration
	// Filter on Frigate's labels (classification, face, plate): IgnoreSubLabels does
	// not notify these labels, IgnoreKnown no labeled object at all. Since the label
	// arrives after the detection, the notification waits up to SubLabelWait for it;
	// without a label in time, the object is unknown.
	IgnoreSubLabels []string
	IgnoreKnown     bool
	SubLabelWait    time.Duration
}

// FiltersSubLabels reports whether the notification depends on the object's label.
func (n Notify) FiltersSubLabels() bool { return len(n.IgnoreSubLabels) > 0 || n.IgnoreKnown }

// IgnoresSubLabel reports whether a (non-empty) label is to be ignored, regardless
// of case.
func (n Notify) IgnoresSubLabel(sub string) bool {
	if sub == "" {
		return false
	}
	return n.IgnoreKnown || slices.ContainsFunc(n.IgnoreSubLabels, func(s string) bool { return strings.EqualFold(strings.TrimSpace(s), sub) })
}

// Config gathers the settings of the service. The notify and cameras sections can
// be changed live by the web interface: they are only reachable through the
// methods below, which guard them with a lock.
type Config struct {
	Timezone   string
	Location   *time.Location
	Mode       string
	Frigate    Frigate
	MQTT       MQTT
	Telegram   Telegram
	Web        Web
	Presence   Presence
	StateFile  string
	HTTPListen string
	LogLevel   string
	Source     string    // file read, or "environment"
	Language   i18n.Lang // language of Telegram messages and errors (en by default)

	mu          sync.RWMutex
	notify      Notify
	cameras     map[string]Notify
	recipients  map[string]Recipient
	externalURL string // address of the "Open in Frigate" links; empty = the one of frigate.url

	// state from config.yml alone, kept to be able to go back when the web
	// interface removes its overrides.
	fileNotify      Notify
	fileCameras     map[string]Notify
	fileRecipients  map[string]Recipient
	fileExternalURL string
}

// ForCamera returns the effective configuration of a camera (the global one if not listed).
func (c *Config) ForCamera(name string) Notify {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if n, ok := c.cameras[name]; ok {
		return n
	}
	return c.notify
}

// ExternalURL returns the Frigate address used for the notification links: the
// one set (FRIGATE_EXTERNAL_URL or web interface), otherwise frigate.url, the
// address the service reaches Frigate at.
func (c *Config) ExternalURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.externalURL == "" {
		return c.Frigate.URL
	}
	return c.externalURL
}

// Recipient returns the restrictions of a recipient (empty if it has none).
func (c *Config) Recipient(chat string) Recipient {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.recipients[chat]
}

// Global returns the default notification settings.
func (c *Config) Global() Notify {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.notify
}

// CameraNames returns, sorted, the names of the cameras with their own settings.
func (c *Config) CameraNames() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return slices.Sorted(maps.Keys(c.cameras))
}

// ChatID returns the Telegram ID of a named chat.
func (c *Config) ChatID(name string) int64 { return c.Telegram.Chats[name] }

// ChatNames returns, sorted, the declared chat names.
func (c *Config) ChatNames() []string { return slices.Sorted(maps.Keys(c.Telegram.Chats)) }

// IsAdmin reports whether the Telegram user can control the bot.
func (c *Config) IsAdmin(userID int64) bool { return slices.Contains(c.Telegram.Admins, userID) }

// CanControl reports whether userID, writing in chatID, may use the commands and
// buttons: the admins if there are any, wherever they write; without admins, anyone
// in a recipient chat (the person of a private chat, the members of a group).
func (c *Config) CanControl(userID, chatID int64) bool {
	if len(c.Telegram.Admins) > 0 {
		return c.IsAdmin(userID)
	}
	for _, id := range c.Telegram.Chats {
		if id == chatID {
			return true
		}
	}
	return false
}

type fileYAML struct {
	Language   string                 `yaml:"language"`
	Timezone   string                 `yaml:"timezone"`
	Mode       string                 `yaml:"mode"`
	Frigate    Frigate                `yaml:"frigate"`
	MQTT       MQTT                   `yaml:"mqtt"`
	Telegram   Telegram               `yaml:"telegram"`
	Web        webYAML                `yaml:"web"`
	Presence   Presence               `yaml:"presence"`
	Recipients map[string]Recipient   `yaml:"recipients"`
	Notify     NotifyPatch            `yaml:"notify"`
	Cameras    map[string]NotifyPatch `yaml:"cameras"`
	StateFile  string                 `yaml:"state_file"`
	HTTPListen string                 `yaml:"http_listen"`
	LogLevel   string                 `yaml:"log_level"`
}

type webYAML struct {
	Enabled        *bool    `yaml:"enabled"`
	Password       string   `yaml:"password"`
	AllowedHosts   []string `yaml:"allowed_hosts"`
	ProtectMetrics bool     `yaml:"protect_metrics"`
}

// NotifyPatch is a partial override of Notify: missing fields leave the base value
// unchanged. Pointers tell "missing" from "zero value".
type NotifyPatch struct {
	Enabled          *bool        `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Chats            *[]string    `yaml:"chats,omitempty" json:"chats,omitempty"`
	Labels           *[]string    `yaml:"labels,omitempty" json:"labels,omitempty"`
	Zones            *[]string    `yaml:"zones,omitempty" json:"zones,omitempty"`
	MinScore         *MinScore    `yaml:"min_score,omitempty" json:"min_score,omitempty"`
	Cooldown         *Duration    `yaml:"cooldown,omitempty" json:"cooldown,omitempty"`
	IgnoreStationary *bool        `yaml:"ignore_stationary,omitempty" json:"ignore_stationary,omitempty"`
	Severity         *[]string    `yaml:"severity,omitempty" json:"severity,omitempty"`
	Snapshot         *bool        `yaml:"snapshot,omitempty" json:"snapshot,omitempty"`
	Crop             *bool        `yaml:"crop,omitempty" json:"crop,omitempty"`
	MediaInPlace     *bool        `yaml:"media_in_place,omitempty" json:"media_in_place,omitempty"`
	Clip             *bool        `yaml:"clip,omitempty" json:"clip,omitempty"`
	GIF              *bool        `yaml:"gif,omitempty" json:"gif,omitempty"`
	GenAIDescription *bool        `yaml:"genai_description,omitempty" json:"genai_description,omitempty"`
	ClipDelay        *Duration    `yaml:"clip_delay,omitempty" json:"clip_delay,omitempty"`
	QuietHours       *[]TimeRange `yaml:"quiet_hours,omitempty" json:"quiet_hours,omitempty"`
	OffHours         *[]TimeRange `yaml:"off_hours,omitempty" json:"off_hours,omitempty"`
	WhenHome         *string      `yaml:"when_home,omitempty" json:"when_home,omitempty"`
	Group            *Duration    `yaml:"group,omitempty" json:"group,omitempty"`
	IgnoreSubLabels  *[]string    `yaml:"ignore_sub_labels,omitempty" json:"ignore_sub_labels,omitempty"`
	IgnoreKnown      *bool        `yaml:"ignore_known,omitempty" json:"ignore_known,omitempty"`
	SubLabelWait     *Duration    `yaml:"sub_label_wait,omitempty" json:"sub_label_wait,omitempty"`
}

func set[T any](dst *T, src *T) {
	if src != nil {
		*dst = *src
	}
}

func setDuration(dst *time.Duration, src *Duration) {
	if src != nil {
		*dst = time.Duration(*src)
	}
}

// ApplyTo lays the present fields over base (field by field replacement).
func (y NotifyPatch) ApplyTo(base Notify) Notify {
	n := base
	set(&n.Enabled, y.Enabled)
	set(&n.Chats, y.Chats)
	set(&n.Labels, y.Labels)
	set(&n.Zones, y.Zones)
	set(&n.MinScore, y.MinScore)
	setDuration(&n.Cooldown, y.Cooldown)
	set(&n.IgnoreStationary, y.IgnoreStationary)
	set(&n.Severity, y.Severity)
	set(&n.Snapshot, y.Snapshot)
	set(&n.Crop, y.Crop)
	set(&n.MediaInPlace, y.MediaInPlace)
	set(&n.Clip, y.Clip)
	set(&n.GIF, y.GIF)
	set(&n.GenAIDescription, y.GenAIDescription)
	setDuration(&n.ClipDelay, y.ClipDelay)
	set(&n.QuietHours, y.QuietHours)
	set(&n.OffHours, y.OffHours)
	set(&n.WhenHome, y.WhenHome)
	setDuration(&n.Group, y.Group)
	set(&n.IgnoreSubLabels, y.IgnoreSubLabels)
	set(&n.IgnoreKnown, y.IgnoreKnown)
	setDuration(&n.SubLabelWait, y.SubLabelWait)
	return n
}

// FullPatch describes n entirely: every field is set.
func FullPatch(n Notify) NotifyPatch {
	cooldown, clipDelay, group, wait := Duration(n.Cooldown), Duration(n.ClipDelay), Duration(n.Group), Duration(n.SubLabelWait)
	return NotifyPatch{
		Enabled: &n.Enabled, Chats: &n.Chats, Labels: &n.Labels, Zones: &n.Zones,
		MinScore: &n.MinScore, Cooldown: &cooldown, IgnoreStationary: &n.IgnoreStationary,
		Severity: &n.Severity, Snapshot: &n.Snapshot, Crop: &n.Crop, Clip: &n.Clip, GIF: &n.GIF, MediaInPlace: &n.MediaInPlace,
		GenAIDescription: &n.GenAIDescription, ClipDelay: &clipDelay,
		QuietHours: &n.QuietHours, OffHours: &n.OffHours, WhenHome: &n.WhenHome, Group: &group,
		IgnoreSubLabels: &n.IgnoreSubLabels, IgnoreKnown: &n.IgnoreKnown, SubLabelWait: &wait,
	}
}

// DiffPatch only describes the fields where n differs from base.
func DiffPatch(base, n Notify) NotifyPatch {
	var p NotifyPatch
	diff(&p.Enabled, base.Enabled, n.Enabled)
	diffSlice(&p.Chats, base.Chats, n.Chats)
	diffSlice(&p.Labels, base.Labels, n.Labels)
	diffSlice(&p.Zones, base.Zones, n.Zones)
	if base.MinScore.Default != n.MinScore.Default || !maps.Equal(base.MinScore.ByLabel, n.MinScore.ByLabel) {
		p.MinScore = &n.MinScore
	}
	if base.Cooldown != n.Cooldown {
		d := Duration(n.Cooldown)
		p.Cooldown = &d
	}
	diff(&p.IgnoreStationary, base.IgnoreStationary, n.IgnoreStationary)
	diffSlice(&p.Severity, base.Severity, n.Severity)
	diff(&p.Snapshot, base.Snapshot, n.Snapshot)
	diff(&p.Crop, base.Crop, n.Crop)
	diff(&p.MediaInPlace, base.MediaInPlace, n.MediaInPlace)
	diff(&p.Clip, base.Clip, n.Clip)
	diff(&p.GIF, base.GIF, n.GIF)
	diff(&p.GenAIDescription, base.GenAIDescription, n.GenAIDescription)
	if base.ClipDelay != n.ClipDelay {
		d := Duration(n.ClipDelay)
		p.ClipDelay = &d
	}
	diffSlice(&p.QuietHours, base.QuietHours, n.QuietHours)
	diffSlice(&p.OffHours, base.OffHours, n.OffHours)
	diff(&p.WhenHome, base.WhenHome, n.WhenHome)
	if base.Group != n.Group {
		d := Duration(n.Group)
		p.Group = &d
	}
	diffSlice(&p.IgnoreSubLabels, base.IgnoreSubLabels, n.IgnoreSubLabels)
	diff(&p.IgnoreKnown, base.IgnoreKnown, n.IgnoreKnown)
	if base.SubLabelWait != n.SubLabelWait {
		d := Duration(n.SubLabelWait)
		p.SubLabelWait = &d
	}
	return p
}

func diff[T comparable](dst **T, base, v T) {
	if base != v {
		dst2 := v
		*dst = &dst2
	}
}

func diffSlice[T comparable](dst **[]T, base, v []T) {
	if !slices.Equal(base, v) {
		s := v
		*dst = &s
	}
}

func defaultNotify(chats map[string]int64) Notify {
	return Notify{
		Enabled:          true,
		Chats:            slices.Sorted(maps.Keys(chats)),
		Cooldown:         time.Minute,
		IgnoreStationary: true,
		Severity:         []string{"alert"},
		Snapshot:         true,
		Clip:             true,
		MediaInPlace:     true,
		GenAIDescription: true,
		ClipDelay:        5 * time.Second,
		WhenHome:         HomeSkip,
		SubLabelWait:     5 * time.Second,
	}
}

// Load reads and validates the configuration file, resolving the ${VAR}.
// Without a file at path, the configuration is read from the environment
// variables (see FromEnv), otherwise from the connection saved by the setup page
// at connectionPath; with none of the three, Load returns ErrNotConfigured.
func Load(path, connectionPath string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if envConfigured(os.LookupEnv) || connectionPath == "" {
			return FromEnv(os.LookupEnv)
		}
		return loadConnection(connectionPath)
	}
	if err != nil {
		return nil, envLang(os.LookupEnv).Errorf("reading the configuration: %w", err)
	}
	c, err := Parse(raw, os.LookupEnv)
	if err != nil {
		return nil, err
	}
	c.Source = path
	return c, nil
}

// Parse builds the configuration from raw YAML. The language comes from the
// language key, otherwise from the LANGUAGE variable.
func Parse(raw []byte, lookup func(string) (string, bool)) (*Config, error) {
	lang := envLang(lookup)
	expanded, err := expandEnv(raw, lookup, lang)
	if err != nil {
		return nil, err
	}
	var f fileYAML
	dec := yaml.NewDecoder(bytes.NewReader(expanded))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, lang.Errorf("the configuration is empty")
		}
		return nil, lang.Errorf("invalid configuration: %w", err)
	}
	if f.Language == "" {
		f.Language, _ = lookup("LANGUAGE")
	}
	return build(f)
}

// envLang reads LANGUAGE, for the errors that come before the configuration is decoded.
func envLang(lookup func(string) (string, bool)) i18n.Lang {
	v, _ := lookup("LANGUAGE")
	l, _ := i18n.Parse(v)
	return l
}

// build completes and validates a decoded configuration (file or environment).
func build(f fileYAML) (*Config, error) {
	lang, langErr := i18n.Parse(f.Language)
	c := &Config{
		Language: lang,
		Timezone: orDefault(f.Timezone, "UTC"),
		Mode:     orDefault(f.Mode, ModeEvents),
		Frigate:  f.Frigate,
		MQTT:     f.MQTT,
		Telegram: f.Telegram,
		Web: Web{
			Enabled:        f.Web.Enabled == nil || *f.Web.Enabled,
			Password:       f.Web.Password,
			AllowedHosts:   f.Web.AllowedHosts,
			ProtectMetrics: f.Web.ProtectMetrics,
		},
		Presence:   f.Presence,
		StateFile:  orDefault(f.StateFile, "/data/state.json"),
		HTTPListen: orDefault(f.HTTPListen, DefaultHTTPListen),
		LogLevel:   orDefault(f.LogLevel, "info"),
	}
	c.Frigate.URL = strings.TrimRight(c.Frigate.URL, "/")
	c.Frigate.ExternalURL = strings.TrimRight(c.Frigate.ExternalURL, "/")
	c.MQTT.ClientID = orDefault(c.MQTT.ClientID, "frigate-telegram-enhanced")
	c.MQTT.TopicPrefix = orDefault(c.MQTT.TopicPrefix, "frigate")
	if c.Presence.Enabled() && len(c.Presence.HomeValues) == 0 {
		c.Presence.HomeValues = DefaultHomeValues
	}
	c.notify = f.Notify.ApplyTo(defaultNotify(c.Telegram.Chats))
	c.cameras = make(map[string]Notify, len(f.Cameras))
	for name, y := range f.Cameras {
		c.cameras[name] = y.ApplyTo(c.notify)
	}
	c.recipients = maps.Clone(f.Recipients)
	c.externalURL, c.fileExternalURL = c.Frigate.ExternalURL, c.Frigate.ExternalURL
	c.fileNotify, c.fileCameras, c.fileRecipients = c.notify, maps.Clone(c.cameras), maps.Clone(c.recipients)
	if err := errors.Join(langErr, c.validate()); err != nil {
		return nil, err
	}
	return c, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

var envRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-([^}]*))?\}`)

// expandEnv replaces ${VAR} and ${VAR:-default} in the scalar values of the YAML
// (never in comments or keys). It goes through a yaml.Node so that the substitution
// applies to the decoded Go value, not to the raw text: a quote or a backslash in
// the variable thus needs no re-escaping to stay valid.
func expandEnv(raw []byte, lookup func(string) (string, bool), lang i18n.Lang) ([]byte, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return raw, nil
	}
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, lang.Errorf("invalid configuration: %w", err)
	}
	var missing []string
	expandScalarNodes(&root, lookup, &missing)
	if len(missing) > 0 {
		return nil, lang.Errorf("missing environment variables: %s", strings.Join(missing, ", "))
	}
	out, err := yaml.Marshal(&root)
	if err != nil {
		return nil, lang.Errorf("invalid configuration: %w", err)
	}
	return out, nil
}

// expandScalarNodes applies the ${VAR} substitution to the value of each scalar node
// of the tree (recursively: keys, values, list items).
func expandScalarNodes(n *yaml.Node, lookup func(string) (string, bool), missing *[]string) {
	if n.Kind == yaml.ScalarNode {
		n.Value = envRe.ReplaceAllStringFunc(n.Value, func(m string) string {
			sub := envRe.FindStringSubmatch(m)
			name := sub[1]
			if v, ok := lookup(name); ok {
				return v
			}
			if sub[2] != "" {
				return sub[3]
			}
			*missing = append(*missing, name)
			return ""
		})
	}
	for _, c := range n.Content {
		expandScalarNodes(c, lookup, missing)
	}
}

func (c *Config) validate() error {
	var errs []error
	l := c.Language
	add := func(format string, a ...any) { errs = append(errs, l.Errorf(format, a...)) }

	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		add("invalid timezone %q: %v", c.Timezone, err)
	} else {
		c.Location = loc
	}
	if c.Mode != ModeEvents && c.Mode != ModeReviews {
		add("invalid mode %q (events or reviews)", c.Mode)
	}
	if c.Frigate.URL == "" {
		add("frigate.url is required")
	}
	if c.MQTT.Broker == "" {
		add("mqtt.broker is required")
	}
	if c.Telegram.Token == "" {
		add("telegram.token is required")
	}
	if len(c.Telegram.Chats) == 0 {
		add("telegram.chats must list at least one chat")
	}
	if c.Web.ProtectMetrics && c.Web.Password == "" {
		add("web.protect_metrics requires web.password")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		add("invalid log_level %q (debug, info, warn, error)", c.LogLevel)
	}
	errs = append(errs, c.validateRecipients(c.recipients, c.Language)...)
	if err := validateExternalURL(c.externalURL, c.Language); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, c.validateNotify("notify", c.notify, c.Language)...)
	for _, name := range slices.Sorted(maps.Keys(c.cameras)) {
		errs = append(errs, c.validateNotify("cameras."+name, c.cameras[name], c.Language)...)
	}
	return errors.Join(errs...)
}

// validateExternalURL checks the address of the links: empty, or http(s)://host[…].
func validateExternalURL(u string, l i18n.Lang) error {
	if u == "" {
		return nil
	}
	p, err := url.Parse(u)
	if err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "" {
		return l.Errorf("invalid Frigate external URL %q (e.g. https://frigate.example.com)", u)
	}
	return nil
}

func (c *Config) validateRecipients(rs map[string]Recipient, l i18n.Lang) []error {
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(rs)) {
		if _, ok := c.Telegram.Chats[name]; !ok {
			errs = append(errs, l.Errorf("recipients: unknown chat %q (see telegram.chats)", name))
		}
	}
	return errs
}

func (c *Config) validateNotify(where string, n Notify, l i18n.Lang) []error {
	var errs []error
	for _, ch := range n.Chats {
		if _, ok := c.Telegram.Chats[ch]; !ok {
			errs = append(errs, l.Errorf("%s.chats: unknown chat %q (see telegram.chats)", where, ch))
		}
	}
	for _, s := range n.Severity {
		if s != "alert" && s != "detection" {
			errs = append(errs, l.Errorf("%s.severity: invalid value %q (alert or detection)", where, s))
		}
	}
	if !n.MinScore.valid() {
		errs = append(errs, l.Errorf("%s.min_score must be between 0 and 1", where))
	}
	switch n.WhenHome {
	case HomeNotify, HomeSilent, HomeSkip, "": // empty: setting older than when_home, means HomeSkip
	default:
		errs = append(errs, l.Errorf("%s.when_home: invalid value %q (notify, silent or skip)", where, n.WhenHome))
	}
	if n.Group > time.Hour {
		errs = append(errs, l.Errorf("%s.group: at most 1h", where))
	}
	if n.SubLabelWait > time.Minute {
		errs = append(errs, l.Errorf("%s.sub_label_wait: at most 1m", where))
	}
	if n.Cooldown < 0 || n.ClipDelay < 0 || n.Group < 0 || n.SubLabelWait < 0 {
		errs = append(errs, l.Errorf("%s: durations cannot be negative", where))
	}
	return errs
}
