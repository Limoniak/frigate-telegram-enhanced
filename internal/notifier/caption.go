package notifier

import (
	"html"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"frigate-telegram-enhanced/internal/actions"
	"frigate-telegram-enhanced/internal/config"
	"frigate-telegram-enhanced/internal/i18n"
	"frigate-telegram-enhanced/internal/labels"
	"frigate-telegram-enhanced/internal/telegram"
)

const maxRichMessage = 32768

// openInFrigate is the "open in Frigate" link of a caption.
func openInFrigate(link string, lang i18n.Lang) string {
	return "\n🔗 <a href=\"" + html.EscapeString(link) + "\">" + lang.T("Open in Frigate") + "</a>"
}

type captionData struct {
	Label, SubLabel, Camera string
	Emoji                   string // the camera's, before its name; empty: none
	Sound                   bool   // Label is a sound of the audio detection
	Zones                   []string
	Score                   float64
	HasScore                bool
	Start                   time.Time
	Description             string
	Link                    string
	Group                   string // HTML block of the grouped detections, already escaped
	ClipNote                string // HTML line about the clip, already escaped
	// The camera's choices of what to show; Link is left empty when the link is not.
	NoZones, NoScore, NoDate bool
}

// dress applies to d the caption choices of its camera.
func dress(d captionData, cfg config.Notify) captionData {
	d.Emoji = cfg.Emoji
	d.NoZones, d.NoScore, d.NoDate = !cfg.ShowZones, !cfg.ShowScore, !cfg.ShowDate
	if !cfg.ShowLink {
		d.Link = ""
	}
	return d
}

// cameraName is the camera as a caption names it, after its emoji if it has one.
func cameraName(camera, emoji string) string {
	if emoji == "" {
		return camera
	}
	return emoji + " " + camera
}

// buildCaption builds the HTML caption for a rich message (≤ 32768 characters).
func buildCaption(d captionData, loc *time.Location, lang i18n.Lang) string {
	esc := html.EscapeString
	var b strings.Builder
	b.WriteString("<b>" + esc(labels.Text(d.Label, d.Sound, lang)) + "</b> — " + esc(cameraName(d.Camera, d.Emoji)))
	if d.SubLabel != "" {
		// Frigate's label: classification ("clio 3 océane"), face, plate.
		b.WriteString("\n🏷 " + esc(d.SubLabel))
	}
	var details []string
	if len(d.Zones) > 0 && !d.NoZones {
		details = append(details, "📍 "+esc(strings.Join(d.Zones, ", ")))
	}
	if d.HasScore && d.Score > 0 && !d.NoScore {
		details = append(details, "\U0001F3AF "+lang.Tf("%d%%", int(math.Round(d.Score*100))))
	}
	if len(details) > 0 {
		b.WriteString("\n" + strings.Join(details, "\n"))
	}
	layout := lang.DateTime()
	if d.NoDate {
		layout = "15:04:05"
	}
	b.WriteString("\n\U0001F551 " + esc(d.Start.In(loc).Format(layout)))

	footer := d.Group
	if d.Link != "" {
		footer += openInFrigate(d.Link, lang)
	}
	if d.ClipNote != "" {
		footer += "\n" + d.ClipNote
	}
	if d.Description != "" {
		// Telegram counts the text outside tags: counting the whole HTML string is on the safe side.
		room := maxRichMessage - utf8.RuneCountInString(b.String()) - utf8.RuneCountInString(footer) - len("\n\n<i></i>")
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
// clip) then silence (camera 1 h, everything 30 min), plus a third one opening
// Home Assistant when haURL is set. A button whose callback_data exceeds 64 bytes
// is left out.
func buttons(camera, id, haURL string, lang i18n.Lang) *telegram.InlineKeyboardMarkup {
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
	first := [][2]string{{lang.T("📷 Now"), actions.Snapshot(camera)}}
	if id != "" {
		first = append(first, [2]string{lang.T("🎬 Clip"), actions.Clip(id)})
	}
	row(first...)
	row([2]string{"🔇 1 h", actions.Mute(camera, time.Hour)}, [2]string{"⏸ 30 min", actions.Pause(30 * time.Minute)})
	if haURL != "" {
		rows = append(rows, []telegram.InlineKeyboardButton{{Text: "🏠 Home Assistant", URL: haURL}})
	}
	return &telegram.InlineKeyboardMarkup{InlineKeyboard: rows}
}
