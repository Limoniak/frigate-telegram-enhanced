package bot

import (
	"context"
	"fmt"
	"html"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"frigate-telegram/internal/actions"
	"frigate-telegram/internal/frigate"
	"frigate-telegram/internal/state"
	"frigate-telegram/internal/telegram"
)

const helpText = `<b>Commandes</b>
/pause [durée] [caméra] — pause (1 h par défaut, 0 = jusqu'à /resume)
/resume [caméra] — reprendre (sans argument : tout reprendre)
/status — état du service
/cameras — liste des caméras
/snapshot [caméra] — image en direct
/last [caméra] — dernier événement
Durées : 30m, 2h, 1h30m, 1d`

var esc = html.EscapeString

// parseCommand découpe "/cmd@bot arg1 arg2" en ("cmd", [arg1 arg2]).
func parseCommand(text string) (string, []string) {
	f := strings.Fields(text)
	if len(f) == 0 {
		return "", nil
	}
	name := strings.TrimPrefix(f[0], "/")
	if i := strings.IndexByte(name, '@'); i >= 0 {
		name = name[:i]
	}
	return strings.ToLower(name), f[1:]
}

// ParseDuration accepte "30m", "2h", "1h30m", "1d" et "0" (illimité).
func ParseDuration(s string) (time.Duration, error) {
	if s == "0" {
		return 0, nil
	}
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("durée invalide %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("durée invalide %q", s)
	}
	return d, nil
}

func (b *Bot) handleCommand(ctx context.Context, m telegram.Message) {
	if m.From == nil || !b.Config.IsAdmin(m.From.ID) {
		from := int64(0)
		if m.From != nil {
			from = m.From.ID
		}
		b.Log.Warn("commande refusée : utilisateur non autorisé", "user", from, "text", m.Text)
		return
	}
	name, args := parseCommand(m.Text)
	chat := m.Chat.ID
	switch name {
	case "pause":
		b.reply(ctx, chat, b.cmdPause(ctx, args))
	case "resume":
		b.reply(ctx, chat, b.cmdResume(args))
	case "status":
		b.reply(ctx, chat, b.cmdStatus())
	case "cameras":
		b.reply(ctx, chat, b.cmdCameras(ctx))
	case "snapshot":
		b.async(func() { b.cmdSnapshot(ctx, chat, args) })
	case "last":
		camera := ""
		if len(args) > 0 {
			camera = args[0]
		}
		b.async(func() {
			if err := b.Notifier.SendLast(ctx, chat, camera); err != nil {
				b.reply(ctx, chat, "⚠️ "+esc(err.Error()))
			}
		})
	case "help", "start":
		b.reply(ctx, chat, helpText)
	default:
		b.reply(ctx, chat, "Commande inconnue. /help")
	}
}

func (b *Bot) cmdPause(ctx context.Context, args []string) string {
	dur, camera := time.Hour, ""
	for _, a := range args {
		if d, err := ParseDuration(a); err == nil {
			dur = d
			continue
		}
		camera = a
	}
	if camera != "" {
		if err := b.checkCamera(ctx, camera); err != nil {
			return err.Error()
		}
	}
	until := state.Forever
	if dur > 0 {
		until = b.Now().Add(dur)
	}
	var err error
	target := "Notifications"
	if camera == "" {
		err = b.State.Pause(until)
	} else {
		err = b.State.Mute(camera, until)
		target = "Caméra <b>" + esc(camera) + "</b>"
	}
	if err != nil {
		return "⚠️ Impossible d'enregistrer l'état : " + esc(err.Error())
	}
	return "⏸ " + target + " en pause " + b.untilText(until)
}

func (b *Bot) cmdResume(args []string) string {
	if len(args) == 0 {
		if err := b.State.Resume(); err != nil {
			return "⚠️ " + esc(err.Error())
		}
		return "▶️ Notifications réactivées (toutes les caméras)."
	}
	if err := b.State.Unmute(args[0]); err != nil {
		return "⚠️ " + esc(err.Error())
	}
	return "▶️ Caméra <b>" + esc(args[0]) + "</b> réactivée."
}

func (b *Bot) cmdStatus() string {
	now := b.Now()
	st := b.State.Status(now)
	var sb strings.Builder
	sb.WriteString("<b>État</b>\n")
	if b.MQTTConnected() {
		sb.WriteString("MQTT : ✅ connecté\n")
	} else {
		sb.WriteString("MQTT : ❌ déconnecté\n")
	}
	if st.PausedUntil.IsZero() {
		sb.WriteString("▶️ Notifications actives")
	} else {
		sb.WriteString("⏸ Pause globale " + b.untilText(st.PausedUntil))
	}
	for _, cam := range slices.Sorted(maps.Keys(st.Mutes)) {
		sb.WriteString("\n🔇 " + esc(cam) + " " + b.untilText(st.Mutes[cam]))
	}
	fmt.Fprintf(&sb, "\n📨 %d notification(s) sur 24 h", b.Notifier.Count24h())
	return sb.String()
}

func (b *Bot) cmdCameras(ctx context.Context) string {
	cams, err := b.Frigate.Cameras(ctx)
	if err != nil {
		return "⚠️ Frigate injoignable : " + esc(err.Error())
	}
	now := b.Now()
	var sb strings.Builder
	sb.WriteString("<b>Caméras</b>")
	for _, cam := range cams {
		icon := "✅"
		switch {
		case !b.Config.ForCamera(cam).Enabled:
			icon = "🚫"
		case b.State.IsMuted(cam, now):
			icon = "🔇"
		}
		sb.WriteString("\n" + icon + " " + esc(cam))
	}
	return sb.String()
}

