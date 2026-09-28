// Commande frigate-telegram : notifications Telegram pour Frigate NVR.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"frigate-telegram/internal/bot"
	"frigate-telegram/internal/config"
	"frigate-telegram/internal/filter"
	"frigate-telegram/internal/frigate"
	"frigate-telegram/internal/metrics"
	"frigate-telegram/internal/mqttsub"
	"frigate-telegram/internal/notifier"
	"frigate-telegram/internal/server"
	"frigate-telegram/internal/state"
	"frigate-telegram/internal/telegram"
)

func main() {
	configPath := flag.String("config", "/config/config.yml", "chemin du fichier de configuration")
	healthcheck := flag.Bool("healthcheck", false, "interroge /healthz et sort avec 0 si le service est sain")
	healthURL := flag.String("healthcheck-url", "http://127.0.0.1:8080/healthz", "URL utilisée par -healthcheck")
	flag.Parse()

	if *healthcheck {
		if err := server.Check(*healthURL); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := run(*configPath); err != nil {
		fmt.Fprintln(os.Stderr, "erreur:", err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	m := metrics.New()
	st, err := state.Load(cfg.StateFile, time.Now())
	if err != nil {
		log.Warn("état précédent ignoré", "err", err)
	}
	fr, err := frigate.NewClient(cfg.Frigate)
	if err != nil {
		return err
	}
	tg := telegram.New(cfg.Telegram.Token, telegram.WithErrorHook(func(method string, code int) {
		m.TelegramErrors.WithLabelValues(method, strconv.Itoa(code)).Inc()
	}))

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

	srv := server.New(cfg.HTTPListen, func() error {
		if !sub.Connected() {
			return errors.New("MQTT déconnecté")
		}
		if time.Since(b.LastPoll()) > 2*time.Minute {
			return errors.New("Telegram injoignable")
		}
		return nil
	}, m.Registry)
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
	log.Info("frigate-telegram démarré", "mode", cfg.Mode, "broker", cfg.MQTT.Broker, "frigate", cfg.Frigate.URL)

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
