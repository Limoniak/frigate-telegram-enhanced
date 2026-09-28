package notifier

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"frigate-telegram/internal/frigate"
)

const evID = "1790604000.000001-abc"

func eventMsg(typ, id, camera, label string, zones []string) []byte {
	ev := map[string]any{
		"id": id, "camera": camera, "label": label, "sub_label": nil,
		"score": 0.9, "top_score": 0.9, "entered_zones": zones, "current_zones": zones,
		"stationary": false, "false_positive": false, "has_snapshot": true, "has_clip": true,
		"start_time": 1790604000.0,
	}
	if typ == "end" {
		ev["end_time"] = 1790604030.0
	}
	b, _ := json.Marshal(map[string]any{"type": typ, "before": ev, "after": ev})
	return b
}

func TestNewEventSendsSnapshotToEveryChat(t *testing.T) {
	h := newHarness(t, "events")
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))

	photos := h.tg.byMethod("sendPhoto")
	if len(photos) != 2 {
		t.Fatalf("photos = %d, attendu 2", len(photos))
	}
	for _, c := range photos {
		if !strings.Contains(c.Text, "Personne") || !strings.Contains(c.Text, "garage") {
			t.Errorf("légende inattendue : %q", c.Text)
		}
		if c.Opts.Markup == nil || len(c.Opts.Markup.InlineKeyboard[0]) != 3 {
			t.Error("boutons manquants")
		}
	}
	if countData(photos, "jpeg") != 1 || photos[0].FileID != "" {
		t.Errorf("le premier envoi doit uploader : %+v", photos[0])
	}
	if _, ok := find(photos, 1); !ok {
		t.Error("chat 1 non notifié")
	}
	if _, ok := find(photos, -100); !ok {
		t.Error("chat -100 non notifié")
	}
	reused := 0
	for _, c := range photos {
		if c.FileID == "photo-file" {
			reused++
		}
	}
	if reused != 1 {
		t.Errorf("le second envoi doit réutiliser le file_id (reused=%d)", reused)
	}
	if h.n.Count24h() != 1 {
		t.Errorf("Count24h = %d", h.n.Count24h())
	}
}

func TestEndSendsClipAsReplyToSnapshot(t *testing.T) {
	h := newHarness(t, "events")
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.fr.files[frigate.EventClipPath(evID)] = []byte("mp4")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	h.send(t, "frigate/events", eventMsg("end", evID, "garage", "person", nil))

	photos, videos := h.tg.byMethod("sendPhoto"), h.tg.byMethod("sendVideo")
	if len(videos) != 2 {
		t.Fatalf("vidéos = %d, attendu 2", len(videos))
	}
	for _, v := range videos {
		p, _ := find(photos, v.ChatID)
		if v.Opts.ReplyTo != p.Result {
			t.Errorf("chat %d : reply_to=%d, attendu %d", v.ChatID, v.Opts.ReplyTo, p.Result)
		}
		if !v.Opts.Silent {
			t.Error("le clip doit être envoyé sans son")
		}
	}
	if countData(videos, "mp4") != 1 {
		t.Error("le clip doit être uploadé une seule fois")
	}
}

func TestZoneEnteredOnUpdate(t *testing.T) {
	h := newHarness(t, "events")
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.send(t, "frigate/events", eventMsg("new", evID, "jardin", "person", nil))
	if h.tg.count() != 0 {
		t.Fatal("aucune notification attendue hors zone")
	}
	h.send(t, "frigate/events", eventMsg("update", evID, "jardin", "person", []string{"allee"}))
	if len(h.tg.byMethod("sendPhoto")) != 2 {
		t.Fatal("notification attendue à l'entrée dans la zone")
	}
	h.send(t, "frigate/events", eventMsg("update", evID, "jardin", "person", []string{"allee"}))
	if len(h.tg.byMethod("sendPhoto")) != 2 {
		t.Error("un événement ne doit être notifié qu'une fois")
	}
}

