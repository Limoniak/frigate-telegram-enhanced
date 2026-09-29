package notifier

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"frigate-telegram-enhanced/internal/frigate"
)

// TestMediaPoolBoundsConcurrentDownloads vérifie que le pool limite le nombre de
// DownloadToFile concurrents (F1) : deux événements se terminant en même temps sur
// des caméras différentes ne doivent pas dépasser MediaWorkers téléchargements en
// parallèle, et les deux clips doivent quand même être livrés.
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
	// Déclenche les deux fins d'événement "en même temps" pour forcer la concurrence.
	for _, cam := range cams {
		h.n.Process(context.Background(), "frigate/events", eventMsg("end", cam, cam, "person", nil))
	}
	if !h.n.Wait(5 * time.Second) {
		t.Fatal("envois non terminés après 5 s")
	}

	if max := atomic.LoadInt32(&h.fr.maxConcurrent); max != 1 {
		t.Errorf("concurrence max des téléchargements = %d, attendu 1", max)
	}
	if n := len(h.tg.videos()); n != 4 {
		t.Errorf("vidéos envoyées = %d, attendu 4 (2 événements x 2 chats)", n)
	}
}
