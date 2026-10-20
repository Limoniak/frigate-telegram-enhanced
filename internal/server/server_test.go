package server

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestHealthzAndCheck(t *testing.T) {
	var healthy atomic.Bool
	healthy.Store(true)
	srv := New(":0", func() error {
		if healthy.Load() {
			return nil
		}
		return errors.New("MQTT disconnected")
	}, http.NotFoundHandler(), nil)
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	if err := Check(ts.URL + "/healthz"); err != nil {
		t.Fatalf("healthy: %v", err)
	}
	healthy.Store(false)
	err := Check(ts.URL + "/healthz")
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("err = %v, want 503", err)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := prometheus.NewCounter(prometheus.CounterOpts{Name: "ft_test_total", Help: "test"})
	reg.MustRegister(c)
	c.Inc()
	ts := httptest.NewServer(New(":0", func() error { return nil }, promhttp.HandlerFor(reg, promhttp.HandlerOpts{}), nil).Handler)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "ft_test_total 1") {
		t.Errorf("metrics:\n%s", body)
	}
}

func TestHeaders(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})
	for _, c := range []struct {
		frames   []string
		csp, xfo string
	}{
		{nil, "frame-ancestors 'self'", "SAMEORIGIN"},
		{[]string{"https://HA.example.com:8123/lovelace/0", "http://homeassistant.local:8123", "https://ha.example.com:8123"},
			"frame-ancestors 'self' https://ha.example.com:8123 http://homeassistant.local:8123", ""},
		{[]string{"", "javascript:alert(1)", "ha.local"}, "frame-ancestors 'self'", "SAMEORIGIN"},
	} {
		rec := httptest.NewRecorder()
		Headers(ok, func() []string { return c.frames }).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		h := rec.Header()
		if h.Get("Content-Security-Policy") != c.csp || h.Get("X-Frame-Options") != c.xfo ||
			h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") != "same-origin" {
			t.Errorf("frames %q: headers %v", c.frames, h)
		}
	}
}
