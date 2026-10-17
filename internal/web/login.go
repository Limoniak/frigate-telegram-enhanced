package web

import (
	"html/template"
	"net/http"
	"regexp"
	"strconv"

	"frigate-telegram-enhanced/internal/i18n"
)

// loginTemplate is the login page: a password field, in the style of the interface.
// No script: a plain form.
var loginTemplate = template.Must(template.ParseFS(assets, "login.html"))

// loginView fills the login page, in the language of the request.
type loginView struct {
	Lang, Title, Label, Button, Error, Next string
	Langs                                   []loginLang
}

type loginLang struct {
	Code    i18n.Lang
	Current bool
}

// nextPage only accepts the interface's own pages ("", "setup"): never another site.
var nextPage = regexp.MustCompile(`^[a-z]*$`)

// loginLang reads ?lang=, set by the page's language links, then the browser's.
func loginLangOf(r *http.Request, def i18n.Lang) i18n.Lang {
	if l, err := i18n.Parse(r.URL.Query().Get("lang")); err == nil && r.URL.Query().Get("lang") != "" {
		return l
	}
	return requestLang(r, def)
}

func (h *Handler) renderLogin(w http.ResponseWriter, r *http.Request, code int, errText string) {
	l := loginLangOf(r, h.cfg.Language)
	next := r.FormValue("next")
	if !nextPage.MatchString(next) {
		next = ""
	}
	v := loginView{Lang: string(l), Title: l.T("Frigate notifications"), Label: l.T("Password"),
		Button: l.T("Log in"), Error: errText, Next: next}
	for _, c := range i18n.Languages() {
		v.Langs = append(v.Langs, loginLang{Code: c, Current: c == l})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if err := loginTemplate.Execute(w, v); err != nil {
		h.log.Error("login page", "err", err)
	}
}

func (h *Handler) loginPage(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Web.Password == "" || h.auth.hasSession(r) {
		http.Redirect(w, r, "./", http.StatusSeeOther)
		return
	}
	h.renderLogin(w, r, http.StatusOK, "")
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		h.renderLogin(w, r, http.StatusBadRequest, "")
		return
	}
	l := loginLangOf(r, h.cfg.Language)
	ip := clientIP(r)
	if wait := h.auth.blocked(ip); wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		h.renderLogin(w, r, http.StatusTooManyRequests, l.T("too many wrong passwords, try again in a few minutes"))
		return
	}
	if h.cfg.Web.Password == "" || !h.auth.check(ip, r.PostFormValue("password")) {
		h.renderLogin(w, r, http.StatusUnauthorized, l.T("Wrong password."))
		return
	}
	h.auth.openSession(w, r)
	next := r.PostFormValue("next")
	if !nextPage.MatchString(next) {
		next = ""
	}
	http.Redirect(w, r, "./"+next, http.StatusSeeOther)
}

func (h *Handler) logout(w http.ResponseWriter, _ *http.Request) {
	closeSession(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
