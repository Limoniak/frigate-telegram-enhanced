package notifier

import (
	"context"
	"strings"
	"testing"

	"frigate-telegram-enhanced/internal/frigate"
)

func TestSendClipToTrackedEvent(t *testing.T) {
	h := newHarness(t, "events")
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.fr.files[frigate.EventClipPath(evID)] = []byte("mp4")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	if err := h.n.SendClipTo(context.Background(), 1, 42, evID); err != nil {
		t.Fatal(err)
	}
	v := h.tg.byMethod("sendVideo")
	if len(v) != 1 || v[0].ChatID != 1 || v[0].Opts.ReplyTo != 42 || v[0].Data != "mp4" {
		t.Errorf("vidéo = %+v", v)
	}
}

func TestSendClipToUnknownEvent(t *testing.T) {
	h := newHarness(t, "events")
	h.fr.files[frigate.EventClipPath("ancien")] = []byte("mp4")
	if err := h.n.SendClipTo(context.Background(), 1, 0, "ancien"); err != nil {
		t.Fatal(err)
	}
	if len(h.tg.byMethod("sendVideo")) != 1 {
		t.Error("vidéo attendue")
	}
}

func TestSendClipToUnknownReview(t *testing.T) {
	h := newHarness(t, "reviews")
	end := 1790604030.5
	h.fr.reviews[revID] = frigate.Review{ID: revID, Camera: "garage", StartTime: 1790604000.0, EndTime: &end}
	h.fr.files[frigate.RecordingClipPath("garage", 1790604000.0, end)] = []byte("mp4")
	if err := h.n.SendClipTo(context.Background(), 1, 0, revID); err != nil {
		t.Fatal(err)
	}
	if len(h.tg.byMethod("sendVideo")) != 1 {
		t.Error("vidéo attendue")
	}
}

func TestSendClipToMissingClipReturnsError(t *testing.T) {
	h := newHarness(t, "events")
	if err := h.n.SendClipTo(context.Background(), 1, 0, "absent"); err == nil {
		t.Error("erreur attendue")
	}
}

func TestSendLast(t *testing.T) {
	h := newHarness(t, "events")
	end := 1790604030.0
	h.fr.events = []frigate.APIEvent{{ID: evID, Camera: "garage", Label: "person", StartTime: 1790604000.0, EndTime: &end, HasSnapshot: true, HasClip: true}}
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.fr.files[frigate.EventClipPath(evID)] = []byte("mp4")
	if err := h.n.SendLast(context.Background(), 1, ""); err != nil {
		t.Fatal(err)
	}
	photos, videos := h.tg.byMethod("sendPhoto"), h.tg.byMethod("sendVideo")
	if len(photos) != 1 || len(videos) != 1 || videos[0].Opts.ReplyTo != photos[0].Result {
		t.Errorf("photos=%+v vidéos=%+v", photos, videos)
	}
}

func TestSendLastWithoutEvents(t *testing.T) {
	h := newHarness(t, "events")
	if err := h.n.SendLast(context.Background(), 1, "garage"); err != nil {
		t.Fatal(err)
	}
	if m := h.tg.byMethod("sendMessage"); len(m) != 1 || m[0].Text != "No event found." {
		t.Errorf("messages = %+v", m)
	}
}

func TestSendTestUsesLiveSnapshotAndRecipients(t *testing.T) {
	h := newHarness(t, "events")
	h.fr.files[frigate.LatestPath("garage")] = []byte("live")
	if err := h.n.SendTest(context.Background(), "garage"); err != nil {
		t.Fatal(err)
	}
	photos := h.tg.byMethod("sendPhoto")
	if len(photos) != 2 {
		t.Fatalf("photos = %+v, attendu une par chat", photos)
	}
	if !strings.Contains(photos[0].Text, "Test notification") || !strings.Contains(photos[0].Text, "video clip will follow") {
		t.Errorf("légende = %q", photos[0].Text)
	}
}

func TestSendTestFallsBackToTextWithoutSnapshot(t *testing.T) {
	h := newHarness(t, "events")
	if err := h.n.SendTest(context.Background(), "garage"); err != nil {
		t.Fatal(err)
	}
	if n := len(h.tg.byMethod("sendMessage")); n != 2 {
		t.Errorf("messages texte = %d, attendu 2", n)
	}
}
