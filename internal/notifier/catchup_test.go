package notifier

import (
	"context"
	"testing"
	"time"

	"frigate-telegram-enhanced/internal/frigate"
)

// deliver handles each caught-up message like an MQTT message, sends included.
func (h *harness) deliver(t *testing.T) func(topic string, payload []byte) {
	return func(topic string, payload []byte) { h.send(t, topic, payload) }
}

// toChat keeps the sends to one chat (the test configuration has two).
func toChat(calls []tgCall, chatID int64) []tgCall {
	var out []tgCall
	for _, c := range calls {
		if c.ChatID == chatID {
			out = append(out, c)
		}
	}
	return out
}

func apiEvent(id, camera, label string, start float64, end *float64) frigate.APIEvent {
	score := 0.9
	return frigate.APIEvent{ID: id, Camera: camera, Label: label, StartTime: start, EndTime: end,
		HasClip: true, HasSnapshot: true, TopScore: &score}
}

func TestCatchUpNotifiesEventMissedDuringOutage(t *testing.T) {
	h := newHarness(t, "events")
	end := 1790604030.0
	h.fr.files[frigate.EventSnapshotPath("x")] = []byte("jpeg")
	h.fr.files[frigate.EventClipPath("x")] = []byte("mp4")
	h.fr.events = []frigate.APIEvent{apiEvent("x", "garage", "person", 1790604000, &end)}

	h.n.catchUp(context.Background(), h.clock.Now().Add(-5*time.Minute), h.deliver(t))

	if n := len(toChat(h.tg.byMethod("sendPhoto"), 1)); n != 1 {
		t.Errorf("%d photos, want 1", n)
	}
	if n := len(toChat(h.tg.videos(), 1)); n != 1 {
		t.Errorf("%d clips, want 1 (the event had ended)", n)
	}
}

func TestCatchUpSkipsWhatWasAlreadyHandled(t *testing.T) {
	h := newHarness(t, "events")
	h.fr.files[frigate.EventSnapshotPath("p")] = []byte("jpeg")
	h.send(t, "frigate/events", eventMsg("new", "p", "garage", "person", nil))
	h.send(t, "frigate/events", eventMsg("end", "p", "garage", "person", nil))
	h.send(t, "frigate/events", eventMsg("new", "d", "garage", "dog", nil))
	h.send(t, "frigate/events", eventMsg("end", "d", "garage", "dog", nil))
	photos, history := len(toChat(h.tg.byMethod("sendPhoto"), 1)), len(h.n.History())

	end := 1790604030.0
	h.fr.events = []frigate.APIEvent{
		apiEvent("p", "garage", "person", 1790604000, &end),
		apiEvent("d", "garage", "dog", 1790604000, &end),
	}
	h.n.catchUp(context.Background(), h.clock.Now().Add(-time.Minute), h.deliver(t))

	if n := len(toChat(h.tg.byMethod("sendPhoto"), 1)); n != photos {
		t.Errorf("%d photos, want %d: p was already notified", n, photos)
	}
	if n := len(h.n.History()); n != history {
		t.Errorf("history: %d entries, want %d: d was already filtered out", n, history)
	}
}

func TestCatchUpFinishesEventEndedDuringOutage(t *testing.T) {
	h := newHarness(t, "events")
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.fr.files[frigate.EventClipPath(evID)] = []byte("mp4")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	if n := len(toChat(h.tg.videos(), 1)); n != 0 {
		t.Fatalf("%d clips before the end", n)
	}

	end := 1790604030.0
	h.fr.byID = map[string]frigate.APIEvent{evID: apiEvent(evID, "garage", "person", 1790604000, &end)}
	h.n.catchUp(context.Background(), h.clock.Now().Add(-time.Minute), h.deliver(t))

	if n := len(toChat(h.tg.videos(), 1)); n != 1 {
		t.Errorf("%d clips, want 1: the missed end must send the clip", n)
	}
	if n := len(toChat(h.tg.byMethod("sendPhoto"), 1)); n != 1 {
		t.Errorf("%d photos, want 1 (no new notification)", n)
	}
}

func TestCatchUpLooksBackOneHourAtMost(t *testing.T) {
	h := newHarness(t, "events")
	h.n.catchUp(context.Background(), h.clock.Now().Add(-5*time.Hour), h.deliver(t))
	if want := h.clock.Now().Add(-catchUpWindow); h.fr.after.Before(want) {
		t.Errorf("catching up since %v, want %v at the earliest", h.fr.after, want)
	}
}

func TestCatchUpReviews(t *testing.T) {
	h := newHarness(t, "reviews")
	end := 1790604030.5
	h.fr.files[frigate.EventSnapshotPath("det1")] = []byte("jpeg")
	h.fr.reviewList = []frigate.Review{{
		ID: "r1", Camera: "garage", Severity: "alert", StartTime: 1790604000, EndTime: &end,
		Data: frigate.ReviewData{Detections: []string{"det1"}, Objects: []string{"person"}},
	}}

	h.n.catchUp(context.Background(), h.clock.Now().Add(-5*time.Minute), h.deliver(t))

	if n := len(toChat(h.tg.byMethod("sendPhoto"), 1)); n != 1 {
		t.Errorf("%d photos, want 1", n)
	}
}
