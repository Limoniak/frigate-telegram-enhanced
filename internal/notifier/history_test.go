package notifier

import (
	"os"
	"path/filepath"
	"testing"

	"frigate-telegram-enhanced/internal/frigate"
)

func TestHistoryRecordsSentAndFiltered(t *testing.T) {
	h := newHarness(t, "events")
	h.fr.files[frigate.EventSnapshotPath("a")] = []byte("jpeg")
	h.send(t, "frigate/events", eventMsg("new", "a", "garage", "person", nil))
	h.send(t, "frigate/events", eventMsg("new", "b", "garage", "dog", nil))
	h.send(t, "frigate/events", eventMsg("end", "b", "garage", "dog", nil))

	got := h.n.History()
	if len(got) != 2 {
		t.Fatalf("historique = %+v", got)
	}
	if got[0].ID != "b" || got[0].Sent || got[0].Reason != "label" || got[0].Label != "dog" {
		t.Errorf("plus récent = %+v, attendu b filtré pour label", got[0])
	}
	if got[1].ID != "a" || !got[1].Sent || got[1].Camera != "garage" {
		t.Errorf("plus ancien = %+v, attendu a envoyé", got[1])
	}
	if p, ok := h.n.HistoryThumb("a"); !ok || p != frigate.EventThumbnailPath("a") {
		t.Errorf("miniature de a = %q %v", p, ok)
	}
	if _, ok := h.n.HistoryThumb("inconnu"); ok {
		t.Error("un id hors historique ne doit pas donner de miniature")
	}
}

func TestHistoryIsBounded(t *testing.T) {
	h := newHarness(t, "events")
	for i := range historySize + 10 {
		id := string(rune('a'+i%26)) + string(rune('0'+i/26))
		h.send(t, "frigate/events", eventMsg("new", id, "garage", "dog", nil))
		h.send(t, "frigate/events", eventMsg("end", id, "garage", "dog", nil))
	}
	if n := len(h.n.History()); n != historySize {
		t.Errorf("taille = %d, attendu %d", n, historySize)
	}
}

func TestHistorySurvivesRestart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "history.json")
	withFile := func(d *Deps) { d.HistoryFile = file }
	h := newHarness(t, "events", withFile)
	h.fr.files[frigate.EventSnapshotPath("a")] = []byte("jpeg")
	h.send(t, "frigate/events", eventMsg("new", "a", "garage", "person", nil))
	h.send(t, "frigate/events", eventMsg("new", "b", "garage", "dog", nil))
	h.send(t, "frigate/events", eventMsg("end", "b", "garage", "dog", nil))
	if err := h.n.FlushHistory(); err != nil {
		t.Fatal(err)
	}

	again := newHarness(t, "events", withFile)
	got := again.n.History()
	if len(got) != 2 || got[0].ID != "b" || got[0].Reason != "label" || got[1].ID != "a" || !got[1].Sent {
		t.Fatalf("historique relu = %+v", got)
	}
	if p, ok := again.n.HistoryThumb("a"); !ok || p != frigate.EventThumbnailPath("a") {
		t.Errorf("miniature relue = %q %v", p, ok)
	}
}

func TestHistoryFileCorruptedIsIgnored(t *testing.T) {
	file := filepath.Join(t.TempDir(), "history.json")
	os.WriteFile(file, []byte("{pas du json"), 0o600)
	h := newHarness(t, "events", func(d *Deps) { d.HistoryFile = file })
	if n := len(h.n.History()); n != 0 {
		t.Errorf("historique = %d entrées, attendu vide", n)
	}
}
