//go:build screenshots

// Takes the screenshots of the README into docs/images/, with generic sample data.
// Run on demand: go test -tags screenshots -run Screenshots ./internal/web/
package web

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"frigate-telegram-enhanced/internal/frigate"
	"frigate-telegram-enhanced/internal/i18n"
	"frigate-telegram-enhanced/internal/notifier"
	"frigate-telegram-enhanced/internal/state"
)

const screenshotConfig = `
frigate:
  url: http://frigate:5000
mqtt:
  broker: tcp://mqtt:1883
telegram:
  token: abc
  admins: [42]
  chats:
    me: 42
notify:
  chats: [me]
  labels: [person, car]
`

// maxPNG is the size above which a screenshot is too heavy for the README.
const maxPNG = 400 << 10

func TestScreenshots(t *testing.T) {
	photo, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "assets", "sample.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	cams := fakeCameras{cams: []frigate.CameraInfo{
		{Name: "garage", Zones: []string{"parking"}, Labels: []string{"person", "car"}},
		{Name: "entree", Zones: []string{"porche"}, Labels: []string{"person", "car"}},
		{Name: "allee", Zones: []string{"chemin"}, Labels: []string{"person", "car"}},
	}}
	now := time.Now()
	ago := func(m int) time.Time { return now.Add(-time.Duration(m) * time.Minute) }
	media := fakeMedia{}
	entry := func(id string, m int, camera, label string, zones []string, score float64, sent bool, reason string) notifier.HistoryEntry {
		e := notifier.HistoryEntry{ID: id, At: ago(m), Camera: camera, Label: label, Zones: zones, Score: score, Sent: sent,
			Reason: reason}
		if label == "person" { // the others show the emoji of their label
			media["/api/events/"+id+"/thumbnail.jpg"] = photo
			e.Thumb = "/api/events/" + id + "/thumbnail.jpg"
		}
		return e
	}
	grouped := func(e notifier.HistoryEntry) notifier.HistoryEntry { e.Grouped = true; return e }
	history := fakeHistory{entries: []notifier.HistoryEntry{
		entry("e1", 4, "entree", "person", []string{"porche"}, 0.91, true, ""),
		entry("e2", 38, "garage", "car", []string{"parking"}, 0.84, true, ""),
		entry("e2b", 41, "garage", "car", []string{"parking"}, 0.8, true, ""),
		entry("e3", 95, "allee", "person", []string{"chemin"}, 0.52, false, "score"),
		entry("e4", 190, "garage", "person", nil, 0.77, false, "zone"),
		entry("e5", 410, "entree", "person", []string{"porche"}, 0.88, true, ""),
		grouped(entry("e6", 460, "garage", "car", []string{"parking"}, 0.8, true, "")),
		entry("e7", 520, "allee", "person", []string{"chemin"}, 0.35, false, "score"),
	}}
	var sentTimes fakeActivity
	for h, n := range []int{0, 0, 0, 0, 0, 1, 2, 4, 5, 2, 1, 1, 2, 1, 0, 1, 2, 3, 5, 4, 2, 1, 0, 1} {
		for i := range n {
			sentTimes = append(sentTimes, now.Add(-time.Duration(h)*time.Hour-time.Duration(5+i*9)*time.Minute))
		}
	}
	health := func(context.Context, i18n.Lang) []Component {
		return []Component{{Name: "Frigate", State: StateOK, Detail: "0.16"}, {Name: "MQTT", State: StateOK, Detail: "1883"},
			{Name: "Telegram", State: StateOK, Detail: "@frigate_sample_bot"}}
	}
	st, err := state.Load(filepath.Join(t.TempDir(), "state.json"), now)
	if err != nil {
		t.Fatal(err)
	}
	_, _, ts := setupConfig(t, screenshotConfig, "", cams, WithHistory(history, media), WithActivity(sentTimes),
		WithHealth(health), WithState(st))

	out := filepath.Join("..", "..", "docs", "images")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, jsErrors := browser(t)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	shot := func(name string, width, height int, hash, ready, theme string) chromedp.Action[chromedp.Void] {
		return func(ctx context.Context, tg *chromedp.Target) (chromedp.Void, error) {
			var png []byte
			err := chromedp.Do(ctx,
				chromedp.EmulateViewport(int64(width), int64(height)),
				chromedp.Navigate(ts.URL+"/"+hash),
				chromedp.WaitVisible(ready),
				chromedp.Poll[chromedp.Void](`document.fonts.status === "loaded" && [...document.images].every(i => i.complete && i.naturalWidth > 0)`),
				chromedp.Evaluate[chromedp.Void](`document.documentElement.dataset.theme = "`+theme+`"; 0`),
				chromedp.Sleep(800*time.Millisecond), // the transitions end
				into(chromedp.FullScreenshot(100), &png),
			)
			if err != nil {
				return chromedp.Void{}, err
			}
			if len(png) > maxPNG {
				t.Errorf("%s is %d KB, over %d KB: crop it", name, len(png)>>10, maxPNG>>10)
			}
			return chromedp.Void{}, os.WriteFile(filepath.Join(out, name), png, 0o644)
		}
	}
	for _, lang := range []string{"en", "fr"} {
		err := chromedp.Do(ctx,
			chromedp.Navigate(ts.URL+"/"),
			chromedp.Evaluate[chromedp.Void](`localStorage.setItem("frigate-telegram-enhanced-lang", "`+lang+`"); 0`),
			shot(lang+"-overview.png", 1280, 770, "", `#last-notification img`, "dark"),
			shot(lang+"-style.png", 1280, 790, "#style", `#live-preview img`, "dark"),
			shot(lang+"-phone.png", 390, 900, "#style", `#live-preview img`, "dark"),
			shot(lang+"-light.png", 1280, 790, "#style", `#live-preview img`, "light"),
			shot(lang+"-cameras.png", 900, 760, "#cameras/garage", `[data-camera="garage"][open]`, "dark"),
			shot(lang+"-activity.png", 1000, 900, "#activity", `#activity li`, "dark"),
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}
