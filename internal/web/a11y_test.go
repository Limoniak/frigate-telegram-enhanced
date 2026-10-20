//go:build browser

package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"frigate-telegram-enhanced/internal/frigate"
	"frigate-telegram-enhanced/internal/i18n"
	"frigate-telegram-enhanced/internal/notifier"
	"frigate-telegram-enhanced/internal/state"
)

// Every page, in both themes, passes axe-core's WCAG 2.1 A and AA rules: contrast,
// names of controls, landmarks, ARIA. Folded parts are opened first, so that what
// they hold is checked too.
func TestBrowserAccessibility(t *testing.T) {
	axe, err := os.ReadFile(filepath.Join("..", "..", "web", "node_modules", "axe-core", "axe.min.js"))
	if err != nil {
		t.Fatalf("axe-core is missing (npm ci in web/): %v", err)
	}
	now := time.Now()
	cams := fakeCameras{cams: []frigate.CameraInfo{
		{Name: "garage", Zones: []string{"parking"}, Labels: []string{"person", "car"}},
		{Name: "entree", Zones: []string{"porche"}, Labels: []string{"person", "car"}},
	}}
	history := fakeHistory{entries: []notifier.HistoryEntry{
		{ID: "a1", At: now.Add(-4 * time.Minute), Camera: "garage", Label: "person", Zones: []string{"parking"}, Score: 0.9, Sent: true, SentAt: now.Add(-4 * time.Minute)},
		{ID: "a2", At: now.Add(-30 * time.Minute), Camera: "entree", Label: "car", Score: 0.4, Reason: "score"},
	}}
	health := func(context.Context, i18n.Lang) []Component {
		return []Component{{Name: "Frigate", State: StateOK, Detail: "0.16"}, {Name: "MQTT", State: StateWarn, Detail: "reconnecting", Hint: "wait"}}
	}
	st, err := state.Load(filepath.Join(t.TempDir(), "state.json"), now)
	if err != nil {
		t.Fatal(err)
	}
	sp := NewSetup(filepath.Join(t.TempDir(), "connection.yml"), nil, &fakeProber{}, slog.New(slog.DiscardHandler), func() {})
	_, _, ts := setupConfig(t, testConfig, "", cams, WithHistory(history, nil), WithActivity(fakeActivity{now}),
		WithHealth(health), WithState(st), WithTester(&fakeTester{}), WithSetup(sp))
	_, _, locked := setupConfig(t, testConfig, "s3cret", cams)
	alone := http.NewServeMux()
	NewSetup(filepath.Join(t.TempDir(), "connection.yml"), nil, &fakeProber{}, slog.New(slog.DiscardHandler), func() {}).MountAlone(alone)
	first := httptest.NewServer(alone)
	defer first.Close()

	openAll := `document.querySelectorAll("details").forEach((d) => { d.open = true }); 0`
	pages := []struct{ name, url, ready, prepare string }{
		{"overview", ts.URL + "/#overview", `#cameras-list`, "0"},
		{"style", ts.URL + "/#style", `#styles`, "0"},
		{"when", ts.URL + "/#when", `section#when`, openAll},
		{"cameras", ts.URL + "/#cameras/garage", `[data-camera="garage"][open]`, openAll},
		{"activity", ts.URL + "/#activity", `#activity-list`, `document.querySelector("#activity li button")?.click(); 0`},
		{"advanced", ts.URL + "/#advanced", `section#advanced`, "0"},
		{"menu", ts.URL + "/#overview", `#cameras-list`, `document.querySelector("[aria-controls=screens]").click(); 0`},
		{"setup", first.URL + "/", `#intro-first`, openAll},
		{"login", locked.URL + "/", `.login-card input[type=password]`, "0"},
	}
	type violation struct {
		ID, Impact, Help string
		Nodes            []string
	}
	run := `axe.run(document, {runOnly: {type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]}}).then((r) =>
		JSON.stringify(r.violations.map((v) => ({ID: v.id, Impact: v.impact, Help: v.help,
			Nodes: v.nodes.slice(0, 4).map((n) => n.target.join(" ") + ": " + n.failureSummary.replace(/\s+/g, " "))}))))`
	for _, theme := range []string{"dark", "light"} {
		for _, p := range pages {
			width := int64(1280)
			if p.name == "menu" {
				width = 390 // the folded menu exists under lg only
			}
			// A browser per page: axe takes seconds, more than one browser's time budget for all.
			ctx, jsErrors := browser(t)
			var out string
			err := chromedp.Do(ctx,
				chromedp.EmulateViewport(width, 900),
				inEnglish(p.url),
				chromedp.WaitVisible(p.ready),
				chromedp.Evaluate[chromedp.Void](`localStorage.setItem("frigate-telegram-enhanced-theme", "`+theme+`"); document.documentElement.dataset.theme = "`+theme+`"; 0`),
				chromedp.Evaluate[chromedp.Void](p.prepare),
				chromedp.Sleep(700*time.Millisecond), // the transitions end: axe reads the final colors
				chromedp.Evaluate[chromedp.Void](string(axe)+"; 0"),
				into(chromedp.Evaluate[string](run, chromedp.EvalAwaitPromise), &out),
			)
			if err != nil {
				t.Fatalf("%s (%s): %v", p.name, theme, err)
			}
			var vs []violation
			if err := json.Unmarshal([]byte(out), &vs); err != nil {
				t.Fatal(err)
			}
			for _, v := range vs {
				t.Errorf("%s (%s): %s [%s] %s\n    %s", p.name, theme, v.ID, v.Impact, v.Help, strings.Join(v.Nodes, "\n    "))
			}
			if errs := jsErrors(); len(errs) > 0 {
				t.Errorf("%s (%s): JavaScript errors: %q", p.name, theme, errs)
			}
		}
	}
}
