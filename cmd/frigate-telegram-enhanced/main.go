// Command frigate-telegram-enhanced: Telegram notifications for Frigate NVR.
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

// run runs the command with the arguments args and returns its exit code.
func run(args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("frigate-telegram-enhanced", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "/config/config.yml", "path of the configuration file")
	connectionPath := flags.String("connection", config.DefaultConnectionPath, "path of the connection saved by the setup page")
	healthcheck := flags.Bool("healthcheck", false, "query /healthz and exit with 0 if the service is healthy")
	healthURL := flags.String("healthcheck-url", "", "URL used by -healthcheck (default: derived from the configured port)")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	if *healthcheck {
		url := *healthURL
		if url == "" {
			url = healthURLFor(*configPath, *connectionPath)
		}
		if err := server.Check(url); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.Run(ctx, *configPath, *connectionPath); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}

// healthURLFor derives the /healthz URL from the configured port (http_listen or
// HTTP_LISTEN), so that the container probe follows a custom port.
func healthURLFor(configPath, connectionPath string) string {
	listen := config.SetupListen()
	if cfg, err := config.Load(configPath, connectionPath); err == nil {
		listen = cfg.HTTPListen
	}
	_, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		_, port, _ = net.SplitHostPort(config.DefaultHTTPListen)
	}
	return "http://127.0.0.1:" + port + "/healthz"
}
