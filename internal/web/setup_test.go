package web

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"frigate-telegram-enhanced/internal/config"
	"frigate-telegram-enhanced/internal/i18n"
)

type fakeProber struct {
	mqttErr     error
	tokens      []string
	discover    bool   // Discover finds a Frigate
	brokerState string // state of the broker found through Frigate
	hosts       []string
	mqtt        config.MQTT    // the last broker tried
	frigate     config.Frigate // the last Frigate tried
}

func (f *fakeProber) Telegram(_ context.Context, token string, _ i18n.Lang) (string, []FoundChat, error) {
	f.tokens = append(f.tokens, token)
	return "@frigate_bot", []FoundChat{{ID: 111, Name: "Alice", Private: true}}, nil
}

func (f *fakeProber) Frigate(_ context.Context, fr config.Frigate, _ i18n.Lang) (FoundFrigate, error) {
	f.frigate = fr
	return FoundFrigate{URL: fr.URL, Version: "0.16.0", MQTT: config.MQTT{Broker: "192.168.1.10:1883", TopicPrefix: "frigate"},
		BrokerState: f.brokerState}, nil
}

func (f *fakeProber) Discover(_ context.Context, hosts []string, l i18n.Lang) *FoundFrigate {
	f.hosts = hosts
	if !f.discover {
		return nil
	}
	found, _ := f.Frigate(context.Background(), config.Frigate{URL: "http://192.168.1.10:5000"}, l)
	return &found
}

func (f *fakeProber) MQTT(_ context.Context, m config.MQTT, _ i18n.Lang) error {
	f.mqtt = m
	return f.mqttErr
}

func setupServer(t *testing.T, current *config.Connection, p Prober) (string, *atomic.Int32, *httptest.Server) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "connection.yml")
	var saved atomic.Int32
	s := NewSetup(path, current, p, slog.New(slog.DiscardHandler), func() { saved.Add(1) })
	mux := http.NewServeMux()
	s.MountAlone(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return path, &saved, ts
}

func send(t *testing.T, method, url string, body any) (*http.Response, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, url, bytes.NewReader(b))
	req.Header.Set("X-Requested-With", requestedWith)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func pageConnection() config.Connection {
	return config.Connection{
		Frigate:  config.Frigate{URL: "http://192.168.1.10:5000"},
		MQTT:     config.MQTT{Broker: "192.168.1.10:1883"},
		Telegram: config.Telegram{Token: "123:abc", Chats: map[string]int64{"Alice": 111}},
	}
}

func TestSetupAloneLeadsToTheSetupPage(t *testing.T) {
	_, _, ts := setupServer(t, nil, &fakeProber{})
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/setup" {
		t.Errorf("GET / = %d to %q, want a redirect to setup", resp.StatusCode, resp.Header.Get("Location"))
	}
	for _, path := range []string{"/setup", "/setup.js", "/ui.css", "/i18n.js"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d", path, resp.StatusCode)
		}
	}
}

func TestSetupFindsTheBotAndFrigatesBroker(t *testing.T) {
	_, _, ts := setupServer(t, nil, &fakeProber{})
	resp, out := send(t, "POST", ts.URL+"/api/connection/telegram", map[string]string{"token": "123:abc"})
	if resp.StatusCode != http.StatusOK || out["bot"] != "@frigate_bot" || len(out["chats"].([]any)) != 1 {
		t.Errorf("telegram: %d %v", resp.StatusCode, out)
	}
	resp, out = send(t, "POST", ts.URL+"/api/connection/frigate", map[string]string{"url": "http://192.168.1.10:5000"})
	if resp.StatusCode != http.StatusOK || out["version"] != "0.16.0" || out["mqtt"].(map[string]any)["broker"] != "192.168.1.10:1883" {
		t.Errorf("frigate: %d %v", resp.StatusCode, out)
	}
	if resp, _ := send(t, "POST", ts.URL+"/api/connection/telegram", map[string]string{}); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("telegram without a token: %d", resp.StatusCode)
	}
}

