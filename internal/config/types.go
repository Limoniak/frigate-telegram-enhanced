package config

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// MinScore est un score minimal global ou par label.
// En YAML : `min_score: 0.7` ou `min_score: {person: 0.7, car: 0.85}`.
type MinScore struct {
	Default float64
	ByLabel map[string]float64
}

func (m *MinScore) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.MappingNode {
		return n.Decode(&m.ByLabel)
	}
	return n.Decode(&m.Default)
}

// For renvoie le score minimal applicable à un label.
func (m MinScore) For(label string) float64 {
	if v, ok := m.ByLabel[label]; ok {
		return v
	}
	return m.Default
}

func (m MinScore) valid() bool {
	ok := func(v float64) bool { return v >= 0 && v <= 1 }
	if !ok(m.Default) {
		return false
	}
	for _, v := range m.ByLabel {
		if !ok(v) {
			return false
		}
	}
	return true
}

// TimeRange est une plage horaire quotidienne, en minutes depuis minuit.
// Une plage dont From > To passe minuit (ex. 22:00 → 07:00).
type TimeRange struct {
	From, To int
}

func (t *TimeRange) UnmarshalYAML(n *yaml.Node) error {
	var raw struct {
		From string `yaml:"from"`
		To   string `yaml:"to"`
	}
	if err := n.Decode(&raw); err != nil {
		return err
	}
	var err error
	if t.From, err = parseClock(raw.From); err != nil {
		return err
	}
	t.To, err = parseClock(raw.To)
	return err
}

func parseClock(s string) (int, error) {
	tm, err := time.Parse("15:04", s)
	if err != nil {
		return 0, fmt.Errorf("heure invalide %q (format HH:MM)", s)
	}
	return tm.Hour()*60 + tm.Minute(), nil
}

// Contains indique si la minute de la journée tombe dans la plage (borne de fin exclue).
func (t TimeRange) Contains(minute int) bool {
	if t.From <= t.To {
		return minute >= t.From && minute < t.To
	}
	return minute >= t.From || minute < t.To
}

// InRanges indique si l'heure locale de at tombe dans une des plages.
func InRanges(rs []TimeRange, at time.Time) bool {
	minute := at.Hour()*60 + at.Minute()
	for _, r := range rs {
		if r.Contains(minute) {
			return true
		}
	}
	return false
}
