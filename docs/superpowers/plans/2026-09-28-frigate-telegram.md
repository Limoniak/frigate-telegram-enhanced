# frigate-telegram — Plan d'implémentation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Service Go (image Docker) qui transforme les détections Frigate reçues par MQTT en notifications Telegram (snapshot, puis clip), avec filtres, commandes et boutons.

**Architecture:** Un seul binaire. `mqttsub` reçoit les messages et les passe au `notifier`. Celui-ci suit le cycle de vie de chaque événement, interroge le moteur `filter`, qui est pur, puis envoie les médias récupérés via `frigate` au client `telegram`. Le `bot` fait du long polling sur Telegram pour les commandes et les boutons, et modifie l'état (`state`), persisté en JSON. `server` expose `/healthz` et `/metrics`.

**Tech Stack:** Go 1.27, `github.com/eclipse/paho.mqtt.golang`, `gopkg.in/yaml.v3`, `github.com/prometheus/client_golang`, `github.com/mochi-mqtt/server/v2` (tests uniquement), stdlib pour tout le reste. Image `gcr.io/distroless/static-debian12:nonroot`.

**Spec:** `docs/superpowers/specs/2026-09-28-frigate-telegram-design.md`

## Global Constraints

- Module Go : `frigate-telegram` (imports `frigate-telegram/internal/...`), directive `go 1.27`.
- Toute la communication utilisateur (messages Telegram, logs, erreurs de config) est en **français**.
- Messages Telegram en `parse_mode=HTML` : tout texte dynamique passe par `html.EscapeString`.
- Limites Telegram : upload de 50 Mo maximum (`50 << 20`), photo de 10 Mo maximum (`10 << 20`), légende de 1024 caractères, `callback_data` de 64 octets.
- Logs : `log/slog` JSON sur stdout.
- Secrets uniquement via `${VAR}` / `${VAR:-défaut}` dans le YAML. Le token Telegram ne doit jamais apparaître dans un log ou une erreur.
- Poste de dev sous Windows : les commandes ci-dessous se lancent depuis la racine du dépôt, en PowerShell ou Git Bash. `-race` nécessite cgo : on l'utilise en CI (Linux), pas en local.
- Chaque commit se termine par la ligne `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>` (passée avec un second `-m`).

## Structure des fichiers

```
cmd/frigate-telegram/main.go         assemblage, flags, arrêt propre
internal/config/config.go            types, Load/Parse, défauts, fusion, validation
internal/config/types.go             MinScore, TimeRange, InRanges
internal/frigate/types.go            payloads MQTT + API, parsing, UnixTime
internal/frigate/paths.go            chemins de l'API Frigate
internal/frigate/client.go           client HTTP (auth JWT, limites de taille, fichiers temporaires)
internal/state/state.go              pauses, coupures, cooldowns, persistance atomique
internal/filter/filter.go            moteur de décision pur
internal/telegram/limiter.go         espacement des envois
internal/telegram/types.go           types Bot API
internal/telegram/client.go          appels Bot API, multipart, retries
internal/actions/actions.go          encodage/décodage des callback_data
internal/metrics/metrics.go          métriques Prometheus
internal/notifier/caption.go         légende et boutons
internal/notifier/notifier.go        boucle, suivi des événements, notify/finish
internal/notifier/media.go           téléchargements et envois multi-chats
internal/notifier/events.go          mode events
internal/notifier/reviews.go         mode reviews + descriptions GenAI
internal/notifier/ondemand.go        clip à la demande, /last
internal/mqttsub/mqttsub.go          client MQTT
internal/bot/bot.go                  boucle getUpdates, routage
internal/bot/commands.go             commandes et callbacks
internal/server/server.go            /healthz, /metrics, Check
Dockerfile, docker-compose.yml, .env.example, config.example.yml, README.md, .gitignore, .github/workflows/docker.yml
```

---

### Task 1: Initialisation du module et configuration

**Files:**
- Create: `go.mod`, `.gitignore`
- Create: `internal/config/types.go`
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces:
  - `config.Parse(raw []byte, lookup func(string) (string, bool)) (*config.Config, error)`, `config.Load(path string) (*config.Config, error)`
  - `type Config struct { Timezone string; Location *time.Location; Mode string; Frigate Frigate; MQTT MQTT; Telegram Telegram; Notify Notify; Cameras map[string]Notify; StateFile, HTTPListen, LogLevel string }`
  - `(*Config).ForCamera(name string) Notify`, `(*Config).ChatID(name string) int64`, `(*Config).IsAdmin(userID int64) bool`
  - `type Frigate struct { URL, ExternalURL, Username, Password string; InsecureSkipVerify bool }`
  - `type MQTT struct { Broker, Username, Password, ClientID, TopicPrefix string; InsecureSkipVerify bool }`
  - `type Telegram struct { Token string; Admins []int64; Chats map[string]int64 }`
  - `type Notify struct { Enabled bool; Chats, Labels, Zones []string; MinScore MinScore; Cooldown time.Duration; IgnoreStationary bool; Severity []string; Snapshot, Clip, GIF, GenAIDescription bool; ClipDelay time.Duration; QuietHours, OffHours []TimeRange }`
  - `type MinScore struct { Default float64; ByLabel map[string]float64 }`, `(MinScore).For(label string) float64`
  - `type TimeRange struct { From, To int }` (minutes depuis minuit), `(TimeRange).Contains(minute int) bool`, `config.InRanges(rs []TimeRange, at time.Time) bool`
  - Constantes `config.ModeEvents = "events"`, `config.ModeReviews = "reviews"`

- [ ] **Step 1: Initialiser le module et les dépendances**

```bash
go mod init frigate-telegram
go get gopkg.in/yaml.v3
```

Créer `.gitignore` :

```gitignore
/data/
/config/config.yml
.env
*.exe
/frigate-telegram
```

- [ ] **Step 2: Écrire les tests (qui échouent)**

`internal/config/config_test.go` :

```go
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
	n := c.Notify
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
```

- [ ] **Step 3: Lancer les tests pour vérifier qu'ils échouent**

Run: `go test ./internal/config/`
Expected: FAIL (compilation : `undefined: Parse`, `undefined: TimeRange`…)

- [ ] **Step 4: Implémenter `internal/config/types.go`**

```go
package config

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// MinScore est un score minimal global ou par label.
// En YAML : `min_score: 0.7` ou `min_score: {person: 0.7, car: 0.85}`.
type MinScore struct {
	Default float64
	ByLabel map[string]float64
}

func (m *MinScore) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.MappingNode {
		return n.Decode(&m.ByLabel)
	}
	return n.Decode(&m.Default)
}

// For renvoie le score minimal applicable à un label.
func (m MinScore) For(label string) float64 {
	if v, ok := m.ByLabel[label]; ok {
		return v
	}
	return m.Default
}

func (m MinScore) valid() bool {
	ok := func(v float64) bool { return v >= 0 && v <= 1 }
	if !ok(m.Default) {
		return false
	}
	for _, v := range m.ByLabel {
		if !ok(v) {
			return false
		}
	}
	return true
}

// TimeRange est une plage horaire quotidienne, en minutes depuis minuit.
// Une plage dont From > To passe minuit (ex. 22:00 → 07:00).
type TimeRange struct {
	From, To int
}

func (t *TimeRange) UnmarshalYAML(n *yaml.Node) error {
	var raw struct {
		From string `yaml:"from"`
		To   string `yaml:"to"`
	}
	if err := n.Decode(&raw); err != nil {
		return err
	}
	var err error
	if t.From, err = parseClock(raw.From); err != nil {
		return err
	}
	t.To, err = parseClock(raw.To)
	return err
}

func parseClock(s string) (int, error) {
	tm, err := time.Parse("15:04", s)
	if err != nil {
		return 0, fmt.Errorf("heure invalide %q (format HH:MM)", s)
	}
	return tm.Hour()*60 + tm.Minute(), nil
}

// Contains indique si la minute de la journée tombe dans la plage (borne de fin exclue).
func (t TimeRange) Contains(minute int) bool {
	if t.From <= t.To {
		return minute >= t.From && minute < t.To
	}
	return minute >= t.From || minute < t.To
}

// InRanges indique si l'heure locale de at tombe dans une des plages.
func InRanges(rs []TimeRange, at time.Time) bool {
	minute := at.Hour()*60 + at.Minute()
	for _, r := range rs {
		if r.Contains(minute) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 5: Implémenter `internal/config/config.go`**

```go
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

type Config struct {
	Timezone   string
	Location   *time.Location
	Mode       string
	Frigate    Frigate
	MQTT       MQTT
	Telegram   Telegram
	Notify     Notify
	Cameras    map[string]Notify
	StateFile  string
	HTTPListen string
	LogLevel   string
}

// ForCamera renvoie la configuration effective d'une caméra (globale si non listée).
func (c *Config) ForCamera(name string) Notify {
	if n, ok := c.Cameras[name]; ok {
		return n
	}
	return c.Notify
}

// ChatID renvoie l'identifiant Telegram d'un chat nommé.
func (c *Config) ChatID(name string) int64 { return c.Telegram.Chats[name] }

// IsAdmin indique si l'utilisateur Telegram peut piloter le bot.
func (c *Config) IsAdmin(userID int64) bool { return slices.Contains(c.Telegram.Admins, userID) }

type fileYAML struct {
	Timezone   string                `yaml:"timezone"`
	Mode       string                `yaml:"mode"`
	Frigate    Frigate               `yaml:"frigate"`
	MQTT       MQTT                  `yaml:"mqtt"`
	Telegram   Telegram              `yaml:"telegram"`
	Notify     notifyYAML            `yaml:"notify"`
	Cameras    map[string]notifyYAML `yaml:"cameras"`
	StateFile  string                `yaml:"state_file"`
	HTTPListen string                `yaml:"http_listen"`
	LogLevel   string                `yaml:"log_level"`
}

// notifyYAML utilise des pointeurs pour distinguer « absent » de « valeur zéro ».
type notifyYAML struct {
	Enabled          *bool          `yaml:"enabled"`
	Chats            *[]string      `yaml:"chats"`
	Labels           *[]string      `yaml:"labels"`
	Zones            *[]string      `yaml:"zones"`
	MinScore         *MinScore      `yaml:"min_score"`
	Cooldown         *time.Duration `yaml:"cooldown"`
	IgnoreStationary *bool          `yaml:"ignore_stationary"`
	Severity         *[]string      `yaml:"severity"`
	Snapshot         *bool          `yaml:"snapshot"`
	Clip             *bool          `yaml:"clip"`
	GIF              *bool          `yaml:"gif"`
	GenAIDescription *bool          `yaml:"genai_description"`
	ClipDelay        *time.Duration `yaml:"clip_delay"`
	QuietHours       *[]TimeRange   `yaml:"quiet_hours"`
	OffHours         *[]TimeRange   `yaml:"off_hours"`
}

func set[T any](dst *T, src *T) {
	if src != nil {
		*dst = *src
	}
}

// applyTo superpose les champs présents sur base (remplacement champ par champ).
func (y notifyYAML) applyTo(base Notify) Notify {
	n := base
	set(&n.Enabled, y.Enabled)
	set(&n.Chats, y.Chats)
	set(&n.Labels, y.Labels)
	set(&n.Zones, y.Zones)
	set(&n.MinScore, y.MinScore)
	set(&n.Cooldown, y.Cooldown)
	set(&n.IgnoreStationary, y.IgnoreStationary)
	set(&n.Severity, y.Severity)
	set(&n.Snapshot, y.Snapshot)
	set(&n.Clip, y.Clip)
	set(&n.GIF, y.GIF)
	set(&n.GenAIDescription, y.GenAIDescription)
	set(&n.ClipDelay, y.ClipDelay)
	set(&n.QuietHours, y.QuietHours)
	set(&n.OffHours, y.OffHours)
	return n
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
		Timezone:   orDefault(f.Timezone, "UTC"),
		Mode:       orDefault(f.Mode, ModeEvents),
		Frigate:    f.Frigate,
		MQTT:       f.MQTT,
		Telegram:   f.Telegram,
		StateFile:  orDefault(f.StateFile, "/data/state.json"),
		HTTPListen: orDefault(f.HTTPListen, ":8080"),
		LogLevel:   orDefault(f.LogLevel, "info"),
	}
	c.Frigate.URL = strings.TrimRight(c.Frigate.URL, "/")
	c.Frigate.ExternalURL = strings.TrimRight(c.Frigate.ExternalURL, "/")
	c.MQTT.ClientID = orDefault(c.MQTT.ClientID, "frigate-telegram")
	c.MQTT.TopicPrefix = orDefault(c.MQTT.TopicPrefix, "frigate")
	c.Notify = f.Notify.applyTo(defaultNotify(c.Telegram.Chats))
	c.Cameras = make(map[string]Notify, len(f.Cameras))
	for name, y := range f.Cameras {
		c.Cameras[name] = y.applyTo(c.Notify)
	}
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

// expandEnv remplace ${VAR} et ${VAR:-défaut}. Une variable absente sans défaut est une erreur.
func expandEnv(raw []byte, lookup func(string) (string, bool)) ([]byte, error) {
	var missing []string
	out := envRe.ReplaceAllFunc(raw, func(m []byte) []byte {
		sub := envRe.FindSubmatch(m)
		name := string(sub[1])
		if v, ok := lookup(name); ok {
			return []byte(v)
		}
		if len(sub[2]) > 0 {
			return sub[3]
		}
		missing = append(missing, name)
		return nil
	})
	if len(missing) > 0 {
		return nil, fmt.Errorf("variables d'environnement manquantes: %s", strings.Join(missing, ", "))
	}
	return out, nil
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
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		add("log_level %q invalide (debug, info, warn, error)", c.LogLevel)
	}
	errs = append(errs, c.validateNotify("notify", c.Notify)...)
	for _, name := range slices.Sorted(maps.Keys(c.Cameras)) {
		errs = append(errs, c.validateNotify("cameras."+name, c.Cameras[name])...)
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
```

- [ ] **Step 6: Lancer les tests**

Run: `go test ./internal/config/ -v`
Expected: PASS (tous les tests)

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum .gitignore internal/config
git commit -m "feat(config): chargement YAML, variables d'env, défauts et validation" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Payloads Frigate

**Files:**
- Create: `internal/frigate/types.go`
- Create: `internal/frigate/testdata/event_new.json`, `event_end.json`, `review_new.json`, `tracked_description.json`
- Test: `internal/frigate/types_test.go`

**Interfaces:**
- Produces:
  - `type SubLabel string` (accepte `null`, `"nom"`, `["nom", score]`)
  - `type Event struct { ID, Camera, Label string; SubLabel SubLabel; Score, TopScore float64; EnteredZones, CurrentZones []string; Stationary, FalsePositive, HasSnapshot, HasClip bool; StartTime float64; EndTime *float64 }`, `(Event).BestScore() float64`
  - `type EventMessage struct { Type string; Before *Event; After Event }`
  - `type ReviewData struct { Detections, Objects, SubLabels, Zones []string }`
  - `type Review struct { ID, Camera, Severity string; StartTime float64; EndTime *float64; Data ReviewData }`
  - `type ReviewMessage struct { Type string; Before *Review; After Review }`
  - `type TrackedObjectUpdate struct { Type, ID, Description string }`
  - `type APIEvent struct { ID, Camera, Label string; SubLabel SubLabel; StartTime float64; EndTime *float64; Zones []string; HasClip, HasSnapshot bool; TopScore *float64; Data struct{ TopScore float64 } }`, `(APIEvent).Score() float64`
  - `frigate.ParseEventMessage([]byte) (EventMessage, error)`, `frigate.ParseReviewMessage([]byte) (ReviewMessage, error)`, `frigate.ParseTrackedObjectUpdate([]byte) (TrackedObjectUpdate, error)`
  - `frigate.UnixTime(ts float64) time.Time`

- [ ] **Step 1: Créer les fixtures**

`internal/frigate/testdata/event_new.json` :

```json
{"type":"new","before":{"id":"1727520000.123456-abc123","camera":"jardin","frame_time":1727520000.12,"snapshot":null,"label":"person","sub_label":null,"top_score":0.0,"false_positive":true,"start_time":1727520000.12,"end_time":null,"score":0.72,"box":[1,2,3,4],"area":100,"ratio":0.5,"region":[0,0,320,320],"stationary":false,"motionless_count":0,"position_changes":0,"current_zones":[],"entered_zones":[],"has_clip":false,"has_snapshot":false,"attributes":{},"current_attributes":[]},"after":{"id":"1727520000.123456-abc123","camera":"jardin","frame_time":1727520001.5,"snapshot":{"frame_time":1727520001.5,"box":[1,2,3,4],"area":100,"region":[0,0,320,320],"score":0.87,"attributes":[]},"label":"person","sub_label":["Alice",0.93],"top_score":0.87,"false_positive":false,"start_time":1727520000.12,"end_time":null,"score":0.85,"box":[1,2,3,4],"area":100,"ratio":0.5,"region":[0,0,320,320],"stationary":false,"motionless_count":0,"position_changes":2,"current_zones":["allee"],"entered_zones":["allee"],"has_clip":true,"has_snapshot":true,"attributes":{},"current_attributes":[]}}
```

`internal/frigate/testdata/event_end.json` :

```json
{"type":"end","before":{"id":"1727520000.123456-abc123","camera":"jardin","label":"person","sub_label":null,"top_score":0.87,"false_positive":false,"start_time":1727520000.12,"end_time":null,"score":0.85,"stationary":false,"current_zones":["allee"],"entered_zones":["allee"],"has_clip":true,"has_snapshot":true},"after":{"id":"1727520000.123456-abc123","camera":"jardin","label":"person","sub_label":"Alice","top_score":0.87,"false_positive":false,"start_time":1727520000.12,"end_time":1727520030.0,"score":0.85,"stationary":false,"current_zones":[],"entered_zones":["allee"],"has_clip":true,"has_snapshot":true}}
```

`internal/frigate/testdata/review_new.json` :

```json
{"type":"new","before":{"id":"1727520000.2-xyz789","camera":"jardin","start_time":1727520000.2,"end_time":null,"severity":"alert","thumb_path":"/media/frigate/clips/review/thumb-jardin-1727520000.2-xyz789.webp","data":{"detections":["1727520000.123456-abc123"],"objects":["person"],"sub_labels":[],"zones":["allee"],"audio":[]}},"after":{"id":"1727520000.2-xyz789","camera":"jardin","start_time":1727520000.2,"end_time":null,"severity":"alert","thumb_path":"/media/frigate/clips/review/thumb-jardin-1727520000.2-xyz789.webp","data":{"detections":["1727520000.123456-abc123"],"objects":["person"],"sub_labels":[],"zones":["allee"],"audio":[]}}}
```

`internal/frigate/testdata/tracked_description.json` :

```json
{"type":"description","id":"1727520000.123456-abc123","description":"Une personne marche vers la porte d'entrée."}
```

- [ ] **Step 2: Écrire les tests (qui échouent)**

`internal/frigate/types_test.go` :

```go
package frigate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseEventNew(t *testing.T) {
	m, err := ParseEventMessage(fixture(t, "event_new.json"))
	if err != nil {
		t.Fatal(err)
	}
	e := m.After
	if m.Type != "new" || e.ID != "1727520000.123456-abc123" || e.Camera != "jardin" || e.Label != "person" {
		t.Errorf("champs de base incorrects : %+v", m)
	}
	if e.SubLabel != "Alice" {
		t.Errorf("sub_label = %q", e.SubLabel)
	}
	if e.BestScore() != 0.87 {
		t.Errorf("BestScore = %v", e.BestScore())
	}
	if !reflect.DeepEqual(e.EnteredZones, []string{"allee"}) || !e.HasSnapshot || e.EndTime != nil {
		t.Errorf("zones/snapshot/end incorrects : %+v", e)
	}
	if m.Before == nil || !m.Before.FalsePositive {
		t.Error("before non décodé")
	}
}

func TestParseEventEnd(t *testing.T) {
	m, err := ParseEventMessage(fixture(t, "event_end.json"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Type != "end" || m.After.EndTime == nil || *m.After.EndTime != 1727520030.0 {
		t.Errorf("end incorrect : %+v", m.After)
	}
	if m.After.SubLabel != "Alice" {
		t.Errorf("sub_label (format chaîne) = %q", m.After.SubLabel)
	}
}

func TestSubLabelFormats(t *testing.T) {
	cases := map[string]SubLabel{`null`: "", `"Bob"`: "Bob", `["Bob",0.9]`: "Bob", `[]`: ""}
	for raw, want := range cases {
		var s SubLabel
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			t.Errorf("%s : %v", raw, err)
		}
		if s != want {
			t.Errorf("%s : %q, attendu %q", raw, s, want)
		}
	}
}

func TestParseReview(t *testing.T) {
	m, err := ParseReviewMessage(fixture(t, "review_new.json"))
	if err != nil {
		t.Fatal(err)
	}
	r := m.After
	if m.Type != "new" || r.Severity != "alert" || r.Camera != "jardin" || r.StartTime != 1727520000.2 {
		t.Errorf("review incorrecte : %+v", r)
	}
	if !reflect.DeepEqual(r.Data.Detections, []string{"1727520000.123456-abc123"}) ||
		!reflect.DeepEqual(r.Data.Objects, []string{"person"}) ||
		!reflect.DeepEqual(r.Data.Zones, []string{"allee"}) {
		t.Errorf("data incorrecte : %+v", r.Data)
	}
}

func TestParseTrackedObjectUpdate(t *testing.T) {
	u, err := ParseTrackedObjectUpdate(fixture(t, "tracked_description.json"))
	if err != nil {
		t.Fatal(err)
	}
	if u.Type != "description" || u.ID != "1727520000.123456-abc123" || u.Description == "" {
		t.Errorf("update incorrecte : %+v", u)
	}
}

func TestParseRejectsMissingID(t *testing.T) {
	if _, err := ParseEventMessage([]byte(`{"type":"new","after":{}}`)); err == nil {
		t.Error("event sans id accepté")
	}
	if _, err := ParseReviewMessage([]byte(`{"type":"new","after":{}}`)); err == nil {
		t.Error("review sans id acceptée")
	}
	if _, err := ParseEventMessage([]byte(`pas du json`)); err == nil {
		t.Error("json invalide accepté")
	}
}

func TestAPIEventScore(t *testing.T) {
	var e APIEvent
	if err := json.Unmarshal([]byte(`{"id":"e1","top_score":null,"data":{"top_score":0.91}}`), &e); err != nil {
		t.Fatal(err)
	}
	if e.Score() != 0.91 {
		t.Errorf("Score = %v", e.Score())
	}
}

func TestUnixTime(t *testing.T) {
	if got := UnixTime(1727520000.5).UnixMilli(); got != 1727520000500 {
		t.Errorf("UnixTime = %d", got)
	}
}
```

- [ ] **Step 3: Vérifier l'échec**

Run: `go test ./internal/frigate/`
Expected: FAIL (`undefined: ParseEventMessage`…)

- [ ] **Step 4: Implémenter `internal/frigate/types.go`**

```go
// Package frigate décrit les messages MQTT de Frigate et fournit un client pour son API HTTP.
package frigate

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
)

// SubLabel accepte les formats de Frigate : null, "nom" ou ["nom", score].
type SubLabel string

func (s *SubLabel) UnmarshalJSON(b []byte) error {
	*s = ""
	if string(b) == "null" {
		return nil
	}
	var str string
	if err := json.Unmarshal(b, &str); err == nil {
		*s = SubLabel(str)
		return nil
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(b, &arr); err != nil {
		return fmt.Errorf("sub_label: format inattendu %s", b)
	}
	if len(arr) == 0 {
		return nil
	}
	if err := json.Unmarshal(arr[0], &str); err != nil {
		return fmt.Errorf("sub_label: format inattendu %s", b)
	}
	*s = SubLabel(str)
	return nil
}

// Event est l'objet suivi publié sur frigate/events.
type Event struct {
	ID            string   `json:"id"`
	Camera        string   `json:"camera"`
	Label         string   `json:"label"`
	SubLabel      SubLabel `json:"sub_label"`
	Score         float64  `json:"score"`
	TopScore      float64  `json:"top_score"`
	EnteredZones  []string `json:"entered_zones"`
	CurrentZones  []string `json:"current_zones"`
	Stationary    bool     `json:"stationary"`
	FalsePositive bool     `json:"false_positive"`
	HasSnapshot   bool     `json:"has_snapshot"`
	HasClip       bool     `json:"has_clip"`
	StartTime     float64  `json:"start_time"`
	EndTime       *float64 `json:"end_time"`
}

// BestScore renvoie le meilleur score connu de l'objet.
func (e Event) BestScore() float64 { return math.Max(e.Score, e.TopScore) }

type EventMessage struct {
	Type   string `json:"type"`
	Before *Event `json:"before"`
	After  Event  `json:"after"`
}

type ReviewData struct {
	Detections []string `json:"detections"`
	Objects    []string `json:"objects"`
	SubLabels  []string `json:"sub_labels"`
	Zones      []string `json:"zones"`
}

// Review est un élément de revue (alert ou detection) publié sur frigate/reviews.
type Review struct {
	ID        string     `json:"id"`
	Camera    string     `json:"camera"`
	Severity  string     `json:"severity"`
	StartTime float64    `json:"start_time"`
	EndTime   *float64   `json:"end_time"`
	Data      ReviewData `json:"data"`
}

type ReviewMessage struct {
	Type   string  `json:"type"`
	Before *Review `json:"before"`
	After  Review  `json:"after"`
}

// TrackedObjectUpdate est publié sur frigate/tracked_object_update (ex. description GenAI).
type TrackedObjectUpdate struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	Description string `json:"description"`
}

// APIEvent est un événement tel que renvoyé par GET /api/events.
type APIEvent struct {
	ID          string   `json:"id"`
	Camera      string   `json:"camera"`
	Label       string   `json:"label"`
	SubLabel    SubLabel `json:"sub_label"`
	StartTime   float64  `json:"start_time"`
	EndTime     *float64 `json:"end_time"`
	Zones       []string `json:"zones"`
	HasClip     bool     `json:"has_clip"`
	HasSnapshot bool     `json:"has_snapshot"`
	TopScore    *float64 `json:"top_score"`
	Data        struct {
		TopScore float64 `json:"top_score"`
	} `json:"data"`
}

// Score renvoie le meilleur score, quel que soit l'emplacement utilisé par la version de Frigate.
func (e APIEvent) Score() float64 {
	if e.TopScore != nil && *e.TopScore > 0 {
		return *e.TopScore
	}
	return e.Data.TopScore
}

var errNoID = errors.New("message frigate sans id")

func ParseEventMessage(b []byte) (EventMessage, error) {
	var m EventMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("event frigate illisible: %w", err)
	}
	if m.After.ID == "" {
		return m, errNoID
	}
	return m, nil
}

