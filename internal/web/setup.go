package web

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"frigate-telegram-enhanced/internal/config"
	"frigate-telegram-enhanced/internal/i18n"
)

// FoundChat is a Telegram chat that wrote to the bot, offered as a recipient.
type FoundChat struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Private bool   `json:"private"` // a person, who can then control the bot; otherwise a group
}

// Problem is a connection that does not work, explained for the user: what is
// wrong, and what to change.
type Problem struct {
	Detail string
	Hint   string
	Auth   bool // credentials refused: the server answers, only they are wrong
}

func (p *Problem) Error() string { return p.Detail }

// State of the broker found through Frigate.
const (
	BrokerOK       = "ok"       // it accepts the connection
	BrokerPassword = "password" // it answers, but wants a password Frigate does not give
	BrokerError    = "error"    // unreachable
)

// FoundFrigate is a Frigate that answered, with the broker it publishes to, as
// this container reaches it: what the setup page fills in by itself.
type FoundFrigate struct {
	URL     string `json:"url"`
	Version string `json:"version"`
	// MQTT is read from Frigate's configuration; an empty broker if it could not be.
	MQTT        config.MQTT `json:"mqtt"`
	BrokerState string      `json:"broker_state"` // BrokerOK, BrokerPassword, BrokerError; empty if not tried
	BrokerError string      `json:"broker_error"`
}

// Prober tries the connection the setup page is filling in. Errors are written in
// the language l, as *Problem when they can be explained.
type Prober interface {
	// Telegram checks the token and returns the bot's @name and the chats that wrote to it.
	Telegram(ctx context.Context, token string, l i18n.Lang) (bot string, chats []FoundChat, err error)
	// Frigate checks the address and returns what it found there.
	Frigate(ctx context.Context, f config.Frigate, l i18n.Lang) (FoundFrigate, error)
	// Discover looks for a Frigate without authentication at the usual addresses,
	// hosts first (the one the page was opened with); nil if none answers.
	Discover(ctx context.Context, hosts []string, l i18n.Lang) *FoundFrigate
	// MQTT tries a connection to the broker.
	MQTT(ctx context.Context, m config.MQTT, l i18n.Lang) error
}

// Setup serves the setup page: the connection to Telegram, Frigate and MQTT, saved
// in a file of the volume (see config.Connection). It is the only page before the
// first configuration, then the "Connection" link of the interface.
type Setup struct {
	path    string
	current *config.Connection // nil: first setup
	probe   Prober
	log     *slog.Logger
	saved   func() // called once the connection is saved: the service restarts with it

	mu   sync.Mutex
	done bool // saved: the service is restarting
}

// NewSetup builds the setup page. current is the saved connection (nil on the first
// setup); saved is called after a successful save.
func NewSetup(path string, current *config.Connection, probe Prober, log *slog.Logger, saved func()) *Setup {
	return &Setup{path: path, current: current, probe: probe, log: log, saved: saved}
}

// MountAlone registers the setup page alone, for a service that is not configured
// yet: every other page leads to it. With no password set yet, only the Host check
// protects it (see checkHost).
func (s *Setup) MountAlone(mux *http.ServeMux) {
	guard := func(next http.Handler) http.Handler {
		return checkHost(nil, i18n.Default, setupHostRefused, next)
	}
	mux.Handle("GET /{$}", guard(http.RedirectHandler("setup", http.StatusFound)))
	mux.Handle("GET /ui.css", guard(asset("ui.css", "text/css; charset=utf-8")))
	mux.Handle("GET /i18n.js", guard(serve(i18nScript, "text/javascript; charset=utf-8")))
	s.mount(mux, guard)
}

// setupHostRefused is the error of checkHost before the setup: no name is known yet.
const setupHostRefused = "open this page with the server's IP address (for example http://192.168.1.10:8431): the name will be accepted once the setup is saved"

