// Package mqttsub gère la connexion au broker MQTT et l'abonnement aux topics de Frigate.
package mqttsub

import (
	"crypto/tls"
	"log/slog"
	"sync/atomic"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"frigate-telegram/internal/config"
)

type Handler func(topic string, payload []byte)

type Subscriber struct {
	client    mqtt.Client
	connected atomic.Bool
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
				log.Error("abonnement MQTT échoué", "err", err)
				return // état laissé à false : pas sain tant que l'abonnement n'a pas réussi
			}
			log.Info("MQTT connecté", "broker", cfg.Broker, "topics", topics)
			setState(true)
		}()
	})
	opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		setState(false)
		log.Warn("connexion MQTT perdue", "err", err)
	})
	opts.SetReconnectingHandler(func(mqtt.Client, *mqtt.ClientOptions) {
		log.Info("reconnexion MQTT…")
	})
	s.client = mqtt.NewClient(opts)
	return s
}

// Start lance la connexion ; les échecs sont retentés en arrière-plan.
func (s *Subscriber) Start() { s.client.Connect() }

func (s *Subscriber) Connected() bool { return s.connected.Load() }

func (s *Subscriber) Stop() {
	s.client.Disconnect(250)
	s.connected.Store(false)
}
