// Package mqttsub gère la connexion au broker MQTT et l'abonnement aux topics de Frigate.
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
	lost      atomic.Pointer[error] // cause de la dernière perte de connexion
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
		go func() { // ne jamais attendre un token dans un handler paho
			tok.Wait()
			if err := tok.Error(); err != nil {
				log.Error("MQTT subscription failed", "err", err)
				return // état laissé à false : pas sain tant que l'abonnement n'a pas réussi
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

// LostError renvoie la cause de la dernière perte de connexion, nil si aucune.
func (s *Subscriber) LostError() error {
	if p := s.lost.Load(); p != nil {
		return *p
	}
	return nil
}

// Probe tente une connexion unique au broker, avec les mêmes paramètres mais un
// identifiant distinct, et renvoie l'erreur obtenue : le client principal, qui
// réessaie en boucle, ne remonte pas la cause d'un échec (identifiants refusés,
// broker injoignable…). Sert au diagnostic de l'interface web.
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

// Start lance la connexion ; les échecs sont retentés en arrière-plan.
func (s *Subscriber) Start() { s.client.Connect() }

func (s *Subscriber) Connected() bool { return s.connected.Load() }

func (s *Subscriber) Stop() {
	s.client.Disconnect(250)
	s.connected.Store(false)
}

// Match indique si topic correspond au filtre MQTT filter, jokers + (un niveau) et
// # (tous les niveaux restants) compris.
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
