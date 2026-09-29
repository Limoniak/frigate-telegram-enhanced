// Commande frigate-telegram : notifications Telegram pour Frigate NVR.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"frigate-telegram/internal/app"
	"frigate-telegram/internal/config"
	"frigate-telegram/internal/server"
)

func main() {
	configPath := flag.String("config", "/config/config.yml", "chemin du fichier de configuration")
	healthcheck := flag.Bool("healthcheck", false, "interroge /healthz et sort avec 0 si le service est sain")
	healthURL := flag.String("healthcheck-url", "", "URL utilisée par -healthcheck (défaut : déduite du port configuré)")
	flag.Parse()

	if *healthcheck {
		url := *healthURL
		if url == "" {
			url = healthURLFor(*configPath)
		}
		if err := server.Check(url); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.Run(ctx, *configPath); err != nil {
		fmt.Fprintln(os.Stderr, "erreur:", err)
		os.Exit(1)
	}
}

// healthURLFor déduit l'URL de /healthz du port configuré (http_listen ou
// HTTP_LISTEN), pour que la sonde du conteneur suive un port personnalisé.
func healthURLFor(configPath string) string {
	listen := config.DefaultHTTPListen
	if cfg, err := config.Load(configPath); err == nil {
		listen = cfg.HTTPListen
	}
	_, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		_, port, _ = net.SplitHostPort(config.DefaultHTTPListen)
	}
	return "http://127.0.0.1:" + port + "/healthz"
}
