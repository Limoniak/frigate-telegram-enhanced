package notifier

import (
	"context"
	"errors"
	"net/url"
	"os"

	"frigate-telegram/internal/config"
	"frigate-telegram/internal/frigate"
	"frigate-telegram/internal/telegram"
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
		_, err := n.Telegram.SendMessage(ctx, chatID, "Aucun événement trouvé.", telegram.SendOptions{})
		return err
	}
	ev := evs[0]
	caption := buildCaption(captionData{
		Label: ev.Label, SubLabel: string(ev.SubLabel), Camera: ev.Camera, Zones: ev.Zones,
		Score: ev.Score(), HasScore: true, Start: frigate.UnixTime(ev.StartTime),
		Link: n.uiLink("/explore?event_id=" + url.QueryEscape(ev.ID)),
	}, n.Config.Location)
	markup := buttons(ev.Camera, ev.ID)

	var msg telegram.Message
	if ev.HasSnapshot {
		if photo := n.fetchSnapshot(ctx, frigate.EventSnapshotPath(ev.ID)); photo != nil {
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
