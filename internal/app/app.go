// Package app assembles the components of the service and orchestrates its startup and shutdown.
package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"frigate-telegram-enhanced/internal/bot"
	"frigate-telegram-enhanced/internal/config"
	"frigate-telegram-enhanced/internal/filter"
	"frigate-telegram-enhanced/internal/frigate"
	"frigate-telegram-enhanced/internal/metrics"
	"frigate-telegram-enhanced/internal/mqttsub"
	"frigate-telegram-enhanced/internal/notifier"
	"frigate-telegram-enhanced/internal/server"
	"frigate-telegram-enhanced/internal/state"
	"frigate-telegram-enhanced/internal/telegram"
	"frigate-telegram-enhanced/internal/web"
)

// Run starts the service and runs it until ctx is canceled, then stops it
// cleanly: sends in progress finished (or interrupted after a delay), state
// saved, HTTP server closed. tgOpts lets tests redirect the Telegram API to a
// fake server.
func Run(ctx context.Context, configPath string, tgOpts ...telegram.Option) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)
	slog.SetDefault(log)

	// The settings saved by the web interface take precedence over the notify and
	// cameras sections of config.yml. An unreadable file, or one that became invalid
	// (a chat renamed in the meantime, for example), must not prevent the service from
	// starting: it is reported and the service falls back to config.yml.
	overlayPath := filepath.Join(filepath.Dir(cfg.StateFile), "notify.yml")
	if o, err := config.LoadOverlay(overlayPath); err != nil {
		log.Warn("web interface settings ignored", "file", overlayPath, "err", err)
	} else if o != nil {
		if err := cfg.ApplyOverlay(o); err != nil {
			log.Warn("web interface settings ignored", "file", overlayPath, "err", err)
		} else {
			log.Info("notification settings loaded", "file", overlayPath)
		}
	}

	m := metrics.New()
	st, err := state.Load(cfg.StateFile, time.Now())
	if err != nil {
		log.Warn("previous state ignored", "err", err)
	}
	fr, err := frigate.NewClient(cfg.Frigate)
	if err != nil {
		return err
	}
	tg := telegram.New(cfg.Telegram.Token, append([]telegram.Option{telegram.WithErrorHook(func(method string, code int) {
		m.TelegramErrors.WithLabelValues(method, strconv.Itoa(code)).Inc()
	})}, tgOpts...)...)

	notif := notifier.New(notifier.Deps{
		Config: cfg, Engine: filter.New(cfg, st), State: st,
		Frigate: fr, Telegram: tg, Metrics: m, Log: log,
		HistoryFile: filepath.Join(filepath.Dir(cfg.StateFile), "history.json"),
	})
	topics, handle := notif.Topics(), notif.Handle
	if cfg.Presence.Enabled() {
		topics = append(topics, cfg.Presence.Topics...)
		handle = presenceRouter(cfg.Presence, st, log, notif.Handle)
		log.Info("presence tracked", "topics", cfg.Presence.Topics)
	}
	// lostAt: start of the current MQTT outage (UnixNano), 0 when connected. On
	// reconnection, the notifier catches up from Frigate on what was missed.
	var lostAt atomic.Int64
	sub := mqttsub.New(cfg.MQTT, topics, handle, log, func(up bool) {
		if up {
			m.MQTTConnected.Set(1)
			if lost := lostAt.Swap(0); lost != 0 {
				go notif.CatchUp(ctx, time.Unix(0, lost))
			}
		} else {
			m.MQTTConnected.Set(0)
			lostAt.CompareAndSwap(0, time.Now().UnixNano())
		}
	})
	b := bot.New(bot.Deps{
		Config: cfg, Telegram: tg, Frigate: fr, Notifier: notif, State: st,
		Log: log, MQTTConnected: sub.Connected,
	})

	var mount []func(*http.ServeMux)
	var protect func(http.Handler) http.Handler // interface password, shared failure count
	if cfg.Web.Enabled {
		chk := &checker{cfg: cfg, fr: fr, sub: sub, bot: b, tg: tg, drops: notif}
		ui := web.New(cfg, overlayPath, fr, log, web.WithTester(notif), web.WithState(st), web.WithHistory(notif, fr),
			web.WithHealth(chk.check), web.WithRefused(b))
		mount = append(mount, ui.Mount)
		protect = ui.Protect
	}
	var metricsHandler http.Handler = promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{})
	if cfg.Web.ProtectMetrics {
		if protect == nil {
			protect = web.NewAuth(cfg.Web.Password, log).Wrap
		}
		metricsHandler = protect(metricsHandler)
	}
	srv := server.New(cfg.HTTPListen, func() error {
		if !sub.Connected() {
			return errors.New("MQTT disconnected")
		}
		if time.Since(b.LastPoll()) > 2*time.Minute {
			return errors.New("no answer from Telegram for more than 2 min")
		}
		return nil
	}, metricsHandler, mount...)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("HTTP server stopped", "err", err)
		}
	}()

	// notif.Run is tracked through runDone: Shutdown calls wg.Wait, and Process (called
	// from Run) calls wg.Add — the two must never run at the same time (sync.WaitGroup
	// forbids it when the counter restarts from zero). So we wait for Run to actually
	// finish before calling notif.Shutdown.
	runDone := make(chan struct{})
	go func() {
		notif.Run(ctx)
		close(runDone)
	}()
	botDone := make(chan struct{})
	go func() {
		b.Run(ctx)
		close(botDone)
	}()
	sub.Start()
	log.Info("frigate-telegram-enhanced started", "configuration", cfg.Source, "mode", cfg.Mode, "broker", cfg.MQTT.Broker, "frigate", cfg.Frigate.URL)
	if cfg.Web.Enabled {
		log.Info("web interface available", "address", cfg.HTTPListen,
			"authentication", cfg.Web.Password != "")
	}

	flush := time.NewTicker(30 * time.Second)
	defer flush.Stop()
	for running := true; running; {
		select {
		case <-ctx.Done():
			running = false
		case <-flush.C:
			if err := st.FlushIfDirty(); err != nil {
				log.Warn("saving the state failed", "err", err)
			}
			if err := notif.FlushHistory(); err != nil {
				log.Warn("saving the recent activity failed", "err", err)
			}
		}
	}

	log.Info("shutting down…")
	sub.Stop()
	if err := st.Save(); err != nil {
		log.Warn("saving the state failed", "err", err)
	}
	<-runDone
	if !notif.Shutdown(10 * time.Second) {
		log.Warn("some notifications were interrupted by the shutdown")
	}
	<-botDone
	if err := st.Save(); err != nil {
		log.Warn("saving the state failed", "err", err)
	}
	if err := notif.FlushHistory(); err != nil {
		log.Warn("saving the recent activity failed", "err", err)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	_ = l.UnmarshalText([]byte(level)) // level already validated by the configuration
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l}))
}

// presenceRouter dispatches MQTT messages: those of presence topics update who is
// home, the others go to the notifier (next).
func presenceRouter(p config.Presence, st *state.Store, log *slog.Logger, next mqttsub.Handler) mqttsub.Handler {
	return func(topic string, payload []byte) {
		for _, f := range p.Topics {
			if mqttsub.Match(f, topic) {
				// Home Assistant publishes the raw state ("home"), sometimes in quotes.
				v := strings.ToLower(strings.Trim(strings.TrimSpace(string(payload)), `"`))
				home := slices.Contains(p.HomeValues, v)
				st.SetPresence(topic, home)
				log.Debug("presence", "topic", topic, "value", v, "home", home)
				return
			}
		}
		next(topic, payload)
	}
}
