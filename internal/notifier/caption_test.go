package notifier

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"frigate-telegram-enhanced/internal/i18n"
)

func paris(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestBuildCaption(t *testing.T) {
	got := buildCaption(captionData{
		Label: "person", SubLabel: "Alice", Camera: "jardin",
		Zones: []string{"allee", "portail"}, Score: 0.87, HasScore: true,
		Start: time.Date(2026, 9, 28, 14, 32, 5, 0, time.UTC),
		Link:  "https://nvr.example/explore?event_id=abc",
	}, paris(t), i18n.FR)
	want := "<b>🚶 Personne</b> — jardin\n🏷 Alice\n" +
		"📍 allee, portail · 87 %\n" +
		"🕑 28/09 16:32:05\n" +
		"🔗 <a href=\"https://nvr.example/explore?event_id=abc\">Ouvrir dans Frigate</a>"
	if got != want {
		t.Errorf("caption:\n%s\nwant:\n%s", got, want)
	}
}

func TestBuildCaptionEnglish(t *testing.T) {
	got := buildCaption(captionData{
		Label: "car", Camera: "garage", Score: 0.9, HasScore: true,
		Start: time.Date(2026, 9, 28, 14, 32, 5, 0, time.UTC),
		Link:  "https://nvr.example/x",
	}, time.UTC, i18n.EN)
	want := "<b>🚗 Car</b> — garage\n" +
		"90%\n" +
		"🕑 Sep 28 14:32:05\n" +
		"🔗 <a href=\"https://nvr.example/x\">Open in Frigate</a>"
	if got != want {
		t.Errorf("caption:\n%s\nwant:\n%s", got, want)
	}
}

func TestBuildCaptionEscapesAndUnknownLabel(t *testing.T) {
	got := buildCaption(captionData{Label: "raccoon", Camera: "<cam>", Start: time.Unix(0, 0)}, time.UTC, i18n.FR)
	if !strings.Contains(got, "🔔 raccoon") || !strings.Contains(got, "&lt;cam&gt;") {
		t.Errorf("caption = %q", got)
	}
	if strings.Contains(got, "📍") || strings.Contains(got, "🔗") {
		t.Errorf("unexpected empty lines: %q", got)
	}
}

func TestBuildCaptionTruncatesDescription(t *testing.T) {
	got := buildCaption(captionData{Label: "person", Camera: "jardin", Start: time.Unix(0, 0),
		Description: strings.Repeat("é", 2000), Link: "https://nvr.example/x"}, time.UTC, i18n.FR)
	if n := utf8.RuneCountInString(got); n > 1024 {
		t.Errorf("caption of %d characters (max 1024)", n)
	}
	if !strings.Contains(got, "…") || !strings.HasSuffix(got, "Ouvrir dans Frigate</a>") {
		t.Errorf("wrong truncation: %q", got[len(got)-80:])
	}
}

func TestButtons(t *testing.T) {
	kb := buttons("jardin", "abc", i18n.EN).InlineKeyboard
	if len(kb) != 2 {
		t.Fatalf("want two rows: %+v", kb)
	}
	see, quiet := kb[0], kb[1]
	if len(see) != 2 || see[0].Text != "📷 Now" || see[0].CallbackData != "s:jardin" || see[1].CallbackData != "c:abc" {
		t.Errorf("look row = %+v", see)
	}
	if len(quiet) != 2 || quiet[0].CallbackData != "m:jardin:3600" || quiet[1].CallbackData != "p:1800" {
		t.Errorf("silence row = %+v", quiet)
	}
	if fr := buttons("jardin", "abc", i18n.FR).InlineKeyboard[0][0].Text; fr != "📷 Maintenant" {
		t.Errorf("French wording = %q", fr)
	}
	long := strings.Repeat("x", 70)
	if row := buttons("jardin", long, i18n.EN).InlineKeyboard[0]; len(row) != 1 || row[0].Text != "📷 Now" {
		t.Errorf("a callback_data > 64 bytes must be left out: %+v", row)
	}
}
