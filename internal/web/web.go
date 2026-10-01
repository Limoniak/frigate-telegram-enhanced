// Package web serves the notification settings interface: a single page and a
// small JSON API over the override file (see config.Overlay).
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"frigate-telegram-enhanced/internal/bot"
	"frigate-telegram-enhanced/internal/config"
	"frigate-telegram-enhanced/internal/frigate"
	"frigate-telegram-enhanced/internal/i18n"
	"frigate-telegram-enhanced/internal/notifier"
	"frigate-telegram-enhanced/internal/state"
)

// The interface: the page, its style sheet and its script, with no build step,
// and the setup page.
//
//go:embed ui.html ui.css ui.js setup.html setup.js
var assets embed.FS

// maxBody bounds the size of a save: the override of a realistic installation
// weighs a few kilobytes.
const maxBody = 1 << 20

// SubLabelLister gives the labels Frigate knows; optional for a CameraLister.
type SubLabelLister interface {
	SubLabels(ctx context.Context) ([]string, error)
}

// CameraLister lists Frigate's cameras with their zones and tracked objects.
type CameraLister interface {
	CameraDetails(ctx context.Context) ([]frigate.CameraInfo, error)
}

// Tester sends a sample notification for a camera.
type Tester interface {
	SendTest(ctx context.Context, camera string) error
}

// State is the state shared with the Telegram commands: global pause and muted cameras.
type State interface {
	Status(now time.Time) state.Status
	Pause(until time.Time) error
	Resume() error
	Unmute(camera string) error
}

// History gives the latest detections and their thumbnails.
type History interface {
	History() []notifier.HistoryEntry
	HistoryThumb(id string) (string, bool)
}

// Media reads an image from Frigate.
type Media interface {
	GetBytes(ctx context.Context, path string, max int64) ([]byte, error)
}

// maxThumb bounds the size of a thumbnail relayed from Frigate.
const maxThumb = 1 << 20

// State of a connection, as the interface shows it.
const (
	StateOK      = "ok"
	StatePending = "pending"
	StateWarn    = "warn" // works, but with a problem to report
	StateError   = "error"
)

// Component describes the state of one of the service's connections (Frigate, MQTT, Telegram).
type Component struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Detail string `json:"detail"`
	Hint   string `json:"hint,omitempty"` // what to fix, on error
}

// Refusals gives the Telegram users recently refused by the bot.
type Refusals interface {
	Refused() []bot.Refused
}

// HealthFunc diagnoses the connections, with messages in the language l.
type HealthFunc func(ctx context.Context, l i18n.Lang) []Component

// testInterval spaces out the test notifications: a double click must not send
// two of them.
const testInterval = 3 * time.Second

type Handler struct {
	cfg     *config.Config
	path    string // override file
	cameras CameraLister
	log     *slog.Logger
	tester  Tester // nil: no test sending
	state   State  // nil: no pause from the interface
	history History
	media   Media      // nil: history without thumbnails
	health  HealthFunc // nil: no connection status
	refused Refusals   // nil: no list of refused users
	setup   *Setup     // nil: connection set by config.yml or the environment
	auth    *Auth      // password, shared by every route
	now     func() time.Time

	// save serializes the saves: two tabs open at the same time must not interleave
	// validation, writing and applying.
	save sync.Mutex

	testMu   sync.Mutex
	lastTest time.Time
}

type Option func(*Handler)

// WithTester enables the "send me a sample" button.
func WithTester(t Tester) Option { return func(h *Handler) { h.tester = t } }

// WithState enables the pause display and the pause/resume buttons.
func WithState(s State) Option { return func(h *Handler) { h.state = s } }

// WithHistory enables the recent activity; media (optional) serves the thumbnails.
func WithHistory(h History, media Media) Option {
	return func(x *Handler) { x.history, x.media = h, media }
}

// WithHealth enables the connection status display.
func WithHealth(f HealthFunc) Option { return func(h *Handler) { h.health = f } }

// WithRefused adds to the connection status the Telegram users recently refused,
// with their ID to add to the configuration.
func WithRefused(r Refusals) Option { return func(h *Handler) { h.refused = r } }

