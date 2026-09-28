// Package frigate décrit les messages MQTT de Frigate et fournit un client pour son API HTTP.
package frigate

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
)

// SubLabel accepte les formats de Frigate : null, "nom" ou ["nom", score].
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
		return fmt.Errorf("sub_label: format inattendu %s", b)
	}
	if len(arr) == 0 {
		return nil
	}
	if err := json.Unmarshal(arr[0], &str); err != nil {
		return fmt.Errorf("sub_label: format inattendu %s", b)
	}
	*s = SubLabel(str)
	return nil
}

// Event est l'objet suivi publié sur frigate/events.
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

// BestScore renvoie le meilleur score connu de l'objet.
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

// Review est un élément de revue (alert ou detection) publié sur frigate/reviews.
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

// TrackedObjectUpdate est publié sur frigate/tracked_object_update (ex. description GenAI).
type TrackedObjectUpdate struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	Description string `json:"description"`
}

// APIEvent est un événement tel que renvoyé par GET /api/events.
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
	TopScore    *float64 `json:"top_score"`
	Data        struct {
		TopScore float64 `json:"top_score"`
	} `json:"data"`
}

// Score renvoie le meilleur score, quel que soit l'emplacement utilisé par la version de Frigate.
func (e APIEvent) Score() float64 {
	if e.TopScore != nil && *e.TopScore > 0 {
		return *e.TopScore
	}
	return e.Data.TopScore
}

var errNoID = errors.New("message frigate sans id")

func ParseEventMessage(b []byte) (EventMessage, error) {
	var m EventMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("event frigate illisible: %w", err)
	}
	if m.After.ID == "" {
		return m, errNoID
	}
	return m, nil
}

func ParseReviewMessage(b []byte) (ReviewMessage, error) {
	var m ReviewMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("review frigate illisible: %w", err)
	}
	if m.After.ID == "" {
		return m, errNoID
	}
	return m, nil
}

func ParseTrackedObjectUpdate(b []byte) (TrackedObjectUpdate, error) {
	var u TrackedObjectUpdate
	if err := json.Unmarshal(b, &u); err != nil {
		return u, fmt.Errorf("tracked_object_update illisible: %w", err)
	}
	if u.ID == "" {
		return u, errNoID
	}
	return u, nil
}

// UnixTime convertit un horodatage Frigate (secondes flottantes) en time.Time.
func UnixTime(ts float64) time.Time {
	sec, frac := math.Modf(ts)
	return time.Unix(int64(sec), int64(math.Round(frac*1e9)))
}
