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

// audioReviewMsg is a review of sounds only: Frigate sends no object and no detection.
func audioReviewMsg(typ, id, camera, severity string, objects, audio []string) []byte {
	r := map[string]any{
		"id": id, "camera": camera, "severity": severity, "start_time": 1790604000.0,
		"data": map[string]any{"detections": []string{}, "objects": objects, "sub_labels": []string{}, "zones": []string{}, "audio": audio},
	}
	if typ == "end" {
		r["end_time"] = 1790604030.5
	}
	b, _ := json.Marshal(map[string]any{"type": typ, "before": r, "after": r})
	return b
}

// allowLabels sets the labels notified for every camera.
func allowLabels(t *testing.T, h *harness, labels ...string) {
	t.Helper()
	o := h.n.Config.CurrentOverlay()
	o.Notify.Labels = &labels
	if err := h.n.Config.ApplyOverlay(&o); err != nil {
		t.Fatal(err)
	}
}

// A sound is notified like an object: its name in the caption, the camera's image of
// the moment (an audio review has no detection), then the recording.
func TestAudioReviewNotifiesTheSound(t *testing.T) {
	h := newHarness(t, "reviews")
	allowLabels(t, h, "person", "bark")
	h.fr.files[frigate.LatestPath("salon")] = []byte("jpeg")
	h.fr.files[frigate.RecordingClipPath("salon", 1790604000.0, 1790604030.5)] = []byte("mp4")
	h.send(t, "frigate/reviews", audioReviewMsg("update", revID, "salon", "alert", []string{}, []string{"bark"}))
	h.send(t, "frigate/reviews", audioReviewMsg("end", revID, "salon", "alert", []string{}, []string{"bark"}))

	photos := h.tg.byMethod("sendPhoto")
	if len(photos) != 2 || countData(photos, "jpeg") != 1 {
		t.Fatalf("photos = %+v", photos)
	}
	if !strings.Contains(photos[0].Text, "Barking") || !strings.Contains(photos[0].Text, "salon") {
		t.Errorf("caption = %q, want the sound and the camera", photos[0].Text)
	}
	if videos := h.tg.videos(); len(videos) != 2 || countData(videos, "mp4") != 1 {
		t.Fatalf("videos = %+v", videos)
	}
}

// The label filter applies to sounds: a sound left out of the labels is not notified.
func TestAudioReviewFollowsTheLabelFilter(t *testing.T) {
	h := newHarness(t, "reviews") // labels: [person]
	h.send(t, "frigate/reviews", audioReviewMsg("update", revID, "salon", "alert", []string{}, []string{"speech"}))
	if h.tg.count() != 0 {
		t.Error("speech is not among the labels: no notification")
	}
}

// A review with an object and a sound is named after the object.
func TestReviewWithObjectAndSoundNamedAfterTheObject(t *testing.T) {
	h := newHarness(t, "reviews")
	allowLabels(t, h, "person", "bark")
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	r := map[string]any{
		"id": revID, "camera": "garage", "severity": "alert", "start_time": 1790604000.0,
		"data": map[string]any{"detections": []string{evID}, "objects": []string{"person"}, "sub_labels": []string{}, "zones": []string{}, "audio": []string{"bark"}},
	}
	b, _ := json.Marshal(map[string]any{"type": "new", "before": r, "after": r})
	h.send(t, "frigate/reviews", b)
	photos := h.tg.byMethod("sendPhoto")
	if len(photos) != 2 || !strings.Contains(photos[0].Text, "Person") || countData(photos, "jpeg") != 1 {
		t.Fatalf("photos = %+v", photos)
	}
}
