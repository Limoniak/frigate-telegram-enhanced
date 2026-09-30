// Package filter decides, without any input/output, whether a detection must be notified.
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
	ReasonSubLabel       = "sub_label"
	ReasonOffHours       = "off_hours"
	ReasonCooldown       = "cooldown"
)

// State is the read view of the state (pauses, mutes, cooldowns).
type State interface {
	IsPaused(now time.Time) bool
	IsMuted(camera string, now time.Time) bool
	LastNotified(key string) time.Time
	SomeoneHome() bool
}

// Input describes a detection. Labels holds one label (events) or several objects (reviews).
type Input struct {
	Camera        string
	Labels        []string
	Score         float64
	HasScore      bool
	Zones         []string
	Severity      string
	Stationary    bool
	FalsePositive bool
	SubLabels     []string // known Frigate labels (classification, face, plate)
}

type Decision struct {
	Notify     bool
	Silent     bool     // silent notification for everyone (camera's quiet_hours, presence)
	Label      string   // label kept, for the caption and the cooldown
	Chats      []string // names of the recipient chats
	QuietChats []string // recipients who get it silently (their own quiet_hours)
	Reason     string   // reason for the refusal when Notify is false
}

// SilentFor reports whether chat gets the notification silently.
func (d Decision) SilentFor(chat string) bool { return d.Silent || slices.Contains(d.QuietChats, chat) }

type Engine struct {
	cfg   *config.Config
	state State
}

func New(cfg *config.Config, st State) *Engine { return &Engine{cfg: cfg, state: st} }

func CooldownKey(camera, label string) string { return camera + "/" + label }

// Evaluate applies the rules in the order of the spec; the first one that fails gives Reason.
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
	if ignoredSubLabels(n, in.SubLabels) {
		return reject(ReasonSubLabel)
	}
	if e.state.IsPaused(now) {
		return reject(ReasonPaused)
	}
	if e.state.IsMuted(in.Camera, now) {
		return reject(ReasonMuted)
	}
	// Someone home: depending on the camera, nothing, silent, or as usual.
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
	// Restrictions of each recipient: objects and hours.
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

// matchLabel returns the first accepted label (any label if the allowed list is empty).
func matchLabel(allowed, labels []string) string {
	for _, l := range labels {
		if len(allowed) == 0 || slices.Contains(allowed, l) {
			return l
		}
	}
	return ""
}

// ignoredSubLabels reports whether every object of the detection carries a label to
// ignore. An empty label stands for an object without a label, maybe an unknown
// one: the detection is then notified.
func ignoredSubLabels(n config.Notify, subs []string) bool {
	if len(subs) == 0 {
		return false
	}
	for _, s := range subs {
		if s == "" || !n.IgnoresSubLabel(s) {
			return false
		}
	}
	return true
}
