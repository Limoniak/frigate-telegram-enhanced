package notifier

import (
	"context"
	"errors"
	"html"
	"os"
	"strings"
	"sync"
	"time"

	"frigate-telegram-enhanced/internal/frigate"
	"frigate-telegram-enhanced/internal/mp4fix"
	"frigate-telegram-enhanced/internal/telegram"
)

type sendFunc func(ctx context.Context, chatID int64, chat string, f telegram.InputFile) (telegram.Message, error)

// deliver sends a content to several chats. If f holds a file, it is uploaded only
// once (first chat that accepts it); the others reuse the file_id, in parallel.
func (n *Notifier) deliver(ctx context.Context, chats []string, kind string, f telegram.InputFile, send sendFunc, onSent func(chat string, m telegram.Message)) {
	one := func(chat string, f telegram.InputFile) (telegram.Message, error) {
		m, err := send(ctx, n.Config.ChatID(chat), chat, f)
		if err != nil {
			n.Log.Error("Telegram send failed", "kind", kind, "chat", chat, "err", err)
			return m, err
		}
		n.Metrics.NotificationsSent.WithLabelValues(kind).Inc()
		if onSent != nil {
			onSent(chat, m)
		}
		return m, nil
	}
	rest := chats
	if f.Data != nil || f.Path != "" {
		for i, chat := range chats {
			rest = chats[i+1:]
			m, err := one(chat, f)
			if err != nil {
				continue // retry the upload with the next chat
			}
			if id := m.FileID(); id != "" {
				f = telegram.InputFile{FileID: id}
			}
			break
		}
	}
	var wg sync.WaitGroup
	for _, chat := range rest {
		wg.Add(1)
		go func() {
			defer wg.Done()
			one(chat, f)
		}()
	}
	wg.Wait()
}

// fetchSnapshot gets the snapshot (one more try if Frigate does not have it yet); nil if unavailable.
func (n *Notifier) fetchSnapshot(ctx context.Context, path string) []byte {
	if path == "" {
		return nil
	}
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 && sleepCtx(ctx, n.SnapshotRetryDelay) != nil {
			return nil
		}
		start := time.Now()
		b, err := n.Frigate.GetBytes(ctx, path, maxPhotoSize)
		n.Metrics.MediaDownload.Observe(time.Since(start).Seconds())
		if err == nil {
			return b
		}
		n.Log.Warn("snapshot unavailable", "path", path, "attempt", attempt+1, "err", err)
		if !frigate.Retryable(err) {
			return nil
		}
	}
	return nil
}

// sendSnapshot sends the initial notification (photo, or text without a snapshot), then closes t.ready.
// silent tells, chat by chat, whether it gets it silently.
func (n *Notifier) sendSnapshot(ctx context.Context, t *tracked, path, caption string, silent func(chat string) bool, chats []string) {
	defer close(t.ready)
	markup := buttons(t.camera, t.id, n.Config.Language)
	photo := n.fetchSnapshot(ctx, path)
	if photo == nil {
		n.deliver(ctx, chats, "text", telegram.InputFile{},
			func(ctx context.Context, chatID int64, chat string, _ telegram.InputFile) (telegram.Message, error) {
				return n.Telegram.SendMessage(ctx, chatID, caption, telegram.SendOptions{Silent: silent(chat), Markup: markup})
			},
			func(chat string, m telegram.Message) { t.setMessage(chat, sentMsg{id: m.MessageID, text: true}) })
		return
	}
	n.deliver(ctx, chats, "photo", telegram.InputFile{Name: "snapshot.jpg", Data: photo},
		func(ctx context.Context, chatID int64, chat string, f telegram.InputFile) (telegram.Message, error) {
			return n.Telegram.SendPhoto(ctx, chatID, f, telegram.SendOptions{Caption: caption, Silent: silent(chat), Markup: markup})
		},
		func(chat string, m telegram.Message) { t.setMessage(chat, sentMsg{id: m.MessageID}) })
}

// sendFollowUp sends the clip ("video") or the GIF ("animation"). With inPlace, it
// replaces the image of the notification message (same message, no new ring);
// otherwise, or if that message has no image, it arrives as a reply.
func (n *Notifier) sendFollowUp(ctx context.Context, t *tracked, kind, path string, chats []string, delay time.Duration, inPlace bool) {
	select {
	case <-t.ready:
	case <-ctx.Done():
		return
	}
	if err := sleepCtx(ctx, delay); err != nil {
		return
	}
	if err := n.acquireMedia(ctx); err != nil {
		return
	}
	defer n.releaseMedia()
	var file string
	var err error
	if kind == "video" {
		file, err = n.fetchClip(ctx, path)
	} else {
		file, err = n.download(ctx, path, maxUploadSize)
	}
	if errors.Is(err, frigate.ErrTooLarge) {
		text := n.tooLargeText(path)
		n.deliver(ctx, chats, "text", telegram.InputFile{},
			func(ctx context.Context, chatID int64, chat string, _ telegram.InputFile) (telegram.Message, error) {
				return n.Telegram.SendMessage(ctx, chatID, text, telegram.SendOptions{Silent: true, ReplyTo: t.message(chat).id})
			}, nil)
		return
	}
	if err != nil {
		n.Log.Warn("media unavailable", "kind", kind, "event_id", t.id, "path", path, "err", err)
		return
	}
	defer os.Remove(file)
	name := "clip.mp4"
	if kind == "animation" {
		name = "preview.gif"
	}
	n.deliver(ctx, chats, kind, telegram.InputFile{Name: name, Path: file},
		func(ctx context.Context, chatID int64, chat string, f telegram.InputFile) (telegram.Message, error) {
			m := t.message(chat)
			if inPlace && m.id != 0 && !m.text {
				n.mu.Lock()
				caption := n.captionFor(t, chat)
				n.mu.Unlock()
				edited, err := n.Telegram.EditMessageMedia(ctx, chatID, m.id, kind, f, caption, buttons(t.camera, t.id, n.Config.Language))
				if err == nil {
					return edited, nil
				}
				// Message deleted in the meantime, for example: fall back to a reply.
				n.Log.Warn("replacing the image failed, sending as a reply", "kind", kind, "chat", chat, "err", err)
			}
			o := telegram.SendOptions{Silent: true, ReplyTo: m.id}
			if kind == "animation" {
				return n.Telegram.SendAnimation(ctx, chatID, f, o)
			}
			return n.Telegram.SendVideo(ctx, chatID, f, o)
		}, nil)
}

