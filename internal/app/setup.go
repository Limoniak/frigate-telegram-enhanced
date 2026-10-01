package app

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"frigate-telegram-enhanced/internal/bot"
	"frigate-telegram-enhanced/internal/config"
	"frigate-telegram-enhanced/internal/frigate"
	"frigate-telegram-enhanced/internal/i18n"
	"frigate-telegram-enhanced/internal/mqttsub"
	"frigate-telegram-enhanced/internal/server"
	"frigate-telegram-enhanced/internal/telegram"
	"frigate-telegram-enhanced/internal/web"
)

// runSetup serves the setup page alone, until a connection is saved (nil) or ctx
// is canceled (nil too: the caller checks ctx).
func runSetup(ctx context.Context, connectionPath string, log *slog.Logger, tgOpts []telegram.Option) error {
	saved := make(chan struct{})
	setup := web.NewSetup(connectionPath, nil, prober{tgOpts: tgOpts}, log, func() { close(saved) })
	addr := config.SetupListen()
	// The container is healthy while it waits: nothing is broken, it waits for its setup.
	srv := server.New(addr, func() error { return nil }, http.NotFoundHandler(), setup.MountAlone)
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("not configured yet: open the web interface to set up the service", "address", addr)

	var err error
	select {
	case <-ctx.Done():
	case <-saved:
	case err = <-errc:
		return err
	}
	stopServer(srv)
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// stopServer stops the HTTP server, leaving 2 s to the requests in progress. Past
// that, the remaining connections are closed: a browser sometimes opens one in
// advance and sends nothing on it, which Shutdown would wait for up to 5 s — and
// the port must be free for the service that starts next.
func stopServer(srv *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		srv.Close()
	}
}

// prober tries the connections of the setup page.
type prober struct {
	tgOpts []telegram.Option
	// The running service's bot reads the updates of token: reading them too would
	// cut its connection. The users it refused then stand for those who wrote.
	token   string
	refused func() []bot.Refused
}

func (p prober) Telegram(ctx context.Context, token string, l i18n.Lang) (string, []web.FoundChat, error) {
	tg := telegram.New(token, p.tgOpts...)
	me, err := tg.GetMe(ctx)
	if err != nil {
		return "", nil, telegramProblem(err, true, l)
	}
	var chats []web.FoundChat
	if p.refused != nil && token == p.token {
		for _, r := range p.refused() {
			chats = append(chats, web.FoundChat{ID: r.ID, Name: personName(r.Name, r.Username, r.ID), Private: true})
		}
		return "@" + me.Username, chats, nil
	}
	// Without an offset, the updates stay with Telegram: the bot will answer these
	// /start once the service runs.
	ups, err := tg.GetUpdates(ctx, 0, 0)
	if err != nil {
		return "", nil, telegramProblem(err, true, l)
	}
	seen := map[int64]bool{}
	for _, u := range ups {
		m := u.Message
		if m == nil || seen[m.Chat.ID] {
			continue
		}
		seen[m.Chat.ID] = true
		c := web.FoundChat{ID: m.Chat.ID, Name: m.Chat.Title, Private: m.Chat.Type == "private"}
		if c.Private && m.From != nil {
			c.Name = personName(m.From.FirstName, m.From.Username, m.From.ID)
		}
		if c.Name == "" {
			c.Name = strconv.FormatInt(c.ID, 10)
		}
		chats = append(chats, c)
	}
	return "@" + me.Username, chats, nil
}

func personName(first, username string, id int64) string {
	switch {
	case first != "":
		return first
	case username != "":
		return username
	}
	return strconv.FormatInt(id, 10)
}

func (p prober) Frigate(ctx context.Context, f config.Frigate, l i18n.Lang) (web.FoundFrigate, error) {
	found := web.FoundFrigate{URL: f.URL}
	fr, err := frigate.NewClient(f)
	if err != nil {
		return found, &web.Problem{Detail: err.Error(),
			Hint: l.T("Check the address: it must point to Frigate's API (port 5000, or 8971 with authentication).")}
	}
	if found.Version, err = fr.Version(ctx); err != nil {
		return found, frigateProblem(err, f.URL, true, l)
	}
	ms, err := fr.MQTT(ctx)
	if err != nil || ms.Host == "" {
		return found, nil // the broker is then typed by hand
	}
	found.MQTT, found.BrokerState, found.BrokerError = p.broker(ctx, ms, f.URL, l)
	return found, nil
}

