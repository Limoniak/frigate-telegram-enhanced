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
	"sync"
	"time"

	"frigate-telegram-enhanced/internal/config"
)

// ErrTooLarge signale un média plus gros que la limite demandée.
var ErrTooLarge = errors.New("média trop volumineux")

// HTTPError est une réponse non-200 de Frigate.
type HTTPError struct {
	Status int
	Path   string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("frigate %s: HTTP %d", e.Path, e.Status) }

// Retryable indique si réessayer a une chance d'aboutir (média pas encore prêt, serveur ou réseau en difficulté).
func Retryable(err error) bool {
	if errors.Is(err, ErrTooLarge) || errors.Is(err, context.Canceled) {
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
}

func NewClient(cfg config.Frigate) (*Client, error) {
	if _, err := url.Parse(cfg.URL); err != nil {
		return nil, fmt.Errorf("frigate.url invalide: %w", err)
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
	resp, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized && c.user != "" {
		resp.Body.Close()
		if err := c.login(ctx); err != nil {
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

func (c *Client) login(ctx context.Context) error {
	c.loginMu.Lock()
	defer c.loginMu.Unlock()
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
		return fmt.Errorf("login frigate refusé: HTTP %d", resp.StatusCode)
	}
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
// L'appelant doit supprimer le fichier.
func (c *Client) DownloadToFile(ctx context.Context, path string, max int64) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
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
	n, err := io.Copy(f, io.LimitReader(resp.Body, max+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > max {
		err = ErrTooLarge
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
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

// Review renvoie un élément de revue par son id.
func (c *Client) Review(ctx context.Context, id string) (Review, error) {
	var r Review
	err := c.getJSON(ctx, "/api/review/"+url.PathEscape(id), &r)
	return r, err
}