func TestSetupSavesOnlyWorkingConnectionsUnlessForced(t *testing.T) {
	p := &fakeProber{mqttErr: &Problem{Detail: "refused", Hint: "check"}}
	path, saved, ts := setupServer(t, nil, p)

	resp, out := send(t, "PUT", ts.URL+"/api/connection", map[string]any{"connection": pageConnection()})
	if resp.StatusCode != http.StatusUnprocessableEntity || saved.Load() != 0 {
		t.Fatalf("failing MQTT: %d %v, saved %d", resp.StatusCode, out, saved.Load())
	}
	if _, err := config.LoadConnection(path); err != config.ErrNotConfigured {
		t.Errorf("a failing connection was written: %v", err)
	}

	resp, out = send(t, "PUT", ts.URL+"/api/connection", map[string]any{"connection": pageConnection(), "force": true})
	if resp.StatusCode != http.StatusOK || saved.Load() != 1 {
		t.Fatalf("forced: %d %v, saved %d", resp.StatusCode, out, saved.Load())
	}
	c, err := config.LoadConnection(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.MQTT.Broker != "tcp://192.168.1.10:1883" || len(c.Telegram.Admins) != 1 {
		t.Errorf("saved %+v", c)
	}
	// Saved once: the service restarts, a second save would race with it.
	if resp, _ := send(t, "PUT", ts.URL+"/api/connection", map[string]any{"connection": pageConnection(), "force": true}); resp.StatusCode != http.StatusConflict {
		t.Errorf("second save: %d", resp.StatusCode)
	}
}

func TestSetupRejectsAnInvalidConnection(t *testing.T) {
	_, saved, ts := setupServer(t, nil, &fakeProber{})
	c := pageConnection()
	c.Telegram.Chats = nil
	resp, out := send(t, "PUT", ts.URL+"/api/connection", map[string]any{"connection": c, "force": true})
	if resp.StatusCode != http.StatusBadRequest || saved.Load() != 0 || !strings.Contains(out["error"].(string), "telegram.chats") {
		t.Errorf("no chat: %d %v", resp.StatusCode, out)
	}
	if resp, _ := send(t, "PUT", ts.URL+"/api/connection", map[string]any{"connection": map[string]any{"bogus": 1}}); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown field: %d", resp.StatusCode)
	}
}

func TestSetupNeverSendsSecretsBackAndKeepsThem(t *testing.T) {
	current := pageConnection()
	current.MQTT.Password, current.Web.Password = "mqtt-secret", "web-secret"
	p := &fakeProber{}
	path, _, ts := setupServer(t, &current, p)

	resp, err := http.Get(ts.URL + "/api/connection")
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	body.ReadFrom(resp.Body)
	resp.Body.Close()
	for _, secret := range []string{"123:abc", "mqtt-secret", "web-secret"} {
		if strings.Contains(body.String(), secret) {
			t.Errorf("GET /api/connection leaks %q: %s", secret, body.String())
		}
	}
	if !strings.Contains(body.String(), `"token":true`) || !strings.Contains(body.String(), `"first":false`) {
		t.Errorf("GET /api/connection = %s", body.String())
	}

	// Empty secrets keep the saved ones, for the checks as for the file.
	c := pageConnection()
	c.Telegram.Token = ""
	if resp, out := send(t, "PUT", ts.URL+"/api/connection", map[string]any{"connection": c}); resp.StatusCode != http.StatusOK {
		t.Fatalf("save: %d %v", resp.StatusCode, out)
	}
	if p.tokens[len(p.tokens)-1] != "123:abc" {
		t.Errorf("checked with token %q", p.tokens[len(p.tokens)-1])
	}
	saved, err := config.LoadConnection(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Telegram.Token != "123:abc" || saved.MQTT.Password != "mqtt-secret" || saved.Web.Password != "web-secret" {
		t.Errorf("secrets not kept: %+v", saved)
	}
}

func TestSetupAloneOnlyAnswersIPAddresses(t *testing.T) {
	mux := http.NewServeMux()
	NewSetup(filepath.Join(t.TempDir(), "c.yml"), nil, &fakeProber{}, slog.New(slog.DiscardHandler), func() {}).MountAlone(mux)
	for host, want := range map[string]int{"192.168.1.10:8431": http.StatusOK, "nas.lan:8431": http.StatusForbidden} {
		req := httptest.NewRequest("GET", "/api/connection", nil)
		req.Host = host
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != want {
			t.Errorf("Host %s: %d, want %d", host, w.Code, want)
		}
	}
}

func TestSetupDiscoversFrigateFromTheAddressOfThePage(t *testing.T) {
	p := &fakeProber{discover: true, brokerState: BrokerPassword}
	_, _, ts := setupServer(t, nil, p)
	resp, out := send(t, "POST", ts.URL+"/api/connection/discover", map[string]any{})
	found, _ := out["found"].(map[string]any)
	if resp.StatusCode != http.StatusOK || found == nil || found["url"] != "http://192.168.1.10:5000" || found["broker_state"] != BrokerPassword {
		t.Errorf("discover: %d %v", resp.StatusCode, out)
	}
	if len(p.hosts) != 1 || p.hosts[0] != "127.0.0.1" {
		t.Errorf("hosts tried first = %v, want the page's", p.hosts)
	}
	p.discover = false
	if _, out := send(t, "POST", ts.URL+"/api/connection/discover", map[string]any{}); out["found"] != nil {
		t.Errorf("nothing found: %v", out)
	}
}

func TestSetupChecksTheBroker(t *testing.T) {
	current := pageConnection()
	current.MQTT.Password = "mqtt-secret"
	p := &fakeProber{}
	_, _, ts := setupServer(t, &current, p)

	resp, out := send(t, "POST", ts.URL+"/api/connection/mqtt", map[string]string{"broker": "192.168.1.10", "username": "frigate"})
	if resp.StatusCode != http.StatusOK || out["broker"] != "tcp://192.168.1.10:1883" {
		t.Fatalf("check: %d %v", resp.StatusCode, out)
	}
	// A password left empty is the one saved, for the same broker: the page never gets it back.
	if p.mqtt.Broker != "tcp://192.168.1.10:1883" || p.mqtt.Username != "frigate" || p.mqtt.Password != "mqtt-secret" {
		t.Errorf("broker tried = %+v", p.mqtt)
	}

	p.mqttErr = &Problem{Detail: "the broker refused the password", Hint: "check the MQTT password"}
	resp, out = send(t, "POST", ts.URL+"/api/connection/mqtt", map[string]string{"broker": "192.168.1.10"})
	if resp.StatusCode != http.StatusBadGateway || out["error"] != "the broker refused the password" || out["hint"] != "check the MQTT password" {
		t.Errorf("failing broker: %d %v", resp.StatusCode, out)
	}
	if resp, _ := send(t, "POST", ts.URL+"/api/connection/mqtt", map[string]string{}); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("without a broker: %d", resp.StatusCode)
	}
}

