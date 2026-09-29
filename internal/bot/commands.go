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

	"frigate-telegram-enhanced/internal/actions"
	"frigate-telegram-enhanced/internal/frigate"
	"frigate-telegram-enhanced/internal/i18n"
	"frigate-telegram-enhanced/internal/state"
	"frigate-telegram-enhanced/internal/telegram"
)

func helpText(l i18n.Lang) string {
	return l.T(`<b>Commands</b>
/menu — control panel with buttons
/pause [duration] [camera] — pause (1 h by default, 0 = until /resume)
/resume [camera] — resume (no argument: resume everything)
/status — service status
/cameras — list cameras
/snapshot [camera] — live image
/last [camera] — latest event
Durations: 30m, 2h, 1h30m, 1d`, `<b>Commandes</b>
/menu — tableau de contrôle avec boutons
/pause [durée] [caméra] — pause (1 h par défaut, 0 = jusqu'à /resume)
/resume [caméra] — reprendre (sans argument : tout reprendre)
/status — état du service
/cameras — liste des caméras
/snapshot [caméra] — image en direct
/last [caméra] — dernier événement
Durées : 30m, 2h, 1h30m, 1d`)
}

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
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	return d, nil
}

func (b *Bot) handleCommand(ctx context.Context, m telegram.Message) {
	if m.From == nil || !b.Config.IsAdmin(m.From.ID) {
		from := int64(0)
		if m.From != nil {
			from = m.From.ID
		}
		b.Log.Warn("command refused: user not allowed", "user", from, "text", m.Text)
		return
	}
	name, args := parseCommand(m.Text)
	chat := m.Chat.ID
	switch name {
	case "pause":
		b.serial(func() { b.reply(ctx, chat, b.cmdPause(ctx, args)) })
	case "resume":
		b.serial(func() { b.reply(ctx, chat, b.cmdResume(args)) })
	case "status":
		b.serial(func() { b.reply(ctx, chat, b.cmdStatus()) })
	case "cameras":
		b.serial(func() { b.reply(ctx, chat, b.cmdCameras(ctx)) })
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
	case "menu":
		b.serial(func() { b.cmdMenu(ctx, chat) })
	case "help", "start":
		b.serial(func() { b.reply(ctx, chat, helpText(b.Config.Language)) })
	default:
		b.serial(func() { b.reply(ctx, chat, b.Config.Language.T("Unknown command. /help", "Commande inconnue. /help")) })
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
		if msg := b.checkCamera(ctx, camera); msg != "" {
			return msg
		}
	}
	until := state.Forever
	if dur > 0 {
		until = b.Now().Add(dur)
	}
	l := b.Config.Language
	var err error
	target := l.T("Notifications paused", "Notifications en pause")
	if camera == "" {
		err = b.State.Pause(until)
	} else {
		err = b.State.Mute(camera, until)
		target = l.Tf("Camera <b>%s</b> paused", "Caméra <b>%s</b> en pause", esc(camera))
	}
	if err != nil {
		return l.T("⚠️ Could not save the state: ", "⚠️ Impossible d'enregistrer l'état : ") + esc(err.Error())
	}
	return "⏸ " + target + " " + b.untilText(until)
}

func (b *Bot) cmdResume(args []string) string {
	if len(args) == 0 {
		if err := b.State.Resume(); err != nil {
			return "⚠️ " + esc(err.Error())
		}
		return b.Config.Language.T("▶️ Notifications resumed (all cameras).", "▶️ Notifications réactivées (toutes les caméras).")
	}
	if err := b.State.Unmute(args[0]); err != nil {
		return "⚠️ " + esc(err.Error())
	}
	return b.Config.Language.Tf("▶️ Camera <b>%s</b> resumed.", "▶️ Caméra <b>%s</b> réactivée.", esc(args[0]))
}

