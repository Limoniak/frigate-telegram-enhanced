package app

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"

	"frigate-telegram-enhanced/internal/bot"
	"frigate-telegram-enhanced/internal/config"
	"frigate-telegram-enhanced/internal/frigate"
	"frigate-telegram-enhanced/internal/i18n"
	"frigate-telegram-enhanced/internal/mqttsub"
	"frigate-telegram-enhanced/internal/telegram"
	"frigate-telegram-enhanced/internal/web"
)

// probeTTL espace les connexions d'essai au broker MQTT : l'interface interroge
// l'état toutes les 30 s, inutile de marteler un broker qui refuse.
const probeTTL = 20 * time.Second

// checker diagnostique les trois connexions du service pour l'interface web : pour
// chacune, un état et, en cas de problème, la cause en clair et la variable à corriger.
type checker struct {
	cfg *config.Config
	fr  *frigate.Client
	sub *mqttsub.Subscriber
	bot *bot.Bot
	tg  *telegram.Client
	// drops : messages MQTT écartés, file pleine (le notifier) ; nil pour ne pas les signaler.
	drops interface{ Dropped() (int, time.Time) }

	mu       sync.Mutex
	probeErr error
	probeAt  time.Time
	botName  string
}

func (c *checker) check(ctx context.Context, l i18n.Lang) []web.Component {
	var wg sync.WaitGroup
	out := make([]web.Component, 3)
	wg.Add(3)
	go func() { defer wg.Done(); out[0] = c.frigate(ctx, l) }()
	go func() { defer wg.Done(); out[1] = c.mqtt(l) }()
	go func() { defer wg.Done(); out[2] = c.telegram(ctx, l) }()
	wg.Wait()
	if e, ok := c.dropped(l); ok {
		out = append(out, e)
	}
	return out
}

// dropWindow : au-delà, une perte ancienne n'est plus signalée.
const dropWindow = 24 * time.Hour

// dropped signale les messages MQTT écartés récemment, faute de place dans la file
// d'attente : des détections ont pu être perdues sans autre trace que les métriques.
func (c *checker) dropped(l i18n.Lang) (web.Component, bool) {
	if c.drops == nil {
		return web.Component{}, false
	}
	n, last := c.drops.Dropped()
	if n == 0 || time.Since(last) > dropWindow {
		return web.Component{}, false
	}
	loc := c.cfg.Location
	if loc == nil {
		loc = time.Local
	}
	return web.Component{
		Name:  l.T("Events"),
		State: web.StateWarn,
		Detail: l.Tf("%d messages from Frigate ignored since startup, queue full (latest at %s).",
			n, last.In(loc).Format("15:04")),
		Hint: l.T("Detections may have been missed. Frigate sends more messages than the service can handle: check the machine's load, or reduce the number of tracked objects."),
	}, true
}

func (c *checker) frigate(ctx context.Context, l i18n.Lang) web.Component {
	comp := web.Component{Name: "Frigate"}
	v, err := c.fr.Version(ctx)
	if err == nil {
		comp.State, comp.Detail = web.StateOK, l.Tf("version %s · %s", v, c.cfg.Frigate.URL)
		return comp
	}
	comp.State = web.StateError
	var he *frigate.HTTPError
	var ue x509.UnknownAuthorityError
	switch {
	case errors.Is(err, frigate.ErrAuth):
		comp.Detail = l.T("Frigate refused the username or password.")
		comp.Hint = l.T("Check FRIGATE_USERNAME and FRIGATE_PASSWORD.")
	case errors.As(err, &he) && he.Status == 401:
		comp.Detail = l.T("Frigate requires authentication.")
		comp.Hint = l.T("Set FRIGATE_USERNAME and FRIGATE_PASSWORD, or use port 5000 (no authentication).")
	case errors.As(err, &he):
		comp.Detail = l.Tf("Frigate answers HTTP %d at %s.", he.Status, c.cfg.Frigate.URL)
		comp.Hint = l.T("Check FRIGATE_URL: it must point to Frigate's API (port 5000, or 8971 with authentication).")
	case errors.As(err, &ue) || strings.Contains(err.Error(), "x509"):
		comp.Detail = l.T("Frigate's HTTPS certificate is not trusted.")
		comp.Hint = l.T("For a self-signed certificate, set FRIGATE_INSECURE_SKIP_VERIFY=true.")
	default:
		comp.Detail = l.Tf("Frigate is unreachable at %s (%s).", c.cfg.Frigate.URL, netCause(err, l))
		comp.Hint = unreachableHint("FRIGATE_URL", l)
	}
	return comp
}

