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
