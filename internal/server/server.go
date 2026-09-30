// Package server serves /healthz, /metrics and, when enabled, the web interface.
package server

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// New builds the HTTP server. metrics serves /metrics (possibly already behind a
// password). Each mount function gets the router to add its own routes — the web
// interface plugs in without this package having to know it.
func New(addr string, health func() error, metrics http.Handler, mount ...func(*http.ServeMux)) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		if err := health(); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		io.WriteString(w, "ok\n")
	})
	mux.Handle("GET /metrics", metrics)
	for _, m := range mount {
		m(mux)
	}
	return &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
}

// Check queries url and returns nil if the response is 200 (used by -healthcheck).
func Check(url string) error {
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
