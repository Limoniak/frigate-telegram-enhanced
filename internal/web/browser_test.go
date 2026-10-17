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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"

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