func (b *Bot) cmdStatus() string {
	l := b.Config.Language
	now := b.Now()
	st := b.State.Status(now)
	var sb strings.Builder
	sb.WriteString(l.T("<b>Status</b>\n", "<b>État</b>\n"))
	if b.MQTTConnected() {
		sb.WriteString(l.T("MQTT: ✅ connected\n", "MQTT : ✅ connecté\n"))
	} else {
		sb.WriteString(l.T("MQTT: ❌ disconnected\n", "MQTT : ❌ déconnecté\n"))
	}
	if st.PausedUntil.IsZero() {
		sb.WriteString(l.T("▶️ Notifications active", "▶️ Notifications actives"))
	} else {
		sb.WriteString(l.T("⏸ Everything paused ", "⏸ Pause globale ") + b.untilText(st.PausedUntil))
	}
	for _, cam := range slices.Sorted(maps.Keys(st.Mutes)) {
		sb.WriteString("\n🔇 " + esc(cam) + " " + b.untilText(st.Mutes[cam]))
	}
	if b.Config.Presence.Enabled() {
		if len(st.Home) > 0 {
			names := make([]string, len(st.Home))
			for i, t := range st.Home {
				names[i] = esc(PresenceName(t))
			}
			sb.WriteString(l.T("\n🏠 At home: ", "\n🏠 À la maison : ") + strings.Join(names, ", "))
		} else {
			sb.WriteString(l.T("\n🚪 Nobody at home", "\n🚪 Personne à la maison"))
		}
	}
	sb.WriteString(l.Tf("\n📨 %d notification(s) in the last 24 h", "\n📨 %d notification(s) sur 24 h", b.Notifier.Count24h()))
	return sb.String()
}

func (b *Bot) cmdCameras(ctx context.Context) string {
	cams, err := b.cameras(ctx)
	if err != nil {
		return b.Config.Language.T("⚠️ Frigate unreachable: ", "⚠️ Frigate injoignable : ") + esc(err.Error())
	}
	now := b.Now()
	var sb strings.Builder
	sb.WriteString(b.Config.Language.T("<b>Cameras</b>", "<b>Caméras</b>"))
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
		b.sendLiveSnapshot(ctx, chat, 0, args[0])
		return
	}
	cams, err := b.cameras(ctx)
	if err != nil || len(cams) == 0 {
		b.reply(ctx, chat, b.Config.Language.T("⚠️ Could not list the cameras.", "⚠️ Impossible de lister les caméras."))
		return
	}
	var rows [][]telegram.InlineKeyboardButton
	for i, cam := range cams {
		if i%2 == 0 {
			rows = append(rows, nil)
		}
		rows[len(rows)-1] = append(rows[len(rows)-1], telegram.InlineKeyboardButton{Text: cam, CallbackData: actions.Snapshot(cam)})
	}
	if _, err := b.Telegram.SendMessage(ctx, chat, b.Config.Language.T("📷 Pick a camera:", "📷 Choisis une caméra :"),
		telegram.SendOptions{Markup: &telegram.InlineKeyboardMarkup{InlineKeyboard: rows}}); err != nil {
		b.Log.Warn("sending the keyboard failed", "err", err)
	}
}

// sendLiveSnapshot envoie l'image en direct d'une caméra, en réponse au message
// replyTo s'il n'est pas nul (la notification dont on a touché « 📷 Maintenant »).
func (b *Bot) sendLiveSnapshot(ctx context.Context, chat int64, replyTo int, camera string) {
	img, err := b.Frigate.GetBytes(ctx, frigate.LatestPath(camera), 10<<20)
	if err != nil {
		b.reply(ctx, chat, b.Config.Language.T("⚠️ No snapshot available for ", "⚠️ Snapshot indisponible pour ")+esc(camera))
		return
	}
	caption := "📷 <b>" + esc(camera) + "</b> — " + b.Config.Language.T("live at ", "en direct à ") +
		b.Now().In(b.Config.Location).Format("15:04:05")
	if _, err := b.Telegram.SendPhoto(ctx, chat, telegram.InputFile{Name: camera + ".jpg", Data: img},
		telegram.SendOptions{Caption: caption, ReplyTo: replyTo}); err != nil {
		b.Log.Warn("sending the snapshot failed", "camera", camera, "err", err)
	}
}

