// Package app assemble les composants du service et orchestre son démarrage et son arrêt.
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

// Run démarre le service et le fait tourner jusqu'à l'annulation de ctx, puis
// l'arrête proprement : envois en cours terminés (ou interrompus après un délai),
// état sauvegardé, serveur HTTP fermé. tgOpts permet aux tests de rediriger l'API
// Telegram vers un faux serveur.
func Run(ctx context.Context, configPath string, tgOpts ...telegram.Option) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)
	slog.SetDefault(log)

	// Les réglages enregistrés par l'interface web priment sur les sections notify
	// et cameras de config.yml. Un fichier illisible ou devenu invalide (un chat
	// renommé entre-temps, par exemple) ne doit pas empêcher le service de démarrer :
	// on le signale et on repart sur config.yml.
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
	// lostAt : début de la coupure MQTT en cours (UnixNano), 0 si connecté. À la
	// reconnexion, le notifier rattrape auprès de Frigate ce qui a été manqué.
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
	var protect func(http.Handler) http.Handler // mot de passe de l'interface, décompte d'échecs commun
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

	// notif.Run est suivi via runDone : Shutdown appelle wg.Wait, et Process (appelé
	// depuis Run) appelle wg.Add — les deux ne doivent jamais s'exécuter en même temps
	// (sync.WaitGroup l'interdit lorsque le compteur repart de zéro). On attend donc la
	// fin effective de Run avant d'appeler notif.Shutdown.
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
	_ = l.UnmarshalText([]byte(level)) // niveau déjà validé par la config
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l}))
}

// presenceRouter aiguille les messages MQTT : ceux des topics de présence mettent à
// jour qui est à la maison, les autres vont au notifier (next).
func presenceRouter(p config.Presence, st *state.Store, log *slog.Logger, next mqttsub.Handler) mqttsub.Handler {
	return func(topic string, payload []byte) {
		for _, f := range p.Topics {
			if mqttsub.Match(f, topic) {
				// Home Assistant publie l'état brut ("home"), parfois entre guillemets.
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
