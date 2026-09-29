// Package app assemble les composants du service et orchestre son démarrage et son arrêt.
package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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
		log.Warn("réglages de l'interface web ignorés", "fichier", overlayPath, "err", err)
	} else if o != nil {
		if err := cfg.ApplyOverlay(o); err != nil {
			log.Warn("réglages de l'interface web ignorés", "fichier", overlayPath, "err", err)
		} else {
			log.Info("réglages de notification chargés", "fichier", overlayPath)
		}
	}

	m := metrics.New()
	st, err := state.Load(cfg.StateFile, time.Now())
	if err != nil {
		log.Warn("état précédent ignoré", "err", err)
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
	})
	sub := mqttsub.New(cfg.MQTT, notif.Topics(), notif.Handle, log, func(up bool) {
		if up {
			m.MQTTConnected.Set(1)
		} else {
			m.MQTTConnected.Set(0)
		}
	})
	b := bot.New(bot.Deps{
		Config: cfg, Telegram: tg, Frigate: fr, Notifier: notif, State: st,
		Log: log, MQTTConnected: sub.Connected,
	})

	var mount []func(*http.ServeMux)
	if cfg.Web.Enabled {
		ui := web.New(cfg, overlayPath, fr, log, web.WithTester(notif), web.WithState(st), web.WithHistory(notif, fr))
		mount = append(mount, ui.Mount)
	}
	var metricsHandler http.Handler = promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{})
	if cfg.Web.ProtectMetrics {
		metricsHandler = web.BasicAuth(cfg.Web.Password, metricsHandler)
	}
	srv := server.New(cfg.HTTPListen, func() error {
		if !sub.Connected() {
			return errors.New("MQTT déconnecté")
		}
		if time.Since(b.LastPoll()) > 2*time.Minute {
			return errors.New("aucune réponse de Telegram depuis plus de 2 min")
		}
		return nil
	}, metricsHandler, mount...)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("serveur HTTP arrêté", "err", err)
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
	log.Info("frigate-telegram-enhanced démarré", "configuration", cfg.Source, "mode", cfg.Mode, "broker", cfg.MQTT.Broker, "frigate", cfg.Frigate.URL)
	if cfg.Web.Enabled {
		log.Info("interface web disponible", "adresse", cfg.HTTPListen,
			"authentification", cfg.Web.Password != "")
	}

	flush := time.NewTicker(30 * time.Second)
	defer flush.Stop()
	for running := true; running; {
		select {
		case <-ctx.Done():
			running = false
		case <-flush.C:
			if err := st.FlushIfDirty(); err != nil {
				log.Warn("sauvegarde de l'état échouée", "err", err)
			}
		}
	}

	log.Info("arrêt en cours…")
	sub.Stop()
	if err := st.Save(); err != nil {
		log.Warn("sauvegarde de l'état échouée", "err", err)
	}
	<-runDone
	if !notif.Shutdown(10 * time.Second) {
		log.Warn("des envois ont été interrompus à l'arrêt")
	}
	<-botDone
	if err := st.Save(); err != nil {
		log.Warn("sauvegarde de l'état échouée", "err", err)
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