func ParseReviewMessage(b []byte) (ReviewMessage, error) {
	var m ReviewMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("review frigate illisible: %w", err)
	}
	if m.After.ID == "" {
		return m, errNoID
	}
	return m, nil
}

func ParseTrackedObjectUpdate(b []byte) (TrackedObjectUpdate, error) {
	var u TrackedObjectUpdate
	if err := json.Unmarshal(b, &u); err != nil {
		return u, fmt.Errorf("tracked_object_update illisible: %w", err)
	}
	if u.ID == "" {
		return u, errNoID
	}
	return u, nil
}

// UnixTime convertit un horodatage Frigate (secondes flottantes) en time.Time.
func UnixTime(ts float64) time.Time {
	sec, frac := math.Modf(ts)
	return time.Unix(int64(sec), int64(math.Round(frac*1e9)))
}
```

- [ ] **Step 5: Lancer les tests**

Run: `go test ./internal/frigate/ -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/frigate
git commit -m "feat(frigate): décodage des payloads MQTT events, reviews et tracked_object_update" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Client HTTP Frigate

**Files:**
- Create: `internal/frigate/paths.go`
- Create: `internal/frigate/client.go`
- Test: `internal/frigate/client_test.go`

**Interfaces:**
- Consumes: `config.Frigate` (Task 1), `APIEvent`, `Review` (Task 2)
- Produces:
  - `frigate.ErrTooLarge`, `type HTTPError struct { Status int; Path string }`, `frigate.Retryable(err error) bool`
  - `frigate.NewClient(cfg config.Frigate) (*Client, error)`
  - `(*Client).GetBytes(ctx, path string, max int64) ([]byte, error)`
  - `(*Client).DownloadToFile(ctx, path string, max int64) (string, error)` : l'appelant supprime le fichier
  - `(*Client).Cameras(ctx) ([]string, error)`, `(*Client).Events(ctx, camera string, limit int) ([]APIEvent, error)`, `(*Client).Review(ctx, id string) (Review, error)`
  - `EventSnapshotPath(id)`, `EventClipPath(id)`, `EventGIFPath(id)`, `ReviewGIFPath(id)`, `LatestPath(camera)`, `RecordingClipPath(camera string, start, end float64)`, qui renvoient tous des `string`

- [ ] **Step 1: Écrire les tests (qui échouent)**

`internal/frigate/client_test.go` :

```go
package frigate

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"frigate-telegram/internal/config"
)

func newTestClient(t *testing.T, url, user, pass string) *Client {
	t.Helper()
	c, err := NewClient(config.Frigate{URL: url, Username: user, Password: pass})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPaths(t *testing.T) {
	cases := map[string]string{
		EventSnapshotPath("a1"):                           "/api/events/a1/snapshot.jpg?bbox=1",
		EventClipPath("a1"):                               "/api/events/a1/clip.mp4",
		EventGIFPath("a1"):                                "/api/events/a1/preview.gif",
		ReviewGIFPath("r1"):                               "/api/review/r1/preview?format=gif",
		LatestPath("jardin"):                              "/api/jardin/latest.jpg",
		RecordingClipPath("jardin", 100.4, 130.2):         "/api/jardin/start/100/end/131/clip.mp4",
		EventClipPath("a b"):                              "/api/events/a%20b/clip.mp4",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("%q, attendu %q", got, want)
		}
	}
}

func TestGetBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/events/abc/snapshot.jpg" || r.URL.Query().Get("bbox") != "1" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte("jpeg"))
	}))
	defer srv.Close()
	b, err := newTestClient(t, srv.URL, "", "").GetBytes(context.Background(), EventSnapshotPath("abc"), 1024)
	if err != nil || string(b) != "jpeg" {
		t.Fatalf("GetBytes = %q, %v", b, err)
	}
}

func TestGetBytesTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, 100))
	}))
	defer srv.Close()
	_, err := newTestClient(t, srv.URL, "", "").GetBytes(context.Background(), "/x", 10)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, attendu ErrTooLarge", err)
	}
}

func TestHTTPErrorAndRetryable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	_, err := newTestClient(t, srv.URL, "", "").GetBytes(context.Background(), "/x", 10)
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != 404 {
		t.Fatalf("err = %v, attendu HTTPError 404", err)
	}
	cases := []struct {
		err  error
		want bool
	}{
		{&HTTPError{Status: 404}, true},
		{&HTTPError{Status: 503}, true},
		{&HTTPError{Status: 400}, false},
		{ErrTooLarge, false},
		{context.Canceled, false},
		{errors.New("connection refused"), true},
	}
	for _, tc := range cases {
		if got := Retryable(tc.err); got != tc.want {
			t.Errorf("Retryable(%v) = %v, attendu %v", tc.err, got, tc.want)
		}
	}
}

func TestLoginOnUnauthorized(t *testing.T) {
	var logins atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/login" {
			var body struct{ User, Password string }
			json.NewDecoder(r.Body).Decode(&body)
			if r.Method != http.MethodPost || body.User != "admin" || body.Password != "secret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			logins.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "frigate_token", Value: "tok", Path: "/"})
			return
		}
		if c, err := r.Cookie("frigate_token"); err != nil || c.Value != "tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL, "admin", "secret")
	for i := 0; i < 2; i++ {
		b, err := c.GetBytes(context.Background(), "/api/x", 10)
		if err != nil || string(b) != "ok" {
			t.Fatalf("appel %d : %q, %v", i, b, err)
		}
	}
	if logins.Load() != 1 {
		t.Errorf("logins = %d, attendu 1", logins.Load())
	}
}

func TestDownloadToFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("mp4data"))
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL, "", "")
	path, err := c.DownloadToFile(context.Background(), "/clip.mp4", 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	b, _ := os.ReadFile(path)
	if string(b) != "mp4data" {
		t.Errorf("contenu = %q", b)
	}
	if _, err := c.DownloadToFile(context.Background(), "/clip.mp4", 3); !errors.Is(err, ErrTooLarge) {
		t.Errorf("err = %v, attendu ErrTooLarge", err)
	}
}

func TestCamerasEventsReview(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/config":
			w.Write([]byte(`{"cameras":{"jardin":{},"garage":{}}}`))
		case "/api/events":
			if r.URL.Query().Get("cameras") != "jardin" || r.URL.Query().Get("limit") != "1" {
				http.Error(w, "bad query", 400)
				return
			}
			w.Write([]byte(`[{"id":"e1","camera":"jardin","label":"person","sub_label":null,"start_time":1.5,"end_time":3.0,"zones":["allee"],"has_clip":true,"has_snapshot":true,"top_score":null,"data":{"top_score":0.91}}]`))
		case "/api/review/r1":
			w.Write([]byte(`{"id":"r1","camera":"jardin","severity":"alert","start_time":10.0,"end_time":20.0,"data":{"detections":["e1"],"objects":["person"],"zones":[]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL, "", "")
	ctx := context.Background()

	cams, err := c.Cameras(ctx)
	if err != nil || len(cams) != 2 || cams[0] != "garage" || cams[1] != "jardin" {
		t.Errorf("Cameras = %v, %v", cams, err)
	}
	evs, err := c.Events(ctx, "jardin", 1)
	if err != nil || len(evs) != 1 || evs[0].ID != "e1" || evs[0].Score() != 0.91 {
		t.Errorf("Events = %+v, %v", evs, err)
	}
	rv, err := c.Review(ctx, "r1")
	if err != nil || rv.Camera != "jardin" || rv.EndTime == nil || *rv.EndTime != 20.0 {
		t.Errorf("Review = %+v, %v", rv, err)
	}
}
```

- [ ] **Step 2: Vérifier l'échec**

Run: `go test ./internal/frigate/`
Expected: FAIL (`undefined: NewClient`…)

- [ ] **Step 3: Implémenter `internal/frigate/paths.go`**

```go
package frigate

import (
	"fmt"
	"math"
	"net/url"
)

func EventSnapshotPath(id string) string {
	return "/api/events/" + url.PathEscape(id) + "/snapshot.jpg?bbox=1"
}

func EventClipPath(id string) string { return "/api/events/" + url.PathEscape(id) + "/clip.mp4" }

func EventGIFPath(id string) string { return "/api/events/" + url.PathEscape(id) + "/preview.gif" }

func ReviewGIFPath(id string) string {
	return "/api/review/" + url.PathEscape(id) + "/preview?format=gif"
}

func LatestPath(camera string) string { return "/api/" + url.PathEscape(camera) + "/latest.jpg" }

// RecordingClipPath renvoie le clip des enregistrements d'une caméra entre deux instants.
func RecordingClipPath(camera string, start, end float64) string {
	return fmt.Sprintf("/api/%s/start/%d/end/%d/clip.mp4",
		url.PathEscape(camera), int64(math.Floor(start)), int64(math.Ceil(end)))
}
```

- [ ] **Step 4: Implémenter `internal/frigate/client.go`**

```go
package frigate

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	"frigate-telegram/internal/config"
)

// ErrTooLarge signale un média plus gros que la limite demandée.
var ErrTooLarge = errors.New("média trop volumineux")

// HTTPError est une réponse non-200 de Frigate.
type HTTPError struct {
	Status int
	Path   string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("frigate %s: HTTP %d", e.Path, e.Status) }

// Retryable indique si réessayer a une chance d'aboutir (média pas encore prêt, serveur ou réseau en difficulté).
func Retryable(err error) bool {
	if errors.Is(err, ErrTooLarge) || errors.Is(err, context.Canceled) {
		return false
	}
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status == http.StatusNotFound || he.Status >= 500
	}
	return true
}

// Client appelle l'API HTTP de Frigate, avec authentification optionnelle.
type Client struct {
	base       string
	user, pass string
	http       *http.Client
	loginMu    sync.Mutex
}

func NewClient(cfg config.Frigate) (*Client, error) {
	if _, err := url.Parse(cfg.URL); err != nil {
		return nil, fmt.Errorf("frigate.url invalide: %w", err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = 8
	if cfg.InsecureSkipVerify {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // option explicite de l'utilisateur
	}
	return &Client{
		base: cfg.URL,
		user: cfg.Username,
		pass: cfg.Password,
		http: &http.Client{Transport: tr, Jar: jar},
	}, nil
}

func (c *Client) get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	return c.http.Do(req)
}

// do exécute un GET ; sur 401 avec identifiants, se reconnecte puis réessaie une fois.
func (c *Client) do(ctx context.Context, path string) (*http.Response, error) {
	resp, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized && c.user != "" {
		resp.Body.Close()
		if err := c.login(ctx); err != nil {
			return nil, err
		}
		if resp, err = c.get(ctx, path); err != nil {
			return nil, err
		}
	}
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, &HTTPError{Status: resp.StatusCode, Path: path}
	}
	return resp, nil
}

func (c *Client) login(ctx context.Context) error {
	c.loginMu.Lock()
	defer c.loginMu.Unlock()
	body, _ := json.Marshal(map[string]string{"user": c.user, "password": c.pass})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/login", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("login frigate: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("login frigate refusé: HTTP %d", resp.StatusCode)
	}
	return nil
}

// GetBytes télécharge une ressource en mémoire (images), en refusant au-delà de max octets.
func (c *Client) GetBytes(ctx context.Context, path string, max int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resp, err := c.do(ctx, path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, ErrTooLarge
	}
	return b, nil
}

