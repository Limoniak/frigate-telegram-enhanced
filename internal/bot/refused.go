package bot

import (
	"context"
	"slices"
	"strconv"
	"time"

	"frigate-telegram-enhanced/internal/telegram"
)

// maxRefused borne la liste des utilisateurs refusés gardée pour l'interface web.
const maxRefused = 10

// idReplyInterval espace les réponses « voici votre identifiant » à un même
// utilisateur : le bot ne doit pas devenir bavard avec un inconnu insistant.
const idReplyInterval = 10 * time.Minute

// Refused décrit un utilisateur Telegram dont une commande ou un bouton a été
// refusé. L'interface web le montre, pour qu'on retrouve facilement son identifiant.
type Refused struct {
	ID       int64     `json:"id"`
	Name     string    `json:"name,omitempty"`
	Username string    `json:"username,omitempty"`
	At       time.Time `json:"at"`
}

// Refused renvoie les derniers utilisateurs refusés, du plus récent au plus ancien.
func (b *Bot) Refused() []Refused {
	b.refMu.Lock()
	defer b.refMu.Unlock()
	out := slices.Clone(b.refused)
	slices.Reverse(out)
	return out
}

// refuse note un utilisateur refusé. Il renvoie true si le bot doit lui répondre
// avec son identifiant : pas plus d'une fois par idReplyInterval.
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

// refuseCommand traite une commande d'un utilisateur non autorisé : en privé, le
// bot lui donne son identifiant, que le propriétaire du bot doit ajouter à la
// configuration ; dans un groupe, il se tait.
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
	text := l.Tf("⛔ You are not allowed to use this bot.\n\nYour Telegram ID is <code>%s</code>.\nIf this bot is yours, add this ID to TELEGRAM_CHAT_ID (and to TELEGRAM_ADMINS if you set it), then restart the container.",
		"⛔ Vous n'êtes pas autorisé à utiliser ce bot.\n\nVotre identifiant Telegram est <code>%s</code>.\nSi ce bot est le vôtre, ajoutez cet identifiant à TELEGRAM_CHAT_ID (et à TELEGRAM_ADMINS si vous l'avez défini), puis redémarrez le conteneur.", id)
	b.async(func() { b.reply(ctx, chat, text) })
}
