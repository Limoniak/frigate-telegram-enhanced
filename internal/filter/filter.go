// Package filter décide, sans aucune entrée/sortie, si une détection doit être notifiée.
package filter

import (
	"slices"
	"time"

	"frigate-telegram-enhanced/internal/config"
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
	ReasonHome           = "home"
	ReasonRecipients     = "recipients"
	ReasonOffHours       = "off_hours"
	ReasonCooldown       = "cooldown"
)

// State est la vue en lecture de l'état (pauses, coupures, cooldowns).
type State interface {
	IsPaused(now time.Time) bool
	IsMuted(camera string, now time.Time) bool
	LastNotified(key string) time.Time
	SomeoneHome() bool
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
	Notify     bool
	Silent     bool     // notification sans son pour tous (quiet_hours de la caméra, présence)
	Label      string   // label retenu, pour la légende et le cooldown
	Chats      []string // noms des chats destinataires
	QuietChats []string // destinataires qui la reçoivent sans son (leurs propres quiet_hours)
	Reason     string   // raison du refus quand Notify est faux
}

// SilentFor indique si chat reçoit la notification sans son.
func (d Decision) SilentFor(chat string) bool { return d.Silent || slices.Contains(d.QuietChats, chat) }

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
	// Quelqu'un à la maison : selon la caméra, rien, sans son, ou comme d'habitude.
	homeSilent := false
	if e.cfg.Presence.Enabled() && e.state.SomeoneHome() {
		switch n.WhenHome {
		case config.HomeNotify:
		case config.HomeSilent:
			homeSilent = true
		default:
			return reject(ReasonHome)
		}
	}
	local := now.In(e.cfg.Location)
	if config.InRanges(n.OffHours, local) {
		return reject(ReasonOffHours)
	}
	// Restrictions propres à chaque destinataire : objets et heures.
	var chats, quiet []string
	for _, chat := range n.Chats {
		r := e.cfg.Recipient(chat)
		if !slices.ContainsFunc(in.Labels, func(l string) bool {
			return (len(n.Labels) == 0 || slices.Contains(n.Labels, l)) && (len(r.Labels) == 0 || slices.Contains(r.Labels, l))
		}) || config.InRanges(r.OffHours, local) {
			continue
		}
		chats = append(chats, chat)
		if config.InRanges(r.QuietHours, local) {
			quiet = append(quiet, chat)
		}
	}
	if len(chats) == 0 {
		return reject(ReasonRecipients)
	}
	if n.Cooldown > 0 {
		last := e.state.LastNotified(CooldownKey(in.Camera, label))
		if !last.IsZero() && now.Sub(last) < n.Cooldown {
			return reject(ReasonCooldown)
		}
	}
	return Decision{
		Notify:     true,
		Silent:     homeSilent || config.InRanges(n.QuietHours, local),
		Label:      label,
		Chats:      chats,
		QuietChats: quiet,
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