func (s *Setup) mount(mux *http.ServeMux, guard func(http.Handler) http.Handler) {
	mux.Handle("GET /setup", guard(asset("setup.html", "text/html; charset=utf-8")))
	mux.Handle("GET /setup.js", guard(asset("setup.js", "text/javascript; charset=utf-8")))
	mux.Handle("GET /api/connection", guard(http.HandlerFunc(s.get)))
	mux.Handle("POST /api/connection/telegram", guard(sameOrigin(http.HandlerFunc(s.telegram))))
	mux.Handle("POST /api/connection/frigate", guard(sameOrigin(http.HandlerFunc(s.frigate))))
	mux.Handle("POST /api/connection/mqtt", guard(sameOrigin(http.HandlerFunc(s.mqtt))))
	mux.Handle("POST /api/connection/discover", guard(sameOrigin(http.HandlerFunc(s.discover))))
	mux.Handle("PUT /api/connection", guard(sameOrigin(http.HandlerFunc(s.put))))
}

// secrets tells the page which secrets are saved: they are never sent back, an
// empty field keeps them.
type secrets struct {
	Token           bool `json:"token"`
	FrigatePassword bool `json:"frigate_password"`
	MQTTPassword    bool `json:"mqtt_password"`
	WebPassword     bool `json:"web_password"`
}

func (s *Setup) get(w http.ResponseWriter, _ *http.Request) {
	v := struct {
		First      bool              `json:"first"`
		Connection config.Connection `json:"connection"`
		Secrets    secrets           `json:"secrets"`
	}{First: s.current == nil}
	if c := s.current; c != nil {
		v.Connection = *c
		v.Secrets = secrets{c.Telegram.Token != "", c.Frigate.Password != "", c.MQTT.Password != "", c.Web.Password != ""}
		v.Connection.Telegram.Token, v.Connection.Frigate.Password = "", ""
		v.Connection.MQTT.Password, v.Connection.Web.Password = "", ""
	}
	writeJSON(w, http.StatusOK, v)
}

// keep fills the secrets left empty with the saved ones, except those named in
// clear ("frigate_password"…): the page removes a saved password that way. c must
// be normalized. A password is only kept for the address it was saved for: typed
// with another address, it would go to whoever answers there.
func (s *Setup) keep(c *config.Connection, clear []string) {
	if s.current == nil {
		return
	}
	cur := *s.current
	cur.Normalize() // the saved addresses, in the same form as c's
	keep := func(dst *string, saved, name string, sameTarget bool) {
		if *dst == "" && sameTarget && !slices.Contains(clear, name) {
			*dst = saved
		}
	}
	keep(&c.Telegram.Token, s.current.Telegram.Token, "token", true)
	keep(&c.Frigate.Password, s.current.Frigate.Password, "frigate_password", sameAddress(c.Frigate.URL, cur.Frigate.URL))
	keep(&c.MQTT.Password, s.current.MQTT.Password, "mqtt_password", strings.EqualFold(c.MQTT.Broker, cur.MQTT.Broker))
	keep(&c.Web.Password, s.current.Web.Password, "web_password", true)
}

// sameAddress reports whether two URLs have the same scheme and host (port included).
func sameAddress(a, b string) bool {
	ua, err1 := url.Parse(a)
	ub, err2 := url.Parse(b)
	return err1 == nil && err2 == nil && ua.Host != "" &&
		strings.EqualFold(ua.Scheme, ub.Scheme) && strings.EqualFold(ua.Host, ub.Host)
}

// clearParam reads the secrets to leave out of a check: ?clear=mqtt_password.
func clearParam(r *http.Request) []string {
	return strings.Split(r.URL.Query().Get("clear"), ",")
}

