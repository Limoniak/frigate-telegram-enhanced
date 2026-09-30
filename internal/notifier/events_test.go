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
		t.Fatalf("photos = %d, want 2", len(photos))
	}
	for _, c := range photos {
		if !strings.Contains(c.Text, "Person") || !strings.Contains(c.Text, "garage") {
			t.Errorf("unexpected caption: %q", c.Text)
		}
		if c.Opts.Markup == nil || len(c.Opts.Markup.InlineKeyboard) != 2 || c.Opts.Markup.InlineKeyboard[0][0].CallbackData != "s:garage" {
			t.Error("boutons manquants (📷 Maintenant, 🎬 Clip, 🔇, ⏸)")
		}
	}
	if countData(photos, "jpeg") != 1 || photos[0].FileID != "" {
		t.Errorf("the first send must upload: %+v", photos[0])
	}
	if _, ok := find(photos, 1); !ok {
		t.Error("chat 1 not notified")
	}
	if _, ok := find(photos, -100); !ok {
		t.Error("chat -100 not notified")
	}
	reused := 0
	for _, c := range photos {
		if c.FileID == "photo-file" {
			reused++
		}
	}
	if reused != 1 {
		t.Errorf("the second send must reuse the file_id (reused=%d)", reused)
	}
	if h.n.Count24h() != 1 {
		t.Errorf("Count24h = %d", h.n.Count24h())
	}
}

// By default, the clip replaces the image in the notification message.
func TestEndReplacesSnapshotWithClip(t *testing.T) {
	h := newHarness(t, "events")
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.fr.files[frigate.EventClipPath(evID)] = []byte("mp4")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	h.send(t, "frigate/events", eventMsg("end", evID, "garage", "person", nil))

	photos, edits := h.tg.byMethod("sendPhoto"), h.tg.byMethod("editMessageMedia:video")
	if len(edits) != 2 || len(h.tg.byMethod("sendVideo")) != 0 {
		t.Fatalf("replacements = %d, replies = %d; want 2 replacements", len(edits), len(h.tg.byMethod("sendVideo")))
	}
	for _, e := range edits {
		p, _ := find(photos, e.ChatID)
		if e.Target != p.Result {
			t.Errorf("chat %d: edited message %d, want %d (the notification)", e.ChatID, e.Target, p.Result)
		}
		if !strings.Contains(e.Text, "Person") {
			t.Errorf("the caption must be kept: %q", e.Text)
		}
	}
	if countData(edits, "mp4") != 1 {
		t.Error("the clip must be uploaded only once")
	}
}

// With media_in_place disabled, the clip arrives as a reply to the notification.
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
		t.Fatalf("videos = %d, want 2", len(videos))
	}
	for _, v := range videos {
		p, _ := find(photos, v.ChatID)
		if v.Opts.ReplyTo != p.Result {
			t.Errorf("chat %d: reply_to=%d, want %d", v.ChatID, v.Opts.ReplyTo, p.Result)
		}
		if !v.Opts.Silent {
			t.Error("the clip must be sent silently")
		}
	}
	if countData(videos, "mp4") != 1 {
		t.Error("the clip must be uploaded only once")
	}
}

func TestZoneEnteredOnUpdate(t *testing.T) {
	h := newHarness(t, "events")
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.send(t, "frigate/events", eventMsg("new", evID, "jardin", "person", nil))
	if h.tg.count() != 0 {
		t.Fatal("want no notification outside the zone")
	}
	h.send(t, "frigate/events", eventMsg("update", evID, "jardin", "person", []string{"allee"}))
	if len(h.tg.byMethod("sendPhoto")) != 2 {
		t.Fatal("want a notification when entering the zone")
	}
	h.send(t, "frigate/events", eventMsg("update", evID, "jardin", "person", []string{"allee"}))
	if len(h.tg.byMethod("sendPhoto")) != 2 {
		t.Error("an event must be notified only once")
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
		t.Fatalf("photos = %d: event b must be blocked by the cooldown", n)
	}
	h.clock.Add(2 * time.Minute)
	h.send(t, "frigate/events", eventMsg("new", "c", "garage", "person", nil))
	if n := len(h.tg.byMethod("sendPhoto")); n != 4 {
		t.Errorf("photos = %d: event c must go through", n)
	}
}

// An event refused because of the cooldown must not be notified when the cooldown
// expires, in the middle of the event, with a snapshot unrelated to its start.
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
		t.Fatalf("photos = %d: b, refused because of the cooldown, must not be notified later", n)
	}
	h.send(t, "frigate/events", eventMsg("end", "b", "garage", "person", nil))
	if got := testutil.ToFloat64(h.m.EventsFiltered.WithLabelValues("cooldown")); got != 1 {
		t.Errorf("filtered{cooldown} = %v, want 1", got)
	}
}

// A filtered event whose end never arrives is counted when sweep forgets it.
func TestSweptFilteredEventIsCounted(t *testing.T) {
	h := newHarness(t, "events")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "dog", nil))
	h.clock.Add(staleAfter + time.Minute)
	h.n.sweep()
	if got := testutil.ToFloat64(h.m.EventsFiltered.WithLabelValues("label")); got != 1 {
		t.Errorf("filtered{label} = %v, want 1", got)
	}
}

