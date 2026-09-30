// Package web sert l'interface de réglage des notifications : une page unique et
// une petite API JSON au-dessus du fichier de surcharge (voir config.Overlay).
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"frigate-telegram-enhanced/internal/bot"
	"frigate-telegram-enhanced/internal/config"
	"frigate-telegram-enhanced/internal/frigate"
	"frigate-telegram-enhanced/internal/i18n"
	"frigate-telegram-enhanced/internal/notifier"
	"frigate-telegram-enhanced/internal/state"
)

// L'interface : la page, sa feuille de style et son script, sans étape de build.
//
//go:embed ui.html ui.css ui.js
var assets embed.FS

// maxBody borne la taille d'un enregistrement : la surcharge d'une installation
// réaliste pèse quelques kilo-octets.
const maxBody = 1 << 20

// SubLabelLister donne les étiquettes connues de Frigate ; facultatif pour CameraLister.
type SubLabelLister interface {
	SubLabels(ctx context.Context) ([]string, error)
}

// CameraLister énumère les caméras de Frigate avec leurs zones et leurs objets suivis.
type CameraLister interface {
	CameraDetails(ctx context.Context) ([]frigate.CameraInfo, error)
}

// Tester envoie une notification d'exemple pour une caméra.
type Tester interface {
	SendTest(ctx context.Context, camera string) error
}

// State est l'état partagé avec les commandes Telegram : pause globale et caméras coupées.
type State interface {
	Status(now time.Time) state.Status
	Pause(until time.Time) error
	Resume() error
	Unmute(camera string) error
}

// History donne les dernières détections et leurs miniatures.
type History interface {
	History() []notifier.HistoryEntry
	HistoryThumb(id string) (string, bool)
}

// Media lit une image sur Frigate.
type Media interface {
	GetBytes(ctx context.Context, path string, max int64) ([]byte, error)
}

// maxThumb borne la taille d'une miniature relayée depuis Frigate.
const maxThumb = 1 << 20

// État d'une connexion, tel que l'interface l'affiche.
const (
	StateOK      = "ok"
	StatePending = "pending"
	StateWarn    = "warn" // fonctionne, mais avec un problème à signaler
	StateError   = "error"
)

// Component décrit l'état d'une connexion du service (Frigate, MQTT, Telegram).
type Component struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Detail string `json:"detail"`
	Hint   string `json:"hint,omitempty"` // que corriger, en cas d'erreur
}

// Refusals donne les utilisateurs Telegram refusés récemment par le bot.
type Refusals interface {
	Refused() []bot.Refused
}

// HealthFunc diagnostique les connexions, avec des messages dans la langue l.
type HealthFunc func(ctx context.Context, l i18n.Lang) []Component

// testInterval espace les notifications de test : un double clic ne doit pas
// en envoyer deux.
const testInterval = 3 * time.Second

type Handler struct {
	cfg     *config.Config
	path    string // fichier de surcharge
	cameras CameraLister
	log     *slog.Logger
	tester  Tester // nil : pas d'envoi de test
	state   State  // nil : pas de pause depuis l'interface
	history History
	media   Media      // nil : historique sans miniatures
	health  HealthFunc // nil : pas d'état des connexions
	refused Refusals   // nil : pas de liste des utilisateurs refusés
	auth    *Auth      // mot de passe, commun à toutes les routes
	now     func() time.Time

	// save sérialise les enregistrements : deux onglets ouverts en même temps ne
	// doivent pas entrelacer validation, écriture et application.
	save sync.Mutex

	testMu   sync.Mutex
	lastTest time.Time
}

type Option func(*Handler)

// WithTester active le bouton « m'envoyer un exemple ».
func WithTester(t Tester) Option { return func(h *Handler) { h.tester = t } }

// WithState active l'affichage de la pause et les boutons pause/reprise.
func WithState(s State) Option { return func(h *Handler) { h.state = s } }

// WithHistory active l'activité récente ; media (facultatif) sert les miniatures.
func WithHistory(h History, media Media) Option {
	return func(x *Handler) { x.history, x.media = h, media }
}

// WithHealth active l'affichage de l'état des connexions.
func WithHealth(f HealthFunc) Option { return func(h *Handler) { h.health = f } }

