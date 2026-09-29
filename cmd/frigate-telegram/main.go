// Commande frigate-telegram : notifications Telegram pour Frigate NVR.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"frigate-telegram/internal/app"
	"frigate-telegram/internal/server"
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.Run(ctx, *configPath); err != nil {
		fmt.Fprintln(os.Stderr, "erreur:", err)
		os.Exit(1)
	}
}
