package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"frigate-telegram-enhanced/internal/bot"
	"frigate-telegram-enhanced/internal/config"
	"frigate-telegram-enhanced/internal/frigate"
	"frigate-telegram-enhanced/internal/i18n"
	"frigate-telegram-enhanced/internal/notifier"
	"frigate-telegram-enhanced/internal/state"
)

const testConfig = `
frigate:
  url: http://frigate:5000
mqtt:
  broker: tcp://mqtt:1883
telegram:
  token: abc
  admins: [42]
  chats:
    moi: 42
    famille: -100
notify:
  chats: [moi]
  labels: [person]
cameras:
  garage:
    min_score: 0.9
  salon: {}
`

type fakeCameras struct {
	cams []frigate.CameraInfo
	err  error
}

func (f fakeCameras) CameraDetails(context.Context) ([]frigate.CameraInfo, error) {
	return f.cams, f.err
}

// setup mounts the interface on a test server and returns the live configuration,
// the path of the override file and the server.
func setup(t *testing.T, password string, cams fakeCameras, opts ...Option) (*config.Config, string, *httptest.Server) {
	t.Helper()
	return setupConfig(t, testConfig, password, cams, opts...)
}

// setupConfig is setup with its own config.yml.
func setupConfig(t *testing.T, yaml, password string, cams fakeCameras, opts ...Option) (*config.Config, string, *httptest.Server) {
	t.Helper()
	cfg, err := config.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cfg.Web.Password = password
	path := filepath.Join(t.TempDir(), "notify.yml")
	h := New(cfg, path, cams, slog.New(slog.DiscardHandler), opts...)
	mux := http.NewServeMux()
	h.Mount(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return cfg, path, ts
}

func do(t *testing.T, ts *httptest.Server, method, path, body string, auth ...string) *http.Response {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, ts.URL+path, r)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", requestedWith)
	if len(auth) > 0 {
		req.SetBasicAuth("admin", auth[0])
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestGetSettingsDescribesConfigAndCameras(t *testing.T) {
	cams := fakeCameras{cams: []frigate.CameraInfo{
		{Name: "jardin", Zones: []string{"allee"}, Labels: []string{"person", "car"}},
	}}
	_, _, ts := setup(t, "", cams)

	resp := do(t, ts, "GET", "/api/settings", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var s settings
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	if len(s.Chats) != 2 || s.Chats[0] != "famille" {
		t.Errorf("chats = %v", s.Chats)
	}
	if len(s.Cameras) != 1 || s.Cameras[0].Zones[0] != "allee" {
		t.Errorf("cameras = %+v", s.Cameras)
	}
	if s.Custom {
		t.Error("no override file exists yet, custom should be false")
	}
	if s.Overlay.Notify.Labels == nil || (*s.Overlay.Notify.Labels)[0] != "person" {
		t.Errorf("overlay.notify = %+v", s.Overlay.Notify)
	}
	if got := s.Overlay.Cameras["garage"]; got.MinScore == nil || got.MinScore.Default != 0.9 {
		t.Errorf("garage must appear as an override: %+v", got)
	}
}

func TestGetSettingsWarnsWhenFrigateIsDown(t *testing.T) {
	_, _, ts := setup(t, "", fakeCameras{err: errors.New("connection refused")})

	var s settings
	if err := json.NewDecoder(do(t, ts, "GET", "/api/settings", "").Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	if s.Warning == "" {
		t.Error("want a warning")
	}
	// The cameras already set up are still offered, otherwise the interface would erase them.
	if len(s.Cameras) != 2 || s.Cameras[0].Name != "garage" || s.Cameras[1].Name != "salon" {
		t.Errorf("cameras = %+v", s.Cameras)
	}
}

func TestPutAppliesImmediatelyAndPersists(t *testing.T) {
	cfg, path, ts := setup(t, "", fakeCameras{})

	body := `{"notify":{"chats":["famille"],"labels":["car"],"cooldown":"30s"},
	          "cameras":{"jardin":{"min_score":{"default":0.5}}}}`
	resp := do(t, ts, "PUT", "/api/settings", body)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d: %s", resp.StatusCode, b)
	}

	if got := cfg.Global(); got.Cooldown != 30*time.Second || got.Chats[0] != "famille" {
		t.Errorf("the settings were not applied live: %+v", got)
	}
	if got := cfg.ForCamera("jardin").MinScore.Default; got != 0.5 {
		t.Errorf("jardin.min_score = %v", got)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the override file was not written: %v", err)
	}
	if !strings.Contains(string(raw), "cooldown: 30s") {
		t.Errorf("file written:\n%s", raw)
	}
}

func TestPutRejectsInvalidSettingsWithoutWriting(t *testing.T) {
	cfg, path, ts := setup(t, "", fakeCameras{})
	before := cfg.Global()

	resp := do(t, ts, "PUT", "/api/settings", `{"notify":{"chats":["inconnu"]}}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if !strings.Contains(body["error"], "inconnu") {
		t.Errorf("message = %q", body["error"])
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Error("nothing must be written when validation fails")
	}
	if cfg.Global().Chats[0] != before.Chats[0] {
		t.Error("the settings in force changed despite the refusal")
	}
}

func TestPutRejectsUnknownFields(t *testing.T) {
	_, _, ts := setup(t, "", fakeCameras{})
	resp := do(t, ts, "PUT", "/api/settings", `{"notify":{"coldown":"30s"}}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, a misspelled key must be reported", resp.StatusCode)
	}
}

func TestResetReturnsToConfigFile(t *testing.T) {
	cfg, path, ts := setup(t, "", fakeCameras{})

	if resp := do(t, ts, "PUT", "/api/settings", `{"notify":{"labels":["car"]},"cameras":{}}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT: status %d", resp.StatusCode)
	}
	if resp := do(t, ts, "POST", "/api/settings/reset", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("reset: status %d", resp.StatusCode)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Error("the override file should have been deleted")
	}
	if got := cfg.Global().Labels; len(got) != 1 || got[0] != "person" {
		t.Errorf("labels = %v, config.yml should have taken over again", got)
	}
	if got := cfg.ForCamera("garage").MinScore.Default; got != 0.9 {
		t.Errorf("garage.min_score = %v, the override from config.yml should have come back", got)
	}
}

func TestPasswordProtectsInterfaceButNotProbes(t *testing.T) {
	_, _, ts := setup(t, "s3cret", fakeCameras{})
	ts.Client().CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse } // the redirect itself is checked

	for _, tc := range []struct {
		name, method, path, body string
		auth                     []string
		want                     int
	}{
		{name: "without password", method: "GET", path: "/api/settings", want: http.StatusUnauthorized},
		{name: "wrong password", method: "GET", path: "/api/settings", auth: []string{"autre"}, want: http.StatusUnauthorized},
		{name: "page without password", method: "GET", path: "/", want: http.StatusUnauthorized},
		{name: "app script without password", method: "GET", path: "/app.js", want: http.StatusUnauthorized},
		{name: "app sheet without password", method: "GET", path: "/app.css", want: http.StatusUnauthorized},
		{name: "classic redirect without password", method: "GET", path: "/classic", want: http.StatusFound},
		{name: "write without password", method: "PUT", path: "/api/settings", body: "{}", want: http.StatusUnauthorized},
		{name: "snapshot without password", method: "GET", path: "/api/history/a/snapshot", want: http.StatusUnauthorized},
		{name: "right password", method: "GET", path: "/api/settings", auth: []string{"s3cret"}, want: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := do(t, ts, tc.method, tc.path, tc.body, tc.auth...)
			if resp.StatusCode != tc.want {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

// The interface is at /: its page loads its script, its sheet and the catalogs, and
// every file it references is served.
func TestNewInterfaceServed(t *testing.T) {
	_, _, ts := setup(t, "", fakeCameras{})
	resp := do(t, ts, "GET", "/", "")
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("status = %d, type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	page, _ := io.ReadAll(resp.Body)
	for _, ref := range []string{`src="i18n.js"`, `src="./app.js"`} {
		if !strings.Contains(string(page), ref) {
			t.Errorf("the page does not load %s", ref)
		}
	}
	refs := regexp.MustCompile(`(?:src|href)="(?:\./)?([a-z0-9][^":]*)"`).FindAllStringSubmatch(string(page), -1)
	if len(refs) < 3 {
		t.Fatalf("the page references too few files: %.400s", page)
	}
	for _, ref := range refs {
		if resp := do(t, ts, "GET", "/"+ref[1], ""); resp.StatusCode != http.StatusOK {
			t.Errorf("%s: %d", ref[1], resp.StatusCode)
		}
	}
}

// The classic interface is gone: /classic leads to the new one, its files are 404.
func TestClassicRedirects(t *testing.T) {
	_, _, ts := setup(t, "", fakeCameras{})
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path string) *http.Response {
		resp, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp
	}
	if resp := get("/classic"); resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "./" {
		t.Errorf("/classic: status %d, Location %q, want 302 to ./", resp.StatusCode, resp.Header.Get("Location"))
	}
	for _, path := range []string{"/ui.js", "/ui.css"} {
		if resp := get(path); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, resp.StatusCode)
		}
	}
	if resp := get("/i18n.js"); resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/javascript; charset=utf-8" {
		t.Errorf("/i18n.js: status %d, type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	cat, _ := io.ReadAll(do(t, ts, "GET", "/i18n.js", "").Body)
	for _, want := range []string{`const LANGUAGES = [{"code":"en","name":"English"},{"code":"de","name":"Deutsch"},{"code":"es","name":"Español"},{"code":"fr","name":"Français"},{"code":"it","name":"Italiano"},{"code":"pt","name":"Português"},{"code":"ru","name":"Русский"},{"code":"sl","name":"Slovenščina"}]`, `"Help":"Aide"`, `"person":{"emoji":"🚶","name":"Person"}`} {
		if !strings.Contains(string(cat), want) {
			t.Errorf("i18n.js does not contain %s", want)
		}
	}
	if resp := do(t, ts, "GET", "/web.go", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("/web.go: status %d, want 404", resp.StatusCode)
	}
}