func (s *Setup) telegram(w http.ResponseWriter, r *http.Request) {
	var c config.Connection
	if err := decodeBody(w, r, &c.Telegram); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.keep(&c, nil)
	if c.Telegram.Token == "" {
		writeErr(w, r, http.StatusBadRequest, i18n.NewError("missing bot token"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	bot, chats, err := s.probe.Telegram(ctx, strings.TrimSpace(c.Telegram.Token), requestLang(r, i18n.Default))
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	if chats == nil {
		chats = []FoundChat{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"bot": bot, "chats": chats})
}

func (s *Setup) frigate(w http.ResponseWriter, r *http.Request) {
	var c config.Connection
	if err := decodeBody(w, r, &c.Frigate); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	c.Normalize()
	s.keep(&c, clearParam(r))
	if c.Frigate.URL == "" {
		writeErr(w, r, http.StatusBadRequest, i18n.NewError("missing Frigate address"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	found, err := s.probe.Frigate(ctx, c.Frigate, requestLang(r, i18n.Default))
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, found)
}

// mqtt tries the broker as typed in the page; an empty password is the saved one.
func (s *Setup) mqtt(w http.ResponseWriter, r *http.Request) {
	var c config.Connection
	if err := decodeBody(w, r, &c.MQTT); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	c.Normalize()
	s.keep(&c, clearParam(r))
	if c.MQTT.Broker == "" {
		writeErr(w, r, http.StatusBadRequest, i18n.NewError("missing broker address"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := s.probe.MQTT(ctx, c.MQTT, requestLang(r, i18n.Default)); err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"broker": c.MQTT.Broker})
}

// discover looks for Frigate by itself, starting with the machine the page was
// opened on: Frigate often runs next to this service. Answers {"found": null}
// when there is none.
func (s *Setup) discover(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var hosts []string
	if h := requestHost(r.Host); h != "" {
		hosts = append(hosts, h)
	}
	writeJSON(w, http.StatusOK, map[string]any{"found": s.probe.Discover(ctx, hosts, requestLang(r, i18n.Default))})
}

// check tries the three connections at once; a component per connection.
func (s *Setup) check(ctx context.Context, c config.Connection, l i18n.Lang) []Component {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out := []Component{{Name: "Telegram"}, {Name: "Frigate"}, {Name: "MQTT"}}
	var wg sync.WaitGroup
	result := func(i int, detail string, err error) {
		defer wg.Done()
		out[i].State, out[i].Detail = StateOK, detail
		if err != nil {
			out[i].State, out[i].Detail = StateError, err.Error()
			var p *Problem
			if errors.As(err, &p) {
				out[i].Hint = p.Hint
			}
		}
	}
	wg.Add(3)
	go func() { bot, _, err := s.probe.Telegram(ctx, c.Telegram.Token, l); result(0, bot, err) }()
	go func() { f, err := s.probe.Frigate(ctx, c.Frigate, l); result(1, f.Version, err) }()
	go func() { result(2, c.MQTT.Broker, s.probe.MQTT(ctx, c.MQTT, l)) }()
	wg.Wait()
	return out
}

func (s *Setup) put(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Connection config.Connection `json:"connection"`
		Force      bool              `json:"force"` // save even if a connection fails
		Clear      []string          `json:"clear"` // saved secrets to remove (see keep)
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	c := req.Connection
	c.Normalize()
	s.keep(&c, req.Clear)
	c.Telegram.Token = strings.TrimSpace(c.Telegram.Token)
	if err := c.Validate(); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		writeErr(w, r, http.StatusConflict, i18n.NewError("the service is already restarting"))
		return
	}
	if !req.Force {
		checks := s.check(r.Context(), c, requestLang(r, i18n.Default))
		if slices.ContainsFunc(checks, func(c Component) bool { return c.State != StateOK }) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"checks": checks})
			return
		}
	}
	if err := config.SaveConnection(s.path, c); err != nil {
		s.log.Error("saving the connection failed", "err", err)
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.done = true
	s.log.Info("connection saved from the setup page, restarting", "file", s.path)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	s.saved()
}

// requestHost is the host name of a Host header, without port, brackets nor final dot.
func requestHost(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
}

// writeErr answers a JSON error in the language of the request.
func writeErr(w http.ResponseWriter, r *http.Request, code int, err error) {
	writeJSON(w, code, map[string]string{"error": requestLang(r, i18n.Default).Message(err)})
}

// writeProblem answers a connection that does not work, with what to change.
func writeProblem(w http.ResponseWriter, r *http.Request, err error) {
	var p *Problem
	if errors.As(err, &p) {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": p.Detail, "hint": p.Hint})
		return
	}
	writeErr(w, r, http.StatusBadGateway, err)
}
