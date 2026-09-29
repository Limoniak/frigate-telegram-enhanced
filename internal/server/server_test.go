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
		return errors.New("MQTT déconnecté")
	}, http.NotFoundHandler())
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	if err := Check(ts.URL + "/healthz"); err != nil {
		t.Fatalf("sain : %v", err)
	}
	healthy.Store(false)
	err := Check(ts.URL + "/healthz")
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("err = %v, attendu 503", err)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := prometheus.NewCounter(prometheus.CounterOpts{Name: "ft_test_total", Help: "test"})
	reg.MustRegister(c)
	c.Inc()
	ts := httptest.NewServer(New(":0", func() error { return nil }, promhttp.HandlerFor(reg, promhttp.HandlerOpts{})).Handler)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "ft_test_total 1") {
		t.Errorf("métriques :\n%s", body)
	}
}
