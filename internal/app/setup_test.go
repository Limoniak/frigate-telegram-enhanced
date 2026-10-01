package app

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"

	"frigate-telegram-enhanced/internal/config"
	"frigate-telegram-enhanced/internal/i18n"
	"frigate-telegram-enhanced/internal/web"
)

func TestParseGateway(t *testing.T) {
	table := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\n" +
		"eth0\t0000A8C0\t00000000\t0001\t0\t0\t0\t00FFFFFF\n" +
		"eth0\t00000000\t0100A8C0\t0003\t0\t0\t0\t00000000\n"
	if got := parseGateway(table); got != "192.168.0.1" {
		t.Errorf("parseGateway = %q, want 192.168.0.1", got)
	}
	if got := parseGateway(""); got != "" {
		t.Errorf("empty table: %q", got)
	}
}

// startBroker starts a local MQTT broker; with password set, it only accepts
// frigate/password.
func startBroker(t *testing.T, password string) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	srv := mochi.New(&mochi.Options{Logger: slog.New(slog.DiscardHandler)})
	if password == "" {
		err = srv.AddHook(new(auth.AllowHook), nil)
	} else {
		ledger := &auth.Ledger{Auth: auth.AuthRules{{Username: "frigate", Password: auth.RString(password), Allow: true}}}
		err = srv.AddHook(new(auth.Hook), &auth.Options{Ledger: ledger})
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.AddListener(listeners.NewTCP(listeners.Config{ID: "t", Address: addr})); err != nil {
		t.Fatal(err)
	}
	go srv.Serve()
	t.Cleanup(func() { srv.Close() })
	_, port, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(port)
	return p
}

// fakeFrigate answers /api/version and an /api/config whose broker is mqttHost:port.
func fakeFrigate(t *testing.T, mqttHost string, port int) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			fmt.Fprint(w, "0.16.0")
		case "/api/config":
			fmt.Fprintf(w, `{"cameras": {}, "mqtt": {"host": %q, "port": %d, "user": "frigate", "topic_prefix": "frigate"}}`, mqttHost, port)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestFrigateFindsItsBrokerFromHere(t *testing.T) {
	cases := []struct {
		name, host, password, want string
	}{
		// Frigate's localhost is its own machine: here, Frigate's address.
		{"localhost, open broker", "localhost", "", web.BrokerOK},
		// A Docker name unknown here: Frigate's address too. The broker answers, but
		// Frigate does not give its password: the page asks for it.
		{"docker name, password", "mosquitto.invalid", "secret", web.BrokerPassword},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port := startBroker(t, tc.password)
			fr := fakeFrigate(t, tc.host, port)
			found, err := prober{}.Frigate(context.Background(), config.Frigate{URL: fr.URL}, i18n.EN)
			if err != nil {
				t.Fatal(err)
			}
			want := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
			if found.Version != "0.16.0" || found.BrokerState != tc.want || found.MQTT.Username != "frigate" {
				t.Errorf("found %+v, want broker state %s", found, tc.want)
			}
			if tc.want == web.BrokerOK && found.MQTT.Broker != want {
				t.Errorf("broker = %q, want %q", found.MQTT.Broker, want)
			}
			if tc.want == web.BrokerPassword && found.MQTT.Broker != want {
				t.Errorf("broker = %q, want %q (the one that answered)", found.MQTT.Broker, want)
			}
		})
	}
}
