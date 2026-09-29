// Package config charge, complète et valide la configuration YAML du service.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	ModeEvents  = "events"
	ModeReviews = "reviews"
)

type Frigate struct {
	URL                string `yaml:"url"`
	ExternalURL        string `yaml:"external_url"`
	Username           string `yaml:"username"`
	Password           string `yaml:"password"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
}

type MQTT struct {
	Broker             string `yaml:"broker"`
	Username           string `yaml:"username"`
	Password           string `yaml:"password"`
	ClientID           string `yaml:"client_id"`
	TopicPrefix        string `yaml:"topic_prefix"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
}

type Telegram struct {
	Token  string           `yaml:"token"`
	Admins []int64          `yaml:"admins"`
	Chats  map[string]int64 `yaml:"chats"`
}

// Web configure l'interface de réglage des notifications, servie par le même
// serveur HTTP que /healthz et /metrics.
type Web struct {
	Enabled  bool
	Password string // vide = pas d'authentification
	// AllowedHosts liste les noms d'hôte acceptés, en plus des adresses IP et de
	// localhost, quand aucun mot de passe n'est défini (protection anti-rebinding DNS).
	AllowedHosts []string
	// ProtectMetrics soumet aussi /metrics au mot de passe de l'interface.
	ProtectMetrics bool
}

// Notify est la configuration de notification effective d'une caméra.
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
	Clip             bool
	GIF              bool
	GenAIDescription bool
	ClipDelay        time.Duration
	QuietHours       []TimeRange
	OffHours         []TimeRange
}

// Config rassemble les réglages du service. Les sections notify et cameras sont
// modifiables à chaud par l'interface web : elles ne sont accessibles qu'à travers
// les méthodes ci-dessous, qui les protègent par un verrou.
type Config struct {
	Timezone   string
	Location   *time.Location
	Mode       string
	Frigate    Frigate
	MQTT       MQTT
	Telegram   Telegram
	Web        Web
	StateFile  string
	HTTPListen string
	LogLevel   string

	mu      sync.RWMutex
	notify  Notify
	cameras map[string]Notify

	// état issu du seul config.yml, conservé pour pouvoir revenir en arrière
	// quand l'interface web supprime ses surcharges.
	fileNotify  Notify
	fileCameras map[string]Notify
}

// ForCamera renvoie la configuration effective d'une caméra (globale si non listée).
func (c *Config) ForCamera(name string) Notify {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if n, ok := c.cameras[name]; ok {
		return n
	}
	return c.notify
}

// Global renvoie les réglages de notification par défaut.
func (c *Config) Global() Notify {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.notify
}

// CameraNames renvoie, triés, les noms des caméras ayant des réglages propres.
func (c *Config) CameraNames() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return slices.Sorted(maps.Keys(c.cameras))
}

// ChatID renvoie l'identifiant Telegram d'un chat nommé.
func (c *Config) ChatID(name string) int64 { return c.Telegram.Chats[name] }

// ChatNames renvoie, triés, les noms de chats déclarés.
func (c *Config) ChatNames() []string { return slices.Sorted(maps.Keys(c.Telegram.Chats)) }

// IsAdmin indique si l'utilisateur Telegram peut piloter le bot.
func (c *Config) IsAdmin(userID int64) bool { return slices.Contains(c.Telegram.Admins, userID) }

type fileYAML struct {
	Timezone   string                 `yaml:"timezone"`
	Mode       string                 `yaml:"mode"`
	Frigate    Frigate                `yaml:"frigate"`
	MQTT       MQTT                   `yaml:"mqtt"`
	Telegram   Telegram               `yaml:"telegram"`
	Web        webYAML                `yaml:"web"`
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

// NotifyPatch est une surcharge partielle de Notify : les champs absents laissent
// la valeur de base inchangée. Les pointeurs distinguent « absent » de « valeur zéro ».
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
	Clip             *bool        `yaml:"clip,omitempty" json:"clip,omitempty"`
	GIF              *bool        `yaml:"gif,omitempty" json:"gif,omitempty"`
	GenAIDescription *bool        `yaml:"genai_description,omitempty" json:"genai_description,omitempty"`
	ClipDelay        *Duration    `yaml:"clip_delay,omitempty" json:"clip_delay,omitempty"`
	QuietHours       *[]TimeRange `yaml:"quiet_hours,omitempty" json:"quiet_hours,omitempty"`
	OffHours         *[]TimeRange `yaml:"off_hours,omitempty" json:"off_hours,omitempty"`
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

// ApplyTo superpose les champs présents sur base (remplacement champ par champ).
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
	set(&n.Clip, y.Clip)
	set(&n.GIF, y.GIF)
	set(&n.GenAIDescription, y.GenAIDescription)
	setDuration(&n.ClipDelay, y.ClipDelay)
	set(&n.QuietHours, y.QuietHours)
	set(&n.OffHours, y.OffHours)
	return n
}

// FullPatch décrit n entièrement : tous les champs sont renseignés.
func FullPatch(n Notify) NotifyPatch {
	cooldown, clipDelay := Duration(n.Cooldown), Duration(n.ClipDelay)
	return NotifyPatch{
		Enabled: &n.Enabled, Chats: &n.Chats, Labels: &n.Labels, Zones: &n.Zones,
		MinScore: &n.MinScore, Cooldown: &cooldown, IgnoreStationary: &n.IgnoreStationary,
		Severity: &n.Severity, Snapshot: &n.Snapshot, Clip: &n.Clip, GIF: &n.GIF,
		GenAIDescription: &n.GenAIDescription, ClipDelay: &clipDelay,
		QuietHours: &n.QuietHours, OffHours: &n.OffHours,
	}
}