// fetchClip downloads a clip that Telegram accepts. A clip over the upload limit
// gives frigate.ErrTooLarge (a link is sent instead), unless compress_clips has
// it re-encoded under the limit.
func (n *Notifier) fetchClip(ctx context.Context, path string) (string, error) {
	if !n.Config.Global().CompressClips {
		file, err := n.download(ctx, path, maxUploadSize)
		if errors.Is(err, frigate.ErrTooLarge) {
			n.Log.Warn("clip over the Telegram limit, sending a link", "path", path, "limit_mb", maxUploadSize>>20)
		}
		return file, err
	}
	file, err := n.download(ctx, path, maxCompressInput)
	if err != nil {
		if errors.Is(err, frigate.ErrTooLarge) {
			n.Log.Warn("clip too large even to re-encode, sending a link", "path", path, "limit_mb", maxCompressInput>>20)
		}
		return "", err
	}
	st, err := os.Stat(file)
	if err != nil || st.Size() <= maxUploadSize {
		return file, err
	}
	defer os.Remove(file)
	select {
	case n.transcoding <- struct{}{}:
		defer func() { <-n.transcoding }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	start := time.Now()
	out, err := n.Transcode(ctx, file, maxUploadSize)
	if err != nil {
		n.Log.Warn("re-encoding the clip failed, sending a link", "path", path, "size_mb", st.Size()>>20, "err", err)
		return "", frigate.ErrTooLarge
	}
	if o, err := os.Stat(out); err == nil {
		n.Log.Info("clip re-encoded", "path", path, "size_mb", st.Size()>>20, "new_size_mb", o.Size()>>20, "took", time.Since(start).Round(time.Second))
	}
	return out, nil
}

// download fetches a media into a temporary file, with the retries of ClipRetryDelays.
func (n *Notifier) download(ctx context.Context, path string, max int64) (string, error) {
	for attempt := 0; ; attempt++ {
		start := time.Now()
		file, err := n.Frigate.DownloadToFile(ctx, path, max)
		n.Metrics.MediaDownload.Observe(time.Since(start).Seconds())
		if err != nil && file != "" { // partly received (frigate.ErrIncomplete)
			if n.keepCompletePart(file, path) {
				err = nil
			} else {
				os.Remove(file)
			}
		}
		if err == nil {
			n.repairClip(file, path)
			return file, nil
		}
		if !frigate.Retryable(err) || attempt >= len(n.ClipRetryDelays) {
			return "", err
		}
		if serr := sleepCtx(ctx, n.ClipRetryDelays[attempt]); serr != nil {
			return "", serr
		}
	}
}

// repairClip fixes the aberrant timestamps of a Frigate MP4 clip (see mp4fix):
// without it, Telegram may announce a video of several hours that does not play.
// A failure leaves the file as is.
func (n *Notifier) repairClip(file, path string) {
	if !isMP4(path) {
		return
	}
	fixed, err := mp4fix.Fix(file)
	switch {
	case err != nil:
		n.Log.Debug("clip not repaired", "path", path, "err", err)
	case fixed > 0:
		n.Log.Info("clip timestamps repaired", "path", path, "samples", fixed)
	}
}

// keepCompletePart cuts a partly received MP4 clip after its last complete
// fragment; false if nothing sendable is left.
func (n *Notifier) keepCompletePart(file, path string) bool {
	if !isMP4(path) {
		return false
	}
	frags, err := mp4fix.Trim(file)
	if err != nil || frags == 0 {
		n.Log.Warn("clip incomplete, nothing to send", "path", path, "err", err)
		return false
	}
	n.Log.Warn("Frigate stopped sending the clip, sending its complete part", "path", path, "fragments", frags)
	return true
}

func isMP4(path string) bool {
	p, _, _ := strings.Cut(path, "?")
	return strings.HasSuffix(p, ".mp4")
}

func (n *Notifier) tooLargeText(path string) string {
	l := n.Config.Language
	text := l.T("🎬 Clip too large for Telegram (over 50 MB).")
	if base := n.Config.ExternalURL(); base != "" {
		text += "\n<a href=\"" + html.EscapeString(base+path) + "\">" + l.T("Download the clip") + "</a>"
	}
	return text
}

// acquireMedia blocks until it gets a slot of the download pool (clip/GIF), or
// until ctx is canceled.
func (n *Notifier) acquireMedia(ctx context.Context) error {
	select {
	case n.media <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (n *Notifier) releaseMedia() { <-n.media }

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
