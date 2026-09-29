package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"frigate-telegram/internal/config"
	"frigate-telegram/internal/frigate"
)

const testConfig = `
frigate:
  url: http://frigate:5000
mqtt:
  broker: tcp://mqtt:1883
telegram:
  token: abc
  admins: [42]
  chats:
    moi: 42
    famille: -100
notify:
  chats: [moi]
  labels: [person]
cameras:
  garage:
    min_score: 0.9
  salon: {}
`

type fakeCameras struct {
	cams []frigate.CameraInfo
	err  error
}

func (f fakeCameras) CameraDetails(context.Context) ([]frigate.CameraInfo, error) {
	return f.cams, f.err
}

// setup monte l'interface sur un serveur de test et renvoie la config vivante,
// le chemin du fichier de surcharge et le serveur.
func setup(t *testing.T, password string, cams fakeCameras) (*config.Config, string, *httptest.Server) {
	t.Helper()
	cfg, err := config.Parse([]byte(testConfig), func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cfg.Web.Password = password
	path := filepath.Join(t.TempDir(), "notify.yml")
	h := New(cfg, path, cams, slog.New(slog.DiscardHandler))
	mux := http.NewServeMux()
	h.Mount(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return cfg, path, ts
}

func do(t *testing.T, ts *httptest.Server, method, path, body string, auth ...string) *http.Response {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, ts.URL+path, r)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", requestedWith)
	if len(auth) > 0 {
		req.SetBasicAuth("admin", auth[0])
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestGetSettingsDescribesConfigAndCameras(t *testing.T) {
	cams := fakeCameras{cams: []frigate.CameraInfo{
		{Name: "jardin", Zones: []string{"allee"}, Labels: []string{"person", "car"}},
	}}
	_, _, ts := setup(t, "", cams)

	resp := do(t, ts, "GET", "/api/settings", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("statut = %d", resp.StatusCode)
	}
	var s settings
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	if len(s.Chats) != 2 || s.Chats[0] != "famille" {
		t.Errorf("chats = %v", s.Chats)
	}
	if len(s.Cameras) != 1 || s.Cameras[0].Zones[0] != "allee" {
		t.Errorf("cameras = %+v", s.Cameras)
	}
	if s.Custom {
		t.Error("aucun fichier de surcharge n'existe encore, custom devrait être faux")
	}
	if s.Overlay.Notify.Labels == nil || (*s.Overlay.Notify.Labels)[0] != "person" {
		t.Errorf("overlay.notify = %+v", s.Overlay.Notify)
	}
	if got := s.Overlay.Cameras["garage"]; got.MinScore == nil || got.MinScore.Default != 0.9 {
		t.Errorf("garage doit apparaître comme surcharge : %+v", got)
	}
}

func TestGetSettingsWarnsWhenFrigateIsDown(t *testing.T) {
	_, _, ts := setup(t, "", fakeCameras{err: errors.New("connexion refusée")})

	var s settings
	if err := json.NewDecoder(do(t, ts, "GET", "/api/settings", "").Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	if s.Warning == "" {
		t.Error("un avertissement était attendu")
	}
	// Les caméras déjà réglées restent proposées, sinon l'interface les effacerait.
	if len(s.Cameras) != 2 || s.Cameras[0].Name != "garage" || s.Cameras[1].Name != "salon" {
		t.Errorf("cameras = %+v", s.Cameras)
	}
}

func TestPutAppliesImmediatelyAndPersists(t *testing.T) {
	cfg, path, ts := setup(t, "", fakeCameras{})

	body := `{"notify":{"chats":["famille"],"labels":["car"],"cooldown":"30s"},
	          "cameras":{"jardin":{"min_score":{"default":0.5}}}}`
	resp := do(t, ts, "PUT", "/api/settings", body)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("statut = %d : %s", resp.StatusCode, b)
	}

	if got := cfg.Global(); got.Cooldown != 30*time.Second || got.Chats[0] != "famille" {
		t.Errorf("les réglages n'ont pas été appliqués à chaud : %+v", got)
	}
	if got := cfg.ForCamera("jardin").MinScore.Default; got != 0.5 {
		t.Errorf("jardin.min_score = %v", got)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("le fichier de surcharge n'a pas été écrit : %v", err)
	}
	if !strings.Contains(string(raw), "cooldown: 30s") {
		t.Errorf("fichier écrit :\n%s", raw)
	}
}

func TestPutRejectsInvalidSettingsWithoutWriting(t *testing.T) {
	cfg, path, ts := setup(t, "", fakeCameras{})
	before := cfg.Global()

	resp := do(t, ts, "PUT", "/api/settings", `{"notify":{"chats":["inconnu"]}}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("statut = %d, attendu 400", resp.StatusCode)
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if !strings.Contains(body["error"], "inconnu") {
		t.Errorf("message = %q", body["error"])
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Error("rien ne doit être écrit quand la validation échoue")
	}
	if cfg.Global().Chats[0] != before.Chats[0] {
		t.Error("les réglages en vigueur ont changé malgré le refus")
	}
}

func TestPutRejectsUnknownFields(t *testing.T) {
	_, _, ts := setup(t, "", fakeCameras{})
	resp := do(t, ts, "PUT", "/api/settings", `{"notify":{"coldown":"30s"}}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("statut = %d, une clé mal orthographiée doit être signalée", resp.StatusCode)
	}
}

func TestResetReturnsToConfigFile(t *testing.T) {
	cfg, path, ts := setup(t, "", fakeCameras{})

	if resp := do(t, ts, "PUT", "/api/settings", `{"notify":{"labels":["car"]},"cameras":{}}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT: statut %d", resp.StatusCode)
	}
	if resp := do(t, ts, "POST", "/api/settings/reset", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("reset: statut %d", resp.StatusCode)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Error("le fichier de surcharge devait être supprimé")
	}
	if got := cfg.Global().Labels; len(got) != 1 || got[0] != "person" {
		t.Errorf("labels = %v, config.yml devait reprendre la main", got)
	}
	if got := cfg.ForCamera("garage").MinScore.Default; got != 0.9 {
		t.Errorf("garage.min_score = %v, la surcharge de config.yml devait revenir", got)
	}
}

func TestPasswordProtectsInterfaceButNotProbes(t *testing.T) {
	_, _, ts := setup(t, "s3cret", fakeCameras{})

	for _, tc := range []struct {
		name, method, path, body string
		auth                     []string
		want                     int
	}{
		{name: "sans mot de passe", method: "GET", path: "/api/settings", want: http.StatusUnauthorized},
		{name: "mauvais mot de passe", method: "GET", path: "/api/settings", auth: []string{"autre"}, want: http.StatusUnauthorized},
		{name: "page sans mot de passe", method: "GET", path: "/", want: http.StatusUnauthorized},
		{name: "écriture sans mot de passe", method: "PUT", path: "/api/settings", body: "{}", want: http.StatusUnauthorized},
		{name: "bon mot de passe", method: "GET", path: "/api/settings", auth: []string{"s3cret"}, want: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := do(t, ts, tc.method, tc.path, tc.body, tc.auth...)
			if resp.StatusCode != tc.want {
				t.Errorf("statut = %d, attendu %d", resp.StatusCode, tc.want)
			}
		})
	}
}

func TestPageIsServed(t *testing.T) {
	_, _, ts := setup(t, "", fakeCameras{})
	resp := do(t, ts, "GET", "/", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("statut = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "api/settings") {
		t.Error("la page ne référence pas son API")
	}
}

func TestWritesRequireTheRequestedWithHeader(t *testing.T) {
	_, path, ts := setup(t, "", fakeCameras{})

	// Une page tierce peut envoyer un POST simple sans contrôle préalable CORS ;
	// l'en-tête, lui, ne peut pas être ajouté sans ce contrôle.
	for _, tc := range []struct{ method, path, body string }{
		{"PUT", "/api/settings", `{"notify":{"labels":["car"]}}`},
		{"POST", "/api/settings/reset", ""},
	} {
		req, err := http.NewRequest(tc.method, ts.URL+tc.path, strings.NewReader(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s : statut = %d, attendu 403", tc.method, tc.path, resp.StatusCode)
		}
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Error("une écriture refusée ne doit rien laisser sur disque")
	}
}

// Ouvrir l'interface puis enregistrer sans rien toucher doit laisser les réglages
// effectifs identiques : c'est l'aller-retour que fait le premier enregistrement,
// celui qui bascule une installation de config.yml vers le fichier de surcharge.
func TestSavingUntouchedSettingsChangesNothing(t *testing.T) {
	cfg, _, ts := setup(t, "", fakeCameras{})
	names := append(cfg.CameraNames(), "inconnue")
	before := map[string]config.Notify{"": cfg.Global()}
	for _, n := range names {
		before[n] = cfg.ForCamera(n)
	}

	var s settings
	if err := json.NewDecoder(do(t, ts, "GET", "/api/settings", "").Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	// La page retire les caméras qui ne surchargent rien avant d'envoyer.
	for name, patch := range s.Overlay.Cameras {
		if patch == (config.NotifyPatch{}) {
			delete(s.Overlay.Cameras, name)
		}
	}
	body, err := json.Marshal(s.Overlay)
	if err != nil {
		t.Fatal(err)
	}
	if resp := do(t, ts, "PUT", "/api/settings", string(body)); resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("statut = %d : %s", resp.StatusCode, b)
	}

	if got := cfg.Global(); !reflect.DeepEqual(got, before[""]) {
		t.Errorf("global :\navant %+v\naprès %+v", before[""], got)
	}
	for _, n := range names {
		if got := cfg.ForCamera(n); !reflect.DeepEqual(got, before[n]) {
			t.Errorf("caméra %s :\navant %+v\naprès %+v", n, before[n], got)
		}
	}
}

// Personnaliser un réglage pour le vider — « cette caméra, elle, n'a aucune contrainte
// de zone » — doit être distingué de « ne rien surcharger ».
func TestEmptyOverrideIsDistinctFromNoOverride(t *testing.T) {
	cfg, _, ts := setup(t, "", fakeCameras{})

	body := `{"notify":{"zones":["allee"]},"cameras":{"jardin":{"zones":[]},"garage":{}}}`
	if resp := do(t, ts, "PUT", "/api/settings", body); resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("statut = %d : %s", resp.StatusCode, b)
	}
	if got := cfg.ForCamera("jardin").Zones; len(got) != 0 {
		t.Errorf("jardin.zones = %v, la surcharge vide devait lever la contrainte", got)
	}
	if got := cfg.ForCamera("garage").Zones; len(got) != 1 || got[0] != "allee" {
		t.Errorf("garage.zones = %v, sans surcharge elle suit le global", got)
	}
}
