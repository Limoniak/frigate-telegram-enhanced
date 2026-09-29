// Package web sert l'interface de réglage des notifications : une page unique et
// une petite API JSON au-dessus du fichier de surcharge (voir config.Overlay).
package web

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"frigate-telegram/internal/config"
	"frigate-telegram/internal/frigate"
)

//go:embed ui.html
var assets embed.FS

// maxBody borne la taille d'un enregistrement : la surcharge d'une installation
// réaliste pèse quelques kilo-octets.
const maxBody = 1 << 20

// CameraLister énumère les caméras de Frigate avec leurs zones et leurs objets suivis.
type CameraLister interface {
	CameraDetails(ctx context.Context) ([]frigate.CameraInfo, error)
}

type Handler struct {
	cfg     *config.Config
	path    string // fichier de surcharge
	cameras CameraLister
	log     *slog.Logger

	// save sérialise les enregistrements : deux onglets ouverts en même temps ne
	// doivent pas entrelacer validation, écriture et application.
	save sync.Mutex
}

func New(cfg *config.Config, overlayPath string, cameras CameraLister, log *slog.Logger) *Handler {
	return &Handler{cfg: cfg, path: overlayPath, cameras: cameras, log: log}
}

// Mount enregistre l'interface et son API sur mux, protégées par mot de passe si la
// configuration en définit un. /healthz et /metrics restent en dehors : la sonde du
// conteneur et le scrape Prometheus ne s'authentifient pas.
func (h *Handler) Mount(mux *http.ServeMux) {
	mux.Handle("GET /{$}", h.auth(http.HandlerFunc(h.page)))
	mux.Handle("GET /api/settings", h.auth(http.HandlerFunc(h.get)))
	mux.Handle("PUT /api/settings", h.auth(sameOrigin(http.HandlerFunc(h.put))))
	mux.Handle("POST /api/settings/reset", h.auth(sameOrigin(http.HandlerFunc(h.reset))))
}

// requestedWith est l'en-tête que la page joint à ses écritures. Il ne vaut rien comme
// secret : il sert à rendre la requête « non simple » au sens CORS, pour qu'un site
// tiers ouvert dans le même navigateur ne puisse pas la déclencher à notre insu — le
// contrôle préalable qu'elle impose échouera, faute d'en-têtes CORS de notre part.
const requestedWith = "frigate-telegram"

func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Requested-With") != requestedWith {
			http.Error(w, "requête refusée : en-tête X-Requested-With manquant", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) auth(next http.Handler) http.Handler {
	pass := h.cfg.Web.Password
	if pass == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, got, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(pass)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="frigate-telegram", charset="UTF-8"`)
			http.Error(w, "authentification requise", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) page(w http.ResponseWriter, r *http.Request) {
	body, err := assets.ReadFile("ui.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(body)
}

// settings est la vue que l'interface charge au démarrage.
type settings struct {
	Mode     string               `json:"mode"`
	Timezone string               `json:"timezone"`
	Chats    []string             `json:"chats"`
	Cameras  []frigate.CameraInfo `json:"cameras"`
	Overlay  config.Overlay       `json:"overlay"`
	Custom   bool                 `json:"custom"`  // une surcharge est enregistrée
	Warning  string               `json:"warning"` // Frigate injoignable, etc.
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	s := settings{
		Mode:     h.cfg.Mode,
		Timezone: h.cfg.Timezone,
		Chats:    h.cfg.ChatNames(),
		Overlay:  h.cfg.CurrentOverlay(),
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
		s.Warning = "Frigate est injoignable : la liste des caméras, zones et objets est incomplète."
		for _, name := range h.cfg.CameraNames() {
			cams = append(cams, frigate.CameraInfo{Name: name})
		}
	}
	s.Cameras = cams
	writeJSON(w, http.StatusOK, s)
}

func (h *Handler) put(w http.ResponseWriter, r *http.Request) {
	var o config.Overlay
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&o); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	h.save.Lock()
	defer h.save.Unlock()
	// Valider avant d'écrire : un fichier de surcharge invalide serait rejeté au
	// prochain démarrage, et le service repartirait sur config.yml sans prévenir.
	if err := h.cfg.ValidateOverlay(&o); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := config.SaveOverlay(h.path, o); err != nil {
		h.log.Error("enregistrement des réglages échoué", "err", err)
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := h.cfg.ApplyOverlay(&o); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	h.log.Info("réglages de notification mis à jour depuis l'interface web")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) reset(w http.ResponseWriter, r *http.Request) {
	h.save.Lock()
	defer h.save.Unlock()
	if err := os.Remove(h.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := h.cfg.ApplyOverlay(nil); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	h.log.Info("réglages de notification revenus à config.yml")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
