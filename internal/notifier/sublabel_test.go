package notifier

import (
	"strings"
	"testing"
	"time"

	"frigate-telegram-enhanced/internal/frigate"
)

// sublabelHarness : filtre « ne pas prévenir pour Océane », attente de 5 s. Les
// attentes programmées ne partent pas seules : le test les déclenche avec fire.
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
		t.Fatalf("attendu : aucune notification, une attente de 5 s ; envois=%d attentes=%v", h.tg.count(), *waits)
	}
	// Une mise à jour sans étiquette ne met pas fin à l'attente.
	h.send(t, "frigate/events", eventMsg("update", evID, "garage", "person", nil))
	if h.tg.count() != 0 || len(*waits) != 1 {
		t.Fatalf("mise à jour sans étiquette : envois=%d attentes=%v", h.tg.count(), *waits)
	}
	h.send(t, "frigate/events", withSubLabel(eventMsg("update", evID, "garage", "person", nil), "océane"))
	fire(t, h)
	h.send(t, "frigate/events", eventMsg("end", evID, "garage", "person", nil))
	if h.tg.count() != 0 {
		t.Fatalf("Océane est ignorée : %d envois", h.tg.count())
	}
	if hist := h.n.History(); len(hist) != 1 || hist[0].Reason != "sub_label" {
		t.Errorf("historique : %+v", hist)
	}
}

func TestOtherSubLabelNotifiesAtOnceWithLabel(t *testing.T) {
	h, _ := sublabelHarness(t)
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	h.send(t, "frigate/events", withSubLabel(eventMsg("update", evID, "garage", "person", nil), "Baptiste"))
	photos := h.tg.byMethod("sendPhoto")
	if len(photos) != 2 || !strings.Contains(photos[0].Text, "🏷 Baptiste") {
		t.Fatalf("notification attendue dès l'étiquette connue, avec elle : %+v", photos)
	}
}

func TestUnknownIsNotifiedWhenTheWaitEnds(t *testing.T) {
	h, _ := sublabelHarness(t)
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	fire(t, h)
	if n := len(h.tg.byMethod("sendPhoto")); n != 2 {
		t.Fatalf("sans étiquette à la fin de l'attente, l'objet est inconnu : photos = %d", n)
	}
}

// Événement plus court que l'attente : la décision se prend à sa fin, et le clip suit.
func TestShortEventEndingDuringTheWaitIsNotLost(t *testing.T) {
	h, _ := sublabelHarness(t)
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	h.send(t, "frigate/events", eventMsg("end", evID, "garage", "person", nil))
	if n := len(h.tg.byMethod("sendPhoto")); n != 2 {
		t.Fatalf("photos = %d", n)
	}
	if n := len(h.tg.videos()); n != 2 {
		t.Errorf("clips = %d, attendu 2", n)
	}
	fire(t, h) // l'attente qui se termine ensuite ne notifie pas une seconde fois
	if n := len(h.tg.byMethod("sendPhoto")); n != 2 {
		t.Errorf("photos = %d après la fin de l'attente", n)
	}
}

func TestNoWaitWithoutSubLabelFilter(t *testing.T) {
	var waits int
	h := newHarness(t, "events", func(d *Deps) { d.Schedule = func(time.Duration, func()) { waits++ } })
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	if waits != 0 || len(h.tg.byMethod("sendPhoto")) != 2 {
		t.Errorf("sans filtre d'étiquettes : attentes=%d photos=%d", waits, len(h.tg.byMethod("sendPhoto")))
	}
}
