//go:build browser

// Smoke test of the interface in a real browser (Chrome, driven by chromedp): the
// page loads without JavaScript errors, shows cameras, status and activity, and
// saves a setting by itself. Run separately: go test -tags browser ./internal/web/
package web

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
	"gopkg.in/yaml.v3"

	"frigate-telegram-enhanced/internal/config"
	"frigate-telegram-enhanced/internal/frigate"
	"frigate-telegram-enhanced/internal/i18n"
	"frigate-telegram-enhanced/internal/notifier"
	"frigate-telegram-enhanced/internal/state"
)

func TestBrowserSmoke(t *testing.T) {
	cams := fakeCameras{cams: []frigate.CameraInfo{
		{Name: "garage", Zones: []string{"allee"}, Labels: []string{"person", "car"}},
		{Name: "salon", Labels: []string{"person"}},
	}}
	history := fakeHistory{entries: []notifier.HistoryEntry{
		{ID: "e1", At: time.Now(), Camera: "garage", Label: "person", Sent: true},
		{ID: "e2", At: time.Now(), Camera: "salon", Label: "car", Reason: "label"},
	}}
	health := func(context.Context, i18n.Lang) []Component {
		return []Component{{Name: "Frigate", State: StateOK, Detail: "0.16"}, {Name: "MQTT", State: StateError, Detail: "refused", Hint: "check"}}
	}
	_, path, ts := setup(t, "", cams, WithHistory(history, nil), WithHealth(health),
		WithRefused(fakeRefusals{{ID: 999, Name: "Alice", At: time.Now()}}))

	ctx, jsErrors := browser(t)

	var cameras, activity, health2, title string
	err := chromedp.Do(ctx,
		chromedp.Navigate(ts.URL+"/classic"),
		chromedp.WaitVisible(`#cameras .cam-name`),
		into(chromedp.Text(`#cameras`), &cameras),
		chromedp.WaitVisible(`#activity li, #activity > *`),
		into(chromedp.Text(`#activity`), &activity),
		chromedp.WaitVisible(`#health-errors`),
		into(chromedp.Text(`#health-errors`), &health2),
		// Muting a camera is saved by itself, shortly after.
		chromedp.Click(`details.camera[data-name="salon"] label.toggle`),
		chromedp.WaitVisible(`#status.saved`), // saved by itself, no button
		// The language menu comes from the catalogs; in French, the page is translated.
		chromedp.Evaluate[chromedp.Void](`const m = document.querySelector(".lang select"); m.value = "fr"; m.dispatchEvent(new Event("change")); 0`),
		into(chromedp.Text(`#sec-when h2`), &title),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"garage", "salon"} {
		if !strings.Contains(cameras, want) {
			t.Errorf("cameras shown = %q, %s missing", cameras, want)
		}
	}
	if activity == "" {
		t.Error("recent activity empty")
	}
	for _, want := range []string{"MQTT", "999"} {
		if !strings.Contains(health2, want) {
			t.Errorf("status shown = %q, %s missing", health2, want)
		}
	}
	if title != "Quand être prévenu ?" {
		t.Errorf("title in French = %q, want Quand être prévenu ?", title)
	}
	saved, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(saved), "salon") {
		t.Errorf("override saved = %q, %v", saved, err)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// The overview: the day's count and health, the last notification sent, a camera
// turned off (saved by itself), and the pause.
func TestBrowserOverview(t *testing.T) {
	cams := fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}, {Name: "entree"}}}
	now := time.Now()
	history := fakeHistory{entries: []notifier.HistoryEntry{
		{ID: "e1", At: now, Camera: "garage", Label: "person", Sent: true},
		{ID: "e2", At: now, Camera: "entree", Label: "car", Reason: "label"},
	}}
	health := func(context.Context, i18n.Lang) []Component {
		return []Component{{Name: "Frigate", State: StateOK, Detail: "0.16"}, {Name: "MQTT", State: StateOK, Detail: "1883"}}
	}
	st, err := state.Load(filepath.Join(t.TempDir(), "state.json"), now)
	if err != nil {
		t.Fatal(err)
	}
	_, path, ts := setup(t, "", cams, WithHistory(history, nil), WithActivity(fakeActivity{now, now, now}),
		WithHealth(health), WithState(st))

	ctx, jsErrors := browser(t)
	var sent, today, last, lastCamera, pause string
	err = chromedp.Do(ctx,
		inEnglish(ts.URL+"/"),
		chromedp.WaitVisible(`#cameras-list`),
		chromedp.WaitVisible(`#connections`), // the health arrives last
		chromedp.WaitVisible(`#sent-24h`),
		into(chromedp.Text(`#sent-24h`), &sent),
		into(chromedp.Text(`#today`), &today),
		chromedp.WaitVisible(`#last-notification`),
		into(chromedp.Text(`#last-notification`), &last),
		into(chromedp.Evaluate[string](`document.querySelector("#last-notification").textContent.includes("entree") ? "entree" : "garage"`), &lastCamera),
		// Turning a camera off is saved by itself.
		chromedp.Click(`#cameras-list [role=switch][aria-label="entree"]`),
		chromedp.WaitVisible(`#save-status.ok`),
		chromedp.Click(`#pause-bar button`), // "30 min"
		chromedp.WaitVisible(`#pause-bar .btn-primary`),
		into(chromedp.Text(`#pause-bar`), &pause),
	)
	if err != nil {
		t.Fatal(err)
	}
	if sent != "3" {
		t.Errorf("sent in 24 h = %q, want 3", sent)
	}
	if !strings.Contains(today, "Everything works") {
		t.Errorf("today = %q, want Everything works", today)
	}
	if !strings.Contains(last, "garage") || lastCamera != "garage" {
		t.Errorf("last notification = %q, want the one sent from garage", last)
	}
	if saved, err := os.ReadFile(path); err != nil || !strings.Contains(string(saved), "entree") || !strings.Contains(string(saved), "enabled: false") {
		t.Errorf("override saved = %q, %v", saved, err)
	}
	if !strings.Contains(pause, "Notifications paused") {
		t.Errorf("pause bar = %q, want Notifications paused", pause)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// Without history, state or health, the overview shows the cameras and nothing else.
func TestBrowserOverviewMinimal(t *testing.T) {
	_, _, ts := setup(t, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}}})
	ctx, jsErrors := browser(t)
	var absent bool
	err := chromedp.Do(ctx,
		chromedp.Navigate(ts.URL+"/"),
		chromedp.WaitVisible(`#cameras-list`),
		chromedp.WaitVisible(`#today`),
		into(chromedp.Evaluate[bool](`!document.querySelector("#sent-24h") && !document.querySelector("#pause-bar") && !document.querySelector("#last-notification")`), &absent),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !absent {
		t.Error("the counter, the pause bar or the last notification is shown without data")
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// No notification yet: the count is 0 and the last notification says so.
func TestBrowserOverviewNothingSent(t *testing.T) {
	_, _, ts := setup(t, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}}},
		WithHistory(fakeHistory{}, nil), WithActivity(fakeActivity{}))
	ctx, jsErrors := browser(t)
	var sent, last string
	err := chromedp.Do(ctx,
		inEnglish(ts.URL+"/"),
		chromedp.WaitVisible(`#sent-24h`),
		into(chromedp.Text(`#sent-24h`), &sent),
		chromedp.WaitVisible(`#last-notification`),
		into(chromedp.Text(`#last-notification`), &last),
	)
	if err != nil {
		t.Fatal(err)
	}
	if sent != "0" || !strings.Contains(last, "No notification sent yet.") {
		t.Errorf("sent = %q, last notification = %q", sent, last)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// The language follows the browser until one is chosen in the menu (kept after a
// reload), and again once "Auto" is chosen back.
func TestBrowserLanguageAuto(t *testing.T) {
	_, _, ts := setup(t, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}}})
	ctx, jsErrors := browser(t)
	choose := func(code string) chromedp.Action[chromedp.Void] {
		return chromedp.Evaluate[chromedp.Void](`const m = document.querySelector(".lang select"); m.value = "` + code + `"; m.dispatchEvent(new Event("change")); 0`)
	}
	// menu value, page language, saved choice
	const state = `[document.querySelector(".lang select").value, document.documentElement.lang, localStorage.getItem("frigate-telegram-enhanced-lang") || "-"].join(" ")`
	var first, forced, back string
	err := chromedp.Do(ctx,
		chromedp.Navigate(ts.URL+"/classic"),
		chromedp.WaitVisible(`#cameras .cam-name`),
		into(chromedp.Evaluate[string](state), &first),
		choose("de"),
		chromedp.Reload(),
		chromedp.WaitVisible(`#cameras .cam-name`),
		into(chromedp.Evaluate[string](state), &forced),
		choose("auto"),
		into(chromedp.Evaluate[string](state), &back),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first, "auto ") || !strings.HasSuffix(first, " -") {
		t.Errorf("on arrival: %q, want auto and nothing saved", first)
	}
	if forced != "de de de" {
		t.Errorf("after choosing German and reloading: %q, want \"de de de\"", forced)
	}
	if back != first {
		t.Errorf("back to Auto: %q, want %q as on arrival", back, first)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// browser starts Chrome for the test; jsErrors returns the uncaught JavaScript errors so far.
func browser(t *testing.T) (ctx context.Context, jsErrors func() []string) {
	t.Helper()
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.WindowSize(1280, 900),
		// A CI runner can take a while to start Chrome.
		chromedp.WSURLReadTimeout(60*time.Second))
	// CHROME_PATH picks the browser: without it, chromedp takes the first one it
	// finds, which can be a slow snap wrapper (chromium-browser on Ubuntu).
	if p := os.Getenv("CHROME_PATH"); p != "" {
		opts = append(opts, chromedp.ExecPath(p))
	}
	actx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	t.Cleanup(cancel)
	ctx, cancel = chromedp.NewContext(actx)
	t.Cleanup(cancel)
	ctx, cancel = context.WithTimeout(ctx, 90*time.Second)
	t.Cleanup(cancel)

	var mu sync.Mutex
	var errs []string
	messages := chromedp.Console(ctx) // subscribes now, before the page loads
	go func() {
		for m, err := range messages {
			if err != nil {
				return
			}
			if m.IsException() {
				mu.Lock()
				errs = append(errs, m.Text)
				mu.Unlock()
			}
		}
	}()
	return ctx, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(errs)
	}
}