func TestWritesRequireTheRequestedWithHeader(t *testing.T) {
	_, path, ts := setup(t, "", fakeCameras{})

	// A third-party page can send a simple POST without a CORS preflight; the header,
	// however, cannot be added without that check.
	for _, tc := range []struct{ method, path, body string }{
		{"PUT", "/api/settings", `{"notify":{"labels":["car"]}}`},
		{"POST", "/api/settings/reset", ""},
	} {
		req, err := http.NewRequest(tc.method, ts.URL+tc.path, strings.NewReader(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s: status = %d, want 403", tc.method, tc.path, resp.StatusCode)
		}
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Error("a refused write must leave nothing on disk")
	}
}

// Without a password, an unknown domain name in Host is the mark of DNS rebinding:
// the request is refused even if it carries X-Requested-With.
func TestHostCheckBlocksDNSRebinding(t *testing.T) {
	_, path, ts := setup(t, "", fakeCameras{})
	for _, tc := range []struct {
		host string
		want int
	}{
		{"evil.example.com", http.StatusForbidden},
		{"evil.example.com:8080", http.StatusForbidden},
		{"localhost:8080", http.StatusOK},
		{"192.168.1.10:8080", http.StatusOK},
		{"[::1]:8080", http.StatusOK},
	} {
		req, err := http.NewRequest("PUT", ts.URL+"/api/settings", strings.NewReader(`{"notify":{"labels":["car"]}}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = tc.host
		req.Header.Set("X-Requested-With", requestedWith)
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("Host %q: status = %d, want %d", tc.host, resp.StatusCode, tc.want)
		}
		if tc.want == http.StatusForbidden {
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("Host %q: a refused write must leave nothing on disk", tc.host)
			}
		}
	}
}

func TestHostAllowed(t *testing.T) {
	allowed := []string{"nas.lan"}
	for host, want := range map[string]bool{
		"NAS.lan:8080":       true,
		"nas.lan.":           true,
		"frigate.localhost":  true,
		"127.0.0.1":          true,
		"autre.lan":          false,
		"":                   false,
		"nas.lan.evil.com":   false,
		"localhost.evil.com": false,
	} {
		if got := hostAllowed(host, allowed); got != want {
			t.Errorf("hostAllowed(%q) = %v, want %v", host, got, want)
		}
	}
}

// Opening the interface then saving without touching anything must leave the
// effective settings identical: that is the round trip of the first save, the one
// that moves an installation from config.yml to the override file.
func TestSavingUntouchedSettingsChangesNothing(t *testing.T) {
	cfg, _, ts := setup(t, "", fakeCameras{})
	names := append(cfg.CameraNames(), "inconnue")
	before := map[string]config.Notify{"": cfg.Global()}
	for _, n := range names {
		before[n] = cfg.ForCamera(n)
	}

	var s settings
	if err := json.NewDecoder(do(t, ts, "GET", "/api/settings", "").Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	// The page removes the cameras that override nothing before sending.
	for name, patch := range s.Overlay.Cameras {
		if patch == (config.NotifyPatch{}) {
			delete(s.Overlay.Cameras, name)
		}
	}
	body, err := json.Marshal(s.Overlay)
	if err != nil {
		t.Fatal(err)
	}
	if resp := do(t, ts, "PUT", "/api/settings", string(body)); resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d: %s", resp.StatusCode, b)
	}

	if got := cfg.Global(); !reflect.DeepEqual(got, before[""]) {
		t.Errorf("global:\nbefore %+v\nafter %+v", before[""], got)
	}
	for _, n := range names {
		if got := cfg.ForCamera(n); !reflect.DeepEqual(got, before[n]) {
			t.Errorf("camera %s:\nbefore %+v\nafter %+v", n, before[n], got)
		}
	}
}

// Customizing a setting to empty it — "this camera has no zone constraint" — must
// be told apart from "override nothing".
func TestEmptyOverrideIsDistinctFromNoOverride(t *testing.T) {
	cfg, _, ts := setup(t, "", fakeCameras{})

	body := `{"notify":{"zones":["allee"]},"cameras":{"jardin":{"zones":[]},"garage":{}}}`
	if resp := do(t, ts, "PUT", "/api/settings", body); resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d: %s", resp.StatusCode, b)
	}
	if got := cfg.ForCamera("jardin").Zones; len(got) != 0 {
		t.Errorf("jardin.zones = %v, the empty override should have lifted the constraint", got)
	}
	if got := cfg.ForCamera("garage").Zones; len(got) != 1 || got[0] != "allee" {
		t.Errorf("garage.zones = %v, without an override it follows the global settings", got)
	}
}

type fakeTester struct {
	mu      sync.Mutex
	cameras []string
	err     error
}

func (f *fakeTester) SendTest(_ context.Context, camera string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cameras = append(f.cameras, camera)
	return f.err
}

func TestSendTestNotification(t *testing.T) {
	clock := time.Date(2026, 9, 28, 14, 0, 0, 0, time.UTC)
	tester := &fakeTester{}
	_, _, ts := setup(t, "", fakeCameras{}, WithTester(tester), WithClock(func() time.Time { return clock }))

	if resp := do(t, ts, "POST", "/api/test", `{"camera":"garage"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if resp := do(t, ts, "POST", "/api/test", `{"camera":"garage"}`); resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("second immediate test: status = %d, want 429", resp.StatusCode)
	}
	clock = clock.Add(testInterval)
	if resp := do(t, ts, "POST", "/api/test", `{}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("without a camera: status = %d, want 400", resp.StatusCode)
	}
	tester.err = errors.New("chat not found")
	resp := do(t, ts, "POST", "/api/test", `{"camera":"salon"}`)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(string(body), "chat not found") {
		t.Errorf("send failure: %d %s", resp.StatusCode, body)
	}
	if !reflect.DeepEqual(tester.cameras, []string{"garage", "salon"}) {
		t.Errorf("tests sent = %v", tester.cameras)
	}
}

func TestPauseAndResume(t *testing.T) {
	clock := time.Date(2026, 9, 28, 14, 0, 0, 0, time.UTC)
	st, _ := state.Load(filepath.Join(t.TempDir(), "state.json"), clock)
	if err := st.Mute("garage", clock.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	_, _, ts := setup(t, "", fakeCameras{}, WithState(st), WithClock(func() time.Time { return clock }))

	read := func(resp *http.Response) stateView {
		t.Helper()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		var v stateView
		if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	v := read(do(t, ts, "GET", "/api/state", ""))
	if v.Paused || len(v.Mutes) != 1 || v.Mutes[0].Camera != "garage" {
		t.Fatalf("initial state = %+v", v)
	}
	v = read(do(t, ts, "POST", "/api/pause", `{"minutes":30}`))
	if !v.Paused || v.PausedUntil == nil || !v.PausedUntil.Equal(clock.Add(30*time.Minute)) {
		t.Errorf("after a 30 min pause = %+v", v)
	}
	v = read(do(t, ts, "POST", "/api/pause", `{"minutes":0}`))
	if !v.Paused || v.PausedUntil != nil {
		t.Errorf("pause without a deadline = %+v", v)
	}
	v = read(do(t, ts, "POST", "/api/resume", `{"camera":"garage"}`))
	if !v.Paused || len(v.Mutes) != 0 {
		t.Errorf("after resuming garage = %+v", v)
	}
	v = read(do(t, ts, "POST", "/api/resume", ""))
	if v.Paused {
		t.Errorf("after the global resume = %+v", v)
	}
	if resp := do(t, ts, "POST", "/api/pause", `{"minutes":-5}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("negative duration: status = %d", resp.StatusCode)
	}
}

func TestOptionalFeaturesAreAdvertised(t *testing.T) {
	_, _, ts := setup(t, "", fakeCameras{})
	var s settings
	json.NewDecoder(do(t, ts, "GET", "/api/settings", "").Body).Decode(&s)
	if s.CanTest || s.CanPause {
		t.Errorf("without options: can_test=%v can_pause=%v", s.CanTest, s.CanPause)
	}
	if resp := do(t, ts, "POST", "/api/test", `{"camera":"garage"}`); resp.StatusCode != http.StatusNotFound {
		t.Errorf("test without a Tester: status = %d", resp.StatusCode)
	}
}

type fakeHistory struct{ entries []notifier.HistoryEntry }

func (f fakeHistory) History() []notifier.HistoryEntry { return f.entries }
func (f fakeHistory) HistoryThumb(id string) (string, bool) {
	for _, e := range f.entries {
		if e.ID == id && e.Thumb != "" {
			return e.Thumb, true
		}
	}
	return "", false
}

func (f fakeHistory) HistorySnapshot(id string) (string, bool) {
	for _, e := range f.entries {
		if e.ID == id && e.Snapshot != "" {
			return e.Snapshot, true
		}
	}
	return "", false
}

// fakeMedia gives the bytes by path and records the paths asked for.
type fakeMedia struct {
	files map[string][]byte
	asked *[]string
}

func (f fakeMedia) GetBytes(_ context.Context, path string, _ int64) ([]byte, error) {
	if f.asked != nil {
		*f.asked = append(*f.asked, path)
	}
	if b, ok := f.files[path]; ok {
		return b, nil
	}
	return nil, errors.New("absent")
}

func TestHistoryAndThumbnails(t *testing.T) {
	hist := fakeHistory{entries: []notifier.HistoryEntry{
		{ID: "b", Camera: "garage", Label: "dog", Reason: "label", Thumb: "/api/events/b/thumbnail.jpg"},
		{ID: "a", Camera: "garage", Label: "person", Sent: true},
	}}
	media := fakeMedia{files: map[string][]byte{"/api/events/b/thumbnail.jpg": []byte("jpeg")}}
	_, _, ts := setup(t, "", fakeCameras{}, WithHistory(hist, media))

	var v struct {
		Entries []struct {
			ID       string `json:"id"`
			Sent     bool   `json:"sent"`
			Reason   string `json:"reason"`
			HasThumb bool   `json:"has_thumb"`
			Thumb    string `json:"Thumb"`
		} `json:"entries"`
	}
	resp := do(t, ts, "GET", "/api/history", "")
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	if len(v.Entries) != 2 || v.Entries[0].Reason != "label" || !v.Entries[0].HasThumb || v.Entries[1].HasThumb || !v.Entries[1].Sent {
		t.Errorf("history = %+v", v.Entries)
	}
	if v.Entries[0].Thumb != "" {
		t.Error("the Frigate path must not be exposed")
	}

	resp = do(t, ts, "GET", "/api/history/b/thumb", "")
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "jpeg" || resp.Header.Get("Content-Type") != "image/jpeg" {
		t.Errorf("thumbnail: %d %q", resp.StatusCode, body)
	}
	for _, id := range []string{"a", "inconnu"} {
		if resp := do(t, ts, "GET", "/api/history/"+id+"/thumb", ""); resp.StatusCode != http.StatusNotFound {
			t.Errorf("thumbnail of %s: status = %d, want 404", id, resp.StatusCode)
		}
	}
}

func TestHistorySnapshotAndLink(t *testing.T) {
	hist := fakeHistory{entries: []notifier.HistoryEntry{
		{ID: "a", Camera: "garage", Label: "person", Sent: true, Link: "https://frigate.example/explore?event_id=a", Snapshot: "/api/events/a/snapshot.jpg?bbox=1"},
		{ID: "b", Camera: "garage", Label: "dog"},
	}}
	media := fakeMedia{files: map[string][]byte{"/api/events/a/snapshot.jpg?bbox=1": []byte("big")}}
	_, _, ts := setup(t, "", fakeCameras{}, WithHistory(hist, media))

	var v struct {
		Entries []struct {
			Link        string `json:"link"`
			HasSnapshot bool   `json:"has_snapshot"`
			Snapshot    string `json:"Snapshot"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(do(t, ts, "GET", "/api/history", "").Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	if len(v.Entries) != 2 || !v.Entries[0].HasSnapshot || v.Entries[1].HasSnapshot || v.Entries[0].Link != hist.entries[0].Link || v.Entries[1].Link != "" {
		t.Errorf("history = %+v", v.Entries)
	}
	if v.Entries[0].Snapshot != "" {
		t.Error("the Frigate path must not be exposed")
	}

	resp := do(t, ts, "GET", "/api/history/a/snapshot", "")
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "big" || resp.Header.Get("Content-Type") != "image/jpeg" {
		t.Errorf("snapshot: %d %q", resp.StatusCode, body)
	}
	for _, id := range []string{"b", "inconnu"} {
		if resp := do(t, ts, "GET", "/api/history/"+id+"/snapshot", ""); resp.StatusCode != http.StatusNotFound {
			t.Errorf("snapshot of %s: status = %d, want 404", id, resp.StatusCode)
		}
	}
}

type fakeWebhook struct{ deleted *bool }

func (f fakeWebhook) DeleteWebhook(context.Context) error { *f.deleted = true; return nil }

// The interface removes the bot's webhook when it can; without a webhook remover,
// the route answers 404.
func TestDeleteWebhook(t *testing.T) {
	var deleted bool
	_, _, ts := setup(t, "", fakeCameras{}, WithWebhook(fakeWebhook{&deleted}))
	if resp := do(t, ts, "POST", "/api/telegram/webhook/delete", ""); resp.StatusCode != http.StatusOK || !deleted {
		t.Errorf("POST: %d, deleted %v", resp.StatusCode, deleted)
	}
	_, _, ts = setup(t, "", fakeCameras{})
	if resp := do(t, ts, "POST", "/api/telegram/webhook/delete", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("without a remover: %d, want 404", resp.StatusCode)
	}
}

type fakeActivity []time.Time

func (f fakeActivity) SentTimes() []time.Time { return f }

// The history also gives the notifications sent over 24 h, per clock hour, the current
// hour last.
func TestHistoryGivesSentHours(t *testing.T) {
	now := time.Date(2026, 10, 17, 21, 30, 0, 0, time.UTC)
	// 21:30 - 23h50 = 21:40 the day before: before the first bar (22:00).
	act := fakeActivity{now.Add(-23*time.Hour - 30*time.Minute), now.Add(-23*time.Hour - 50*time.Minute), now.Add(-90 * time.Minute), now.Add(-10 * time.Minute), now}
	_, _, ts := setup(t, "", fakeCameras{}, WithHistory(fakeHistory{}, nil), WithActivity(act), WithClock(func() time.Time { return now }))
	var v struct {
		Sent  int       `json:"sent_24h"`
		Hours []int     `json:"sent_hours"`
		Now   time.Time `json:"now"`
		Hour  time.Time `json:"hour"`
	}
	if err := json.NewDecoder(do(t, ts, "GET", "/api/history", "").Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	want := make([]int, 24)
	want[0], want[22], want[23] = 1, 1, 2 // 22:00, 20:00 (20:00 itself), 21:00 (21:20 and 21:30)
	if !v.Now.Equal(now) {
		t.Errorf("now = %v, want the server's clock %v (the bars' windows end then)", v.Now, now)
	}
	if want := time.Date(2026, 10, 17, 21, 0, 0, 0, time.UTC); !v.Hour.Equal(want) {
		t.Errorf("hour = %v, want the start of the current clock hour %v", v.Hour, want)
	}
	if v.Sent != 4 || !slices.Equal(v.Hours, want) {
		t.Errorf("sent %d, hours %v; want 4 (the bars' total), %v", v.Sent, v.Hours, want)
	}
}

// A clock hour starts on the zone's :00, also in a zone offset by 30 min.
func TestHourOf(t *testing.T) {
	kolkata, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Skip(err)
	}
	at := time.Date(2026, 10, 17, 21, 10, 5, 0, time.UTC) // 02:40:05 in Kolkata
	if got, want := hourOf(at, kolkata), time.Date(2026, 10, 17, 20, 30, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("hourOf = %v, want %v", got, want)
	}
}

// Errors follow the interface's language (X-Lang), otherwise the browser's,
// otherwise the service's (English by default).
func TestErrorsFollowInterfaceLanguage(t *testing.T) {
	_, _, ts := setup(t, "", fakeCameras{})
	bad := `{"notify":{"chats":["nope"]}}`
	for _, tc := range []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{"default", nil, "unknown chat"},
		{"X-Lang", map[string]string{"X-Lang": "fr"}, "inconnu (voir"},
		{"Accept-Language", map[string]string{"Accept-Language": "fr-FR,fr;q=0.9,en;q=0.8"}, "inconnu (voir"},
		{"X-Lang prime", map[string]string{"X-Lang": "en", "Accept-Language": "fr-FR"}, "unknown chat"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest("PUT", ts.URL+"/api/settings", strings.NewReader(bad))
			req.Header.Set("X-Requested-With", requestedWith)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			resp, err := ts.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), tc.want) {
				t.Errorf("%d %s; want %q", resp.StatusCode, body, tc.want)
			}
		})
	}
}

func TestRecipientsAreSavedAndValidated(t *testing.T) {
	cfg, path, ts := setup(t, "", fakeCameras{})
	if resp := do(t, ts, "PUT", "/api/settings", `{"notify":{},"recipients":{"inconnu":{"labels":["person"]}}}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown recipient: status = %d", resp.StatusCode)
	}
	body := `{"notify":{},"recipients":{"famille":{"labels":["person"],"off_hours":[{"from":"07:00","to":"22:00"}]}}}`
	if resp := do(t, ts, "PUT", "/api/settings", body); resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if r := cfg.Recipient("famille"); !reflect.DeepEqual(r.Labels, []string{"person"}) || len(r.OffHours) != 1 {
		t.Errorf("famille = %+v", r)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "recipients:") {
		t.Errorf("file without recipients:\n%s", raw)
	}
}

func TestWrongPasswordsBlockTheAddress(t *testing.T) {
	_, _, ts := setup(t, "s3cret", fakeCameras{})
	for i := range maxFailures {
		if resp := do(t, ts, "GET", "/api/settings", "", "faux"); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d", i+1, resp.StatusCode)
		}
	}
	// Blocked, even with the right password, and on another route.
	resp := do(t, ts, "GET", "/", "", "s3cret")
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("after %d failures: status = %d, Retry-After = %q", maxFailures, resp.StatusCode, resp.Header.Get("Retry-After"))
	}
}

func TestBlockedAddressKeepsItsSessions(t *testing.T) {
	a := NewAuth("pw", nil, nil)
	login := httptest.NewRecorder()
	a.openSession(login, httptest.NewRequest("POST", "/login", nil))
	for range maxFailures {
		a.fail("192.0.2.1")
	}
	ok := a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	r := httptest.NewRequest("GET", "/api/settings", nil) // RemoteAddr 192.0.2.1
	r.AddCookie(login.Result().Cookies()[0])
	w := httptest.NewRecorder()
	ok.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("a session on a blocked address: status = %d", w.Code)
	}
}

func TestAuthUnblocksAfterDelay(t *testing.T) {
	now := time.Date(2026, 9, 28, 14, 0, 0, 0, time.UTC)
	a := NewAuth("pw", nil, nil)
	a.now = func() time.Time { return now }
	for range maxFailures {
		a.fail("10.0.0.1")
	}
	if a.blocked("10.0.0.1") <= 0 || a.blocked("10.0.0.2") > 0 {
		t.Fatal("only 10.0.0.1 must be blocked")
	}
	now = now.Add(blockFor + time.Second)
	if a.blocked("10.0.0.1") > 0 {
		t.Error("the block must expire")
	}
	// Failures more than a minute apart do not add up.
	for range maxFailures {
		a.fail("10.0.0.3")
		now = now.Add(failureWindow + time.Second)
	}
	if a.blocked("10.0.0.3") > 0 {
		t.Error("failures far apart must not block")
	}
}

func TestExternalURLIsSavedAndValidated(t *testing.T) {
	cfg, _, ts := setup(t, "", fakeCameras{})
	var s settings
	json.NewDecoder(do(t, ts, "GET", "/api/settings", "").Body).Decode(&s)
	if s.Overlay.ExternalURL == nil {
		t.Fatal("the current address must be passed to the interface")
	}
	if resp := do(t, ts, "PUT", "/api/settings", `{"notify":{},"external_url":"frigate.lan"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid address: status = %d", resp.StatusCode)
	}
	if resp := do(t, ts, "PUT", "/api/settings", `{"notify":{},"external_url":"http://192.168.1.10:5000/"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := cfg.ExternalURL(); got != "http://192.168.1.10:5000" {
		t.Errorf("address = %q", got)
	}
}

func TestHomeAssistantURLIsSavedAndValidated(t *testing.T) {
	cfg, _, ts := setup(t, "", fakeCameras{})
	if resp := do(t, ts, "PUT", "/api/settings", `{"notify":{},"home_assistant_url":"ha.lan"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid address: status = %d", resp.StatusCode)
	}
	if resp := do(t, ts, "PUT", "/api/settings", `{"notify":{},"home_assistant_url":"http://192.168.1.20:8123/"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := cfg.HomeAssistantURL(); got != "http://192.168.1.20:8123" {
		t.Errorf("address = %q", got)
	}
}

type fakeRefusals []bot.Refused

func (f fakeRefusals) Refused() []bot.Refused { return f }

func TestHealthListsRefusedUsers(t *testing.T) {
	health := func(context.Context, i18n.Lang) []Component {
		return []Component{{Name: "MQTT", State: StateOK}}
	}
	at := time.Date(2026, 9, 28, 14, 0, 0, 0, time.UTC)
	_, _, ts := setup(t, "", fakeCameras{}, WithHealth(health),
		WithRefused(fakeRefusals{{ID: 999, Name: "Alice", Username: "alice", At: at}}))
	var got struct {
		Components []Component   `json:"components"`
		Refused    []bot.Refused `json:"refused"`
	}
	if err := json.NewDecoder(do(t, ts, "GET", "/api/health", "").Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Components) != 1 || len(got.Refused) != 1 || got.Refused[0].ID != 999 || got.Refused[0].Username != "alice" {
		t.Errorf("response = %+v", got)
	}

	_, _, ts = setup(t, "", fakeCameras{}, WithHealth(health))
	var bare map[string]json.RawMessage
	json.NewDecoder(do(t, ts, "GET", "/api/health", "").Body).Decode(&bare)
	if string(bare["refused"]) != "[]" {
		t.Errorf("refused without a source = %s, want []", bare["refused"])
	}
}

// The built front end is embedded and served: the login page's sheet before any
// session, and the fonts it points to, with a long cache since their names carry a hash.
func TestBuiltAssetsServed(t *testing.T) {
	_, _, ts := setup(t, "s3cret", fakeCameras{})
	resp := do(t, ts, "GET", "/login.css", "")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/css; charset=utf-8" {
		t.Fatalf("login.css: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	css, _ := io.ReadAll(resp.Body)
	font := regexp.MustCompile(`url\(\./(assets/[^)]+\.woff2)\)`).FindSubmatch(css)
	if font == nil {
		t.Fatalf("login.css points to no font: %.200s", css)
	}
	resp = do(t, ts, "GET", "/"+string(font[1]), "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Errorf("%s: %d, Cache-Control %q", font[1], resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
}

// The sample photo of the previews is served as a JPEG, with or without a password.
func TestBuiltPhotoServed(t *testing.T) {
	photos, _ := fs.Glob(distFS, "assets/sample-*.jpg")
	if len(photos) != 1 {
		t.Fatalf("dist/assets/sample-*.jpg = %v, want one file", photos)
	}
	for _, password := range []string{"", "s3cret"} {
		_, _, ts := setup(t, password, fakeCameras{})
		resp := do(t, ts, "GET", "/"+photos[0], "")
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/jpeg" {
			t.Errorf("password %q: %d %s, want 200 image/jpeg", password, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
	}
}

// dist/assets/ serves existing files only: no directory listing, and no long cache on a 404.
func TestBuiltAssetsOnlyFiles(t *testing.T) {
	_, _, ts := setup(t, "s3cret", fakeCameras{})
	resp := do(t, ts, "GET", "/assets/", "")
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusNotFound || strings.Contains(string(body), ".woff2") {
		t.Errorf("/assets/: %d, body %.100q, want 404 without a listing", resp.StatusCode, body)
	}
	resp = do(t, ts, "GET", "/assets/nope.woff2", "")
	if resp.StatusCode != http.StatusNotFound || strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Errorf("/assets/nope.woff2: %d, Cache-Control %q, want 404 not cached", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
}
