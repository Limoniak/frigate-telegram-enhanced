// Commande frigate-telegram-enhanced : notifications Telegram pour Frigate NVR.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"

	"frigate-telegram-enhanced/internal/app"
	"frigate-telegram-enhanced/internal/config"
	"frigate-telegram-enhanced/internal/server"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

// run exécute la commande avec les arguments args et renvoie son code de sortie.
func run(args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("frigate-telegram-enhanced", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "/config/config.yml", "chemin du fichier de configuration")
	healthcheck := flags.Bool("healthcheck", false, "interroge /healthz et sort avec 0 si le service est sain")
	healthURL := flags.String("healthcheck-url", "", "URL utilisée par -healthcheck (défaut : déduite du port configuré)")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	if *healthcheck {
		url := *healthURL
		if url == "" {
			url = healthURLFor(*configPath)
		}
		if err := server.Check(url); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.Run(ctx, *configPath); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
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