// DownloadToFile écrit une ressource dans un fichier temporaire (clips) et renvoie son chemin.
// L'appelant doit supprimer le fichier.
func (c *Client) DownloadToFile(ctx context.Context, path string, max int64) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	resp, err := c.do(ctx, path)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.ContentLength > max {
		return "", ErrTooLarge
	}
	f, err := os.CreateTemp("", "frigate-*")
	if err != nil {
		return "", err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, max+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > max {
		err = ErrTooLarge
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resp, err := c.do(ctx, path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(out)
}

// Cameras renvoie les noms des caméras déclarées dans Frigate, triés.
func (c *Client) Cameras(ctx context.Context) ([]string, error) {
	var cfg struct {
		Cameras map[string]json.RawMessage `json:"cameras"`
	}
	if err := c.getJSON(ctx, "/api/config", &cfg); err != nil {
		return nil, err
	}
	return slices.Sorted(maps.Keys(cfg.Cameras)), nil
}

// Events renvoie les derniers événements, éventuellement d'une seule caméra.
func (c *Client) Events(ctx context.Context, camera string, limit int) ([]APIEvent, error) {
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if camera != "" {
		q.Set("cameras", camera) // Frigate ≥ 0.14
		q.Set("camera", camera)  // versions antérieures
	}
	var evs []APIEvent
	err := c.getJSON(ctx, "/api/events?"+q.Encode(), &evs)
	return evs, err
}

// Review renvoie un élément de revue par son id.
func (c *Client) Review(ctx context.Context, id string) (Review, error) {
	var r Review
	err := c.getJSON(ctx, "/api/review/"+url.PathEscape(id), &r)
	return r, err
}
```

- [ ] **Step 5: Lancer les tests**

Run: `go test ./internal/frigate/ -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/frigate
git commit -m "feat(frigate): client HTTP avec auth JWT, limites de taille et fichiers temporaires" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: État persistant

**Files:**
- Create: `internal/state/state.go`
- Test: `internal/state/state_test.go`

**Interfaces:**
- Produces:
  - `state.Forever` (`time.Time`, pause sans échéance)
  - `state.Load(path string, now time.Time) (*Store, error)` : renvoie **toujours** un store utilisable. Une erreur non nil n'est qu'un avertissement (fichier corrompu ou illisible).
  - `(*Store).IsPaused(now) bool`, `IsMuted(camera string, now) bool`, `LastNotified(key string) time.Time`, `MarkNotified(key string, at time.Time)`
  - `(*Store).Pause(until time.Time) error`, `Resume() error` (lève la pause **et** toutes les coupures), `Mute(camera string, until time.Time) error`, `Unmute(camera string) error`
  - `type Status struct { PausedUntil time.Time; Mutes map[string]time.Time }`, `(*Store).Status(now) Status`
  - `(*Store).Save() error`, `(*Store).FlushIfDirty() error`

- [ ] **Step 1: Écrire les tests (qui échouent)**

`internal/state/state_test.go` :

```go
package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sub", "state.json")
	s, err := Load(path, t0)
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestPauseAndResume(t *testing.T) {
	s, _ := newStore(t)
	if s.IsPaused(t0) {
		t.Fatal("pause au démarrage")
	}
	if err := s.Pause(t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !s.IsPaused(t0.Add(30*time.Minute)) || s.IsPaused(t0.Add(2*time.Hour)) {
		t.Error("fenêtre de pause incorrecte")
	}
	s.Mute("jardin", Forever)
	if err := s.Resume(); err != nil {
		t.Fatal(err)
	}
	if s.IsPaused(t0) || s.IsMuted("jardin", t0) {
		t.Error("Resume doit tout lever")
	}
}

func TestMuteIsPerCamera(t *testing.T) {
	s, _ := newStore(t)
	s.Mute("jardin", t0.Add(time.Hour))
	if !s.IsMuted("jardin", t0) || s.IsMuted("garage", t0) {
		t.Error("coupure mal ciblée")
	}
	s.Unmute("jardin")
	if s.IsMuted("jardin", t0) {
		t.Error("Unmute sans effet")
	}
}

func TestPersistenceRoundTrip(t *testing.T) {
	s, path := newStore(t)
	s.Pause(t0.Add(time.Hour))
	s.Mute("jardin", t0.Add(10*time.Minute))
	s.Mute("garage", t0.Add(3*time.Hour))
	s.MarkNotified("jardin/person", t0)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	later := t0.Add(30 * time.Minute)
	s2, err := Load(path, later)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.IsPaused(later) || !s2.IsMuted("garage", later) {
		t.Error("état non restauré")
	}
	if _, ok := s2.Status(later).Mutes["jardin"]; ok {
		t.Error("coupure expirée non purgée")
	}
	if !s2.LastNotified("jardin/person").Equal(t0) {
		t.Error("cooldown non restauré")
	}
}

func TestForeverSurvivesRoundTrip(t *testing.T) {
	s, path := newStore(t)
	s.Pause(Forever)
	s2, _ := Load(path, t0.AddDate(5, 0, 0))
	if !s2.IsPaused(t0.AddDate(5, 0, 0)) {
		t.Error("pause illimitée perdue")
	}
}

func TestCorruptFileGivesEmptyState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	os.WriteFile(path, []byte("{"), 0o600)
	s, err := Load(path, t0)
	if err == nil {
		t.Error("avertissement attendu")
	}
	if s == nil || s.IsPaused(t0) {
		t.Fatal("un état vide utilisable est attendu")
	}
}

func TestFlushIfDirty(t *testing.T) {
	s, path := newStore(t)
	if err := s.FlushIfDirty(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("aucune écriture attendue sans modification")
	}
	s.MarkNotified("jardin/person", t0)
	if err := s.FlushIfDirty(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("fichier attendu : %v", err)
	}
}

func TestStatus(t *testing.T) {
	s, _ := newStore(t)
	if st := s.Status(t0); !st.PausedUntil.IsZero() || len(st.Mutes) != 0 {
		t.Errorf("status vide attendu : %+v", st)
	}
	s.Pause(t0.Add(time.Hour))
	s.Mute("jardin", t0.Add(time.Hour))
	st := s.Status(t0)
	if !st.PausedUntil.Equal(t0.Add(time.Hour)) || len(st.Mutes) != 1 {
		t.Errorf("status = %+v", st)
	}
}
```

- [ ] **Step 2: Vérifier l'échec**

Run: `go test ./internal/state/`
Expected: FAIL (`undefined: Load`)

- [ ] **Step 3: Implémenter `internal/state/state.go`**

```go
// Package state conserve les pauses, coupures de caméras et cooldowns, avec persistance JSON.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Forever représente une pause sans échéance (jusqu'à /resume).
var Forever = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

const cooldownRetention = 24 * time.Hour

type Store struct {
	mu        sync.Mutex
	path      string
	pause     time.Time
	mutes     map[string]time.Time
	cooldowns map[string]time.Time
	dirty     bool
}

type fileData struct {
	GlobalPauseUntil time.Time            `json:"global_pause_until"`
	CameraMutes      map[string]time.Time `json:"camera_mutes"`
	Cooldowns        map[string]time.Time `json:"cooldowns"`
}

// Load lit l'état depuis path. Le store renvoyé est toujours utilisable ; une erreur
// non nil signale seulement un fichier illisible, ignoré (à journaliser en avertissement).
func Load(path string, now time.Time) (*Store, error) {
	s := &Store{path: path, mutes: map[string]time.Time{}, cooldowns: map[string]time.Time{}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("lecture de l'état: %w", err)
	}
	var f fileData
	if err := json.Unmarshal(raw, &f); err != nil {
		return s, fmt.Errorf("état corrompu, ignoré: %w", err)
	}
	if f.GlobalPauseUntil.After(now) {
		s.pause = f.GlobalPauseUntil
	}
	for cam, until := range f.CameraMutes {
		if until.After(now) {
			s.mutes[cam] = until
		}
	}
	for key, at := range f.Cooldowns {
		if now.Sub(at) < cooldownRetention {
			s.cooldowns[key] = at
		}
	}
	return s, nil
}

func (s *Store) IsPaused(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return now.Before(s.pause)
}

func (s *Store) IsMuted(camera string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	until, ok := s.mutes[camera]
	return ok && now.Before(until)
}

func (s *Store) LastNotified(key string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cooldowns[key]
}

// MarkNotified enregistre une notification pour le cooldown ; écrit sur disque au prochain FlushIfDirty.
func (s *Store) MarkNotified(key string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cooldowns[key] = at
	s.dirty = true
}

func (s *Store) Pause(until time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pause = until
	return s.saveLocked()
}

// Resume lève la pause globale et toutes les coupures de caméras.
func (s *Store) Resume() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pause = time.Time{}
	s.mutes = map[string]time.Time{}
	return s.saveLocked()
}

func (s *Store) Mute(camera string, until time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mutes[camera] = until
	return s.saveLocked()
}

func (s *Store) Unmute(camera string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.mutes, camera)
	return s.saveLocked()
}

type Status struct {
	PausedUntil time.Time            // zéro si pas de pause active
	Mutes       map[string]time.Time // coupures actives uniquement
}

func (s *Store) Status(now time.Time) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{Mutes: map[string]time.Time{}}
	if now.Before(s.pause) {
		st.PausedUntil = s.pause
	}
	for cam, until := range s.mutes {
		if now.Before(until) {
			st.Mutes[cam] = until
		}
	}
	return st
}

func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

// FlushIfDirty écrit l'état seulement si des cooldowns ont changé depuis la dernière écriture.
func (s *Store) FlushIfDirty() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	return s.saveLocked()
}

// saveLocked écrit atomiquement (fichier temporaire puis rename).
func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(fileData{
		GlobalPauseUntil: s.pause,
		CameraMutes:      maps.Clone(s.mutes),
		Cooldowns:        maps.Clone(s.cooldowns),
	}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("écriture de l'état: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("écriture de l'état: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("écriture de l'état: %w", err)
	}
	s.dirty = false
	return nil
}
```

- [ ] **Step 4: Lancer les tests**

Run: `go test ./internal/state/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/state
git commit -m "feat(state): pauses, coupures et cooldowns persistés en JSON" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Moteur de filtre

**Files:**
- Create: `internal/filter/filter.go`
- Test: `internal/filter/filter_test.go`

**Interfaces:**
- Consumes: `config.Config`, `ForCamera`, `MinScore.For`, `config.InRanges` (Task 1). Le `*state.Store` (Task 4) satisfait `filter.State`.
- Produces:
  - `type State interface { IsPaused(now time.Time) bool; IsMuted(camera string, now time.Time) bool; LastNotified(key string) time.Time }`
  - `type Input struct { Camera string; Labels []string; Score float64; HasScore bool; Zones []string; Severity string; Stationary, FalsePositive bool }`
  - `type Decision struct { Notify, Silent bool; Label string; Chats []string; Reason string }`
  - `filter.New(cfg *config.Config, st State) *Engine`, `(*Engine).Evaluate(in Input, now time.Time) Decision`
  - `filter.CooldownKey(camera, label string) string` (renvoie `camera + "/" + label`)
  - Constantes `Reason*` : `camera_disabled`, `false_positive`, `stationary`, `severity`, `label`, `score`, `zone`, `paused`, `muted`, `off_hours`, `cooldown`

- [ ] **Step 1: Écrire les tests (qui échouent)**

`internal/filter/filter_test.go` :

```go
package filter

import (
	"reflect"
	"testing"
	"time"

	"frigate-telegram/internal/config"
)

type fakeState struct {
	paused bool
	muted  map[string]bool
	last   map[string]time.Time
}

func (f *fakeState) IsPaused(time.Time) bool             { return f.paused }
func (f *fakeState) IsMuted(c string, _ time.Time) bool  { return f.muted[c] }
func (f *fakeState) LastNotified(k string) time.Time     { return f.last[k] }

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
```

- [ ] **Step 2: Vérifier l'échec**

Run: `go test ./internal/filter/`
Expected: FAIL (`undefined: New`)

- [ ] **Step 3: Implémenter `internal/filter/filter.go`**

```go
// Package filter décide, sans aucune entrée/sortie, si une détection doit être notifiée.
package filter

import (
	"slices"
	"time"

	"frigate-telegram/internal/config"
)

const (
	ReasonCameraDisabled = "camera_disabled"
	ReasonFalsePositive  = "false_positive"
	ReasonStationary     = "stationary"
	ReasonSeverity       = "severity"
	ReasonLabel          = "label"
	ReasonScore          = "score"
	ReasonZone           = "zone"
	ReasonPaused         = "paused"
	ReasonMuted          = "muted"
	ReasonOffHours       = "off_hours"
	ReasonCooldown       = "cooldown"
)

// State est la vue en lecture de l'état (pauses, coupures, cooldowns).
type State interface {
	IsPaused(now time.Time) bool
	IsMuted(camera string, now time.Time) bool
	LastNotified(key string) time.Time
}

// Input décrit une détection. Labels contient un label (events) ou plusieurs objets (reviews).
type Input struct {
	Camera        string
	Labels        []string
	Score         float64
	HasScore      bool
	Zones         []string
	Severity      string
	Stationary    bool
	FalsePositive bool
}

type Decision struct {
	Notify bool
	Silent bool     // notification sans son (quiet_hours)
	Label  string   // label retenu, pour la légende et le cooldown
	Chats  []string // noms des chats destinataires
	Reason string   // raison du refus quand Notify est faux
}

type Engine struct {
	cfg   *config.Config
	state State
}

func New(cfg *config.Config, st State) *Engine { return &Engine{cfg: cfg, state: st} }

func CooldownKey(camera, label string) string { return camera + "/" + label }

// Evaluate applique les règles dans l'ordre de la spec ; la première qui échoue donne Reason.
func (e *Engine) Evaluate(in Input, now time.Time) Decision {
	n := e.cfg.ForCamera(in.Camera)
	reject := func(reason string) Decision { return Decision{Reason: reason} }

	switch {
	case !n.Enabled:
		return reject(ReasonCameraDisabled)
	case in.FalsePositive:
		return reject(ReasonFalsePositive)
	case in.Stationary && n.IgnoreStationary:
		return reject(ReasonStationary)
	case in.Severity != "" && !slices.Contains(n.Severity, in.Severity):
		return reject(ReasonSeverity)
	}
	label := matchLabel(n.Labels, in.Labels)
	if label == "" {
		return reject(ReasonLabel)
	}
	if in.HasScore && in.Score < n.MinScore.For(label) {
		return reject(ReasonScore)
	}
	if len(n.Zones) > 0 && !slices.ContainsFunc(in.Zones, func(z string) bool { return slices.Contains(n.Zones, z) }) {
		return reject(ReasonZone)
	}
	if e.state.IsPaused(now) {
		return reject(ReasonPaused)
	}
	if e.state.IsMuted(in.Camera, now) {
		return reject(ReasonMuted)
	}
	local := now.In(e.cfg.Location)
	if config.InRanges(n.OffHours, local) {
		return reject(ReasonOffHours)
	}
	if n.Cooldown > 0 {
		last := e.state.LastNotified(CooldownKey(in.Camera, label))
		if !last.IsZero() && now.Sub(last) < n.Cooldown {
			return reject(ReasonCooldown)
		}
	}
	return Decision{
		Notify: true,
		Silent: config.InRanges(n.QuietHours, local),
		Label:  label,
		Chats:  n.Chats,
	}
}

// matchLabel renvoie le premier label accepté (tous si la liste autorisée est vide).
func matchLabel(allowed, labels []string) string {
	for _, l := range labels {
		if len(allowed) == 0 || slices.Contains(allowed, l) {
			return l
		}
	}
	return ""
}
```

- [ ] **Step 4: Lancer les tests**

Run: `go test ./internal/filter/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/filter
git commit -m "feat(filter): moteur de décision (caméra, label, score, zone, pause, horaires, cooldown)" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Limiteur de débit Telegram

**Files:**
- Create: `internal/telegram/limiter.go`
- Test: `internal/telegram/limiter_test.go`

**Interfaces:**
- Produces:
  - `telegram.NewLimiter() *Limiter` (1/30 s au total, 1 s par chat privé, 3 s par groupe, c'est-à-dire un ID négatif)
  - `telegram.NewLimiterWith(global, private, group time.Duration) *Limiter`
  - `(*Limiter).Wait(ctx context.Context, chatID int64) error`
  - fonction interne `sleep(ctx context.Context, d time.Duration) error` (réutilisée par le client en Task 7)

- [ ] **Step 1: Écrire les tests (qui échouent)**

`internal/telegram/limiter_test.go` :

```go
package telegram

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLimiterSpacesMessagesPerChat(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewLimiterWith(0, time.Second, 3*time.Second)
	l.now = func() time.Time { return now }
	steps := []struct {
		chat int64
		want time.Duration
	}{
		{42, 0}, {42, time.Second}, {7, 0}, {-100, 0}, {-100, 3 * time.Second},
	}
	for i, s := range steps {
		if got := l.reserveChat(s.chat); got != s.want {
			t.Errorf("étape %d (chat %d) : %v, attendu %v", i, s.chat, got, s.want)
		}
	}
}

func TestLimiterGlobal(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewLimiterWith(100*time.Millisecond, 0, 0)
	l.now = func() time.Time { return now }
	for i, want := range []time.Duration{0, 100 * time.Millisecond, 200 * time.Millisecond} {
		if got := l.reserveGlobal(); got != want {
			t.Errorf("réservation %d : %v, attendu %v", i, got, want)
		}
	}
}

func TestWaitHonoursContext(t *testing.T) {
	l := NewLimiterWith(0, time.Hour, time.Hour)
	if err := l.Wait(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.Wait(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, attendu context.Canceled", err)
	}
}
```

- [ ] **Step 2: Vérifier l'échec**

Run: `go test ./internal/telegram/`
Expected: FAIL (`undefined: NewLimiterWith`)

- [ ] **Step 3: Implémenter `internal/telegram/limiter.go`**

```go
// Package telegram est un client minimal de l'API Bot Telegram.
package telegram

import (
	"context"
	"sync"
	"time"
)

// Limiter espace les envois pour respecter les limites de Telegram :
// environ 30 messages/s au total, 1/s par chat privé, 20/min par groupe.
// Chaque chat a son propre créneau : un chat lent ne retarde pas les autres.
type Limiter struct {
	mu                     sync.Mutex
	global, private, group time.Duration
	nextGlobal             time.Time
	nextChat               map[int64]time.Time
	now                    func() time.Time
}

func NewLimiter() *Limiter { return NewLimiterWith(time.Second/30, time.Second, 3*time.Second) }

func NewLimiterWith(global, private, group time.Duration) *Limiter {
	return &Limiter{global: global, private: private, group: group, nextChat: map[int64]time.Time{}, now: time.Now}
}

// Wait bloque jusqu'à ce qu'un envoi vers chatID soit autorisé.
func (l *Limiter) Wait(ctx context.Context, chatID int64) error {
	if err := sleep(ctx, l.reserveChat(chatID)); err != nil {
		return err
	}
	return sleep(ctx, l.reserveGlobal())
}

func (l *Limiter) reserveChat(chatID int64) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	at := now
	if next := l.nextChat[chatID]; next.After(at) {
		at = next
	}
	interval := l.private
	if chatID < 0 {
		interval = l.group
	}
	l.nextChat[chatID] = at.Add(interval)
	return at.Sub(now)
}

func (l *Limiter) reserveGlobal() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	at := now
	if l.nextGlobal.After(at) {
		at = l.nextGlobal
	}
	l.nextGlobal = at.Add(l.global)
	return at.Sub(now)
}

// sleep attend d ou l'annulation de ctx.
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
```

- [ ] **Step 4: Lancer les tests**

Run: `go test ./internal/telegram/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/telegram
git commit -m "feat(telegram): limiteur de débit par chat et global" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Client Bot API Telegram

**Files:**
- Create: `internal/telegram/types.go`
- Create: `internal/telegram/client.go`
- Test: `internal/telegram/client_test.go`

**Interfaces:**
- Consumes: `Limiter`, `sleep` (Task 6)
- Produces:
  - Types : `User{ID int64; Username, FirstName string}`, `Chat{ID int64; Type string}`, `PhotoSize{FileID string}`, `Video{FileID string}`, `Animation{FileID string}`, `Message{MessageID int; From *User; Chat Chat; Text string; Photo []PhotoSize; Video *Video; Animation *Animation}`, `(Message).FileID() string`, `CallbackQuery{ID string; From User; Message *Message; Data string}`, `Update{UpdateID int; Message *Message; CallbackQuery *CallbackQuery}`, `InlineKeyboardButton{Text, CallbackData, URL string}`, `InlineKeyboardMarkup{InlineKeyboard [][]InlineKeyboardButton}`, `BotCommand{Command, Description string}`
  - `InputFile{FileID, Name string; Data []byte; Path string}`
  - `SendOptions{Caption string; Silent bool; ReplyTo int; Markup *InlineKeyboardMarkup}`
  - `type APIError struct { Method string; Code int; Description string; RetryAfter time.Duration }`
  - `telegram.New(token string, opts ...Option) *Client` ; options `WithBaseURL(string)`, `WithBackoff(time.Duration)`, `WithLimiter(*Limiter)`, `WithErrorHook(func(method string, code int))`
  - Méthodes (`ctx context.Context` en premier) :
    - `SendMessage(ctx, chatID int64, text string, o SendOptions) (Message, error)`
    - `SendPhoto / SendVideo / SendAnimation(ctx, chatID int64, f InputFile, o SendOptions) (Message, error)`
    - `EditMessageCaption(ctx, chatID int64, messageID int, caption string, markup *InlineKeyboardMarkup) error`
    - `EditMessageText(ctx, chatID int64, messageID int, text string, markup *InlineKeyboardMarkup) error`
    - `AnswerCallbackQuery(ctx, id, text string) error`
    - `GetUpdates(ctx, offset int, timeout time.Duration) ([]Update, error)`
    - `SetMyCommands(ctx, cmds []BotCommand) error`

- [ ] **Step 1: Écrire les tests (qui échouent)**

`internal/telegram/client_test.go` :

```go
package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const token = "123:SECRET"

func testClient(url string, opts ...Option) *Client {
	base := []Option{WithBaseURL(url), WithBackoff(time.Millisecond), WithLimiter(NewLimiterWith(0, 0, 0))}
	return New(token, append(base, opts...)...)
}

func okResult(w http.ResponseWriter, result string) {
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, `{"ok":true,"result":`+result+`}`)
}

func TestSendPhotoMultipart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot"+token+"/sendPhoto" {
			t.Errorf("chemin = %s", r.URL.Path)
		}
		if r.ContentLength <= 0 {
			t.Error("Content-Length attendu (pas de chunked)")
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		checks := map[string]string{"chat_id": "42", "caption": "<b>hi</b>", "parse_mode": "HTML", "disable_notification": "true"}
		for k, want := range checks {
			if got := r.FormValue(k); got != want {
				t.Errorf("%s = %q, attendu %q", k, got, want)
			}
		}
		var markup InlineKeyboardMarkup
		if err := json.Unmarshal([]byte(r.FormValue("reply_markup")), &markup); err != nil || markup.InlineKeyboard[0][0].CallbackData != "p:1800" {
			t.Errorf("reply_markup = %q", r.FormValue("reply_markup"))
		}
		if !strings.Contains(r.FormValue("reply_parameters"), `"message_id":5`) {
			t.Errorf("reply_parameters = %q", r.FormValue("reply_parameters"))
		}
		f, h, err := r.FormFile("photo")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(f)
		if h.Filename != "snapshot.jpg" || string(b) != "jpegdata" {
			t.Errorf("fichier = %s %q", h.Filename, b)
		}
		okResult(w, `{"message_id":7,"chat":{"id":42},"photo":[{"file_id":"small"},{"file_id":"big"}]}`)
	}))
	defer srv.Close()

	m, err := testClient(srv.URL).SendPhoto(context.Background(), 42,
		InputFile{Name: "snapshot.jpg", Data: []byte("jpegdata")},
		SendOptions{Caption: "<b>hi</b>", Silent: true, ReplyTo: 5,
			Markup: &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{{{Text: "x", CallbackData: "p:1800"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if m.MessageID != 7 || m.FileID() != "big" {
		t.Errorf("message = %+v, FileID = %q", m, m.FileID())
	}
}

func TestSendVideoFromPathThenFileID(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if r.FormValue("supports_streaming") != "true" {
			t.Error("supports_streaming manquant")
		}
		if n == 1 {
			f, _, err := r.FormFile("video")
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(f)
			if string(b) != "mp4data" {
				t.Errorf("contenu = %q", b)
			}
		} else if r.FormValue("video") != "vid-1" {
			t.Errorf("file_id = %q", r.FormValue("video"))
		}
		okResult(w, `{"message_id":8,"chat":{"id":1},"video":{"file_id":"vid-1"}}`)
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "clip.mp4")
	os.WriteFile(path, []byte("mp4data"), 0o600)

	c := testClient(srv.URL)
	m, err := c.SendVideo(context.Background(), 1, InputFile{Name: "clip.mp4", Path: path}, SendOptions{})
	if err != nil || m.FileID() != "vid-1" {
		t.Fatalf("upload : %+v, %v", m, err)
	}
	if _, err := c.SendVideo(context.Background(), 2, InputFile{FileID: m.FileID()}, SendOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestRetryOn429(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":1}}`)
			return
		}
		okResult(w, `{"message_id":1,"chat":{"id":1}}`)
	}))
	defer srv.Close()
	start := time.Now()
	if _, err := testClient(srv.URL).SendMessage(context.Background(), 1, "hi", SendOptions{}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || time.Since(start) < time.Second {
		t.Errorf("appels = %d, durée = %v : retry_after non respecté", calls.Load(), time.Since(start))
	}
}

func TestRetryOn5xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusBadGateway)
			io.WriteString(w, `<html>bad gateway</html>`)
			return
		}
		okResult(w, `{"message_id":1,"chat":{"id":1}}`)
	}))
	defer srv.Close()
	if _, err := testClient(srv.URL).SendMessage(context.Background(), 1, "hi", SendOptions{}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Errorf("appels = %d, attendu 3", calls.Load())
	}
}

func TestNoRetryOn400AndHook(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`)
	}))
	defer srv.Close()
	var hookMethod string
	var hookCode int
	c := testClient(srv.URL, WithErrorHook(func(m string, code int) { hookMethod, hookCode = m, code }))
	_, err := c.SendMessage(context.Background(), 1, "hi", SendOptions{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != 400 {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 1 || hookMethod != "sendMessage" || hookCode != 400 {
		t.Errorf("appels=%d hook=%s/%d", calls.Load(), hookMethod, hookCode)
	}
}

func TestErrorsNeverLeakToken(t *testing.T) {
	c := testClient("http://127.0.0.1:1")
	_, err := c.SendMessage(context.Background(), 1, "hi", SendOptions{})
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("err = %v (le token ne doit pas apparaître)", err)
	}
}

func TestGetUpdates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("offset") != "10" || r.FormValue("timeout") != "50" {
			t.Errorf("params = %v", r.Form)
		}
		okResult(w, `[{"update_id":10,"message":{"message_id":1,"from":{"id":5},"chat":{"id":5},"text":"/status"}},{"update_id":11,"callback_query":{"id":"q","from":{"id":5},"data":"p:1800","message":{"message_id":2,"chat":{"id":5}}}}]`)
	}))
	defer srv.Close()
	ups, err := testClient(srv.URL).GetUpdates(context.Background(), 10, 50*time.Second)
	if err != nil || len(ups) != 2 {
		t.Fatalf("ups = %+v, %v", ups, err)
	}
	if ups[0].Message.Text != "/status" || ups[1].CallbackQuery.Data != "p:1800" || ups[1].CallbackQuery.Message.Chat.ID != 5 {
		t.Errorf("décodage incorrect : %+v", ups)
	}
}
```

- [ ] **Step 2: Vérifier l'échec**

Run: `go test ./internal/telegram/`
Expected: FAIL (`undefined: New`, `undefined: InputFile`…)

- [ ] **Step 3: Implémenter `internal/telegram/types.go`**

```go
package telegram

type User struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type PhotoSize struct {
	FileID string `json:"file_id"`
}

type Video struct {
	FileID string `json:"file_id"`
}

type Animation struct {
	FileID string `json:"file_id"`
}

type Message struct {
	MessageID int         `json:"message_id"`
	From      *User       `json:"from"`
	Chat      Chat        `json:"chat"`
	Text      string      `json:"text"`
	Photo     []PhotoSize `json:"photo"`
	Video     *Video      `json:"video"`
	Animation *Animation  `json:"animation"`
}

// FileID renvoie l'identifiant du média envoyé, réutilisable vers un autre chat sans nouvel upload.
func (m Message) FileID() string {
	switch {
	case m.Video != nil:
		return m.Video.FileID
	case m.Animation != nil:
		return m.Animation.FileID
	case len(m.Photo) > 0:
		return m.Photo[len(m.Photo)-1].FileID
	}
	return ""
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

type Update struct {
	UpdateID      int            `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// InputFile désigne un média : FileID (déjà sur Telegram), Data (en mémoire) ou Path (sur disque).
type InputFile struct {
	FileID string
	Name   string
	Data   []byte
	Path   string
}

// SendOptions regroupe les options communes d'envoi. Caption est ignoré par SendMessage.
type SendOptions struct {
	Caption string
	Silent  bool
	ReplyTo int
	Markup  *InlineKeyboardMarkup
}
```

- [ ] **Step 4: Implémenter `internal/telegram/client.go`**

```go
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.telegram.org"

// APIError est une réponse d'erreur de l'API Bot.
type APIError struct {
	Method      string
	Code        int
	Description string
	RetryAfter  time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram %s: %d %s", e.Method, e.Code, e.Description)
}

type Client struct {
	token   string
	baseURL string
	http    *http.Client
	limiter *Limiter
	backoff time.Duration
	retries int
	onError func(method string, code int)
}

type Option func(*Client)

func WithBaseURL(u string) Option                    { return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") } }
func WithBackoff(d time.Duration) Option             { return func(c *Client) { c.backoff = d } }
func WithLimiter(l *Limiter) Option                  { return func(c *Client) { c.limiter = l } }
func WithErrorHook(f func(method string, code int)) Option { return func(c *Client) { c.onError = f } }

func New(token string, opts ...Option) *Client {
	c := &Client{
		token:   token,
		baseURL: defaultBaseURL,
		http:    &http.Client{},
		limiter: NewLimiter(),
		backoff: time.Second,
		retries: 3,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

type request struct {
	method  string
	chatID  int64 // 0 : pas de limitation de débit
	params  map[string]string
	file    *fileParam
	timeout time.Duration
	noRetry bool
}

type fileParam struct {
	field string
	file  InputFile
}

func (c *Client) call(ctx context.Context, r request, out any) error {
	if r.timeout == 0 {
		r.timeout = 30 * time.Second
	}
	var err error
	for attempt := 0; ; attempt++ {
		if r.chatID != 0 {
			if err = c.limiter.Wait(ctx, r.chatID); err != nil {
				return err
			}
		}
		if err = c.once(ctx, r, out); err == nil {
			return nil
		}
		delay, retry := c.retryDelay(ctx, err, attempt)
		if !retry || r.noRetry || attempt >= c.retries {
			break
		}
		if serr := sleep(ctx, delay); serr != nil {
			return serr
		}
	}
	if c.onError != nil && ctx.Err() == nil {
		code := 0
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			code = apiErr.Code
		}
		c.onError(r.method, code)
	}
	return err
}

func (c *Client) retryDelay(ctx context.Context, err error, attempt int) (time.Duration, bool) {
	if ctx.Err() != nil {
		return 0, false
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Code == http.StatusTooManyRequests:
			d := apiErr.RetryAfter
			if d <= 0 {
				d = c.backoff
			}
			return min(d, time.Minute), true
		case apiErr.Code >= 500:
			return c.backoff << attempt, true
		default:
			return 0, false
		}
	}
	return c.backoff << attempt, true // erreur réseau
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

func (c *Client) once(ctx context.Context, r request, out any) error {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	body, size, contentType, err := r.body()
	if err != nil {
		return err
	}
	defer body.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/bot"+c.token+"/"+r.method, body)
	if err != nil {
		return errors.New(c.redact(err.Error()))
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", contentType)
	resp, err := c.http.Do(req)
	if err != nil {
		// L'erreur contient l'URL, donc le token : on le masque.
		return fmt.Errorf("telegram %s: %s", r.method, c.redact(err.Error()))
	}
	defer resp.Body.Close()
	var ar apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return &APIError{Method: r.method, Code: resp.StatusCode, Description: "réponse illisible"}
	}
	if !ar.OK {
		e := &APIError{Method: r.method, Code: ar.ErrorCode, Description: ar.Description}
		if e.Code == 0 {
			e.Code = resp.StatusCode
		}
		if ar.Parameters != nil {
			e.RetryAfter = time.Duration(ar.Parameters.RetryAfter) * time.Second
		}
		return e
	}
	if out != nil && len(ar.Result) > 0 {
		if err := json.Unmarshal(ar.Result, out); err != nil {
			return fmt.Errorf("telegram %s: %w", r.method, err)
		}
	}
	return nil
}

func (c *Client) redact(s string) string { return strings.ReplaceAll(s, c.token, "<token>") }

// body construit le corps : formulaire simple, ou multipart de taille connue (pas de chunked).
func (r request) body() (io.ReadCloser, int64, string, error) {
	if r.file == nil || r.file.file.FileID != "" {
		v := url.Values{}
		for k, val := range r.params {
			v.Set(k, val)
		}
		if r.file != nil {
			v.Set(r.file.field, r.file.file.FileID)
		}
		s := v.Encode()
		return io.NopCloser(strings.NewReader(s)), int64(len(s)), "application/x-www-form-urlencoded", nil
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, k := range slices.Sorted(maps.Keys(r.params)) {
		if err := mw.WriteField(k, r.params[k]); err != nil {
			return nil, 0, "", err
		}
	}
	if _, err := mw.CreateFormFile(r.file.field, r.file.file.Name); err != nil {
		return nil, 0, "", err
	}
	head := bytes.Clone(buf.Bytes())
	buf.Reset()
	if err := mw.Close(); err != nil {
		return nil, 0, "", err
	}
	tail := bytes.Clone(buf.Bytes())

	content, size, closer, err := r.file.file.open()
	if err != nil {
		return nil, 0, "", err
	}
	rc := struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(head), content, bytes.NewReader(tail)), closer}
	return rc, int64(len(head)) + size + int64(len(tail)), mw.FormDataContentType(), nil
}

func (f InputFile) open() (io.Reader, int64, io.Closer, error) {
	if f.Path == "" {
		return bytes.NewReader(f.Data), int64(len(f.Data)), io.NopCloser(nil), nil
	}
	file, err := os.Open(f.Path)
	if err != nil {
		return nil, 0, nil, err
	}
	st, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, nil, err
	}
	return file, st.Size(), file, nil
}

func chatParam(id int64) string { return strconv.FormatInt(id, 10) }

func (o SendOptions) apply(p map[string]string) {
	p["parse_mode"] = "HTML"
	if o.Caption != "" {
		p["caption"] = o.Caption
	}
	if o.Silent {
		p["disable_notification"] = "true"
	}
	if o.ReplyTo != 0 {
		p["reply_parameters"] = fmt.Sprintf(`{"message_id":%d,"allow_sending_without_reply":true}`, o.ReplyTo)
	}
	if o.Markup != nil {
		b, _ := json.Marshal(o.Markup)
		p["reply_markup"] = string(b)
	}
}

func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, o SendOptions) (Message, error) {
	p := map[string]string{"chat_id": chatParam(chatID), "text": text, "link_preview_options": `{"is_disabled":true}`}
	o.Caption = ""
	o.apply(p)
	var m Message
	err := c.call(ctx, request{method: "sendMessage", chatID: chatID, params: p}, &m)
	return m, err
}

