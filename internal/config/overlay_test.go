package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveLoadOverlayRoundTrip(t *testing.T) {
	c := mustParse(t, minimal)
	o := c.CurrentOverlay()
	o.Cameras = map[string]NotifyPatch{"jardin": DiffPatch(c.Global(), Notify{})}

	path := filepath.Join(t.TempDir(), "notify.yml")
	if err := SaveOverlay(path, o); err != nil {
		t.Fatalf("SaveOverlay: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Durations must stay readable: time.Duration would be encoded in nanoseconds.
	if !strings.Contains(string(raw), "cooldown: 1m0s") {
		t.Errorf("unreadable duration in the written file:\n%s", raw)
	}
	back, err := LoadOverlay(path)
	if err != nil {
		t.Fatalf("LoadOverlay: %v", err)
	}
	if err := c.ApplyOverlay(back); err != nil {
		t.Fatalf("ApplyOverlay: %v", err)
	}
	if got := c.Global().Cooldown; got != time.Minute {
		t.Errorf("cooldown after a round trip = %v", got)
	}
}

func TestLoadOverlayMissingFileIsNotAnError(t *testing.T) {
	o, err := LoadOverlay(filepath.Join(t.TempDir(), "absent.yml"))
	if err != nil || o != nil {
		t.Fatalf("LoadOverlay(missing) = %v, %v; want nil, nil", o, err)
	}
}

func TestApplyOverlayChangesEffectiveSettings(t *testing.T) {
	c := mustParse(t, minimal+`
notify:
  labels: [person]
cameras:
  garage:
    min_score: 0.9
`)
	cooldown := Duration(30 * time.Second)
	labels := []string{"car"}
	o := &Overlay{
		Notify:  NotifyPatch{Labels: &labels, Cooldown: &cooldown},
		Cameras: map[string]NotifyPatch{"jardin": {Zones: &[]string{"allee"}}},
	}
	if err := c.ApplyOverlay(o); err != nil {
		t.Fatalf("ApplyOverlay: %v", err)
	}
	if g := c.Global(); g.Cooldown != 30*time.Second || len(g.Labels) != 1 || g.Labels[0] != "car" {
		t.Errorf("global = %+v", g)
	}
	// The overlay entirely replaces the cameras section: garage gets the global settings back.
	if got := c.ForCamera("garage").MinScore.Default; got != 0 {
		t.Errorf("garage.min_score = %v, the override from config.yml should have gone", got)
	}
	if got := c.ForCamera("jardin").Zones; len(got) != 1 || got[0] != "allee" {
		t.Errorf("jardin.zones = %v", got)
	}

	// Back to config.yml.
	if err := c.ApplyOverlay(nil); err != nil {
		t.Fatalf("ApplyOverlay(nil): %v", err)
	}
	if got := c.ForCamera("garage").MinScore.Default; got != 0.9 {
		t.Errorf("garage.min_score after going back = %v", got)
	}
	if got := c.Global().Labels; len(got) != 1 || got[0] != "person" {
		t.Errorf("labels after going back = %v", got)
	}
}

func TestApplyOverlayRejectsInvalidAndKeepsPrevious(t *testing.T) {
	c := mustParse(t, minimal)
	before := c.Global()

	o := &Overlay{Notify: NotifyPatch{Chats: &[]string{"inconnu"}}}
	err := c.ApplyOverlay(o)
	if err == nil || !strings.Contains(err.Error(), "inconnu") {
		t.Fatalf("ApplyOverlay = %v, want a refusal mentioning the unknown chat", err)
	}
	if got := c.Global(); got.Chats[0] != before.Chats[0] {
		t.Errorf("the settings changed despite the refusal: %v", got.Chats)
	}
	if c.ValidateOverlay(o, c.Language) == nil {
		t.Error("ValidateOverlay should refuse the same overlay")
	}
}

func TestCurrentOverlayReducesCamerasToTheirDifferences(t *testing.T) {
	c := mustParse(t, minimal+`
notify:
  labels: [person, car]
cameras:
  garage:
    labels: [person]
  salon: {}
`)
	o := c.CurrentOverlay()
	if o.Notify.Labels == nil || len(*o.Notify.Labels) != 2 {
		t.Errorf("the global settings must be described in full: %+v", o.Notify)
	}
	garage := o.Cameras["garage"]
	if garage.Labels == nil || len(*garage.Labels) != 1 {
		t.Errorf("garage.labels = %+v", garage.Labels)
	}
	if garage.Cooldown != nil {
		t.Errorf("garage does not override cooldown, it must not appear: %v", *garage.Cooldown)
	}
	if salon, ok := o.Cameras["salon"]; !ok || salon != (NotifyPatch{}) {
		t.Errorf("salon overrides nothing, its patch must be empty: %+v", salon)
	}
}

func TestSaveOverlayReplacesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notify.yml")
	if err := os.WriteFile(path, []byte("ancien"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveOverlay(path, Overlay{}); err != nil {
		t.Fatalf("SaveOverlay: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "notify.yml" {
		t.Errorf("the temporary file was not cleaned up: %v", entries)
	}
}

// The reload happens while the filter engine reads the configuration: this test
// is only meaningful under -race.
func TestApplyOverlayIsSafeWhileReading(t *testing.T) {
	c := mustParse(t, minimal)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 200 {
			d := Duration(time.Duration(i) * time.Second)
			if err := c.ApplyOverlay(&Overlay{
				Notify:  NotifyPatch{Cooldown: &d},
				Cameras: map[string]NotifyPatch{"jardin": {}},
			}); err != nil {
				t.Errorf("ApplyOverlay: %v", err)
				return
			}
		}
	}()
	for range 200 {
		_ = c.ForCamera("jardin").Cooldown
		_ = c.Global().Chats
		_ = c.CameraNames()
	}
	<-done
}

func TestExternalURLOverride(t *testing.T) {
	c := mustParse(t, strings.Replace(minimal, "url: http://frigate:5000/", "url: http://frigate:5000/\n  external_url: https://ancien.example/", 1))
	if c.ExternalURL() != "https://ancien.example" {
		t.Fatalf("from the configuration = %q", c.ExternalURL())
	}
	o := c.CurrentOverlay()
	u := " https://frigate.maison.example/ "
	o.ExternalURL = &u
	if err := c.ApplyOverlay(&o); err != nil {
		t.Fatal(err)
	}
	if c.ExternalURL() != "https://frigate.maison.example" {
		t.Errorf("after the change = %q", c.ExternalURL())
	}
	bad := "frigate.maison"
	o.ExternalURL = &bad
	if err := c.ApplyOverlay(&o); err == nil || !strings.Contains(err.Error(), "external URL") {
		t.Errorf("address without http(s): %v", err)
	}
	empty := ""
	o.ExternalURL = &empty
	if err := c.ApplyOverlay(&o); err != nil || c.ExternalURL() != "http://frigate:5000" {
		t.Errorf("empty = the address of frigate.url: %q, %v", c.ExternalURL(), err)
	}
	if err := c.ApplyOverlay(nil); err != nil || c.ExternalURL() != "https://ancien.example" {
		t.Errorf("back to the configuration: %q, %v", c.ExternalURL(), err)
	}
}

func TestHomeAssistantURLOverride(t *testing.T) {
	c := mustParse(t, minimal+"home_assistant:\n  url: https://ha.example/\n")
	if c.HomeAssistantURL() != "https://ha.example" {
		t.Fatalf("from the configuration = %q", c.HomeAssistantURL())
	}
	o := c.CurrentOverlay()
	u := " https://maison.example:8123/ "
	o.HomeAssistantURL = &u
	if err := c.ApplyOverlay(&o); err != nil || c.HomeAssistantURL() != "https://maison.example:8123" {
		t.Errorf("after the change = %q, %v", c.HomeAssistantURL(), err)
	}
	bad := "homeassistant://navigate/lovelace"
	o.HomeAssistantURL = &bad
	if err := c.ApplyOverlay(&o); err == nil || !strings.Contains(err.Error(), "Home Assistant URL") {
		t.Errorf("address without http(s): %v", err)
	}
	empty := ""
	o.HomeAssistantURL = &empty
	if err := c.ApplyOverlay(&o); err != nil || c.HomeAssistantURL() != "" {
		t.Errorf("empty = no button: %q, %v", c.HomeAssistantURL(), err)
	}
	if err := c.ApplyOverlay(nil); err != nil || c.HomeAssistantURL() != "https://ha.example" {
		t.Errorf("back to the configuration: %q, %v", c.HomeAssistantURL(), err)
	}
	if _, err := Parse([]byte(minimal + "home_assistant:\n  url: ha.lan\n")); err == nil {
		t.Error("an invalid address in the configuration must be refused")
	}
}

func TestExternalURLDefaultsToFrigateURL(t *testing.T) {
	if c := mustParse(t, minimal); c.ExternalURL() != "http://frigate:5000" {
		t.Errorf("without an external address, the links use frigate.url: %q", c.ExternalURL())
	}
}
