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

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"

	"frigate-telegram-enhanced/internal/config"
	"frigate-telegram-enhanced/internal/frigate"
	"frigate-telegram-enhanced/internal/i18n"
	"frigate-telegram-enhanced/internal/notifier"
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
	err := chromedp.Run(ctx,
		chromedp.Navigate(ts.URL+"/"),
		chromedp.WaitVisible(`#cameras .cam-name`),
		chromedp.Text(`#cameras`, &cameras),
		chromedp.WaitVisible(`#activity li, #activity > *`),
		chromedp.Text(`#activity`, &activity),
		chromedp.WaitVisible(`#health-errors`),
		chromedp.Text(`#health-errors`, &health2),
		// Muting a camera is saved by itself, shortly after.
		chromedp.Click(`details.camera[data-name="salon"] label.toggle`),
		chromedp.WaitVisible(`#status.saved`), // saved by itself, no button
		// The language selector comes from the catalogs; in French, the page is translated.
		chromedp.Click(`.lang button[data-lang="fr"]`),
		chromedp.Text(`#sec-when h2`, &title),
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
	chromedp.ListenTarget(ctx, func(ev any) {
		if e, ok := ev.(*runtime.EventExceptionThrown); ok {
			mu.Lock()
			errs = append(errs, e.ExceptionDetails.Error())
			mu.Unlock()
		}
	})
	return ctx, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(errs)
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
	err := chromedp.Run(ctx,
		chromedp.Navigate(ts.URL+"/"),
		chromedp.WaitVisible(`#intro-first`),
		chromedp.SendKeys(`#token`, "123:abc"),
		chromedp.Click(`#check-token`),
		chromedp.WaitVisible(`#chats .chip`),
		// An ID by hand: refused with a message if it is not a number, added on Enter.
		chromedp.Click(`#manual summary`),
		chromedp.SendKeys(`#manual-id`, "abc"+kb.Enter),
		chromedp.WaitVisible(`#manual-result.err`),
		chromedp.Evaluate(`document.getElementById("manual-id").value = ""`, nil),
		chromedp.SendKeys(`#manual-id`, "-100 123"),
		chromedp.SendKeys(`#manual-name`, "Family"+kb.Enter),
		chromedp.WaitVisible(`#manual-result.ok`),
		chromedp.Text(`#chats`, &chats),
		chromedp.SendKeys(`#frigate-url`, "http://192.168.1.10:5000"),
		chromedp.Click(`#check-frigate`),
		chromedp.WaitVisible(`#frigate-result.ok`),
		chromedp.Value(`#mqtt-broker`, &broker),
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
	err := chromedp.Run(ctx,
		chromedp.Navigate(ts.URL+"/"),
		chromedp.WaitVisible(`#frigate-result.ok`),
		chromedp.Value(`#frigate-url`, &url),
		chromedp.Value(`#mqtt-broker`, &broker),
		chromedp.WaitVisible(`#mqtt-pass`),
		chromedp.Text(`#broker-status`, &status),
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
	err := chromedp.Run(ctx,
		chromedp.Navigate(ts.URL+"/"),
		chromedp.WaitVisible(`#messages .notice.info`), // first visit: welcome
		chromedp.Evaluate(`!document.querySelector("#questions details.more").open`, &folded),
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

// With a password, a page of the service's own leads to its login page (never the
// browser's dialog), then back to the interface.
func TestBrowserLogin(t *testing.T) {
	_, _, ts := setup(t, "s3cret", fakeCameras{cams: []frigate.CameraInfo{{Name: "garage"}}})
	ctx, jsErrors := browser(t)
	var errText string
	err := chromedp.Run(ctx,
		chromedp.Navigate(ts.URL+"/"),
		chromedp.WaitVisible(`.login-card input[type=password]`),
		chromedp.SendKeys(`#password`, "wrong"),
		chromedp.Submit(`.login-card`),
		chromedp.WaitVisible(`.login-error`),
		chromedp.Text(`.login-error`, &errText),
		chromedp.SendKeys(`#password`, "s3cret"),
		chromedp.Submit(`.login-card`),
		chromedp.WaitVisible(`#cameras .cam-name`),
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
	var before, after, unchanged string
	var webDisabled bool
	err := chromedp.Run(ctx,
		chromedp.Navigate(ts.URL+"/"),
		chromedp.WaitVisible(`#clear-web-pass-row`),
		chromedp.AttributeValue(`#mqtt-pass`, "placeholder", &before, nil),
		chromedp.Evaluate(`T("unchanged")`, &unchanged), // in the browser's language
		chromedp.Evaluate(`document.getElementById("mqtt-broker").value = "192.168.1.99"; document.getElementById("mqtt-broker").dispatchEvent(new Event("input")); 0`, nil),
		chromedp.AttributeValue(`#mqtt-pass`, "placeholder", &after, nil),
		chromedp.Click(`#clear-web-pass`),
		chromedp.Evaluate(`document.getElementById("web-pass").disabled`, &webDisabled),
		chromedp.Evaluate(`document.getElementById("mqtt-broker").value = "192.168.1.10"; 0`, nil),
		chromedp.Click(`#save`),
		chromedp.WaitVisible(`#save-result.ok`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if before != unchanged || after == unchanged {
		t.Errorf("MQTT password placeholder: %q, then %q after changing the broker", before, after)
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