func (c *Client) SendPhoto(ctx context.Context, chatID int64, f InputFile, o SendOptions) (Message, error) {
	return c.sendMedia(ctx, "sendPhoto", "photo", chatID, f, o, nil)
}

func (c *Client) SendVideo(ctx context.Context, chatID int64, f InputFile, o SendOptions) (Message, error) {
	return c.sendMedia(ctx, "sendVideo", "video", chatID, f, o, map[string]string{"supports_streaming": "true"})
}

func (c *Client) SendAnimation(ctx context.Context, chatID int64, f InputFile, o SendOptions) (Message, error) {
	return c.sendMedia(ctx, "sendAnimation", "animation", chatID, f, o, nil)
}

func (c *Client) sendMedia(ctx context.Context, method, field string, chatID int64, f InputFile, o SendOptions, extra map[string]string) (Message, error) {
	p := map[string]string{"chat_id": chatParam(chatID)}
	maps.Copy(p, extra)
	o.apply(p)
	timeout := 30 * time.Second
	if f.FileID == "" {
		timeout = 5 * time.Minute // upload jusqu'à 50 Mo
	}
	var m Message
	err := c.call(ctx, request{method: method, chatID: chatID, params: p, file: &fileParam{field: field, file: f}, timeout: timeout}, &m)
	return m, err
}

func (c *Client) EditMessageCaption(ctx context.Context, chatID int64, messageID int, caption string, markup *InlineKeyboardMarkup) error {
	p := map[string]string{"chat_id": chatParam(chatID), "message_id": strconv.Itoa(messageID)}
	SendOptions{Caption: caption, Markup: markup}.apply(p)
	return c.call(ctx, request{method: "editMessageCaption", chatID: chatID, params: p}, nil)
}

func (c *Client) EditMessageText(ctx context.Context, chatID int64, messageID int, text string, markup *InlineKeyboardMarkup) error {
	p := map[string]string{"chat_id": chatParam(chatID), "message_id": strconv.Itoa(messageID), "text": text, "link_preview_options": `{"is_disabled":true}`}
	SendOptions{Markup: markup}.apply(p)
	return c.call(ctx, request{method: "editMessageText", chatID: chatID, params: p}, nil)
}

func (c *Client) AnswerCallbackQuery(ctx context.Context, id, text string) error {
	p := map[string]string{"callback_query_id": id}
	if text != "" {
		p["text"] = text
	}
	return c.call(ctx, request{method: "answerCallbackQuery", params: p}, nil)
}

// GetUpdates fait un long polling ; pas de retry interne (la boucle appelante s'en charge).
func (c *Client) GetUpdates(ctx context.Context, offset int, timeout time.Duration) ([]Update, error) {
	p := map[string]string{
		"offset":          strconv.Itoa(offset),
		"timeout":         strconv.Itoa(int(timeout.Seconds())),
		"allowed_updates": `["message","callback_query"]`,
	}
	var ups []Update
	err := c.call(ctx, request{method: "getUpdates", params: p, timeout: timeout + 15*time.Second, noRetry: true}, &ups)
	return ups, err
}

func (c *Client) SetMyCommands(ctx context.Context, cmds []BotCommand) error {
	b, err := json.Marshal(cmds)
	if err != nil {
		return err
	}
	return c.call(ctx, request{method: "setMyCommands", params: map[string]string{"commands": string(b)}}, nil)
}
```

- [ ] **Step 5: Lancer les tests**

Run: `go test ./internal/telegram/ -v`
Expected: PASS (TestRetryOn429 dure environ 1 s)

- [ ] **Step 6: Commit**

```bash
git add internal/telegram
git commit -m "feat(telegram): client Bot API (multipart de taille connue, retries 429/5xx, token masqué)" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Actions des boutons et métriques

**Files:**
- Create: `internal/actions/actions.go`
- Test: `internal/actions/actions_test.go`
- Create: `internal/metrics/metrics.go`
- Test: `internal/metrics/metrics_test.go`

**Interfaces:**
- Produces:
  - `actions.KindMute = "m"`, `KindPause = "p"`, `KindClip = "c"`, `KindSnapshot = "s"`
  - `actions.Mute(camera string, d time.Duration) string`, `Pause(d time.Duration) string`, `Clip(id string) string`, `Snapshot(camera string) string`
  - `type Action struct { Kind, Camera, ID string; Duration time.Duration }`, `actions.Parse(data string) (Action, error)`
  - `metrics.New() *Metrics` avec les champs `Registry *prometheus.Registry`, `EventsReceived *prometheus.CounterVec{camera,label}`, `EventsFiltered *prometheus.CounterVec{reason}`, `EventsDropped prometheus.Counter`, `NotificationsSent *prometheus.CounterVec{kind}`, `TelegramErrors *prometheus.CounterVec{method,code}`, `MQTTConnected prometheus.Gauge`, `MediaDownload prometheus.Histogram`

- [ ] **Step 1: Ajouter la dépendance Prometheus**

```bash
go get github.com/prometheus/client_golang/prometheus
```

- [ ] **Step 2: Écrire les tests (qui échouent)**

`internal/actions/actions_test.go` :

```go
package actions

import (
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	cases := []struct {
		data string
		want Action
	}{
		{Mute("jardin", time.Hour), Action{Kind: KindMute, Camera: "jardin", Duration: time.Hour}},
		{Pause(30 * time.Minute), Action{Kind: KindPause, Duration: 30 * time.Minute}},
		{Clip("1727520000.123456-abc123"), Action{Kind: KindClip, ID: "1727520000.123456-abc123"}},
		{Snapshot("garage"), Action{Kind: KindSnapshot, Camera: "garage"}},
	}
	for _, tc := range cases {
		got, err := Parse(tc.data)
		if err != nil || got != tc.want {
			t.Errorf("Parse(%q) = %+v, %v ; attendu %+v", tc.data, got, err, tc.want)
		}
	}
	if Mute("jardin", time.Hour) != "m:jardin:3600" || Pause(30*time.Minute) != "p:1800" {
		t.Error("encodage inattendu")
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	for _, data := range []string{"", "x", "z:1", "m:jardin", "m:jardin:abc", "p:-5", "c:"} {
		if _, err := Parse(data); err == nil {
			t.Errorf("Parse(%q) aurait dû échouer", data)
		}
	}
}
```

`internal/metrics/metrics_test.go` :

```go
package metrics

import "testing"

func TestRegistryExposesMetrics(t *testing.T) {
	m := New()
	m.EventsDropped.Inc()
	m.EventsFiltered.WithLabelValues("label").Inc()
	families, err := m.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, f := range families {
		found[f.GetName()] = true
	}
	for _, name := range []string{"ft_events_dropped_total", "ft_events_filtered_total", "ft_mqtt_connected", "go_goroutines"} {
		if !found[name] {
			t.Errorf("métrique %s absente", name)
		}
	}
}
```

- [ ] **Step 3: Vérifier l'échec**

Run: `go test ./internal/actions/ ./internal/metrics/`
Expected: FAIL (`undefined: Parse`, `undefined: New`)

- [ ] **Step 4: Implémenter `internal/actions/actions.go`**

```go
// Package actions encode et décode les callback_data des boutons Telegram (64 octets max).
package actions

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	KindMute     = "m" // m:<caméra>:<secondes>
	KindPause    = "p" // p:<secondes>
	KindClip     = "c" // c:<id événement ou review>
	KindSnapshot = "s" // s:<caméra>
)

type Action struct {
	Kind     string
	Camera   string
	ID       string
	Duration time.Duration
}

func Mute(camera string, d time.Duration) string {
	return KindMute + ":" + camera + ":" + strconv.Itoa(int(d.Seconds()))
}

func Pause(d time.Duration) string { return KindPause + ":" + strconv.Itoa(int(d.Seconds())) }

func Clip(id string) string { return KindClip + ":" + id }

func Snapshot(camera string) string { return KindSnapshot + ":" + camera }

func Parse(data string) (Action, error) {
	invalid := fmt.Errorf("action invalide %q", data)
	kind, rest, ok := strings.Cut(data, ":")
	if !ok || rest == "" {
		return Action{}, invalid
	}
	seconds := func(s string) (time.Duration, bool) {
		n, err := strconv.Atoi(s)
		return time.Duration(n) * time.Second, err == nil && n > 0
	}
	switch kind {
	case KindMute:
		i := strings.LastIndexByte(rest, ':')
		if i <= 0 {
			return Action{}, invalid
		}
		d, ok := seconds(rest[i+1:])
		if !ok {
			return Action{}, invalid
		}
		return Action{Kind: kind, Camera: rest[:i], Duration: d}, nil
	case KindPause:
		d, ok := seconds(rest)
		if !ok {
			return Action{}, invalid
		}
		return Action{Kind: kind, Duration: d}, nil
	case KindClip:
		return Action{Kind: kind, ID: rest}, nil
	case KindSnapshot:
		return Action{Kind: kind, Camera: rest}, nil
	}
	return Action{}, invalid
}
```

- [ ] **Step 5: Implémenter `internal/metrics/metrics.go`**

```go
// Package metrics déclare les métriques Prometheus du service.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

type Metrics struct {
	Registry          *prometheus.Registry
	EventsReceived    *prometheus.CounterVec
	EventsFiltered    *prometheus.CounterVec
	EventsDropped     prometheus.Counter
	NotificationsSent *prometheus.CounterVec
	TelegramErrors    *prometheus.CounterVec
	MQTTConnected     prometheus.Gauge
	MediaDownload     prometheus.Histogram
}

func New() *Metrics {
	m := &Metrics{
		Registry: prometheus.NewRegistry(),
		EventsReceived: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ft_events_received_total", Help: "Événements Frigate reçus.",
		}, []string{"camera", "label"}),
		EventsFiltered: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ft_events_filtered_total", Help: "Événements non notifiés, par raison.",
		}, []string{"reason"}),
		EventsDropped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "ft_events_dropped_total", Help: "Messages MQTT perdus car la file était pleine.",
		}),
		NotificationsSent: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ft_notifications_sent_total", Help: "Messages Telegram envoyés, par type.",
		}, []string{"kind"}),
		TelegramErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ft_telegram_errors_total", Help: "Appels Telegram en échec.",
		}, []string{"method", "code"}),
		MQTTConnected: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "ft_mqtt_connected", Help: "1 si le client est connecté au broker MQTT.",
		}),
		MediaDownload: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "ft_media_download_seconds", Help: "Durée des téléchargements depuis Frigate.",
			Buckets: prometheus.ExponentialBuckets(0.05, 2, 10),
		}),
	}
	m.Registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.EventsReceived, m.EventsFiltered, m.EventsDropped, m.NotificationsSent,
		m.TelegramErrors, m.MQTTConnected, m.MediaDownload,
	)
	return m
}
```

- [ ] **Step 6: Lancer les tests**

