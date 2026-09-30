package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const token = "123:SECRET"

func testClient(url string, opts ...Option) *Client {
	base := []Option{WithBaseURL(url), WithBackoff(time.Millisecond), WithLimiter(NewLimiterWith(0, 0, 0))}
	return New(token, append(base, opts...)...)
}

func okResult(w http.ResponseWriter, result string) {
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, `{"ok":true,"result":`+result+`}`)
}

func TestSendPhotoMultipart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot"+token+"/sendPhoto" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.ContentLength <= 0 {
			t.Error("want a Content-Length (not chunked)")
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		checks := map[string]string{"chat_id": "42", "caption": "<b>hi</b>", "parse_mode": "HTML", "disable_notification": "true"}
		for k, want := range checks {
			if got := r.FormValue(k); got != want {
				t.Errorf("%s = %q, want %q", k, got, want)
			}
		}
		var markup InlineKeyboardMarkup
		if err := json.Unmarshal([]byte(r.FormValue("reply_markup")), &markup); err != nil || markup.InlineKeyboard[0][0].CallbackData != "p:1800" {
			t.Errorf("reply_markup = %q", r.FormValue("reply_markup"))
		}
		if !strings.Contains(r.FormValue("reply_parameters"), `"message_id":5`) {
			t.Errorf("reply_parameters = %q", r.FormValue("reply_parameters"))
		}
		f, h, err := r.FormFile("photo")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(f)
		if h.Filename != "snapshot.jpg" || string(b) != "jpegdata" {
			t.Errorf("file = %s %q", h.Filename, b)
		}
		okResult(w, `{"message_id":7,"chat":{"id":42},"photo":[{"file_id":"small"},{"file_id":"big"}]}`)
	}))
	defer srv.Close()

	m, err := testClient(srv.URL).SendPhoto(context.Background(), 42,
		InputFile{Name: "snapshot.jpg", Data: []byte("jpegdata")},
		SendOptions{Caption: "<b>hi</b>", Silent: true, ReplyTo: 5,
			Markup: &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{{{Text: "x", CallbackData: "p:1800"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if m.MessageID != 7 || m.FileID() != "big" {
		t.Errorf("message = %+v, FileID = %q", m, m.FileID())
	}
}

func TestSendVideoFromPathThenFileID(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if r.FormValue("supports_streaming") != "true" {
			t.Error("supports_streaming missing")
		}
		if n == 1 {
			f, _, err := r.FormFile("video")
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(f)
			if string(b) != "mp4data" {
				t.Errorf("content = %q", b)
			}
		} else if r.FormValue("video") != "vid-1" {
			t.Errorf("file_id = %q", r.FormValue("video"))
		}
		okResult(w, `{"message_id":8,"chat":{"id":1},"video":{"file_id":"vid-1"}}`)
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "clip.mp4")
	os.WriteFile(path, []byte("mp4data"), 0o600)

	c := testClient(srv.URL)
	m, err := c.SendVideo(context.Background(), 1, InputFile{Name: "clip.mp4", Path: path}, SendOptions{})
	if err != nil || m.FileID() != "vid-1" {
		t.Fatalf("upload: %+v, %v", m, err)
	}
	if _, err := c.SendVideo(context.Background(), 2, InputFile{FileID: m.FileID()}, SendOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestRetryOn429(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":1}}`)
			return
		}
		okResult(w, `{"message_id":1,"chat":{"id":1}}`)
	}))
	defer srv.Close()
	start := time.Now()
	if _, err := testClient(srv.URL).SendMessage(context.Background(), 1, "hi", SendOptions{}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || time.Since(start) < time.Second {
		t.Errorf("calls = %d, duration = %v: retry_after not honored", calls.Load(), time.Since(start))
	}
}

func TestRetryOn5xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusBadGateway)
			io.WriteString(w, `<html>bad gateway</html>`)
			return
		}
		okResult(w, `{"message_id":1,"chat":{"id":1}}`)
	}))
	defer srv.Close()
	if _, err := testClient(srv.URL).SendMessage(context.Background(), 1, "hi", SendOptions{}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Errorf("calls = %d, want 3", calls.Load())
	}
}