func (b *Bot) handleCallback(ctx context.Context, q telegram.CallbackQuery) {
	if !b.Config.IsAdmin(q.From.ID) {
		b.Log.Warn("button refused: user not allowed", "user", q.From.ID)
		b.answer(ctx, q.ID, b.Config.Language.T("⛔ Not allowed", "⛔ Non autorisé"))
		return
	}
	l := b.Config.Language
	a, err := actions.Parse(q.Data)
	if err != nil {
		b.answer(ctx, q.ID, l.T("Unknown action", "Action inconnue"))
		return
	}
	var chat int64
	replyTo := 0
	if q.Message != nil {
		chat, replyTo = q.Message.Chat.ID, q.Message.MessageID
	}
	now := b.Now()
	if a.Menu {
		// Après l'action (plus bas), le menu est redessiné pour montrer le nouvel état.
		defer b.refreshMenu(ctx, q)
	}
	switch a.Kind {
	case actions.KindUnmute:
		if err := b.State.Unmute(a.Camera); err != nil {
			b.answer(ctx, q.ID, l.T("⚠️ Could not save", "⚠️ Erreur d'enregistrement"))
			return
		}
		b.answer(ctx, q.ID, "🔔 "+l.Tf("%s back on", "%s réactivée", a.Camera))
	case actions.KindResume:
		if err := b.State.Resume(); err != nil {
			b.answer(ctx, q.ID, l.T("⚠️ Could not save", "⚠️ Erreur d'enregistrement"))
			return
		}
		b.answer(ctx, q.ID, l.T("▶️ Notifications resumed", "▶️ Notifications réactivées"))
	case actions.KindRefresh:
		b.answer(ctx, q.ID, "")
	case actions.KindMute:
		until := now.Add(a.Duration)
		if err := b.State.Mute(a.Camera, until); err != nil {
			b.answer(ctx, q.ID, l.T("⚠️ Could not save", "⚠️ Erreur d'enregistrement"))
			return
		}
		b.answer(ctx, q.ID, "🔇 "+l.Tf("%s muted ", "%s coupée ", a.Camera)+b.untilText(until))
	case actions.KindPause:
		until := now.Add(a.Duration)
		if err := b.State.Pause(until); err != nil {
			b.answer(ctx, q.ID, l.T("⚠️ Could not save", "⚠️ Erreur d'enregistrement"))
			return
		}
		b.answer(ctx, q.ID, l.T("⏸ Paused ", "⏸ Pause ")+b.untilText(until))
	case actions.KindClip, actions.KindSnapshot:
		if chat == 0 {
			b.answer(ctx, q.ID, l.T("⚠️ Message too old", "⚠️ Message trop ancien"))
			return
		}
		if a.Kind == actions.KindClip {
			b.answer(ctx, q.ID, l.T("🎬 Sending the clip…", "🎬 Envoi du clip…"))
			b.async(func() {
				if err := b.Notifier.SendClipTo(ctx, chat, replyTo, a.ID); err != nil {
					b.reply(ctx, chat, l.T("⚠️ Clip unavailable: ", "⚠️ Clip indisponible : ")+esc(l.Message(err)))
				}
			})
			return
		}
		b.answer(ctx, q.ID, l.T("📷 Live image…", "📷 Image en direct…"))
		b.async(func() { b.sendLiveSnapshot(ctx, chat, replyTo, a.Camera) })
	}
}

// checkCamera vérifie que la caméra existe dans Frigate (accepte si Frigate est
// injoignable) ; renvoie le message à afficher sinon, vide si tout va bien.
func (b *Bot) checkCamera(ctx context.Context, camera string) string {
	cams, err := b.cameras(ctx)
	if err != nil || slices.Contains(cams, camera) {
		return ""
	}
	return b.Config.Language.Tf("Unknown camera: %s. Cameras: %s", "Caméra inconnue : %s. Caméras : %s",
		esc(camera), esc(strings.Join(cams, ", ")))
}

func (b *Bot) untilText(until time.Time) string {
	if !until.Before(state.Forever) {
		return b.Config.Language.T("until /resume", "jusqu'à /resume")
	}
	l := b.Config.Language
	return l.T("until ", "jusqu'à ") + until.In(b.Config.Location).Format(l.DateTimeShort())
}

func (b *Bot) reply(ctx context.Context, chat int64, text string) {
	if _, err := b.Telegram.SendMessage(ctx, chat, text, telegram.SendOptions{}); err != nil {
		b.Log.Warn("Telegram reply failed", "chat", chat, "err", err)
	}
}

func (b *Bot) answer(ctx context.Context, id, text string) {
	if err := b.Telegram.AnswerCallbackQuery(ctx, id, text); err != nil {
		b.Log.Warn("answerCallbackQuery failed", "err", err)
	}
}

// PresenceName tire un nom lisible d'un topic de présence :
// "homeassistant/person/alice/state" donne "alice".
func PresenceName(topic string) string {
	parts := strings.Split(topic, "/")
	if n := len(parts); n >= 2 && parts[n-1] == "state" {
		return parts[n-2]
	}
	return parts[len(parts)-1]
}
