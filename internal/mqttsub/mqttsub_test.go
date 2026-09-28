package mqttsub

import (
	"log/slog"
	"net"
	"testing"
	"time"

	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"

	"frigate-telegram/internal/config"
)

func startBroker(t *testing.T) (*mochi.Server, string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()

	srv := mochi.New(&mochi.Options{InlineClient: true})
	if err := srv.AddHook(new(auth.AllowHook), nil); err != nil {
		t.Fatal(err)
	}
	if err := srv.AddListener(listeners.NewTCP(listeners.Config{ID: "test", Address: addr})); err != nil {
		t.Fatal(err)
	}
	go srv.Serve()
	t.Cleanup(func() { srv.Close() })
	return srv, "tcp://" + addr
}

func TestSubscriberReceivesMessages(t *testing.T) {
	srv, url := startBroker(t)
	got := make(chan string, 10)
	s := New(config.MQTT{Broker: url, ClientID: "test"}, []string{"frigate/events"},
		func(topic string, p []byte) {
			select {
			case got <- topic + "|" + string(p):
			default:
			}
		},
		slog.New(slog.DiscardHandler), nil)
	s.Start()
	defer s.Stop()

	deadline := time.After(10 * time.Second)
	for {
		// On publie en boucle : l'abonnement se fait de façon asynchrone après la connexion.
		srv.Publish("frigate/events", []byte("hello"), false, 0)
		select {
		case v := <-got:
			if v != "frigate/events|hello" {
				t.Fatalf("reçu %q", v)
			}
			if !s.Connected() {
				t.Error("Connected() doit être vrai")
			}
			return
		case <-time.After(100 * time.Millisecond):
		case <-deadline:
			t.Fatal("aucun message reçu en 10 s")
		}
	}
}
