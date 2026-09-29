package notifier

import (
	"strings"
	"testing"
	"time"

	"frigate-telegram-enhanced/internal/config"
	"frigate-telegram-enhanced/internal/frigate"
)

func enableGrouping(t *testing.T, h *harness, d time.Duration) {
	t.Helper()
	o := h.n.Config.CurrentOverlay()
	g := config.Duration(d)
	o.Notify.Group = &g
	if err := h.n.Config.ApplyOverlay(&o); err != nil {
		t.Fatal(err)
	}
}

func TestBurstIsGroupedIntoTheFirstMessage(t *testing.T) {
	h := newHarness(t, "events")
	enableGrouping(t, h, 2*time.Minute)
	for _, id := range []string{"a", "b", "c"} {
		h.fr.files[frigate.EventSnapshotPath(id)] = []byte("jpeg-" + id)
		h.fr.files[frigate.EventClipPath(id)] = []byte("mp4-" + id)
	}

	h.send(t, "frigate/events", eventMsg("new", "a", "garage", "person", nil))
	h.clock.Add(30 * time.Second)
	h.send(t, "frigate/events", eventMsg("new", "b", "entree", "person", []string{"porte"}))

	if n := len(h.tg.byMethod("sendPhoto")); n != 2 {
		t.Fatalf("photos = %d : b doit rejoindre le message de a, pas en envoyer un nouveau", n)
	}
	edits := h.tg.byMethod("editMessageCaption")
	if len(edits) != 2 {
		t.Fatalf("éditions = %+v, attendu une par destinataire", edits)
	}
	for _, e := range edits {
		if !strings.Contains(e.Text, "1 more detection") || !strings.Contains(e.Text, "entree") || !strings.Contains(e.Text, "porte") {
			t.Errorf("légende regroupée :\n%s", e.Text)
		}
	}

	// b regroupée : ni clip ni GIF à la fin de son événement.
	h.send(t, "frigate/events", eventMsg("end", "b", "entree", "person", []string{"porte"}))
	if n := h.fr.countCalls(frigate.EventClipPath("b")); n != 0 {
		t.Errorf("clip de b téléchargé %d fois", n)
	}
	if hist := h.n.History(); !hist[0].Grouped || hist[1].Grouped {
		t.Errorf("historique : %+v", hist)
	}

	// Après la fenêtre, une nouvelle rafale repart sur un nouveau message.
	h.clock.Add(3 * time.Minute)
	h.send(t, "frigate/events", eventMsg("new", "c", "jardin", "person", []string{"allee"}))
	if n := len(h.tg.byMethod("sendPhoto")); n != 4 {
		t.Errorf("photos = %d, c doit partir dans un nouveau message", n)
	}
}

func TestGroupingDisabledByDefault(t *testing.T) {
	h := newHarness(t, "events")
	for _, id := range []string{"a", "b"} {
		h.fr.files[frigate.EventSnapshotPath(id)] = []byte("jpeg")
	}
	h.send(t, "frigate/events", eventMsg("new", "a", "garage", "person", nil))
	h.send(t, "frigate/events", eventMsg("new", "b", "entree", "person", nil))
	if n := len(h.tg.byMethod("sendPhoto")); n != 4 {
		t.Errorf("photos = %d, sans regroupement chaque détection a son message", n)
	}
}

func TestGroupBlockIsBounded(t *testing.T) {
	h := newHarness(t, "events")
	g := &group{}
	for range maxGroupLines + 3 {
		g.count++
		if len(g.lines) < maxGroupLines {
			g.lines = append(g.lines, "ligne")
		}
	}
	block := h.n.groupBlock(g)
	if strings.Count(block, "• ligne") != maxGroupLines || !strings.Contains(block, "and 3 more") {
		t.Errorf("bloc :\n%s", block)
	}
}
