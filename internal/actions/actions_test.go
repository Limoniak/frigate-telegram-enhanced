package actions

import (
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	cases := []struct {
		data string
		want Action
	}{
		{Mute("jardin", time.Hour), Action{Kind: KindMute, Camera: "jardin", Duration: time.Hour}},
		{Pause(30 * time.Minute), Action{Kind: KindPause, Duration: 30 * time.Minute}},
		{Clip("1727520000.123456-abc123"), Action{Kind: KindClip, ID: "1727520000.123456-abc123"}},
		{Snapshot("garage"), Action{Kind: KindSnapshot, Camera: "garage"}},
	}
	for _, tc := range cases {
		got, err := Parse(tc.data)
		if err != nil || got != tc.want {
			t.Errorf("Parse(%q) = %+v, %v ; attendu %+v", tc.data, got, err, tc.want)
		}
	}
	if Mute("jardin", time.Hour) != "m:jardin:3600" || Pause(30*time.Minute) != "p:1800" {
		t.Error("encodage inattendu")
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	for _, data := range []string{"", "x", "z:1", "m:jardin", "m:jardin:abc", "p:-5", "c:"} {
		if _, err := Parse(data); err == nil {
			t.Errorf("Parse(%q) aurait dû échouer", data)
		}
	}
}