// WithRefused joint à l'état des connexions les utilisateurs Telegram refusés
// récemment, avec leur identifiant à ajouter à la configuration.
func WithRefused(r Refusals) Option { return func(h *Handler) { h.refused = r } }

// WithClock remplace l'horloge (tests).
func WithClock(now func() time.Time) Option { return func(h *Handler) { h.now = now } }

func New(cfg *config.Config, overlayPath string, cameras CameraLister, log *slog.Logger, opts ...Option) *Handler {
	h := &Handler{cfg: cfg, path: overlayPath, cameras: cameras, log: log, now: time.Now,
		auth: NewAuth(cfg.Web.Password, log)}
	for _, o := range opts {
		o(h)
	}
	return h
}

// Protect soumet next au mot de passe de l'interface, avec le même décompte
// d'échecs (sert à /metrics avec web.protect_metrics).
func (h *Handler) Protect(next http.Handler) http.Handler { return h.auth.Wrap(next) }

// Mount enregistre l'interface et son API sur mux, protégées par mot de passe si la
// configuration en définit un. /healthz et /metrics restent en dehors : la sonde du
// conteneur et le scrape Prometheus ne s'authentifient pas (voir web.protect_metrics).
func (h *Handler) Mount(mux *http.ServeMux) {
	mux.Handle("GET /{$}", h.guard(asset("ui.html", "text/html; charset=utf-8")))
	mux.Handle("GET /ui.css", h.guard(asset("ui.css", "text/css; charset=utf-8")))
	mux.Handle("GET /ui.js", h.guard(asset("ui.js", "text/javascript; charset=utf-8")))
	mux.Handle("GET /i18n.js", h.guard(serve(i18nScript, "text/javascript; charset=utf-8")))
	mux.Handle("GET /api/settings", h.guard(http.HandlerFunc(h.get)))
	mux.Handle("PUT /api/settings", h.guard(sameOrigin(http.HandlerFunc(h.put))))
	mux.Handle("POST /api/settings/reset", h.guard(sameOrigin(http.HandlerFunc(h.reset))))
	mux.Handle("POST /api/test", h.guard(sameOrigin(http.HandlerFunc(h.test))))
	mux.Handle("GET /api/state", h.guard(http.HandlerFunc(h.getState)))
	mux.Handle("POST /api/pause", h.guard(sameOrigin(http.HandlerFunc(h.pause))))
	mux.Handle("POST /api/resume", h.guard(sameOrigin(http.HandlerFunc(h.resume))))
	mux.Handle("GET /api/history", h.guard(http.HandlerFunc(h.getHistory)))
	mux.Handle("GET /api/health", h.guard(http.HandlerFunc(h.getHealth)))
	mux.Handle("GET /api/history/{id}/thumb", h.guard(http.HandlerFunc(h.thumb)))
}

// guard applique le contrôle d'accès commun à toutes les routes de l'interface :
// le mot de passe s'il y en a un, sinon la vérification de l'en-tête Host.
func (h *Handler) guard(next http.Handler) http.Handler {
	if h.cfg.Web.Password != "" {
		return h.auth.Wrap(next)
	}
	return checkHost(h.cfg.Web.AllowedHosts, h.cfg.Language, next)
}

