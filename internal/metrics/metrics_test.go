package metrics

import "testing"

func TestRegistryExposesMetrics(t *testing.T) {
	m := New()
	m.EventsDropped.Inc()
	m.EventsFiltered.WithLabelValues("label").Inc()
	families, err := m.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, f := range families {
		found[f.GetName()] = true
	}
	for _, name := range []string{"ft_events_dropped_total", "ft_events_filtered_total", "ft_mqtt_connected", "go_goroutines"} {
		if !found[name] {
			t.Errorf("metric %s missing", name)
		}
	}
}
