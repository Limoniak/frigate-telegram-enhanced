package web

import (
	"crypto/subtle"
	"log/slog"
	"net"
	"net/http"
	"strconv"
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

// Auth protects routes with a password (HTTP Basic authentication, any user name)
// and temporarily blocks an address that piles up failures. A single instance must
// serve every route: the count is shared.
type Auth struct {
	pass string
	log  *slog.Logger
	now  func() time.Time

	mu      sync.Mutex
	clients map[string]*attempts
}

type attempts struct {
	failures     int
	since        time.Time // start of the counting window
	blockedUntil time.Time
}

func NewAuth(pass string, log *slog.Logger) *Auth {
	return &Auth{pass: pass, log: log, now: time.Now, clients: map[string]*attempts{}}
}

// Wrap requires the password before next.
func (a *Auth) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		l := requestLang(r, i18n.Default)
		if wait := a.blocked(ip); wait > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			http.Error(w, l.T("too many wrong passwords, try again in a few minutes"), http.StatusTooManyRequests)
			return
		}
		_, got, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(a.pass)) != 1 {
			if ok { // without credentials, it is the browser asking: not a failure
				a.fail(ip)
			}
			w.Header().Set("WWW-Authenticate", `Basic realm="frigate-telegram-enhanced", charset="UTF-8"`)
			http.Error(w, l.T("authentication required"), http.StatusUnauthorized)
			return
		}
		a.succeed(ip)
		next.ServeHTTP(w, r)
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
