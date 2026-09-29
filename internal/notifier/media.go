package notifier

import (
	"context"
	"errors"
	"html"
	"os"
	"sync"
	"time"

	"frigate-telegram-enhanced/internal/frigate"
	"frigate-telegram-enhanced/internal/telegram"
)

type sendFunc func(ctx context.Context, chatID int64, chat string, f telegram.InputFile) (telegram.Message, error)

// deliver envoie un contenu à plusieurs chats. Si f porte un fichier, il est uploadé
// une seule fois (premier chat qui l'accepte) ; les autres réutilisent le file_id, en parallèle.
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
				continue // on retente l'upload avec le chat suivant
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

// fetchSnapshot récupère le snapshot (un nouvel essai si Frigate ne l'a pas encore) ; nil si indisponible.
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

// sendSnapshot envoie la notification initiale (photo, ou texte si pas de snapshot), puis ferme t.ready.
// silent indique, chat par chat, s'il la reçoit sans son.
func (n *Notifier) sendSnapshot(ctx context.Context, t *tracked, path, caption string, silent func(chat string) bool, chats []string) {
	defer close(t.ready)
	markup := buttons(t.camera, t.id)
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

// sendFollowUp envoie le clip ("video") ou le GIF ("animation") en réponse au snapshot.
func (n *Notifier) sendFollowUp(ctx context.Context, t *tracked, kind, path string, chats []string, delay time.Duration) {
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
	file, err := n.download(ctx, path)
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
			o := telegram.SendOptions{Silent: true, ReplyTo: t.message(chat).id}
			if kind == "animation" {
				return n.Telegram.SendAnimation(ctx, chatID, f, o)
			}
			return n.Telegram.SendVideo(ctx, chatID, f, o)
		}, nil)
}

// download récupère un média dans un fichier temporaire, avec les nouveaux essais de ClipRetryDelays.
func (n *Notifier) download(ctx context.Context, path string) (string, error) {
	for attempt := 0; ; attempt++ {
		start := time.Now()
		file, err := n.Frigate.DownloadToFile(ctx, path, maxUploadSize)
		n.Metrics.MediaDownload.Observe(time.Since(start).Seconds())
		if err == nil {
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

func (n *Notifier) tooLargeText(path string) string {
	l := n.Config.Language
	text := l.T("🎬 Clip too large for Telegram (over 50 MB).", "🎬 Clip trop volumineux pour Telegram (plus de 50 Mo).")
	if base := n.Config.Frigate.ExternalURL; base != "" {
		text += "\n<a href=\"" + html.EscapeString(base+path) + "\">" + l.T("Download the clip", "Télécharger le clip") + "</a>"
	}
	return text
}

// acquireMedia bloque jusqu'à obtenir un slot du pool de téléchargement (clip/GIF),
// ou jusqu'à l'annulation de ctx.
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
