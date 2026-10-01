package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"frigate-telegram-enhanced/internal/i18n"
)

// Limits against repeated password attempts: beyond maxFailures failures in
// failureWindow from one address, it is blocked for blockFor.
const (
	maxFailures   = 5
	failureWindow = time.Minute
	blockFor      = 5 * time.Minute
)

// Auth protects routes with a password and temporarily blocks an address that piles
// up failures. In a browser, the password is typed once on the login page, which
// opens a session (a signed cookie); scripts (a Prometheus scrape, curl) can send it
// as HTTP Basic credentials, any user name. A single instance must serve every
// route: the count is shared.
type Auth struct {
	pass   string
	secret []byte // signs the sessions
	log    *slog.Logger
	now    func() time.Time

	mu      sync.Mutex
	clients map[string]*attempts
}

type attempts struct {
	failures     int
	since        time.Time // start of the counting window
	blockedUntil time.Time
}

// sessionCookie holds the session; it lasts sessionFor.
const (
	sessionCookie = "fte_session"
	sessionFor    = 30 * 24 * time.Hour
)

// NewAuth protects with pass. secret signs the sessions: kept on disk, they survive
// restarts (see LoadSessionKey); nil, a random one is drawn and a restart asks for
// the password again.
func NewAuth(pass string, secret []byte, log *slog.Logger) *Auth {
	if len(secret) == 0 {
		secret = make([]byte, 32)
		rand.Read(secret)
	}
	return &Auth{pass: pass, secret: secret, log: log, now: time.Now, clients: map[string]*attempts{}}
}

// LoadSessionKey reads the key that signs the sessions, creating it the first time.
func LoadSessionKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err == nil && len(key) >= 32 {
		return key, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key = make([]byte, 32)
	rand.Read(key)
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// sign returns the signature of a session ending at exp. The password is part of
// it: changing the password closes every session.
func (a *Auth) sign(exp int64) string {
	pass := sha256.Sum256([]byte(a.pass))
	mac := hmac.New(sha256.New, a.secret)
	fmt.Fprintf(mac, "session|%d|%x", exp, pass)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// hasSession reports a valid session cookie.
func (a *Auth) hasSession(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	expText, sig, ok := strings.Cut(c.Value, ".")
	exp, err := strconv.ParseInt(expText, 10, 64)
	if !ok || err != nil || a.now().Unix() > exp {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(a.sign(exp)))
}

// openSession sets the session cookie.
func (a *Auth) openSession(w http.ResponseWriter, r *http.Request) {
	exp := a.now().Add(sessionFor).Unix()
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: strconv.FormatInt(exp, 10) + "." + a.sign(exp),
		Path: "/", MaxAge: int(sessionFor.Seconds()), HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})
}

func closeSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
}

// check tells whether the password is right, counting the failures of ip.
func (a *Auth) check(ip, given string) bool {
	if subtle.ConstantTimeCompare([]byte(given), []byte(a.pass)) != 1 {
		a.fail(ip)
		return false
	}
	a.succeed(ip)
	return true
}

// Wrap requires a session (or the password as Basic credentials) before next. A page
// opened without one leads to the login page; the rest answers 401.
func (a *Auth) Wrap(next http.Handler) http.Handler { return a.wrap(next, false) }

// WrapBasic does the same for a route read by programs (/metrics): without
// credentials, it asks for them the HTTP way.
func (a *Auth) WrapBasic(next http.Handler) http.Handler { return a.wrap(next, true) }

func (a *Auth) wrap(next http.Handler, challenge bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		l := requestLang(r, i18n.Default)
		if wait := a.blocked(ip); wait > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			http.Error(w, l.T("too many wrong passwords, try again in a few minutes"), http.StatusTooManyRequests)
			return
		}
		if a.hasSession(r) {
			next.ServeHTTP(w, r)
			return
		}
		if _, given, ok := r.BasicAuth(); ok && a.check(ip, given) {
			next.ServeHTTP(w, r)
			return
		}
		switch {
		case challenge:
			w.Header().Set("WWW-Authenticate", `Basic realm="frigate-telegram-enhanced", charset="UTF-8"`)
		case r.Method == http.MethodGet && strings.Contains(r.Header.Get("Accept"), "text/html"):
			// A page: the login page, back here afterwards.
			http.Redirect(w, r, "login?next="+url.QueryEscape(strings.TrimPrefix(r.URL.Path, "/")), http.StatusSeeOther)
			return
		}
		http.Error(w, l.T("authentication required"), http.StatusUnauthorized)
	})
}

func (a *Auth) blocked(ip string) time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	if c := a.clients[ip]; c != nil {
		return c.blockedUntil.Sub(a.now())
	}
	return 0
}

func (a *Auth) fail(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	a.prune(now)
	c := a.clients[ip]
	if c == nil || now.Sub(c.since) > failureWindow {
		c = &attempts{since: now}
		a.clients[ip] = c
	}
	c.failures++
	if c.failures >= maxFailures {
		c.blockedUntil, c.failures, c.since = now.Add(blockFor), 0, now
		if a.log != nil {
			a.log.Warn("too many wrong passwords, address blocked", "ip", ip, "for", blockFor)
		}
	}
}

func (a *Auth) succeed(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.clients, ip)
}

// prune forgets the addresses with no recent failure nor ongoing block. Called under a.mu.
func (a *Auth) prune(now time.Time) {
	for ip, c := range a.clients {
		if now.Sub(c.since) > failureWindow && now.After(c.blockedUntil) {
			delete(a.clients, ip)
		}
	}
}

func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
