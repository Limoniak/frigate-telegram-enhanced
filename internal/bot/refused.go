package bot

import (
	"context"
	"slices"
	"strconv"
	"time"

	"frigate-telegram-enhanced/internal/telegram"
)

// maxRefused bounds the list of refused users kept for the web interface.
const maxRefused = 10

// idReplyInterval spaces out the "here is your ID" replies to the same user: the
// bot must not become chatty with an insistent stranger.
const idReplyInterval = 10 * time.Minute

// Refused describes a Telegram user whose command or button was refused. The web
// interface shows it, so that one's own ID is easy to find.
type Refused struct {
	ID       int64     `json:"id"`
	Name     string    `json:"name,omitempty"`
	Username string    `json:"username,omitempty"`
	At       time.Time `json:"at"`
}

// Refused returns the latest refused users, most recent first.
func (b *Bot) Refused() []Refused {
	b.refMu.Lock()
	defer b.refMu.Unlock()
	out := slices.Clone(b.refused)
	slices.Reverse(out)
	return out
}

// refuse records a refused user. It returns true if the bot should reply with
// their ID: no more than once per idReplyInterval.
func (b *Bot) refuse(u telegram.User) bool {
	b.refMu.Lock()
	defer b.refMu.Unlock()
	now := b.Now()
	b.refused = slices.DeleteFunc(b.refused, func(r Refused) bool { return r.ID == u.ID })
	b.refused = append(b.refused, Refused{ID: u.ID, Name: u.FirstName, Username: u.Username, At: now})
	if len(b.refused) > maxRefused {
		b.refused = slices.Delete(b.refused, 0, len(b.refused)-maxRefused)
	}
	if b.replied == nil {
		b.replied = map[int64]time.Time{}
	}
	for id, at := range b.replied {
		if now.Sub(at) >= idReplyInterval {
			delete(b.replied, id)
		}
	}
	if _, recent := b.replied[u.ID]; recent {
		return false
	}
	b.replied[u.ID] = now
	return true
}

// refuseCommand handles a command from a user who is not allowed: in private, the
// bot gives them their ID, which the bot's owner has to add to the configuration;
// in a group, it stays silent.
func (b *Bot) refuseCommand(ctx context.Context, m telegram.Message) {
	if m.From == nil {
		b.Log.Warn("command refused: unknown sender", "text", m.Text)
		return
	}
	b.Log.Warn("command refused: user not allowed", "user", m.From.ID, "text", m.Text)
	if !b.refuse(*m.From) || m.Chat.Type != "private" {
		return
	}
	id := strconv.FormatInt(m.From.ID, 10)
	l := b.Config.Language
	chat := m.Chat.ID
	text := l.Tf("⛔ You are not allowed to use this bot.\n\nYour Telegram ID is <code>%s</code>.\nIf this bot is yours, add this ID to TELEGRAM_CHAT_ID (and to TELEGRAM_ADMINS if you set it), then restart the container.", id)
	b.async(func() { b.reply(ctx, chat, text) })
}