// checkHost refuse les requêtes dont l'en-tête Host n'est ni une adresse IP, ni
// localhost, ni un nom listé dans allowed. Sans mot de passe, c'est ce qui bloque le
// rebinding DNS : un site tiers qui fait pointer son propre domaine vers 127.0.0.1
// devient « même origine » pour le navigateur — X-Requested-With ne l'arrête plus —
// mais ses requêtes portent toujours son nom de domaine dans Host.
func checkHost(allowed []string, def i18n.Lang, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hostAllowed(r.Host, allowed) {
			http.Error(w, requestLang(r, def).T(
				"host not allowed: add this name to WEB_ALLOWED_HOSTS (web.allowed_hosts), or set WEB_PASSWORD"), http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func hostAllowed(host string, allowed []string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	if host == "" {
		return false
	}
	if net.ParseIP(host) != nil || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	return slices.ContainsFunc(allowed, func(a string) bool { return strings.EqualFold(a, host) })
}

// requestedWith est l'en-tête que la page joint à ses écritures. Il ne vaut rien comme
// secret : il sert à rendre la requête « non simple » au sens CORS, pour qu'un site
// tiers ouvert dans le même navigateur ne puisse pas la déclencher à notre insu — le
// contrôle préalable qu'elle impose échouera, faute d'en-têtes CORS de notre part.
const requestedWith = "frigate-telegram-enhanced"

func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Requested-With") != requestedWith {
			http.Error(w, requestLang(r, i18n.Default).T("request refused: missing X-Requested-With header"), http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// asset sert un fichier de l'interface. no-store : après une mise à jour du
// service, le navigateur ne doit pas garder l'ancien script avec la nouvelle page.
func asset(name, contentType string) http.Handler {
	body, err := assets.ReadFile(name)
	if err != nil {
		panic(err) // embarqué : absent seulement si le code est incohérent
	}
	return serve(body, contentType)
}

func serve(body []byte, contentType string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		w.Write(body)
	})
}

// i18nScript is i18n.js: the languages and their catalogs, for the page's T().
var i18nScript = func() []byte {
	type language struct {
		Code i18n.Lang `json:"code"`
		Name string    `json:"name"`
	}
	var langs []language
	catalogs := map[i18n.Lang]map[string]string{}
	for _, l := range i18n.Languages() {
		langs = append(langs, language{Code: l, Name: l.Name()})
		if l != i18n.EN {
			catalogs[l] = i18n.Catalogs()[l].Messages
		}
	}
	lj, err := json.Marshal(langs)
	if err != nil {
		panic(err)
	}
	cj, err := json.Marshal(catalogs)
	if err != nil {
		panic(err)
	}
	return []byte("\"use strict\";\nconst LANGUAGES = " + string(lj) + ";\nconst CATALOGS = " + string(cj) + ";\n")
}()

// settings est la vue que l'interface charge au démarrage.
type settings struct {
	Mode     string               `json:"mode"`
	Timezone string               `json:"timezone"`
	Chats    []string             `json:"chats"`
	Cameras  []frigate.CameraInfo `json:"cameras"`
	Overlay  config.Overlay       `json:"overlay"`
	Custom   bool                 `json:"custom"`  // une surcharge est enregistrée
	Warning  string               `json:"warning"` // Frigate injoignable, etc.
	CanTest  bool                 `json:"can_test"`
	CanPause bool                 `json:"can_pause"`
	CanHist  bool                 `json:"can_history"`
	CanHlth  bool                 `json:"can_health"`
	Presence bool                 `json:"presence"` // topics de présence configurés
	// FrigateURL est l'adresse des liens quand aucune adresse externe n'est réglée.
	FrigateURL string `json:"frigate_url"`
	// SubLabels : étiquettes connues de Frigate, proposées par le filtre des étiquettes.
	SubLabels []string `json:"sub_labels"`
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	s := settings{
		Mode:       h.cfg.Mode,
		Timezone:   h.cfg.Timezone,
		Chats:      h.cfg.ChatNames(),
		Overlay:    h.cfg.CurrentOverlay(),
		CanTest:    h.tester != nil,
		CanPause:   h.state != nil,
		CanHist:    h.history != nil,
		CanHlth:    h.health != nil,
		Presence:   h.cfg.Presence.Enabled(),
		FrigateURL: h.cfg.Frigate.URL,
	}
	if _, err := os.Stat(h.path); err == nil {
		s.Custom = true
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	cams, err := h.cameras.CameraDetails(ctx)
	if err != nil {
		// Frigate injoignable : on affiche au moins les caméras déjà réglées,
		// pour que l'interface reste utilisable et n'efface rien.
		s.Warning = h.lang(r).T("Frigate is unreachable: the list of cameras, zones and objects is incomplete.")
		for _, name := range h.cfg.CameraNames() {
			cams = append(cams, frigate.CameraInfo{Name: name})
		}
	}
	s.Cameras = cams
	s.SubLabels = []string{}
	if sl, ok := h.cameras.(SubLabelLister); ok && err == nil {
		if subs, err := sl.SubLabels(ctx); err == nil {
			s.SubLabels = subs
		}
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *Handler) put(w http.ResponseWriter, r *http.Request) {
	var o config.Overlay
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&o); err != nil {
		h.writeError(w, r, http.StatusBadRequest, err)
		return
	}

	h.save.Lock()
	defer h.save.Unlock()
	// Valider avant d'écrire : un fichier de surcharge invalide serait rejeté au
	// prochain démarrage, et le service repartirait sur config.yml sans prévenir.
	if err := h.cfg.ValidateOverlay(&o, h.lang(r)); err != nil {
		h.writeError(w, r, http.StatusBadRequest, err)
		return
	}
	if err := config.SaveOverlay(h.path, o); err != nil {
		h.log.Error("saving settings failed", "err", err)
		h.writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	if err := h.cfg.ApplyOverlay(&o); err != nil {
		h.writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	h.log.Info("notification settings updated from the web interface")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) reset(w http.ResponseWriter, r *http.Request) {
	h.save.Lock()
	defer h.save.Unlock()
	if err := os.Remove(h.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		h.writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	if err := h.cfg.ApplyOverlay(nil); err != nil {
		h.writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	h.log.Info("notification settings reverted to config.yml")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// decodeBody lit un petit corps JSON ; un corps vide laisse v à sa valeur zéro.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func (h *Handler) test(w http.ResponseWriter, r *http.Request) {
	if h.tester == nil {
		h.writeError(w, r, http.StatusNotFound, i18n.NewError("test notifications are unavailable"))
		return
	}
	var req struct {
		Camera string `json:"camera"`
	}
	if err := decodeBody(w, r, &req); err != nil || req.Camera == "" {
		h.writeError(w, r, http.StatusBadRequest, i18n.NewError("missing camera"))
		return
	}
	if !h.allowTest() {
		h.writeError(w, r, http.StatusTooManyRequests, i18n.NewError("a test was just sent, wait a few seconds"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := h.tester.SendTest(ctx, req.Camera); err != nil {
		h.log.Warn("test notification failed", "camera", req.Camera, "err", err)
		h.writeError(w, r, http.StatusBadGateway, err)
		return
	}
	h.log.Info("test notification sent from the web interface", "camera", req.Camera)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// allowTest réserve un créneau d'envoi de test ; false si le précédent date de
// moins de testInterval.
func (h *Handler) allowTest() bool {
	h.testMu.Lock()
	defer h.testMu.Unlock()
	now := h.now()
	if !h.lastTest.IsZero() && now.Sub(h.lastTest) < testInterval {
		return false
	}
	h.lastTest = now
	return true
}

// stateView est l'état de pause tel que l'interface l'affiche. Une échéance nulle
// signifie « jusqu'à reprise ».
type stateView struct {
	Paused      bool       `json:"paused"`
	PausedUntil *time.Time `json:"paused_until"`
	Mutes       []muteView `json:"mutes"`
	Home        []string   `json:"home"` // topics de présence « à la maison »
}

type muteView struct {
	Camera string     `json:"camera"`
	Until  *time.Time `json:"until"`
}

func until(t time.Time) *time.Time {
	if !t.Before(state.Forever) {
		return nil
	}
	return &t
}

func (h *Handler) writeState(w http.ResponseWriter) {
	st := h.state.Status(h.now())
	v := stateView{Paused: !st.PausedUntil.IsZero(), Mutes: []muteView{}, Home: append([]string{}, st.Home...)}
	if v.Paused {
		v.PausedUntil = until(st.PausedUntil)
	}
	for _, cam := range slices.Sorted(maps.Keys(st.Mutes)) {
		v.Mutes = append(v.Mutes, muteView{Camera: cam, Until: until(st.Mutes[cam])})
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) getState(w http.ResponseWriter, r *http.Request) {
	if h.state == nil {
		h.writeError(w, r, http.StatusNotFound, i18n.NewError("state unavailable"))
		return
	}
	h.writeState(w)
}

func (h *Handler) pause(w http.ResponseWriter, r *http.Request) {
	if h.state == nil {
		h.writeError(w, r, http.StatusNotFound, i18n.NewError("state unavailable"))
		return
	}
	var req struct {
		Minutes int `json:"minutes"`
	}
	if err := decodeBody(w, r, &req); err != nil || req.Minutes < 0 || req.Minutes > 7*24*60 {
		h.writeError(w, r, http.StatusBadRequest, i18n.NewError("invalid duration (0 = until resumed, 7 days at most)"))
		return
	}
	end := state.Forever
	if req.Minutes > 0 {
		end = h.now().Add(time.Duration(req.Minutes) * time.Minute)
	}
	if err := h.state.Pause(end); err != nil {
		h.writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	h.log.Info("notifications paused from the web interface", "minutes", req.Minutes)
	h.writeState(w)
}

func (h *Handler) resume(w http.ResponseWriter, r *http.Request) {
	if h.state == nil {
		h.writeError(w, r, http.StatusNotFound, i18n.NewError("state unavailable"))
		return
	}
	var req struct {
		Camera string `json:"camera"`
	}
	if err := decodeBody(w, r, &req); err != nil {
		h.writeError(w, r, http.StatusBadRequest, err)
		return
	}
	var err error
	if req.Camera == "" {
		err = h.state.Resume()
	} else {
		err = h.state.Unmute(req.Camera)
	}
	if err != nil {
		h.writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	h.log.Info("notifications resumed from the web interface", "camera", req.Camera)
	h.writeState(w)
}

func (h *Handler) getHealth(w http.ResponseWriter, r *http.Request) {
	if h.health == nil {
		h.writeError(w, r, http.StatusNotFound, i18n.NewError("health unavailable"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	refused := []bot.Refused{}
	if h.refused != nil {
		refused = append(refused, h.refused.Refused()...)
	}
	writeJSON(w, http.StatusOK, struct {
		Components []Component   `json:"components"`
		Refused    []bot.Refused `json:"refused"`
	}{h.health(ctx, h.lang(r)), refused})
}

type historyView struct {
	Entries []historyEntryView `json:"entries"`
}

type historyEntryView struct {
	notifier.HistoryEntry
	HasThumb bool `json:"has_thumb"`
}

func (h *Handler) getHistory(w http.ResponseWriter, r *http.Request) {
	if h.history == nil {
		h.writeError(w, r, http.StatusNotFound, i18n.NewError("history unavailable"))
		return
	}
	v := historyView{Entries: []historyEntryView{}}
	for _, e := range h.history.History() {
		v.Entries = append(v.Entries, historyEntryView{HistoryEntry: e, HasThumb: h.media != nil && e.Thumb != ""})
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) thumb(w http.ResponseWriter, r *http.Request) {
	if h.history == nil || h.media == nil {
		http.NotFound(w, r)
		return
	}
	path, ok := h.history.HistoryThumb(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	img, err := h.media.GetBytes(ctx, path, maxThumb)
	if err != nil {
		http.Error(w, h.lang(r).T("thumbnail unavailable"), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Write(img)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// writeError répond une erreur JSON, dans la langue de l'interface si l'erreur est bilingue.
func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, code int, err error) {
	writeJSON(w, code, map[string]string{"error": h.lang(r).Message(err)})
}

// langHeader porte la langue affichée par l'interface, jointe à chacune de ses requêtes.
const langHeader = "X-Lang"

// lang est la langue des réponses à r : celle de l'interface, sinon celle du
// navigateur, sinon celle du service.
func (h *Handler) lang(r *http.Request) i18n.Lang { return requestLang(r, h.cfg.Language) }

func requestLang(r *http.Request, def i18n.Lang) i18n.Lang {
	if l, err := i18n.Parse(r.Header.Get(langHeader)); err == nil && r.Header.Get(langHeader) != "" {
		return l
	}
	// Accept-Language : "fr-FR,fr;q=0.9,en;q=0.8" — seule la première langue compte.
	first, _, _ := strings.Cut(r.Header.Get("Accept-Language"), ",")
	first, _, _ = strings.Cut(first, ";")
	if l, err := i18n.Parse(first); err == nil && first != "" {
		return l
	}
	return def
}