// inEnglish opens url with the page in English, whatever the language of the browser:
// the menu's choice is kept in the page's storage.
func inEnglish(url string) chromedp.Action[chromedp.Void] {
	return chromedp.Steps(
		chromedp.Navigate(url),
		chromedp.Evaluate[chromedp.Void](`localStorage.setItem("frigate-telegram-enhanced-lang", "en"); 0`),
		chromedp.Reload(),
	)
}

// into runs a and stores its result in dst, so that it can be a step of chromedp.Do.
func into[T any](a chromedp.Action[T], dst *T) chromedp.Action[chromedp.Void] {
	return func(ctx context.Context, t *chromedp.Target) (chromedp.Void, error) {
		v, err := a(ctx, t)
		*dst = v
		return chromedp.Void{}, err
	}
}

// The setup page, on its own: the bot is found, the chat that wrote to it offered,
// Frigate's broker filled in, and the connection saved.
func TestBrowserSetup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "connection.yml")
	saved := make(chan struct{})
	s := NewSetup(path, nil, &fakeProber{}, slog.New(slog.DiscardHandler), func() { close(saved) })
	mux := http.NewServeMux()
	s.MountAlone(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, jsErrors := browser(t)
	var chats, broker string
	err := chromedp.Do(ctx,
		chromedp.Navigate(ts.URL+"/"),
		chromedp.WaitVisible(`#intro-first`),
		chromedp.SendKeys(`#token`, "123:abc"),
		chromedp.Click(`#check-token`),
		chromedp.WaitVisible(`#chats .chip`),
		// An ID by hand: refused with a message if it is not a number, added on Enter.
		chromedp.Click(`#manual summary`),
		chromedp.SendKeys(`#manual-id`, "abc"+kb.Enter),
		chromedp.WaitVisible(`#manual-result.err`),
		chromedp.Evaluate[chromedp.Void](`document.getElementById("manual-id").value = ""`),
		chromedp.SendKeys(`#manual-id`, "-100 123"),
		chromedp.SendKeys(`#manual-name`, "Family"+kb.Enter),
		chromedp.WaitVisible(`#manual-result.ok`),
		into(chromedp.Text(`#chats`), &chats),
		chromedp.SendKeys(`#frigate-url`, "http://192.168.1.10:5000"),
		chromedp.Click(`#check-frigate`),
		chromedp.WaitVisible(`#frigate-result.ok`),
		into(chromedp.Value(`#mqtt-broker`), &broker),
		chromedp.Click(`#check-mqtt`),
		chromedp.WaitVisible(`#mqtt-result.ok`),
		chromedp.Click(`#save`),
		chromedp.WaitVisible(`#save-result.ok`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(chats, "Alice") || !strings.Contains(chats, "Family") {
		t.Errorf("chats offered = %q", chats)
	}
	if broker != "192.168.1.10:1883" {
		t.Errorf("broker filled in = %q", broker)
	}
	select {
	case <-saved:
	case <-time.After(5 * time.Second):
		t.Fatal("the connection was not saved")
	}
	c, err := config.LoadConnection(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Telegram.Chats["Alice"] != 111 || c.Telegram.Chats["Family"] != -100123 || c.Timezone == "" || c.Frigate.URL != "http://192.168.1.10:5000" {
		t.Errorf("saved %+v", c)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// For a beginner: Frigate is found on opening, its broker filled in, and only its
// password is asked for.
func TestBrowserSetupFindsFrigate(t *testing.T) {
	s := NewSetup(filepath.Join(t.TempDir(), "connection.yml"), nil, &fakeProber{discover: true, brokerState: BrokerPassword},
		slog.New(slog.DiscardHandler), func() {})
	mux := http.NewServeMux()
	s.MountAlone(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, jsErrors := browser(t)
	var url, broker, status string
	err := chromedp.Do(ctx,
		chromedp.Navigate(ts.URL+"/"),
		chromedp.WaitVisible(`#frigate-result.ok`),
		into(chromedp.Value(`#frigate-url`), &url),
		into(chromedp.Value(`#mqtt-broker`), &broker),
		chromedp.WaitVisible(`#mqtt-pass`),
		into(chromedp.Text(`#broker-status`), &status),
	)
	if err != nil {
		t.Fatal(err)
	}
	if url != "http://192.168.1.10:5000" || broker != "192.168.1.10:1883" || !strings.Contains(status, "192.168.1.10:1883") {
		t.Errorf("found url %q, broker %q, status %q", url, broker, status)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// For a beginner: the fine settings are folded, and a sample asked for right
// after a change goes out with that change saved.
func TestBrowserSampleSavesFirst(t *testing.T) {
	tester := &fakeTester{}
	_, path, ts := setup(t, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage", Labels: []string{"person"}}}},
		WithTester(tester))
	ctx, jsErrors := browser(t)
	var folded bool
	err := chromedp.Do(ctx,
		chromedp.Navigate(ts.URL+"/classic"),
		chromedp.WaitVisible(`#messages .notice.info`), // first visit: welcome
		into(chromedp.Evaluate[bool](`!document.querySelector("#questions details.more").open`), &folded),
		chromedp.Click(`.style-card:nth-child(2)`), // "Photo only", then the sample at once
		chromedp.Click(`#try button`),
		chromedp.WaitVisible(`#try .result.ok`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !folded {
		t.Error("the More choices box is open on a first visit")
	}
	if saved, err := os.ReadFile(path); err != nil || !strings.Contains(string(saved), "clip: false") {
		t.Errorf("the sample went out before the change was saved: %q, %v", saved, err)
	}
	if len(tester.cameras) != 1 {
		t.Errorf("samples sent: %v", tester.cameras)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// The Style screen: the chosen style and the options show on the phone at once, the
// cameras that set their own are named, and the sample leaves with the saved choice.
func TestBrowserStyle(t *testing.T) {
	tester := &fakeTester{}
	_, path, ts := setup(t, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}, {Name: "entree"}}},
		WithTester(tester))
	if resp := do(t, ts, "PUT", "/api/settings", `{"cameras":{"entree":{"clip":false}}}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/settings: %d", resp.StatusCode)
	}
	ctx, jsErrors := browser(t)
	var overrides, preview string
	err := chromedp.Do(ctx,
		inEnglish(ts.URL+"/#style"),
		chromedp.WaitVisible(`#styles [data-style="clip"][aria-pressed="true"]`),
		into(chromedp.Text(`#style-overrides`), &overrides),
		chromedp.Click(`[data-style="gif"]`),
		chromedp.WaitVisible(`#styles [data-style="gif"][aria-pressed="true"]`),
		chromedp.Poll[chromedp.Void](`document.querySelector("#live-preview").textContent.includes("GIF")`),
		chromedp.Click(`#style-in-place`),
		chromedp.Poll[chromedp.Void](`document.querySelector("#live-preview").textContent.includes("As a reply")`),
		into(chromedp.Text(`#live-preview`), &preview),
		chromedp.Click(`#try button`), // the pending change is saved first
		chromedp.WaitVisible(`#try-result.ok`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(overrides, "entree") {
		t.Errorf("overrides = %q, want entree", overrides)
	}
	if !strings.Contains(preview, "GIF") {
		t.Errorf("preview = %q, want GIF", preview)
	}
	if saved, err := os.ReadFile(path); err != nil || !strings.Contains(string(saved), "gif: true") || !strings.Contains(string(saved), "media_in_place: false") {
		t.Errorf("the sample went out before the change was saved: %q, %v", saved, err)
	}
	if len(tester.cameras) != 1 {
		t.Errorf("samples sent: %v", tester.cameras)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// byText finds a button by its exact text, inside the optional scope.
func byText(scope, text string) chromedp.Search {
	return chromedp.Search(scope + `//button[normalize-space()="` + text + `"]`)
}

// The When screen: a choice is saved by itself, the recipients question shows with two
// chats, and the page fits a phone.
func TestBrowserWhen(t *testing.T) {
	_, path, ts := setup(t, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}}})
	ctx, jsErrors := browser(t)
	var page string
	var width int
	chats := `//*[@role="group"][@aria-label="Who receives the notifications?"]`
	err := chromedp.Do(ctx,
		chromedp.EmulateViewport(360, 740),
		inEnglish(ts.URL+"/#when"),
		chromedp.WaitVisible(`#when`),
		chromedp.Click(byText("", "🚶🚗 People and cars")),
	)
	if err != nil {
		t.Fatal(err)
	}
	// "person" is already in config.yml: the saved list must be exactly the choice.
	waitSaved(t, path, "car and person", func(f map[string]any) bool {
		return slices.Equal(strs(notifyOf(f)["labels"]), []string{"car", "person"})
	})
	err = chromedp.Do(ctx,
		chromedp.Click(byText("", "🌙 No notifications")),
	)
	if err != nil {
		t.Fatal(err)
	}
	f := waitSaved(t, path, "off at night", func(f map[string]any) bool {
		return slices.Equal(hours(notifyOf(f)["off_hours"]), []string{"22:00-07:00"})
	})
	if q := hours(notifyOf(f)["quiet_hours"]); len(q) != 0 {
		t.Errorf("quiet hours = %v, want none", q)
	}
	// Choosing who receives: the chip adds the chat to the saved list.
	err = chromedp.Do(ctx,
		chromedp.Click(byText(chats, "famille")),
	)
	if err != nil {
		t.Fatal(err)
	}
	waitSaved(t, path, "moi and famille as recipients", func(f map[string]any) bool {
		return slices.Equal(strs(notifyOf(f)["chats"]), []string{"famille", "moi"})
	})
	err = chromedp.Do(ctx,
		into(chromedp.Text(`#when`), &page),
		into(chromedp.Evaluate[int](`document.documentElement.scrollWidth`), &width),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page, "Who receives the notifications?") {
		t.Errorf("the recipients question is missing: %q", page)
	}
	if width > 360 {
		t.Errorf("the page is %d px wide on a 360 px screen", width)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// The folded choices of When, in events mode: the sensitivity slider only shows its
// value while dragged and saves on release, and the known-labels pills are saved.
func TestBrowserWhenMoreChoices(t *testing.T) {
	_, path, ts := setupConfig(t, "mode: events\n"+testConfig, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}}})
	if resp := do(t, ts, "PUT", "/api/settings", `{"notify":{"ignore_sub_labels":["Bob"]}}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/settings: %d", resp.StatusCode)
	}
	ctx, jsErrors := browser(t)
	slider := `input[type=range][aria-label="Minimum score"]`
	var hidden bool
	var during map[string]any
	err := chromedp.Do(ctx,
		inEnglish(ts.URL+"/#when"),
		chromedp.WaitVisible(`#when`),
		chromedp.Click(`#when details.more > summary`),
		chromedp.WaitVisible(slider),
		// Dragging (input events) changes the text only.
		chromedp.Evaluate[chromedp.Void](`var r = document.querySelector('input[type=range]'); r.value = "0.4"; r.dispatchEvent(new Event("input", { bubbles: true })); 0`),
		chromedp.Poll[chromedp.Void](`document.querySelector("#when").textContent.includes("40%")`),
		into(chromedp.Evaluate[bool](`document.getElementById("save-status").hidden`), &hidden),
	)
	if err != nil {
		t.Fatal(err)
	}
	during = savedFile(t, path)
	if !hidden || notifyOf(during)["min_score"] != nil {
		t.Errorf("dragging saved: status hidden = %v, saved %v", hidden, during)
	}
	// Releasing it saves the value.
	err = chromedp.Do(ctx,
		chromedp.Evaluate[chromedp.Void](`document.querySelector('input[type=range]').dispatchEvent(new Event("change", { bubbles: true })); 0`),
	)
	if err != nil {
		t.Fatal(err)
	}
	waitSaved(t, path, "sensitivity 0.4", func(f map[string]any) bool {
		return notifyOf(f)["min_score"] == 0.4
	})
	// Known labels: "Only unknown objects" is saved, and hides the labels to ignore.
	err = chromedp.Do(ctx,
		chromedp.Click(byText("", "🏷 Only unknown objects")),
		chromedp.WaitNotPresent(`#when [aria-label="Don't notify for:"]`),
	)
	if err != nil {
		t.Fatal(err)
	}
	waitSaved(t, path, "only unknown objects", func(f map[string]any) bool { return notifyOf(f)["ignore_known"] == true })
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// A change made on one screen is saved even if the screen is left before the delay,
// and the links of the footer send the pending change before leaving the page.
func TestBrowserSavesWhenLeaving(t *testing.T) {
	_, path, ts := setup(t, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}}})
	ctx, jsErrors := browser(t)
	var where string
	err := chromedp.Do(ctx,
		inEnglish(ts.URL+"/#when"),
		chromedp.WaitVisible(`#when`),
		chromedp.Click(byText("", "🚶🚗 People and cars")),
		chromedp.Click(`#nav a[href="#style"]`), // well before the 600 ms delay
		chromedp.WaitVisible(`#styles`),
	)
	if err != nil {
		t.Fatal(err)
	}
	waitSaved(t, path, "car and person", func(f map[string]any) bool {
		return slices.Equal(strs(notifyOf(f)["labels"]), []string{"car", "person"})
	})
	err = chromedp.Do(ctx,
		chromedp.Click(`#nav a[href="#when"]`),
		chromedp.WaitVisible(`#when`),
		chromedp.Click(byText("", "🌙 No notifications")),
		chromedp.Click(`footer a[href="classic"]`),
		chromedp.WaitVisible(`footer a[href="./"]`), // the classic page
		into(chromedp.Evaluate[string](`location.pathname`), &where),
	)
	if err != nil {
		t.Fatal(err)
	}
	// Read at once, with no waiting: the save has to be over before the page is left.
	if f := savedFile(t, path); !slices.Equal(hours(notifyOf(f)["off_hours"]), []string{"22:00-07:00"}) {
		t.Errorf("the change was not saved before leaving for %s: %v", where, f)
	}
	if !strings.HasSuffix(where, "/classic") {
		t.Errorf("left for %q, want the classic interface", where)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// The Recipients screen: limiting one chat is saved, and going back to "All the time"
// removes it again.
func TestBrowserRecipients(t *testing.T) {
	_, path, ts := setup(t, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}}})
	ctx, jsErrors := browser(t)
	famille := `//*[@data-chat="famille"]`
	err := chromedp.Do(ctx,
		chromedp.EmulateViewport(360, 740),
		inEnglish(ts.URL+"/#recipients"),
		chromedp.WaitVisible(`#recipients`),
		chromedp.Click(byText(famille, "🌙 Only at night")),
		chromedp.WaitVisible(`#save-status.ok`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if saved, err := os.ReadFile(path); err != nil || !strings.Contains(string(saved), "famille") || !strings.Contains(string(saved), "off_hours") {
		t.Errorf("recipient not saved: %q, %v", saved, err)
	}
	err = chromedp.Do(ctx,
		chromedp.Click(byText(famille, "All the time")),
		chromedp.WaitVisible(`#save-status.ok`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if saved, err := os.ReadFile(path); err != nil || strings.Contains(string(saved), "famille") {
		t.Errorf("recipient not removed: %q, %v", saved, err)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// With a single chat there is nobody to choose between: no recipients question, no
// Recipients screen in the menu.
func TestBrowserWhenSingleChat(t *testing.T) {
	yaml := strings.Replace(testConfig, "    famille: -100\n", "", 1)
	_, _, ts := setupConfig(t, yaml, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}}})
	ctx, jsErrors := browser(t)
	var page, current string
	var links int
	err := chromedp.Do(ctx,
		inEnglish(ts.URL+"/#when"),
		chromedp.WaitVisible(`#when`),
		into(chromedp.Text(`#when`), &page),
		into(chromedp.Evaluate[int](`document.querySelectorAll('#nav a[href="#recipients"]').length`), &links),
		// A link to a screen that is not offered shows the overview, and the menu says so.
		chromedp.Evaluate[chromedp.Void](`location.hash = "#recipients"; 0`),
		chromedp.WaitVisible(`#overview`),
		into(chromedp.Text(`#nav [aria-current=page]`), &current),
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(page, "Who receives the notifications?") {
		t.Errorf("the recipients question shows with one chat: %q", page)
	}
	if links != 0 {
		t.Errorf("the menu links to Recipients with one chat")
	}
	if current != "Overview" {
		t.Errorf("current menu entry for #recipients with one chat = %q, want Overview", current)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// The Advanced screen: an invalid duration or address is flagged and never saved, a
// valid one is saved by itself, and the page fits a phone.
func TestBrowserAdvanced(t *testing.T) {
	_, path, ts := setup(t, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}}})
	ctx, jsErrors := browser(t)
	cooldown := `[data-key="cooldown"] input`
	var invalid, urlInvalid, shadow, described, message string
	var typingAlerts, urlAlerts, width int
	err := chromedp.Do(ctx,
		chromedp.EmulateViewport(360, 740),
		inEnglish(ts.URL+"/#advanced"),
		chromedp.WaitVisible(`#advanced`),
		chromedp.Evaluate[chromedp.Void](`var i = document.querySelector('[data-key="cooldown"] input'); i.focus(); i.select(); 0`),
		chromedp.SendKeys(cooldown, "5 minutes"+kb.Tab),
		into(chromedp.Evaluate[string](`document.querySelector('[data-key="cooldown"] input').getAttribute("aria-invalid") || ""`), &invalid),
		// The flag is visible: a red outline, and a message the field points to.
		into(chromedp.Evaluate[string](`getComputedStyle(document.querySelector('[data-key="cooldown"] input')).boxShadow`), &shadow),
		into(chromedp.Evaluate[string](`document.querySelector('[data-key="cooldown"] input').getAttribute("aria-describedby") || ""`), &described),
		into(chromedp.Text(`[data-key="cooldown"] [role=alert]`), &message),
	)
	if err != nil {
		t.Fatal(err)
	}
	if invalid != "true" {
		t.Errorf("aria-invalid = %q for 5 minutes", invalid)
	}
	if !strings.Contains(shadow, "inset") || !strings.Contains(described, "-error") || message == "" {
		t.Errorf("invalid duration: box-shadow %q, aria-describedby %q, message %q", shadow, described, message)
	}
	// A valid address is saved without its trailing slash, and the Test link points to it.
	// That save carries the whole overlay: it proves the invalid duration is not in it.
	var link string
	err = chromedp.Do(ctx,
		chromedp.SendKeys(`#external-url`, "https://frigate.example.org/"+kb.Tab),
		chromedp.Poll[chromedp.Void](`document.querySelector('[data-key="external_url"] a')?.getAttribute("href") === "https://frigate.example.org"`),
		into(chromedp.Evaluate[string](`document.querySelector('[data-key="external_url"] a').getAttribute("target") + " " + document.querySelector('[data-key="external_url"] a').getAttribute("rel")`), &link),
	)
	if err != nil {
		t.Fatal(err)
	}
	f := waitSaved(t, path, "the valid address", func(f map[string]any) bool { return f["external_url"] == "https://frigate.example.org" })
	if c := notifyOf(f)["cooldown"]; c != "1m0s" { // config.yml's value, still
		t.Errorf("an invalid duration was saved: cooldown = %v", c)
	}
	if link != "_blank noopener" {
		t.Errorf("Test link target and rel = %q", link)
	}
	err = chromedp.Do(ctx,
		chromedp.Evaluate[chromedp.Void](`var i = document.querySelector('[data-key="cooldown"] input'); i.focus(); i.select(); 0`),
		chromedp.SendKeys(cooldown, "90s"+kb.Tab),
		chromedp.Evaluate[chromedp.Void](`var i = document.getElementById("external-url"); i.focus(); i.select(); 0`),
		chromedp.SendKeys(`#external-url`, "frigate.local"),
		// No error while typing: it comes when the field is left.
		into(chromedp.Evaluate[int](`document.querySelectorAll('[data-key="external_url"] [role=alert]').length`), &typingAlerts),
		chromedp.SendKeys(`#external-url`, kb.Tab),
		into(chromedp.Evaluate[string](`document.getElementById("external-url").getAttribute("aria-invalid") || ""`), &urlInvalid),
		into(chromedp.Evaluate[int](`document.querySelectorAll('[data-key="external_url"] [role=alert]').length`), &urlAlerts),
		into(chromedp.Evaluate[int](`document.documentElement.scrollWidth`), &width),
	)
	if err != nil {
		t.Fatal(err)
	}
	f = waitSaved(t, path, "the 90 s cooldown", func(f map[string]any) bool { c := notifyOf(f)["cooldown"]; return c == "1m30s" || c == "90s" })
	if f["external_url"] != "https://frigate.example.org" {
		t.Errorf("an invalid address was saved: external_url = %v", f["external_url"])
	}
	if urlInvalid != "true" || typingAlerts != 0 || urlAlerts != 1 {
		t.Errorf("frigate.local: aria-invalid %q, %d alerts while typing, %d once left", urlInvalid, typingAlerts, urlAlerts)
	}
	if width > 360 {
		t.Errorf("the page is %d px wide on a 360 px screen", width)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// The Advanced controls: time ranges, the minimum score and the chips are saved, every
// control is named apart from its siblings, and declining the reset changes nothing.
func TestBrowserAdvancedControls(t *testing.T) {
	cams := fakeCameras{cams: []frigate.CameraInfo{{Name: "garage", Labels: []string{"person", "car"}}}}
	_, path, ts := setup(t, "", cams)
	ctx, jsErrors := browser(t)
	var names, describedBy, scoreSelect, groups string
	var asked int
	const times = `JSON.stringify([...document.querySelectorAll('#advanced input[type=time]')].map(i => i.getAttribute("aria-label")))`
	err := chromedp.Do(ctx,
		inEnglish(ts.URL+"/#advanced"),
		chromedp.WaitVisible(`#advanced`),
		// Time ranges: two quiet ranges and one off range; the first one starts earlier, the second is removed.
		chromedp.Click(`[data-key="quiet_hours"] button.btn-link`),
		chromedp.Click(`[data-key="quiet_hours"] button.btn-link`),
		chromedp.Click(`[data-key="off_hours"] button.btn-link`),
		into(chromedp.Evaluate[string](times), &names),
		chromedp.Evaluate[chromedp.Void](`var i = document.querySelector('[data-key="quiet_hours"] input[type=time]'); i.value = "21:30"; i.dispatchEvent(new Event("change", { bubbles: true })); 0`),
		chromedp.Click(`[data-key="quiet_hours"] div.flex-wrap:nth-of-type(2) button[aria-label="Remove"]`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if want := `["Quiet hours 1 – from","Quiet hours 1 – to","Quiet hours 2 – from","Quiet hours 2 – to","Off hours 1 – from","Off hours 1 – to"]`; names != want {
		t.Errorf("time inputs are named %s, want %s", names, want)
	}
	waitSaved(t, path, "one quiet range from 21:30 and one off range", func(f map[string]any) bool {
		n := notifyOf(f)
		return slices.Equal(hours(n["quiet_hours"]), []string{"21:30-07:00"}) && slices.Equal(hours(n["off_hours"]), []string{"22:00-07:00"})
	})

	// Minimum score: one value per object, then a value for the first object.
	score := `[data-key="min_score"]`
	err = chromedp.Do(ctx,
		chromedp.Evaluate[chromedp.Void](`var s = document.querySelector('[data-key="min_score"] select'); s.value = "label"; s.dispatchEvent(new Event("change", { bubbles: true })); 0`),
		chromedp.WaitVisible(score+` input[type=number][aria-label="car"]`),
		into(chromedp.Evaluate[string](`document.querySelector('[data-key="min_score"] select').getAttribute("aria-label")`), &scoreSelect),
		chromedp.Evaluate[chromedp.Void](`var n = document.querySelector('[data-key="min_score"] input[type=number]'); n.value = "0.8"; n.dispatchEvent(new Event("change", { bubbles: true })); 0`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if scoreSelect != "One value per object" {
		t.Errorf("score mode select is named %q", scoreSelect)
	}
	waitSaved(t, path, "a score for car", func(f map[string]any) bool {
		m, _ := notifyOf(f)["min_score"].(map[string]any)
		return m["car"] == 0.8
	})

	// Chips: a value typed in the field and a toggled option.
	err = chromedp.Do(ctx,
		chromedp.SendKeys(`[data-key="labels"] input[aria-label="Objects – add…"]`, "chien"+kb.Enter),
		chromedp.Click(byText(`//*[@data-key="labels"]`, "car")),
	)
	if err != nil {
		t.Fatal(err)
	}
	waitSaved(t, path, "chien and car added to person", func(f map[string]any) bool {
		return slices.Equal(strs(notifyOf(f)["labels"]), []string{"car", "chien", "person"})
	})

	// Each control says what it is: the hint is tied to the field, the groups carry its name.
	err = chromedp.Do(ctx,
		into(chromedp.Evaluate[string](`var i = document.querySelector('[data-key="cooldown"] input'); i.getAttribute("aria-describedby").split(" ").map(id => document.getElementById(id)?.textContent).join("|")`), &describedBy),
		into(chromedp.Evaluate[string](`[...document.querySelectorAll('#advanced [role=group]')].map(g => g.getAttribute("aria-label")).join("|")`), &groups),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(describedBy, "Per camera and per object") {
		t.Errorf("the cooldown input is described by %q, want its hint", describedBy)
	}
	for _, want := range []string{"Quiet hours", "Off hours", "Objects", "Minimum score"} {
		if !strings.Contains("|"+groups+"|", "|"+want+"|") {
			t.Errorf("groups = %q, %s missing", groups, want)
		}
	}

	// Declining the confirmation keeps the saved settings.
	err = chromedp.Do(ctx,
		chromedp.Evaluate[chromedp.Void](`window.asked = 0; window.confirm = () => { window.asked++; return false; }; 0`),
		chromedp.Click(`#reset`),
		chromedp.Poll[chromedp.Void](`window.asked === 1`),
		// A round trip to the server after the click: a reset sent anyway would be done by now.
		chromedp.Evaluate[chromedp.Void](`fetch("api/settings").then(r => r.json()).then(d => { window.custom = d.custom; }); 0`),
		chromedp.Poll[chromedp.Void](`window.custom !== undefined`),
		into(chromedp.Evaluate[int](`window.custom ? 1 : 0`), &asked),
	)
	if err != nil {
		t.Fatal(err)
	}
	if asked != 1 || savedFile(t, path) == nil {
		t.Errorf("declining the reset: settings still custom = %d, file kept = %v", asked, savedFile(t, path) != nil)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// Going back to the config.yml settings forgets the text typed in an address field.
func TestBrowserAdvancedResetForgetsTypedText(t *testing.T) {
	_, _, ts := setup(t, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}}})
	ctx, jsErrors := browser(t)
	cooldown := `[data-key="cooldown"] input`
	var invalid string
	var alerts int
	err := chromedp.Do(ctx,
		inEnglish(ts.URL+"/#advanced"),
		chromedp.WaitVisible(`#advanced`),
		chromedp.Evaluate[chromedp.Void](`var i = document.querySelector('[data-key="cooldown"] input'); i.focus(); i.select(); 0`),
		chromedp.SendKeys(cooldown, "90s"+kb.Tab),
		chromedp.WaitVisible(`#save-status.ok`),
		chromedp.SendKeys(`#external-url`, "frigate.local"+kb.Tab),
		chromedp.Evaluate[chromedp.Void](`window.confirm = () => true; 0`),
		chromedp.Click(`#reset`),
		chromedp.WaitNotPresent(`#reset`),
		into(chromedp.Evaluate[string](`document.getElementById("external-url").getAttribute("aria-invalid") || ""`), &invalid),
		into(chromedp.Evaluate[int](`document.querySelectorAll('#advanced [role="alert"]').length`), &alerts),
	)
	if err != nil {
		t.Fatal(err)
	}
	if invalid != "" || alerts != 0 {
		t.Errorf("after the reset: aria-invalid %q, %d alerts", invalid, alerts)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// With a password, a page of the service's own leads to its login page (never the
// browser's dialog), then back to the interface.
func TestBrowserLogin(t *testing.T) {
	_, _, ts := setup(t, "s3cret", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}}})
	ctx, jsErrors := browser(t)
	var errText string
	err := chromedp.Do(ctx,
		chromedp.Navigate(ts.URL+"/"),
		chromedp.WaitVisible(`.login-card input[type=password]`),
		chromedp.SendKeys(`#password`, "wrong"),
		chromedp.Submit(`.login-card`),
		chromedp.WaitVisible(`.login-error`),
		into(chromedp.Text(`.login-error`), &errText),
		chromedp.SendKeys(`#password`, "s3cret"),
		chromedp.Submit(`.login-card`),
		chromedp.WaitVisible(`#cameras-list`),
		chromedp.WaitVisible(`#logout`),
		chromedp.Click(`#logout`),
		chromedp.WaitVisible(`.login-card`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if errText == "" {
		t.Error("no error shown for a wrong password")
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// A deep link survives the login: the fragment is carried through the form.
func TestBrowserLoginKeepsRoute(t *testing.T) {
	_, _, ts := setup(t, "s3cret", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}}})
	ctx, jsErrors := browser(t)
	var hash string
	err := chromedp.Do(ctx,
		chromedp.Navigate(ts.URL+"/#cameras/garage"),
		chromedp.WaitVisible(`.login-card input[type=password]`),
		chromedp.SendKeys(`#password`, "s3cret"),
		chromedp.Click(`.login-card button[type=submit]`), // Submit() would skip the submit event
		chromedp.WaitVisible(`#cameras`),
		into(chromedp.Evaluate[string](`location.hash`), &hash),
	)
	if err != nil {
		t.Fatal(err)
	}
	if hash != "#cameras/garage" {
		t.Errorf("location.hash after login = %q, want #cameras/garage", hash)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// Changing a connection with saved passwords: a box removes one, and a password
// is no longer "unchanged" once its address changes (it would not be kept).
func TestBrowserSetupSavedPasswords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "connection.yml")
	current := config.Connection{
		Frigate:  config.Frigate{URL: "http://192.168.1.10:5000"},
		MQTT:     config.MQTT{Broker: "tcp://192.168.1.10:1883", Password: "mqtt-secret"},
		Telegram: config.Telegram{Token: "123:abc", Chats: map[string]int64{"Alice": 111}},
		Web:      config.ConnectionWeb{Password: "web-secret"},
	}
	saved := make(chan struct{})
	s := NewSetup(path, &current, &fakeProber{}, slog.New(slog.DiscardHandler), func() { close(saved) })
	mux := http.NewServeMux()
	s.MountAlone(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, jsErrors := browser(t)
	var before, after chromedp.AttributeResult
	var unchanged string
	var webDisabled bool
	err := chromedp.Do(ctx,
		chromedp.Navigate(ts.URL+"/"),
		chromedp.WaitVisible(`#clear-web-pass-row`),
		into(chromedp.AttributeValue(`#mqtt-pass`, "placeholder"), &before),
		into(chromedp.Evaluate[string](`T("unchanged")`), &unchanged), // in the browser's language
		chromedp.Evaluate[chromedp.Void](`document.getElementById("mqtt-broker").value = "192.168.1.99"; document.getElementById("mqtt-broker").dispatchEvent(new Event("input")); 0`),
		into(chromedp.AttributeValue(`#mqtt-pass`, "placeholder"), &after),
		chromedp.Click(`#clear-web-pass`),
		into(chromedp.Evaluate[bool](`document.getElementById("web-pass").disabled`), &webDisabled),
		chromedp.Evaluate[chromedp.Void](`document.getElementById("mqtt-broker").value = "192.168.1.10"; 0`),
		chromedp.Click(`#save`),
		chromedp.WaitVisible(`#save-result.ok`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if before.Value != unchanged || after.Value == unchanged {
		t.Errorf("MQTT password placeholder: %q, then %q after changing the broker", before.Value, after.Value)
	}
	if !webDisabled {
		t.Error("removing the interface password must disable its field")
	}
	select {
	case <-saved:
	case <-time.After(5 * time.Second):
		t.Fatal("the connection was not saved")
	}
	c, err := config.LoadConnection(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Web.Password != "" || c.MQTT.Password != "mqtt-secret" {
		t.Errorf("web password %q (want removed), mqtt password %q (want kept)", c.Web.Password, c.MQTT.Password)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// On a phone, in German (the longest texts), the setup page does not scroll sideways.
func TestBrowserSetupFitsAPhone(t *testing.T) {
	s := NewSetup(filepath.Join(t.TempDir(), "connection.yml"), nil, &fakeProber{}, slog.New(slog.DiscardHandler), func() {})
	mux := http.NewServeMux()
	s.MountAlone(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, jsErrors := browser(t)
	var width int
	var heading string
	err := chromedp.Do(ctx,
		chromedp.EmulateViewport(360, 740),
		chromedp.Navigate(ts.URL+"/"),
		chromedp.WaitVisible(`#intro-first`),
		chromedp.Evaluate[chromedp.Void](`const m = document.querySelector("header select"); m.value = "de"; m.dispatchEvent(new Event("change", {bubbles: true})); 0`),
		chromedp.SendKeys(`#token`, "123:abc"),
		chromedp.Click(`#check-token`),
		chromedp.WaitVisible(`#chats .chip`),
		chromedp.Click(`#manual summary`),
		into(chromedp.Evaluate[int](`document.documentElement.scrollWidth`), &width),
		into(chromedp.Evaluate[string](`document.querySelector("#sec-telegram h2").textContent`), &heading),
	)
	if err != nil {
		t.Fatal(err)
	}
	if width > 360 {
		t.Errorf("the page is %d px wide on a 360 px screen", width)
	}
	if heading != "Telegram-Bot" {
		t.Errorf("the Telegram card heading is %q in German, want \"Telegram-Bot\"", heading)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// A manual check of Frigate shows its answer at once, even while the automatic
// search on opening is still running.
func TestBrowserSetupCheckDuringSearch(t *testing.T) {
	wait := make(chan struct{})
	s := NewSetup(filepath.Join(t.TempDir(), "connection.yml"), nil, &fakeProber{discover: true, discoverWait: wait},
		slog.New(slog.DiscardHandler), func() {})
	mux := http.NewServeMux()
	s.MountAlone(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	defer close(wait) // runs first: lets the blocked search end, so ts.Close does not wait for it

	ctx, jsErrors := browser(t)
	var text string
	err := chromedp.Do(ctx, chromedp.Navigate(ts.URL+"/"), chromedp.WaitVisible(`#frigate-result`)) // "Looking for Frigate on your network…"
	if err != nil {
		t.Fatal(err)
	}
	// The search lasts up to 20 s: the answer must come well before.
	quick, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err = chromedp.Do(quick,
		chromedp.SendKeys(`#frigate-url`, "http://192.168.1.20:5000"),
		chromedp.Click(`#check-frigate`),
		chromedp.WaitVisible(`#frigate-result.ok`),
		into(chromedp.Text(`#frigate-result`), &text),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "0.16.0") {
		t.Errorf("result %q, want the Frigate checked", text)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// The setup page's language menu: a choice is saved and kept after a reload, and
// Auto removes it so that the page follows the browser again.
func TestBrowserSetupLanguageMenu(t *testing.T) {
	s := NewSetup(filepath.Join(t.TempDir(), "connection.yml"), nil, &fakeProber{}, slog.New(slog.DiscardHandler), func() {})
	mux := http.NewServeMux()
	s.MountAlone(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, jsErrors := browser(t)
	choose := func(code string) chromedp.Action[chromedp.Void] {
		return chromedp.Evaluate[chromedp.Void](`const m = document.querySelector("header select"); m.value = "` + code + `"; m.dispatchEvent(new Event("change", {bubbles: true})); 0`)
	}
	// menu value, page language, saved choice
	const state = `[document.querySelector("header select").value, document.documentElement.lang, localStorage.getItem("frigate-telegram-enhanced-lang") || "-"].join(" ")`
	var first, forced, back string
	err := chromedp.Do(ctx,
		chromedp.Navigate(ts.URL+"/"),
		chromedp.WaitVisible(`#intro-first`),
		into(chromedp.Evaluate[string](state), &first),
		choose("de"),
		chromedp.Reload(),
		chromedp.WaitVisible(`#intro-first`),
		into(chromedp.Evaluate[string](state), &forced),
		choose("auto"),
		into(chromedp.Evaluate[string](state), &back),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first, "auto ") || !strings.HasSuffix(first, " -") {
		t.Errorf("on arrival: %q, want auto and nothing saved", first)
	}
	if forced != "de de de" {
		t.Errorf("after choosing German and reloading: %q, want \"de de de\"", forced)
	}
	if back != first {
		t.Errorf("back to Auto: %q, want %q as on arrival", back, first)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// savedCameras reads the camera overrides of the saved settings file.
func savedCameras(t *testing.T, path string) map[string]map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var f struct {
		Cameras map[string]map[string]any `yaml:"cameras"`
	}
	if err := yaml.Unmarshal(b, &f); err != nil {
		t.Fatalf("saved settings are not YAML: %v\n%s", err, b)
	}
	return f.Cameras
}

// waitCameras waits (up to 5 s) for the saved camera overrides to satisfy ok.
func waitCameras(t *testing.T, path, what string, ok func(map[string]map[string]any) bool) map[string]map[string]any {
	t.Helper()
	var cams map[string]map[string]any
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if cams = savedCameras(t, path); ok(cams) {
			return cams
		}
	}
	t.Fatalf("saved cameras never became %s: %v", what, cams)
	return nil
}

// savedFile reads the whole saved settings file (nil while nothing is saved).
func savedFile(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var f map[string]any
	if err := yaml.Unmarshal(b, &f); err != nil {
		t.Fatalf("saved settings are not YAML: %v\n%s", err, b)
	}
	return f
}

// notifyOf is the default (global) notification settings of a saved file.
func notifyOf(f map[string]any) map[string]any {
	n, _ := f["notify"].(map[string]any)
	return n
}

// waitSaved waits (up to 5 s) for the saved file to satisfy ok.
func waitSaved(t *testing.T, path, what string, ok func(map[string]any) bool) map[string]any {
	t.Helper()
	var f map[string]any
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if f = savedFile(t, path); f != nil && ok(f) {
			return f
		}
	}
	t.Fatalf("the saved settings never became %s: %v", what, f)
	return nil
}

// strs is a saved list of text as a sorted slice.
func strs(v any) []string {
	var out []string
	l, _ := v.([]any)
	for _, x := range l {
		out = append(out, x.(string))
	}
	slices.Sort(out)
	return out
}

// hours is a saved list of time ranges as "from-to" texts.
func hours(v any) []string {
	var out []string
	l, _ := v.([]any)
	for _, x := range l {
		r := x.(map[string]any)
		out = append(out, r["from"].(string)+"-"+r["to"].(string))
	}
	return out
}

// The Cameras screen: #cameras/<name> opens that card, a style, a zone and a
// customized setting are saved under the camera, and resetting a card removes it.
func TestBrowserCameras(t *testing.T) {
	cams := fakeCameras{cams: []frigate.CameraInfo{
		{Name: "garage", Zones: []string{"allee"}, Labels: []string{"person"}},
		{Name: "entree"},
	}}
	_, path, ts := setup(t, "", cams)
	if resp := do(t, ts, "PUT", "/api/settings", `{"cameras":{"ancienne":{"clip":false}}}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/settings: %d", resp.StatusCode)
	}
	ctx, jsErrors := browser(t)
	garage, old := `[data-camera="garage"]`, `[data-camera="ancienne"]`
	var open, oldExists, stillOpen, focused bool
	var customizes string
	var nested, width int
	err := chromedp.Do(ctx,
		chromedp.EmulateViewport(360, 740),
		inEnglish(ts.URL+"/#cameras/garage"),
		chromedp.WaitVisible(`#cameras`),
		chromedp.WaitVisible(garage+` summary`),
		chromedp.Poll[chromedp.Void](`document.querySelector('[data-camera="garage"]').open`),
		// A followed link leaves the focus on the camera's summary, with no control nested in it.
		chromedp.Poll[chromedp.Void](`document.activeElement.tagName === "SUMMARY"`),
		into(chromedp.Evaluate[bool](`document.activeElement.parentElement.dataset.camera === "garage"`), &focused),
		into(chromedp.Evaluate[int](`document.querySelectorAll('summary button, summary input').length`), &nested),
		into(chromedp.Evaluate[bool](`document.querySelector('[data-camera="garage"]').open`), &open),
		into(chromedp.Evaluate[bool](`!!document.querySelector('[data-camera="ancienne"]')`), &oldExists),
		// The camera's switch does not open or close its card.
		chromedp.Click(`[role=switch][aria-label="Notifications for garage"]`),
		into(chromedp.Evaluate[bool](`document.querySelector('[data-camera="garage"]').open`), &stillOpen),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !focused || nested != 0 {
		t.Errorf("summary focused = %v, %d controls nested in summaries", focused, nested)
	}
	if !open || !oldExists || !stillOpen {
		t.Fatalf("garage open = %v, ancienne listed = %v, garage open after its switch = %v", open, oldExists, stillOpen)
	}
	waitCameras(t, path, "garage disabled", func(c map[string]map[string]any) bool { return c["garage"]["enabled"] == false })
	err = chromedp.Do(ctx,
		chromedp.Click(`[role=switch][aria-label="Notifications for garage"]`),
	)
	if err != nil {
		t.Fatal(err)
	}
	waitCameras(t, path, "garage enabled again", func(c map[string]map[string]any) bool { return len(c["garage"]) == 0 })

	err = chromedp.Do(ctx,
		chromedp.Click(byText(`//details[@data-camera="garage"]`, "Photo only")),
	)
	if err != nil {
		t.Fatal(err)
	}
	waitCameras(t, path, "garage photo only", func(c map[string]map[string]any) bool {
		return c["garage"]["clip"] == false && c["garage"]["snapshot"] == true && c["garage"]["gif"] == false
	})
	err = chromedp.Do(ctx, chromedp.Click(byText(`//details[@data-camera="garage"]`, "Default")))
	if err != nil {
		t.Fatal(err)
	}
	waitCameras(t, path, "garage back to default", func(c map[string]map[string]any) bool { _, ok := c["garage"]; return !ok })

	err = chromedp.Do(ctx, chromedp.Click(byText(`//details[@data-camera="garage"]`, "📍 allee")))
	if err != nil {
		t.Fatal(err)
	}
	c := waitCameras(t, path, "garage in allee", func(c map[string]map[string]any) bool { return len(c["garage"]) > 0 })
	if z, _ := c["garage"]["zones"].([]any); len(z) != 1 || z[0] != "allee" {
		t.Errorf("garage zones = %v, want [allee]", c["garage"]["zones"])
	}

	// A customized setting in "All settings for garage" is saved, then removed.
	cooldown := garage + ` [data-key="cooldown"] input[type=checkbox]`
	err = chromedp.Do(ctx,
		chromedp.Click(garage+` details.more > summary`),
		chromedp.WaitVisible(cooldown),
		// The box is described by the name of its setting, or the repeated "customize" tell nothing apart.
		into(chromedp.Evaluate[string](`var c = document.querySelector('[data-camera="garage"] [data-key="cooldown"] input[type=checkbox]'); document.getElementById(c.getAttribute("aria-describedby")).textContent`), &customizes),
		chromedp.Click(cooldown),
	)
	if err != nil {
		t.Fatal(err)
	}
	if customizes != "Delay between two notifications" {
		t.Errorf("the customize box of the cooldown is described by %q", customizes)
	}
	waitCameras(t, path, "garage cooldown customized", func(c map[string]map[string]any) bool { _, ok := c["garage"]["cooldown"]; return ok })
	err = chromedp.Do(ctx, chromedp.Click(cooldown))
	if err != nil {
		t.Fatal(err)
	}
	waitCameras(t, path, "garage cooldown removed", func(c map[string]map[string]any) bool { _, ok := c["garage"]["cooldown"]; return !ok })

	err = chromedp.Do(ctx,
		chromedp.Click(old+` summary`),
		chromedp.Click(old+` details.more > summary`),
		chromedp.Click(byText(`//details[@data-camera="ancienne"]`, "Reset everything to default")),
	)
	if err != nil {
		t.Fatal(err)
	}
	c = waitCameras(t, path, "ancienne reset", func(c map[string]map[string]any) bool { _, ok := c["ancienne"]; return !ok })
	if _, ok := c["garage"]["zones"]; !ok {
		t.Errorf("resetting ancienne touched garage: %v", c)
	}
	// "Everywhere" equals the global level: the zones override is removed, not stored empty.
	err = chromedp.Do(ctx, chromedp.Click(byText(`//details[@data-camera="garage"]`, "Everywhere")))
	if err != nil {
		t.Fatal(err)
	}
	waitCameras(t, path, "garage with no zones override", func(c map[string]map[string]any) bool { _, ok := c["garage"]["zones"]; return !ok })
	err = chromedp.Do(ctx, into(chromedp.Evaluate[int](`document.documentElement.scrollWidth`), &width))
	if err != nil {
		t.Fatal(err)
	}
	if width > 360 {
		t.Errorf("the page is %d px wide on a 360 px screen", width)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// Following a camera link of the overview opens that card on the Cameras screen.
func TestBrowserCamerasLink(t *testing.T) {
	_, _, ts := setup(t, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}, {Name: "entree"}}})
	ctx, jsErrors := browser(t)
	var open, other bool
	err := chromedp.Do(ctx,
		inEnglish(ts.URL+"/"),
		chromedp.Click(`#cameras-list a[href="#cameras/entree"]`),
		chromedp.WaitVisible(`[data-camera="entree"] summary`),
		chromedp.Poll[chromedp.Void](`document.querySelector('[data-camera="entree"]').open`),
		into(chromedp.Evaluate[bool](`document.querySelector('[data-camera="entree"]').open`), &open),
		into(chromedp.Evaluate[bool](`document.querySelector('[data-camera="garage"]').open`), &other),
		// Changing the hash on the screen opens the other card too.
		chromedp.Evaluate[chromedp.Void](`location.hash = "#cameras/garage"; 0`),
		chromedp.Poll[chromedp.Void](`document.querySelector('[data-camera="garage"]').open`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !open || other {
		t.Errorf("entree open = %v, garage open = %v", open, other)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// The activity screen: eight lines until "Show all", the reason in plain words, the
// grouped outcome, and the link that opens the camera card to adjust.
func TestBrowserActivity(t *testing.T) {
	cams := fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}, {Name: "porte d'entrée"}}}
	now := time.Now()
	entries := []notifier.HistoryEntry{{ID: "x1", At: now, Camera: "porte d'entrée", Label: "person", Reason: "zone"}}
	for i := 2; i <= 10; i++ {
		entries = append(entries, notifier.HistoryEntry{ID: "x" + strconv.Itoa(i), At: now, Camera: "garage", Label: "car", Sent: true, Grouped: i == 3})
	}
	_, _, ts := setup(t, "", cams, WithHistory(fakeHistory{entries: entries}, nil))

	ctx, jsErrors := browser(t)
	var rows, rowsAll, width int
	var foot, first, page, hash string
	var open bool
	err := chromedp.Do(ctx,
		chromedp.EmulateViewport(360, 740),
		inEnglish(ts.URL+"/#activity"),
		chromedp.WaitVisible(`#activity li[data-id="x1"]`),
		into(chromedp.Evaluate[int](`document.querySelectorAll("#activity li").length`), &rows),
		into(chromedp.Text(`#activity`), &page),
		into(chromedp.Evaluate[int](`document.documentElement.scrollWidth`), &width),
		chromedp.Click(byText("", "Show all")),
		chromedp.Poll[chromedp.Void](`document.querySelectorAll("#activity li").length == 10`),
		into(chromedp.Evaluate[int](`document.querySelectorAll("#activity li").length`), &rowsAll),
		into(chromedp.Text(`#activity-foot`), &foot),
		into(chromedp.Text(`#activity li[data-id="x1"]`), &first),
		chromedp.Click(`#activity li[data-id="x1"] a`),
		chromedp.WaitVisible(`[data-camera="porte d'entrée"] summary`),
		chromedp.Poll[chromedp.Void](`document.querySelector('[data-camera="porte d\'entrée"]').open`),
		into(chromedp.Evaluate[string](`location.hash`), &hash),
		into(chromedp.Evaluate[bool](`document.querySelector('[data-camera="porte d\'entrée"]').open`), &open),
	)
	if err != nil {
		t.Fatal(err)
	}
	if rows != 8 || rowsAll != 10 {
		t.Errorf("rows = %d then %d, want 8 then 10", rows, rowsAll)
	}
	if !strings.Contains(foot, "Out of the last 10 detections:") {
		t.Errorf("foot = %q", foot)
	}
	if !strings.Contains(first, "outside the chosen zones") {
		t.Errorf("x1 = %q, want its reason", first)
	}
	if !strings.Contains(page, "📦 Grouped") {
		t.Errorf("page = %q, want a grouped line", page)
	}
	if hash != "#cameras/porte%20d'entr%C3%A9e" || !open {
		t.Errorf("hash = %q, open = %v", hash, open)
	}
	if width > 360 {
		t.Errorf("the page is %d px wide on a 360 px screen", width)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// The menu stays inside the new interface; only the footer links to the classic one,
// and the classic page links back.
func TestBrowserNoClassicLinks(t *testing.T) {
	_, _, ts := setup(t, "", fakeCameras{})
	ctx, jsErrors := browser(t)
	var inMenu, footer, back int
	err := chromedp.Do(ctx,
		inEnglish(ts.URL+"/"),
		chromedp.WaitVisible(`#nav`),
		chromedp.WaitVisible(`footer a[href="classic"]`),
		into(chromedp.Evaluate[int](`document.querySelectorAll('#nav a[href^="classic"]').length`), &inMenu),
		into(chromedp.Evaluate[int](`document.querySelectorAll('footer a[href="classic"]').length`), &footer),
		inEnglish(ts.URL+"/classic"),
		chromedp.WaitVisible(`footer`),
		into(chromedp.Evaluate[int](`[...document.querySelectorAll('footer a[href="./"]')].filter(a => a.textContent.trim() === "New interface").length`), &back),
	)
	if err != nil {
		t.Fatal(err)
	}
	if inMenu != 0 || footer != 1 || back != 1 {
		t.Errorf("classic links in the menu = %d, in the footer = %d, links back in the classic footer = %d", inMenu, footer, back)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}
