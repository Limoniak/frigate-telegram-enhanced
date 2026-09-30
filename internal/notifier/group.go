package notifier

import (
	"context"
	"errors"
	"html"
	"strings"
	"time"

	"frigate-telegram-enhanced/internal/telegram"
)

// maxGroupLines bounds the number of detections detailed in a grouped message;
// beyond it, the caption only says how many others followed.
const maxGroupLines = 8

// group gathers, for a recipient, the detections that arrived shortly after a
// notification (its "head"): they are added to its caption instead of sending new
// messages. Editing a message does not make the phone ring.
type group struct {
	lead    *tracked
	started time.Time
	lines   []string // "14:33 🚗 Car — garage", already escaped
	count   int      // detections added, lines not detailed included
}

// split sorts the recipients of a new notification: those with a grouping in
// progress (grouped) and the others (fresh). Called under n.mu.
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

// startGroups makes t the head of the groupings of chats. Called under n.mu.
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

// addToGroups adds t to the groupings in progress of chats and updates their
// messages. Called under n.mu.
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

// groupBlock is the block added to the caption of a grouping's head.
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

// captionFor is the caption of t as chat sees it, grouping included.
// Called under n.mu.
func (n *Notifier) captionFor(t *tracked, chat string) string {
	d := n.captionData(t)
	d.Group = n.groupBlock(t.groups[chat])
	return buildCaption(d, n.Config.Location, n.Config.Language)
}

// editCaption replaces the caption of t's message in chat with its current version
// (GenAI description, grouped detections). The caption is computed when sending:
// two updates close together both send the latest one.
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
