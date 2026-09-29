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

// Limites contre les essais de mot de passe à répétition : au-delà de maxFailures
// échecs en failureWindow depuis une même adresse, elle est bloquée blockFor.
const (
	maxFailures   = 5
	failureWindow = time.Minute
	blockFor      = 5 * time.Minute
)

// Auth protège des routes par mot de passe (authentification HTTP Basic, nom
// d'utilisateur libre) et bloque temporairement une adresse qui enchaîne les échecs.
// Une seule instance doit servir toutes les routes : le décompte est commun.
type Auth struct {
	pass string
	log  *slog.Logger
	now  func() time.Time

	mu      sync.Mutex
	clients map[string]*attempts
}

type attempts struct {
	failures     int
	since        time.Time // début de la fenêtre de décompte
	blockedUntil time.Time
}

func NewAuth(pass string, log *slog.Logger) *Auth {
	return &Auth{pass: pass, log: log, now: time.Now, clients: map[string]*attempts{}}
}

// Wrap exige le mot de passe avant next.
func (a *Auth) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		l := requestLang(r, i18n.Default)
		if wait := a.blocked(ip); wait > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			http.Error(w, l.T("too many wrong passwords, try again in a few minutes",
				"trop de mots de passe erronés, réessayez dans quelques minutes"), http.StatusTooManyRequests)
			return
		}
		_, got, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(a.pass)) != 1 {
			if ok { // sans identifiants, c'est le navigateur qui demande : pas un échec
				a.fail(ip)
			}
			w.Header().Set("WWW-Authenticate", `Basic realm="frigate-telegram-enhanced", charset="UTF-8"`)
			http.Error(w, l.T("authentication required", "authentification requise"), http.StatusUnauthorized)
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

// prune oublie les adresses sans échec récent ni blocage en cours. Appelé sous a.mu.
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
