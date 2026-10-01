package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"frigate-telegram-enhanced/internal/telegram"
)

// fakeTelegram answers the Bot API: the first getUpdates delivers a /pause command,
// the next ones long-poll until the request is canceled.
func fakeTelegram(t *testing.T, polled chan<- struct{}) *httptest.Server {
	t.Helper()
	var polls atomic.Int32
	stop := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read the body: without it, the server does not notice the client closing the
		// connection and r.Context() is never canceled.
		io.Copy(io.Discard, r.Body)
		method := r.URL.Path[strings.LastIndexByte(r.URL.Path, '/')+1:]
		var result any = true
		switch method {
		case "getUpdates":
			if polls.Add(1) == 1 {
				result = []map[string]any{{
					"update_id": 1,
					"message": map[string]any{
						"message_id": 1, "text": "/pause 2h",
						"from": map[string]any{"id": 42}, "chat": map[string]any{"id": 42},
					},
				}}
				break
			}
			select {
			case polled <- struct{}{}:
			default:
			}
			select {
			case <-r.Context().Done():
			case <-stop:
			}
			return
		case "sendMessage":
			result = map[string]any{"message_id": 2, "chat": map[string]any{"id": 42}}
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	}))
	t.Cleanup(ts.Close)
	t.Cleanup(func() { close(stop) }) // runs before ts.Close
	return ts
}

// Run must stop cleanly when the context is canceled — MQTT unreachable, Telegram
// request in progress — and leave on disk the state changed by a command.
func TestRunShutsDownCleanlyAndSavesState(t *testing.T) {
	dir := t.TempDir()
	polled := make(chan struct{}, 1)
	tg := fakeTelegram(t, polled)
	frigate := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(frigate.Close)

	cfgPath := filepath.Join(dir, "config.yml")
	statePath := filepath.Join(dir, "data", "state.json")
	cfg := fmt.Sprintf(`
frigate: {url: %q}
mqtt: {broker: "tcp://127.0.0.1:1"}
telegram: {token: "t", admins: [42], chats: {moi: 42}}
web: {password: "p", protect_metrics: true}
state_file: %q
http_listen: "127.0.0.1:0"
log_level: error
`, frigate.URL, statePath)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, cfgPath, "", telegram.WithBaseURL(tg.URL), telegram.WithBackoff(time.Millisecond))
	}()

	select {
	case <-polled: // the command was read, the bot is long-polling again
	case err := <-done:
		t.Fatalf("Run stopped early: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("the bot never queried Telegram")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Run did not stop")
	}

	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("state not saved: %v", err)
	}
	var st struct {
		GlobalPauseUntil time.Time `json:"global_pause_until"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	if time.Until(st.GlobalPauseUntil) < time.Hour {
		t.Errorf("pause saved until %v, want ~2 h", st.GlobalPauseUntil)
	}
}

func TestRunRejectsInvalidConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte("mode: foo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), path, ""); err == nil {
		t.Fatal("an invalid configuration must be refused")
	}
}

// freeAddr returns a local address nobody listens on (the setup page's address
// comes from HTTP_LISTEN, it cannot be :0).
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

// waitFor polls url until it answers want.
func waitFor(t *testing.T, url string, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == want {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET %s never answered %d (last: %v)", url, want, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// With nothing configured, Run serves the setup page; once the connection is
// saved, the service starts with it, on the same address.
func TestRunStartsFromTheSetupPage(t *testing.T) {
	dir := t.TempDir()
	addr := freeAddr(t)
	t.Setenv("HTTP_LISTEN", addr)
	polled := make(chan struct{}, 1)
	tg := fakeTelegram(t, polled)
	frigate := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(frigate.Close)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, filepath.Join(dir, "config.yml"), filepath.Join(dir, "connection.yml"),
			telegram.WithBaseURL(tg.URL), telegram.WithBackoff(time.Millisecond))
	}()
	base := "http://" + addr
	waitFor(t, base+"/api/connection", http.StatusOK)
	waitFor(t, base+"/healthz", http.StatusOK) // waiting for its setup is not unhealthy

	body := fmt.Sprintf(`{"force": true, "connection": {"frigate": {"url": %q}, "mqtt": {"broker": "127.0.0.1:1"},
		"telegram": {"token": "t", "chats": {"moi": 42}}}}`, frigate.URL)
	req, _ := http.NewRequest("PUT", base+"/api/connection", strings.NewReader(body))
	req.Header.Set("X-Requested-With", "frigate-telegram-enhanced")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/connection = %d", resp.StatusCode)
	}

	select {
	case <-polled: // the bot runs: the service started with the saved connection
	case err := <-done:
		t.Fatalf("Run stopped: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("the service did not start after the setup")
	}
	waitFor(t, base+"/api/settings", http.StatusOK)
	if _, err := os.Stat(filepath.Join(dir, "connection.yml")); err != nil {
		t.Errorf("connection not saved: %v", err)
	}

	// Changed from the interface, the connection restarts the service with it.
	body = strings.Replace(body, `"moi"`, `"me"`, 1)
	req, _ = http.NewRequest("PUT", base+"/api/connection", strings.NewReader(body))
	req.Header.Set("X-Requested-With", "frigate-telegram-enhanced")
	if resp, err = http.DefaultClient.Do(req); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second PUT /api/connection = %d", resp.StatusCode)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var s struct{ Chats []string }
		if resp, err := http.Get(base + "/api/settings"); err == nil {
			json.NewDecoder(resp.Body).Decode(&s)
			resp.Body.Close()
		}
		if len(s.Chats) == 1 && s.Chats[0] == "me" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the service did not restart with the new connection: chats %v", s.Chats)
		}
		time.Sleep(50 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Run did not stop")
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json")); err != nil {
		t.Errorf("state not saved next to the connection: %v", err)
	}
}
