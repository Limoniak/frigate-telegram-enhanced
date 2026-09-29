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
	if t.notified || t.suppressed {
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
		t.suppressed = d.Reason == filter.ReasonCooldown
		return
	}
	n.notify(ctx, t, d)
}
