package frigate

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"frigate-telegram-enhanced/internal/config"
)

func newTestClient(t *testing.T, url, user, pass string) *Client {
	t.Helper()
	c, err := NewClient(config.Frigate{URL: url, Username: user, Password: pass})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPaths(t *testing.T) {
	cases := map[string]string{
		EventSnapshotPath("a1"):                   "/api/events/a1/snapshot.jpg?bbox=1",
		EventClipPath("a1"):                       "/api/events/a1/clip.mp4",
		EventGIFPath("a1"):                        "/api/events/a1/preview.gif",
		ReviewGIFPath("r1"):                       "/api/review/r1/preview?format=gif",
		LatestPath("jardin"):                      "/api/jardin/latest.jpg",
		RecordingClipPath("jardin", 100.4, 130.2): "/api/jardin/start/100/end/131/clip.mp4",
		EventClipPath("a b"):                      "/api/events/a%20b/clip.mp4",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("%q, attendu %q", got, want)
		}
	}
}

func TestGetBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/events/abc/snapshot.jpg" || r.URL.Query().Get("bbox") != "1" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte("jpeg"))
	}))
	defer srv.Close()
	b, err := newTestClient(t, srv.URL, "", "").GetBytes(context.Background(), EventSnapshotPath("abc"), 1024)
	if err != nil || string(b) != "jpeg" {
		t.Fatalf("GetBytes = %q, %v", b, err)
	}
}

func TestGetBytesTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, 100))
	}))
	defer srv.Close()
	_, err := newTestClient(t, srv.URL, "", "").GetBytes(context.Background(), "/x", 10)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, attendu ErrTooLarge", err)
	}
}

func TestHTTPErrorAndRetryable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	_, err := newTestClient(t, srv.URL, "", "").GetBytes(context.Background(), "/x", 10)
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != 404 {
		t.Fatalf("err = %v, attendu HTTPError 404", err)
	}
	cases := []struct {
		err  error
		want bool
	}{
		{&HTTPError{Status: 404}, true},
		{&HTTPError{Status: 503}, true},
		{&HTTPError{Status: 400}, false},
		{ErrTooLarge, false},
		{context.Canceled, false},
		{errors.New("connection refused"), true},
	}
	for _, tc := range cases {
		if got := Retryable(tc.err); got != tc.want {
			t.Errorf("Retryable(%v) = %v, attendu %v", tc.err, got, tc.want)
		}
	}
}

func TestLoginOnUnauthorized(t *testing.T) {
	var logins atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/login" {
			var body struct{ User, Password string }
			json.NewDecoder(r.Body).Decode(&body)
			if r.Method != http.MethodPost || body.User != "admin" || body.Password != "secret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			logins.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "frigate_token", Value: "tok", Path: "/"})
			return
		}
		if c, err := r.Cookie("frigate_token"); err != nil || c.Value != "tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL, "admin", "secret")
	for i := 0; i < 2; i++ {
		b, err := c.GetBytes(context.Background(), "/api/x", 10)
		if err != nil || string(b) != "ok" {
			t.Fatalf("appel %d : %q, %v", i, b, err)
		}
	}
	if logins.Load() != 1 {
		t.Errorf("logins = %d, attendu 1", logins.Load())
	}
}

func TestDownloadToFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("mp4data"))
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL, "", "")
	path, err := c.DownloadToFile(context.Background(), "/clip.mp4", 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	b, _ := os.ReadFile(path)
	if string(b) != "mp4data" {
		t.Errorf("contenu = %q", b)
	}
	if _, err := c.DownloadToFile(context.Background(), "/clip.mp4", 3); !errors.Is(err, ErrTooLarge) {
		t.Errorf("err = %v, attendu ErrTooLarge", err)
	}
}

// Frigate envoie parfois presque tout un clip puis garde la connexion ouverte sans
// jamais finir : le téléchargement s'arrête dès que plus rien n'arrive, et rend ce qui
// a été reçu.
func TestDownloadToFileStalled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/partial.mp4" {
			w.Write([]byte("partial"))
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL, "", "")
	c.stallTimeout = 50 * time.Millisecond

	path, err := c.DownloadToFile(context.Background(), "/partial.mp4", 1024)
	if !errors.Is(err, ErrIncomplete) || path == "" {
		t.Fatalf("DownloadToFile = %q, %v ; attendu le fichier partiel et ErrIncomplete", path, err)
	}
	defer os.Remove(path)
	if b, _ := os.ReadFile(path); string(b) != "partial" {
		t.Errorf("contenu = %q", b)
	}
	if Retryable(err) {
		t.Error("un clip qui cale ne doit pas être retenté : Frigate cale au même endroit")
	}

	if path, err := c.DownloadToFile(context.Background(), "/empty.mp4", 1024); !errors.Is(err, ErrIncomplete) || path != "" {
		t.Errorf("rien reçu : %q, %v ; attendu aucun fichier et ErrIncomplete", path, err)
	}
}

func TestCamerasEventsReview(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/config":
			w.Write([]byte(`{"cameras":{"jardin":{},"garage":{}}}`))
		case "/api/events":
			if r.URL.Query().Get("cameras") != "jardin" || r.URL.Query().Get("limit") != "1" {
				http.Error(w, "bad query", 400)
				return
			}
			w.Write([]byte(`[{"id":"e1","camera":"jardin","label":"person","sub_label":null,"start_time":1.5,"end_time":3.0,"zones":["allee"],"has_clip":true,"has_snapshot":true,"top_score":null,"data":{"top_score":0.91}}]`))
		case "/api/review/r1":
			w.Write([]byte(`{"id":"r1","camera":"jardin","severity":"alert","start_time":10.0,"end_time":20.0,"data":{"detections":["e1"],"objects":["person"],"zones":[]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL, "", "")
	ctx := context.Background()

	cams, err := c.Cameras(ctx)
	if err != nil || len(cams) != 2 || cams[0] != "garage" || cams[1] != "jardin" {
		t.Errorf("Cameras = %v, %v", cams, err)
	}
	evs, err := c.Events(ctx, "jardin", 1)
	if err != nil || len(evs) != 1 || evs[0].ID != "e1" || evs[0].Score() != 0.91 {
		t.Errorf("Events = %+v, %v", evs, err)
	}
	rv, err := c.Review(ctx, "r1")
	if err != nil || rv.Camera != "jardin" || rv.EndTime == nil || *rv.EndTime != 20.0 {
		t.Errorf("Review = %+v, %v", rv, err)
	}
}