func TestNoRetryOn400AndHook(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`)
	}))
	defer srv.Close()
	var hookMethod string
	var hookCode int
	c := testClient(srv.URL, WithErrorHook(func(m string, code int) { hookMethod, hookCode = m, code }))
	_, err := c.SendMessage(context.Background(), 1, "hi", SendOptions{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != 400 {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 1 || hookMethod != "sendMessage" || hookCode != 400 {
		t.Errorf("calls=%d hook=%s/%d", calls.Load(), hookMethod, hookCode)
	}
}

func TestErrorsNeverLeakToken(t *testing.T) {
	c := testClient("http://127.0.0.1:1")
	_, err := c.SendMessage(context.Background(), 1, "hi", SendOptions{})
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("err = %v (the token must not appear)", err)
	}
}

func TestGetUpdates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("offset") != "10" || r.FormValue("timeout") != "50" {
			t.Errorf("params = %v", r.Form)
		}
		okResult(w, `[{"update_id":10,"message":{"message_id":1,"from":{"id":5},"chat":{"id":5},"text":"/status"}},{"update_id":11,"callback_query":{"id":"q","from":{"id":5},"data":"p:1800","message":{"message_id":2,"chat":{"id":5}}}}]`)
	}))
	defer srv.Close()
	ups, err := testClient(srv.URL).GetUpdates(context.Background(), 10, 50*time.Second)
	if err != nil || len(ups) != 2 {
		t.Fatalf("ups = %+v, %v", ups, err)
	}
	if ups[0].Message.Text != "/status" || ups[1].CallbackQuery.Data != "p:1800" || ups[1].CallbackQuery.Message.Chat.ID != 5 {
		t.Errorf("wrong decoding: %+v", ups)
	}
}

func TestEditMessageMedia(t *testing.T) {
	var got []map[string]string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/editMessageMedia") {
			t.Errorf("method = %s", r.URL.Path)
		}
		fields := map[string]string{}
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			for k, v := range r.MultipartForm.Value {
				fields[k] = v[0]
			}
			if f := r.MultipartForm.File["file"]; len(f) == 1 {
				fields["file"] = f[0].Filename
			}
		} else {
			r.ParseForm()
			for k := range r.PostForm {
				fields[k] = r.PostForm.Get(k)
			}
		}
		got = append(got, fields)
		w.Write([]byte(`{"ok":true,"result":{"message_id":7,"video":{"file_id":"VID"}}}`))
	}))
	defer ts.Close()
	c := New("123:abc", WithBaseURL(ts.URL), WithBackoff(0))

	m, err := c.EditMessageMedia(context.Background(), 42, 7, "video", InputFile{Name: "clip.mp4", Data: []byte("mp4")}, "<b>Person</b>", nil)
	if err != nil || m.FileID() != "VID" {
		t.Fatalf("upload: %+v, %v", m, err)
	}
	if got[0]["file"] != "clip.mp4" || !strings.Contains(got[0]["media"], `"media":"attach://file"`) ||
		!strings.Contains(got[0]["media"], `"type":"video"`) || got[0]["message_id"] != "7" {
		t.Errorf("fields sent = %v", got[0])
	}
	if _, err := c.EditMessageMedia(context.Background(), 43, 8, "video", InputFile{FileID: "VID"}, "x", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got[1]["media"], `"media":"VID"`) || got[1]["file"] != "" {
		t.Errorf("file_id reuse: %v", got[1])
	}
}

// dropAfterRead reads the request, then closes the connection without answering:
// Telegram got the send, but the response was lost.
func dropAfterRead(calls *atomic.Int32) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.Copy(io.Discard, r.Body)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			conn.Close()
		}
	}
}

func TestSendNotRetriedWhenResponseLost(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(dropAfterRead(&calls))
	defer srv.Close()
	c := testClient(srv.URL)
	if _, err := c.SendPhoto(context.Background(), 1, InputFile{Name: "a.jpg", Data: []byte("x")}, SendOptions{}); err == nil {
		t.Fatal("want an error")
	}
	if _, err := c.SendMessage(context.Background(), 1, "hi", SendOptions{}); err == nil {
		t.Fatal("want an error")
	}
	if n := calls.Load(); n != 2 {
		t.Errorf("%d requests, want 2 (a single try per send: it may have arrived)", n)
	}
}

func TestEditRetriedWhenResponseLost(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(dropAfterRead(&calls))
	defer srv.Close()
	if err := testClient(srv.URL).EditMessageCaption(context.Background(), 1, 2, "c", nil); err == nil {
		t.Fatal("want an error")
	}
	if n := calls.Load(); n != 4 {
		t.Errorf("%d requests, want 4 (an edit can be replayed without a duplicate)", n)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSendRetriedWhenNotDelivered(t *testing.T) {
	var calls atomic.Int32
	c := testClient("http://telegram.invalid")
	c.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("dial tcp: connection refused") // nothing went out
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":1,"chat":{"id":1}}}`))}, nil
	})}
	if _, err := c.SendMessage(context.Background(), 1, "hi", SendOptions{}); err != nil {
		t.Fatal(err)
	}
	if n := calls.Load(); n != 2 {
		t.Errorf("%d requests, want 2", n)
	}
}