// WithSetup adds the "Connection" page, to change what the setup page saved.
func WithSetup(s *Setup) Option { return func(h *Handler) { h.setup = s } }

// WithClock replaces the clock (tests).
func WithClock(now func() time.Time) Option { return func(h *Handler) { h.now = now } }

func New(cfg *config.Config, overlayPath string, cameras CameraLister, log *slog.Logger, opts ...Option) *Handler {
	h := &Handler{cfg: cfg, path: overlayPath, cameras: cameras, log: log, now: time.Now,
		auth: NewAuth(cfg.Web.Password, log)}
	for _, o := range opts {
		o(h)
	}
	return h
}

// Protect puts next behind the interface password, with the same failure count
// (serves /metrics with web.protect_metrics).
func (h *Handler) Protect(next http.Handler) http.Handler { return h.auth.Wrap(next) }

// Mount registers the interface and its API on mux, behind a password if the
// configuration sets one. /healthz and /metrics stay outside: the container probe
// and the Prometheus scrape do not authenticate (see web.protect_metrics).
func (h *Handler) Mount(mux *http.ServeMux) {
	mux.Handle("GET /{$}", h.guard(asset("ui.html", "text/html; charset=utf-8")))
	mux.Handle("GET /ui.css", h.guard(asset("ui.css", "text/css; charset=utf-8")))
	mux.Handle("GET /ui.js", h.guard(asset("ui.js", "text/javascript; charset=utf-8")))
	mux.Handle("GET /i18n.js", h.guard(serve(i18nScript, "text/javascript; charset=utf-8")))
	mux.Handle("GET /api/settings", h.guard(http.HandlerFunc(h.get)))
	mux.Handle("PUT /api/settings", h.guard(sameOrigin(http.HandlerFunc(h.put))))
	mux.Handle("POST /api/settings/reset", h.guard(sameOrigin(http.HandlerFunc(h.reset))))
	mux.Handle("POST /api/test", h.guard(sameOrigin(http.HandlerFunc(h.test))))
	mux.Handle("GET /api/state", h.guard(http.HandlerFunc(h.getState)))
	mux.Handle("POST /api/pause", h.guard(sameOrigin(http.HandlerFunc(h.pause))))
	mux.Handle("POST /api/resume", h.guard(sameOrigin(http.HandlerFunc(h.resume))))
	mux.Handle("GET /api/history", h.guard(http.HandlerFunc(h.getHistory)))
	mux.Handle("GET /api/health", h.guard(http.HandlerFunc(h.getHealth)))
	mux.Handle("GET /api/history/{id}/thumb", h.guard(http.HandlerFunc(h.thumb)))
	if h.setup != nil {
		h.setup.mount(mux, h.guard)
	}
}

// guard applies the access control shared by every route of the interface: the
// password if there is one, otherwise the Host header check.
func (h *Handler) guard(next http.Handler) http.Handler {
	if h.cfg.Web.Password != "" {
		return h.auth.Wrap(next)
	}
	refused := hostRefused
	if h.setup != nil {
		refused = hostRefusedPage
	}
	return checkHost(h.cfg.Web.AllowedHosts, h.cfg.Language, refused, next)
}

// Errors of checkHost once the service is configured, by config.yml or the
// environment, or by the setup page.
const (
	hostRefused     = "host not allowed: add this name to WEB_ALLOWED_HOSTS (web.allowed_hosts), or set WEB_PASSWORD"
	hostRefusedPage = "host not allowed: open the interface with the server's IP address, or set a password on the Connection page"
)