Run: `go test ./internal/actions/ ./internal/metrics/ -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/actions internal/metrics
git commit -m "feat: actions des boutons et métriques Prometheus" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Légende et boutons

**Files:**
- Create: `internal/notifier/caption.go`
- Test: `internal/notifier/caption_test.go`

**Interfaces:**
- Consumes: `actions.Mute/Pause/Clip` (Task 8), `telegram.InlineKeyboardMarkup` (Task 7)
- Produces (interne au package notifier) :
  - `type captionData struct { Label, SubLabel, Camera string; Zones []string; Score float64; HasScore bool; Start time.Time; Description, Link string }`
  - `buildCaption(d captionData, loc *time.Location) string` (≤ 1024 caractères)
  - `buttons(camera, id string) *telegram.InlineKeyboardMarkup`
  - `labelText(label string) string`

- [ ] **Step 1: Écrire les tests (qui échouent)**

`internal/notifier/caption_test.go` :

```go
package notifier

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func paris(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestBuildCaption(t *testing.T) {
	got := buildCaption(captionData{
		Label: "person", SubLabel: "Alice", Camera: "jardin",
		Zones: []string{"allee", "portail"}, Score: 0.87, HasScore: true,
		Start: time.Date(2026, 9, 28, 14, 32, 5, 0, time.UTC),
		Link:  "https://nvr.example/explore?event_id=abc",
	}, paris(t))
	want := "<b>🚶 Personne</b> — jardin (Alice)\n" +
		"📍 allee, portail · 87 %\n" +
		"🕑 28/09 16:32:05\n" +
		"🔗 <a href=\"https://nvr.example/explore?event_id=abc\">Ouvrir dans Frigate</a>"
	if got != want {
		t.Errorf("légende :\n%s\nattendu :\n%s", got, want)
	}
}

func TestBuildCaptionEscapesAndUnknownLabel(t *testing.T) {
	got := buildCaption(captionData{Label: "raccoon", Camera: "<cam>", Start: time.Unix(0, 0)}, time.UTC)
	if !strings.Contains(got, "🔔 raccoon") || !strings.Contains(got, "&lt;cam&gt;") {
		t.Errorf("légende = %q", got)
	}
	if strings.Contains(got, "📍") || strings.Contains(got, "🔗") {
		t.Errorf("lignes vides inattendues : %q", got)
	}
}

func TestBuildCaptionTruncatesDescription(t *testing.T) {
	got := buildCaption(captionData{Label: "person", Camera: "jardin", Start: time.Unix(0, 0),
		Description: strings.Repeat("é", 2000), Link: "https://nvr.example/x"}, time.UTC)
	if n := utf8.RuneCountInString(got); n > 1024 {
		t.Errorf("légende de %d caractères (max 1024)", n)
	}
	if !strings.Contains(got, "…") || !strings.HasSuffix(got, "Ouvrir dans Frigate</a>") {
		t.Errorf("troncature incorrecte : %q", got[len(got)-80:])
	}
}

func TestButtons(t *testing.T) {
	row := buttons("jardin", "abc").InlineKeyboard[0]
	if len(row) != 3 || row[0].CallbackData != "m:jardin:3600" || row[1].CallbackData != "p:1800" || row[2].CallbackData != "c:abc" {
		t.Errorf("boutons = %+v", row)
	}
	long := strings.Repeat("x", 70)
	if row := buttons("jardin", long).InlineKeyboard[0]; len(row) != 2 {
		t.Errorf("un callback_data > 64 octets doit être omis : %+v", row)
	}
}
```

- [ ] **Step 2: Vérifier l'échec**

Run: `go test ./internal/notifier/`
Expected: FAIL (`undefined: buildCaption`)

- [ ] **Step 3: Implémenter `internal/notifier/caption.go`**

```go
package notifier

import (
	"fmt"
	"html"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"frigate-telegram/internal/actions"
	"frigate-telegram/internal/telegram"
)

const maxCaption = 1024

var labelNames = map[string]struct{ emoji, name string }{
	"person":     {"🚶", "Personne"},
	"car":        {"🚗", "Voiture"},
	"dog":        {"🐕", "Chien"},
	"cat":        {"🐈", "Chat"},
	"bicycle":    {"🚲", "Vélo"},
	"motorcycle": {"🏍️", "Moto"},
	"bird":       {"🐦", "Oiseau"},
	"package":    {"📦", "Colis"},
}

func labelText(label string) string {
	if l, ok := labelNames[label]; ok {
		return l.emoji + " " + l.name
	}
	if label == "" {
		return "🔔 Détection"
	}
	return "🔔 " + label
}

type captionData struct {
	Label, SubLabel, Camera string
	Zones                   []string
	Score                   float64
	HasScore                bool
	Start                   time.Time
	Description             string
	Link                    string
}

// buildCaption produit la légende HTML (≤ 1024 caractères ; la description est tronquée si besoin).
func buildCaption(d captionData, loc *time.Location) string {
	esc := html.EscapeString
	var b strings.Builder
	b.WriteString("<b>" + esc(labelText(d.Label)) + "</b> — " + esc(d.Camera))
	if d.SubLabel != "" {
		b.WriteString(" (" + esc(d.SubLabel) + ")")
	}
	var details []string
	if len(d.Zones) > 0 {
		details = append(details, "📍 "+esc(strings.Join(d.Zones, ", ")))
	}
	if d.HasScore && d.Score > 0 {
		details = append(details, fmt.Sprintf("%d %%", int(math.Round(d.Score*100))))
	}
	if len(details) > 0 {
		b.WriteString("\n" + strings.Join(details, " · "))
	}
	b.WriteString("\n🕑 " + d.Start.In(loc).Format("02/01 15:04:05"))

	footer := ""
	if d.Link != "" {
		footer = "\n🔗 <a href=\"" + esc(d.Link) + "\">Ouvrir dans Frigate</a>"
	}
	if d.Description != "" {
		// Telegram compte le texte hors balises : compter la chaîne HTML entière est prudent.
		room := maxCaption - utf8.RuneCountInString(b.String()) - utf8.RuneCountInString(footer) - len("\n\n<i></i>")
		if room > 20 {
			b.WriteString("\n\n<i>" + esc(truncate(d.Description, room)) + "</i>")
		}
	}
	b.WriteString(footer)
	return b.String()
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

// buttons renvoie les boutons d'une notification ; un bouton dont le callback_data dépasse 64 octets est omis.
func buttons(camera, id string) *telegram.InlineKeyboardMarkup {
	var row []telegram.InlineKeyboardButton
	add := func(text, data string) {
		if len(data) <= 64 {
			row = append(row, telegram.InlineKeyboardButton{Text: text, CallbackData: data})
		}
	}
	add("🔇 1 h", actions.Mute(camera, time.Hour))
	add("⏸ 30 min", actions.Pause(30*time.Minute))
	add("🎬 Clip", actions.Clip(id))
	return &telegram.InlineKeyboardMarkup{InlineKeyboard: [][]telegram.InlineKeyboardButton{row}}
}
```

- [ ] **Step 4: Lancer les tests**

Run: `go test ./internal/notifier/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/notifier
git commit -m "feat(notifier): légende HTML et boutons des notifications" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Notifier, cœur et mode events

**Files:**
- Create: `internal/notifier/notifier.go`
- Create: `internal/notifier/media.go`
- Create: `internal/notifier/events.go`
- Test: `internal/notifier/fakes_test.go`, `internal/notifier/events_test.go`

**Interfaces:**
- Consumes: `config` (T1), `frigate` (T2, T3 : `ParseEventMessage`, chemins, `Retryable`, `ErrTooLarge`, `HTTPError`), `state.Store` (T4), `filter.Engine`, `filter.CooldownKey` (T5), types `telegram` (T7), `metrics.Metrics` (T8), `buildCaption`, `buttons` (T9)
- Produces:
  - `type Frigate interface { GetBytes(ctx, path string, max int64) ([]byte, error); DownloadToFile(ctx, path string, max int64) (string, error); Review(ctx, id string) (frigate.Review, error); Events(ctx, camera string, limit int) ([]frigate.APIEvent, error) }`
  - `type Telegram interface { SendMessage(...) ; SendPhoto(...); SendVideo(...); SendAnimation(...); EditMessageCaption(...); EditMessageText(...) }`, avec les signatures de la Task 7
  - `type Deps struct { Config *config.Config; Engine *filter.Engine; State *state.Store; Frigate Frigate; Telegram Telegram; Metrics *metrics.Metrics; Log *slog.Logger; Now func() time.Time; ClipRetryDelays []time.Duration; SnapshotRetryDelay time.Duration }`
  - `notifier.New(d Deps) *Notifier`
  - `(*Notifier).Topics() []string`, `Handle(topic string, payload []byte)` (non bloquant), `Run(ctx)`, `Process(ctx, topic string, payload []byte)` (synchrone ; les envois partent en arrière-plan), `Wait(timeout) bool`, `Shutdown(timeout) bool`, `Count24h() int`
  - Interne : `tracked`, `notify`, `finish`, `deliver`, `sendSnapshot`, `sendFollowUp`, `download`, `uiLink`, `caption`

- [ ] **Step 1: Écrire les fakes de test**

`internal/notifier/fakes_test.go` :

```go
package notifier

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"frigate-telegram/internal/config"
	"frigate-telegram/internal/filter"
	"frigate-telegram/internal/frigate"
	"frigate-telegram/internal/metrics"
	"frigate-telegram/internal/state"
	"frigate-telegram/internal/telegram"
)

type fakeFrigate struct {
	mu       sync.Mutex
	files    map[string][]byte
	fails    map[string]int // nombre de 404 à renvoyer avant succès
	tooLarge map[string]bool
	calls    []string
	events   []frigate.APIEvent
	reviews  map[string]frigate.Review
}

func newFakeFrigate() *fakeFrigate {
	return &fakeFrigate{files: map[string][]byte{}, fails: map[string]int{}, tooLarge: map[string]bool{}, reviews: map[string]frigate.Review{}}
}

func (f *fakeFrigate) GetBytes(_ context.Context, path string, _ int64) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, path)
	if f.tooLarge[path] {
		return nil, frigate.ErrTooLarge
	}
	if f.fails[path] > 0 {
		f.fails[path]--
		return nil, &frigate.HTTPError{Status: 404, Path: path}
	}
	b, ok := f.files[path]
	if !ok {
		return nil, &frigate.HTTPError{Status: 404, Path: path}
	}
	return b, nil
}

func (f *fakeFrigate) DownloadToFile(ctx context.Context, path string, max int64) (string, error) {
	b, err := f.GetBytes(ctx, path, max)
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp("", "fake-*")
	if err != nil {
		return "", err
	}
	defer tmp.Close()
	_, err = tmp.Write(b)
	return tmp.Name(), err
}

func (f *fakeFrigate) Review(_ context.Context, id string) (frigate.Review, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.reviews[id]
	if !ok {
		return r, &frigate.HTTPError{Status: 404, Path: "/api/review/" + id}
	}
	return r, nil
}

func (f *fakeFrigate) Events(context.Context, string, int) ([]frigate.APIEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.events, nil
}

func (f *fakeFrigate) countCalls(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == path {
			n++
		}
	}
	return n
}

type tgCall struct {
	Method string
	ChatID int64
	Target int    // message visé par une édition
	Text   string // texte ou légende
	FileID string
	Data   string // contenu uploadé
	Opts   telegram.SendOptions
	Result int // message_id renvoyé
}

type fakeTelegram struct {
	mu     sync.Mutex
	calls  []tgCall
	nextID int
}

func (f *fakeTelegram) add(c tgCall) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	c.Result = f.nextID
	f.calls = append(f.calls, c)
	return f.nextID
}

func fileData(file telegram.InputFile) string {
	if file.Path != "" {
		b, _ := os.ReadFile(file.Path)
		return string(b)
	}
	return string(file.Data)
}

func (f *fakeTelegram) SendMessage(_ context.Context, chatID int64, text string, o telegram.SendOptions) (telegram.Message, error) {
	return telegram.Message{MessageID: f.add(tgCall{Method: "sendMessage", ChatID: chatID, Text: text, Opts: o})}, nil
}

func (f *fakeTelegram) SendPhoto(_ context.Context, chatID int64, file telegram.InputFile, o telegram.SendOptions) (telegram.Message, error) {
	id := f.add(tgCall{Method: "sendPhoto", ChatID: chatID, Text: o.Caption, FileID: file.FileID, Data: fileData(file), Opts: o})
	return telegram.Message{MessageID: id, Photo: []telegram.PhotoSize{{FileID: "photo-file"}}}, nil
}

func (f *fakeTelegram) SendVideo(_ context.Context, chatID int64, file telegram.InputFile, o telegram.SendOptions) (telegram.Message, error) {
	id := f.add(tgCall{Method: "sendVideo", ChatID: chatID, FileID: file.FileID, Data: fileData(file), Opts: o})
	return telegram.Message{MessageID: id, Video: &telegram.Video{FileID: "video-file"}}, nil
}

func (f *fakeTelegram) SendAnimation(_ context.Context, chatID int64, file telegram.InputFile, o telegram.SendOptions) (telegram.Message, error) {
	id := f.add(tgCall{Method: "sendAnimation", ChatID: chatID, FileID: file.FileID, Data: fileData(file), Opts: o})
	return telegram.Message{MessageID: id, Animation: &telegram.Animation{FileID: "gif-file"}}, nil
}

func (f *fakeTelegram) EditMessageCaption(_ context.Context, chatID int64, messageID int, caption string, _ *telegram.InlineKeyboardMarkup) error {
	f.add(tgCall{Method: "editMessageCaption", ChatID: chatID, Target: messageID, Text: caption})
	return nil
}

func (f *fakeTelegram) EditMessageText(_ context.Context, chatID int64, messageID int, text string, _ *telegram.InlineKeyboardMarkup) error {
	f.add(tgCall{Method: "editMessageText", ChatID: chatID, Target: messageID, Text: text})
	return nil
}

func (f *fakeTelegram) byMethod(method string) []tgCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []tgCall
	for _, c := range f.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeTelegram) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func find(calls []tgCall, chatID int64) (tgCall, bool) {
	for _, c := range calls {
		if c.ChatID == chatID {
			return c, true
		}
	}
	return tgCall{}, false
}

func countData(calls []tgCall, data string) int {
	n := 0
	for _, c := range calls {
		if c.Data == data {
			n++
		}
	}
	return n
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration)     { c.mu.Lock(); defer c.mu.Unlock(); c.t = c.t.Add(d) }

const testConfig = `
timezone: Europe/Paris
mode: %s
frigate:
  url: http://frigate:5000
  external_url: https://nvr.example
mqtt:
  broker: tcp://mqtt:1883
telegram:
  token: t
  admins: [1]
  chats:
    moi: 1
    famille: -100
notify:
  labels: [person]
  cooldown: 1m
  clip_delay: 0s
  severity: [alert]
cameras:
  jardin:
    zones: [allee]
`

type harness struct {
	n     *Notifier
	fr    *fakeFrigate
	tg    *fakeTelegram
	clock *clock
	m     *metrics.Metrics
}

func newHarness(t *testing.T, mode string) *harness {
	t.Helper()
	cfg, err := config.Parse([]byte(fmt.Sprintf(testConfig, mode)), func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	clk := &clock{t: time.Date(2026, 9, 28, 14, 0, 0, 0, time.UTC)}
	st, err := state.Load(filepath.Join(t.TempDir(), "state.json"), clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{fr: newFakeFrigate(), tg: &fakeTelegram{}, clock: clk, m: metrics.New()}
	h.n = New(Deps{
		Config: cfg, Engine: filter.New(cfg, st), State: st,
		Frigate: h.fr, Telegram: h.tg, Metrics: h.m,
		Log: slog.New(slog.DiscardHandler), Now: clk.Now,
		ClipRetryDelays:    []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond},
		SnapshotRetryDelay: time.Millisecond,
	})
	return h
}

// send traite un message puis attend la fin de tous les envois déclenchés.
func (h *harness) send(t *testing.T, topic string, payload []byte) {
	t.Helper()
	h.n.Process(context.Background(), topic, payload)
	if !h.n.Wait(5 * time.Second) {
		t.Fatal("envois non terminés après 5 s")
	}
}
```

- [ ] **Step 2: Écrire les tests du mode events (qui échouent)**

`internal/notifier/events_test.go` :

```go
package notifier

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"frigate-telegram/internal/frigate"
)

const evID = "1790604000.000001-abc"

func eventMsg(typ, id, camera, label string, zones []string) []byte {
	ev := map[string]any{
		"id": id, "camera": camera, "label": label, "sub_label": nil,
		"score": 0.9, "top_score": 0.9, "entered_zones": zones, "current_zones": zones,
		"stationary": false, "false_positive": false, "has_snapshot": true, "has_clip": true,
		"start_time": 1790604000.0,
	}
	if typ == "end" {
		ev["end_time"] = 1790604030.0
	}
	b, _ := json.Marshal(map[string]any{"type": typ, "before": ev, "after": ev})
	return b
}

func TestNewEventSendsSnapshotToEveryChat(t *testing.T) {
	h := newHarness(t, "events")
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))

	photos := h.tg.byMethod("sendPhoto")
	if len(photos) != 2 {
		t.Fatalf("photos = %d, attendu 2", len(photos))
	}
	for _, c := range photos {
		if !strings.Contains(c.Text, "Personne") || !strings.Contains(c.Text, "garage") {
			t.Errorf("légende inattendue : %q", c.Text)
		}
		if c.Opts.Markup == nil || len(c.Opts.Markup.InlineKeyboard[0]) != 3 {
			t.Error("boutons manquants")
		}
	}
	if countData(photos, "jpeg") != 1 || photos[0].FileID != "" {
		t.Errorf("le premier envoi doit uploader : %+v", photos[0])
	}
	if _, ok := find(photos, 1); !ok {
		t.Error("chat 1 non notifié")
	}
	if _, ok := find(photos, -100); !ok {
		t.Error("chat -100 non notifié")
	}
	reused := 0
	for _, c := range photos {
		if c.FileID == "photo-file" {
			reused++
		}
	}
	if reused != 1 {
		t.Errorf("le second envoi doit réutiliser le file_id (reused=%d)", reused)
	}
	if h.n.Count24h() != 1 {
		t.Errorf("Count24h = %d", h.n.Count24h())
	}
}

func TestEndSendsClipAsReplyToSnapshot(t *testing.T) {
	h := newHarness(t, "events")
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.fr.files[frigate.EventClipPath(evID)] = []byte("mp4")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	h.send(t, "frigate/events", eventMsg("end", evID, "garage", "person", nil))

	photos, videos := h.tg.byMethod("sendPhoto"), h.tg.byMethod("sendVideo")
	if len(videos) != 2 {
		t.Fatalf("vidéos = %d, attendu 2", len(videos))
	}
	for _, v := range videos {
		p, _ := find(photos, v.ChatID)
		if v.Opts.ReplyTo != p.Result {
			t.Errorf("chat %d : reply_to=%d, attendu %d", v.ChatID, v.Opts.ReplyTo, p.Result)
		}
		if !v.Opts.Silent {
			t.Error("le clip doit être envoyé sans son")
		}
	}
	if countData(videos, "mp4") != 1 {
		t.Error("le clip doit être uploadé une seule fois")
	}
}

func TestZoneEnteredOnUpdate(t *testing.T) {
	h := newHarness(t, "events")
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.send(t, "frigate/events", eventMsg("new", evID, "jardin", "person", nil))
	if h.tg.count() != 0 {
		t.Fatal("aucune notification attendue hors zone")
	}
	h.send(t, "frigate/events", eventMsg("update", evID, "jardin", "person", []string{"allee"}))
	if len(h.tg.byMethod("sendPhoto")) != 2 {
		t.Fatal("notification attendue à l'entrée dans la zone")
	}
	h.send(t, "frigate/events", eventMsg("update", evID, "jardin", "person", []string{"allee"}))
	if len(h.tg.byMethod("sendPhoto")) != 2 {
		t.Error("un événement ne doit être notifié qu'une fois")
	}
}

func TestCooldownBlocksSecondEvent(t *testing.T) {
	h := newHarness(t, "events")
	for _, id := range []string{"a", "b", "c"} {
		h.fr.files[frigate.EventSnapshotPath(id)] = []byte("jpeg")
	}
	h.send(t, "frigate/events", eventMsg("new", "a", "garage", "person", nil))
	h.clock.Add(30 * time.Second)
	h.send(t, "frigate/events", eventMsg("new", "b", "garage", "person", nil))
	if n := len(h.tg.byMethod("sendPhoto")); n != 2 {
		t.Fatalf("photos = %d : l'événement b doit être bloqué par le cooldown", n)
	}
	h.clock.Add(2 * time.Minute)
	h.send(t, "frigate/events", eventMsg("new", "c", "garage", "person", nil))
	if n := len(h.tg.byMethod("sendPhoto")); n != 4 {
		t.Errorf("photos = %d : l'événement c doit passer", n)
	}
}

func TestFilteredEventIsCountedOnce(t *testing.T) {
	h := newHarness(t, "events")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "dog", nil))
	h.send(t, "frigate/events", eventMsg("update", evID, "garage", "dog", nil))
	h.send(t, "frigate/events", eventMsg("end", evID, "garage", "dog", nil))
	if h.tg.count() != 0 {
		t.Error("aucun envoi attendu")
	}
	if got := testutil.ToFloat64(h.m.EventsFiltered.WithLabelValues("label")); got != 1 {
		t.Errorf("filtered{label} = %v, attendu 1", got)
	}
}

func TestClipRetriedUntilAvailable(t *testing.T) {
	h := newHarness(t, "events")
	clip := frigate.EventClipPath(evID)
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.fr.files[clip] = []byte("mp4")
	h.fr.fails[clip] = 2
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	h.send(t, "frigate/events", eventMsg("end", evID, "garage", "person", nil))
	if len(h.tg.byMethod("sendVideo")) != 2 || h.fr.countCalls(clip) != 3 {
		t.Errorf("vidéos=%d appels clip=%d", len(h.tg.byMethod("sendVideo")), h.fr.countCalls(clip))
	}
}

func TestClipTooLargeSendsLink(t *testing.T) {
	h := newHarness(t, "events")
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.fr.tooLarge[frigate.EventClipPath(evID)] = true
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	h.send(t, "frigate/events", eventMsg("end", evID, "garage", "person", nil))
	msgs := h.tg.byMethod("sendMessage")
	if len(msgs) != 2 || !strings.Contains(msgs[0].Text, "https://nvr.example/api/events/"+evID+"/clip.mp4") {
		t.Fatalf("messages = %+v", msgs)
	}
	if len(h.tg.byMethod("sendVideo")) != 0 {
		t.Error("aucune vidéo attendue")
	}
}

func TestMissingSnapshotFallsBackToText(t *testing.T) {
	h := newHarness(t, "events")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	msgs := h.tg.byMethod("sendMessage")
	if len(msgs) != 2 || !strings.Contains(msgs[0].Text, "Personne") {
		t.Fatalf("messages = %+v", msgs)
	}
	if h.fr.countCalls(frigate.EventSnapshotPath(evID)) != 2 {
		t.Error("le snapshot doit être réessayé une fois")
	}
}

func TestHandleDropsWhenInboxFull(t *testing.T) {
	h := newHarness(t, "events")
	for i := 0; i < inboxSize+1; i++ {
		h.n.Handle("frigate/events", []byte("{}"))
	}
	if got := testutil.ToFloat64(h.m.EventsDropped); got != 1 {
		t.Errorf("dropped = %v, attendu 1", got)
	}
}

func TestCount24hExpires(t *testing.T) {
	h := newHarness(t, "events")
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	h.clock.Add(25 * time.Hour)
	if h.n.Count24h() != 0 {
		t.Errorf("Count24h = %d, attendu 0", h.n.Count24h())
	}
}

func TestTopicsDependOnMode(t *testing.T) {
	if got := newHarness(t, "events").n.Topics(); got[0] != "frigate/events" || got[1] != "frigate/tracked_object_update" {
		t.Errorf("topics = %v", got)
	}
	if got := newHarness(t, "reviews").n.Topics(); got[0] != "frigate/reviews" {
		t.Errorf("topics = %v", got)
	}
}
```

- [ ] **Step 3: Vérifier l'échec**

Run: `go test ./internal/notifier/`
Expected: FAIL (`undefined: New`, `undefined: Deps`…)

- [ ] **Step 4: Implémenter `internal/notifier/notifier.go`**

```go
// Package notifier transforme les messages MQTT de Frigate en notifications Telegram.
package notifier

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"frigate-telegram/internal/config"
	"frigate-telegram/internal/filter"
	"frigate-telegram/internal/frigate"
	"frigate-telegram/internal/metrics"
	"frigate-telegram/internal/state"
	"frigate-telegram/internal/telegram"
)

const (
	maxPhotoSize   = 10 << 20
	maxUploadSize  = 50 << 20
	inboxSize      = 256
	endedRetention = 10 * time.Minute // laisse le temps aux descriptions GenAI d'arriver
	staleAfter     = time.Hour
)

type Frigate interface {
	GetBytes(ctx context.Context, path string, max int64) ([]byte, error)
	DownloadToFile(ctx context.Context, path string, max int64) (string, error)
	Review(ctx context.Context, id string) (frigate.Review, error)
	Events(ctx context.Context, camera string, limit int) ([]frigate.APIEvent, error)
}

type Telegram interface {
	SendMessage(ctx context.Context, chatID int64, text string, o telegram.SendOptions) (telegram.Message, error)
	SendPhoto(ctx context.Context, chatID int64, f telegram.InputFile, o telegram.SendOptions) (telegram.Message, error)
	SendVideo(ctx context.Context, chatID int64, f telegram.InputFile, o telegram.SendOptions) (telegram.Message, error)
	SendAnimation(ctx context.Context, chatID int64, f telegram.InputFile, o telegram.SendOptions) (telegram.Message, error)
	EditMessageCaption(ctx context.Context, chatID int64, messageID int, caption string, markup *telegram.InlineKeyboardMarkup) error
	EditMessageText(ctx context.Context, chatID int64, messageID int, text string, markup *telegram.InlineKeyboardMarkup) error
}

type Deps struct {
	Config             *config.Config
	Engine             *filter.Engine
	State              *state.Store
	Frigate            Frigate
	Telegram           Telegram
	Metrics            *metrics.Metrics
	Log                *slog.Logger
	Now                func() time.Time // défaut : time.Now
	ClipRetryDelays    []time.Duration  // défaut : 5 s, 10 s, 20 s
	SnapshotRetryDelay time.Duration    // défaut : 1 s
}

type Notifier struct {
	Deps
	inbox       chan inMsg
	topics      topics
	sendCtx     context.Context
	cancelSends context.CancelFunc
	wg          sync.WaitGroup

	mu      sync.Mutex // protège tracked, sent et les champs des *tracked (sauf messages)
	tracked map[string]*tracked
	sent    []time.Time
}

type inMsg struct {
	topic   string
	payload []byte
}

type topics struct{ events, reviews, updates string }

type sentMsg struct {
	id   int
	text bool // message texte (pas de photo) : s'édite avec editMessageText
}

// tracked suit un événement (mode events) ou une review (mode reviews).
type tracked struct {
	id, camera   string // immuables après création
	label        string
	subLabel     string
	zones        []string
	score        float64
	hasScore     bool
	start        time.Time
	startTS      float64
	eventIDs     []string // ids des événements Frigate liés (descriptions GenAI)
	snapshotPath string
	clipPath     string
	gifPath      string
	link         string
	description  string
	lastReason   string

	notified, silent, ended bool
	chats                   []string
	endedAt, lastSeen       time.Time

	ready    chan struct{} // fermé quand le snapshot a été envoyé à tous les chats
	msgMu    sync.Mutex
	messages map[string]sentMsg // nom du chat → message envoyé
}

func (t *tracked) setMessage(chat string, m sentMsg) {
	t.msgMu.Lock()
	defer t.msgMu.Unlock()
	t.messages[chat] = m
}

func (t *tracked) message(chat string) sentMsg {
	t.msgMu.Lock()
	defer t.msgMu.Unlock()
	return t.messages[chat]
}

func (t *tracked) messagesCopy() map[string]sentMsg {
	t.msgMu.Lock()
	defer t.msgMu.Unlock()
	return maps.Clone(t.messages)
}

func New(d Deps) *Notifier {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.ClipRetryDelays == nil {
		d.ClipRetryDelays = []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second}
	}
	if d.SnapshotRetryDelay == 0 {
		d.SnapshotRetryDelay = time.Second
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	p := d.Config.MQTT.TopicPrefix
	ctx, cancel := context.WithCancel(context.Background())
	return &Notifier{
		Deps:        d,
		inbox:       make(chan inMsg, inboxSize),
		topics:      topics{events: p + "/events", reviews: p + "/reviews", updates: p + "/tracked_object_update"},
		sendCtx:     ctx,
		cancelSends: cancel,
		tracked:     map[string]*tracked{},
	}
}

// Topics renvoie les topics MQTT à écouter selon le mode.
func (n *Notifier) Topics() []string {
	main := n.topics.events
	if n.Config.Mode == config.ModeReviews {
		main = n.topics.reviews
	}
	return []string{main, n.topics.updates}
}

// Handle est appelé par le client MQTT ; il ne bloque jamais.
func (n *Notifier) Handle(topic string, payload []byte) {
	select {
	case n.inbox <- inMsg{topic: topic, payload: payload}:
	default:
		n.Metrics.EventsDropped.Inc()
		n.Log.Warn("file d'événements pleine, message ignoré", "topic", topic)
	}
}

// Run consomme la file jusqu'à l'annulation de ctx. Les envois utilisent un contexte
// distinct, annulé seulement par Shutdown, pour pouvoir se terminer proprement.
func (n *Notifier) Run(ctx context.Context) {
	sweep := time.NewTicker(time.Minute)
	defer sweep.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-n.inbox:
			n.Process(n.sendCtx, m.topic, m.payload)
		case <-sweep.C:
			n.sweep()
		}
	}
}

// Process traite un message de façon synchrone ; les envois partent en arrière-plan.
func (n *Notifier) Process(ctx context.Context, topic string, payload []byte) {
	n.mu.Lock()
	defer n.mu.Unlock()
	var err error
	switch topic {
	case n.topics.events:
		var msg frigate.EventMessage
		if msg, err = frigate.ParseEventMessage(payload); err == nil {
			n.handleEvent(ctx, msg)
		}
	}
	if err != nil {
		n.Log.Warn("message MQTT ignoré", "topic", topic, "err", err)
	}
}

// Wait attend la fin des envois en cours ; false si timeout est dépassé.
func (n *Notifier) Wait(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		n.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// Shutdown laisse timeout aux envois en cours, puis les annule.
func (n *Notifier) Shutdown(timeout time.Duration) bool {
	ok := n.Wait(timeout)
	n.cancelSends()
	if !ok {
		n.Wait(2 * time.Second)
	}
	return ok
}

// Count24h renvoie le nombre de notifications envoyées sur les dernières 24 h.
func (n *Notifier) Count24h() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.pruneSent(n.Now())
	return len(n.sent)
}

func (n *Notifier) recordSent(now time.Time) {
	n.sent = append(n.sent, now)
	n.pruneSent(now)
}

func (n *Notifier) pruneSent(now time.Time) {
	i := slices.IndexFunc(n.sent, func(t time.Time) bool { return now.Sub(t) < 24*time.Hour })
	if i < 0 {
		n.sent = n.sent[:0]
		return
	}
	n.sent = n.sent[i:]
}

func (n *Notifier) goAsync(f func()) {
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		f()
	}()
}

func (n *Notifier) sweep() {
	n.mu.Lock()
	defer n.mu.Unlock()
	now := n.Now()
	for id, t := range n.tracked {
		if (t.ended && now.Sub(t.endedAt) > endedRetention) || now.Sub(t.lastSeen) > staleAfter {
			delete(n.tracked, id)
		}
	}
}

func (n *Notifier) uiLink(path string) string {
	if n.Config.Frigate.ExternalURL == "" {
		return ""
	}
	return n.Config.Frigate.ExternalURL + path
}

func (n *Notifier) caption(t *tracked) string {
	return buildCaption(captionData{
		Label: t.label, SubLabel: t.subLabel, Camera: t.camera, Zones: t.zones,
		Score: t.score, HasScore: t.hasScore, Start: t.start,
		Description: t.description, Link: t.link,
	}, n.Config.Location)
}

// notify marque l'événement notifié et lance l'envoi du snapshot. Appelé sous n.mu.
func (n *Notifier) notify(ctx context.Context, t *tracked, d filter.Decision) {
	now := n.Now()
	t.notified, t.silent, t.chats, t.label = true, d.Silent, d.Chats, d.Label
	t.ready = make(chan struct{})
	t.messages = map[string]sentMsg{}
	n.State.MarkNotified(filter.CooldownKey(t.camera, d.Label), now)
	n.recordSent(now)

	path := ""
	if n.Config.ForCamera(t.camera).Snapshot {
		path = t.snapshotPath
	}
	caption, silent, chats := n.caption(t), d.Silent, d.Chats
	n.Log.Info("notification", "camera", t.camera, "label", d.Label, "event_id", t.id, "silent", silent, "chats", chats)
	n.goAsync(func() { n.sendSnapshot(ctx, t, path, caption, silent, chats) })
}

// finish traite la fin d'un événement : comptage du filtrage, ou envoi du clip et du GIF. Appelé sous n.mu.
func (n *Notifier) finish(ctx context.Context, t *tracked, hasClip bool) {
	if t == nil {
		return
	}
	now := n.Now()
	t.lastSeen = now
	if !t.notified {
		if t.lastReason != "" {
			n.Metrics.EventsFiltered.WithLabelValues(t.lastReason).Inc()
		}
		delete(n.tracked, t.id)
		return
	}
	if t.ended {
		return
	}
	t.ended, t.endedAt = true, now
	cfg := n.Config.ForCamera(t.camera)
	chats := t.chats
	if cfg.Clip && hasClip {
		path := t.clipPath
		n.goAsync(func() { n.sendFollowUp(ctx, t, "video", path, chats, cfg.ClipDelay) })
	}
	if cfg.GIF && t.gifPath != "" {
		path := t.gifPath
		n.goAsync(func() { n.sendFollowUp(ctx, t, "animation", path, chats, 0) })
	}
}
```

- [ ] **Step 5: Implémenter `internal/notifier/media.go`**

```go
package notifier

import (
	"context"
	"errors"
	"html"
	"os"
	"sync"
	"time"

	"frigate-telegram/internal/frigate"
	"frigate-telegram/internal/telegram"
)

type sendFunc func(ctx context.Context, chatID int64, chat string, f telegram.InputFile) (telegram.Message, error)

// deliver envoie un contenu à plusieurs chats. Si f porte un fichier, il est uploadé
// une seule fois (premier chat qui l'accepte) ; les autres réutilisent le file_id, en parallèle.
func (n *Notifier) deliver(ctx context.Context, chats []string, kind string, f telegram.InputFile, send sendFunc, onSent func(chat string, m telegram.Message)) {
	one := func(chat string, f telegram.InputFile) (telegram.Message, error) {
		m, err := send(ctx, n.Config.ChatID(chat), chat, f)
		if err != nil {
			n.Log.Error("envoi Telegram échoué", "kind", kind, "chat", chat, "err", err)
			return m, err
		}
		n.Metrics.NotificationsSent.WithLabelValues(kind).Inc()
		if onSent != nil {
			onSent(chat, m)
		}
		return m, nil
	}
	rest := chats
	if f.Data != nil || f.Path != "" {
		for i, chat := range chats {
			rest = chats[i+1:]
			m, err := one(chat, f)
			if err != nil {
				continue // on retente l'upload avec le chat suivant
			}
			if id := m.FileID(); id != "" {
				f = telegram.InputFile{FileID: id}
			}
			break
		}
	}
	var wg sync.WaitGroup
	for _, chat := range rest {
		wg.Add(1)
		go func() {
			defer wg.Done()
			one(chat, f)
		}()
	}
	wg.Wait()
}

// fetchSnapshot récupère le snapshot (un nouvel essai si Frigate ne l'a pas encore) ; nil si indisponible.
func (n *Notifier) fetchSnapshot(ctx context.Context, path string) []byte {
	if path == "" {
		return nil
	}
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 && sleepCtx(ctx, n.SnapshotRetryDelay) != nil {
			return nil
		}
		start := time.Now()
		b, err := n.Frigate.GetBytes(ctx, path, maxPhotoSize)
		n.Metrics.MediaDownload.Observe(time.Since(start).Seconds())
		if err == nil {
			return b
		}
		n.Log.Warn("snapshot indisponible", "path", path, "attempt", attempt+1, "err", err)
		if !frigate.Retryable(err) {
			return nil
		}
	}
	return nil
}

// sendSnapshot envoie la notification initiale (photo, ou texte si pas de snapshot), puis ferme t.ready.
func (n *Notifier) sendSnapshot(ctx context.Context, t *tracked, path, caption string, silent bool, chats []string) {
	defer close(t.ready)
	markup := buttons(t.camera, t.id)
	photo := n.fetchSnapshot(ctx, path)
	if photo == nil {
		n.deliver(ctx, chats, "text", telegram.InputFile{},
			func(ctx context.Context, chatID int64, _ string, _ telegram.InputFile) (telegram.Message, error) {
				return n.Telegram.SendMessage(ctx, chatID, caption, telegram.SendOptions{Silent: silent, Markup: markup})
			},
			func(chat string, m telegram.Message) { t.setMessage(chat, sentMsg{id: m.MessageID, text: true}) })
		return
	}
	n.deliver(ctx, chats, "photo", telegram.InputFile{Name: "snapshot.jpg", Data: photo},
		func(ctx context.Context, chatID int64, _ string, f telegram.InputFile) (telegram.Message, error) {
			return n.Telegram.SendPhoto(ctx, chatID, f, telegram.SendOptions{Caption: caption, Silent: silent, Markup: markup})
		},
		func(chat string, m telegram.Message) { t.setMessage(chat, sentMsg{id: m.MessageID}) })
}

// sendFollowUp envoie le clip ("video") ou le GIF ("animation") en réponse au snapshot.
func (n *Notifier) sendFollowUp(ctx context.Context, t *tracked, kind, path string, chats []string, delay time.Duration) {
	select {
	case <-t.ready:
	case <-ctx.Done():
		return
	}
	if err := sleepCtx(ctx, delay); err != nil {
		return
	}
	file, err := n.download(ctx, path)
	if errors.Is(err, frigate.ErrTooLarge) {
		text := n.tooLargeText(path)
		n.deliver(ctx, chats, "text", telegram.InputFile{},
			func(ctx context.Context, chatID int64, chat string, _ telegram.InputFile) (telegram.Message, error) {
				return n.Telegram.SendMessage(ctx, chatID, text, telegram.SendOptions{Silent: true, ReplyTo: t.message(chat).id})
			}, nil)
		return
	}
	if err != nil {
		n.Log.Warn("média indisponible", "kind", kind, "event_id", t.id, "path", path, "err", err)
		return
	}
	defer os.Remove(file)
	name := "clip.mp4"
	if kind == "animation" {
		name = "preview.gif"
	}
	n.deliver(ctx, chats, kind, telegram.InputFile{Name: name, Path: file},
		func(ctx context.Context, chatID int64, chat string, f telegram.InputFile) (telegram.Message, error) {
			o := telegram.SendOptions{Silent: true, ReplyTo: t.message(chat).id}
			if kind == "animation" {
				return n.Telegram.SendAnimation(ctx, chatID, f, o)
			}
			return n.Telegram.SendVideo(ctx, chatID, f, o)
		}, nil)
}

// download récupère un média dans un fichier temporaire, avec les nouveaux essais de ClipRetryDelays.
func (n *Notifier) download(ctx context.Context, path string) (string, error) {
	for attempt := 0; ; attempt++ {
		start := time.Now()
		file, err := n.Frigate.DownloadToFile(ctx, path, maxUploadSize)
		n.Metrics.MediaDownload.Observe(time.Since(start).Seconds())
		if err == nil {
			return file, nil
		}
		if !frigate.Retryable(err) || attempt >= len(n.ClipRetryDelays) {
			return "", err
		}
		if serr := sleepCtx(ctx, n.ClipRetryDelays[attempt]); serr != nil {
			return "", serr
		}
	}
}

func (n *Notifier) tooLargeText(path string) string {
	text := "🎬 Clip trop volumineux pour Telegram (plus de 50 Mo)."
	if base := n.Config.Frigate.ExternalURL; base != "" {
		text += "\n<a href=\"" + html.EscapeString(base+path) + "\">Télécharger le clip</a>"
	}
	return text
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
```

- [ ] **Step 6: Implémenter `internal/notifier/events.go`**

```go
package notifier

import (
	"context"
	"net/url"

	"frigate-telegram/internal/filter"
	"frigate-telegram/internal/frigate"
)

// handleEvent traite frigate/events. Appelé sous n.mu.
func (n *Notifier) handleEvent(ctx context.Context, msg frigate.EventMessage) {
	ev := msg.After
	t := n.tracked[ev.ID]
	if msg.Type == "end" {
		n.finish(ctx, t, ev.HasClip)
		return
	}
	if msg.Type != "new" && msg.Type != "update" {
		return
	}
	now := n.Now()
	if t == nil {
		t = &tracked{
			id:           ev.ID,
			camera:       ev.Camera,
			start:        frigate.UnixTime(ev.StartTime),
			startTS:      ev.StartTime,
			eventIDs:     []string{ev.ID},
			snapshotPath: frigate.EventSnapshotPath(ev.ID),
			clipPath:     frigate.EventClipPath(ev.ID),
			gifPath:      frigate.EventGIFPath(ev.ID),
			link:         n.uiLink("/explore?event_id=" + url.QueryEscape(ev.ID)),
		}
		n.tracked[ev.ID] = t
		n.Metrics.EventsReceived.WithLabelValues(ev.Camera, ev.Label).Inc()
	}
	t.lastSeen = now
	if t.notified {
		return
	}
	t.label, t.subLabel, t.zones = ev.Label, string(ev.SubLabel), ev.EnteredZones
	t.score, t.hasScore = ev.BestScore(), true
	d := n.Engine.Evaluate(filter.Input{
		Camera:        ev.Camera,
		Labels:        []string{ev.Label},
		Score:         ev.BestScore(),
		HasScore:      true,
		Zones:         ev.EnteredZones,
		Stationary:    ev.Stationary,
		FalsePositive: ev.FalsePositive,
	}, now)
	if !d.Notify {
		t.lastReason = d.Reason
		return
	}
	n.notify(ctx, t, d)
}
```

- [ ] **Step 7: Lancer les tests**

Run: `go test ./internal/notifier/ -v`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/notifier
git commit -m "feat(notifier): mode events (snapshot, clip en réponse, cooldown, retries, repli texte)" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: Notifier, mode reviews et descriptions GenAI

**Files:**
- Create: `internal/notifier/reviews.go`
- Modify: `internal/notifier/notifier.go` (le `switch` de `Process`)
- Test: `internal/notifier/reviews_test.go`

**Interfaces:**
- Consumes: tout le notifier de la Task 10, `frigate.ParseReviewMessage`, `frigate.ParseTrackedObjectUpdate`, `frigate.RecordingClipPath`, `frigate.ReviewGIFPath`, `frigate.LatestPath`
- Produces (interne) : `handleReview(ctx, frigate.ReviewMessage)`, `handleUpdate(ctx, frigate.TrackedObjectUpdate)`, `findByEventID(id string) *tracked`

- [ ] **Step 1: Écrire les tests (qui échouent)**

`internal/notifier/reviews_test.go` :

```go
package notifier

import (
	"encoding/json"
	"strings"
	"testing"

	"frigate-telegram/internal/frigate"
)

const revID = "1790604000.5-rev"

func reviewMsg(typ, id, camera, severity string, objects, zones, detections []string) []byte {
	r := map[string]any{
		"id": id, "camera": camera, "severity": severity, "start_time": 1790604000.0,
		"data": map[string]any{"detections": detections, "objects": objects, "sub_labels": []string{}, "zones": zones},
	}
	if typ == "end" {
		r["end_time"] = 1790604030.5
	}
	b, _ := json.Marshal(map[string]any{"type": typ, "before": r, "after": r})
	return b
}

func descriptionMsg(id, text string) []byte {
	b, _ := json.Marshal(map[string]any{"type": "description", "id": id, "description": text})
	return b
}

func TestReviewAlertSendsDetectionSnapshotAndRecordingClip(t *testing.T) {
	h := newHarness(t, "reviews")
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.fr.files[frigate.RecordingClipPath("garage", 1790604000.0, 1790604030.5)] = []byte("mp4")
	h.send(t, "frigate/reviews", reviewMsg("new", revID, "garage", "alert", []string{"person"}, nil, []string{evID}))
	h.send(t, "frigate/reviews", reviewMsg("end", revID, "garage", "alert", []string{"person"}, nil, []string{evID}))

	photos := h.tg.byMethod("sendPhoto")
	if len(photos) != 2 || countData(photos, "jpeg") != 1 {
		t.Fatalf("photos = %+v", photos)
	}
	if !strings.Contains(photos[0].Opts.Markup.InlineKeyboard[0][2].CallbackData, revID) {
		t.Error("le bouton clip doit porter l'id de la review")
	}
	if videos := h.tg.byMethod("sendVideo"); len(videos) != 2 || countData(videos, "mp4") != 1 {
		t.Fatalf("vidéos = %+v", videos)
	}
}

func TestReviewDetectionSeverityIgnored(t *testing.T) {
	h := newHarness(t, "reviews")
	h.send(t, "frigate/reviews", reviewMsg("new", revID, "garage", "detection", []string{"person"}, nil, []string{evID}))
	if h.tg.count() != 0 {
		t.Error("une review de sévérité detection ne doit pas notifier")
	}
}

func TestReviewEscalatedToAlertOnUpdate(t *testing.T) {
	h := newHarness(t, "reviews")
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.send(t, "frigate/reviews", reviewMsg("new", revID, "garage", "detection", []string{"person"}, nil, []string{evID}))
	h.send(t, "frigate/reviews", reviewMsg("update", revID, "garage", "alert", []string{"person"}, nil, []string{evID}))
	if len(h.tg.byMethod("sendPhoto")) != 2 {
		t.Error("le passage en alert doit notifier")
	}
}

func TestDescriptionEditsCaption(t *testing.T) {
	h := newHarness(t, "events")
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	h.send(t, "frigate/tracked_object_update", descriptionMsg(evID, "Un livreur dépose un colis."))

	photos, edits := h.tg.byMethod("sendPhoto"), h.tg.byMethod("editMessageCaption")
	if len(edits) != 2 {
		t.Fatalf("éditions = %d, attendu 2", len(edits))
	}
	for _, e := range edits {
		p, _ := find(photos, e.ChatID)
		if e.Target != p.Result || !strings.Contains(e.Text, "livreur") {
			t.Errorf("édition incorrecte : %+v", e)
		}
	}
}

func TestDescriptionEditsTextWhenNoSnapshot(t *testing.T) {
	h := newHarness(t, "events")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	h.send(t, "frigate/tracked_object_update", descriptionMsg(evID, "Un chat."))
	if len(h.tg.byMethod("editMessageText")) != 2 {
		t.Error("les messages texte doivent être édités avec editMessageText")
	}
}

func TestDescriptionForReviewMatchesDetection(t *testing.T) {
	h := newHarness(t, "reviews")
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.send(t, "frigate/reviews", reviewMsg("new", revID, "garage", "alert", []string{"person"}, nil, []string{evID}))
	h.send(t, "frigate/tracked_object_update", descriptionMsg(evID, "Quelqu'un sonne."))
	if len(h.tg.byMethod("editMessageCaption")) != 2 {
		t.Error("la description d'une détection doit mettre à jour la review")
	}
}

func TestDescriptionForUnknownEventIgnored(t *testing.T) {
	h := newHarness(t, "events")
	h.send(t, "frigate/tracked_object_update", descriptionMsg("inconnu", "x"))
	if h.tg.count() != 0 {
		t.Error("aucun envoi attendu")
	}
}
```

- [ ] **Step 2: Vérifier l'échec**

Run: `go test ./internal/notifier/`
Expected: FAIL (reviews et descriptions non traitées, les assertions échouent)

- [ ] **Step 3: Implémenter `internal/notifier/reviews.go`**

```go
package notifier

import (
	"context"
	"net/url"
	"slices"
	"strings"

	"frigate-telegram/internal/filter"
	"frigate-telegram/internal/frigate"
)

// handleReview traite frigate/reviews. Appelé sous n.mu.
func (n *Notifier) handleReview(ctx context.Context, msg frigate.ReviewMessage) {
	r := msg.After
	t := n.tracked[r.ID]
	now := n.Now()
	if msg.Type == "end" {
		if t != nil {
			end := float64(now.Unix())
			if r.EndTime != nil {
				end = *r.EndTime
			}
			t.clipPath = frigate.RecordingClipPath(r.Camera, r.StartTime, end)
			t.eventIDs = r.Data.Detections
		}
		n.finish(ctx, t, true)
		return
	}
	if msg.Type != "new" && msg.Type != "update" {
		return
	}
	if t == nil {
		t = &tracked{
			id:      r.ID,
			camera:  r.Camera,
			start:   frigate.UnixTime(r.StartTime),
			startTS: r.StartTime,
			gifPath: frigate.ReviewGIFPath(r.ID),
			link:    n.uiLink("/review?id=" + url.QueryEscape(r.ID)),
		}
		n.tracked[r.ID] = t
		label := "review"
		if len(r.Data.Objects) > 0 {
			label = r.Data.Objects[0]
		}
		n.Metrics.EventsReceived.WithLabelValues(r.Camera, label).Inc()
	}
	t.lastSeen = now
	t.eventIDs = r.Data.Detections
	if t.notified {
		return
	}
	t.zones = r.Data.Zones
	t.subLabel = strings.Join(r.Data.SubLabels, ", ")
	t.snapshotPath = frigate.LatestPath(r.Camera)
	if len(r.Data.Detections) > 0 {
		t.snapshotPath = frigate.EventSnapshotPath(r.Data.Detections[0])
	}
	t.clipPath = frigate.RecordingClipPath(r.Camera, r.StartTime, float64(now.Unix())) // remplacé à la fin
	d := n.Engine.Evaluate(filter.Input{
		Camera:   r.Camera,
		Labels:   r.Data.Objects,
		Zones:    r.Data.Zones,
		Severity: r.Severity,
	}, now)
	if !d.Notify {
		t.lastReason = d.Reason
		return
	}
	n.notify(ctx, t, d)
}

// handleUpdate ajoute la description GenAI aux messages déjà envoyés. Appelé sous n.mu.
func (n *Notifier) handleUpdate(ctx context.Context, u frigate.TrackedObjectUpdate) {
	if u.Type != "description" || u.Description == "" {
		return
	}
	t := n.findByEventID(u.ID)
	if t == nil || !t.notified || !n.Config.ForCamera(t.camera).GenAIDescription {
		return
	}
	t.description = u.Description
	caption := n.caption(t)
	markup := buttons(t.camera, t.id)
	n.goAsync(func() {
		select {
		case <-t.ready:
		case <-ctx.Done():
			return
		}
		for chat, m := range t.messagesCopy() {
			chatID := n.Config.ChatID(chat)
			var err error
			if m.text {
				err = n.Telegram.EditMessageText(ctx, chatID, m.id, caption, markup)
			} else {
				err = n.Telegram.EditMessageCaption(ctx, chatID, m.id, caption, markup)
			}
			if err != nil {
				n.Log.Warn("mise à jour de la légende échouée", "chat", chat, "event_id", t.id, "err", err)
			}
		}
	})
}

// findByEventID retrouve un suivi par son id, ou par l'id d'un événement Frigate lié.
func (n *Notifier) findByEventID(id string) *tracked {
	if t, ok := n.tracked[id]; ok {
		return t
	}
	for _, t := range n.tracked {
		if slices.Contains(t.eventIDs, id) {
			return t
		}
	}
	return nil
}
```

- [ ] **Step 4: Brancher les topics dans `Process` (`internal/notifier/notifier.go`)**

Remplacer le `switch` de `Process` par :

```go
	switch topic {
	case n.topics.events:
		var msg frigate.EventMessage
		if msg, err = frigate.ParseEventMessage(payload); err == nil {
			n.handleEvent(ctx, msg)
		}
	case n.topics.reviews:
		var msg frigate.ReviewMessage
		if msg, err = frigate.ParseReviewMessage(payload); err == nil {
			n.handleReview(ctx, msg)
		}
	case n.topics.updates:
		var u frigate.TrackedObjectUpdate
		if u, err = frigate.ParseTrackedObjectUpdate(payload); err == nil {
			n.handleUpdate(ctx, u)
		}
	}
```

- [ ] **Step 5: Lancer les tests**

Run: `go test ./internal/notifier/ -v`
Expected: PASS (tests events et reviews)

- [ ] **Step 6: Commit**

```bash
git add internal/notifier
git commit -m "feat(notifier): mode reviews et mise à jour des légendes avec la description GenAI" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 12: Notifier, clip à la demande et /last

**Files:**
- Create: `internal/notifier/ondemand.go`
- Test: `internal/notifier/ondemand_test.go`

**Interfaces:**
- Consumes: Tasks 10 et 11
- Produces :
  - `(*Notifier).SendClipTo(ctx context.Context, chatID int64, replyTo int, id string) error`
  - `(*Notifier).SendLast(ctx context.Context, chatID int64, camera string) error`

- [ ] **Step 1: Écrire les tests (qui échouent)**

`internal/notifier/ondemand_test.go` :

```go
package notifier

import (
	"context"
	"testing"

	"frigate-telegram/internal/frigate"
)

func TestSendClipToTrackedEvent(t *testing.T) {
	h := newHarness(t, "events")
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.fr.files[frigate.EventClipPath(evID)] = []byte("mp4")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	if err := h.n.SendClipTo(context.Background(), 1, 42, evID); err != nil {
		t.Fatal(err)
	}
	v := h.tg.byMethod("sendVideo")
	if len(v) != 1 || v[0].ChatID != 1 || v[0].Opts.ReplyTo != 42 || v[0].Data != "mp4" {
		t.Errorf("vidéo = %+v", v)
	}
}

func TestSendClipToUnknownEvent(t *testing.T) {
	h := newHarness(t, "events")
	h.fr.files[frigate.EventClipPath("ancien")] = []byte("mp4")
	if err := h.n.SendClipTo(context.Background(), 1, 0, "ancien"); err != nil {
		t.Fatal(err)
	}
	if len(h.tg.byMethod("sendVideo")) != 1 {
		t.Error("vidéo attendue")
	}
}

func TestSendClipToUnknownReview(t *testing.T) {
	h := newHarness(t, "reviews")
	end := 1790604030.5
	h.fr.reviews[revID] = frigate.Review{ID: revID, Camera: "garage", StartTime: 1790604000.0, EndTime: &end}
	h.fr.files[frigate.RecordingClipPath("garage", 1790604000.0, end)] = []byte("mp4")
	if err := h.n.SendClipTo(context.Background(), 1, 0, revID); err != nil {
		t.Fatal(err)
	}
	if len(h.tg.byMethod("sendVideo")) != 1 {
		t.Error("vidéo attendue")
	}
}

func TestSendClipToMissingClipReturnsError(t *testing.T) {
	h := newHarness(t, "events")
	if err := h.n.SendClipTo(context.Background(), 1, 0, "absent"); err == nil {
		t.Error("erreur attendue")
	}
}

func TestSendLast(t *testing.T) {
	h := newHarness(t, "events")
	end := 1790604030.0
	h.fr.events = []frigate.APIEvent{{ID: evID, Camera: "garage", Label: "person", StartTime: 1790604000.0, EndTime: &end, HasSnapshot: true, HasClip: true}}
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.fr.files[frigate.EventClipPath(evID)] = []byte("mp4")
	if err := h.n.SendLast(context.Background(), 1, ""); err != nil {
		t.Fatal(err)
	}
	photos, videos := h.tg.byMethod("sendPhoto"), h.tg.byMethod("sendVideo")
	if len(photos) != 1 || len(videos) != 1 || videos[0].Opts.ReplyTo != photos[0].Result {
		t.Errorf("photos=%+v vidéos=%+v", photos, videos)
	}
}

func TestSendLastWithoutEvents(t *testing.T) {
	h := newHarness(t, "events")
	if err := h.n.SendLast(context.Background(), 1, "garage"); err != nil {
		t.Fatal(err)
	}
	if m := h.tg.byMethod("sendMessage"); len(m) != 1 || m[0].Text != "Aucun événement trouvé." {
		t.Errorf("messages = %+v", m)
	}
}
```

- [ ] **Step 2: Vérifier l'échec**

Run: `go test ./internal/notifier/`
Expected: FAIL (`h.n.SendClipTo undefined`)

- [ ] **Step 3: Implémenter `internal/notifier/ondemand.go`**

```go
package notifier

import (
	"context"
	"errors"
	"net/url"
	"os"

	"frigate-telegram/internal/config"
	"frigate-telegram/internal/frigate"
	"frigate-telegram/internal/telegram"
)

// SendClipTo envoie le clip d'un événement ou d'une review à un chat, en réponse à replyTo.
func (n *Notifier) SendClipTo(ctx context.Context, chatID int64, replyTo int, id string) error {
	path, err := n.clipPathFor(ctx, id)
	if err != nil {
		return err
	}
	return n.sendClipFile(ctx, chatID, replyTo, path)
}

func (n *Notifier) clipPathFor(ctx context.Context, id string) (string, error) {
	n.mu.Lock()
	path := ""
	if t := n.findByEventID(id); t != nil {
		path = t.clipPath
		if n.Config.Mode == config.ModeReviews && !t.ended {
			path = frigate.RecordingClipPath(t.camera, t.startTS, float64(n.Now().Unix()))
		}
	}
	n.mu.Unlock()
	if path != "" {
		return path, nil
	}
	if n.Config.Mode == config.ModeReviews {
		if r, err := n.Frigate.Review(ctx, id); err == nil {
			end := float64(n.Now().Unix())
			if r.EndTime != nil {
				end = *r.EndTime
			}
			return frigate.RecordingClipPath(r.Camera, r.StartTime, end), nil
		}
	}
	return frigate.EventClipPath(id), nil
}

func (n *Notifier) sendClipFile(ctx context.Context, chatID int64, replyTo int, path string) error {
	file, err := n.download(ctx, path)
	if errors.Is(err, frigate.ErrTooLarge) {
		_, err = n.Telegram.SendMessage(ctx, chatID, n.tooLargeText(path), telegram.SendOptions{ReplyTo: replyTo})
		return err
	}
	if err != nil {
		return err
	}
	defer os.Remove(file)
	_, err = n.Telegram.SendVideo(ctx, chatID, telegram.InputFile{Name: "clip.mp4", Path: file}, telegram.SendOptions{ReplyTo: replyTo})
	if err == nil {
		n.Metrics.NotificationsSent.WithLabelValues("video").Inc()
	}
	return err
}

// SendLast renvoie le dernier événement (d'une caméra si camera n'est pas vide) : snapshot puis clip.
func (n *Notifier) SendLast(ctx context.Context, chatID int64, camera string) error {
	evs, err := n.Frigate.Events(ctx, camera, 1)
	if err != nil {
		return err
	}
	if len(evs) == 0 {
		_, err := n.Telegram.SendMessage(ctx, chatID, "Aucun événement trouvé.", telegram.SendOptions{})
		return err
	}
	ev := evs[0]
	caption := buildCaption(captionData{
		Label: ev.Label, SubLabel: string(ev.SubLabel), Camera: ev.Camera, Zones: ev.Zones,
		Score: ev.Score(), HasScore: true, Start: frigate.UnixTime(ev.StartTime),
		Link: n.uiLink("/explore?event_id=" + url.QueryEscape(ev.ID)),
	}, n.Config.Location)
	markup := buttons(ev.Camera, ev.ID)

	var msg telegram.Message
	if ev.HasSnapshot {
		if photo := n.fetchSnapshot(ctx, frigate.EventSnapshotPath(ev.ID)); photo != nil {
			msg, err = n.Telegram.SendPhoto(ctx, chatID, telegram.InputFile{Name: "snapshot.jpg", Data: photo},
				telegram.SendOptions{Caption: caption, Markup: markup})
			if err != nil {
				return err
			}
		}
	}
	if msg.MessageID == 0 {
		if msg, err = n.Telegram.SendMessage(ctx, chatID, caption, telegram.SendOptions{Markup: markup}); err != nil {
			return err
		}
	}
	if ev.HasClip && ev.EndTime != nil {
		return n.sendClipFile(ctx, chatID, msg.MessageID, frigate.EventClipPath(ev.ID))
	}
	return nil
}
```

- [ ] **Step 4: Lancer les tests**

Run: `go test ./internal/notifier/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/notifier
git commit -m "feat(notifier): envoi de clip à la demande et dernier événement" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 13: Client MQTT

**Files:**
- Create: `internal/mqttsub/mqttsub.go`
- Test: `internal/mqttsub/mqttsub_test.go`

**Interfaces:**
- Consumes: `config.MQTT` (Task 1)
- Produces :
  - `type Handler func(topic string, payload []byte)`
  - `mqttsub.New(cfg config.MQTT, topics []string, h Handler, log *slog.Logger, onState func(connected bool)) *Subscriber`
  - `(*Subscriber).Start()`, `Connected() bool`, `Stop()`

- [ ] **Step 1: Ajouter les dépendances**

```bash
go get github.com/eclipse/paho.mqtt.golang
go get github.com/mochi-mqtt/server/v2
```

- [ ] **Step 2: Écrire le test (qui échoue)**

`internal/mqttsub/mqttsub_test.go` :

```go
package mqttsub

import (
	"log/slog"
	"net"
	"testing"
	"time"

	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"

	"frigate-telegram/internal/config"
)

func startBroker(t *testing.T) (*mochi.Server, string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()

	srv := mochi.New(&mochi.Options{InlineClient: true})
	if err := srv.AddHook(new(auth.AllowHook), nil); err != nil {
		t.Fatal(err)
	}
	if err := srv.AddListener(listeners.NewTCP(listeners.Config{ID: "test", Address: addr})); err != nil {
		t.Fatal(err)
	}
	go srv.Serve()
	t.Cleanup(func() { srv.Close() })
	return srv, "tcp://" + addr
}

func TestSubscriberReceivesMessages(t *testing.T) {
	srv, url := startBroker(t)
	got := make(chan string, 10)
	s := New(config.MQTT{Broker: url, ClientID: "test"}, []string{"frigate/events"},
		func(topic string, p []byte) {
			select {
			case got <- topic + "|" + string(p):
			default:
			}
		},
		slog.New(slog.DiscardHandler), nil)
	s.Start()
	defer s.Stop()

	deadline := time.After(10 * time.Second)
	for {
		// On publie en boucle : l'abonnement se fait de façon asynchrone après la connexion.
		srv.Publish("frigate/events", []byte("hello"), false, 0)
		select {
		case v := <-got:
			if v != "frigate/events|hello" {
				t.Fatalf("reçu %q", v)
			}
			if !s.Connected() {
				t.Error("Connected() doit être vrai")
			}
			return
		case <-time.After(100 * time.Millisecond):
		case <-deadline:
			t.Fatal("aucun message reçu en 10 s")
		}
	}
}
```

- [ ] **Step 3: Vérifier l'échec**

Run: `go test ./internal/mqttsub/`
Expected: FAIL (`undefined: New`)

> Si `listeners.NewTCP` n'accepte pas `listeners.Config` dans la version résolue de mochi, lancer `go doc github.com/mochi-mqtt/server/v2/listeners NewTCP` et adapter l'appel du test : les anciennes versions prennent `NewTCP(id, address string, config *listeners.Config)`.

- [ ] **Step 4: Implémenter `internal/mqttsub/mqttsub.go`**

```go
// Package mqttsub gère la connexion au broker MQTT et l'abonnement aux topics de Frigate.
package mqttsub

import (
	"crypto/tls"
	"log/slog"
	"sync/atomic"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"frigate-telegram/internal/config"
)

type Handler func(topic string, payload []byte)

type Subscriber struct {
	client    mqtt.Client
	connected atomic.Bool
}

func New(cfg config.MQTT, topics []string, h Handler, log *slog.Logger, onState func(connected bool)) *Subscriber {
	s := &Subscriber{}
	setState := func(up bool) {
		s.connected.Store(up)
		if onState != nil {
			onState(up)
		}
	}
	opts := mqtt.NewClientOptions().
		AddBroker(cfg.Broker).
		SetClientID(cfg.ClientID).
		SetUsername(cfg.Username).
		SetPassword(cfg.Password).
		SetCleanSession(true).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second).
		SetMaxReconnectInterval(30 * time.Second).
		SetKeepAlive(30 * time.Second)
	if cfg.InsecureSkipVerify {
		opts.SetTLSConfig(&tls.Config{InsecureSkipVerify: true}) //nolint:gosec // option explicite de l'utilisateur
	}
	opts.SetOnConnectHandler(func(c mqtt.Client) {
		filters := make(map[string]byte, len(topics))
		for _, t := range topics {
			filters[t] = 0
		}
		tok := c.SubscribeMultiple(filters, func(_ mqtt.Client, m mqtt.Message) { h(m.Topic(), m.Payload()) })
		go func() { // ne jamais attendre un token dans un handler paho
			tok.Wait()
			if err := tok.Error(); err != nil {
				log.Error("abonnement MQTT échoué", "err", err)
				return
			}
			log.Info("MQTT connecté", "broker", cfg.Broker, "topics", topics)
		}()
		setState(true)
	})
	opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		setState(false)
		log.Warn("connexion MQTT perdue", "err", err)
	})
	opts.SetReconnectingHandler(func(mqtt.Client, *mqtt.ClientOptions) {
		log.Info("reconnexion MQTT…")
	})
	s.client = mqtt.NewClient(opts)
	return s
}

// Start lance la connexion ; les échecs sont retentés en arrière-plan.
func (s *Subscriber) Start() { s.client.Connect() }

func (s *Subscriber) Connected() bool { return s.connected.Load() }

func (s *Subscriber) Stop() {
	s.client.Disconnect(250)
	s.connected.Store(false)
}
```

- [ ] **Step 5: Lancer le test**

Run: `go test ./internal/mqttsub/ -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/mqttsub
git commit -m "feat(mqttsub): client MQTT avec reconnexion automatique" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 14: Bot, commandes et boutons

**Files:**
- Create: `internal/bot/bot.go`
- Create: `internal/bot/commands.go`
- Test: `internal/bot/bot_test.go`

**Interfaces:**
- Consumes: `config` (T1), `frigate.LatestPath` (T3), `state.Store`, `state.Forever` (T4), types `telegram` (T7), `actions` (T8). Le notifier (T10 à T12) satisfait `bot.Notifier`.
- Produces :
  - `type Telegram interface { GetUpdates(ctx, offset int, timeout time.Duration) ([]telegram.Update, error); SetMyCommands(ctx, []telegram.BotCommand) error; SendMessage(...); SendPhoto(...); AnswerCallbackQuery(ctx, id, text string) error }`
  - `type Frigate interface { Cameras(ctx) ([]string, error); GetBytes(ctx, path string, max int64) ([]byte, error) }`
  - `type Notifier interface { SendClipTo(ctx, chatID int64, replyTo int, id string) error; SendLast(ctx, chatID int64, camera string) error; Count24h() int }`
  - `type Deps struct { Config *config.Config; Telegram Telegram; Frigate Frigate; Notifier Notifier; State *state.Store; Log *slog.Logger; Now func() time.Time; MQTTConnected func() bool }`
  - `bot.New(d Deps) *Bot`, `(*Bot).Run(ctx)`, `HandleUpdate(ctx, telegram.Update)`, `LastPoll() time.Time`, `Wait()`
  - `bot.ParseDuration(s string) (time.Duration, error)` (`0` signifie illimité)

- [ ] **Step 1: Écrire les tests (qui échouent)**

`internal/bot/bot_test.go` :

```go
package bot

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"frigate-telegram/internal/config"
	"frigate-telegram/internal/state"
	"frigate-telegram/internal/telegram"
)

type fakeTG struct {
	mu        sync.Mutex
	messages  []string
	photos    []string
	answers   []string
	keyboards int
}

func (f *fakeTG) GetUpdates(context.Context, int, time.Duration) ([]telegram.Update, error) {
	return nil, nil
}
func (f *fakeTG) SetMyCommands(context.Context, []telegram.BotCommand) error { return nil }
func (f *fakeTG) SendMessage(_ context.Context, _ int64, text string, o telegram.SendOptions) (telegram.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, text)
	if o.Markup != nil {
		f.keyboards++
	}
	return telegram.Message{MessageID: len(f.messages)}, nil
}
func (f *fakeTG) SendPhoto(_ context.Context, _ int64, _ telegram.InputFile, o telegram.SendOptions) (telegram.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.photos = append(f.photos, o.Caption)
	return telegram.Message{MessageID: 1}, nil
}
func (f *fakeTG) AnswerCallbackQuery(_ context.Context, _, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers = append(f.answers, text)
	return nil
}
func (f *fakeTG) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.messages) == 0 {
		return ""
	}
	return f.messages[len(f.messages)-1]
}

