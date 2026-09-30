package notifier

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"time"

	"frigate-telegram-enhanced/internal/config"
	"frigate-telegram-enhanced/internal/frigate"
)

// Catching up after an MQTT outage. Beyond catchUpWindow, alerts that old are no
// longer of interest: getting a burst of them would be worse than nothing.
const (
	catchUpWindow = time.Hour
	catchUpMargin = 30 * time.Second // messages in flight when the connection was lost
	catchUpLimit  = 100
)

// CatchUp catches up on what Frigate published during a broker outage (lost: time
// the connection was lost). The missed events go through the queue of MQTT
// messages, as if they had just arrived: same filters, same sends.
func (n *Notifier) CatchUp(ctx context.Context, lost time.Time) {
	n.catchUp(ctx, lost, func(topic string, payload []byte) {
		select {
		case n.inbox <- inMsg{topic: topic, payload: payload}:
		case <-ctx.Done():
		}
	})
}

func (n *Notifier) catchUp(ctx context.Context, lost time.Time, deliver func(topic string, payload []byte)) {
	since := lost.Add(-catchUpMargin)
	if earliest := n.Now().Add(-catchUpWindow); since.Before(earliest) {
		since = earliest
	}
	open, known := n.openAndKnown()
	var msgs []inMsg
	var err error
	if n.Config.Mode == config.ModeReviews {
		msgs, err = n.missedReviews(ctx, since, open, known)
	} else {
		msgs, err = n.missedEvents(ctx, since, open, known)
	}
	if err != nil {
		n.Log.Warn("catching up on events missed during the MQTT outage failed", "err", err)
	}
	n.Log.Info("catching up on events missed during the MQTT outage", "since", since, "messages", len(msgs))
	for _, m := range msgs {
		deliver(m.topic, m.payload)
	}
}

// openAndKnown returns the tracked items not finished yet, and every id already
// handled (tracked now or recently, history): those are not replayed.
func (n *Notifier) openAndKnown() (open []string, known map[string]bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	known = map[string]bool{}
	for id, t := range n.tracked {
		known[id] = true
		if !t.ended {
			open = append(open, id)
		}
	}
	for _, e := range n.history {
		known[e.ID] = true
	}
	slices.Sort(open)
	return open, known
}

// missedEvents: the missed ends of the tracked events, then the events that
// started during the outage, oldest first.
func (n *Notifier) missedEvents(ctx context.Context, since time.Time, open []string, known map[string]bool) ([]inMsg, error) {
	var out []inMsg
	push := func(typ string, ev frigate.APIEvent) {
		out = append(out, inMsg{topic: n.topics.events, payload: eventPayload(typ, ev)})
	}
	for _, id := range open {
		if ev, err := n.Frigate.Event(ctx, id); err == nil && ev.EndTime != nil {
			push("end", ev)
		}
	}
	evs, err := n.Frigate.EventsSince(ctx, since, catchUpLimit)
	slices.SortFunc(evs, func(a, b frigate.APIEvent) int { return cmp.Compare(a.StartTime, b.StartTime) })
	for _, ev := range evs {
		if known[ev.ID] {
			continue
		}
		push("new", ev)
		if ev.EndTime != nil {
			push("end", ev)
		}
	}
	return out, err
}

// missedReviews: same as missedEvents, for the reviews mode.
func (n *Notifier) missedReviews(ctx context.Context, since time.Time, open []string, known map[string]bool) ([]inMsg, error) {
	var out []inMsg
	push := func(typ string, r frigate.Review) {
		b, _ := json.Marshal(frigate.ReviewMessage{Type: typ, After: r})
		out = append(out, inMsg{topic: n.topics.reviews, payload: b})
	}
	for _, id := range open {
		if r, err := n.Frigate.Review(ctx, id); err == nil && r.EndTime != nil {
			push("end", r)
		}
	}
	rs, err := n.Frigate.ReviewsSince(ctx, since, catchUpLimit)
	slices.SortFunc(rs, func(a, b frigate.Review) int { return cmp.Compare(a.StartTime, b.StartTime) })
	for _, r := range rs {
		if known[r.ID] {
			continue
		}
		push("new", r)
		if r.EndTime != nil {
			push("end", r)
		}
	}
	return out, err
}

// eventPayload rebuilds the MQTT message of an event read from the API.
func eventPayload(typ string, ev frigate.APIEvent) []byte {
	b, _ := json.Marshal(frigate.EventMessage{Type: typ, After: frigate.Event{
		ID: ev.ID, Camera: ev.Camera, Label: ev.Label, SubLabel: ev.SubLabel,
		Score: ev.Score(), TopScore: ev.Score(), EnteredZones: ev.Zones, CurrentZones: ev.Zones,
		FalsePositive: ev.FalsePositive, HasSnapshot: ev.HasSnapshot, HasClip: ev.HasClip,
		StartTime: ev.StartTime, EndTime: ev.EndTime,
	}})
	return b
}
