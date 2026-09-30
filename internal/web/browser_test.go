//go:build browser

// Smoke test of the interface in a real browser (Chrome, driven by chromedp): the
// page loads without JavaScript errors, shows cameras, status and activity, and
// saves a setting. Run separately: go test -tags browser ./internal/web/
package web

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

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

	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.WindowSize(1280, 900))
	actx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()
	ctx, cancel := chromedp.NewContext(actx)
	defer cancel()
	ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var mu sync.Mutex
	var jsErrors []string
	chromedp.ListenTarget(ctx, func(ev any) {
		if e, ok := ev.(*runtime.EventExceptionThrown); ok {
			mu.Lock()
			jsErrors = append(jsErrors, e.ExceptionDetails.Error())
			mu.Unlock()
		}
	})

	var cameras, activity, health2, saveLabel string
	err := chromedp.Run(ctx,
		chromedp.Navigate(ts.URL+"/"),
		chromedp.WaitVisible(`#cameras .cam-name`),
		chromedp.Text(`#cameras`, &cameras),
		chromedp.WaitVisible(`#activity li, #activity > *`),
		chromedp.Text(`#activity`, &activity),
		chromedp.WaitVisible(`#health-errors`),
		chromedp.Text(`#health-errors`, &health2),
		// Muting a camera makes the page "modified", then saving writes the override.
		chromedp.Click(`details.camera[data-name="salon"] label.toggle`),
		chromedp.WaitEnabled(`#save`),
		chromedp.Click(`#save`),
		chromedp.WaitVisible(`#status.saved`),
		// The language selector comes from the catalogs; in French, the page is translated.
		chromedp.Click(`.lang button[data-lang="fr"]`),
		chromedp.Text(`#save`, &saveLabel),
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
	if saveLabel != "Enregistrer" {
		t.Errorf("button in French = %q, want Enregistrer", saveLabel)
	}
	saved, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(saved), "salon") {
		t.Errorf("override saved = %q, %v", saved, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(jsErrors) > 0 {
		t.Errorf("JavaScript errors: %q", jsErrors)
	}
}
