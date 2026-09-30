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

// labelNames: emoji and name (English, translated by the catalog) of common objects.
var labelNames = map[string]struct{ emoji, name string }{
	"person":     {"🚶", "Person"},
	"car":        {"🚗", "Car"},
	"dog":        {"🐕", "Dog"},
	"cat":        {"🐈", "Cat"},
	"bicycle":    {"🚲", "Bicycle"},
	"motorcycle": {"🏍️", "Motorcycle"},
	"bird":       {"🐦", "Bird"},
	"package":    {"📦", "Package"},
}

func labelText(label string, lang i18n.Lang) string {
	if l, ok := labelNames[label]; ok {
		return l.emoji + " " + lang.T(l.name)
	}
	if label == "" {
		return "🔔 " + lang.T("Detection")
	}
	return "🔔 " + label
}

// openInFrigate is the "open in Frigate" link of a caption.
func openInFrigate(link string, lang i18n.Lang) string {
	return "\n🔗 <a href=\"" + html.EscapeString(link) + "\">" + lang.T("Open in Frigate") + "</a>"
}

type captionData struct {
	Label, SubLabel, Camera string
	Zones                   []string
	Score                   float64
	HasScore                bool
	Start                   time.Time
	Description             string
	Link                    string
	Group                   string // HTML block of the grouped detections, already escaped
}

// buildCaption builds the HTML caption (≤ 1024 characters; the description is truncated if needed).
func buildCaption(d captionData, loc *time.Location, lang i18n.Lang) string {
	esc := html.EscapeString
	var b strings.Builder
	b.WriteString("<b>" + esc(labelText(d.Label, lang)) + "</b> — " + esc(d.Camera))
	if d.SubLabel != "" {
		// Frigate's label: classification ("clio 3 océane"), face, plate.
		b.WriteString("\n🏷 " + esc(d.SubLabel))
	}
	var details []string
	if len(d.Zones) > 0 {
		details = append(details, "📍 "+esc(strings.Join(d.Zones, ", ")))
	}
	if d.HasScore && d.Score > 0 {
		details = append(details, lang.Tf("%d%%", int(math.Round(d.Score*100))))
	}
	if len(details) > 0 {
		b.WriteString("\n" + strings.Join(details, " · "))
	}
	b.WriteString("\n🕑 " + d.Start.In(loc).Format(lang.DateTime()))

	footer := d.Group
	if d.Link != "" {
		footer += openInFrigate(d.Link, lang)
	}
	if d.Description != "" {
		// Telegram counts the text outside tags: counting the whole HTML string is on the safe side.
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

// buttons returns the buttons of a notification, on two rows: look (live image,
// clip) then silence (camera 1 h, everything 30 min). A button whose callback_data
// exceeds 64 bytes is left out.
func buttons(camera, id string, lang i18n.Lang) *telegram.InlineKeyboardMarkup {
	var rows [][]telegram.InlineKeyboardButton
	row := func(keys ...[2]string) {
		var r []telegram.InlineKeyboardButton
		for _, k := range keys {
			if len(k[1]) <= 64 {
				r = append(r, telegram.InlineKeyboardButton{Text: k[0], CallbackData: k[1]})
			}
		}
		if len(r) > 0 {
			rows = append(rows, r)
		}
	}
	row([2]string{lang.T("📷 Now"), actions.Snapshot(camera)}, [2]string{"🎬 Clip", actions.Clip(id)})
	row([2]string{"🔇 1 h", actions.Mute(camera, time.Hour)}, [2]string{"⏸ 30 min", actions.Pause(30 * time.Minute)})
	return &telegram.InlineKeyboardMarkup{InlineKeyboard: rows}
}
