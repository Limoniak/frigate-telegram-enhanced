package notifier

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"frigate-telegram-enhanced/internal/frigate"
)

// TestMediaPoolBoundsConcurrentDownloads checks that the pool bounds the number of
// concurrent DownloadToFile (F1): two events ending at the same time on different
// cameras must not exceed MediaWorkers parallel downloads, and both clips must
// still be delivered.
func TestMediaPoolBoundsConcurrentDownloads(t *testing.T) {
	h := newHarness(t, "events", func(d *Deps) { d.MediaWorkers = 1 })
	h.fr.downloadDelay = 50 * time.Millisecond

	cams := []string{"garage", "cour"}
	for _, id := range cams {
		h.fr.files[frigate.EventSnapshotPath(id)] = []byte("jpeg")
		h.fr.files[frigate.EventClipPath(id)] = []byte("mp4")
	}
	for _, cam := range cams {
		h.send(t, "frigate/events", eventMsg("new", cam, cam, "person", nil))
	}
	// Triggers both event ends "at the same time" to force concurrency.
	for _, cam := range cams {
		h.n.Process(context.Background(), "frigate/events", eventMsg("end", cam, cam, "person", nil))
	}
	if !h.n.Wait(5 * time.Second) {
		t.Fatal("sends not finished after 5 s")
	}

	if max := atomic.LoadInt32(&h.fr.maxConcurrent); max != 1 {
		t.Errorf("max download concurrency = %d, want 1", max)
	}
	if n := len(h.tg.videos()); n != 4 {
		t.Errorf("videos sent = %d, want 4 (2 events x 2 chats)", n)
	}
}