type fakeFR struct{}

func (fakeFR) Cameras(context.Context) ([]string, error)                { return []string{"garage", "jardin"}, nil }
func (fakeFR) GetBytes(context.Context, string, int64) ([]byte, error) { return []byte("jpeg"), nil }

type fakeNotif struct {
	mu    sync.Mutex
	clips []string
	lasts []string
}

func (f *fakeNotif) SendClipTo(_ context.Context, chatID int64, replyTo int, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clips = append(f.clips, fmt.Sprintf("%d/%d/%s", chatID, replyTo, id))
	return nil
}
func (f *fakeNotif) SendLast(_ context.Context, _ int64, camera string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lasts = append(f.lasts, camera)
	return nil
}
func (f *fakeNotif) Count24h() int { return 3 }

const cfgYAML = `
timezone: Europe/Paris
frigate: {url: "http://f"}
mqtt: {broker: "tcp://m:1883"}
telegram: {token: t, admins: [1], chats: {moi: 1}}
cameras:
  salon: {enabled: false}
`

var now = time.Date(2026, 9, 28, 14, 0, 0, 0, time.UTC)

type env struct {
	b     *Bot
	tg    *fakeTG
	notif *fakeNotif
	st    *state.Store
}

func newEnv(t *testing.T) env {
	t.Helper()
	cfg, err := config.Parse([]byte(cfgYAML), func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	st, _ := state.Load(filepath.Join(t.TempDir(), "state.json"), now)
	e := env{tg: &fakeTG{}, notif: &fakeNotif{}, st: st}
	e.b = New(Deps{Config: cfg, Telegram: e.tg, Frigate: fakeFR{}, Notifier: e.notif, State: st,
		Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return now }, MQTTConnected: func() bool { return true }})
	return e
}

