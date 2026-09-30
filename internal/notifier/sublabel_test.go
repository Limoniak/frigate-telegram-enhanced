package notifier

import (
	"strings"
	"testing"
	"time"

	"frigate-telegram-enhanced/internal/frigate"
)

// sublabelHarness: filter "don't notify for Océane", 5 s wait. The scheduled waits
// do not fire by themselves: the test triggers them with fire.
func sublabelHarness(t *testing.T) (*harness, *[]time.Duration) {
	t.Helper()
	var waits []time.Duration
	h := newHarness(t, "events", func(d *Deps) {
		d.Schedule = func(wait time.Duration, _ func()) { waits = append(waits, wait) }
	})
	o := h.n.Config.CurrentOverlay()
	ignored := []string{"Océane"}
	o.Notify.IgnoreSubLabels = &ignored
	if err := h.n.Config.ApplyOverlay(&o); err != nil {
		t.Fatal(err)
	}
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.fr.files[frigate.EventClipPath(evID)] = []byte("mp4")
	return h, &waits
}

func fire(t *testing.T, h *harness) { h.send(t, recheckTopic, []byte(evID)) }

func TestIgnoredSubLabelSuppressesNotification(t *testing.T) {
	h, waits := sublabelHarness(t)
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	if h.tg.count() != 0 || len(*waits) != 1 || (*waits)[0] != 5*time.Second {
		t.Fatalf("want no notification and a 5 s wait; sends=%d waits=%v", h.tg.count(), *waits)
	}
	// An update without a label does not end the wait.
	h.send(t, "frigate/events", eventMsg("update", evID, "garage", "person", nil))
	if h.tg.count() != 0 || len(*waits) != 1 {
		t.Fatalf("update without a label: sends=%d waits=%v", h.tg.count(), *waits)
	}
	h.send(t, "frigate/events", withSubLabel(eventMsg("update", evID, "garage", "person", nil), "océane"))
	fire(t, h)
	h.send(t, "frigate/events", eventMsg("end", evID, "garage", "person", nil))
	if h.tg.count() != 0 {
		t.Fatalf("Océane is ignored: %d sends", h.tg.count())
	}
	if hist := h.n.History(); len(hist) != 1 || hist[0].Reason != "sub_label" {
		t.Errorf("history: %+v", hist)
	}
}

func TestOtherSubLabelNotifiesAtOnceWithLabel(t *testing.T) {
	h, _ := sublabelHarness(t)
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	h.send(t, "frigate/events", withSubLabel(eventMsg("update", evID, "garage", "person", nil), "Baptiste"))
	photos := h.tg.byMethod("sendPhoto")
	if len(photos) != 2 || !strings.Contains(photos[0].Text, "🏷 Baptiste") {
		t.Fatalf("want a notification as soon as the label is known, with it: %+v", photos)
	}
}

func TestUnknownIsNotifiedWhenTheWaitEnds(t *testing.T) {
	h, _ := sublabelHarness(t)
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	fire(t, h)
	if n := len(h.tg.byMethod("sendPhoto")); n != 2 {
		t.Fatalf("without a label at the end of the wait, the object is unknown: photos = %d", n)
	}
}

// Event shorter than the wait: the decision is made at its end, and the clip follows.
func TestShortEventEndingDuringTheWaitIsNotLost(t *testing.T) {
	h, _ := sublabelHarness(t)
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	h.send(t, "frigate/events", eventMsg("end", evID, "garage", "person", nil))
	if n := len(h.tg.byMethod("sendPhoto")); n != 2 {
		t.Fatalf("photos = %d", n)
	}
	if n := len(h.tg.videos()); n != 2 {
		t.Errorf("clips = %d, want 2", n)
	}
	fire(t, h) // the wait that ends afterwards does not notify a second time
	if n := len(h.tg.byMethod("sendPhoto")); n != 2 {
		t.Errorf("photos = %d after the end of the wait", n)
	}
}

func TestNoWaitWithoutSubLabelFilter(t *testing.T) {
	var waits int
	h := newHarness(t, "events", func(d *Deps) { d.Schedule = func(time.Duration, func()) { waits++ } })
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	if waits != 0 || len(h.tg.byMethod("sendPhoto")) != 2 {
		t.Errorf("without a label filter: waits=%d photos=%d", waits, len(h.tg.byMethod("sendPhoto")))
	}
}
