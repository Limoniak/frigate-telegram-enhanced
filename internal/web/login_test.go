package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// noRedirect is a client that shows redirects instead of following them.
func noRedirect(ts *httptest.Server) *http.Client {
	c := *ts.Client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &c
}

func get(t *testing.T, c *http.Client, url string, cookies ...*http.Cookie) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func postLogin(t *testing.T, c *http.Client, base, password, next string) *http.Response {
	t.Helper()
	resp, err := c.PostForm(base+"/login", url.Values{"password": {password}, "next": {next}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func sessionOf(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	return nil
}

func TestPagesLeadToTheLoginPageNotTheBrowserDialog(t *testing.T) {
	_, _, ts := setup(t, "s3cret", fakeCameras{})
	c := noRedirect(ts)

	resp := get(t, c, ts.URL+"/")
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login?next=" {
		t.Errorf("GET / = %d to %q, want the login page", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp.Header.Get("WWW-Authenticate") != "" {
		t.Error("the interface asks for the browser's password dialog")
	}
	// The API answers 401, without the dialog either.
	if resp := do(t, ts, "GET", "/api/settings", ""); resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("WWW-Authenticate") != "" {
		t.Errorf("API: %d, WWW-Authenticate %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}

	resp = get(t, c, ts.URL+"/login?next=setup")
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `type="password"`) || !strings.Contains(string(body), `value="setup"`) {
		t.Errorf("login page: %d\n%s", resp.StatusCode, body)
	}
	// Its style sheet is reachable before the password.
	if resp := get(t, c, ts.URL+"/ui.css"); resp.StatusCode != http.StatusOK {
		t.Errorf("ui.css before login: %d", resp.StatusCode)
	}
}

func TestLoginOpensASession(t *testing.T) {
	_, _, ts := setup(t, "s3cret", fakeCameras{})
	c := noRedirect(ts)

	resp := postLogin(t, c, ts.URL, "wrong", "")
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusUnauthorized || sessionOf(resp) != nil || !strings.Contains(string(body), "Wrong password.") {
		t.Errorf("wrong password: %d, cookie %v", resp.StatusCode, sessionOf(resp))
	}

	resp = postLogin(t, c, ts.URL, "s3cret", "setup")
	session := sessionOf(resp)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/setup" || session == nil || !session.HttpOnly {
		t.Fatalf("right password: %d to %q, cookie %+v", resp.StatusCode, resp.Header.Get("Location"), session)
	}
	if resp := get(t, c, ts.URL+"/", session); resp.StatusCode != http.StatusOK {
		t.Errorf("page with the session: %d", resp.StatusCode)
	}

	// Never back to another site.
	if resp := postLogin(t, c, ts.URL, "s3cret", "//evil.example"); resp.Header.Get("Location") != "/" {
		t.Errorf("next=//evil.example: redirected to %q", resp.Header.Get("Location"))
	}

	// Logging out ends the session in the browser.
	req, _ := http.NewRequest("POST", ts.URL+"/logout", nil)
	req.Header.Set("X-Requested-With", requestedWith)
	out, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	out.Body.Close()
	if ck := sessionOf(out); ck == nil || ck.MaxAge >= 0 {
		t.Errorf("logout cookie = %+v, want it deleted", ck)
	}
}

func TestSessionsSurviveRestartsButNotAPasswordChange(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	a := NewAuth("s3cret", key, slog.New(slog.DiscardHandler))
	w := httptest.NewRecorder()
	a.openSession(w, httptest.NewRequest("POST", "/login", nil))
	cookie := w.Result().Cookies()[0]
	withCookie := func() *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.AddCookie(cookie)
		return r
	}

	if !NewAuth("s3cret", key, nil).hasSession(withCookie()) {
		t.Error("the session is lost on restart")
	}
	if NewAuth("changed", key, nil).hasSession(withCookie()) {
		t.Error("the session survives a password change")
	}
	if NewAuth("s3cret", nil, nil).hasSession(withCookie()) {
		t.Error("a session signed with another key is accepted")
	}
	forged := *cookie
	forged.Value = "99999999999." + strings.SplitN(cookie.Value, ".", 2)[1] // a later end, same signature
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&forged)
	if a.hasSession(r) {
		t.Error("a session with a changed end date is accepted")
	}
}

func TestMetricsStillAskTheHTTPWay(t *testing.T) {
	a := NewAuth("s3cret", nil, nil)
	h := a.WrapBasic(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") == "" {
		t.Errorf("/metrics without credentials: %d, WWW-Authenticate %q", w.Code, w.Header().Get("WWW-Authenticate"))
	}
	r := httptest.NewRequest("GET", "/metrics", nil)
	r.SetBasicAuth("prometheus", "s3cret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("/metrics with Basic credentials: %d", w.Code)
	}
}
