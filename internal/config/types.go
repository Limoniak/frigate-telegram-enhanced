package config

import (
	"encoding/json"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"

	"frigate-telegram-enhanced/internal/i18n"
)

// Duration is a duration serialized as text ("60s", "1h30m") in YAML as well as in
// JSON. time.Duration would be encoded in nanoseconds, unreadable in the overlay
// file written by the web interface.
type Duration time.Duration

func (d Duration) String() string { return time.Duration(d).String() }

func (d *Duration) parse(s string) error {
	v, err := time.ParseDuration(s)
	if err != nil {
		return i18n.NewError("invalid duration %q (e.g. 60s, 1h30m)", s)
	}
	*d = Duration(v)
	return nil
}

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	return d.parse(s)
}

func (d Duration) MarshalYAML() (any, error) { return d.String(), nil }

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	return d.parse(s)
}

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

// MinScore is a minimum score, global or per label.
// In YAML: `min_score: 0.7` or `min_score: {person: 0.7, car: 0.85}`.
// In JSON (web interface): `{"default": 0.7}` or `{"by_label": {"person": 0.7}}`.
type MinScore struct {
	Default float64            `json:"default"`
	ByLabel map[string]float64 `json:"by_label,omitempty"`
}

func (m *MinScore) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.MappingNode {
		return n.Decode(&m.ByLabel)
	}
	return n.Decode(&m.Default)
}

func (m MinScore) MarshalYAML() (any, error) {
	if len(m.ByLabel) > 0 {
		return m.ByLabel, nil
	}
	return m.Default, nil
}

// For returns the minimum score that applies to a label.
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

// TimeRange is a daily time range, in minutes since midnight.
// A range whose From > To spans midnight (e.g. 22:00 → 07:00).
type TimeRange struct {
	From, To int
}

// clockRange is the serialized form of a range: {from: "22:00", to: "07:00"}.
type clockRange struct {
	From string `yaml:"from" json:"from"`
	To   string `yaml:"to" json:"to"`
}

func (t *TimeRange) fromClock(raw clockRange) error {
	var err error
	if t.From, err = parseClock(raw.From); err != nil {
		return err
	}
	t.To, err = parseClock(raw.To)
	return err
}

func (t TimeRange) clock() clockRange {
	return clockRange{From: formatClock(t.From), To: formatClock(t.To)}
}

func (t *TimeRange) UnmarshalYAML(n *yaml.Node) error {
	var raw clockRange
	if err := n.Decode(&raw); err != nil {
		return err
	}
	return t.fromClock(raw)
}

func (t TimeRange) MarshalYAML() (any, error) { return t.clock(), nil }

func (t *TimeRange) UnmarshalJSON(b []byte) error {
	var raw clockRange
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	return t.fromClock(raw)
}

func (t TimeRange) MarshalJSON() ([]byte, error) { return json.Marshal(t.clock()) }

func parseClock(s string) (int, error) {
	tm, err := time.Parse("15:04", s)
	if err != nil {
		return 0, i18n.NewError("invalid time %q (HH:MM format)", s)
	}
	return tm.Hour()*60 + tm.Minute(), nil
}

func formatClock(minute int) string { return fmt.Sprintf("%02d:%02d", minute/60, minute%60) }

// Contains reports whether the minute of the day falls in the range (end excluded).
func (t TimeRange) Contains(minute int) bool {
	if t.From <= t.To {
		return minute >= t.From && minute < t.To
	}
	return minute >= t.From || minute < t.To
}

// InRanges reports whether the local time of at falls in one of the ranges.
func InRanges(rs []TimeRange, at time.Time) bool {
	minute := at.Hour()*60 + at.Minute()
	for _, r := range rs {
		if r.Contains(minute) {
			return true
		}
	}
	return false
}
