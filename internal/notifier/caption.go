package notifier

import (
	"html"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"frigate-telegram-enhanced/internal/actions"
	"frigate-telegram-enhanced/internal/i18n"
	"frigate-telegram-enhanced/internal/telegram"
)

const maxCaption = 1024

var labelNames = map[string]struct{ emoji, en, fr string }{
	"person":     {"🚶", "Person", "Personne"},
	"car":        {"🚗", "Car", "Voiture"},
	"dog":        {"🐕", "Dog", "Chien"},
	"cat":        {"🐈", "Cat", "Chat"},
	"bicycle":    {"🚲", "Bicycle", "Vélo"},
	"motorcycle": {"🏍️", "Motorcycle", "Moto"},
	"bird":       {"🐦", "Bird", "Oiseau"},
	"package":    {"📦", "Package", "Colis"},
}

func labelText(label string, lang i18n.Lang) string {
	if l, ok := labelNames[label]; ok {
		return l.emoji + " " + lang.T(l.en, l.fr)
	}
	if label == "" {
		return "🔔 " + lang.T("Detection", "Détection")
	}
	return "🔔 " + label
}

// openInFrigate est le lien « ouvrir dans Frigate » d'une légende.
func openInFrigate(link string, lang i18n.Lang) string {
	return "\n🔗 <a href=\"" + html.EscapeString(link) + "\">" + lang.T("Open in Frigate", "Ouvrir dans Frigate") + "</a>"
}

type captionData struct {
	Label, SubLabel, Camera string
	Zones                   []string
	Score                   float64
	HasScore                bool
	Start                   time.Time
	Description             string
	Link                    string
}

// buildCaption produit la légende HTML (≤ 1024 caractères ; la description est tronquée si besoin).
func buildCaption(d captionData, loc *time.Location, lang i18n.Lang) string {
	esc := html.EscapeString
	var b strings.Builder
	b.WriteString("<b>" + esc(labelText(d.Label, lang)) + "</b> — " + esc(d.Camera))
	if d.SubLabel != "" {
		b.WriteString(" (" + esc(d.SubLabel) + ")")
	}
	var details []string
	if len(d.Zones) > 0 {
		details = append(details, "📍 "+esc(strings.Join(d.Zones, ", ")))
	}
	if d.HasScore && d.Score > 0 {
		details = append(details, lang.Tf("%d%%", "%d %%", int(math.Round(d.Score*100))))
	}
	if len(details) > 0 {
		b.WriteString("\n" + strings.Join(details, " · "))
	}
	b.WriteString("\n🕑 " + d.Start.In(loc).Format(lang.DateTime()))

	footer := ""
	if d.Link != "" {
		footer = openInFrigate(d.Link, lang)
	}
	if d.Description != "" {
		// Telegram compte le texte hors balises : compter la chaîne HTML entière est prudent.
		room := maxCaption - utf8.RuneCountInString(b.String()) - utf8.RuneCountInString(footer) - len("\n\n<i></i>")
		if room > 20 {
			b.WriteString("\n\n<i>" + esc(truncate(d.Description, room)) + "</i>")
		}
	}
	b.WriteString(footer)
	return b.String()
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

// buttons renvoie les boutons d'une notification ; un bouton dont le callback_data dépasse 64 octets est omis.
func buttons(camera, id string) *telegram.InlineKeyboardMarkup {
	var row []telegram.InlineKeyboardButton
	add := func(text, data string) {
		if len(data) <= 64 {
			row = append(row, telegram.InlineKeyboardButton{Text: text, CallbackData: data})
		}
	}
	add("🔇 1 h", actions.Mute(camera, time.Hour))
	add("⏸ 30 min", actions.Pause(30*time.Minute))
	add("🎬 Clip", actions.Clip(id))
	return &telegram.InlineKeyboardMarkup{InlineKeyboard: [][]telegram.InlineKeyboardButton{row}}
}
