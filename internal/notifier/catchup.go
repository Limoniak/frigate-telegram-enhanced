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

// Rattrapage après une coupure MQTT. Au-delà de catchUpWindow, des alertes aussi
// anciennes n'ont plus d'intérêt : en recevoir une rafale serait pire que rien.
const (
	catchUpWindow = time.Hour
	catchUpMargin = 30 * time.Second // messages en vol au moment de la coupure
	catchUpLimit  = 100
)

// CatchUp rattrape ce que Frigate a publié pendant une coupure du broker (lost :
// heure de la perte de connexion). Les événements manqués passent par la file des
// messages MQTT, comme s'ils venaient d'arriver : mêmes filtres, mêmes envois.
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

// openAndKnown renvoie les suivis pas encore terminés, et tous les identifiants
// déjà traités (suivis en cours ou récents, historique) : ceux-là ne sont pas rejoués.
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

// missedEvents : les fins manquées des suivis en cours, puis les événements
// commencés pendant la coupure, du plus ancien au plus récent.
func (n *Notifier) missedEvents(ctx context.Context, since time.Time, open []string, known map[string]bool) ([]inMsg, error) {
	var out []inMsg
	add := func(typ string, ev frigate.APIEvent) {
		out = append(out, inMsg{topic: n.topics.events, payload: eventPayload(typ, ev)})
	}
	for _, id := range open {
		if ev, err := n.Frigate.Event(ctx, id); err == nil && ev.EndTime != nil {
			add("end", ev)
		}
	}
	evs, err := n.Frigate.EventsSince(ctx, since, catchUpLimit)
	slices.SortFunc(evs, func(a, b frigate.APIEvent) int { return cmp.Compare(a.StartTime, b.StartTime) })
	for _, ev := range evs {
		if known[ev.ID] {
			continue
		}
		add("new", ev)
		if ev.EndTime != nil {
			add("end", ev)
		}
	}
	return out, err
}

// missedReviews : même principe que missedEvents, pour le mode reviews.
func (n *Notifier) missedReviews(ctx context.Context, since time.Time, open []string, known map[string]bool) ([]inMsg, error) {
	var out []inMsg
	add := func(typ string, r frigate.Review) {
		b, _ := json.Marshal(frigate.ReviewMessage{Type: typ, After: r})
		out = append(out, inMsg{topic: n.topics.reviews, payload: b})
	}
	for _, id := range open {
		if r, err := n.Frigate.Review(ctx, id); err == nil && r.EndTime != nil {
			add("end", r)
		}
	}
	rs, err := n.Frigate.ReviewsSince(ctx, since, catchUpLimit)
	slices.SortFunc(rs, func(a, b frigate.Review) int { return cmp.Compare(a.StartTime, b.StartTime) })
	for _, r := range rs {
		if known[r.ID] {
			continue
		}
		add("new", r)
		if r.EndTime != nil {
			add("end", r)
		}
	}
	return out, err
}

// eventPayload reconstitue le message MQTT d'un événement lu sur l'API.
func eventPayload(typ string, ev frigate.APIEvent) []byte {
	b, _ := json.Marshal(frigate.EventMessage{Type: typ, After: frigate.Event{
		ID: ev.ID, Camera: ev.Camera, Label: ev.Label, SubLabel: ev.SubLabel,
		Score: ev.Score(), TopScore: ev.Score(), EnteredZones: ev.Zones, CurrentZones: ev.Zones,
		FalsePositive: ev.FalsePositive, HasSnapshot: ev.HasSnapshot, HasClip: ev.HasClip,
		StartTime: ev.StartTime, EndTime: ev.EndTime,
	}})
	return b
}
