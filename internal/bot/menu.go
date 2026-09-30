package bot

import (
	"context"
	"slices"
	"strings"
	"time"

	"frigate-telegram-enhanced/internal/actions"
	"frigate-telegram-enhanced/internal/telegram"
)

// menuMute est la durée de coupure d'une caméra depuis le menu.
const menuMute = time.Hour

// menu construit le message de contrôle : l'état du service et des boutons pour
// mettre en pause, reprendre, et couper ou réactiver chaque caméra.
func (b *Bot) menu(ctx context.Context) (string, *telegram.InlineKeyboardMarkup) {
	l := b.Config.Language
	now := b.Now()
	st := b.State.Status(now)

	var sb strings.Builder
	sb.WriteString(l.T("🎛 <b>Control</b>\n"))
	if st.PausedUntil.IsZero() {
		sb.WriteString(l.T("▶️ Notifications active"))
	} else {
		sb.WriteString(l.T("⏸ Everything paused ") + b.untilText(st.PausedUntil))
	}
	if b.Config.Presence.Enabled() {
		if len(st.Home) > 0 {
			names := make([]string, len(st.Home))
			for i, t := range st.Home {
				names[i] = esc(PresenceName(t))
			}
			sb.WriteString(l.T("\n🏠 At home: ") + strings.Join(names, ", "))
		} else {
			sb.WriteString(l.T("\n🚪 Nobody at home"))
		}
	}

	btn := func(text, data string) telegram.InlineKeyboardButton {
		return telegram.InlineKeyboardButton{Text: text, CallbackData: actions.InMenu(data)}
	}
	var rows [][]telegram.InlineKeyboardButton
	if st.PausedUntil.IsZero() {
		rows = append(rows, []telegram.InlineKeyboardButton{
			btn("⏸ 30 min", actions.Pause(30*time.Minute)),
			btn("⏸ 1 h", actions.Pause(time.Hour)),
			btn("⏸ 8 h", actions.Pause(8*time.Hour)),
		})
	} else {
		rows = append(rows, []telegram.InlineKeyboardButton{btn(l.T("▶️ Resume"), actions.Resume())})
	}

	cams, err := b.cameras(ctx)
	if err != nil {
		// Frigate injoignable : au moins les caméras connues de la configuration et de l'état.
		cams = b.Config.CameraNames()
		for cam := range st.Mutes {
			if !slices.Contains(cams, cam) {
				cams = append(cams, cam)
			}
		}
		slices.Sort(cams)
	}
	if len(cams) > 0 {
		sb.WriteString(l.T("\n\nTap a camera to mute it for 1 h, or to turn it back on."))
	}
	var row []telegram.InlineKeyboardButton
	for _, cam := range cams {
		if until, muted := st.Mutes[cam]; muted {
			sb.WriteString("\n🔇 " + esc(cam) + " " + b.untilText(until))
			row = append(row, btn("🔇 "+cam, actions.Unmute(cam)))
		} else {
			row = append(row, btn("✅ "+cam, actions.Mute(cam, menuMute)))
		}
		if len(row) == 2 {
			rows, row = append(rows, row), nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	rows = append(rows, []telegram.InlineKeyboardButton{btn(l.T("🔄 Refresh"), actions.Refresh())})
	// Un bouton dont callback_data dépasse la limite de Telegram serait refusé en bloc.
	for i := range rows {
		rows[i] = slices.DeleteFunc(rows[i], func(k telegram.InlineKeyboardButton) bool { return len(k.CallbackData) > 64 })
	}
	return sb.String(), &telegram.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func (b *Bot) cmdMenu(ctx context.Context, chat int64) {
	text, markup := b.menu(ctx)
	if _, err := b.Telegram.SendMessage(ctx, chat, text, telegram.SendOptions{Markup: markup}); err != nil {
		b.Log.Warn("sending the menu failed", "err", err)
	}
}

// refreshMenu redessine le message du menu après une action.
func (b *Bot) refreshMenu(ctx context.Context, q telegram.CallbackQuery) {
	if q.Message == nil {
		return
	}
	text, markup := b.menu(ctx)
	if err := b.Telegram.EditMessageText(ctx, q.Message.Chat.ID, q.Message.MessageID, text, markup); err != nil &&
		!strings.Contains(err.Error(), "message is not modified") {
		b.Log.Warn("refreshing the menu failed", "err", err)
	}
}
