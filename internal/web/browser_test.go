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
		chromedp.Text(`#chats`, &chats),
		chromedp.SendKeys(`#frigate-url`, "http://192.168.1.10:5000"),
		chromedp.Click(`#check-frigate`),
		chromedp.WaitVisible(`#frigate-result.ok`),
		chromedp.Value(`#mqtt-broker`, &broker),
		chromedp.Click(`#save`),
		chromedp.WaitVisible(`#save-result.ok`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(chats, "Alice") {
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
	if c.Telegram.Chats["Alice"] != 111 || c.Timezone == "" || c.Frigate.URL != "http://192.168.1.10:5000" {
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
