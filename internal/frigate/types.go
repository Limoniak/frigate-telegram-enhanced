// Package frigate describes Frigate's MQTT messages and provides a client for its HTTP API.
package frigate

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
)

// SubLabel accepts Frigate's formats: null, "name" or ["name", score].
type SubLabel string

func (s *SubLabel) UnmarshalJSON(b []byte) error {
	*s = ""
	if string(b) == "null" {
		return nil
	}
	var str string
	if err := json.Unmarshal(b, &str); err == nil {
		*s = SubLabel(str)
		return nil
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(b, &arr); err != nil {
		return fmt.Errorf("sub_label: unexpected format %s", b)
	}
	if len(arr) == 0 {
		return nil
	}
	if err := json.Unmarshal(arr[0], &str); err != nil {
		return fmt.Errorf("sub_label: unexpected format %s", b)
	}
	*s = SubLabel(str)
	return nil
}

// Event is the tracked object published on frigate/events.
type Event struct {
	ID            string   `json:"id"`
	Camera        string   `json:"camera"`
	Label         string   `json:"label"`
	SubLabel      SubLabel `json:"sub_label"`
	Score         float64  `json:"score"`
	TopScore      float64  `json:"top_score"`
	EnteredZones  []string `json:"entered_zones"`
	CurrentZones  []string `json:"current_zones"`
	Stationary    bool     `json:"stationary"`
	FalsePositive bool     `json:"false_positive"`
	HasSnapshot   bool     `json:"has_snapshot"`
	HasClip       bool     `json:"has_clip"`
	StartTime     float64  `json:"start_time"`
	EndTime       *float64 `json:"end_time"`
}

// BestScore returns the best known score of the object.
func (e Event) BestScore() float64 { return math.Max(e.Score, e.TopScore) }

type EventMessage struct {
	Type   string `json:"type"`
	Before *Event `json:"before"`
	After  Event  `json:"after"`
}

type ReviewData struct {
	Detections []string `json:"detections"`
	Objects    []string `json:"objects"`
	SubLabels  []string `json:"sub_labels"`
	Zones      []string `json:"zones"`
}

// Review is a review item (alert or detection) published on frigate/reviews.
type Review struct {
	ID        string     `json:"id"`
	Camera    string     `json:"camera"`
	Severity  string     `json:"severity"`
	StartTime float64    `json:"start_time"`
	EndTime   *float64   `json:"end_time"`
	Data      ReviewData `json:"data"`
}

type ReviewMessage struct {
	Type   string  `json:"type"`
	Before *Review `json:"before"`
	After  Review  `json:"after"`
}

// TrackedObjectUpdate is published on frigate/tracked_object_update (e.g. GenAI description).
type TrackedObjectUpdate struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	Description string `json:"description"`
}

// APIEvent is an event as returned by GET /api/events.
type APIEvent struct {
	ID          string   `json:"id"`
	Camera      string   `json:"camera"`
	Label       string   `json:"label"`
	SubLabel    SubLabel `json:"sub_label"`
	StartTime   float64  `json:"start_time"`
	EndTime     *float64 `json:"end_time"`
	Zones       []string `json:"zones"`
	HasClip     bool     `json:"has_clip"`
	HasSnapshot bool     `json:"has_snapshot"`
	// FalsePositive: present in the Frigate versions that fill it in.
	FalsePositive bool     `json:"false_positive"`
	TopScore      *float64 `json:"top_score"`
	Data          struct {
		TopScore float64 `json:"top_score"`
	} `json:"data"`
}

// Score returns the best score, wherever the Frigate version puts it.
func (e APIEvent) Score() float64 {
	if e.TopScore != nil && *e.TopScore > 0 {
		return *e.TopScore
	}
	return e.Data.TopScore
}

var errNoID = errors.New("frigate message without an id")

func ParseEventMessage(b []byte) (EventMessage, error) {
	var m EventMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("unreadable frigate event: %w", err)
	}
	if m.After.ID == "" {
		return m, errNoID
	}
	return m, nil
}

func ParseReviewMessage(b []byte) (ReviewMessage, error) {
	var m ReviewMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("unreadable frigate review: %w", err)
	}
	if m.After.ID == "" {
		return m, errNoID
	}
	return m, nil
}

func ParseTrackedObjectUpdate(b []byte) (TrackedObjectUpdate, error) {
	var u TrackedObjectUpdate
	if err := json.Unmarshal(b, &u); err != nil {
		return u, fmt.Errorf("unreadable tracked_object_update: %w", err)
	}
	if u.ID == "" {
		return u, errNoID
	}
	return u, nil
}

// UnixTime converts a Frigate timestamp (floating-point seconds) into a time.Time.
func UnixTime(ts float64) time.Time {
	sec, frac := math.Modf(ts)
	return time.Unix(int64(sec), int64(math.Round(frac*1e9)))
}
