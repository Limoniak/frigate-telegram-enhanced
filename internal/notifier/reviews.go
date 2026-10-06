package notifier

import (
	"context"
	"net/url"
	"slices"
	"strings"

	"frigate-telegram-enhanced/internal/filter"
	"frigate-telegram-enhanced/internal/frigate"
)

// handleReview handles frigate/reviews. Called under n.mu.
func (n *Notifier) handleReview(ctx context.Context, msg frigate.ReviewMessage) {
	r := msg.After
	t := n.tracked[r.ID]
	now := n.Now()
	if msg.Type == "end" {
		end := float64(now.Unix())
		if r.EndTime != nil {
			end = *r.EndTime
		}
		if t != nil {
			t.clipPath = frigate.RecordingClipPath(r.Camera, r.StartTime, end)
			t.eventIDs = r.Data.Detections
			if t.pending != nil && !t.notified {
				// End while waiting for the label: decide now.
				in := *t.pending
				in.SubLabels = slices.Clone(r.Data.SubLabels)
				if len(in.SubLabels) < len(r.Data.Detections) {
					in.SubLabels = append(in.SubLabels, "")
				}
				n.decide(ctx, t, in, now, true)
			}
		}
		n.finish(ctx, t, true, end)
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
		t.label = r.Data.Objects[0] // for the history; notify replaces it with the label kept
	}
	t.subLabel = strings.Join(r.Data.SubLabels, ", ")
	t.snapshotPath = frigate.LatestPath(r.Camera)
	if len(r.Data.Detections) > 0 {
		t.snapshotPath = frigate.EventSnapshotPath(r.Data.Detections[0])
	}
	t.clipPath = frigate.RecordingClipPath(r.Camera, r.StartTime, float64(now.Unix())) // replaced at the end
	subs := slices.Clone(r.Data.SubLabels)
	if len(subs) < len(r.Data.Detections) {
		subs = append(subs, "") // objects without a label: maybe unknown ones
	}
	n.decide(ctx, t, filter.Input{
		Camera:    r.Camera,
		Labels:    r.Data.Objects,
		Zones:     r.Data.Zones,
		Severity:  r.Severity,
		SubLabels: subs,
	}, now, false)
}

// handleUpdate adds the GenAI description to the messages already sent. Called under n.mu.
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

// findByEventID finds a tracked item by its id, or by the id of a linked Frigate event.
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