// DiffPatch ne décrit que les champs par lesquels n s'écarte de base.
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
	diff(&p.Clip, base.Clip, n.Clip)
	diff(&p.GIF, base.GIF, n.GIF)
	diff(&p.GenAIDescription, base.GenAIDescription, n.GenAIDescription)
	if base.ClipDelay != n.ClipDelay {
		d := Duration(n.ClipDelay)
		p.ClipDelay = &d
	}
	diffSlice(&p.QuietHours, base.QuietHours, n.QuietHours)
	diffSlice(&p.OffHours, base.OffHours, n.OffHours)
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
		GenAIDescription: true,
		ClipDelay:        5 * time.Second,
	}
}

// Load lit et valide le fichier de configuration, en résolvant les ${VAR}.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("lecture de la config: %w", err)
	}
	return Parse(raw, os.LookupEnv)
}

// Parse construit la configuration depuis le YAML brut.
func Parse(raw []byte, lookup func(string) (string, bool)) (*Config, error) {
	expanded, err := expandEnv(raw, lookup)
	if err != nil {
		return nil, err
	}
	var f fileYAML
	dec := yaml.NewDecoder(bytes.NewReader(expanded))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("config vide")
		}
		return nil, fmt.Errorf("config invalide: %w", err)
	}

	c := &Config{
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
		StateFile:  orDefault(f.StateFile, "/data/state.json"),
		HTTPListen: orDefault(f.HTTPListen, ":8080"),
		LogLevel:   orDefault(f.LogLevel, "info"),
	}
	c.Frigate.URL = strings.TrimRight(c.Frigate.URL, "/")
	c.Frigate.ExternalURL = strings.TrimRight(c.Frigate.ExternalURL, "/")
	c.MQTT.ClientID = orDefault(c.MQTT.ClientID, "frigate-telegram")
	c.MQTT.TopicPrefix = orDefault(c.MQTT.TopicPrefix, "frigate")
	c.notify = f.Notify.ApplyTo(defaultNotify(c.Telegram.Chats))
	c.cameras = make(map[string]Notify, len(f.Cameras))
	for name, y := range f.Cameras {
		c.cameras[name] = y.ApplyTo(c.notify)
	}
	c.fileNotify, c.fileCameras = c.notify, maps.Clone(c.cameras)
	if err := c.validate(); err != nil {
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

// expandEnv remplace ${VAR} et ${VAR:-défaut} dans les valeurs scalaires du YAML (jamais
// dans les commentaires ni les clés). On passe par un yaml.Node pour que la substitution
// agisse sur la valeur Go décodée, pas sur le texte brut : un guillemet ou un antislash
// dans la variable n'a donc pas besoin d'être ré-échappé pour rester valide.
func expandEnv(raw []byte, lookup func(string) (string, bool)) ([]byte, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return raw, nil
	}
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("config invalide: %w", err)
	}
	var missing []string
	expandScalarNodes(&root, lookup, &missing)
	if len(missing) > 0 {
		return nil, fmt.Errorf("variables d'environnement manquantes: %s", strings.Join(missing, ", "))
	}
	out, err := yaml.Marshal(&root)
	if err != nil {
		return nil, fmt.Errorf("config invalide: %w", err)
	}
	return out, nil
}

// expandScalarNodes applique la substitution ${VAR} à la valeur de chaque nœud scalaire
// de l'arbre (récursif : clés, valeurs, éléments de liste).
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
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		add("timezone %q invalide: %v", c.Timezone, err)
	} else {
		c.Location = loc
	}
	if c.Mode != ModeEvents && c.Mode != ModeReviews {
		add("mode %q invalide (events ou reviews)", c.Mode)
	}
	if c.Frigate.URL == "" {
		add("frigate.url est obligatoire")
	}
	if c.MQTT.Broker == "" {
		add("mqtt.broker est obligatoire")
	}
	if c.Telegram.Token == "" {
		add("telegram.token est obligatoire")
	}
	if len(c.Telegram.Chats) == 0 {
		add("telegram.chats doit contenir au moins un chat")
	}
	if len(c.Telegram.Admins) == 0 {
		add("telegram.admins doit contenir au moins un utilisateur")
	}
	if c.Web.ProtectMetrics && c.Web.Password == "" {
		add("web.protect_metrics exige web.password")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		add("log_level %q invalide (debug, info, warn, error)", c.LogLevel)
	}
	errs = append(errs, c.validateNotify("notify", c.notify)...)
	for _, name := range slices.Sorted(maps.Keys(c.cameras)) {
		errs = append(errs, c.validateNotify("cameras."+name, c.cameras[name])...)
	}
	return errors.Join(errs...)
}

func (c *Config) validateNotify(where string, n Notify) []error {
	var errs []error
	for _, ch := range n.Chats {
		if _, ok := c.Telegram.Chats[ch]; !ok {
			errs = append(errs, fmt.Errorf("%s.chats: chat %q inconnu (voir telegram.chats)", where, ch))
		}
	}
	for _, s := range n.Severity {
		if s != "alert" && s != "detection" {
			errs = append(errs, fmt.Errorf("%s.severity: valeur %q invalide (alert ou detection)", where, s))
		}
	}
	if !n.MinScore.valid() {
		errs = append(errs, fmt.Errorf("%s.min_score doit être compris entre 0 et 1", where))
	}
	if n.Cooldown < 0 || n.ClipDelay < 0 {
		errs = append(errs, fmt.Errorf("%s: les durées ne peuvent pas être négatives", where))
	}
	return errs
}
