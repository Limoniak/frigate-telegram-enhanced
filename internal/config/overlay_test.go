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
	// Les durées doivent rester lisibles : time.Duration s'encoderait en nanosecondes.
	if !strings.Contains(string(raw), "cooldown: 1m0s") {
		t.Errorf("durée illisible dans le fichier écrit :\n%s", raw)
	}
	back, err := LoadOverlay(path)
	if err != nil {
		t.Fatalf("LoadOverlay: %v", err)
	}
	if err := c.ApplyOverlay(back); err != nil {
		t.Fatalf("ApplyOverlay: %v", err)
	}
	if got := c.Global().Cooldown; got != time.Minute {
		t.Errorf("cooldown après aller-retour = %v", got)
	}
}

func TestLoadOverlayMissingFileIsNotAnError(t *testing.T) {
	o, err := LoadOverlay(filepath.Join(t.TempDir(), "absent.yml"))
	if err != nil || o != nil {
		t.Fatalf("LoadOverlay(absent) = %v, %v ; attendu nil, nil", o, err)
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
	// L'overlay remplace entièrement la section cameras : garage retrouve le global.
	if got := c.ForCamera("garage").MinScore.Default; got != 0 {
		t.Errorf("garage.min_score = %v, la surcharge de config.yml devait disparaître", got)
	}
	if got := c.ForCamera("jardin").Zones; len(got) != 1 || got[0] != "allee" {
		t.Errorf("jardin.zones = %v", got)
	}

	// Retour à config.yml.
	if err := c.ApplyOverlay(nil); err != nil {
		t.Fatalf("ApplyOverlay(nil): %v", err)
	}
	if got := c.ForCamera("garage").MinScore.Default; got != 0.9 {
		t.Errorf("garage.min_score après retour = %v", got)
	}
	if got := c.Global().Labels; len(got) != 1 || got[0] != "person" {
		t.Errorf("labels après retour = %v", got)
	}
}

func TestApplyOverlayRejectsInvalidAndKeepsPrevious(t *testing.T) {
	c := mustParse(t, minimal)
	before := c.Global()

	o := &Overlay{Notify: NotifyPatch{Chats: &[]string{"inconnu"}}}
	err := c.ApplyOverlay(o)
	if err == nil || !strings.Contains(err.Error(), "inconnu") {
		t.Fatalf("ApplyOverlay = %v, attendu un refus mentionnant le chat inconnu", err)
	}
	if got := c.Global(); got.Chats[0] != before.Chats[0] {
		t.Errorf("les réglages ont changé malgré le refus : %v", got.Chats)
	}
	if c.ValidateOverlay(o, c.Language) == nil {
		t.Error("ValidateOverlay devrait refuser le même overlay")
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
		t.Errorf("le global doit être entièrement décrit : %+v", o.Notify)
	}
	garage := o.Cameras["garage"]
	if garage.Labels == nil || len(*garage.Labels) != 1 {
		t.Errorf("garage.labels = %+v", garage.Labels)
	}
	if garage.Cooldown != nil {
		t.Errorf("garage ne surcharge pas cooldown, il ne doit pas apparaître : %v", *garage.Cooldown)
	}
	if salon, ok := o.Cameras["salon"]; !ok || salon != (NotifyPatch{}) {
		t.Errorf("salon ne surcharge rien, son patch doit être vide : %+v", salon)
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
		t.Errorf("le fichier temporaire n'a pas été nettoyé : %v", entries)
	}
}

// Le rechargement se fait pendant que le moteur de filtrage lit la configuration :
// ce test n'a d'intérêt que sous -race.
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
		t.Fatalf("depuis la config = %q", c.ExternalURL())
	}
	o := c.CurrentOverlay()
	u := " https://frigate.maison.example/ "
	o.ExternalURL = &u
	if err := c.ApplyOverlay(&o); err != nil {
		t.Fatal(err)
	}
	if c.ExternalURL() != "https://frigate.maison.example" {
		t.Errorf("après modification = %q", c.ExternalURL())
	}
	bad := "frigate.maison"
	o.ExternalURL = &bad
	if err := c.ApplyOverlay(&o); err == nil || !strings.Contains(err.Error(), "external URL") {
		t.Errorf("adresse sans http(s) : %v", err)
	}
	empty := ""
	o.ExternalURL = &empty
	if err := c.ApplyOverlay(&o); err != nil || c.ExternalURL() != "http://frigate:5000" {
		t.Errorf("vide = l'adresse de frigate.url : %q, %v", c.ExternalURL(), err)
	}
	if err := c.ApplyOverlay(nil); err != nil || c.ExternalURL() != "https://ancien.example" {
		t.Errorf("retour à la config : %q, %v", c.ExternalURL(), err)
	}
}

func TestExternalURLDefaultsToFrigateURL(t *testing.T) {
	if c := mustParse(t, minimal); c.ExternalURL() != "http://frigate:5000" {
		t.Errorf("sans adresse externe, les liens utilisent frigate.url : %q", c.ExternalURL())
	}
}
