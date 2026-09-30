// Package metrics declares the Prometheus metrics of the service.
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
			Name: "ft_events_received_total", Help: "Frigate events received.",
		}, []string{"camera", "label"}),
		EventsFiltered: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ft_events_filtered_total", Help: "Events not notified, by reason.",
		}, []string{"reason"}),
		EventsDropped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "ft_events_dropped_total", Help: "MQTT messages lost because the queue was full.",
		}),
		NotificationsSent: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ft_notifications_sent_total", Help: "Telegram messages sent, by type.",
		}, []string{"kind"}),
		TelegramErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ft_telegram_errors_total", Help: "Failed Telegram calls.",
		}, []string{"method", "code"}),
		MQTTConnected: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "ft_mqtt_connected", Help: "1 if the client is connected to the MQTT broker.",
		}),
		MediaDownload: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "ft_media_download_seconds", Help: "Duration of the downloads from Frigate.",
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
