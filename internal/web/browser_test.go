//go:build browser || screenshots

// Smoke test of the interface in a real browser (Chrome, driven by chromedp): the
// page loads without JavaScript errors, shows cameras, status and activity, and
// saves a setting by itself. Run separately: go test -tags browser ./internal/web/
// (the screenshots tag compiles it too, for the helpers of screenshots_test.go)
package web

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
	"gopkg.in/yaml.v3"

	"frigate-telegram-enhanced/internal/config"
	"frigate-telegram-enhanced/internal/frigate"
	"frigate-telegram-enhanced/internal/i18n"
	"frigate-telegram-enhanced/internal/notifier"
	"frigate-telegram-enhanced/internal/state"
)

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
	var sent, today, last, lastCamera, pause, heading, barLabel, barHash string
	var bars, width int
	err = chromedp.Do(ctx,
		inEnglish(ts.URL+"/"),
		chromedp.WaitVisible(`#cameras-list`),
		chromedp.WaitVisible(`#connections`), // the health arrives last
		into(chromedp.Evaluate[string](`document.querySelector("#overview h1")?.textContent || ""`), &heading),
		chromedp.WaitVisible(`#sent-24h`),
		into(chromedp.Text(`#sent-24h`), &sent),
		into(chromedp.Text(`#today`), &today),
		chromedp.WaitVisible(`#last-notification`),
		into(chromedp.Text(`#last-notification`), &last),
		into(chromedp.Evaluate[string](`document.querySelector("#last-notification").textContent.includes("entree") ? "entree" : "garage"`), &lastCamera),
		// Each bar of the chart leads to the activity of its hour (on a phone too).
		chromedp.EmulateViewport(360, 740),
		into(chromedp.Evaluate[int](`document.querySelectorAll('#today a[href^="#activity/"]').length`), &bars),
		into(chromedp.Evaluate[string](`[...document.querySelectorAll('#today a[href^="#activity/"]')].at(-1).getAttribute("aria-label")`), &barLabel),
		into(chromedp.Evaluate[int](`document.documentElement.scrollWidth`), &width),
		chromedp.Click(`#today a[href^="#activity/"]:last-of-type`),
		chromedp.WaitVisible(`section#activity`),
		into(chromedp.Evaluate[string](`location.hash`), &barHash),
		chromedp.EmulateViewport(1280, 900),
		chromedp.Navigate(ts.URL+"/#overview"),
		chromedp.WaitVisible(`#cameras-list`),
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
	if heading != "Overview" {
		t.Errorf("the landing screen's heading = %q, want Overview", heading)
	}
	if sent != "3" {
		t.Errorf("sent in 24 h = %q, want 3", sent)
	}
	if bars != 24 || !regexp.MustCompile(`^3 sent between \d\d:00 and \d\d:00$`).MatchString(barLabel) {
		t.Errorf("chart: %d bars, last = %q, want 24 links named \"3 sent between ...\"", bars, barLabel)
	}
	if n, err := strconv.ParseInt(strings.TrimPrefix(barHash, "#activity/"), 10, 64); err != nil || n > now.Unix() || now.Unix()-n >= 3600 {
		t.Errorf("the last bar leads to %q, want the current clock hour", barHash)
	}
	if width > 360 {
		t.Errorf("the overview is %d px wide on a 360 px screen", width)
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
		return chromedp.Evaluate[chromedp.Void](`{ const m = document.querySelector('header select[aria-label="Language / Langue"]'); m.value = "` + code + `"; m.dispatchEvent(new Event("change", { bubbles: true })); } 0`)
	}
	// menu value, page language, saved choice
	const state = `[document.querySelector('header select[aria-label="Language / Langue"]').value, document.documentElement.lang, localStorage.getItem("frigate-telegram-enhanced-lang") || "-"].join(" ")`
	var first, forced, back, nav string
	err := chromedp.Do(ctx,
		chromedp.Navigate(ts.URL+"/"),
		chromedp.WaitVisible(`#cameras-list`),
		into(chromedp.Evaluate[string](state), &first),
		choose("de"),
		chromedp.Reload(),
		chromedp.WaitVisible(`#cameras-list`),
		into(chromedp.Evaluate[string](state), &forced),
		choose("auto"),
		into(chromedp.Evaluate[string](state), &back),
		choose("fr"),
		into(chromedp.Text(`#nav`), &nav),
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
	if !strings.Contains(nav, "Aperçu") {
		t.Errorf("menu in French = %q, want Aperçu", nav)
	}
	if back != first {
		t.Errorf("back to Auto: %q, want %q as on arrival", back, first)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// The theme follows the system until one is chosen in the menu (kept after a reload,
// also by the login page; the theme menu is the second select of the header), and again once "Auto" is chosen back.
func TestBrowserTheme(t *testing.T) {
	_, _, ts := setup(t, "s3cret", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}}})
	ctx, jsErrors := browser(t)
	choose := func(v string) chromedp.Action[chromedp.Void] {
		return chromedp.Evaluate[chromedp.Void](`{ const m = document.querySelectorAll("header select")[1]; m.value = "` + v + `"; m.dispatchEvent(new Event("change", { bubbles: true })); } 0`)
	}
	logIn := chromedp.Steps(
		chromedp.WaitVisible(`.login-card input[type=password]`),
		chromedp.SendKeys(`#password`, "s3cret"),
		chromedp.Click(`.login-card button[type=submit]`),
		chromedp.WaitVisible(`#cameras-list`),
	)
	const attr = `document.documentElement.getAttribute("data-theme") || "-"`
	var first, light, reloaded, login, auto string
	err := chromedp.Do(ctx,
		chromedp.Navigate(ts.URL+"/"),
		logIn,
		into(chromedp.Evaluate[string](attr), &first),
		choose("light"),
		into(chromedp.Evaluate[string](attr), &light),
		chromedp.Reload(),
		chromedp.WaitVisible(`#logout`),
		into(chromedp.Evaluate[string](attr), &reloaded),
		chromedp.Click(`#logout`),
		chromedp.WaitVisible(`.login-card`),
		into(chromedp.Evaluate[string](attr), &login),
		logIn,
		choose("auto"),
		into(chromedp.Evaluate[string](attr), &auto),
	)
	if err != nil {
		t.Fatal(err)
	}
	if first != "-" || light != "light" || reloaded != "light" || login != "light" || auto != "-" {
		t.Errorf("data-theme: arrival %q, chosen %q, reloaded %q, login page %q, auto %q; want - light light light -", first, light, reloaded, login, auto)
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
		chromedp.WSURLReadTimeout(60*time.Second),
		// A British browser: English times on a 24-hour clock, whatever the machine.
		chromedp.Flag("lang", "en-GB"), chromedp.Flag("accept-lang", "en-GB"))
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

// A screen is waited for as section#<id>: chromedp's search for #<id> also finds the
// menu's links to it, hidden on a phone, and would wait for them to show.

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
	var chats, broker, picked string
	err := chromedp.Do(ctx,
		chromedp.Navigate(ts.URL+"/"),
		chromedp.WaitVisible(`#intro-first`),
		chromedp.WaitVisible(`#frigate-result.err`), // the search on arrival has answered
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
		// The time zone is found by typing part of its name.
		chromedp.Click(`#timezone-ts-control`),
		chromedp.SendKeys(`#timezone-ts-control`, "tokyo"),
		chromedp.WaitVisible(`.ts-dropdown .option.active[data-value="Asia/Tokyo"]`),
		chromedp.SendKeys(`#timezone-ts-control`, kb.Enter),
		// The zone picked shows in the field: the search no longer hides it. The field is
		// updated after Tom Select's own handlers (a timeout): wait for it, up to a second.
		chromedp.Evaluate[chromedp.Void](`window.shown = () => [...document.querySelectorAll("#sec-options .ts-control .item")].filter((e) => e.checkVisibility()).map((e) => e.textContent).join(); 0`),
		chromedp.Poll[chromedp.Void](`shown() !== "" || (window.waited = (window.waited || 0) + 1) > 20`, chromedp.WithPollingInterval(50*time.Millisecond)),
		into(chromedp.Evaluate[string](`shown()`), &picked),
		chromedp.Click(`#save`),
		chromedp.WaitVisible(`#save-result.ok`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(chats, "Alice") || !strings.Contains(chats, "Family") {
		t.Errorf("chats offered = %q", chats)
	}
	if picked != "Asia/Tokyo" {
		t.Errorf("zone shown after picking it = %q", picked)
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
	if c.Telegram.Chats["Alice"] != 111 || c.Telegram.Chats["Family"] != -100123 || c.Timezone != "Asia/Tokyo" || c.Frigate.URL != "http://192.168.1.10:5000" {
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
	var boxFits bool
	var photo struct {
		Loaded bool   `json:"loaded"`
		Src    string `json:"src"`
	}
	err := chromedp.Do(ctx,
		inEnglish(ts.URL+"/#style"),
		chromedp.WaitVisible(`#styles [data-style="clip"][aria-pressed="true"]`),
		into(chromedp.Text(`#style-overrides`), &overrides),
		// The preview shows a real photo, served from assets/.
		chromedp.Poll[chromedp.Void](`(() => { const i = document.querySelector("#live-preview img"); return !!i && i.complete && i.naturalWidth > 0 })()`),
		into(chromedp.Evaluate[struct {
			Loaded bool   `json:"loaded"`
			Src    string `json:"src"`
		}](`(() => { const i = document.querySelector("#live-preview img"); return { loaded: i.complete && i.naturalWidth > 0, src: i.currentSrc } })()`), &photo),
		// Cropping zooms the photo: the box on the person must stay inside the frame.
		chromedp.Click(`#style-crop`),
		chromedp.Sleep(1000*time.Millisecond), // the zoom's transition
		into(chromedp.Evaluate[bool](`(() => { const e = document.querySelector("#live-preview .border-amber-300"), b = e.getBoundingClientRect(), f = e.parentElement.parentElement.getBoundingClientRect(); return b.top >= f.top - 1 && b.bottom <= f.bottom + 1 })()`), &boxFits),
		chromedp.Click(`[data-style="gif"]`),
		chromedp.WaitVisible(`#styles [data-style="gif"][aria-pressed="true"]`),
		chromedp.Poll[chromedp.Void](`document.querySelector("#live-preview")?.textContent.includes("GIF")`),
		chromedp.Click(`#style-in-place`),
		chromedp.Poll[chromedp.Void](`document.querySelector("#live-preview")?.textContent.includes("As a reply")`),
		into(chromedp.Text(`#live-preview`), &preview),
		chromedp.Click(`#try button`), // the pending change is saved first
		chromedp.WaitVisible(`#try-result.ok`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !photo.Loaded || !strings.Contains(photo.Src, "/assets/") {
		t.Errorf("preview photo = %+v, want a loaded image under assets/", photo)
	}
	if !boxFits {
		t.Error("with the crop on, the box on the person leaves the photo frame")
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

// The preview shows the sample photo, with its box on the person.
func TestBrowserStyleSamplePhoto(t *testing.T) {
	_, _, ts := setup(t, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}}}, WithTester(&fakeTester{}))
	var src string
	ctx, jsErrors := browser(t)
	err := chromedp.Do(ctx,
		inEnglish(ts.URL+"/#style"),
		chromedp.Poll[chromedp.Void](`(() => { const i = document.querySelector("#live-preview img"); return !!i && i.complete && i.naturalWidth > 0 })()`),
		chromedp.WaitVisible(`#live-preview .border-amber-300`),
		into(chromedp.Evaluate[string](`document.querySelector("#live-preview img").currentSrc`), &src),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(src, "/assets/") {
		t.Errorf("preview image %q, want the sample photo", src)
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
	_, path, ts := setup(t, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage", Labels: []string{"car", "person"}}}})
	ctx, jsErrors := browser(t)
	var page string
	var width int
	chats := `//*[@role="group"][@aria-label="Who receives the notifications?"]`
	err := chromedp.Do(ctx,
		chromedp.EmulateViewport(360, 740),
		inEnglish(ts.URL+"/#when"),
		chromedp.WaitVisible(`section#when`),
		chromedp.Click(byText("", "🚗 Car")), // "person" is already chosen
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
	// Another night moves the hours chosen with it.
	err = chromedp.Do(ctx,
		chromedp.Evaluate[chromedp.Void](`{ const i = document.querySelector('input[aria-label="Night starts at"]'); i.value = "23:30"; i.dispatchEvent(new Event("change", { bubbles: true })); } 0`),
	)
	if err != nil {
		t.Fatal(err)
	}
	waitSaved(t, path, "off from 23:30", func(f map[string]any) bool {
		n, _ := f["night"].(map[string]any)
		return slices.Equal(hours(notifyOf(f)["off_hours"]), []string{"23:30-07:00"}) && n["from"] == "23:30"
	})
	// A night that starts where it ends is flagged and not saved; fixing the other end
	// saves both hours as typed.
	setNight := func(which, v string) chromedp.Action[chromedp.Void] {
		return chromedp.Evaluate[chromedp.Void](`{ const i = document.querySelector('input[aria-label="Night ` + which + ` at"]'); i.value = "` + v + `"; i.dispatchEvent(new Event("change", { bubbles: true })); } 0`)
	}
	var nightInvalid, nightError string
	err = chromedp.Do(ctx,
		setNight("starts", "07:00"),
		chromedp.WaitVisible(`#night-error`),
		into(chromedp.Evaluate[string](`document.querySelector('input[aria-label="Night ends at"]').getAttribute("aria-invalid") || ""`), &nightInvalid),
		into(chromedp.Text(`#night-error`), &nightError),
	)
	if err != nil {
		t.Fatal(err)
	}
	if nightInvalid != "true" || !strings.Contains(nightError, "different") {
		t.Errorf("night 07:00-07:00: aria-invalid %q, error %q", nightInvalid, nightError)
	}
	if n, _ := savedFile(t, path)["night"].(map[string]any); n["from"] != "23:30" {
		t.Errorf("night 07:00-07:00 was saved: %v", n)
	}
	err = chromedp.Do(ctx,
		setNight("ends", "06:00"),
		chromedp.WaitNotPresent(`#night-error`),
	)
	if err != nil {
		t.Fatal(err)
	}
	waitSaved(t, path, "night from 07:00 to 06:00", func(f map[string]any) bool {
		n, _ := f["night"].(map[string]any)
		return n["from"] == "07:00" && n["to"] == "06:00"
	})
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
		chromedp.WaitVisible(`section#when`),
		chromedp.Click(`#when details.more:not([data-chat]) > summary`),
		chromedp.WaitVisible(slider),
		// Dragging (input events) changes the text only.
		chromedp.Evaluate[chromedp.Void](`var r = document.querySelector('input[type=range]'); r.value = "0.4"; r.dispatchEvent(new Event("input", { bubbles: true })); 0`),
		chromedp.Poll[chromedp.Void](`document.querySelector("#when")?.textContent.includes("40%")`),
		// Watch the status from the drag on, then wait past the save delay: a drag that saved would have shown it by then.
		chromedp.Evaluate[chromedp.Void](`window.t0 = performance.now(); window.statusChanged = false; new MutationObserver(() => { window.statusChanged = true; }).observe(document.getElementById("save-status"), { attributes: true, childList: true, subtree: true, characterData: true }); 0`),
		chromedp.Poll[chromedp.Void](`performance.now() - window.t0 > 1500`),
		into(chromedp.Evaluate[bool](`document.getElementById("save-status").hidden && !window.statusChanged`), &hidden),
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
	setupPage := NewSetup(filepath.Join(t.TempDir(), "connection.yml"), nil, &fakeProber{}, slog.New(slog.DiscardHandler), func() {})
	_, path, ts := setup(t, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage", Labels: []string{"car", "person"}}}}, WithSetup(setupPage))
	ctx, jsErrors := browser(t)
	var where string
	err := chromedp.Do(ctx,
		inEnglish(ts.URL+"/#when"),
		chromedp.WaitVisible(`section#when`),
		chromedp.Click(byText("", "🚗 Car")),     // "person" is already chosen
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
		chromedp.WaitVisible(`section#when`),
		chromedp.Click(byText("", "🌙 No notifications")),
		chromedp.Click(`#nav a[href="setup"]`),
		chromedp.WaitVisible(`#intro-first`), // the connection page
		into(chromedp.Evaluate[string](`location.pathname`), &where),
	)
	if err != nil {
		t.Fatal(err)
	}
	// Read at once, with no waiting: the save has to be over before the page is left.
	if f := savedFile(t, path); !slices.Equal(hours(notifyOf(f)["off_hours"]), []string{"22:00-07:00"}) {
		t.Errorf("the change was not saved before leaving for %s: %v", where, f)
	}
	if !strings.HasSuffix(where, "/setup") {
		t.Errorf("left for %q, want the connection page", where)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// The recipients, folded under who receives on the When screen: limiting one chat is
// saved, and going back to "Same as everyone" removes it again.
func TestBrowserRecipients(t *testing.T) {
	_, path, ts := setup(t, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}}})
	ctx, jsErrors := browser(t)
	hoursOf := `//*[@aria-label="famille – Hours"]`
	err := chromedp.Do(ctx,
		chromedp.EmulateViewport(360, 740),
		inEnglish(ts.URL+"/#recipients"), // an old link, now the When screen
		chromedp.WaitVisible(`section#when`),
		chromedp.Click(byText(`//*[@role="group"][@aria-label="Who receives the notifications?"]`, "famille")),
		chromedp.Click(`details[data-chat="famille"] > summary`),
		chromedp.Click(byText(hoursOf, "🌙 Only at night")),
	)
	if err != nil {
		t.Fatal(err)
	}
	famille := func(f map[string]any) map[string]any {
		r, _ := f["recipients"].(map[string]any)
		c, _ := r["famille"].(map[string]any)
		return c
	}
	waitSaved(t, path, "famille only at night", func(f map[string]any) bool {
		return slices.Equal(hours(famille(f)["off_hours"]), []string{"07:00-22:00"})
	})
	err = chromedp.Do(ctx,
		chromedp.Click(byText(hoursOf, "Same as everyone")),
	)
	if err != nil {
		t.Fatal(err)
	}
	waitSaved(t, path, "famille without restriction", func(f map[string]any) bool { return famille(f) == nil })
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// With a single chat there is nobody to choose between: no recipients question.
func TestBrowserWhenSingleChat(t *testing.T) {
	yaml := strings.Replace(testConfig, "    famille: -100\n", "", 1)
	_, _, ts := setupConfig(t, yaml, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}}})
	ctx, jsErrors := browser(t)
	var page string
	err := chromedp.Do(ctx,
		inEnglish(ts.URL+"/#when"),
		chromedp.WaitVisible(`section#when`),
		into(chromedp.Text(`#when`), &page),
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(page, "Who receives the notifications?") {
		t.Errorf("the recipients question shows with one chat: %q", page)
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
		chromedp.WaitVisible(`section#advanced`),
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
		chromedp.WaitVisible(`section#advanced`),
		// Time ranges: two quiet ranges and one off range; the first one starts earlier, the second is removed.
		chromedp.Click(`[data-key="quiet_hours"] button.btn-link`),
		chromedp.Click(`[data-key="quiet_hours"] button.btn-link`),
		chromedp.Click(`[data-key="off_hours"] button.btn-link`),
		into(chromedp.Evaluate[string](times), &names),
		chromedp.Evaluate[chromedp.Void](`var i = document.querySelector('[data-key="quiet_hours"] input[type=time]'); i.value = "21:30"; i.dispatchEvent(new Event("change", { bubbles: true })); 0`),
		chromedp.Click(`[data-key="quiet_hours"] button[aria-label="Quiet hours 2 – Remove"]`),
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
	// A range that starts where it ends is flagged where it is and not sent; fixed, it is.
	quietTo := `document.querySelector('[data-key="quiet_hours"] input[aria-label="Quiet hours 1 – to"]')`
	setQuietTo := func(v string) chromedp.Action[chromedp.Void] {
		return chromedp.Evaluate[chromedp.Void](`{ const i = ` + quietTo + `; i.value = "` + v + `"; i.dispatchEvent(new Event("change", { bubbles: true })); } 0`)
	}
	var rangeInvalid, rangeError, rangeStatus string
	err = chromedp.Do(ctx,
		setQuietTo("21:30"),
		chromedp.WaitVisible(`[data-key="quiet_hours"] [role=alert]`),
		into(chromedp.Evaluate[string](quietTo+`.getAttribute("aria-invalid") || ""`), &rangeInvalid),
		into(chromedp.Text(`[data-key="quiet_hours"] [role=alert]`), &rangeError),
		into(chromedp.Evaluate[string](`document.getElementById("save-status").hidden ? "" : document.getElementById("save-status").textContent`), &rangeStatus),
	)
	if err != nil {
		t.Fatal(err)
	}
	if rangeInvalid != "true" || !strings.Contains(rangeError, "different") || strings.Contains(rangeStatus, "Not saved") {
		t.Errorf("range 21:30-21:30: aria-invalid %q, error %q, status %q", rangeInvalid, rangeError, rangeStatus)
	}
	if q := hours(notifyOf(savedFile(t, path))["quiet_hours"]); !slices.Equal(q, []string{"21:30-07:00"}) {
		t.Errorf("range 21:30-21:30 was saved: %v", q)
	}
	err = chromedp.Do(ctx,
		setQuietTo("06:00"),
		chromedp.WaitNotPresent(`[data-key="quiet_hours"] [role=alert]`),
	)
	if err != nil {
		t.Fatal(err)
	}
	waitSaved(t, path, "the quiet range fixed to 21:30-06:00", func(f map[string]any) bool {
		return slices.Equal(hours(notifyOf(f)["quiet_hours"]), []string{"21:30-06:00"})
	})

	// Minimum score: one value per object, then a value for the first object.
	score := `[data-key="min_score"]`
	err = chromedp.Do(ctx,
		chromedp.Evaluate[chromedp.Void](`var s = document.querySelector('[data-key="min_score"] select'); s.value = "label"; s.dispatchEvent(new Event("change", { bubbles: true })); 0`),
		chromedp.WaitVisible(score+` input[type=number][aria-label="🚗 Car"]`),
		into(chromedp.Evaluate[string](`document.querySelector('[data-key="min_score"] select').getAttribute("aria-label")`), &scoreSelect),
	)
	if err != nil {
		t.Fatal(err)
	}
	if scoreSelect != "One value per object" {
		t.Errorf("score mode select is named %q", scoreSelect)
	}
	// The new mode reaches the file before the value changes: each wait covers one save.
	waitSaved(t, path, "a first score for car", func(f map[string]any) bool {
		m, _ := notifyOf(f)["min_score"].(map[string]any)
		return m["car"] == 0.7
	})
	err = chromedp.Do(ctx,
		chromedp.Evaluate[chromedp.Void](`var n = document.querySelector('[data-key="min_score"] input[type=number][aria-label="🚗 Car"]'); n.value = "0.8"; n.dispatchEvent(new Event("change", { bubbles: true })); 0`),
	)
	if err != nil {
		t.Fatal(err)
	}
	waitSaved(t, path, "a score for car", func(f map[string]any) bool {
		m, _ := notifyOf(f)["min_score"].(map[string]any)
		return m["car"] == 0.8
	})

	// Chips: a value typed in the field and a toggled option.
	err = chromedp.Do(ctx,
		chromedp.SendKeys(`[data-key="labels"] input[aria-label="Objects – add…"]`, "chien"+kb.Enter),
		chromedp.Click(byText(`//*[@data-key="labels"]`, "🚗 Car")),
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
		chromedp.Click(`#reset`),
		chromedp.WaitVisible(`#confirm-cancel`),
		chromedp.Click(`#confirm-cancel`),
		chromedp.WaitNotVisible(`#confirm`),
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

// The sites that may show the interface: an address that is not one is flagged and not
// saved; a valid one is saved as its origin.
func TestBrowserFrameSites(t *testing.T) {
	_, path, ts := setup(t, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}}})
	ctx, jsErrors := browser(t)
	field := `[data-key="frame_ancestors"] input[type=text]`
	var alert string
	err := chromedp.Do(ctx,
		inEnglish(ts.URL+"/#advanced"),
		chromedp.WaitVisible(field),
		chromedp.SendKeys(field, "homeassistant.local"+kb.Enter),
		chromedp.WaitVisible(`#frames-error`),
		into(chromedp.Text(`#frames-error`), &alert),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(alert, "homeassistant.local") || savedFile(t, path) != nil {
		t.Errorf("an invalid site: alert %q, file %v", alert, savedFile(t, path))
	}
	err = chromedp.Do(ctx,
		chromedp.SendKeys(field, "http://HomeAssistant.local:8123/lovelace/0"+kb.Enter),
		chromedp.WaitNotPresent(`#frames-error`),
	)
	if err != nil {
		t.Fatal(err)
	}
	waitSaved(t, path, "the site saved as its origin", func(f map[string]any) bool {
		return slices.Equal(strs(f["frame_ancestors"]), []string{"http://homeassistant.local:8123"})
	})
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
		chromedp.WaitVisible(`section#advanced`),
		chromedp.Evaluate[chromedp.Void](`var i = document.querySelector('[data-key="cooldown"] input'); i.focus(); i.select(); 0`),
		chromedp.SendKeys(cooldown, "90s"+kb.Tab),
		chromedp.WaitVisible(`#save-status.ok`),
		chromedp.SendKeys(`#external-url`, "frigate.local"+kb.Tab),
		chromedp.Click(`#reset`),
		chromedp.WaitVisible(`#confirm-ok`),
		chromedp.Click(`#confirm-ok`),
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
		chromedp.WaitVisible(`section#cameras`),
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
// is no longer "unchanged" once its address changes (it would not be kept). A saved
// time zone the browser does not list is shown and kept.
func TestBrowserSetupSavedPasswords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "connection.yml")
	current := config.Connection{
		Frigate:  config.Frigate{URL: "http://192.168.1.10:5000"},
		MQTT:     config.MQTT{Broker: "tcp://192.168.1.10:1883", Password: "mqtt-secret"},
		Telegram: config.Telegram{Token: "123:abc", Chats: map[string]int64{"Alice": 111}},
		Web:      config.ConnectionWeb{Password: "web-secret"},
		Timezone: "Etc/GMT+5",
	}
	saved := make(chan struct{})
	s := NewSetup(path, &current, &fakeProber{}, slog.New(slog.DiscardHandler), func() { close(saved) })
	mux := http.NewServeMux()
	s.MountAlone(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, jsErrors := browser(t)
	var before, after chromedp.AttributeResult
	var unchanged, zone string
	var webDisabled bool
	err := chromedp.Do(ctx,
		chromedp.Navigate(ts.URL+"/"),
		chromedp.WaitVisible(`#clear-web-pass-row`),
		into(chromedp.AttributeValue(`#mqtt-pass`, "placeholder"), &before),
		into(chromedp.Text(`#sec-options .ts-control .item`), &zone),
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
	if zone != "Etc/GMT+5" || c.Timezone != "Etc/GMT+5" {
		t.Errorf("time zone shown %q, saved %q", zone, c.Timezone)
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
	var heading, steps string
	var stepSize float64
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
		into(chromedp.Evaluate[string](`[...document.querySelectorAll("[data-step]")].map(e => e.textContent.trim()).join(",")`), &steps),
		into(chromedp.Evaluate[float64](`parseFloat(getComputedStyle(document.querySelector("#sec-telegram [data-step]")).fontSize)`), &stepSize),
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
	if steps != "1,2,3" {
		t.Errorf("the step numbers are %q, want \"1,2,3\"", steps)
	}
	if stepSize < 15 {
		t.Errorf("the step number is %.1f px on a 360 px screen, want at least 15 px", stepSize)
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
		{Name: "entree"}, {Name: "hall"}, {Name: "kitchen"}, {Name: "porch"}, {Name: "yard"}, // below garage: the page is long enough to scroll to it
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
		chromedp.WaitVisible(`section#cameras`),
		chromedp.WaitVisible(garage+` summary`),
		chromedp.Poll[chromedp.Void](`document.querySelector('[data-camera="garage"]')?.open`),
		// A followed link leaves the focus on the camera's summary, with no control nested in it.
		chromedp.Poll[chromedp.Void](`document.activeElement.tagName === "SUMMARY"`),
		into(chromedp.Evaluate[bool](`document.activeElement.parentElement.dataset.camera === "garage"`), &focused),
		// The page scrolled to the card, and did not leave it under the sticky header.
		chromedp.Poll[chromedp.Void](`scrollY > 0 && document.querySelector('[data-camera="garage"]').getBoundingClientRect().top >= document.querySelector("header").getBoundingClientRect().bottom`),
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
	// Text that is not a duration is dropped with the customization: the field shows the inherited value again, unflagged.
	const field = `document.querySelector('[data-camera="garage"] [data-key="cooldown"] input[type=text]')`
	var inherited, shown, flag string
	err = chromedp.Do(ctx,
		into(chromedp.Evaluate[string](field+`.value`), &inherited),
		chromedp.SendKeys(garage+` [data-key="cooldown"] input[type=text]`, "abc"+kb.Tab),
		chromedp.Poll[chromedp.Void](field+`.getAttribute("aria-invalid") === "true"`),
		chromedp.Click(cooldown),
		chromedp.Poll[chromedp.Void](field+`.disabled`),
		into(chromedp.Evaluate[string](field+`.value`), &shown),
		into(chromedp.Evaluate[string](field+`.getAttribute("aria-invalid") || ""`), &flag),
	)
	if err != nil {
		t.Fatal(err)
	}
	if shown != inherited || flag != "" {
		t.Errorf("after unticking: the cooldown shows %q (aria-invalid %q), want the inherited %q unflagged", shown, flag, inherited)
	}
	waitCameras(t, path, "garage cooldown removed", func(c map[string]map[string]any) bool { _, ok := c["garage"]["cooldown"]; return !ok })

	err = chromedp.Do(ctx,
		chromedp.Click(old+` summary`),
		chromedp.Click(old+` details.more > summary`),
		chromedp.Click(byText(`//details[@data-camera="ancienne"]`, "Reset everything to default")),
		chromedp.WaitVisible(`#confirm-ok`),
		chromedp.Click(`#confirm-ok`),
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
// A camera's emoji is picked from the grid, saved, then removed.
func TestBrowserCameraEmoji(t *testing.T) {
	_, path, ts := setup(t, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}}})
	ctx, jsErrors := browser(t)
	card := `//details[@data-camera="garage"]`
	err := chromedp.Do(ctx,
		inEnglish(ts.URL+"/#cameras/garage"),
		chromedp.WaitVisible(`[data-camera="garage"] summary`),
		chromedp.Click(byText(card, "Choose an emoji")),
		chromedp.Click(chromedp.Search(card+`//*[contains(@class,"emoji-grid")]/button[normalize-space()="🚪"]`)),
	)
	if err != nil {
		t.Fatal(err)
	}
	waitCameras(t, path, "garage's emoji picked", func(c map[string]map[string]any) bool { return c["garage"]["emoji"] == "🚪" })
	var input string
	err = chromedp.Do(ctx,
		into(chromedp.Evaluate[string](`document.querySelector('[aria-label="Emoji of garage"]').value`), &input),
		chromedp.Click(byText(card, "Remove")),
	)
	if err != nil {
		t.Fatal(err)
	}
	if input != "🚪" {
		t.Errorf("field = %q, want the emoji picked", input)
	}
	waitCameras(t, path, "garage's emoji removed", func(c map[string]map[string]any) bool { _, ok := c["garage"]["emoji"]; return !ok })

	// Any emoji is found by its name.
	err = chromedp.Do(ctx,
		chromedp.Click(byText(card, "Choose an emoji")),
		chromedp.SendKeys(chromedp.Search(card+`//input[@type="search"]`), "deciduous"),
		chromedp.Click(chromedp.Search(card+`//*[contains(@class,"emoji-grid")]/button[@aria-label="deciduous tree"]`)),
	)
	if err != nil {
		t.Fatal(err)
	}
	waitCameras(t, path, "garage's emoji searched", func(c map[string]map[string]any) bool { return c["garage"]["emoji"] == "🌳" })
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

func TestBrowserCamerasLink(t *testing.T) {
	_, _, ts := setup(t, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}, {Name: "entree"}}})
	ctx, jsErrors := browser(t)
	var open, other bool
	err := chromedp.Do(ctx,
		inEnglish(ts.URL+"/"),
		chromedp.Click(`#cameras-list a[href="#cameras/entree"]`),
		chromedp.WaitVisible(`[data-camera="entree"] summary`),
		chromedp.Poll[chromedp.Void](`document.querySelector('[data-camera="entree"]')?.open`),
		into(chromedp.Evaluate[bool](`document.querySelector('[data-camera="entree"]').open`), &open),
		into(chromedp.Evaluate[bool](`document.querySelector('[data-camera="garage"]').open`), &other),
		// Changing the hash on the screen opens the other card too.
		chromedp.Evaluate[chromedp.Void](`location.hash = "#cameras/garage"; 0`),
		chromedp.Poll[chromedp.Void](`document.querySelector('[data-camera="garage"]')?.open`),
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
		chromedp.Poll[chromedp.Void](`document.querySelector('[data-camera="porte d\'entrée"]')?.open`),
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

// The activity filters: camera, outcome and hour; a row opens on its snapshot and, when
// Frigate gave one, its link.
func TestBrowserActivityFilters(t *testing.T) {
	photo, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "assets", "sample.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	entries := []notifier.HistoryEntry{
		{ID: "a1", At: now.Add(-10 * time.Minute), Camera: "garage", Label: "person", Sent: true, Link: "http://frigate.example/explore?event_id=a1", Snapshot: "/api/events/a1/snapshot.jpg"},
		{ID: "a2", At: now.Add(-20 * time.Minute), Camera: "entree", Label: "car", Reason: "label"},
		{ID: "a3", At: now.Add(-5*time.Hour - 30*time.Minute), Camera: "garage", Label: "dog", Sent: true},
	}
	media := fakeMedia{files: map[string][]byte{"/api/events/a1/snapshot.jpg": photo}}
	_, _, ts := setup(t, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}, {Name: "entree"}}}, WithHistory(fakeHistory{entries: entries}, media))

	ids := `[...document.querySelectorAll("#activity li[data-id]")].map(e => e.dataset.id).join(",")`
	// An hour from the address covers [hour, hour + 1 h).
	hour := now.Add(-6 * time.Hour).Unix()
	var all, garage, sent, hourRows, none, hash, foot, banner, empty, expanded, noLink string
	var banners int
	var link struct{ Href, Target, Rel string }
	var width int
	ctx, jsErrors := browser(t)
	err = chromedp.Do(ctx,
		chromedp.EmulateViewport(360, 740),
		inEnglish(ts.URL+"/#activity"),
		chromedp.WaitVisible(`#activity li[data-id="a1"]`),
		into(chromedp.Evaluate[string](ids), &all),
		chromedp.SetValue(`#activity-camera`, "garage"),
		chromedp.Poll[chromedp.Void](ids+` == "a1,a3"`),
		into(chromedp.Evaluate[string](ids), &garage),
		chromedp.SetValue(`#activity-camera`, ""),
		chromedp.Click(byText(`//*[@id="activity-outcome"]`, "Ignored")),
		chromedp.Poll[chromedp.Void](ids+` == "a2"`),
		chromedp.Click(byText(`//*[@id="activity-outcome"]`, "Sent")),
		chromedp.Poll[chromedp.Void](ids+` == "a1,a3"`),
		into(chromedp.Evaluate[string](ids), &sent),
		into(chromedp.Text(`#activity-foot`), &foot),
		// A row is a button: it opens on the snapshot and the link.
		into(chromedp.Evaluate[string](`document.querySelector('#activity li[data-id="a1"] button[aria-expanded]').getAttribute("aria-expanded")`), &expanded),
		chromedp.Click(`#activity li[data-id="a1"] button[aria-expanded]`),
		chromedp.Poll[chromedp.Void](`(() => { const i = document.querySelector('#activity li[data-id="a1"] img.big'); return !!i && i.complete && i.naturalWidth > 0 && i.src.endsWith("/api/history/a1/snapshot") })()`),
		into(chromedp.Evaluate[struct{ Href, Target, Rel string }](`(() => { const a = document.querySelector('#activity li[data-id="a1"] a[href*="event_id=a1"]'); return { Href: a.href, Target: a.target, Rel: a.rel } })()`), &link),
		chromedp.Click(`#activity li[data-id="a3"] button[aria-expanded]`),
		chromedp.Poll[chromedp.Void](`document.querySelector('#activity li[data-id="a3"] button[aria-expanded]')?.getAttribute("aria-expanded") == "true"`),
		into(chromedp.Evaluate[string](`String(document.querySelectorAll('#activity li[data-id="a3"] a[target=_blank]').length)`), &noLink),
		into(chromedp.Evaluate[int](`document.documentElement.scrollWidth`), &width),
		// An hour from the address.
		chromedp.Click(byText(`//*[@id="activity-outcome"]`, "All")),
		chromedp.Evaluate[chromedp.Void](`location.hash = "#activity/`+strconv.FormatInt(hour, 10)+`"; 0`),
		chromedp.WaitVisible(`#activity-hour`),
		chromedp.Poll[chromedp.Void](ids+` == "a3"`),
		into(chromedp.Evaluate[string](ids), &hourRows),
		into(chromedp.Text(`#activity-hour`), &banner),
		chromedp.Evaluate[chromedp.Void](`location.hash = "#activity/`+strconv.FormatInt(now.Add(-12*time.Hour).Unix(), 10)+`"; 0`),
		chromedp.Poll[chromedp.Void](`document.querySelector("#activity-list")?.textContent.includes("No detection in this hour.")`),
		into(chromedp.Evaluate[string](ids), &none),
		into(chromedp.Text(`#activity-list`), &empty),
		chromedp.Click(`#activity-hour button`),
		chromedp.Poll[chromedp.Void](ids+` == "a1,a2,a3"`),
		into(chromedp.Evaluate[string](`location.hash`), &hash),
		into(chromedp.Evaluate[int](`document.querySelectorAll("#activity-hour").length`), &banners),
	)
	if err != nil {
		t.Fatal(err)
	}
	if all != "a1,a2,a3" || garage != "a1,a3" || sent != "a1,a3" || hourRows != "a3" || none != "" {
		t.Errorf("rows: all %q, garage %q, sent %q, hour %q, empty hour %q", all, garage, sent, hourRows, none)
	}
	if !strings.Contains(foot, "2 shown: 2 sent, 0 ignored.") {
		t.Errorf("foot = %q, want the count of the filtered list", foot)
	}
	if expanded != "false" || link.Target != "_blank" || link.Rel != "noopener" || !strings.HasPrefix(link.Href, "http://frigate.example/explore?event_id=a1") || noLink != "0" {
		t.Errorf("expansion: collapsed aria-expanded %q, link %+v, links of the entry without one: %s", expanded, link, noLink)
	}
	if !strings.HasPrefix(banner, "Between ") || !strings.Contains(empty, "No detection in this hour.") {
		t.Errorf("banner = %q, empty = %q", banner, empty)
	}
	if hash != "#activity" || banners != 0 {
		t.Errorf("after Show all: hash %q, %d banners", hash, banners)
	}
	if width > 360 {
		t.Errorf("the page is %d px wide on a 360 px screen", width)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// Once the page is scrolled, the navigation bar shrinks; back at the top, it grows again.
func TestBrowserNavShrinksOnScroll(t *testing.T) {
	_, _, ts := setup(t, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}}})
	ctx, jsErrors := browser(t)
	height := `document.getElementById("nav").getBoundingClientRect().height`
	var top float64
	err := chromedp.Do(ctx,
		chromedp.EmulateViewport(1280, 500),
		inEnglish(ts.URL+"/#advanced"),
		chromedp.WaitVisible(`section#advanced`),
		into(chromedp.Evaluate[float64](height), &top),
	)
	if err != nil {
		t.Fatal(err)
	}
	h := strconv.FormatFloat(top, 'f', -1, 64)
	err = chromedp.Do(ctx,
		chromedp.Evaluate[chromedp.Void](`window.scrollTo(0, 400); 0`),
		chromedp.Poll[chromedp.Void](height+` < `+h+` - 4`),
		chromedp.Evaluate[chromedp.Void](`window.scrollTo(0, 0); 0`),
		chromedp.Poll[chromedp.Void](height+` == `+h),
	)
	if err != nil {
		t.Errorf("the bar (%s px at the top) does not shrink on scroll and grow back: %v", h, err)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// The page has no link to /classic, and the old address leads to the interface.
func TestBrowserNoClassicLinks(t *testing.T) {
	_, _, ts := setup(t, "", fakeCameras{})
	ctx, jsErrors := browser(t)
	var links int
	var where string
	err := chromedp.Do(ctx,
		inEnglish(ts.URL+"/"),
		chromedp.WaitVisible(`#nav`),
		chromedp.WaitVisible(`#cameras-list`), // the footer renders once the data is loaded
		into(chromedp.Evaluate[int](`document.querySelectorAll('a[href^="classic"]').length`), &links),
		inEnglish(ts.URL+"/classic"),
		chromedp.WaitVisible(`#cameras-list`),
		into(chromedp.Evaluate[string](`location.pathname`), &where),
	)
	if err != nil {
		t.Fatal(err)
	}
	if links != 0 {
		t.Errorf("classic links in the page = %d, want none", links)
	}
	if where != "/" {
		t.Errorf("/classic led to %q, want /", where)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// A failing connection shows its detail and hint, a refused user is listed with
// how to let them in, and the day's summary says something needs attention.
func TestBrowserOverviewProblems(t *testing.T) {
	health := func(context.Context, i18n.Lang) []Component {
		return []Component{
			{Name: "Frigate", State: StateOK, Detail: "0.16"},
			{Name: "MQTT", State: StateError, Detail: "refused", Hint: "check"},
		}
	}
	_, _, ts := setup(t, "", fakeCameras{}, WithHealth(health),
		WithRefused(fakeRefusals{{ID: 999, Name: "Alice", At: time.Now()}}))
	ctx, jsErrors := browser(t)
	var connections, today string
	err := chromedp.Do(ctx,
		inEnglish(ts.URL+"/"),
		chromedp.WaitVisible(`#connections`),
		into(chromedp.Text(`#connections`), &connections),
		into(chromedp.Text(`#today`), &today),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"MQTT — refused", "👉 check", "Alice", "999",
		"To allow someone, add their ID to telegram.chats in config.yml"} {
		if !strings.Contains(connections, want) {
			t.Errorf("connections = %q, want it to contain %q", connections, want)
		}
	}
	if !strings.Contains(today, "Something needs attention") {
		t.Errorf("today = %q, want %q", today, "Something needs attention")
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// The login page's language links keep the deep link's fragment too.
func TestBrowserLoginLanguageKeepsRoute(t *testing.T) {
	_, _, ts := setup(t, "s3cret", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}}})
	ctx, jsErrors := browser(t)
	var links string
	err := chromedp.Do(ctx,
		chromedp.Navigate(ts.URL+"/#cameras/garage"),
		chromedp.WaitVisible(`.login-card nav a`),
		into(chromedp.Evaluate[string](`[...document.querySelectorAll("nav a")].map(a => new URL(a.href).hash).join("|")`), &links),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range strings.Split(links, "|") {
		if h != "#cameras/garage" {
			t.Errorf("language links carry the fragments %q, want #cameras/garage on each", links)
			break
		}
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// Logging out at once after a change saves it first.
func TestBrowserLogoutSavesPending(t *testing.T) {
	_, path, ts := setup(t, "s3cret", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage", Labels: []string{"car", "person"}}}})
	ctx, jsErrors := browser(t)
	err := chromedp.Do(ctx,
		inEnglish(ts.URL+"/#when"),
		chromedp.WaitVisible(`.login-card input[type=password]`),
		chromedp.SendKeys(`#password`, "s3cret"),
		chromedp.Click(`.login-card button[type=submit]`),
		chromedp.WaitVisible(`section#when`),
		chromedp.Click(byText("", "🚗 Car")), // "person" is already chosen
		chromedp.Click(`#logout`),           // well before the 600 ms delay
		chromedp.WaitVisible(`.login-card`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if f := savedFile(t, path); !slices.Equal(strs(notifyOf(f)["labels"]), []string{"car", "person"}) {
		t.Errorf("the change was not saved before the session ended: %v", f)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// Controls that look alike are told apart: the Test links, the hints of the addresses,
// and the Remove buttons of the ranges and of the scores.
func TestBrowserAdvancedNames(t *testing.T) {
	cams := fakeCameras{cams: []frigate.CameraInfo{{Name: "garage", Labels: []string{"person", "car"}}}}
	_, _, ts := setup(t, "", cams)
	ctx, jsErrors := browser(t)
	var links, describedBy, removes string
	err := chromedp.Do(ctx,
		inEnglish(ts.URL+"/#advanced"),
		chromedp.WaitVisible(`section#advanced`),
		chromedp.SendKeys(`#external-url`, "https://frigate.example.org"+kb.Tab),
		chromedp.SendKeys(`#ha-url`, "https://ha.example.org"+kb.Tab),
		chromedp.WaitVisible(`[data-key="home_assistant_url"] a`),
		chromedp.Click(`[data-key="quiet_hours"] button.btn-link`),
		chromedp.Click(`[data-key="off_hours"] button.btn-link`),
		chromedp.Evaluate[chromedp.Void](`var s = document.querySelector('[data-key="min_score"] select'); s.value = "label"; s.dispatchEvent(new Event("change", { bubbles: true })); 0`),
		chromedp.WaitVisible(`[data-key="min_score"] button.chip`),
		into(chromedp.Evaluate[string](`[...document.querySelectorAll('#advanced a[target=_blank]')].map(a => a.getAttribute("aria-label")).join("|")`), &links),
		into(chromedp.Evaluate[string](`document.getElementById("external-url").getAttribute("aria-describedby").split(" ").map(id => document.getElementById(id)?.textContent).join("|")`), &describedBy),
		into(chromedp.Evaluate[string](`[...document.querySelectorAll('#advanced button.chip[title=Remove]')].map(b => b.getAttribute("aria-label")).join("|")`), &removes),
	)
	if err != nil {
		t.Fatal(err)
	}
	if want := "“Open in Frigate” link – Test ↗|“Home Assistant” button – Test ↗"; links != want {
		t.Errorf("Test links are named %q, want %q", links, want)
	}
	if !strings.Contains(describedBy, "Empty: the address the service reaches Frigate at") {
		t.Errorf("the Frigate address is described by %q, want its hint", describedBy)
	}
	if want := "Minimum score – 🚗 Car – Remove|Quiet hours 1 – Remove|Off hours 1 – Remove"; removes != want {
		t.Errorf("Remove buttons are named %q, want %q", removes, want)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// inLang is inEnglish for another language of the menu.
func inLang(url, lang string) chromedp.Action[chromedp.Void] {
	return chromedp.Steps(
		chromedp.Navigate(url),
		chromedp.Evaluate[chromedp.Void](`localStorage.setItem("frigate-telegram-enhanced-lang", "`+lang+`"); 0`),
		chromedp.Reload(),
	)
}

// The activity's hours are the chart's: a notification counts at the time it was
// sent, arriving from a bar lists what the bar counted (the sent ones first), and an
// empty list says why. The same hours can be picked in a select, which a phone can use
// where the bars are thin.
func TestBrowserActivityHours(t *testing.T) {
	now := time.Now()
	// Sent at the start of the current clock hour (no time zone in the test config: UTC),
	// so that it is in the menu's current hour whatever the minute the test runs at.
	hour := hourOf(now, time.UTC)
	entries := []notifier.HistoryEntry{
		{ID: "recent", At: hour.Add(-time.Minute), SentAt: hour, Camera: "garage", Label: "person", Sent: true},
		// Started before the hour that began six hours ago, sent in it: that bar counts it.
		{ID: "late", At: now.Add(-6*time.Hour - 2*time.Minute), SentAt: now.Add(-5*time.Hour - 50*time.Minute), Camera: "garage", Label: "person", Sent: true},
		{ID: "quiet", At: now.Add(-5*time.Hour - 40*time.Minute), Camera: "entree", Label: "car", Reason: "label"},
		{ID: "alone", At: now.Add(-4*time.Hour - 30*time.Minute), Camera: "entree", Label: "dog", Reason: "label"},
	}
	_, _, ts := setup(t, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}, {Name: "entree"}}},
		WithHistory(fakeHistory{entries: entries}, nil), WithActivity(fakeActivity{hour, now.Add(-5*time.Hour - 50*time.Minute)}))

	ids := `[...document.querySelectorAll("#activity li[data-id]")].map(e => e.dataset.id).join(",")`
	at := func(h time.Duration) string {
		return `location.hash = "#activity/` + strconv.FormatInt(now.Add(h).Unix(), 10) + `"; 0`
	}
	var fromBar, withIgnored, filtered, none, label string
	var group struct{ Role, Label string }
	var options []string
	var picked struct{ Hash, Value string }
	ctx, jsErrors := browser(t)
	err := chromedp.Do(ctx,
		inEnglish(ts.URL+"/#activity"),
		chromedp.WaitVisible(`#activity li[data-id="recent"]`),
		into(chromedp.Evaluate[struct{ Role, Label string }](`(() => { const g = document.querySelector("#activity-outcome"); return { Role: g.getAttribute("role"), Label: g.getAttribute("aria-label") } })()`), &group),
		into(chromedp.Evaluate[[]string](`[...document.querySelectorAll("#activity-hour-pick option")].map(o => o.textContent.trim())`), &options),
		// The select leads to the same address as a bar.
		chromedp.Evaluate[chromedp.Void](`{ const s = document.querySelector("#activity-hour-pick"); s.value = s.options[1].value; s.dispatchEvent(new Event("change", { bubbles: true })); } 0`),
		chromedp.Poll[chromedp.Void](ids+` == "recent"`),
		into(chromedp.Evaluate[struct{ Hash, Value string }](`({ Hash: location.hash, Value: document.querySelector("#activity-hour-pick").value })`), &picked),
		// A bar: the sent ones, then everything of the hour.
		chromedp.Evaluate[chromedp.Void](at(-6*time.Hour)),
		chromedp.Poll[chromedp.Void](ids+` == "late"`),
		into(chromedp.Evaluate[string](ids), &fromBar),
		chromedp.Click(byText(`//*[@id="activity-outcome"]`, "All")),
		chromedp.Poll[chromedp.Void](ids+` == "late,quiet"`),
		into(chromedp.Evaluate[string](ids), &withIgnored),
		// An hour with only an ignored detection, seen as sent only.
		chromedp.Evaluate[chromedp.Void](at(-5*time.Hour)),
		chromedp.Poll[chromedp.Void](`document.querySelector("#activity-list")?.textContent.includes("No detection matches these filters.")`),
		into(chromedp.Evaluate[string](ids), &filtered),
		chromedp.Evaluate[chromedp.Void](at(-12*time.Hour)),
		chromedp.Poll[chromedp.Void](`document.querySelector("#activity-list")?.textContent.includes("No detection in this hour.")`),
		into(chromedp.Evaluate[string](ids), &none),
		into(chromedp.Evaluate[string](`document.querySelector("#activity-hour-pick").getAttribute("aria-label")`), &label),
	)
	if err != nil {
		t.Fatal(err)
	}
	if fromBar != "late" || withIgnored != "late,quiet" || filtered != "" || none != "" {
		t.Errorf("rows: from a bar %q, all outcomes %q, filtered %q, empty hour %q", fromBar, withIgnored, filtered, none)
	}
	if group.Role != "group" || group.Label != "Outcome" {
		t.Errorf("outcome pills = %+v, want a group named Outcome", group)
	}
	if len(options) != 25 || options[0] != "All hours" || !regexp.MustCompile(`^\d\d:\d\d – \d\d:\d\d$`).MatchString(options[1]) || label != "Hour" {
		t.Errorf("hour select: label %q, options %q", label, options)
	}
	if !regexp.MustCompile(`^#activity/\d+$`).MatchString(picked.Hash) || picked.Value != strings.TrimPrefix(picked.Hash, "#activity/") {
		t.Errorf("after picking an hour: %+v", picked)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// An hour older than the 50 detections kept says so, instead of "no detection".
func TestBrowserActivityOlderHour(t *testing.T) {
	now := time.Now()
	var entries []notifier.HistoryEntry
	for i := range 50 {
		entries = append(entries, notifier.HistoryEntry{ID: "n" + strconv.Itoa(i), At: now.Add(-time.Duration(i+1) * time.Minute), Camera: "garage", Label: "dog", Sent: true})
	}
	_, _, ts := setup(t, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}}}, WithHistory(fakeHistory{entries: entries}, nil))
	var list string
	ctx, jsErrors := browser(t)
	err := chromedp.Do(ctx,
		inEnglish(ts.URL+"/#activity/"+strconv.FormatInt(now.Add(-10*time.Hour).Unix(), 10)),
		chromedp.Poll[chromedp.Void](`document.querySelector("#activity-list")?.textContent.includes("older than the last 50 detections")`),
		into(chromedp.Text(`#activity-list`), &list),
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(list, "No detection in this hour.") {
		t.Errorf("list = %q", list)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// Two lines with the same event id (and an id with a space) keep their own state and
// give valid, unique ids to the parts they control.
func TestBrowserActivityRowKeys(t *testing.T) {
	now := time.Now()
	entries := []notifier.HistoryEntry{
		{ID: "dup", At: now.Add(-10 * time.Minute), Camera: "garage", Label: "person", Sent: true},
		{ID: "dup", At: now.Add(-20 * time.Minute), Camera: "garage", Label: "person", Sent: true},
		{ID: "a b", At: now.Add(-30 * time.Minute), Camera: "garage", Label: "dog", Sent: true},
	}
	_, _, ts := setup(t, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}}}, WithHistory(fakeHistory{entries: entries}, nil))
	const buttons = `[...document.querySelectorAll("#activity li button[aria-expanded]")]`
	type view struct {
		Expanded []string `json:"expanded"`
		Controls []string `json:"controls"`
		Unique   bool     `json:"unique"`
		Found    []bool   `json:"found"`
	}
	var state view
	ctx, jsErrors := browser(t)
	err := chromedp.Do(ctx,
		inEnglish(ts.URL+"/#activity"),
		chromedp.WaitVisible(`#activity li`),
		chromedp.Evaluate[chromedp.Void](buttons+`[0].click(); 0`),
		chromedp.Poll[chromedp.Void](buttons+`[0].getAttribute("aria-expanded") == "true"`),
		chromedp.Evaluate[chromedp.Void](buttons+`[2].click(); 0`),
		chromedp.Poll[chromedp.Void](buttons+`[2].getAttribute("aria-expanded") == "true"`),
		into(chromedp.Evaluate[view](`(() => { const b = `+buttons+`; const ids = [...document.querySelectorAll("[id]")].map(e => e.id);
			return { expanded: b.map(x => x.getAttribute("aria-expanded")), controls: b.map(x => x.getAttribute("aria-controls")),
				unique: new Set(ids).size == ids.length, found: b.map(x => !!document.getElementById(x.getAttribute("aria-controls"))) } })()`), &state),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(state.Expanded, []string{"true", "false", "true"}) {
		t.Errorf("aria-expanded = %v, want only the lines opened", state.Expanded)
	}
	if !state.Unique || !slices.Equal(state.Found, []bool{true, false, true}) {
		t.Errorf("ids unique: %v, controlled part found: %v", state.Unique, state.Found)
	}
	for _, c := range state.Controls {
		if c == "" || strings.ContainsAny(c, " \t") {
			t.Errorf("aria-controls = %q, want one valid id", c)
		}
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// An opened line shows the snapshot, else the thumbnail when the snapshot is lost,
// else the emoji of its label: no broken image.
func TestBrowserActivityImageFallbacks(t *testing.T) {
	photo, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "assets", "sample.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	entries := []notifier.HistoryEntry{
		{ID: "thumb", At: now.Add(-10 * time.Minute), Camera: "garage", Label: "person", Sent: true, Thumb: "/api/events/thumb/thumbnail.jpg", Snapshot: "/api/events/thumb/snapshot.jpg"},
		{ID: "none", At: now.Add(-20 * time.Minute), Camera: "garage", Label: "dog", Sent: true},
	}
	media := fakeMedia{files: map[string][]byte{"/api/events/thumb/thumbnail.jpg": photo}} // the snapshot is lost
	_, _, ts := setup(t, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}}}, WithHistory(fakeHistory{entries: entries}, media))
	var big string
	var imgs int
	ctx, jsErrors := browser(t)
	err = chromedp.Do(ctx,
		inEnglish(ts.URL+"/#activity"),
		chromedp.WaitVisible(`#activity li[data-id="thumb"]`),
		chromedp.Click(`#activity li[data-id="thumb"] button[aria-expanded]`),
		chromedp.Poll[chromedp.Void](`(() => { const i = document.querySelector('#activity li[data-id="thumb"] img.big'); return !!i && i.complete && i.naturalWidth > 0 && i.src.endsWith("/api/history/thumb/thumb") })()`),
		into(chromedp.Evaluate[string](`document.querySelector('#activity li[data-id="thumb"] img.big').src`), &big),
		chromedp.Click(`#activity li[data-id="none"] button[aria-expanded]`),
		chromedp.Poll[chromedp.Void](`document.querySelector('#activity li[data-id="none"] button[aria-expanded]')?.getAttribute("aria-expanded") == "true"`),
		into(chromedp.Evaluate[int](`document.querySelectorAll('#activity li[data-id="none"] img').length`), &imgs),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(big, "/api/history/thumb/thumb") || imgs != 0 {
		t.Errorf("fallback image %q, images of the line without any: %d", big, imgs)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// The hour select and the filters fit a phone, in German too.
func TestBrowserActivityFitsAPhone(t *testing.T) {
	now := time.Now()
	entries := []notifier.HistoryEntry{
		{ID: "a", At: now.Add(-10 * time.Minute), SentAt: now.Add(-9 * time.Minute), Camera: "garage", Label: "person", Sent: true},
		{ID: "b", At: now.Add(-20 * time.Minute), Camera: "entree", Label: "car", Reason: "label"},
	}
	_, _, ts := setup(t, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}, {Name: "entree"}}},
		WithHistory(fakeHistory{entries: entries}, nil), WithActivity(fakeActivity{now.Add(-9 * time.Minute)}))
	var width int
	var first string
	ctx, jsErrors := browser(t)
	err := chromedp.Do(ctx,
		chromedp.EmulateViewport(360, 740),
		inLang(ts.URL+"/#activity", "de"),
		chromedp.WaitVisible(`#activity-hour-pick`),
		into(chromedp.Evaluate[string](`document.querySelector("#activity-hour-pick option").textContent`), &first),
		into(chromedp.Evaluate[int](`document.documentElement.scrollWidth`), &width),
	)
	if err != nil {
		t.Fatal(err)
	}
	if first != "Alle Stunden" || width > 360 {
		t.Errorf("first option %q, page %d px wide on a 360 px screen", first, width)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// background is the page's background colour as the sum of its red, green and blue.
const background = `getComputedStyle(document.body).backgroundColor.match(/\d+/g).slice(0, 3).reduce((a, n) => a + Number(n), 0)`

// "Auto" follows the system's colour scheme, a forced theme ignores it, and the setup
// page follows the saved choice as the interface does.
func TestBrowserThemeFollowsTheSystem(t *testing.T) {
	_, _, ts := setup(t, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}}})
	s := NewSetup(filepath.Join(t.TempDir(), "connection.yml"), nil, &fakeProber{}, slog.New(slog.DiscardHandler), func() {})
	mux := http.NewServeMux()
	s.MountAlone(mux)
	setupPage := httptest.NewServer(mux)
	defer setupPage.Close()

	system := func(scheme string) chromedp.Action[chromedp.Void] {
		return chromedp.Func(func(ctx context.Context, tg *chromedp.Target) error {
			_, err := cdp.Call(ctx, tg, emulation.SetEmulatedMedia, emulation.SetEmulatedMediaParams{Features: []*emulation.MediaFeature{{Name: "prefers-color-scheme", Value: scheme}}})
			return err
		})
	}
	choose := func(v string) chromedp.Action[chromedp.Void] {
		return chromedp.Evaluate[chromedp.Void](`{ const m = document.querySelectorAll("header select")[1]; m.value = "` + v + `"; m.dispatchEvent(new Event("change", { bubbles: true })); } 0`)
	}
	var autoLight, autoDark, forcedDark, forcedLight, setupDark, setupLight int
	ctx, jsErrors := browser(t)
	err := chromedp.Do(ctx,
		system("light"),
		chromedp.Navigate(ts.URL+"/"),
		chromedp.WaitVisible(`#cameras-list`),
		into(chromedp.Evaluate[int](background), &autoLight),
		system("dark"),
		into(chromedp.Evaluate[int](background), &autoDark),
		// A forced theme, whatever the system says.
		choose("dark"),
		system("light"),
		into(chromedp.Evaluate[int](background), &forcedDark),
		choose("light"),
		system("dark"),
		into(chromedp.Evaluate[int](background), &forcedLight),
		// The setup page, with the choice saved in its own storage.
		chromedp.Navigate(setupPage.URL+"/"),
		chromedp.Evaluate[chromedp.Void](`localStorage.setItem("frigate-telegram-enhanced-theme", "light"); 0`),
		chromedp.Reload(),
		chromedp.WaitVisible(`#intro-first`),
		into(chromedp.Evaluate[int](background), &setupLight),
		chromedp.Evaluate[chromedp.Void](`localStorage.setItem("frigate-telegram-enhanced-theme", "dark"); 0`),
		system("light"),
		chromedp.Reload(),
		chromedp.WaitVisible(`#intro-first`),
		into(chromedp.Evaluate[int](background), &setupDark),
	)
	if err != nil {
		t.Fatal(err)
	}
	const half = 3 * 128
	if autoLight < half || autoDark >= half {
		t.Errorf("Auto: background %d with a light system, %d with a dark one; want light then dark", autoLight, autoDark)
	}
	if forcedDark >= half || forcedLight < half {
		t.Errorf("forced: dark chosen on a light system %d, light chosen on a dark one %d", forcedDark, forcedLight)
	}
	if setupLight < half || setupDark >= half {
		t.Errorf("setup page: saved light on a dark system %d, saved dark on a light one %d", setupLight, setupDark)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// On a phone the screens fold into a list under a button, and the style's preview,
// below every setting, has a button bringing it up.
func TestBrowserPhoneMenu(t *testing.T) {
	_, _, ts := setup(t, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}}})
	ctx, jsErrors := browser(t)
	var folded, open, closed, button, seen bool
	err := chromedp.Do(ctx,
		chromedp.EmulateViewport(360, 740),
		inEnglish(ts.URL+"/#overview"),
		chromedp.WaitVisible(`section#overview`),
		into(chromedp.Evaluate[bool](`!document.querySelector('#nav a[href="#style"]').checkVisibility()`), &folded),
		chromedp.Click(`#nav button[aria-controls=screens]`),
		into(chromedp.Evaluate[bool](`document.querySelector('#nav a[href="#style"]').checkVisibility()`), &open),
		chromedp.Click(`#nav a[href="#style"]`),
		chromedp.WaitVisible(`section#style`),
		into(chromedp.Evaluate[bool](`!document.querySelector('#nav a[href="#style"]').checkVisibility()`), &closed),
		chromedp.Poll[chromedp.Void](`!!document.querySelector("#see-preview")`),
		into(chromedp.Evaluate[bool](`document.querySelector("#see-preview").checkVisibility()`), &button),
		chromedp.Click(`#see-preview`),
		chromedp.Poll[chromedp.Void](`!document.querySelector("#see-preview")`),
		into(chromedp.Evaluate[bool](`(() => { const r = document.querySelector("#live-preview").getBoundingClientRect(); return r.top < innerHeight && r.bottom > 0 })()`), &seen),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !folded || !open || !closed {
		t.Errorf("menu folded=%v, open after its button=%v, closed after a link=%v", folded, open, closed)
	}
	if !button || !seen {
		t.Errorf("preview button shown=%v, preview in view after it=%v", button, seen)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}

// The save status floats over the page, which doesn't move, and "Saved" fades after a
// moment.
func TestBrowserSaveStatusFloats(t *testing.T) {
	_, _, ts := setup(t, "", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}}})
	ctx, jsErrors := browser(t)
	var before, after float64
	var position string
	err := chromedp.Do(ctx,
		inEnglish(ts.URL+"/#overview"),
		chromedp.WaitVisible(`#cameras-list`),
		into(chromedp.Evaluate[float64](`document.querySelector("main").getBoundingClientRect().top`), &before),
		chromedp.Click(`#cameras-list [role=switch][aria-label="garage"]`),
		chromedp.WaitVisible(`#save-status.ok`),
		into(chromedp.Evaluate[float64](`document.querySelector("main").getBoundingClientRect().top`), &after),
		into(chromedp.Evaluate[string](`getComputedStyle(document.querySelector("#save-status")).position`), &position),
		chromedp.WaitNotVisible(`#save-status`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if before != after || position != "fixed" {
		t.Errorf("page moved from %v to %v, status position %q", before, after, position)
	}
	if errs := jsErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors: %q", errs)
	}
}
