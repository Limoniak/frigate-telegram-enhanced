package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const minimalConfig = `
frigate: {url: "http://frigate:5000"}
mqtt: {broker: "tcp://mqtt:1883"}
telegram: {token: t, admins: [1], chats: {moi: 1}}
`

func writeConfig(t *testing.T, extra string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(minimalConfig+extra), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHealthURLFollowsTheConfiguredPort(t *testing.T) {
	path := writeConfig(t, `http_listen: "0.0.0.0:9000"`+"\n")
	if got, want := healthURLFor(path, ""), "http://127.0.0.1:9000/healthz"; got != want {
		t.Errorf("healthURLFor = %q, want %q", got, want)
	}
}

func TestHealthURLFallsBackToTheDefaultPort(t *testing.T) {
	// Without a file, the configuration comes from the environment: incomplete here.
	t.Setenv("TELEGRAM_TOKEN", "")
	cases := map[string]string{
		"configuration illisible": filepath.Join(t.TempDir(), "absent.yml"),
		"port absent":             writeConfig(t, `http_listen: "127.0.0.1:"`+"\n"),
		"default port":            writeConfig(t, ""),
	}
	for name, path := range cases {
		if got, want := healthURLFor(path, ""), "http://127.0.0.1:8431/healthz"; got != want {
			t.Errorf("%s: healthURLFor = %q, want %q", name, got, want)
		}
	}
}

func TestHealthcheckExitCode(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
	defer srv.Close()
	var stderr strings.Builder
	if code := run([]string{"-healthcheck", "-healthcheck-url", srv.URL}, &stderr); code != 0 {
		t.Errorf("healthy service: code %d (%s)", code, stderr.String())
	}
	status = http.StatusServiceUnavailable
	if code := run([]string{"-healthcheck", "-healthcheck-url", srv.URL}, &stderr); code != 1 || stderr.Len() == 0 {
		t.Errorf("unhealthy service: code %d, output %q", code, stderr.String())
	}
}

func TestInvalidConfigurationExitsWithError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	os.WriteFile(path, []byte("frigate: [not a section"), 0o600)
	var stderr strings.Builder
	if code := run([]string{"-config", path}, &stderr); code != 1 || !strings.Contains(stderr.String(), "error:") {
		t.Errorf("code %d, output %q", code, stderr.String())
	}
}

func TestUnknownFlag(t *testing.T) {
	var stderr strings.Builder
	if code := run([]string{"-inconnu"}, &stderr); code != 2 {
		t.Errorf("code %d, want 2", code)
	}
}