func (b *Bot) cmdSnapshot(ctx context.Context, chat int64, args []string) {
	if len(args) > 0 {
		b.sendLiveSnapshot(ctx, chat, args[0])
		return
	}
	cams, err := b.Frigate.Cameras(ctx)
	if err != nil || len(cams) == 0 {
		b.reply(ctx, chat, "⚠️ Impossible de lister les caméras.")
		return
	}
	var rows [][]telegram.InlineKeyboardButton
	for i, cam := range cams {
		if i%2 == 0 {
			rows = append(rows, nil)
		}
		rows[len(rows)-1] = append(rows[len(rows)-1], telegram.InlineKeyboardButton{Text: cam, CallbackData: actions.Snapshot(cam)})
	}
	if _, err := b.Telegram.SendMessage(ctx, chat, "📷 Choisis une caméra :",
		telegram.SendOptions{Markup: &telegram.InlineKeyboardMarkup{InlineKeyboard: rows}}); err != nil {
		b.Log.Warn("envoi du clavier échoué", "err", err)
	}
}

func (b *Bot) sendLiveSnapshot(ctx context.Context, chat int64, camera string) {
	img, err := b.Frigate.GetBytes(ctx, frigate.LatestPath(camera), 10<<20)
	if err != nil {
		b.reply(ctx, chat, "⚠️ Snapshot indisponible pour "+esc(camera))
		return
	}
	caption := "📷 <b>" + esc(camera) + "</b> — " + b.Now().In(b.Config.Location).Format("15:04:05")
	if _, err := b.Telegram.SendPhoto(ctx, chat, telegram.InputFile{Name: camera + ".jpg", Data: img},
		telegram.SendOptions{Caption: caption}); err != nil {
		b.Log.Warn("envoi du snapshot échoué", "camera", camera, "err", err)
	}
}

func (b *Bot) handleCallback(ctx context.Context, q telegram.CallbackQuery) {
	if !b.Config.IsAdmin(q.From.ID) {
		b.Log.Warn("bouton refusé : utilisateur non autorisé", "user", q.From.ID)
		b.answer(ctx, q.ID, "⛔ Non autorisé")
		return
	}
	a, err := actions.Parse(q.Data)
	if err != nil {
		b.answer(ctx, q.ID, "Action inconnue")
		return
	}
	var chat int64
	replyTo := 0
	if q.Message != nil {
		chat, replyTo = q.Message.Chat.ID, q.Message.MessageID
	}
	now := b.Now()
	switch a.Kind {
	case actions.KindMute:
		until := now.Add(a.Duration)
		if err := b.State.Mute(a.Camera, until); err != nil {
			b.answer(ctx, q.ID, "⚠️ Erreur d'enregistrement")
			return
		}
		b.answer(ctx, q.ID, "🔇 "+a.Camera+" coupée "+b.untilText(until))
	case actions.KindPause:
		until := now.Add(a.Duration)
		if err := b.State.Pause(until); err != nil {
			b.answer(ctx, q.ID, "⚠️ Erreur d'enregistrement")
			return
		}
		b.answer(ctx, q.ID, "⏸ Pause "+b.untilText(until))
	case actions.KindClip, actions.KindSnapshot:
		if chat == 0 {
			b.answer(ctx, q.ID, "⚠️ Message trop ancien")
			return
		}
		if a.Kind == actions.KindClip {
			b.answer(ctx, q.ID, "🎬 Envoi du clip…")
			b.async(func() {
				if err := b.Notifier.SendClipTo(ctx, chat, replyTo, a.ID); err != nil {
					b.reply(ctx, chat, "⚠️ Clip indisponible : "+esc(err.Error()))
				}
			})
			return
		}
		b.answer(ctx, q.ID, "")
		b.async(func() { b.sendLiveSnapshot(ctx, chat, a.Camera) })
	}
}

// checkCamera vérifie que la caméra existe dans Frigate (accepte si Frigate est injoignable).
func (b *Bot) checkCamera(ctx context.Context, camera string) error {
	cams, err := b.Frigate.Cameras(ctx)
	if err != nil || slices.Contains(cams, camera) {
		return nil
	}
	return fmt.Errorf("Caméra inconnue : %s. Caméras : %s", esc(camera), esc(strings.Join(cams, ", ")))
}

func (b *Bot) untilText(until time.Time) string {
	if !until.Before(state.Forever) {
		return "jusqu'à /resume"
	}
	return "jusqu'à " + until.In(b.Config.Location).Format("02/01 15:04")
}

func (b *Bot) reply(ctx context.Context, chat int64, text string) {
	if _, err := b.Telegram.SendMessage(ctx, chat, text, telegram.SendOptions{}); err != nil {
		b.Log.Warn("réponse Telegram échouée", "chat", chat, "err", err)
	}
}

func (b *Bot) answer(ctx context.Context, id, text string) {
	if err := b.Telegram.AnswerCallbackQuery(ctx, id, text); err != nil {
		b.Log.Warn("answerCallbackQuery échoué", "err", err)
	}
}
