package notifier

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"frigate-telegram-enhanced/internal/config"
	"frigate-telegram-enhanced/internal/frigate"
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
		if !strings.Contains(c.Text, "Person") || !strings.Contains(c.Text, "garage") {
			t.Errorf("légende inattendue : %q", c.Text)
		}
		if c.Opts.Markup == nil || len(c.Opts.Markup.InlineKeyboard) != 2 || c.Opts.Markup.InlineKeyboard[0][0].CallbackData != "s:garage" {
			t.Error("boutons manquants (📷 Maintenant, 🎬 Clip, 🔇, ⏸)")
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

// Par défaut, le clip remplace l'image dans le message de la notification.
func TestEndReplacesSnapshotWithClip(t *testing.T) {
	h := newHarness(t, "events")
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.fr.files[frigate.EventClipPath(evID)] = []byte("mp4")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	h.send(t, "frigate/events", eventMsg("end", evID, "garage", "person", nil))

	photos, edits := h.tg.byMethod("sendPhoto"), h.tg.byMethod("editMessageMedia:video")
	if len(edits) != 2 || len(h.tg.byMethod("sendVideo")) != 0 {
		t.Fatalf("remplacements = %d, réponses = %d ; attendu 2 remplacements", len(edits), len(h.tg.byMethod("sendVideo")))
	}
	for _, e := range edits {
		p, _ := find(photos, e.ChatID)
		if e.Target != p.Result {
			t.Errorf("chat %d : message modifié %d, attendu %d (la notification)", e.ChatID, e.Target, p.Result)
		}
		if !strings.Contains(e.Text, "Person") {
			t.Errorf("la légende doit être conservée : %q", e.Text)
		}
	}
	if countData(edits, "mp4") != 1 {
		t.Error("le clip doit être uploadé une seule fois")
	}
}

// Avec media_in_place désactivé, le clip arrive en réponse à la notification.
func TestEndSendsClipAsReplyToSnapshot(t *testing.T) {
	h := newHarness(t, "events")
	o := h.n.Config.CurrentOverlay()
	no := false
	o.Notify.MediaInPlace = &no
	if err := h.n.Config.ApplyOverlay(&o); err != nil {
		t.Fatal(err)
	}
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

// Un événement refusé pour cooldown ne doit pas être notifié à l'expiration du
// cooldown, au milieu de l'événement, avec un snapshot sans rapport avec son début.
func TestCooldownRejectionIsFinal(t *testing.T) {
	h := newHarness(t, "events")
	for _, id := range []string{"a", "b"} {
		h.fr.files[frigate.EventSnapshotPath(id)] = []byte("jpeg")
	}
	h.send(t, "frigate/events", eventMsg("new", "a", "garage", "person", nil))
	h.clock.Add(30 * time.Second)
	h.send(t, "frigate/events", eventMsg("new", "b", "garage", "person", nil))
	h.clock.Add(2 * time.Minute)
	h.send(t, "frigate/events", eventMsg("update", "b", "garage", "person", nil))
	if n := len(h.tg.byMethod("sendPhoto")); n != 2 {
		t.Fatalf("photos = %d : b, refusé pour cooldown, ne doit pas être notifié plus tard", n)
	}
	h.send(t, "frigate/events", eventMsg("end", "b", "garage", "person", nil))
	if got := testutil.ToFloat64(h.m.EventsFiltered.WithLabelValues("cooldown")); got != 1 {
		t.Errorf("filtered{cooldown} = %v, attendu 1", got)
	}
}

// Un événement filtré dont la fin n'arrive jamais est compté quand sweep l'oublie.
func TestSweptFilteredEventIsCounted(t *testing.T) {
	h := newHarness(t, "events")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "dog", nil))
	h.clock.Add(staleAfter + time.Minute)
	h.n.sweep()
	if got := testutil.ToFloat64(h.m.EventsFiltered.WithLabelValues("label")); got != 1 {
		t.Errorf("filtered{label} = %v, attendu 1", got)
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
	if len(h.tg.videos()) != 2 || h.fr.countCalls(clip) != 3 {
		t.Errorf("vidéos=%d appels clip=%d", len(h.tg.videos()), h.fr.countCalls(clip))
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
	if len(h.tg.videos()) != 0 {
		t.Error("aucune vidéo attendue")
	}
}

func TestMissingSnapshotFallsBackToText(t *testing.T) {
	h := newHarness(t, "events")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	msgs := h.tg.byMethod("sendMessage")
	if len(msgs) != 2 || !strings.Contains(msgs[0].Text, "Person") {
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

// Un destinataire en heures calmes reçoit la notification sans son, les autres non.
func TestRecipientQuietHoursAreSilentForThatChatOnly(t *testing.T) {
	h := newHarness(t, "events")
	o := h.n.Config.CurrentOverlay()
	o.Recipients["famille"] = config.Recipient{QuietHours: []config.TimeRange{{From: 0, To: 24*60 - 1}}}
	if err := h.n.Config.ApplyOverlay(&o); err != nil {
		t.Fatal(err)
	}
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	photos := h.tg.byMethod("sendPhoto")
	if len(photos) != 2 {
		t.Fatalf("photos = %+v", photos)
	}
	for _, p := range photos {
		if want := p.ChatID == -100; p.Opts.Silent != want {
			t.Errorf("chat %d : silent = %v, attendu %v", p.ChatID, p.Opts.Silent, want)
		}
	}
}

func TestCroppedSnapshot(t *testing.T) {
	h := newHarness(t, "events")
	o := h.n.Config.CurrentOverlay()
	yes := true
	o.Notify.Crop = &yes
	if err := h.n.Config.ApplyOverlay(&o); err != nil {
		t.Fatal(err)
	}
	h.fr.files[frigate.Cropped(frigate.EventSnapshotPath(evID))] = []byte("zoom")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	photos := h.tg.byMethod("sendPhoto")
	if len(photos) == 0 || countData(photos, "zoom") == 0 {
		t.Fatalf("la photo envoyée doit être la version recadrée : %+v", photos)
	}
	if got := frigate.Cropped(frigate.LatestPath("garage")); got != frigate.LatestPath("garage") {
		t.Errorf("l'image en direct ne se recadre pas : %q", got)
	}
}

// Un message texte (pas d'image) ne peut pas recevoir de média : le clip arrive en réponse.
func TestClipRepliesToTextOnlyNotification(t *testing.T) {
	h := newHarness(t, "events")
	h.fr.files[frigate.EventClipPath(evID)] = []byte("mp4") // pas de snapshot : repli en texte
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	h.send(t, "frigate/events", eventMsg("end", evID, "garage", "person", nil))
	if n := len(h.tg.byMethod("editMessageMedia:video")); n != 0 {
		t.Errorf("remplacements = %d, attendu 0", n)
	}
	replies := h.tg.byMethod("sendVideo")
	texts := h.tg.byMethod("sendMessage")
	if len(replies) != 2 {
		t.Fatalf("réponses vidéo = %d, attendu 2", len(replies))
	}
	for _, v := range replies {
		if m, _ := find(texts, v.ChatID); v.Opts.ReplyTo != m.Result {
			t.Errorf("chat %d : réponse à %d, attendu %d", v.ChatID, v.Opts.ReplyTo, m.Result)
		}
	}
}
