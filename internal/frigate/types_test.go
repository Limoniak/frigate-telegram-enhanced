package frigate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseEventNew(t *testing.T) {
	m, err := ParseEventMessage(fixture(t, "event_new.json"))
	if err != nil {
		t.Fatal(err)
	}
	e := m.After
	if m.Type != "new" || e.ID != "1727520000.123456-abc123" || e.Camera != "jardin" || e.Label != "person" {
		t.Errorf("champs de base incorrects : %+v", m)
	}
	if e.SubLabel != "Alice" {
		t.Errorf("sub_label = %q", e.SubLabel)
	}
	if e.BestScore() != 0.87 {
		t.Errorf("BestScore = %v", e.BestScore())
	}
	if !reflect.DeepEqual(e.EnteredZones, []string{"allee"}) || !e.HasSnapshot || e.EndTime != nil {
		t.Errorf("zones/snapshot/end incorrects : %+v", e)
	}
	if m.Before == nil || !m.Before.FalsePositive {
		t.Error("before non décodé")
	}
}

func TestParseEventEnd(t *testing.T) {
	m, err := ParseEventMessage(fixture(t, "event_end.json"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Type != "end" || m.After.EndTime == nil || *m.After.EndTime != 1727520030.0 {
		t.Errorf("end incorrect : %+v", m.After)
	}
	if m.After.SubLabel != "Alice" {
		t.Errorf("sub_label (format chaîne) = %q", m.After.SubLabel)
	}
}

func TestSubLabelFormats(t *testing.T) {
	cases := map[string]SubLabel{`null`: "", `"Bob"`: "Bob", `["Bob",0.9]`: "Bob", `[]`: ""}
	for raw, want := range cases {
		var s SubLabel
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			t.Errorf("%s : %v", raw, err)
		}
		if s != want {
			t.Errorf("%s : %q, attendu %q", raw, s, want)
		}
	}
}

func TestParseReview(t *testing.T) {
	m, err := ParseReviewMessage(fixture(t, "review_new.json"))
	if err != nil {
		t.Fatal(err)
	}
	r := m.After
	if m.Type != "new" || r.Severity != "alert" || r.Camera != "jardin" || r.StartTime != 1727520000.2 {
		t.Errorf("review incorrecte : %+v", r)
	}
	if !reflect.DeepEqual(r.Data.Detections, []string{"1727520000.123456-abc123"}) ||
		!reflect.DeepEqual(r.Data.Objects, []string{"person"}) ||
		!reflect.DeepEqual(r.Data.Zones, []string{"allee"}) {
		t.Errorf("data incorrecte : %+v", r.Data)
	}
}

func TestParseTrackedObjectUpdate(t *testing.T) {
	u, err := ParseTrackedObjectUpdate(fixture(t, "tracked_description.json"))
	if err != nil {
		t.Fatal(err)
	}
	if u.Type != "description" || u.ID != "1727520000.123456-abc123" || u.Description == "" {
		t.Errorf("update incorrecte : %+v", u)
	}
}

func TestParseRejectsMissingID(t *testing.T) {
	if _, err := ParseEventMessage([]byte(`{"type":"new","after":{}}`)); err == nil {
		t.Error("event sans id accepté")
	}
	if _, err := ParseReviewMessage([]byte(`{"type":"new","after":{}}`)); err == nil {
		t.Error("review sans id acceptée")
	}
	if _, err := ParseEventMessage([]byte(`pas du json`)); err == nil {
		t.Error("json invalide accepté")
	}
}

func TestAPIEventScore(t *testing.T) {
	var e APIEvent
	if err := json.Unmarshal([]byte(`{"id":"e1","top_score":null,"data":{"top_score":0.91}}`), &e); err != nil {
		t.Fatal(err)
	}
	if e.Score() != 0.91 {
		t.Errorf("Score = %v", e.Score())
	}
}

func TestUnixTime(t *testing.T) {
	if got := UnixTime(1727520000.5).UnixMilli(); got != 1727520000500 {
		t.Errorf("UnixTime = %d", got)
	}
}