// broker finds Frigate's broker from here. Frigate names it as it reaches it: a
// Docker name ("mosquitto") or localhost mean nothing to this container, the
// broker is then looked for at Frigate's own address, with the same port.
func (p prober) broker(ctx context.Context, ms frigate.MQTTSettings, frigateURL string, l i18n.Lang) (config.MQTT, string, string) {
	port := ms.Port
	if port == 0 {
		port = 1883
	}
	var hosts []string
	if !loopback(ms.Host) {
		hosts = append(hosts, ms.Host)
	}
	if u, err := url.Parse(frigateURL); err == nil && u.Hostname() != "" && u.Hostname() != ms.Host {
		hosts = append(hosts, u.Hostname())
	}
	var first config.MQTT
	var lastErr error
	for i, h := range hosts {
		m := config.MQTT{Broker: net.JoinHostPort(h, strconv.Itoa(port)), Username: ms.User, TopicPrefix: ms.TopicPrefix}
		if i == 0 {
			first = m
		}
		probe := m
		probe.Broker = "tcp://" + m.Broker
		err := p.MQTT(ctx, probe, l)
		switch {
		case err == nil:
			return m, web.BrokerOK, ""
		case mqttAuthRefused(err):
			// It answers: it is the right one, it only wants the password.
			return m, web.BrokerPassword, err.Error()
		}
		lastErr = err
	}
	if lastErr == nil {
		return first, "", ""
	}
	return first, web.BrokerError, lastErr.Error()
}

func loopback(host string) bool {
	ip := net.ParseIP(host)
	return host == "localhost" || (ip != nil && ip.IsLoopback())
}

// discoverTimeout bounds the wait for each address tried by Discover.
const discoverTimeout = 3 * time.Second

func (p prober) Discover(ctx context.Context, hosts []string, l i18n.Lang) *web.FoundFrigate {
	var candidates []string
	addHost := func(h string) {
		if h != "" && !loopback(h) && !slices.Contains(candidates, h) {
			candidates = append(candidates, h)
		}
	}
	// The machine the page was opened on first (the page opened on the machine
	// itself, as localhost, is the gateway's turn), then the Docker host, the usual
	// Docker names.
	for _, h := range hosts {
		addHost(h)
	}
	addHost(defaultGateway())
	addHost("frigate")              // same Docker network
	addHost("host.docker.internal") // Docker Desktop

	// Every address at once; the first of the list that answers wins.
	versions := make([]string, len(candidates))
	var wg sync.WaitGroup
	for i, h := range candidates {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fr, err := frigate.NewClient(config.Frigate{URL: frigateAt(h)})
			if err != nil {
				return
			}
			c, cancel := context.WithTimeout(ctx, discoverTimeout)
			defer cancel()
			if v, err := fr.Version(c); err == nil {
				versions[i] = v
			}
		}()
	}
	wg.Wait()
	for i, h := range candidates {
		if versions[i] == "" {
			continue
		}
		if found, err := p.Frigate(ctx, config.Frigate{URL: frigateAt(h)}, l); err == nil {
			return &found
		}
	}
	return nil
}

// frigateAt is the address of Frigate's API without authentication on host.
func frigateAt(host string) string { return "http://" + net.JoinHostPort(host, "5000") }

// defaultGateway returns this container's default gateway (the Docker host, in
// bridge mode), read from /proc/net/route; empty if unknown.
func defaultGateway() string {
	raw, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	return parseGateway(string(raw))
}

// parseGateway reads the default route of a /proc/net/route table: destination
// 00000000, gateway in little-endian hexadecimal.
func parseGateway(table string) string {
	lines := strings.Split(table, "\n")
	for _, line := range lines[min(1, len(lines)):] {
		f := strings.Fields(line)
		if len(f) < 3 || f[1] != "00000000" {
			continue
		}
		v, err := strconv.ParseUint(f[2], 16, 32)
		if err != nil || v == 0 {
			continue
		}
		return net.IPv4(byte(v), byte(v>>8), byte(v>>16), byte(v>>24)).String()
	}
	return ""
}

func (p prober) MQTT(ctx context.Context, m config.MQTT, l i18n.Lang) error {
	if m.ClientID == "" {
		m.ClientID = "frigate-telegram-enhanced"
	}
	timeout := 5 * time.Second
	if d, ok := ctx.Deadline(); ok && time.Until(d) < timeout {
		timeout = max(time.Until(d), time.Second)
	}
	sub := mqttsub.New(m, nil, nil, slog.New(slog.DiscardHandler), nil)
	if err := sub.Probe(timeout); err != nil {
		return mqttProblem(err, m.Broker, true, l)
	}
	return nil
}
