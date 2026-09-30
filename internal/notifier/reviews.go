package notifier

import (
	"context"
	"net/url"
	"slices"
	"strings"

	"frigate-telegram-enhanced/internal/filter"
	"frigate-telegram-enhanced/internal/frigate"
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
			if t.pending != nil && !t.notified {
				// Fin pendant l'attente de l'étiquette : on décide maintenant.
				in := *t.pending
				in.SubLabels = slices.Clone(r.Data.SubLabels)
				if len(in.SubLabels) < len(r.Data.Detections) {
					in.SubLabels = append(in.SubLabels, "")
				}
				n.decide(ctx, t, in, now, true)
			}
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
		n.setSubLabel(ctx, t, strings.Join(r.Data.SubLabels, ", "))
		return
	}
	if t.suppressed {
		return
	}
	t.zones = r.Data.Zones
	if len(r.Data.Objects) > 0 {
		t.label = r.Data.Objects[0] // pour l'historique ; notify le remplace par le label retenu
	}
	t.subLabel = strings.Join(r.Data.SubLabels, ", ")
	t.snapshotPath = frigate.LatestPath(r.Camera)
	if len(r.Data.Detections) > 0 {
		t.snapshotPath = frigate.EventSnapshotPath(r.Data.Detections[0])
	}
	t.clipPath = frigate.RecordingClipPath(r.Camera, r.StartTime, float64(now.Unix())) // remplacé à la fin
	subs := slices.Clone(r.Data.SubLabels)
	if len(subs) < len(r.Data.Detections) {
		subs = append(subs, "") // des objets sans étiquette : peut-être des inconnus
	}
	n.decide(ctx, t, filter.Input{
		Camera:    r.Camera,
		Labels:    r.Data.Objects,
		Zones:     r.Data.Zones,
		Severity:  r.Severity,
		SubLabels: subs,
	}, now, false)
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
	for _, chat := range t.chats {
		n.goAsync(func() { n.editCaption(ctx, t, chat) })
	}
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
