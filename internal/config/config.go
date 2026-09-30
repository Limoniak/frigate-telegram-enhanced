// Package config charge, complète et valide la configuration YAML du service.
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

// Comportement d'une caméra quand quelqu'un est à la maison (voir Presence).
const (
	HomeNotify = "notify" // notifier normalement
	HomeSilent = "silent" // notifier sans son
	HomeSkip   = "skip"   // ne rien envoyer
)

// DefaultHomeValues sont les valeurs d'un topic de présence qui signifient « à la
// maison » : celles de Home Assistant (person, device_tracker) et des capteurs binaires.
var DefaultHomeValues = []string{"home", "on", "true", "1", "present"}

// DefaultHTTPListen est l'adresse d'écoute par défaut de l'interface web, de
// /healthz et de /metrics. 8431 : un port qu'aucun service courant n'utilise.
const DefaultHTTPListen = ":8431"

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

// Recipient restreint ce que reçoit un destinataire, en plus des réglages des
// caméras : seulement certains objets, pas à certaines heures, sans son à d'autres.
// Vide, il reçoit tout ce que les caméras lui envoient.
type Recipient struct {
	Labels     []string    `yaml:"labels,omitempty" json:"labels"`           // vide = tous les objets
	QuietHours []TimeRange `yaml:"quiet_hours,omitempty" json:"quiet_hours"` // sans son pour lui
	OffHours   []TimeRange `yaml:"off_hours,omitempty" json:"off_hours"`     // rien pour lui
}

// IsZero indique un destinataire sans restriction.
func (r Recipient) IsZero() bool {
	return len(r.Labels) == 0 && len(r.QuietHours) == 0 && len(r.OffHours) == 0
}

// Presence décrit qui est à la maison, lu sur des topics MQTT (Home Assistant,
// capteur…). Quelqu'un est à la maison dès qu'un des topics porte une des valeurs de
// HomeValues. Les topics acceptent les jokers MQTT + et #.
type Presence struct {
	Topics     []string `yaml:"topics"`
	HomeValues []string `yaml:"home_values"`
}

// Enabled indique si la présence est suivie.
func (p Presence) Enabled() bool { return len(p.Topics) > 0 }

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
	Crop             bool // image recadrée sur l'objet plutôt que le plan large
	Clip             bool
	// MediaInPlace : à la fin de l'événement, la vidéo (ou le GIF) remplace l'image
	// dans le message de la notification, au lieu d'arriver en réponse.
	MediaInPlace     bool
	GIF              bool
	GenAIDescription bool
	ClipDelay        time.Duration
	QuietHours       []TimeRange
	OffHours         []TimeRange
	WhenHome         string // HomeNotify, HomeSilent ou HomeSkip
	// Group regroupe les rafales : pendant ce délai après une notification, les
	// détections suivantes s'ajoutent à son message au lieu d'en envoyer un nouveau.
	// Réglage global (celui de notify) ; 0 = désactivé.
	Group time.Duration
	// Filtre sur les étiquettes de Frigate (classification, visage, plaque) :
	// IgnoreSubLabels ne notifie pas ces étiquettes, IgnoreKnown aucun objet étiqueté.
	// L'étiquette arrivant après la détection, la notification attend jusqu'à
	// SubLabelWait qu'elle soit connue ; sans étiquette à temps, l'objet est inconnu.
	IgnoreSubLabels []string
	IgnoreKnown     bool
	SubLabelWait    time.Duration
}

// FiltersSubLabels indique si la notification dépend de l'étiquette de l'objet.
func (n Notify) FiltersSubLabels() bool { return len(n.IgnoreSubLabels) > 0 || n.IgnoreKnown }

// IgnoresSubLabel indique si une étiquette (non vide) est à ignorer, sans tenir
// compte de la casse.
func (n Notify) IgnoresSubLabel(sub string) bool {
	if sub == "" {
		return false
	}
	return n.IgnoreKnown || slices.ContainsFunc(n.IgnoreSubLabels, func(s string) bool { return strings.EqualFold(strings.TrimSpace(s), sub) })
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
	Presence   Presence
	StateFile  string
	HTTPListen string
	LogLevel   string
	Source     string    // fichier lu, ou "environment"
	Language   i18n.Lang // langue des messages Telegram et des erreurs (en par défaut)

	mu          sync.RWMutex
	notify      Notify
	cameras     map[string]Notify
	recipients  map[string]Recipient
	externalURL string // adresse des liens « Ouvrir dans Frigate » ; vide = celle de frigate.url

	// état issu du seul config.yml, conservé pour pouvoir revenir en arrière
	// quand l'interface web supprime ses surcharges.
	fileNotify      Notify
	fileCameras     map[string]Notify
	fileRecipients  map[string]Recipient
	fileExternalURL string
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

// ExternalURL renvoie l'adresse de Frigate utilisée pour les liens des
// notifications : celle réglée (FRIGATE_EXTERNAL_URL ou interface web), sinon
// frigate.url, l'adresse par laquelle le service joint Frigate.
func (c *Config) ExternalURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.externalURL == "" {
		return c.Frigate.URL
	}
	return c.externalURL
}

// Recipient renvoie les restrictions d'un destinataire (vides s'il n'en a pas).
func (c *Config) Recipient(chat string) Recipient {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.recipients[chat]
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

// FullPatch décrit n entièrement : tous les champs sont renseignés.
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

// Load lit et valide le fichier de configuration, en résolvant les ${VAR}.
// Sans fichier à path, la configuration est lue dans les variables d'environnement
// (voir FromEnv) : c'est l'installation par docker-compose seul.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return FromEnv(os.LookupEnv)
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

// Parse construit la configuration depuis le YAML brut. La langue vient de la clé
// language, sinon de la variable LANGUAGE.
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

// envLang lit LANGUAGE, pour les erreurs qui précèdent le décodage de la config.
func envLang(lookup func(string) (string, bool)) i18n.Lang {
	v, _ := lookup("LANGUAGE")
	l, _ := i18n.Parse(v)
	return l
}

// build complète et valide une configuration décodée (fichier ou environnement).
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

// expandEnv remplace ${VAR} et ${VAR:-défaut} dans les valeurs scalaires du YAML (jamais
// dans les commentaires ni les clés). On passe par un yaml.Node pour que la substitution
// agisse sur la valeur Go décodée, pas sur le texte brut : un guillemet ou un antislash
// dans la variable n'a donc pas besoin d'être ré-échappé pour rester valide.
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
	if len(c.Telegram.Admins) == 0 {
		add("telegram.admins must list at least one user")
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

// validateExternalURL vérifie l'adresse des liens : vide, ou http(s)://hôte[…].
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
	case HomeNotify, HomeSilent, HomeSkip, "": // vide : réglage antérieur à when_home, vaut HomeSkip
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
