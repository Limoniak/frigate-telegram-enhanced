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

// ErrTooLarge reports a media larger than the requested limit.
var ErrTooLarge = errors.New("media too large")

// ErrAuth reports Frigate credentials that were refused.
var ErrAuth = errors.New("credentials refused by Frigate")

// HTTPError is a non-200 response from Frigate.
type HTTPError struct {
	Status int
	Path   string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("frigate %s: HTTP %d", e.Path, e.Status) }

// ErrIncomplete reports a download that Frigate stopped feeding before the end: it
// sometimes sends almost all of a clip, then keeps the connection open.
var ErrIncomplete = i18n.NewError("Frigate stopped sending the clip before the end")

// stallTimeout is the silence after which a download is considered stalled.
const stallTimeout = 15 * time.Second

// Retryable reports whether retrying may succeed (media not ready yet, server or network in trouble).
// A stalled clip is not retried: Frigate stalls at the same place every time.
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

// Client calls Frigate's HTTP API, with optional authentication.
type Client struct {
	base       string
	user, pass string
	http       *http.Client
	loginMu    sync.Mutex
	loggedIn   time.Time // last successful login (under loginMu)

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

// do runs a GET; on a 401 with credentials, logs in again then retries once.
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

// login opens a session. A login that succeeded after sent (when the refused request
// was sent) is enough: several requests refused at once only open one session.
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

// GetBytes downloads a resource into memory (images), refusing more than max bytes.
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

// DownloadToFile writes a resource to a temporary file (clips) and returns its path.
// The caller must remove the file. If Frigate stops sending before the end, it
// returns ErrIncomplete, with the path of what was received if anything was.
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

// idleReader restarts the idle timer on every read that brings data.
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

// CameraInfo describes a camera as Frigate declares it: its zones and the objects
// it tracks, which the web interface uses to offer lists of choices rather than
// free input.
type CameraInfo struct {
	Name   string   `json:"name"`
	Zones  []string `json:"zones"`
	Labels []string `json:"labels"`
}

// apiConfig is the part of /api/config the service uses.
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
	MQTT MQTTSettings `json:"mqtt"`
}

// MQTTSettings is the broker Frigate publishes to, as /api/config gives it: the
// setup page offers it rather than asking. Frigate leaves the password out.
type MQTTSettings struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	User        string `json:"user"`
	TopicPrefix string `json:"topic_prefix"`
}

// MQTT returns the broker settings of Frigate's configuration.
func (c *Client) MQTT(ctx context.Context) (MQTTSettings, error) {
	var cfg apiConfig
	err := c.getJSON(ctx, "/api/config", &cfg)
	return cfg.MQTT, err
}

// Cameras returns the names of the cameras declared in Frigate, sorted.
func (c *Client) Cameras(ctx context.Context) ([]string, error) {
	var cfg apiConfig
	if err := c.getJSON(ctx, "/api/config", &cfg); err != nil {
		return nil, err
	}
	return slices.Sorted(maps.Keys(cfg.Cameras)), nil
}

// CameraDetails returns, per camera and sorted, the zones and the tracked objects.
// A camera that does not redefine objects.track inherits the global list.
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

// Events returns the latest events, optionally of a single camera.
func (c *Client) Events(ctx context.Context, camera string, limit int) ([]APIEvent, error) {
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if camera != "" {
		q.Set("cameras", camera) // Frigate ≥ 0.14
		q.Set("camera", camera)  // earlier versions
	}
	var evs []APIEvent
	err := c.getJSON(ctx, "/api/events?"+q.Encode(), &evs)
	return evs, err
}

// Event returns an event by its id.
func (c *Client) Event(ctx context.Context, id string) (APIEvent, error) {
	var ev APIEvent
	err := c.getJSON(ctx, "/api/events/"+url.PathEscape(id), &ev)
	return ev, err
}

// EventsSince returns the events that started after after, most recent first.
func (c *Client) EventsSince(ctx context.Context, after time.Time, limit int) ([]APIEvent, error) {
	q := url.Values{"after": {unixParam(after)}, "limit": {strconv.Itoa(limit)}, "include_thumbnails": {"0"}}
	var evs []APIEvent
	err := c.getJSON(ctx, "/api/events?"+q.Encode(), &evs)
	return evs, err
}

// ReviewsSince returns the review items that started after after (Frigate ≥ 0.14).
func (c *Client) ReviewsSince(ctx context.Context, after time.Time, limit int) ([]Review, error) {
	q := url.Values{"after": {unixParam(after)}, "limit": {strconv.Itoa(limit)}}
	var rs []Review
	err := c.getJSON(ctx, "/api/review?"+q.Encode(), &rs)
	return rs, err
}

// unixParam formats a date like Frigate's timestamps (seconds).
func unixParam(t time.Time) string {
	return strconv.FormatFloat(float64(t.UnixMilli())/1000, 'f', 3, 64)
}

// Review returns a review item by its id.
func (c *Client) Review(ctx context.Context, id string) (Review, error) {
	var r Review
	err := c.getJSON(ctx, "/api/review/"+url.PathEscape(id), &r)
	return r, err
}

// Version returns Frigate's version (GET /api/version); used to check the
// connection and the credentials.
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

// SubLabels returns the labels Frigate knows (classification, faces, named
// plates): "clio 3 océane", "ohana"…
func (c *Client) SubLabels(ctx context.Context) ([]string, error) {
	var out []string
	err := c.getJSON(ctx, "/api/sub_labels?split_joined=1", &out)
	return out, err
}
