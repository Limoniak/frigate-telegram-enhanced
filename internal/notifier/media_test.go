package notifier

import (
	"context"
	"errors"
	"os"
	"strings"
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

// compressHarness has compress_clips on and a fake re-encoding, which records the
// size of the clip it gets and returns a small clip, or fails with err.
func compressHarness(t *testing.T, err error) (*harness, *atomic.Int64) {
	t.Helper()
	var got atomic.Int64
	h := newHarness(t, "events", func(d *Deps) {
		d.Transcode = func(_ context.Context, in string, max int64) (string, error) {
			if st, serr := os.Stat(in); serr == nil {
				got.Store(st.Size())
			}
			if err != nil {
				return "", err
			}
			f, ferr := os.CreateTemp("", "small-*.mp4")
			if ferr != nil {
				return "", ferr
			}
			f.WriteString("small mp4")
			return f.Name(), f.Close()
		}
	})
	on := true
	o := h.n.Config.CurrentOverlay()
	o.Notify.CompressClips = &on
	if err := h.n.Config.ApplyOverlay(&o); err != nil {
		t.Fatal(err)
	}
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.fr.files[frigate.EventClipPath(evID)] = []byte("mp4")
	return h, &got
}

func TestLargeClipIsCompressed(t *testing.T) {
	h, got := compressHarness(t, nil)
	h.fr.sizes[frigate.EventClipPath(evID)] = 70 << 20
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	h.send(t, "frigate/events", eventMsg("end", evID, "garage", "person", nil))
	if got.Load() != 70<<20 {
		t.Errorf("re-encoded a clip of %d bytes, want the whole 70 MB clip", got.Load())
	}
	if n := len(h.tg.videos()); n != 2 {
		t.Errorf("videos sent = %d, want 2", n)
	}
	for _, m := range h.tg.byMethod("sendMessage") {
		if strings.Contains(m.Text, "clip.mp4") {
			t.Errorf("unexpected link: %q", m.Text)
		}
	}
}

func TestSmallClipIsNotCompressed(t *testing.T) {
	h, got := compressHarness(t, nil)
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	h.send(t, "frigate/events", eventMsg("end", evID, "garage", "person", nil))
	if got.Load() != 0 {
		t.Error("a clip under the limit must be sent as is")
	}
	if n := len(h.tg.videos()); n != 2 {
		t.Errorf("videos sent = %d, want 2", n)
	}
}

func TestFailedCompressionSendsLink(t *testing.T) {
	h, _ := compressHarness(t, errors.New("ffmpeg: not found"))
	h.fr.sizes[frigate.EventClipPath(evID)] = 70 << 20
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	h.send(t, "frigate/events", eventMsg("end", evID, "garage", "person", nil))
	if len(h.tg.videos()) != 0 {
		t.Error("want no video")
	}
	if !sentLink(h) {
		t.Errorf("want the link to the clip, messages = %+v", h.tg.byMethod("sendMessage"))
	}
}

func TestClipOverCompressInputSendsLink(t *testing.T) {
	h, got := compressHarness(t, nil)
	h.fr.sizes[frigate.EventClipPath(evID)] = maxCompressInput + 1
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	h.send(t, "frigate/events", eventMsg("end", evID, "garage", "person", nil))
	if got.Load() != 0 || len(h.tg.videos()) != 0 || !sentLink(h) {
		t.Errorf("re-encoded=%d videos=%d, want only the link", got.Load(), len(h.tg.videos()))
	}
}

func TestCompressionOffSendsLink(t *testing.T) {
	h := newHarness(t, "events")
	if h.n.Config.Global().CompressClips {
		t.Fatal("compress_clips must be off by default")
	}
	h.fr.files[frigate.EventSnapshotPath(evID)] = []byte("jpeg")
	h.fr.files[frigate.EventClipPath(evID)] = []byte("mp4")
	h.fr.sizes[frigate.EventClipPath(evID)] = 70 << 20
	h.send(t, "frigate/events", eventMsg("new", evID, "garage", "person", nil))
	h.send(t, "frigate/events", eventMsg("end", evID, "garage", "person", nil))
	if len(h.tg.videos()) != 0 || !sentLink(h) {
		t.Errorf("videos=%d, want only the link", len(h.tg.videos()))
	}
}

func sentLink(h *harness) bool {
	for _, m := range h.tg.byMethod("sendMessage") {
		if strings.Contains(m.Text, "/api/events/"+evID+"/clip.mp4") {
			return true
		}
	}
	return false
}
