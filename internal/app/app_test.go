package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// fakeTelegram répond à l'API Bot : le premier getUpdates livre une commande /pause,
// les suivants font du long polling jusqu'à l'annulation de la requête.
func fakeTelegram(t *testing.T, polled chan<- struct{}) *httptest.Server {
	t.Helper()
	var polls atomic.Int32
	stop := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Lire le corps : sans cela, le serveur ne détecte pas la fermeture de la
		// connexion par le client et r.Context() n'est jamais annulé.
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
	t.Cleanup(func() { close(stop) }) // exécuté avant ts.Close
	return ts
}

// Run doit s'arrêter proprement à l'annulation du contexte — MQTT injoignable,
// requête Telegram en cours — et laisser sur disque l'état modifié par une commande.
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
		done <- Run(ctx, cfgPath, telegram.WithBaseURL(tg.URL), telegram.WithBackoff(time.Millisecond))
	}()

	select {
	case <-polled: // la commande a été lue, le bot est reparti en long polling
	case err := <-done:
		t.Fatalf("Run s'est arrêté prématurément : %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("le bot n'a jamais interrogé Telegram")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run : %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Run ne s'est pas arrêté")
	}

	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("état non sauvegardé : %v", err)
	}
	var st struct {
		GlobalPauseUntil time.Time `json:"global_pause_until"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	if time.Until(st.GlobalPauseUntil) < time.Hour {
		t.Errorf("pause enregistrée jusqu'à %v, attendu ~2 h", st.GlobalPauseUntil)
	}
}

func TestRunRejectsInvalidConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte("mode: foo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), path); err == nil {
		t.Fatal("une config invalide doit être refusée")
	}
}
