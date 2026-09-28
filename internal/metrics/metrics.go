// Package metrics déclare les métriques Prometheus du service.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

type Metrics struct {
	Registry          *prometheus.Registry
	EventsReceived    *prometheus.CounterVec
	EventsFiltered    *prometheus.CounterVec
	EventsDropped     prometheus.Counter
	NotificationsSent *prometheus.CounterVec
	TelegramErrors    *prometheus.CounterVec
	MQTTConnected     prometheus.Gauge
	MediaDownload     prometheus.Histogram
}

func New() *Metrics {
	m := &Metrics{
		Registry: prometheus.NewRegistry(),
		EventsReceived: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ft_events_received_total", Help: "Événements Frigate reçus.",
		}, []string{"camera", "label"}),
		EventsFiltered: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ft_events_filtered_total", Help: "Événements non notifiés, par raison.",
		}, []string{"reason"}),
		EventsDropped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "ft_events_dropped_total", Help: "Messages MQTT perdus car la file était pleine.",
		}),
		NotificationsSent: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ft_notifications_sent_total", Help: "Messages Telegram envoyés, par type.",
		}, []string{"kind"}),
		TelegramErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ft_telegram_errors_total", Help: "Appels Telegram en échec.",
		}, []string{"method", "code"}),
		MQTTConnected: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "ft_mqtt_connected", Help: "1 si le client est connecté au broker MQTT.",
		}),
		MediaDownload: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "ft_media_download_seconds", Help: "Durée des téléchargements depuis Frigate.",
			Buckets: prometheus.ExponentialBuckets(0.05, 2, 10),
		}),
	}
	m.Registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.EventsReceived, m.EventsFiltered, m.EventsDropped, m.NotificationsSent,
		m.TelegramErrors, m.MQTTConnected, m.MediaDownload,
	)
	return m
}
