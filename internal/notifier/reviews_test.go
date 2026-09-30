package notifier

import (
	"encoding/json"
	"strings"
	"testing"

	"frigate-telegram-enhanced/internal/frigate"
)

const revID = "1790604000.5-rev"

func reviewMsg(typ, id, camera, severity string, objects, zones, detections []string) []byte {
	r := map[string]any{
		"id": id, "camera": camera, "severity": severity, "start_time": 1790604000.0,
		"data": map[string]any{"detections": detections, "objects": objects, "sub_labels": []string{}, "zones": zones},
	}
	if typ == "end" {
		r["end_time"] = 1790604030.5
	}
	b, _ := json.Marshal(map[string]any{"type": typ, "before": r, "after": r})
	return b
}

func descriptionMsg(id, text string) []byte {
	b, _ := json.Marshal(map[string]any{"type": "description", "id": id, "description": text})
	return b
}

func TestReviewAlertSendsDetectionSnapshotAndRecordingClip(t *testing.T) {
	h := newHarness(t, "reviews")
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.fr.files[frigate.RecordingClipPath("garage", 1790604000.0, 1790604030.5)] = []byte("mp4")
	h.send(t, "frigate/reviews", reviewMsg("new", revID, "garage", "alert", []string{"person"}, nil, []string{evID}))
	h.send(t, "frigate/reviews", reviewMsg("end", revID, "garage", "alert", []string{"person"}, nil, []string{evID}))

	photos := h.tg.byMethod("sendPhoto")
	if len(photos) != 2 || countData(photos, "jpeg") != 1 {
		t.Fatalf("photos = %+v", photos)
	}
	if !strings.Contains(photos[0].Opts.Markup.InlineKeyboard[0][1].CallbackData, revID) {
		t.Error("the clip button must carry the review's id")
	}
	if videos := h.tg.videos(); len(videos) != 2 || countData(videos, "mp4") != 1 {
		t.Fatalf("videos = %+v", videos)
	}
}

func TestReviewDetectionSeverityIgnored(t *testing.T) {
	h := newHarness(t, "reviews")
	h.send(t, "frigate/reviews", reviewMsg("new", revID, "garage", "detection", []string{"person"}, nil, []string{evID}))
	if h.tg.count() != 0 {
		t.Error("a review of severity detection must not notify")
	}
}

func TestReviewEscalatedToAlertOnUpdate(t *testing.T) {
	h := newHarness(t, "reviews")
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.send(t, "frigate/reviews", reviewMsg("new", revID, "garage", "detection", []string{"person"}, nil, []string{evID}))
	h.send(t, "frigate/reviews", reviewMsg("update", revID, "garage", "alert", []string{"person"}, nil, []string{evID}))
	if len(h.tg.byMethod("sendPhoto")) != 2 {
		t.Error("switching to alert must notify")
	}
}

func TestDescriptionEditsCaption(t *testing.T) {
	h := newHarness(t, "events")
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	h.send(t, "frigate/tracked_object_update", descriptionMsg(evID, "Un livreur dépose un colis."))

	photos, edits := h.tg.byMethod("sendPhoto"), h.tg.byMethod("editMessageCaption")
	if len(edits) != 2 {
		t.Fatalf("edits = %d, want 2", len(edits))
	}
	for _, e := range edits {
		p, _ := find(photos, e.ChatID)
		if e.Target != p.Result || !strings.Contains(e.Text, "livreur") {
			t.Errorf("wrong edit: %+v", e)
		}
	}
}

func TestDescriptionEditsTextWhenNoSnapshot(t *testing.T) {
	h := newHarness(t, "events")
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	h.send(t, "frigate/tracked_object_update", descriptionMsg(evID, "Un chat."))
	if len(h.tg.byMethod("editMessageText")) != 2 {
		t.Error("text messages must be edited with editMessageText")
	}
}

func TestDescriptionForReviewMatchesDetection(t *testing.T) {
	h := newHarness(t, "reviews")
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.send(t, "frigate/reviews", reviewMsg("new", revID, "garage", "alert", []string{"person"}, nil, []string{evID}))
	h.send(t, "frigate/tracked_object_update", descriptionMsg(evID, "Quelqu'un sonne."))
	if len(h.tg.byMethod("editMessageCaption")) != 2 {
		t.Error("the description of a detection must update the review")
	}
}

func TestDescriptionForUnknownEventIgnored(t *testing.T) {
	h := newHarness(t, "events")
	h.send(t, "frigate/tracked_object_update", descriptionMsg("inconnu", "x"))
	if h.tg.count() != 0 {
		t.Error("want no send")
	}
}