func TestFilteredEventIsCountedOnce(t *testing.T) {
	h := newHarness(t, "events")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "dog", nil))
	h.send(t, "frigate/events", eventMsg("update", evID, "garage", "dog", nil))
	h.send(t, "frigate/events", eventMsg("end", evID, "garage", "dog", nil))
	if h.tg.count() != 0 {
		t.Error("want no send")
	}
	if got := testutil.ToFloat64(h.m.EventsFiltered.WithLabelValues("label")); got != 1 {
		t.Errorf("filtered{label} = %v, want 1", got)
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
		t.Errorf("videos=%d clip calls=%d", len(h.tg.videos()), h.fr.countCalls(clip))
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
		t.Error("want no video")
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
		t.Error("the snapshot must be retried once")
	}
}

func TestHandleDropsWhenInboxFull(t *testing.T) {
	h := newHarness(t, "events")
	for i := 0; i < inboxSize+1; i++ {
		h.n.Handle("frigate/events", []byte("{}"))
	}
	if got := testutil.ToFloat64(h.m.EventsDropped); got != 1 {
		t.Errorf("dropped = %v, want 1", got)
	}
	if n, last := h.n.Dropped(); n != 1 || !last.Equal(h.clock.Now()) {
		t.Errorf("Dropped() = %d, %v; want 1 at %v", n, last, h.clock.Now())
	}
}

func TestDroppedNoneYet(t *testing.T) {
	h := newHarness(t, "events")
	if n, last := h.n.Dropped(); n != 0 || !last.IsZero() {
		t.Errorf("Dropped() = %d, %v", n, last)
	}
}

func TestCount24hExpires(t *testing.T) {
	h := newHarness(t, "events")
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	h.clock.Add(25 * time.Hour)
	if h.n.Count24h() != 0 {
		t.Errorf("Count24h = %d, want 0", h.n.Count24h())
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

// A recipient in quiet hours gets the notification silently, the others don't.
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
			t.Errorf("chat %d: silent = %v, want %v", p.ChatID, p.Opts.Silent, want)
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
		t.Fatalf("the photo sent must be the cropped version: %+v", photos)
	}
	if got := frigate.Cropped(frigate.LatestPath("garage")); got != frigate.LatestPath("garage") {
		t.Errorf("the live image is not cropped: %q", got)
	}
}

// A text message (no image) cannot take a media: the clip arrives as a reply.
func TestClipRepliesToTextOnlyNotification(t *testing.T) {
	h := newHarness(t, "events")
	h.fr.files[frigate.EventClipPath(evID)] = []byte("mp4") // no snapshot: falls back to text
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	h.send(t, "frigate/events", eventMsg("end", evID, "garage", "person", nil))
	if n := len(h.tg.byMethod("editMessageMedia:video")); n != 0 {
		t.Errorf("replacements = %d, want 0", n)
	}
	replies := h.tg.byMethod("sendVideo")
	texts := h.tg.byMethod("sendMessage")
	if len(replies) != 2 {
		t.Fatalf("video replies = %d, want 2", len(replies))
	}
	for _, v := range replies {
		if m, _ := find(texts, v.ChatID); v.Opts.ReplyTo != m.Result {
			t.Errorf("chat %d: reply to %d, want %d", v.ChatID, v.Opts.ReplyTo, m.Result)
		}
	}
}

// The address changed in the interface applies to the links of the next notifications.
func TestExternalURLChangeAppliesToLinks(t *testing.T) {
	h := newHarness(t, "events")
	o := h.n.Config.CurrentOverlay()
	u := "https://frigate.maison.example"
	o.ExternalURL = &u
	if err := h.n.Config.ApplyOverlay(&o); err != nil {
		t.Fatal(err)
	}
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	photos := h.tg.byMethod("sendPhoto")
	if len(photos) == 0 || !strings.Contains(photos[0].Text, `href="https://frigate.maison.example/explore?event_id=`) {
		t.Fatalf("link: %+v", photos)
	}
}

// withSubLabel adds to an event message the label Frigate assigns afterwards, in
// Frigate 0.18's format: ["name", score].
func withSubLabel(msg []byte, sub string) []byte {
	var m map[string]any
	json.Unmarshal(msg, &m)
	m["after"].(map[string]any)["sub_label"] = []any{sub, 0.95}
	b, _ := json.Marshal(m)
	return b
}

// The label (classification, face: "Océane") arrives after the notification: the
// caption is updated, without a new message, and the recent activity shows it.
func TestLateSubLabelUpdatesCaption(t *testing.T) {
	h := newHarness(t, "events")
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	h.send(t, "frigate/events", withSubLabel(eventMsg("update", evID, "garage", "person", nil), "Océane"))

	if n := len(h.tg.byMethod("sendPhoto")); n != 2 {
		t.Fatalf("photos = %d: the label must not create a new message", n)
	}
	edits := h.tg.byMethod("editMessageCaption")
	if len(edits) != 2 {
		t.Fatalf("edits = %d, want one per recipient", len(edits))
	}
	for _, e := range edits {
		if !strings.Contains(e.Text, "🏷 Océane") {
			t.Errorf("updated caption:\n%s", e.Text)
		}
	}
	if hist := h.n.History(); hist[0].SubLabel != "Océane" {
		t.Errorf("history: %+v", hist[0])
	}
	// The same label repeated in the next updates does not edit again.
	h.send(t, "frigate/events", withSubLabel(eventMsg("update", evID, "garage", "person", nil), "Océane"))
	if n := len(h.tg.byMethod("editMessageCaption")); n != 2 {
		t.Errorf("edits = %d after an unchanged label", n)
	}
}
