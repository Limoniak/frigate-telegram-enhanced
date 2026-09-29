package app

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"frigate-telegram-enhanced/internal/bot"
	"frigate-telegram-enhanced/internal/config"
	"frigate-telegram-enhanced/internal/frigate"
	"frigate-telegram-enhanced/internal/i18n"
	"frigate-telegram-enhanced/internal/mqttsub"
	"frigate-telegram-enhanced/internal/state"
	"frigate-telegram-enhanced/internal/telegram"
	"frigate-telegram-enhanced/internal/web"
)

// newChecker construit un diagnostic contre un faux Frigate et un faux Telegram,
// avec un broker MQTT injoignable.
func newChecker(t *testing.T, frigateStatus int, tgBody string) *checker {
	t.Helper()
	fr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if frigateStatus != http.StatusOK {
			w.WriteHeader(frigateStatus)
			return
		}
		w.Write([]byte(`"0.16.1-abc"`))
	}))
	t.Cleanup(fr.Close)
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(tgBody)) }))
	t.Cleanup(tg.Close)

	cfg := &config.Config{Frigate: config.Frigate{URL: fr.URL}, MQTT: config.MQTT{Broker: "tcp://127.0.0.1:1", ClientID: "t"}}
	frc, err := frigate.NewClient(cfg.Frigate)
	if err != nil {
		t.Fatal(err)
	}
	tgc := telegram.New("123:abc", telegram.WithBaseURL(tg.URL), telegram.WithBackoff(0))
	log := slog.New(slog.DiscardHandler)
	return &checker{
		cfg: cfg, fr: frc, tg: tgc,
		sub: mqttsub.New(cfg.MQTT, nil, func(string, []byte) {}, log, nil),
		bot: bot.New(bot.Deps{Config: cfg, Telegram: tgc, Log: log}),
	}
}

func TestHealthAllGoodButMQTT(t *testing.T) {
	c := newChecker(t, http.StatusOK, `{"ok":true,"result":{"id":1,"username":"mon_bot"}}`)
	got := c.check(context.Background(), i18n.EN)
	if got[0].State != web.StateOK || !strings.Contains(got[0].Detail, "0.16.1-abc") {
		t.Errorf("frigate = %+v", got[0])
	}
	if got[1].State != web.StateError || !strings.Contains(got[1].Detail, "connection refused") || !strings.Contains(got[1].Hint, "MQTT_BROKER") {
		t.Errorf("mqtt = %+v", got[1])
	}
	if got[2].State != web.StateOK || got[2].Detail != "@mon_bot" {
		t.Errorf("telegram = %+v", got[2])
	}
}

func TestHealthExplainsFailures(t *testing.T) {
	c := newChecker(t, http.StatusUnauthorized, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
	got := c.check(context.Background(), i18n.FR)
	if got[0].State != web.StateError || !strings.Contains(got[0].Hint, "FRIGATE_USERNAME") {
		t.Errorf("frigate = %+v", got[0])
	}
	if got[2].State != web.StateError || !strings.Contains(got[2].Detail, "token") || !strings.Contains(got[2].Hint, "TELEGRAM_TOKEN") {
		t.Errorf("telegram = %+v", got[2])
	}
	if !strings.Contains(got[1].Detail, "injoignable") {
		t.Errorf("mqtt en français = %+v", got[1])
	}
}

func TestPresenceRouter(t *testing.T) {
	st, _ := state.Load(t.TempDir()+"/s.json", time.Now())
	var forwarded []string
	h := presenceRouter(config.Presence{Topics: []string{"homeassistant/person/+/state"}, HomeValues: config.DefaultHomeValues},
		st, slog.New(slog.DiscardHandler), func(topic string, _ []byte) { forwarded = append(forwarded, topic) })

	h("homeassistant/person/alice/state", []byte(`"home"`))
	h("homeassistant/person/bob/state", []byte("not_home"))
	h("frigate/events", []byte("{}"))
	if !st.SomeoneHome() {
		t.Error("alice est à la maison")
	}
	if len(forwarded) != 1 || forwarded[0] != "frigate/events" {
		t.Errorf("transmis au notifier = %v", forwarded)
	}
	h("homeassistant/person/alice/state", []byte("Travail"))
	if st.SomeoneHome() {
		t.Error("plus personne à la maison")
	}
}