func TestCooldownBlocksSecondEvent(t *testing.T) {
	h := newHarness(t, "events")
	for _, id := range []string{"a", "b", "c"} {
		h.fr.files[frigate.EventSnapshotPath(id)] = []byte("jpeg")
	}
	h.send(t, "frigate/events", eventMsg("new", "a", "garage", "person", nil))
	h.clock.Add(30 * time.Second)
	h.send(t, "frigate/events", eventMsg("new", "b", "garage", "person", nil))
	if n := len(h.tg.byMethod("sendPhoto")); n != 2 {
		t.Fatalf("photos = %d : l'événement b doit être bloqué par le cooldown", n)
	}
	h.clock.Add(2 * time.Minute)
	h.send(t, "frigate/events", eventMsg("new", "c", "garage", "person", nil))
	if n := len(h.tg.byMethod("sendPhoto")); n != 4 {
		t.Errorf("photos = %d : l'événement c doit passer", n)
	}
}

func TestFilteredEventIsCountedOnce(t *testing.T) {
	h := newHarness(t, "events")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "dog", nil))
	h.send(t, "frigate/events", eventMsg("update", evID, "garage", "dog", nil))
	h.send(t, "frigate/events", eventMsg("end", evID, "garage", "dog", nil))
	if h.tg.count() != 0 {
		t.Error("aucun envoi attendu")
	}
	if got := testutil.ToFloat64(h.m.EventsFiltered.WithLabelValues("label")); got != 1 {
		t.Errorf("filtered{label} = %v, attendu 1", got)
	}
}

func TestClipRetriedUntilAvailable(t *testing.T) {
	h := newHarness(t, "events")
	clip := frigate.EventClipPath(evID)
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.fr.files[clip] = []byte("mp4")
	h.fr.fails[clip] = 2
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	h.send(t, "frigate/events", eventMsg("end", evID, "garage", "person", nil))
	if len(h.tg.byMethod("sendVideo")) != 2 || h.fr.countCalls(clip) != 3 {
		t.Errorf("vidéos=%d appels clip=%d", len(h.tg.byMethod("sendVideo")), h.fr.countCalls(clip))
	}
}

func TestClipTooLargeSendsLink(t *testing.T) {
	h := newHarness(t, "events")
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.fr.tooLarge[frigate.EventClipPath(evID)] = true
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	h.send(t, "frigate/events", eventMsg("end", evID, "garage", "person", nil))
	msgs := h.tg.byMethod("sendMessage")
	if len(msgs) != 2 || !strings.Contains(msgs[0].Text, "https://nvr.example/api/events/"+evID+"/clip.mp4") {
		t.Fatalf("messages = %+v", msgs)
	}
	if len(h.tg.byMethod("sendVideo")) != 0 {
		t.Error("aucune vidéo attendue")
	}
}

func TestMissingSnapshotFallsBackToText(t *testing.T) {
	h := newHarness(t, "events")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	msgs := h.tg.byMethod("sendMessage")
	if len(msgs) != 2 || !strings.Contains(msgs[0].Text, "Personne") {
		t.Fatalf("messages = %+v", msgs)
	}
	if h.fr.countCalls(frigate.EventSnapshotPath(evID)) != 2 {
		t.Error("le snapshot doit être réessayé une fois")
	}
}

func TestHandleDropsWhenInboxFull(t *testing.T) {
	h := newHarness(t, "events")
	for i := 0; i < inboxSize+1; i++ {
		h.n.Handle("frigate/events", []byte("{}"))
	}
	if got := testutil.ToFloat64(h.m.EventsDropped); got != 1 {
		t.Errorf("dropped = %v, attendu 1", got)
	}
}

func TestCount24hExpires(t *testing.T) {
	h := newHarness(t, "events")
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	h.clock.Add(25 * time.Hour)
	if h.n.Count24h() != 0 {
		t.Errorf("Count24h = %d, attendu 0", h.n.Count24h())
	}
}

func TestTopicsDependOnMode(t *testing.T) {
	if got := newHarness(t, "events").n.Topics(); got[0] != "frigate/events" || got[1] != "frigate/tracked_object_update" {
		t.Errorf("topics = %v", got)
	}
	if got := newHarness(t, "reviews").n.Topics(); got[0] != "frigate/reviews" {
		t.Errorf("topics = %v", got)
	}
}