func (c *checker) mqtt(l i18n.Lang) web.Component {
	comp := web.Component{Name: "MQTT"}
	if c.sub.Connected() {
		comp.State, comp.Detail = web.StateOK, c.cfg.MQTT.Broker
		return comp
	}
	c.mu.Lock()
	if time.Since(c.probeAt) > probeTTL {
		c.probeErr, c.probeAt = c.sub.Probe(5*time.Second), time.Now()
	}
	err := c.probeErr
	c.mu.Unlock()

	if err == nil {
		// Le broker accepte une connexion : le client principal s'y reconnecte.
		comp.State = web.StatePending
		comp.Detail = l.T("The broker accepts the connection; reconnecting…")
		return comp
	}
	comp.State = web.StateError
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "not authorized") || strings.Contains(msg, "bad user name or password"):
		comp.Detail = l.T("The broker refused the username or password.")
		comp.Hint = l.T("Check MQTT_USERNAME and MQTT_PASSWORD (the same as in Frigate's mqtt section).")
	case strings.Contains(msg, "identifier rejected"):
		comp.Detail = l.T("The broker rejected the client ID.")
		comp.Hint = l.T("Set another MQTT_CLIENT_ID.")
	default:
		comp.Detail = l.Tf("The broker is unreachable at %s (%s).", c.cfg.MQTT.Broker, netCause(err, l))
		comp.Hint = unreachableHint("MQTT_BROKER", l)
	}
	return comp
}

func (c *checker) telegram(ctx context.Context, l i18n.Lang) web.Component {
	comp := web.Component{Name: "Telegram"}
	err := c.bot.PollError()
	if err == nil {
		// Aucune erreur de lecture : on vérifie le token tout de suite, sans attendre
		// la fin du premier long polling (jusqu'à 50 s).
		var name string
		if name, err = c.name(ctx); err == nil {
			comp.State, comp.Detail = web.StateOK, name
			return comp
		}
	}
	comp.State = web.StateError
	var ae *telegram.APIError
	switch {
	case errors.As(err, &ae) && (ae.Code == 401 || ae.Code == 404):
		comp.Detail = l.T("Telegram refused the bot token.")
		comp.Hint = l.T("Check TELEGRAM_TOKEN: copy it again from @BotFather (/mybots → API Token).")
	case errors.As(err, &ae) && ae.Code == 409:
		comp.Detail = l.T("Another program is already using this bot.")
		comp.Hint = l.T("Stop the other instance (or remove its webhook): a bot can only be read by one program.")
	default:
		comp.Detail = l.Tf("Telegram is unreachable (%s).", netCause(err, l))
		comp.Hint = l.T("Check that the container can reach the Internet (api.telegram.org).")
	}
	return comp
}

// name renvoie le @nom du bot, lu auprès de Telegram puis gardé en mémoire.
func (c *checker) name(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.botName != "" {
		return c.botName, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	u, err := c.tg.GetMe(ctx)
	if err != nil {
		return "", err
	}
	c.botName = "@" + u.Username
	return c.botName, nil
}

// netCause résume une erreur réseau en quelques mots. Les erreurs système sont
// reconnues par leur code, le texte variant d'un système à l'autre (Windows écrit
// « actively refused »), puis par leur texte quand une bibliothèque les a aplaties.
func netCause(err error, l i18n.Lang) string {
	var dnsErr *net.DNSError
	msg := strings.ToLower(err.Error())
	switch {
	case errors.As(err, &dnsErr) || strings.Contains(msg, "no such host"):
		return l.T("unknown host name")
	case errors.Is(err, syscall.ECONNREFUSED) || strings.Contains(msg, "connection refused") || strings.Contains(msg, "actively refused"):
		return l.T("connection refused")
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(msg, "timeout") || strings.Contains(msg, "timed out"):
		return l.T("no answer")
	case errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH) ||
		strings.Contains(msg, "no route to host") || strings.Contains(msg, "network is unreachable"):
		return l.T("no route to this address")
	}
	return err.Error()
}

func unreachableHint(variable string, l i18n.Lang) string {
	return l.Tf("Check %s. In Docker, use the machine's IP address rather than localhost, which is the container itself.", variable)
}
