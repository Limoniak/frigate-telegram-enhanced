package mqttsub

import (
	"log/slog"
	"net"
	"testing"
	"time"

	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"

	"frigate-telegram-enhanced/internal/config"
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
		// Publish in a loop: the subscription happens asynchronously after the connection.
		srv.Publish("frigate/events", []byte("hello"), false, 0)
		select {
		case v := <-got:
			if v != "frigate/events|hello" {
				t.Fatalf("received %q", v)
			}
			if !s.Connected() {
				t.Error("Connected() must be true")
			}
			return
		case <-time.After(100 * time.Millisecond):
		case <-deadline:
			t.Fatal("no message received in 10 s")
		}
	}
}

func TestProbeReportsTheCause(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	srv := mochi.New(nil)
	ledger := &auth.Ledger{Auth: auth.AuthRules{{Username: "frigate", Password: "pw", Allow: true}}}
	if err := srv.AddHook(new(auth.Hook), &auth.Options{Ledger: ledger}); err != nil {
		t.Fatal(err)
	}
	if err := srv.AddListener(listeners.NewTCP(listeners.Config{ID: "t", Address: addr})); err != nil {
		t.Fatal(err)
	}
	go srv.Serve()
	t.Cleanup(func() { srv.Close() })

	probe := func(broker, user, pass string) error {
		s := New(config.MQTT{Broker: broker, ClientID: "test", Username: user, Password: pass}, nil,
			func(string, []byte) {}, slog.New(slog.DiscardHandler), nil)
		return s.Probe(2 * time.Second)
	}
	if err := probe("tcp://"+addr, "frigate", "pw"); err != nil {
		t.Errorf("right credentials: %v", err)
	}
	if err := probe("tcp://"+addr, "frigate", "faux"); err == nil {
		t.Error("wrong password: want an error")
	} else {
		t.Logf("wrong password → %v", err)
	}
	if err := probe("tcp://127.0.0.1:1", "", ""); err == nil {
		t.Error("unreachable broker: want an error")
	} else {
		t.Logf("unreachable → %v", err)
	}
}

func TestMatch(t *testing.T) {
	for _, tc := range []struct {
		filter, topic string
		want          bool
	}{
		{"homeassistant/person/+/state", "homeassistant/person/alice/state", true},
		{"homeassistant/person/+/state", "homeassistant/person/alice/attr", false},
		{"homeassistant/#", "homeassistant/person/alice/state", true},
		{"maison/presence", "maison/presence", true},
		{"maison/presence", "maison/presence/x", false},
		{"a/+", "a", false},
	} {
		if got := Match(tc.filter, tc.topic); got != tc.want {
			t.Errorf("Match(%q, %q) = %v", tc.filter, tc.topic, got)
		}
	}
}
