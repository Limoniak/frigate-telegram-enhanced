package notifier

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

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
		t.Fatalf("history = %+v", got)
	}
	if got[0].ID != "b" || got[0].Sent || got[0].Reason != "label" || got[0].Label != "dog" {
		t.Errorf("most recent = %+v, want b filtered out by label", got[0])
	}
	if got[1].ID != "a" || !got[1].Sent || got[1].Camera != "garage" {
		t.Errorf("oldest = %+v, want a sent", got[1])
	}
	if p, ok := h.n.HistoryThumb("a"); !ok || p != frigate.EventThumbnailPath("a") {
		t.Errorf("thumbnail of a = %q %v", p, ok)
	}
	if _, ok := h.n.HistoryThumb("inconnu"); ok {
		t.Error("an id outside the history must not give a thumbnail")
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
		t.Errorf("size = %d, want %d", n, historySize)
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
		t.Fatalf("history read back = %+v", got)
	}
	if p, ok := again.n.HistoryThumb("a"); !ok || p != frigate.EventThumbnailPath("a") {
		t.Errorf("thumbnail read back = %q %v", p, ok)
	}
}

func TestHistoryFileCorruptedIsIgnored(t *testing.T) {
	file := filepath.Join(t.TempDir(), "history.json")
	os.WriteFile(file, []byte("{not json"), 0o600)
	h := newHarness(t, "events", func(d *Deps) { d.HistoryFile = file })
	if n := len(h.n.History()); n != 0 {
		t.Errorf("history = %d entries, want empty", n)
	}
}

func TestSentCountSurvivesRestart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "history.json")
	now := newHarness(t, "events").clock.Now()
	entries := []storedEntry{
		{HistoryEntry: HistoryEntry{ID: "recent", At: now.Add(-time.Hour), Camera: "garage", Label: "person", Sent: true}},
		{HistoryEntry: HistoryEntry{ID: "old", At: now.Add(-25 * time.Hour), Camera: "garage", Label: "person", Sent: true}},
		{HistoryEntry: HistoryEntry{ID: "filtered", At: now.Add(-2 * time.Hour), Camera: "garage", Label: "dog"}},
	}
	b, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, b, 0o600); err != nil {
		t.Fatal(err)
	}

	h := newHarness(t, "events", func(d *Deps) { d.HistoryFile = file })
	if n := h.n.Count24h(); n != 1 {
		t.Errorf("Count24h() = %d, want 1", n)
	}
	sent := h.n.SentTimes()
	if len(sent) != 1 || !sent[0].Equal(now.Add(-time.Hour)) {
		t.Errorf("SentTimes() = %v, want [%v]", sent, now.Add(-time.Hour))
	}
}

// An entry keeps the notification's link to Frigate and the path of its snapshot,
// and both survive a restart.
func TestHistoryKeepsLinkAndSnapshot(t *testing.T) {
	file := filepath.Join(t.TempDir(), "history.json")
	withFile := func(d *Deps) { d.HistoryFile = file }
	h := newHarness(t, "events", withFile)
	o := h.n.Config.CurrentOverlay()
	u := "https://frigate.maison.example"
	o.ExternalURL = &u
	if err := h.n.Config.ApplyOverlay(&o); err != nil {
		t.Fatal(err)
	}
	h.fr.files[frigate.EventSnapshotPath("a")] = []byte("jpeg")
	h.send(t, "frigate/events", eventMsg("new", "a", "garage", "person", nil))
	if err := h.n.FlushHistory(); err != nil {
		t.Fatal(err)
	}

	want := "https://frigate.maison.example/explore?event_id=a"
	got := h.n.History()
	if len(got) != 1 || got[0].Link != want {
		t.Fatalf("history = %+v, want link %q", got, want)
	}
	check := func(n *Notifier) {
		t.Helper()
		if p, ok := n.HistorySnapshot("a"); !ok || p != frigate.EventSnapshotPath("a") {
			t.Errorf("snapshot of a = %q %v", p, ok)
		}
		if _, ok := n.HistorySnapshot("inconnu"); ok {
			t.Error("an id outside the history must not give a snapshot")
		}
	}
	check(h.n)

	again := newHarness(t, "events", withFile)
	if got := again.n.History(); len(got) != 1 || got[0].Link != want {
		t.Errorf("link read back = %+v", got)
	}
	check(again.n)
}
