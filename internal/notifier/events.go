package notifier

import (
	"context"
	"net/url"

	"frigate-telegram-enhanced/internal/filter"
	"frigate-telegram-enhanced/internal/frigate"
)

// handleEvent traite frigate/events. Appelé sous n.mu.
func (n *Notifier) handleEvent(ctx context.Context, msg frigate.EventMessage) {
	ev := msg.After
	t := n.tracked[ev.ID]
	if msg.Type == "end" {
		if t != nil {
			if t.pending != nil && !t.notified {
				// Fin pendant l'attente de l'étiquette : on décide maintenant, avec
				// l'étiquette connue à la fin, pour ne pas perdre la notification.
				t.subLabel = string(ev.SubLabel)
				in := *t.pending
				in.SubLabels = []string{t.subLabel}
				n.decide(ctx, t, in, n.Now(), true)
			}
			n.setSubLabel(ctx, t, string(ev.SubLabel))
		}
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
		// L'étiquette (classification, plaque) arrive souvent après la notification.
		n.setSubLabel(ctx, t, string(ev.SubLabel))
		return
	}
	if t.suppressed {
		return
	}
	t.label, t.subLabel, t.zones = ev.Label, string(ev.SubLabel), ev.EnteredZones
	t.score, t.hasScore = ev.BestScore(), true
	n.decide(ctx, t, filter.Input{
		Camera:        ev.Camera,
		Labels:        []string{ev.Label},
		Score:         ev.BestScore(),
		HasScore:      true,
		Zones:         ev.EnteredZones,
		Stationary:    ev.Stationary,
		FalsePositive: ev.FalsePositive,
		SubLabels:     []string{t.subLabel},
	}, now, false)
}
