// Package mqttsub manages the connection to the MQTT broker and the subscription to Frigate's topics.
package mqttsub

import (
	"crypto/tls"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"frigate-telegram-enhanced/internal/config"
)

type Handler func(topic string, payload []byte)

type Subscriber struct {
	client    mqtt.Client
	connected atomic.Bool
	opts      *mqtt.ClientOptions
	lost      atomic.Pointer[error] // cause of the last connection loss
}

func New(cfg config.MQTT, topics []string, h Handler, log *slog.Logger, onState func(connected bool)) *Subscriber {
	s := &Subscriber{}
	setState := func(up bool) {
		s.connected.Store(up)
		if onState != nil {
			onState(up)
		}
	}
	opts := mqtt.NewClientOptions().
		AddBroker(cfg.Broker).
		SetClientID(cfg.ClientID).
		SetUsername(cfg.Username).
		SetPassword(cfg.Password).
		SetCleanSession(true).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second).
		SetMaxReconnectInterval(30 * time.Second).
		SetKeepAlive(30 * time.Second)
	if cfg.InsecureSkipVerify {
		opts.SetTLSConfig(&tls.Config{InsecureSkipVerify: true}) //nolint:gosec // option explicite de l'utilisateur
	}
	opts.SetOnConnectHandler(func(c mqtt.Client) {
		filters := make(map[string]byte, len(topics))
		for _, t := range topics {
			filters[t] = 0
		}
		tok := c.SubscribeMultiple(filters, func(_ mqtt.Client, m mqtt.Message) { h(m.Topic(), m.Payload()) })
		go func() { // never wait for a token in a paho handler
			tok.Wait()
			if err := tok.Error(); err != nil {
				log.Error("MQTT subscription failed", "err", err)
				return // state left false: not healthy until the subscription succeeded
			}
			log.Info("MQTT connected", "broker", cfg.Broker, "topics", topics)
			setState(true)
		}()
	})
	opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		s.lost.Store(&err)
		setState(false)
		log.Warn("MQTT connection lost", "err", err)
	})
	opts.SetReconnectingHandler(func(mqtt.Client, *mqtt.ClientOptions) {
		log.Info("MQTT reconnecting…")
	})
	s.opts = opts
	s.client = mqtt.NewClient(opts)
	return s
}

// LostError returns the cause of the last connection loss, nil if none.
func (s *Subscriber) LostError() error {
	if p := s.lost.Load(); p != nil {
		return *p
	}
	return nil
}

// Probe tries a single connection to the broker, with the same parameters but a
// distinct client ID, and returns the error it gets: the main client, which retries
// in a loop, does not report why it fails (credentials refused, broker
// unreachable…). Used by the web interface's diagnosis.
func (s *Subscriber) Probe(timeout time.Duration) error {
	o := *s.opts
	o.SetClientID(s.opts.ClientID + "-probe").
		SetConnectRetry(false).
		SetAutoReconnect(false).
		SetConnectTimeout(timeout).
		SetOnConnectHandler(nil).
		SetConnectionLostHandler(nil).
		SetReconnectingHandler(nil)
	c := mqtt.NewClient(&o)
	tok := c.Connect()
	if !tok.WaitTimeout(timeout + time.Second) {
		return errProbeTimeout
	}
	if err := tok.Error(); err != nil {
		return err
	}
	c.Disconnect(100)
	return nil
}

var errProbeTimeout = errors.New("timed out")

// Start starts the connection; failures are retried in the background.
func (s *Subscriber) Start() { s.client.Connect() }

func (s *Subscriber) Connected() bool { return s.connected.Load() }

func (s *Subscriber) Stop() {
	s.client.Disconnect(250)
	s.connected.Store(false)
}

// Match reports whether topic matches the MQTT filter, wildcards + (one level) and
// # (every remaining level) included.
func Match(filter, topic string) bool {
	f, t := strings.Split(filter, "/"), strings.Split(topic, "/")
	for i, part := range f {
		if part == "#" {
			return true
		}
		if i >= len(t) || (part != "+" && part != t[i]) {
			return false
		}
	}
	return len(f) == len(t)
}