// checkHost refuses the requests whose Host header is neither an IP address, nor
// localhost, nor a name listed in allowed. Without a password, that is what blocks
// DNS rebinding: a third-party site that points its own domain at 127.0.0.1 becomes
// "same origin" for the browser — X-Requested-With no longer stops it — but its
// requests still carry its domain name in Host.
// refused is the English text of the error, translated for the request.
func checkHost(allowed []string, def i18n.Lang, refused string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hostAllowed(r.Host, allowed) {
			http.Error(w, requestLang(r, def).T(refused), http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func hostAllowed(host string, allowed []string) bool {
	host = requestHost(host)
	if host == "" {
		return false
	}
	if net.ParseIP(host) != nil || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	return slices.ContainsFunc(allowed, func(a string) bool { return strings.EqualFold(a, host) })
}

// requestedWith is the header the page adds to its writes. It is worth nothing as a
// secret: it makes the request "non-simple" in the CORS sense, so that a
// third-party site open in the same browser cannot trigger it behind our back — the
// preflight check it requires will fail, for lack of CORS headers on our side.
const requestedWith = "frigate-telegram-enhanced"

func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Requested-With") != requestedWith {
			http.Error(w, requestLang(r, i18n.Default).T("request refused: missing X-Requested-With header"), http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// asset serves a file of the interface. no-store: after an update of the service,
// the browser must not keep the old script with the new page.
func asset(name, contentType string) http.Handler {
	body, err := assets.ReadFile(name)
	if err != nil {
		panic(err) // embedded: missing only if the code is inconsistent
	}
	return serve(body, contentType)
}

func serve(body []byte, contentType string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		w.Write(body)
	})
}

// i18nScript is i18n.js: the languages and their catalogs, for the page's T().
var i18nScript = func() []byte {
	type language struct {
		Code i18n.Lang `json:"code"`
		Name string    `json:"name"`
	}
	var langs []language
	catalogs := map[i18n.Lang]map[string]string{}
	for _, l := range i18n.Languages() {
		langs = append(langs, language{Code: l, Name: l.Name()})
		if l != i18n.EN {
			catalogs[l] = i18n.Catalogs()[l].Messages
		}
	}
	lj, err := json.Marshal(langs)
	if err != nil {
		panic(err)
	}
	cj, err := json.Marshal(catalogs)
	if err != nil {
		panic(err)
	}
	return []byte("\"use strict\";\nconst LANGUAGES = " + string(lj) + ";\nconst CATALOGS = " + string(cj) + ";\n")
}()

// settings is the view the interface loads at startup.
type settings struct {
	Mode     string               `json:"mode"`
	Timezone string               `json:"timezone"`
	Chats    []string             `json:"chats"`
	Cameras  []frigate.CameraInfo `json:"cameras"`
	Overlay  config.Overlay       `json:"overlay"`
	Custom   bool                 `json:"custom"`  // an override is saved
	Warning  string               `json:"warning"` // Frigate injoignable, etc.
	CanTest  bool                 `json:"can_test"`
	CanPause bool                 `json:"can_pause"`
	CanHist  bool                 `json:"can_history"`
	CanHlth  bool                 `json:"can_health"`
	Presence bool                 `json:"presence"`       // presence topics configured
	CanConn  bool                 `json:"can_connection"` // connection made on the setup page
	// FrigateURL is the address of the links when no external address is set.
	FrigateURL string `json:"frigate_url"`
	// SubLabels: labels Frigate knows, offered by the label filter.
	SubLabels []string `json:"sub_labels"`
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	s := settings{
		Mode:       h.cfg.Mode,
		Timezone:   h.cfg.Timezone,
		Chats:      h.cfg.ChatNames(),
		Overlay:    h.cfg.CurrentOverlay(),
		CanTest:    h.tester != nil,
		CanPause:   h.state != nil,
		CanHist:    h.history != nil,
		CanHlth:    h.health != nil,
		Presence:   h.cfg.Presence.Enabled(),
		CanConn:    h.setup != nil,
		FrigateURL: h.cfg.Frigate.URL,
	}
	if _, err := os.Stat(h.path); err == nil {
		s.Custom = true
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	cams, err := h.cameras.CameraDetails(ctx)
	if err != nil {
		// Frigate unreachable: show at least the cameras already set up, so that the
		// interface stays usable and erases nothing.
		s.Warning = h.lang(r).T("Frigate is unreachable: the list of cameras, zones and objects is incomplete.")
		for _, name := range h.cfg.CameraNames() {
			cams = append(cams, frigate.CameraInfo{Name: name})
		}
	}
	s.Cameras = cams
	s.SubLabels = []string{}
	if sl, ok := h.cameras.(SubLabelLister); ok && err == nil {
		if subs, err := sl.SubLabels(ctx); err == nil {
			s.SubLabels = subs
		}
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *Handler) put(w http.ResponseWriter, r *http.Request) {
	var o config.Overlay
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&o); err != nil {
		h.writeError(w, r, http.StatusBadRequest, err)
		return
	}

	h.save.Lock()
	defer h.save.Unlock()
	// Validate before writing: an invalid override file would be rejected at the next
	// startup, and the service would fall back to config.yml without warning.
	if err := h.cfg.ValidateOverlay(&o, h.lang(r)); err != nil {
		h.writeError(w, r, http.StatusBadRequest, err)
		return
	}
	if err := config.SaveOverlay(h.path, o); err != nil {
		h.log.Error("saving settings failed", "err", err)
		h.writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	if err := h.cfg.ApplyOverlay(&o); err != nil {
		h.writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	h.log.Info("notification settings updated from the web interface")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) reset(w http.ResponseWriter, r *http.Request) {
	h.save.Lock()
	defer h.save.Unlock()
	if err := os.Remove(h.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		h.writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	if err := h.cfg.ApplyOverlay(nil); err != nil {
		h.writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	h.log.Info("notification settings reverted to config.yml")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// decodeBody reads a small JSON body; an empty body leaves v at its zero value.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func (h *Handler) test(w http.ResponseWriter, r *http.Request) {
	if h.tester == nil {
		h.writeError(w, r, http.StatusNotFound, i18n.NewError("test notifications are unavailable"))
		return
	}
	var req struct {
		Camera string `json:"camera"`
	}
	if err := decodeBody(w, r, &req); err != nil || req.Camera == "" {
		h.writeError(w, r, http.StatusBadRequest, i18n.NewError("missing camera"))
		return
	}
	if !h.allowTest() {
		h.writeError(w, r, http.StatusTooManyRequests, i18n.NewError("a test was just sent, wait a few seconds"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := h.tester.SendTest(ctx, req.Camera); err != nil {
		h.log.Warn("test notification failed", "camera", req.Camera, "err", err)
		h.writeError(w, r, http.StatusBadGateway, err)
		return
	}
	h.log.Info("test notification sent from the web interface", "camera", req.Camera)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// allowTest books a slot for a test send; false if the previous one is less than
// testInterval ago.
func (h *Handler) allowTest() bool {
	h.testMu.Lock()
	defer h.testMu.Unlock()
	now := h.now()
	if !h.lastTest.IsZero() && now.Sub(h.lastTest) < testInterval {
		return false
	}
	h.lastTest = now
	return true
}

// stateView is the pause state as the interface shows it. A null deadline means
// "until resumed".
type stateView struct {
	Paused      bool       `json:"paused"`
	PausedUntil *time.Time `json:"paused_until"`
	Mutes       []muteView `json:"mutes"`
	Home        []string   `json:"home"` // presence topics saying "home"
}

type muteView struct {
	Camera string     `json:"camera"`
	Until  *time.Time `json:"until"`
}

func until(t time.Time) *time.Time {
	if !t.Before(state.Forever) {
		return nil
	}
	return &t
}

func (h *Handler) writeState(w http.ResponseWriter) {
	st := h.state.Status(h.now())
	v := stateView{Paused: !st.PausedUntil.IsZero(), Mutes: []muteView{}, Home: append([]string{}, st.Home...)}
	if v.Paused {
		v.PausedUntil = until(st.PausedUntil)
	}
	for _, cam := range slices.Sorted(maps.Keys(st.Mutes)) {
		v.Mutes = append(v.Mutes, muteView{Camera: cam, Until: until(st.Mutes[cam])})
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) getState(w http.ResponseWriter, r *http.Request) {
	if h.state == nil {
		h.writeError(w, r, http.StatusNotFound, i18n.NewError("state unavailable"))
		return
	}
	h.writeState(w)
}

func (h *Handler) pause(w http.ResponseWriter, r *http.Request) {
	if h.state == nil {
		h.writeError(w, r, http.StatusNotFound, i18n.NewError("state unavailable"))
		return
	}
	var req struct {
		Minutes int `json:"minutes"`
	}
	if err := decodeBody(w, r, &req); err != nil || req.Minutes < 0 || req.Minutes > 7*24*60 {
		h.writeError(w, r, http.StatusBadRequest, i18n.NewError("invalid duration (0 = until resumed, 7 days at most)"))
		return
	}
	end := state.Forever
	if req.Minutes > 0 {
		end = h.now().Add(time.Duration(req.Minutes) * time.Minute)
	}
	if err := h.state.Pause(end); err != nil {
		h.writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	h.log.Info("notifications paused from the web interface", "minutes", req.Minutes)
	h.writeState(w)
}

func (h *Handler) resume(w http.ResponseWriter, r *http.Request) {
	if h.state == nil {
		h.writeError(w, r, http.StatusNotFound, i18n.NewError("state unavailable"))
		return
	}
	var req struct {
		Camera string `json:"camera"`
	}
	if err := decodeBody(w, r, &req); err != nil {
		h.writeError(w, r, http.StatusBadRequest, err)
		return
	}
	var err error
	if req.Camera == "" {
		err = h.state.Resume()
	} else {
		err = h.state.Unmute(req.Camera)
	}
	if err != nil {
		h.writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	h.log.Info("notifications resumed from the web interface", "camera", req.Camera)
	h.writeState(w)
}

func (h *Handler) getHealth(w http.ResponseWriter, r *http.Request) {
	if h.health == nil {
		h.writeError(w, r, http.StatusNotFound, i18n.NewError("health unavailable"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	refused := []bot.Refused{}
	if h.refused != nil {
		refused = append(refused, h.refused.Refused()...)
	}
	writeJSON(w, http.StatusOK, struct {
		Components []Component   `json:"components"`
		Refused    []bot.Refused `json:"refused"`
	}{h.health(ctx, h.lang(r)), refused})
}

type historyView struct {
	Entries []historyEntryView `json:"entries"`
}

type historyEntryView struct {
	notifier.HistoryEntry
	HasThumb bool `json:"has_thumb"`
}

func (h *Handler) getHistory(w http.ResponseWriter, r *http.Request) {
	if h.history == nil {
		h.writeError(w, r, http.StatusNotFound, i18n.NewError("history unavailable"))
		return
	}
	v := historyView{Entries: []historyEntryView{}}
	for _, e := range h.history.History() {
		v.Entries = append(v.Entries, historyEntryView{HistoryEntry: e, HasThumb: h.media != nil && e.Thumb != ""})
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) thumb(w http.ResponseWriter, r *http.Request) {
	if h.history == nil || h.media == nil {
		http.NotFound(w, r)
		return
	}
	path, ok := h.history.HistoryThumb(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	img, err := h.media.GetBytes(ctx, path, maxThumb)
	if err != nil {
		http.Error(w, h.lang(r).T("thumbnail unavailable"), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Write(img)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// writeError answers a JSON error, in the interface's language if the error can be translated.
func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, code int, err error) {
	writeJSON(w, code, map[string]string{"error": h.lang(r).Message(err)})
}

// langHeader carries the language shown by the interface, sent with each of its requests.
const langHeader = "X-Lang"

// lang is the language of the responses to r: the interface's, otherwise the
// browser's, otherwise the service's.
func (h *Handler) lang(r *http.Request) i18n.Lang { return requestLang(r, h.cfg.Language) }

func requestLang(r *http.Request, def i18n.Lang) i18n.Lang {
	if l, err := i18n.Parse(r.Header.Get(langHeader)); err == nil && r.Header.Get(langHeader) != "" {
		return l
	}
	// Accept-Language: "fr-FR,fr;q=0.9,en;q=0.8" — only the first language counts.
	first, _, _ := strings.Cut(r.Header.Get("Accept-Language"), ",")
	first, _, _ = strings.Cut(first, ";")
	if l, err := i18n.Parse(first); err == nil && first != "" {
		return l
	}
	return def
}
