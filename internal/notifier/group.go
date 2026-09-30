package notifier

import (
	"context"
	"errors"
	"html"
	"strings"
	"time"

	"frigate-telegram-enhanced/internal/telegram"
)

// maxGroupLines borne le nombre de détections détaillées dans un message regroupé ;
// au-delà, la légende indique seulement combien d'autres ont suivi.
const maxGroupLines = 8

// group rassemble, pour un destinataire, les détections arrivées peu après une
// notification (sa « tête ») : elles s'ajoutent à la légende de celle-ci au lieu
// d'envoyer de nouveaux messages. Modifier un message ne fait pas sonner le téléphone.
type group struct {
	lead    *tracked
	started time.Time
	lines   []string // « 14:33 🚗 Voiture — garage », déjà échappées
	count   int      // détections ajoutées, lignes non détaillées comprises
}

// split répartit les destinataires d'une nouvelle notification : ceux qui ont un
// regroupement en cours (grouped) et les autres (fresh). Appelé sous n.mu.
func (n *Notifier) split(t *tracked, chats []string, now time.Time) (fresh, grouped []string) {
	window := n.Config.Global().Group
	for _, chat := range chats {
		g := n.groups[chat]
		if window > 0 && g != nil && g.lead != t && now.Sub(g.started) < window {
			grouped = append(grouped, chat)
		} else {
			fresh = append(fresh, chat)
		}
	}
	return fresh, grouped
}

// startGroups fait de t la tête des regroupements de chats. Appelé sous n.mu.
func (n *Notifier) startGroups(t *tracked, chats []string, now time.Time) {
	if n.Config.Global().Group <= 0 {
		return
	}
	for _, chat := range chats {
		g := &group{lead: t, started: now}
		n.groups[chat] = g
		t.groups[chat] = g
	}
}

// addToGroups ajoute t aux regroupements en cours de chats et met à jour leurs
// messages. Appelé sous n.mu.
func (n *Notifier) addToGroups(ctx context.Context, t *tracked, chats []string) {
	l := n.Config.Language
	line := html.EscapeString(t.start.In(n.Config.Location).Format("15:04") + " " + labelText(t.label, l) + " — " + t.camera)
	if len(t.zones) > 0 {
		line += " · 📍 " + html.EscapeString(strings.Join(t.zones, ", "))
	}
	for _, chat := range chats {
		g := n.groups[chat]
		g.count++
		if len(g.lines) < maxGroupLines {
			g.lines = append(g.lines, line)
		}
		lead := g.lead
		n.goAsync(func() { n.editCaption(ctx, lead, chat) })
	}
}

// groupBlock est le bloc ajouté à la légende de la tête d'un regroupement.
func (n *Notifier) groupBlock(g *group) string {
	if g == nil || g.count == 0 {
		return ""
	}
	l := n.Config.Language
	var b strings.Builder
	b.WriteString("\n\n➕ " + l.Tf("%d more detection(s):", g.count))
	for _, line := range g.lines {
		b.WriteString("\n• " + line)
	}
	if more := g.count - len(g.lines); more > 0 {
		b.WriteString("\n" + l.Tf("… and %d more", more))
	}
	return b.String()
}

// captionFor est la légende de t telle que la voit chat, regroupement compris.
// Appelé sous n.mu.
func (n *Notifier) captionFor(t *tracked, chat string) string {
	d := n.captionData(t)
	d.Group = n.groupBlock(t.groups[chat])
	return buildCaption(d, n.Config.Location, n.Config.Language)
}

// editCaption remplace la légende du message de t chez chat par sa version à jour
// (description GenAI, détections regroupées). La légende est calculée au moment de
// l'envoi : deux mises à jour rapprochées envoient toutes deux la plus récente.
func (n *Notifier) editCaption(ctx context.Context, t *tracked, chat string) {
	select {
	case <-t.ready:
	case <-ctx.Done():
		return
	}
	m := t.message(chat)
	if m.id == 0 {
		return
	}
	n.mu.Lock()
	caption := n.captionFor(t, chat)
	n.mu.Unlock()
	chatID, markup := n.Config.ChatID(chat), buttons(t.camera, t.id, n.Config.Language)
	var err error
	if m.text {
		err = n.Telegram.EditMessageText(ctx, chatID, m.id, caption, markup)
	} else {
		err = n.Telegram.EditMessageCaption(ctx, chatID, m.id, caption, markup)
	}
	var ae *telegram.APIError
	if err != nil && !(errors.As(err, &ae) && strings.Contains(ae.Description, "message is not modified")) {
		n.Log.Warn("caption update failed", "chat", chat, "event_id", t.id, "err", err)
	}
}