func (e env) cmd(text string, from int64) {
	e.b.HandleUpdate(context.Background(), telegram.Update{Message: &telegram.Message{
		MessageID: 10, From: &telegram.User{ID: from}, Chat: telegram.Chat{ID: from}, Text: text}})
	e.b.Wait()
}

func (e env) callback(data string, from int64) {
	e.b.HandleUpdate(context.Background(), telegram.Update{CallbackQuery: &telegram.CallbackQuery{
		ID: "q", From: telegram.User{ID: from}, Data: data,
		Message: &telegram.Message{MessageID: 77, Chat: telegram.Chat{ID: 1}}}})
	e.b.Wait()
}

func TestParseCommand(t *testing.T) {
	name, args := parseCommand("/Pause@MonBot 30m jardin")
	if name != "pause" || len(args) != 2 || args[0] != "30m" || args[1] != "jardin" {
		t.Errorf("parseCommand = %q %v", name, args)
	}
}

func TestParseDuration(t *testing.T) {
	ok := map[string]time.Duration{"30m": 30 * time.Minute, "2h": 2 * time.Hour, "1h30m": 90 * time.Minute, "1d": 24 * time.Hour, "0": 0}
	for s, want := range ok {
		if got, err := ParseDuration(s); err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %v, %v", s, got, err)
		}
	}
	for _, s := range []string{"jardin", "-1h", "xd", ""} {
		if _, err := ParseDuration(s); err == nil {
			t.Errorf("ParseDuration(%q) aurait dû échouer", s)
		}
	}
}

func TestPauseGlobalDefaultsToOneHour(t *testing.T) {
	e := newEnv(t)
	e.cmd("/pause", 1)
	if !e.st.IsPaused(now.Add(59*time.Minute)) || e.st.IsPaused(now.Add(61*time.Minute)) {
		t.Error("pause d'une heure attendue")
	}
	if !strings.Contains(e.tg.last(), "jusqu'à 28/09 17:00") {
		t.Errorf("réponse = %q", e.tg.last())
	}
}

func TestPauseCameraWithDuration(t *testing.T) {
	e := newEnv(t)
	e.cmd("/pause jardin 2h", 1)
	if !e.st.IsMuted("jardin", now.Add(time.Hour)) || e.st.IsPaused(now) {
		t.Error("seule la caméra jardin doit être coupée")
	}
}

func TestPauseZeroIsForever(t *testing.T) {
	e := newEnv(t)
	e.cmd("/pause 0", 1)
	if !e.st.IsPaused(now.AddDate(1, 0, 0)) || !strings.Contains(e.tg.last(), "/resume") {
		t.Errorf("pause illimitée attendue, réponse %q", e.tg.last())
	}
}

func TestPauseUnknownCamera(t *testing.T) {
	e := newEnv(t)
	e.cmd("/pause cuisine", 1)
	if e.st.IsMuted("cuisine", now) || !strings.Contains(e.tg.last(), "Caméra inconnue") {
		t.Errorf("réponse = %q", e.tg.last())
	}
}

func TestResume(t *testing.T) {
	e := newEnv(t)
	e.cmd("/pause", 1)
	e.cmd("/pause jardin", 1)
	e.cmd("/resume jardin", 1)
	if e.st.IsMuted("jardin", now) || !e.st.IsPaused(now) {
		t.Error("/resume jardin ne doit lever que la caméra")
	}
	e.cmd("/resume", 1)
	if e.st.IsPaused(now) {
		t.Error("/resume doit tout reprendre")
	}
}

func TestUnauthorizedUserIgnored(t *testing.T) {
	e := newEnv(t)
	e.cmd("/pause", 999)
	if e.st.IsPaused(now) || len(e.tg.messages) != 0 {
		t.Error("un non-admin ne doit rien pouvoir faire")
	}
}

func TestStatus(t *testing.T) {
	e := newEnv(t)
	e.cmd("/pause jardin 1h", 1)
	e.cmd("/status", 1)
	out := e.tg.last()
	for _, want := range []string{"MQTT : ✅", "Notifications actives", "🔇 jardin", "3 notification(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("status sans %q :\n%s", want, out)
		}
	}
}

func TestCameras(t *testing.T) {
	e := newEnv(t)
	e.cmd("/cameras", 1)
	if out := e.tg.last(); !strings.Contains(out, "✅ garage") || !strings.Contains(out, "✅ jardin") {
		t.Errorf("cameras = %q", out)
	}
}

func TestSnapshotCommands(t *testing.T) {
	e := newEnv(t)
	e.cmd("/snapshot", 1)
	if e.tg.keyboards != 1 {
		t.Error("un clavier de sélection est attendu sans argument")
	}
	e.cmd("/snapshot jardin", 1)
	if len(e.tg.photos) != 1 || !strings.Contains(e.tg.photos[0], "jardin") {
		t.Errorf("photos = %v", e.tg.photos)
	}
}

func TestLast(t *testing.T) {
	e := newEnv(t)
	e.cmd("/last garage", 1)
	if len(e.notif.lasts) != 1 || e.notif.lasts[0] != "garage" {
		t.Errorf("lasts = %v", e.notif.lasts)
	}
}

func TestCallbacks(t *testing.T) {
	e := newEnv(t)
	e.callback("m:jardin:3600", 1)
	if !e.st.IsMuted("jardin", now.Add(30*time.Minute)) {
		t.Error("bouton mute sans effet")
	}
	e.callback("p:1800", 1)
	if !e.st.IsPaused(now.Add(20 * time.Minute)) {
		t.Error("bouton pause sans effet")
	}
	e.callback("c:evt1", 1)
	if len(e.notif.clips) != 1 || e.notif.clips[0] != "1/77/evt1" {
		t.Errorf("clips = %v", e.notif.clips)
	}
	e.callback("s:garage", 1)
	if len(e.tg.photos) != 1 {
		t.Error("bouton snapshot sans effet")
	}
}

func TestCallbackUnauthorized(t *testing.T) {
	e := newEnv(t)
	e.callback("p:1800", 999)
	if e.st.IsPaused(now) || len(e.tg.answers) != 1 || !strings.Contains(e.tg.answers[0], "Non autorisé") {
		t.Errorf("answers = %v", e.tg.answers)
	}
}
```

- [ ] **Step 2: Vérifier l'échec**

Run: `go test ./internal/bot/`
Expected: FAIL (`undefined: New`)

- [ ] **Step 3: Implémenter `internal/bot/bot.go`**

```go
// Package bot traite les commandes et les boutons Telegram (long polling).
package bot

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"frigate-telegram/internal/config"
	"frigate-telegram/internal/state"
	"frigate-telegram/internal/telegram"
)

type Telegram interface {
	GetUpdates(ctx context.Context, offset int, timeout time.Duration) ([]telegram.Update, error)
	SetMyCommands(ctx context.Context, cmds []telegram.BotCommand) error
	SendMessage(ctx context.Context, chatID int64, text string, o telegram.SendOptions) (telegram.Message, error)
	SendPhoto(ctx context.Context, chatID int64, f telegram.InputFile, o telegram.SendOptions) (telegram.Message, error)
	AnswerCallbackQuery(ctx context.Context, id, text string) error
}

type Frigate interface {
	Cameras(ctx context.Context) ([]string, error)
	GetBytes(ctx context.Context, path string, max int64) ([]byte, error)
}

type Notifier interface {
	SendClipTo(ctx context.Context, chatID int64, replyTo int, id string) error
	SendLast(ctx context.Context, chatID int64, camera string) error
	Count24h() int
}

type Deps struct {
	Config        *config.Config
	Telegram      Telegram
	Frigate       Frigate
	Notifier      Notifier
	State         *state.Store
	Log           *slog.Logger
	Now           func() time.Time
	MQTTConnected func() bool
}

type Bot struct {
	Deps
	lastPoll atomic.Int64
	wg       sync.WaitGroup
}

func New(d Deps) *Bot {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.MQTTConnected == nil {
		d.MQTTConnected = func() bool { return false }
	}
	b := &Bot{Deps: d}
	b.lastPoll.Store(d.Now().UnixNano())
	return b
}

// LastPoll renvoie l'heure du dernier getUpdates réussi (utilisé par /healthz).
func (b *Bot) LastPoll() time.Time { return time.Unix(0, b.lastPoll.Load()) }

// Wait attend la fin des actions lancées en arrière-plan.
func (b *Bot) Wait() { b.wg.Wait() }

func (b *Bot) async(f func()) {
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		f()
	}()
}

var commands = []telegram.BotCommand{
	{Command: "pause", Description: "Mettre en pause : /pause [durée] [caméra]"},
	{Command: "resume", Description: "Reprendre : /resume [caméra]"},
	{Command: "status", Description: "État du service"},
	{Command: "cameras", Description: "Liste des caméras"},
	{Command: "snapshot", Description: "Image en direct : /snapshot [caméra]"},
	{Command: "last", Description: "Dernier événement : /last [caméra]"},
	{Command: "help", Description: "Aide"},
}

// Run fait du long polling jusqu'à l'annulation de ctx.
func (b *Bot) Run(ctx context.Context) {
	if err := b.Telegram.SetMyCommands(ctx, commands); err != nil {
		b.Log.Warn("setMyCommands échoué", "err", err)
	}
	offset := 0
	for ctx.Err() == nil {
		updates, err := b.Telegram.GetUpdates(ctx, offset, 50*time.Second)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			b.Log.Warn("getUpdates échoué", "err", err)
			sleep(ctx, 5*time.Second)
			continue
		}
		b.lastPoll.Store(b.Now().UnixNano())
		for _, u := range updates {
			offset = u.UpdateID + 1
			b.HandleUpdate(ctx, u)
		}
	}
	b.wg.Wait()
}

