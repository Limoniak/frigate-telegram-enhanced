package frigate

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"frigate-telegram-enhanced/internal/config"
	"frigate-telegram-enhanced/internal/i18n"
)

// ErrTooLarge signale un média plus gros que la limite demandée.
var ErrTooLarge = errors.New("media too large")

// ErrAuth signale des identifiants Frigate refusés.
var ErrAuth = errors.New("credentials refused by Frigate")

// HTTPError est une réponse non-200 de Frigate.
type HTTPError struct {
	Status int
	Path   string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("frigate %s: HTTP %d", e.Path, e.Status) }

// ErrIncomplete signale un téléchargement que Frigate a cessé d'alimenter avant la
// fin : il envoie parfois presque tout un clip puis garde la connexion ouverte.
var ErrIncomplete = i18n.NewError("Frigate stopped sending the clip before the end")

// stallTimeout est le silence au-delà duquel un téléchargement est tenu pour calé.
const stallTimeout = 15 * time.Second

// Retryable indique si réessayer a une chance d'aboutir (média pas encore prêt, serveur ou réseau en difficulté).
// Un clip qui cale n'est pas retenté : Frigate cale au même endroit à chaque fois.
func Retryable(err error) bool {
	if errors.Is(err, ErrTooLarge) || errors.Is(err, ErrIncomplete) || errors.Is(err, context.Canceled) {
		return false
	}
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status == http.StatusNotFound || he.Status >= 500
	}
	return true
}

// Client appelle l'API HTTP de Frigate, avec authentification optionnelle.
type Client struct {
	base       string
	user, pass string
	http       *http.Client
	loginMu    sync.Mutex
	loggedIn   time.Time // dernière connexion réussie (sous loginMu)

	stallTimeout time.Duration
}

func NewClient(cfg config.Frigate) (*Client, error) {
	if _, err := url.Parse(cfg.URL); err != nil {
		return nil, fmt.Errorf("invalid frigate.url: %w", err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = 8
	if cfg.InsecureSkipVerify {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // option explicite de l'utilisateur
	}
	return &Client{
		base: cfg.URL,
		user: cfg.Username,
		pass: cfg.Password,
		http: &http.Client{Transport: tr, Jar: jar},

		stallTimeout: stallTimeout,
	}, nil
}

func (c *Client) get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	return c.http.Do(req)
}

// do exécute un GET ; sur 401 avec identifiants, se reconnecte puis réessaie une fois.
func (c *Client) do(ctx context.Context, path string) (*http.Response, error) {
	sent := time.Now()
	resp, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized && c.user != "" {
		resp.Body.Close()
		if err := c.login(ctx, sent); err != nil {
			return nil, err
		}
		if resp, err = c.get(ctx, path); err != nil {
			return nil, err
		}
	}
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, &HTTPError{Status: resp.StatusCode, Path: path}
	}
	return resp, nil
}

// login ouvre une session. Une connexion réussie après sent (l'envoi de la requête
// refusée) suffit : plusieurs requêtes refusées en même temps n'en ouvrent qu'une.
func (c *Client) login(ctx context.Context, sent time.Time) error {
	c.loginMu.Lock()
	defer c.loginMu.Unlock()
	if c.loggedIn.After(sent) {
		return nil
	}
	body, _ := json.Marshal(map[string]string{"user": c.user, "password": c.pass})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/login", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("login frigate: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: HTTP %d", ErrAuth, resp.StatusCode)
	}
	c.loggedIn = time.Now()
	return nil
}

// GetBytes télécharge une ressource en mémoire (images), en refusant au-delà de max octets.
func (c *Client) GetBytes(ctx context.Context, path string, max int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resp, err := c.do(ctx, path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, ErrTooLarge
	}
	return b, nil
}

