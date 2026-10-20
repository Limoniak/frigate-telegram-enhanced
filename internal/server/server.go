// Package server serves /healthz, /metrics and, when enabled, the web interface.
package server

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// New builds the HTTP server. metrics serves /metrics (possibly already behind a
// password). frames gives the addresses of the sites that may show the interface in
// a frame (nil: none but the service itself). Each mount function gets the router to
// add its own routes — the web interface plugs in without this package having to
// know it. Every response carries the headers of Headers.
func New(addr string, health func() error, metrics http.Handler, frames func() []string, mount ...func(*http.ServeMux)) *http.Server {
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
	return &http.Server{Addr: addr, Handler: Headers(mux, frames), ReadHeaderTimeout: 5 * time.Second}
}

// Headers adds the headers every response of the service carries: no content-type
// sniffing, no referrer sent beyond the service, and framing only by the service
// itself and the sites frames lists (Home Assistant, whose dashboards can show the
// interface in an iframe). Any other site framing it — to make someone click a
// switch without knowing it — gets a blank frame.
func Headers(next http.Handler, frames func() []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		ancestors := []string{"'self'"}
		if frames != nil {
			for _, f := range frames() {
				if o := Origin(f); o != "" && !slices.Contains(ancestors, o) {
					ancestors = append(ancestors, o)
				}
			}
		}
		if len(ancestors) == 1 {
			h.Set("X-Frame-Options", "SAMEORIGIN") // for browsers without frame-ancestors
		}
		h.Set("Content-Security-Policy", "frame-ancestors "+strings.Join(ancestors, " "))
		next.ServeHTTP(w, r)
	})
}

// Origin is the scheme and host of an http(s) address, lower-cased; empty for
// anything else.
func Origin(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
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