// A group alone is enough: no admin to give, its members control the bot.
func TestSetupSavesAGroupWithoutAdmin(t *testing.T) {
	path, saved, ts := setupServer(t, nil, &fakeProber{})
	c := pageConnection()
	c.Telegram.Chats = map[string]int64{"Family": -1001234567890}
	resp, out := send(t, "PUT", ts.URL+"/api/connection", map[string]any{"connection": c})
	if resp.StatusCode != http.StatusOK || saved.Load() != 1 {
		t.Fatalf("group only: %d %v", resp.StatusCode, out)
	}
	got, err := config.LoadConnection(path)
	if err != nil || len(got.Telegram.Admins) != 0 {
		t.Errorf("saved %+v, %v", got, err)
	}
}

// A saved password is only sent back to the address it was saved for: typing
// another address must not hand it over to whoever answers there.
func TestSetupKeepsSecretsForTheSameAddressOnly(t *testing.T) {
	current := pageConnection()
	current.Frigate.Password, current.MQTT.Password = "frigate-secret", "mqtt-secret"
	current.Normalize()
	p := &fakeProber{}
	_, _, ts := setupServer(t, &current, p)

	send(t, "POST", ts.URL+"/api/connection/mqtt", map[string]string{"broker": "attacker.example"})
	if p.mqtt.Password != "" {
		t.Errorf("another broker got the saved password %q", p.mqtt.Password)
	}
	send(t, "POST", ts.URL+"/api/connection/mqtt", map[string]string{"broker": "192.168.1.10"})
	if p.mqtt.Password != "mqtt-secret" {
		t.Errorf("the same broker: password %q, want the saved one", p.mqtt.Password)
	}
	send(t, "POST", ts.URL+"/api/connection/frigate", map[string]string{"url": "http://attacker.example:5000"})
	if p.frigate.Password != "" {
		t.Errorf("another Frigate got the saved password %q", p.frigate.Password)
	}
	send(t, "POST", ts.URL+"/api/connection/frigate", map[string]string{"url": "http://192.168.1.10:5000/"})
	if p.frigate.Password != "frigate-secret" {
		t.Errorf("the same Frigate: password %q, want the saved one", p.frigate.Password)
	}
}

// A saved password can be removed: an empty field keeps it, "clear" removes it.
func TestSetupRemovesASavedPassword(t *testing.T) {
	current := pageConnection()
	current.MQTT.Password, current.Web.Password = "mqtt-secret", "web-secret"
	current.Normalize()
	p := &fakeProber{}
	path, _, ts := setupServer(t, &current, p)

	send(t, "POST", ts.URL+"/api/connection/mqtt?clear=mqtt_password", map[string]string{"broker": "192.168.1.10"})
	if p.mqtt.Password != "" {
		t.Errorf("check without the saved password: got %q", p.mqtt.Password)
	}
	resp, out := send(t, "PUT", ts.URL+"/api/connection", map[string]any{"connection": pageConnection(), "clear": []string{"web_password"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save: %d %v", resp.StatusCode, out)
	}
	c, err := config.LoadConnection(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Web.Password != "" || c.MQTT.Password != "mqtt-secret" {
		t.Errorf("web password %q (want removed), mqtt password %q (want kept)", c.Web.Password, c.MQTT.Password)
	}
}