// DownloadToFile écrit une ressource dans un fichier temporaire (clips) et renvoie son chemin.
// L'appelant doit supprimer le fichier. Si Frigate cesse d'envoyer avant la fin, elle
// renvoie ErrIncomplete, avec le chemin de ce qui a été reçu s'il y en a.
func (c *Client) DownloadToFile(ctx context.Context, path string, max int64) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	ctx, stall := context.WithCancelCause(ctx)
	defer stall(nil)
	resp, err := c.do(ctx, path)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.ContentLength > max {
		return "", ErrTooLarge
	}
	f, err := os.CreateTemp("", "frigate-*")
	if err != nil {
		return "", err
	}
	idle := time.AfterFunc(c.stallTimeout, func() { stall(ErrIncomplete) })
	defer idle.Stop()
	body := &idleReader{r: resp.Body, idle: idle, timeout: c.stallTimeout}
	n, err := io.Copy(f, io.LimitReader(body, max+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > max {
		err = ErrTooLarge
	}
	if err != nil && errors.Is(context.Cause(ctx), ErrIncomplete) {
		err = ErrIncomplete
		if n > 0 {
			return f.Name(), err
		}
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// idleReader relance le minuteur idle à chaque lecture qui apporte des données.
type idleReader struct {
	r       io.Reader
	idle    *time.Timer
	timeout time.Duration
}

func (r *idleReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.idle.Reset(r.timeout)
	}
	return n, err
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resp, err := c.do(ctx, path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(out)
}

// CameraInfo décrit une caméra telle que Frigate la déclare : ses zones et les
// objets qu'elle suit, dont l'interface web se sert pour proposer des listes de
// choix plutôt que de la saisie libre.
type CameraInfo struct {
	Name   string   `json:"name"`
	Zones  []string `json:"zones"`
	Labels []string `json:"labels"`
}

// apiConfig est la partie de /api/config que l'on exploite.
type apiConfig struct {
	Cameras map[string]struct {
		Zones   map[string]json.RawMessage `json:"zones"`
		Objects struct {
			Track []string `json:"track"`
		} `json:"objects"`
	} `json:"cameras"`
	Objects struct {
		Track []string `json:"track"`
	} `json:"objects"`
}

// Cameras renvoie les noms des caméras déclarées dans Frigate, triés.
func (c *Client) Cameras(ctx context.Context) ([]string, error) {
	var cfg apiConfig
	if err := c.getJSON(ctx, "/api/config", &cfg); err != nil {
		return nil, err
	}
	return slices.Sorted(maps.Keys(cfg.Cameras)), nil
}

// CameraDetails renvoie, par caméra et triés, les zones et les objets suivis.
// Une caméra qui ne redéfinit pas objects.track hérite de la liste globale.
func (c *Client) CameraDetails(ctx context.Context) ([]CameraInfo, error) {
	var cfg apiConfig
	if err := c.getJSON(ctx, "/api/config", &cfg); err != nil {
		return nil, err
	}
	out := make([]CameraInfo, 0, len(cfg.Cameras))
	for _, name := range slices.Sorted(maps.Keys(cfg.Cameras)) {
		cam := cfg.Cameras[name]
		labels := cam.Objects.Track
		if len(labels) == 0 {
			labels = cfg.Objects.Track
		}
		out = append(out, CameraInfo{
			Name:   name,
			Zones:  slices.Sorted(maps.Keys(cam.Zones)),
			Labels: slices.Sorted(slices.Values(labels)),
		})
	}
	return out, nil
}

// Events renvoie les derniers événements, éventuellement d'une seule caméra.
func (c *Client) Events(ctx context.Context, camera string, limit int) ([]APIEvent, error) {
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if camera != "" {
		q.Set("cameras", camera) // Frigate ≥ 0.14
		q.Set("camera", camera)  // versions antérieures
	}
	var evs []APIEvent
	err := c.getJSON(ctx, "/api/events?"+q.Encode(), &evs)
	return evs, err
}

// Event renvoie un événement par son id.
func (c *Client) Event(ctx context.Context, id string) (APIEvent, error) {
	var ev APIEvent
	err := c.getJSON(ctx, "/api/events/"+url.PathEscape(id), &ev)
	return ev, err
}

// EventsSince renvoie les événements commencés après after, du plus récent au plus ancien.
func (c *Client) EventsSince(ctx context.Context, after time.Time, limit int) ([]APIEvent, error) {
	q := url.Values{"after": {unixParam(after)}, "limit": {strconv.Itoa(limit)}, "include_thumbnails": {"0"}}
	var evs []APIEvent
	err := c.getJSON(ctx, "/api/events?"+q.Encode(), &evs)
	return evs, err
}

// ReviewsSince renvoie les éléments de revue commencés après after (Frigate ≥ 0.14).
func (c *Client) ReviewsSince(ctx context.Context, after time.Time, limit int) ([]Review, error) {
	q := url.Values{"after": {unixParam(after)}, "limit": {strconv.Itoa(limit)}}
	var rs []Review
	err := c.getJSON(ctx, "/api/review?"+q.Encode(), &rs)
	return rs, err
}

// unixParam formate une date comme les horodatages de Frigate (secondes).
func unixParam(t time.Time) string {
	return strconv.FormatFloat(float64(t.UnixMilli())/1000, 'f', 3, 64)
}

// Review renvoie un élément de revue par son id.
func (c *Client) Review(ctx context.Context, id string) (Review, error) {
	var r Review
	err := c.getJSON(ctx, "/api/review/"+url.PathEscape(id), &r)
	return r, err
}

// Version renvoie la version de Frigate (GET /api/version) ; sert à vérifier la
// connexion et les identifiants.
func (c *Client) Version(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := c.do(ctx, "/api/version")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 128))
	if err != nil {
		return "", err
	}
	return strings.Trim(strings.TrimSpace(string(b)), `"`), nil
}

// SubLabels renvoie les étiquettes connues de Frigate (classification, visages,
// plaques nommées) : « clio 3 océane », « ohana »…
func (c *Client) SubLabels(ctx context.Context) ([]string, error) {
	var out []string
	err := c.getJSON(ctx, "/api/sub_labels?split_joined=1", &out)
	return out, err
}
