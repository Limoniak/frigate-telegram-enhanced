package notifier

import (
	"context"
	"errors"
	"html"
	"net/url"
	"os"
	"sync"

	"frigate-telegram-enhanced/internal/config"
	"frigate-telegram-enhanced/internal/frigate"
	"frigate-telegram-enhanced/internal/i18n"
	"frigate-telegram-enhanced/internal/telegram"
)

// SendClipTo envoie le clip d'un événement ou d'une review à un chat, en réponse à replyTo.
func (n *Notifier) SendClipTo(ctx context.Context, chatID int64, replyTo int, id string) error {
	path, err := n.clipPathFor(ctx, id)
	if err != nil {
		return err
	}
	return n.sendClipFile(ctx, chatID, replyTo, path)
}

func (n *Notifier) clipPathFor(ctx context.Context, id string) (string, error) {
	n.mu.Lock()
	path := ""
	if t := n.findByEventID(id); t != nil {
		path = t.clipPath
		if n.Config.Mode == config.ModeReviews && !t.ended {
			path = frigate.RecordingClipPath(t.camera, t.startTS, float64(n.Now().Unix()))
		}
	}
	n.mu.Unlock()
	if path != "" {
		return path, nil
	}
	if n.Config.Mode == config.ModeReviews {
		if r, err := n.Frigate.Review(ctx, id); err == nil {
			end := float64(n.Now().Unix())
			if r.EndTime != nil {
				end = *r.EndTime
			}
			return frigate.RecordingClipPath(r.Camera, r.StartTime, end), nil
		}
	}
	return frigate.EventClipPath(id), nil
}

func (n *Notifier) sendClipFile(ctx context.Context, chatID int64, replyTo int, path string) error {
	if err := n.acquireMedia(ctx); err != nil {
		return err
	}
	defer n.releaseMedia()
	file, err := n.download(ctx, path)
	if errors.Is(err, frigate.ErrTooLarge) {
		_, err = n.Telegram.SendMessage(ctx, chatID, n.tooLargeText(path), telegram.SendOptions{ReplyTo: replyTo})
		return err
	}
	if err != nil {
		return err
	}
	defer os.Remove(file)
	_, err = n.Telegram.SendVideo(ctx, chatID, telegram.InputFile{Name: "clip.mp4", Path: file}, telegram.SendOptions{ReplyTo: replyTo})
	if err == nil {
		n.Metrics.NotificationsSent.WithLabelValues("video").Inc()
	}
	return err
}

// SendLast renvoie le dernier événement (d'une caméra si camera n'est pas vide) : snapshot puis clip.
func (n *Notifier) SendLast(ctx context.Context, chatID int64, camera string) error {
	evs, err := n.Frigate.Events(ctx, camera, 1)
	if err != nil {
		return err
	}
	if len(evs) == 0 {
		_, err := n.Telegram.SendMessage(ctx, chatID, n.Config.Language.T("No event found.", "Aucun événement trouvé."), telegram.SendOptions{})
		return err
	}
	ev := evs[0]
	caption := buildCaption(captionData{
		Label: ev.Label, SubLabel: string(ev.SubLabel), Camera: ev.Camera, Zones: ev.Zones,
		Score: ev.Score(), HasScore: true, Start: frigate.UnixTime(ev.StartTime),
		Link: n.uiLink("/explore?event_id=" + url.QueryEscape(ev.ID)),
	}, n.Config.Location, n.Config.Language)
	markup := buttons(ev.Camera, ev.ID, n.Config.Language)

	var msg telegram.Message
	if ev.HasSnapshot {
		path := frigate.EventSnapshotPath(ev.ID)
		if n.Config.ForCamera(ev.Camera).Crop {
			path = frigate.Cropped(path)
		}
		if photo := n.fetchSnapshot(ctx, path); photo != nil {
			msg, err = n.Telegram.SendPhoto(ctx, chatID, telegram.InputFile{Name: "snapshot.jpg", Data: photo},
				telegram.SendOptions{Caption: caption, Markup: markup})
			if err != nil {
				return err
			}
		}
	}
	if msg.MessageID == 0 {
		if msg, err = n.Telegram.SendMessage(ctx, chatID, caption, telegram.SendOptions{Markup: markup}); err != nil {
			return err
		}
	}
	if ev.HasClip && ev.EndTime != nil {
		return n.sendClipFile(ctx, chatID, msg.MessageID, frigate.EventClipPath(ev.ID))
	}
	return nil
}

// ErrNoRecipient signale une caméra dont les réglages n'ont aucun destinataire.
var ErrNoRecipient = i18n.NewError("no recipient for this camera", "aucun destinataire pour cette caméra")

// SendTest envoie une notification d'exemple pour camera, telle qu'un vrai
// événement la produirait avec les réglages en vigueur : destinataires, image en
// direct ou texte seul. Les médias de suivi (clip, GIF) n'existent pas pour un test :
// la légende signale seulement qu'ils suivraient. Renvoie une erreur si aucun
// destinataire n'a reçu le message.
func (n *Notifier) SendTest(ctx context.Context, camera string) error {
	cfg := n.Config.ForCamera(camera)
	if len(cfg.Chats) == 0 {
		return ErrNoRecipient
	}
	l := n.Config.Language
	caption := "🧪 <b>" + l.T("Test notification", "Notification de test") + "</b> — " + html.EscapeString(camera) +
		"\n🕑 " + n.Now().In(n.Config.Location).Format(l.DateTime())
	switch {
	case cfg.Clip:
		caption += l.T("\n🎬 On a real event, the video clip will follow as a reply.",
			"\n🎬 Lors d'un vrai événement, le clip vidéo suivra en réponse.")
	case cfg.GIF:
		caption += l.T("\n🎞 On a real event, an animated GIF will follow as a reply.",
			"\n🎞 Lors d'un vrai événement, un GIF animé suivra en réponse.")
	}
	if link := n.uiLink("/#" + url.PathEscape(camera)); link != "" {
		caption += openInFrigate(link, l)
	}

	var photo []byte
	if cfg.Snapshot {
		photo = n.fetchSnapshot(ctx, frigate.LatestPath(camera))
	}
	var mu sync.Mutex
	var sent int
	var lastErr error
	record := func(m telegram.Message, err error) (telegram.Message, error) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			lastErr = err
		} else {
			sent++
		}
		return m, err
	}
	if photo == nil {
		n.deliver(ctx, cfg.Chats, "text", telegram.InputFile{},
			func(ctx context.Context, chatID int64, _ string, _ telegram.InputFile) (telegram.Message, error) {
				return record(n.Telegram.SendMessage(ctx, chatID, caption, telegram.SendOptions{}))
			}, nil)
	} else {
		n.deliver(ctx, cfg.Chats, "photo", telegram.InputFile{Name: "test.jpg", Data: photo},
			func(ctx context.Context, chatID int64, _ string, f telegram.InputFile) (telegram.Message, error) {
				return record(n.Telegram.SendPhoto(ctx, chatID, f, telegram.SendOptions{Caption: caption}))
			}, nil)
	}
	if sent == 0 && lastErr != nil {
		return lastErr
	}
	return nil
}