// HandleUpdate route une mise à jour vers la commande ou le bouton concerné.
func (b *Bot) HandleUpdate(ctx context.Context, u telegram.Update) {
	switch {
	case u.CallbackQuery != nil:
		b.handleCallback(ctx, *u.CallbackQuery)
	case u.Message != nil && len(u.Message.Text) > 0 && u.Message.Text[0] == '/':
		b.handleCommand(ctx, *u.Message)
	}
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
```

- [ ] **Step 4: Implémenter `internal/bot/commands.go`**

```go
package bot

import (
	"context"
	"fmt"
	"html"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"frigate-telegram/internal/actions"
	"frigate-telegram/internal/frigate"
	"frigate-telegram/internal/state"
	"frigate-telegram/internal/telegram"
)

const helpText = `<b>Commandes</b>
/pause [durée] [caméra] — pause (1 h par défaut, 0 = jusqu'à /resume)
/resume [caméra] — reprendre (sans argument : tout reprendre)
/status — état du service
/cameras — liste des caméras
/snapshot [caméra] — image en direct
/last [caméra] — dernier événement
Durées : 30m, 2h, 1h30m, 1d`

var esc = html.EscapeString

// parseCommand découpe "/cmd@bot arg1 arg2" en ("cmd", [arg1 arg2]).
func parseCommand(text string) (string, []string) {
	f := strings.Fields(text)
	if len(f) == 0 {
		return "", nil
	}
	name := strings.TrimPrefix(f[0], "/")
	if i := strings.IndexByte(name, '@'); i >= 0 {
		name = name[:i]
	}
	return strings.ToLower(name), f[1:]
}

// ParseDuration accepte "30m", "2h", "1h30m", "1d" et "0" (illimité).
func ParseDuration(s string) (time.Duration, error) {
	if s == "0" {
		return 0, nil
	}
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("durée invalide %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("durée invalide %q", s)
	}
	return d, nil
}

func (b *Bot) handleCommand(ctx context.Context, m telegram.Message) {
	if m.From == nil || !b.Config.IsAdmin(m.From.ID) {
		from := int64(0)
		if m.From != nil {
			from = m.From.ID
		}
		b.Log.Warn("commande refusée : utilisateur non autorisé", "user", from, "text", m.Text)
		return
	}
	name, args := parseCommand(m.Text)
	chat := m.Chat.ID
	switch name {
	case "pause":
		b.reply(ctx, chat, b.cmdPause(ctx, args))
	case "resume":
		b.reply(ctx, chat, b.cmdResume(args))
	case "status":
		b.reply(ctx, chat, b.cmdStatus())
	case "cameras":
		b.reply(ctx, chat, b.cmdCameras(ctx))
	case "snapshot":
		b.async(func() { b.cmdSnapshot(ctx, chat, args) })
	case "last":
		camera := ""
		if len(args) > 0 {
			camera = args[0]
		}
		b.async(func() {
			if err := b.Notifier.SendLast(ctx, chat, camera); err != nil {
				b.reply(ctx, chat, "⚠️ "+esc(err.Error()))
			}
		})
	case "help", "start":
		b.reply(ctx, chat, helpText)
	default:
		b.reply(ctx, chat, "Commande inconnue. /help")
	}
}

func (b *Bot) cmdPause(ctx context.Context, args []string) string {
	dur, camera := time.Hour, ""
	for _, a := range args {
		if d, err := ParseDuration(a); err == nil {
			dur = d
			continue
		}
		camera = a
	}
	if camera != "" {
		if err := b.checkCamera(ctx, camera); err != nil {
			return err.Error()
		}
	}
	until := state.Forever
	if dur > 0 {
		until = b.Now().Add(dur)
	}
	var err error
	target := "Notifications"
	if camera == "" {
		err = b.State.Pause(until)
	} else {
		err = b.State.Mute(camera, until)
		target = "Caméra <b>" + esc(camera) + "</b>"
	}
	if err != nil {
		return "⚠️ Impossible d'enregistrer l'état : " + esc(err.Error())
	}
	return "⏸ " + target + " en pause " + b.untilText(until)
}

func (b *Bot) cmdResume(args []string) string {
	if len(args) == 0 {
		if err := b.State.Resume(); err != nil {
			return "⚠️ " + esc(err.Error())
		}
		return "▶️ Notifications réactivées (toutes les caméras)."
	}
	if err := b.State.Unmute(args[0]); err != nil {
		return "⚠️ " + esc(err.Error())
	}
	return "▶️ Caméra <b>" + esc(args[0]) + "</b> réactivée."
}

func (b *Bot) cmdStatus() string {
	now := b.Now()
	st := b.State.Status(now)
	var sb strings.Builder
	sb.WriteString("<b>État</b>\n")
	if b.MQTTConnected() {
		sb.WriteString("MQTT : ✅ connecté\n")
	} else {
		sb.WriteString("MQTT : ❌ déconnecté\n")
	}
	if st.PausedUntil.IsZero() {
		sb.WriteString("▶️ Notifications actives")
	} else {
		sb.WriteString("⏸ Pause globale " + b.untilText(st.PausedUntil))
	}
	for _, cam := range slices.Sorted(maps.Keys(st.Mutes)) {
		sb.WriteString("\n🔇 " + esc(cam) + " " + b.untilText(st.Mutes[cam]))
	}
	fmt.Fprintf(&sb, "\n📨 %d notification(s) sur 24 h", b.Notifier.Count24h())
	return sb.String()
}

func (b *Bot) cmdCameras(ctx context.Context) string {
	cams, err := b.Frigate.Cameras(ctx)
	if err != nil {
		return "⚠️ Frigate injoignable : " + esc(err.Error())
	}
	now := b.Now()
	var sb strings.Builder
	sb.WriteString("<b>Caméras</b>")
	for _, cam := range cams {
		icon := "✅"
		switch {
		case !b.Config.ForCamera(cam).Enabled:
			icon = "🚫"
		case b.State.IsMuted(cam, now):
			icon = "🔇"
		}
		sb.WriteString("\n" + icon + " " + esc(cam))
	}
	return sb.String()
}

func (b *Bot) cmdSnapshot(ctx context.Context, chat int64, args []string) {
	if len(args) > 0 {
		b.sendLiveSnapshot(ctx, chat, args[0])
		return
	}
	cams, err := b.Frigate.Cameras(ctx)
	if err != nil || len(cams) == 0 {
		b.reply(ctx, chat, "⚠️ Impossible de lister les caméras.")
		return
	}
	var rows [][]telegram.InlineKeyboardButton
	for i, cam := range cams {
		if i%2 == 0 {
			rows = append(rows, nil)
		}
		rows[len(rows)-1] = append(rows[len(rows)-1], telegram.InlineKeyboardButton{Text: cam, CallbackData: actions.Snapshot(cam)})
	}
	if _, err := b.Telegram.SendMessage(ctx, chat, "📷 Choisis une caméra :",
		telegram.SendOptions{Markup: &telegram.InlineKeyboardMarkup{InlineKeyboard: rows}}); err != nil {
		b.Log.Warn("envoi du clavier échoué", "err", err)
	}
}

func (b *Bot) sendLiveSnapshot(ctx context.Context, chat int64, camera string) {
	img, err := b.Frigate.GetBytes(ctx, frigate.LatestPath(camera), 10<<20)
	if err != nil {
		b.reply(ctx, chat, "⚠️ Snapshot indisponible pour "+esc(camera))
		return
	}
	caption := "📷 <b>" + esc(camera) + "</b> — " + b.Now().In(b.Config.Location).Format("15:04:05")
	if _, err := b.Telegram.SendPhoto(ctx, chat, telegram.InputFile{Name: camera + ".jpg", Data: img},
		telegram.SendOptions{Caption: caption}); err != nil {
		b.Log.Warn("envoi du snapshot échoué", "camera", camera, "err", err)
	}
}

func (b *Bot) handleCallback(ctx context.Context, q telegram.CallbackQuery) {
	if !b.Config.IsAdmin(q.From.ID) {
		b.Log.Warn("bouton refusé : utilisateur non autorisé", "user", q.From.ID)
		b.answer(ctx, q.ID, "⛔ Non autorisé")
		return
	}
	a, err := actions.Parse(q.Data)
	if err != nil {
		b.answer(ctx, q.ID, "Action inconnue")
		return
	}
	var chat int64
	replyTo := 0
	if q.Message != nil {
		chat, replyTo = q.Message.Chat.ID, q.Message.MessageID
	}
	now := b.Now()
	switch a.Kind {
	case actions.KindMute:
		until := now.Add(a.Duration)
		if err := b.State.Mute(a.Camera, until); err != nil {
			b.answer(ctx, q.ID, "⚠️ Erreur d'enregistrement")
			return
		}
		b.answer(ctx, q.ID, "🔇 "+a.Camera+" coupée "+b.untilText(until))
	case actions.KindPause:
		until := now.Add(a.Duration)
		if err := b.State.Pause(until); err != nil {
			b.answer(ctx, q.ID, "⚠️ Erreur d'enregistrement")
			return
		}
		b.answer(ctx, q.ID, "⏸ Pause "+b.untilText(until))
	case actions.KindClip, actions.KindSnapshot:
		if chat == 0 {
			b.answer(ctx, q.ID, "⚠️ Message trop ancien")
			return
		}
		if a.Kind == actions.KindClip {
			b.answer(ctx, q.ID, "🎬 Envoi du clip…")
			b.async(func() {
				if err := b.Notifier.SendClipTo(ctx, chat, replyTo, a.ID); err != nil {
					b.reply(ctx, chat, "⚠️ Clip indisponible : "+esc(err.Error()))
				}
			})
			return
		}
		b.answer(ctx, q.ID, "")
		b.async(func() { b.sendLiveSnapshot(ctx, chat, a.Camera) })
	}
}

// checkCamera vérifie que la caméra existe dans Frigate (accepte si Frigate est injoignable).
func (b *Bot) checkCamera(ctx context.Context, camera string) error {
	cams, err := b.Frigate.Cameras(ctx)
	if err != nil || slices.Contains(cams, camera) {
		return nil
	}
	return fmt.Errorf("Caméra inconnue : %s. Caméras : %s", esc(camera), esc(strings.Join(cams, ", ")))
}

func (b *Bot) untilText(until time.Time) string {
	if !until.Before(state.Forever) {
		return "jusqu'à /resume"
	}
	return "jusqu'à " + until.In(b.Config.Location).Format("02/01 15:04")
}

func (b *Bot) reply(ctx context.Context, chat int64, text string) {
	if _, err := b.Telegram.SendMessage(ctx, chat, text, telegram.SendOptions{}); err != nil {
		b.Log.Warn("réponse Telegram échouée", "chat", chat, "err", err)
	}
}

func (b *Bot) answer(ctx context.Context, id, text string) {
	if err := b.Telegram.AnswerCallbackQuery(ctx, id, text); err != nil {
		b.Log.Warn("answerCallbackQuery échoué", "err", err)
	}
}
```

- [ ] **Step 5: Lancer les tests**

Run: `go test ./internal/bot/ -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/bot
git commit -m "feat(bot): commandes /pause /resume /status /cameras /snapshot /last et boutons" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 15: Serveur HTTP et point d'entrée

**Files:**
- Create: `internal/server/server.go`
- Test: `internal/server/server_test.go`
- Create: `cmd/frigate-telegram/main.go`

**Interfaces:**
- Consumes: tous les packages précédents
- Produces :
  - `server.New(addr string, health func() error, reg *prometheus.Registry) *http.Server`
  - `server.Check(url string) error`
  - binaire `frigate-telegram` avec les flags `-config` (défaut `/config/config.yml`), `-healthcheck` et `-healthcheck-url` (défaut `http://127.0.0.1:8080/healthz`)

- [ ] **Step 1: Écrire le test du serveur (qui échoue)**

`internal/server/server_test.go` :

```go
package server

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestHealthzAndCheck(t *testing.T) {
	var healthy atomic.Bool
	healthy.Store(true)
	srv := New(":0", func() error {
		if healthy.Load() {
			return nil
		}
		return errors.New("MQTT déconnecté")
	}, prometheus.NewRegistry())
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	if err := Check(ts.URL + "/healthz"); err != nil {
		t.Fatalf("sain : %v", err)
	}
	healthy.Store(false)
	err := Check(ts.URL + "/healthz")
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("err = %v, attendu 503", err)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := prometheus.NewCounter(prometheus.CounterOpts{Name: "ft_test_total", Help: "test"})
	reg.MustRegister(c)
	c.Inc()
	ts := httptest.NewServer(New(":0", func() error { return nil }, reg).Handler)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "ft_test_total 1") {
		t.Errorf("métriques :\n%s", body)
	}
}
```

- [ ] **Step 2: Vérifier l'échec**

Run: `go test ./internal/server/`
Expected: FAIL (`undefined: New`)

- [ ] **Step 3: Implémenter `internal/server/server.go`**

```go
// Package server expose /healthz et /metrics.
package server

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func New(addr string, health func() error, reg *prometheus.Registry) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		if err := health(); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		io.WriteString(w, "ok\n")
	})
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	return &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
}

// Check interroge url et renvoie nil si la réponse est 200 (utilisé par -healthcheck).
func Check(url string) error {
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
```

- [ ] **Step 4: Lancer le test**

Run: `go test ./internal/server/ -v`
Expected: PASS

- [ ] **Step 5: Implémenter `cmd/frigate-telegram/main.go`**

```go
// Commande frigate-telegram : notifications Telegram pour Frigate NVR.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"frigate-telegram/internal/bot"
	"frigate-telegram/internal/config"
	"frigate-telegram/internal/filter"
	"frigate-telegram/internal/frigate"
	"frigate-telegram/internal/metrics"
	"frigate-telegram/internal/mqttsub"
	"frigate-telegram/internal/notifier"
	"frigate-telegram/internal/server"
	"frigate-telegram/internal/state"
	"frigate-telegram/internal/telegram"
)

func main() {
	configPath := flag.String("config", "/config/config.yml", "chemin du fichier de configuration")
	healthcheck := flag.Bool("healthcheck", false, "interroge /healthz et sort avec 0 si le service est sain")
	healthURL := flag.String("healthcheck-url", "http://127.0.0.1:8080/healthz", "URL utilisée par -healthcheck")
	flag.Parse()

	if *healthcheck {
		if err := server.Check(*healthURL); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := run(*configPath); err != nil {
		fmt.Fprintln(os.Stderr, "erreur:", err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	m := metrics.New()
	st, err := state.Load(cfg.StateFile, time.Now())
	if err != nil {
		log.Warn("état précédent ignoré", "err", err)
	}
	fr, err := frigate.NewClient(cfg.Frigate)
	if err != nil {
		return err
	}
	tg := telegram.New(cfg.Telegram.Token, telegram.WithErrorHook(func(method string, code int) {
		m.TelegramErrors.WithLabelValues(method, strconv.Itoa(code)).Inc()
	}))

	notif := notifier.New(notifier.Deps{
		Config: cfg, Engine: filter.New(cfg, st), State: st,
		Frigate: fr, Telegram: tg, Metrics: m, Log: log,
	})
	sub := mqttsub.New(cfg.MQTT, notif.Topics(), notif.Handle, log, func(up bool) {
		if up {
			m.MQTTConnected.Set(1)
		} else {
			m.MQTTConnected.Set(0)
		}
	})
	b := bot.New(bot.Deps{
		Config: cfg, Telegram: tg, Frigate: fr, Notifier: notif, State: st,
		Log: log, MQTTConnected: sub.Connected,
	})

	srv := server.New(cfg.HTTPListen, func() error {
		if !sub.Connected() {
			return errors.New("MQTT déconnecté")
		}
		if time.Since(b.LastPoll()) > 2*time.Minute {
			return errors.New("Telegram injoignable")
		}
		return nil
	}, m.Registry)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("serveur HTTP arrêté", "err", err)
		}
	}()

	go notif.Run(ctx)
	botDone := make(chan struct{})
	go func() {
		b.Run(ctx)
		close(botDone)
	}()
	sub.Start()
	log.Info("frigate-telegram démarré", "mode", cfg.Mode, "broker", cfg.MQTT.Broker, "frigate", cfg.Frigate.URL)

	flush := time.NewTicker(30 * time.Second)
	defer flush.Stop()
	for running := true; running; {
		select {
		case <-ctx.Done():
			running = false
		case <-flush.C:
			if err := st.FlushIfDirty(); err != nil {
				log.Warn("sauvegarde de l'état échouée", "err", err)
			}
		}
	}

	log.Info("arrêt en cours…")
	sub.Stop()
	if !notif.Shutdown(10 * time.Second) {
		log.Warn("des envois ont été interrompus à l'arrêt")
	}
	<-botDone
	if err := st.Save(); err != nil {
		log.Warn("sauvegarde de l'état échouée", "err", err)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	_ = l.UnmarshalText([]byte(level)) // niveau déjà validé par la config
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l}))
}
```

- [ ] **Step 6: Vérifier la compilation, `go vet` et toute la suite de tests**

Run: `go build ./... ; go vet ./... ; go test ./...`
Expected: build et vet sans sortie ; tous les packages `ok`

- [ ] **Step 7: Vérifier le comportement du binaire**

Run: `go run ./cmd/frigate-telegram -config introuvable.yml`
Expected: `erreur: lecture de la config: open introuvable.yml: ...` et code de sortie 1

Run: `go run ./cmd/frigate-telegram -healthcheck -healthcheck-url http://127.0.0.1:1/healthz`
Expected: message d'erreur de connexion et code de sortie 1

- [ ] **Step 8: Commit**

```bash
git add internal/server cmd
git commit -m "feat: serveur /healthz /metrics et assemblage du service" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 16: Livraison (Docker, compose, exemple de config, README, CI)

**Files:**
- Create: `Dockerfile`, `.dockerignore`, `docker-compose.yml`, `.env.example`, `config.example.yml`, `README.md`, `.github/workflows/docker.yml`
- Test: `internal/config/example_test.go` (vérifie que `config.example.yml` reste valide)

**Interfaces:**
- Consumes: `config.Parse` (Task 1), binaire (Task 15)

- [ ] **Step 1: Écrire le test de l'exemple (qui échoue)**

`internal/config/example_test.go` :

```go
package config

import (
	"os"
	"testing"
)

func TestExampleConfigIsValid(t *testing.T) {
	raw, err := os.ReadFile("../../config.example.yml")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Parse(raw, env(map[string]string{"TELEGRAM_TOKEN": "123:abc"}))
	if err != nil {
		t.Fatalf("config.example.yml invalide : %v", err)
	}
	if c.ForCamera("salon").Enabled {
		t.Error("l'exemple désactive la caméra salon")
	}
}
```

Run: `go test ./internal/config/ -run Example`
Expected: FAIL (fichier absent)

- [ ] **Step 2: Créer `config.example.yml`**

```yaml
# Configuration de frigate-telegram.
# Copier ce fichier vers config/config.yml puis l'adapter.
# Les valeurs ${VAR} viennent des variables d'environnement (.env) ;
# ${VAR:-défaut} utilise "défaut" si la variable n'est pas définie.

timezone: Europe/Paris
mode: events                     # events (un message par objet) ou reviews (alertes Frigate ≥ 0.14)

frigate:
  url: http://frigate:5000       # URL interne de l'API (5000 sans auth, 8971 avec auth)
  external_url: ""               # URL publique pour les liens « Ouvrir dans Frigate » (optionnel)
  username: ""                   # renseigner si l'authentification Frigate est activée
  password: "${FRIGATE_PASSWORD:-}"
  insecure_skip_verify: false    # true pour un certificat auto-signé

mqtt:
  broker: tcp://mosquitto:1883   # ssl://hôte:8883 pour du TLS
  username: ""
  password: "${MQTT_PASSWORD:-}"
  client_id: frigate-telegram
  topic_prefix: frigate          # doit correspondre à mqtt.topic_prefix de Frigate

telegram:
  token: "${TELEGRAM_TOKEN}"
  admins: [123456789]            # identifiants des utilisateurs autorisés à piloter le bot
  chats:                         # noms libres → identifiants de chat
    moi: 123456789
    # famille: -1001234567890    # un groupe a un identifiant négatif

notify:                          # réglages globaux, surchargeables par caméra
  chats: [moi]
  labels: [person, car]          # vide = tous les objets
  zones: []                      # vide = pas de contrainte de zone
  min_score: 0.7                 # ou par label : {person: 0.7, car: 0.85}
  cooldown: 60s                  # délai minimum entre deux notifications caméra+objet
  ignore_stationary: true        # ignorer les objets immobiles (voitures garées…)
  severity: [alert]              # mode reviews : alert et/ou detection
  snapshot: true
  clip: true
  gif: false
  genai_description: true        # ajoute la description GenAI de Frigate quand elle arrive
  clip_delay: 5s                 # attente après la fin de l'événement avant de récupérer le clip
  quiet_hours:                   # notifications sans son
    - {from: "22:00", to: "07:00"}
  off_hours: []                  # aucune notification

cameras:
  jardin:
    zones: [allee, portail]
  garage:
    labels: [person]
    min_score: 0.8
  salon:
    enabled: false

state_file: /data/state.json
http_listen: ":8080"
log_level: info                  # debug, info, warn, error
```

Run: `go test ./internal/config/ -run Example -v`
Expected: PASS

- [ ] **Step 3: Créer `Dockerfile` et `.dockerignore`**

`Dockerfile` :

```dockerfile
# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -tags timetzdata -ldflags="-s -w" -o /out/frigate-telegram ./cmd/frigate-telegram

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/frigate-telegram /frigate-telegram
USER nonroot:nonroot
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 CMD ["/frigate-telegram", "-healthcheck"]
ENTRYPOINT ["/frigate-telegram"]
CMD ["-config", "/config/config.yml"]
```

`.dockerignore` :

```
.git
.github
docs
data
config
.env
*.exe
```

- [ ] **Step 4: Créer `docker-compose.yml` et `.env.example`**

`docker-compose.yml` :

```yaml
services:
  frigate-telegram:
    build: .
    # image: ghcr.io/<utilisateur>/frigate-telegram:latest   # après publication par la CI
    container_name: frigate-telegram
    restart: unless-stopped
    env_file: .env
    volumes:
      - ./config:/config:ro
      - ./data:/data
    tmpfs:
      - /tmp:size=256m        # clips temporaires (50 Mo max chacun)
    ports:
      - "8080:8080"           # optionnel : /healthz et /metrics
```

`.env.example` :

```
TELEGRAM_TOKEN=123456789:AAAA-remplacer-par-le-token-de-BotFather
FRIGATE_PASSWORD=
MQTT_PASSWORD=
```

- [ ] **Step 5: Créer `.github/workflows/docker.yml`**

```yaml
name: ci

on:
  push:
    branches: [main]
    tags: ["v*"]
  pull_request:

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: go vet ./...
      - run: go test -race ./...

  image:
    needs: test
    if: startsWith(github.ref, 'refs/tags/v')
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
    steps:
      - uses: actions/checkout@v4
      - uses: docker/setup-qemu-action@v3
      - uses: docker/setup-buildx-action@v3
      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - id: meta
        uses: docker/metadata-action@v5
        with:
          images: ghcr.io/${{ github.repository }}
          tags: |
            type=semver,pattern={{version}}
            type=semver,pattern={{major}}.{{minor}}
          flavor: latest=auto
      - uses: docker/build-push-action@v6
        with:
          context: .
          platforms: linux/amd64,linux/arm64
          push: true
          tags: ${{ steps.meta.outputs.tags }}
          labels: ${{ steps.meta.outputs.labels }}
          cache-from: type=gha
          cache-to: type=gha,mode=max
```

- [ ] **Step 6: Créer `README.md`**

````markdown
# frigate-telegram

Notifications Telegram pour [Frigate NVR](https://frigate.video) : un snapshot dès la détection, puis le clip vidéo en réponse, avec des filtres fins et un pilotage depuis Telegram.

## Fonctionnalités

- Mode **events** (un message par objet détecté) ou **reviews** (alertes regroupées, Frigate ≥ 0.14)
- Snapshot immédiat, clip MP4 en réponse, et GIF en option ; au-delà de 50 Mo, un lien vers le clip
- Description GenAI de Frigate ajoutée à la légende dès qu'elle est disponible
- Filtres par caméra, objet, zone, score minimum et sévérité ; cooldown ; plages silencieuses et plages coupées
- Plusieurs destinataires avec routage par caméra
- Boutons 🔇 *couper la caméra 1 h*, ⏸ *pause 30 min*, 🎬 *clip*
- Commandes `/pause`, `/resume`, `/status`, `/cameras`, `/snapshot`, `/last`
- État persistant, `/healthz`, métriques Prometheus, image distroless multi-arch (amd64 et arm64)

## Prérequis

1. **MQTT activé dans Frigate** (`config.yml` de Frigate) :

   ```yaml
   mqtt:
     enabled: true
     host: mosquitto
     user: frigate
     password: ...
   ```

2. **Un bot Telegram** : écrire à [@BotFather](https://t.me/BotFather), `/newbot`, puis noter le token.

3. **Votre identifiant Telegram** : écrire un message à votre bot, puis ouvrir
   `https://api.telegram.org/bot<TOKEN>/getUpdates` et relever `message.from.id`.
   Pour un groupe : ajouter le bot au groupe, y écrire un message et relever `message.chat.id` (négatif).

## Installation

```bash
mkdir -p config data
cp config.example.yml config/config.yml   # puis l'adapter
cp .env.example .env                      # puis y mettre le token
sudo chown 65532:65532 data               # l'image tourne en utilisateur non-root (uid 65532)
docker compose up -d --build
docker compose logs -f
```

## Configuration

Tout est documenté dans [`config.example.yml`](config.example.yml). Points clés :

- Les réglages de `notify` s'appliquent à toutes les caméras ; une entrée dans `cameras` remplace champ par champ.
- `zones` : ne notifie que si l'objet est **entré** dans une des zones listées.
- `quiet_hours` : notification sans son ; `off_hours` : aucune notification.
- Mode `reviews` : `severity: [alert]` suit la configuration `review.alerts` de Frigate.
- Les secrets passent par `.env` (`${TELEGRAM_TOKEN}`…). Mettre les valeurs entre guillemets dans le YAML.

## Commandes

| Commande | Effet |
|---|---|
| `/pause [durée] [caméra]` | Pause globale ou d'une caméra (1 h par défaut, `0` = jusqu'à `/resume`). Durées : `30m`, `2h`, `1d` |
| `/resume [caméra]` | Reprend une caméra, ou tout sans argument |
| `/status` | Connexion MQTT, pauses actives, notifications sur 24 h |
| `/cameras` | Caméras et leur état |
| `/snapshot [caméra]` | Image en direct |
| `/last [caméra]` | Dernier événement (snapshot et clip) |

Seuls les utilisateurs listés dans `telegram.admins` peuvent utiliser les commandes et les boutons.

## Supervision

- `GET /healthz` : 200 si MQTT est connecté et Telegram joignable (utilisé par le `HEALTHCHECK` Docker)
- `GET /metrics` : métriques Prometheus préfixées par `ft_`

## Dépannage

- **Aucune notification** : `log_level: debug`, puis vérifier `ft_events_received_total` et `ft_events_filtered_total{reason=...}` sur `/metrics`.
- **Clip manquant** : augmenter `clip_delay` (Frigate n'a pas encore fini d'écrire le clip).
- **`permission denied` sur `/data`** : voir le `chown` de l'installation.

## Développement

```bash
go test ./...
go build ./cmd/frigate-telegram
```
````

- [ ] **Step 7: Vérification finale**

Run: `go vet ./... ; go test ./...`
Expected: tout est `ok`

- [ ] **Step 8: Commit**

```bash
git add Dockerfile .dockerignore docker-compose.yml .env.example config.example.yml README.md .github internal/config/example_test.go
git commit -m "chore: Dockerfile distroless, docker-compose, exemple de config, README et CI" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Couverture de la spec

| Spec | Task |
|---|---|
| §2 Architecture, goroutines, arrêt propre | 10 (Run/Shutdown), 13, 14, 15 |
| §3 Packages | 1 à 15 (s'y ajoutent `actions` et `metrics`, extraits pour ne pas coupler bot et notifier) |
| §4 Configuration, `${ENV}`, fusion, validation | 1, 16 (exemple testé) |
| §5.1 Mode events | 10 |
| §5.2 Mode reviews | 11 |
| §5.3 GenAI | 11 |
| §5.4 Légende | 9 |
| §5.5 Fichiers temporaires, 50 Mo, réutilisation du file_id | 3, 10 |
| §6 Filtre | 5 |
| §7 Commandes, boutons, sécurité | 8, 14 |
| §8 État persistant | 4, 15 (sauvegarde toutes les 30 s et à l'arrêt) |
| §9 Fiabilité (reconnexion, 401, retries, limites de débit, isolation, file bornée) | 3, 6, 7, 10, 13 |
| §10 Logs, healthz, métriques | 8, 15 |
| §11 Livraison | 16 |
| §12 Tests | chaque task ; `-race` en CI |

Écart assumé par rapport à la spec §2 : il n'y a pas de goroutine « sender » dédiée par chat. L'isolation entre chats vient du limiteur (un créneau par chat) et des envois parallèles de `deliver`. Le résultat est le même avec moins de code.
