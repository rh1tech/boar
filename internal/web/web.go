// SPDX-License-Identifier: GPL-3.0-or-later

// Package web is the sysop web interface: the sysop menu, in a browser.
//
// It is meant to listen on loopback behind a TLS-terminating proxy (nginx on
// the production host). There is no registration and no account for anyone
// but sysops: the login accepts only unlocked sysop accounts, and every
// request reloads the account, so a demotion or a lock ends a web session on
// the very next click.
//
// Every page is server-rendered HTML with no JavaScript at all, so the
// Content-Security-Policy can forbid scripts outright. State changes are POST
// forms carrying a per-session token, on top of SameSite=Strict cookies and an
// Origin check.
package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"boar/internal/bbs"
	"boar/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

const (
	cookieName = "__Host-boar-sysop"

	// A session ends after half an hour without a request, and after twelve
	// hours whatever happens. Sessions live in memory, so a restart signs
	// everyone out too.
	idleTimeout     = 30 * time.Minute
	absoluteTimeout = 12 * time.Hour
	maxSessions     = 64

	maxFormBytes = 64 << 10
)

// Handler serves the sysop web interface.
type Handler struct {
	bbs      *bbs.Server
	log      *slog.Logger
	pages    map[string]*template.Template
	mux      *http.ServeMux
	sessions *sessionTable
	now      func() time.Time
}

// New builds the handler. srv is the running BBS, so kicking and
// broadcasting reach the callers who are online right now.
func New(srv *bbs.Server, log *slog.Logger) (*Handler, error) {
	h := &Handler{bbs: srv, log: log, sessions: newSessionTable(), now: time.Now}
	pages, err := parsePages()
	if err != nil {
		return nil, err
	}
	h.pages = pages
	h.mux = h.routes()
	return h, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hdr := w.Header()
	hdr.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; font-src 'self'; img-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("X-Frame-Options", "DENY")
	hdr.Set("Referrer-Policy", "same-origin")
	hdr.Set("Cache-Control", "no-store")
	hdr.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	h.mux.ServeHTTP(w, r)
}

// ── sessions ────────────────────────────────────────────────────────────────

type session struct {
	userID  int64
	csrf    string
	flash   string
	created time.Time
	seen    time.Time
}

type sessionTable struct {
	mu sync.Mutex
	m  map[string]*session // keyed by the SHA-256 of the cookie value
}

func newSessionTable() *sessionTable { return &sessionTable{m: map[string]*session{}} }

func tokenKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (t *sessionTable) create(userID int64, now time.Time) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sweep(now)
	if len(t.m) >= maxSessions {
		// Drop the stalest rather than refusing a sysop who just typed the
		// right password.
		var oldest string
		for k, s := range t.m {
			if oldest == "" || s.seen.Before(t.m[oldest].seen) {
				oldest = k
			}
		}
		delete(t.m, oldest)
	}
	token := randomToken()
	t.m[tokenKey(token)] = &session{userID: userID, csrf: randomToken(), created: now, seen: now}
	return token
}

func (t *sessionTable) get(token string, now time.Time) (*session, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := tokenKey(token)
	s, ok := t.m[key]
	if !ok {
		return nil, false
	}
	if now.Sub(s.seen) > idleTimeout || now.Sub(s.created) > absoluteTimeout {
		delete(t.m, key)
		return nil, false
	}
	s.seen = now
	return s, true
}

func (t *sessionTable) drop(token string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.m, tokenKey(token))
}

// dropUser signs an account out everywhere, for when it stops being a sysop.
func (t *sessionTable) dropUser(userID int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for k, s := range t.m {
		if s.userID == userID {
			delete(t.m, k)
		}
	}
}

func (t *sessionTable) sweep(now time.Time) {
	for k, s := range t.m {
		if now.Sub(s.seen) > idleTimeout || now.Sub(s.created) > absoluteTimeout {
			delete(t.m, k)
		}
	}
}

func (t *sessionTable) setFlash(s *session, msg string) {
	t.mu.Lock()
	s.flash = msg
	t.mu.Unlock()
}

func (t *sessionTable) takeFlash(s *session) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	msg := s.flash
	s.flash = ""
	return msg
}

// ── request plumbing ────────────────────────────────────────────────────────

// clientIP is the caller's address. X-Real-IP is only believed from loopback,
// which is where the proxy connects from.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if real := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(real) != nil {
			return real
		}
	}
	return host
}

// sameOrigin rejects a cross-site POST that got past SameSite: when the
// browser says where the form came from, it has to be this host.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || origin == "null" {
		ref := r.Header.Get("Referer")
		if ref == "" {
			return origin == "" // no header at all: old client, the token still guards it
		}
		origin = ref
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

// request is what a signed-in handler gets.
type request struct {
	w    http.ResponseWriter
	r    *http.Request
	sess *session
	user store.User
	ip   string
}

// signedIn wraps a handler that needs a sysop. POSTs also need the session's
// form token and a same-origin request.
func (h *Handler) signedIn(fn func(*request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		sess, ok := h.sessions.get(c.Value, h.now())
		if !ok {
			clearCookie(w)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		u, err := h.bbs.Sysop(sess.userID)
		if err != nil {
			h.sessions.dropUser(sess.userID)
			clearCookie(w)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if r.Method == http.MethodPost {
			r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
			if err := r.ParseForm(); err != nil {
				http.Error(w, "Bad form.", http.StatusBadRequest)
				return
			}
			token := r.PostFormValue("csrf")
			if !sameOrigin(r) || subtle.ConstantTimeCompare([]byte(token), []byte(sess.csrf)) != 1 {
				http.Error(w, "This form has expired. Go back, reload the page and try again.", http.StatusForbidden)
				return
			}
		}
		fn(&request{w: w, r: r, sess: sess, user: u, ip: clientIP(r)})
	}
}

func setCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: token, Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
		MaxAge: int(absoluteTimeout / time.Second),
	})
}

func clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
}

// done finishes a POST: a message for the next page, and back to where the
// sysop came from (a path on this site only).
func (h *Handler) done(q *request, to string, msg string) {
	if msg != "" {
		h.sessions.setFlash(q.sess, msg)
	}
	if !strings.HasPrefix(to, "/") || strings.HasPrefix(to, "//") {
		to = "/"
	}
	http.Redirect(q.w, q.r, to, http.StatusSeeOther)
}

// failed turns an error into a message the sysop can act on. Validation and
// permission errors are shown as they are; anything else is logged and
// reported vaguely, so internals never reach the page.
func (h *Handler) failed(q *request, to string, err error) {
	var input *store.InputError
	switch {
	case errors.As(err, &input):
		h.done(q, to, "! "+input.Msg)
	case errors.Is(err, store.ErrForbidden):
		h.done(q, to, "! That is not allowed (you cannot do it to your own account).")
	case errors.Is(err, store.ErrNotFound):
		h.done(q, to, "! It no longer exists.")
	default:
		h.log.Error("web sysop action failed", "path", q.r.URL.Path, "sysop", q.user.Handle, "err", err)
		h.done(q, to, "! Something went wrong. The details are in the server log.")
	}
}

// ── templates ───────────────────────────────────────────────────────────────

func parsePages() (map[string]*template.Template, error) {
	funcs := template.FuncMap{
		"when":    func(t time.Time) string { return formatTime(t) },
		"ago":     ago,
		"plural":  plural,
		"hotkey":  hotkey,
		"initial": func(s string) string { return strings.ToUpper(s[:1]) },
	}
	names, err := fs.Glob(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	pages := map[string]*template.Template{}
	for _, name := range names {
		base := strings.TrimSuffix(strings.TrimPrefix(name, "templates/"), ".html")
		if base == "layout" {
			continue
		}
		t, err := template.New("layout.html").Funcs(funcs).ParseFS(templateFS, "templates/layout.html", name)
		if err != nil {
			return nil, err
		}
		pages[base] = t
	}
	return pages, nil
}

// page is what every template gets; Data is the page's own content.
type page struct {
	Title   string
	Nav     string
	BBS     string
	User    store.User
	CSRF    string
	Flash   string
	Error   bool
	Online  int
	Waiting int
	Data    any
}

// render shows a page. q is nil only for the login page, before anyone is
// signed in.
func (h *Handler) render(w http.ResponseWriter, q *request, name, title, nav string, data any) {
	p := page{Title: title, Nav: nav, BBS: h.bbs.Name(), Data: data}
	if q != nil {
		p.User, p.CSRF = q.user, q.sess.csrf
		p.Online = len(h.bbs.Online())
		p.Flash = h.sessions.takeFlash(q.sess)
		if strings.HasPrefix(p.Flash, "! ") {
			p.Flash, p.Error = strings.TrimPrefix(p.Flash, "! "), true
		}
		if pending, err := h.bbs.Store().PendingUsers(); err == nil {
			p.Waiting = len(pending)
		}
	}
	h.write(w, name, p)
}

func (h *Handler) write(w http.ResponseWriter, name string, p page) {
	t, ok := h.pages[name]
	if !ok {
		http.Error(w, "No such page.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout.html", p); err != nil {
		h.log.Error("render page", "page", name, "err", err)
	}
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.Local().Format("2006-01-02 15:04")
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d/time.Minute), "minute") + " ago"
	case d < 48*time.Hour:
		return plural(int(d/time.Hour), "hour") + " ago"
	default:
		return plural(int(d/(24*time.Hour)), "day") + " ago"
	}
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

// hotkey splits a label around its access key, the way BBS menus light up
// the letter you press: "Users" with key U renders as [U]sers.
func hotkey(label string) template.HTML {
	if label == "" {
		return ""
	}
	first, rest := label[:1], label[1:]
	return template.HTML(`<span class="key">` + template.HTMLEscapeString(first) + `</span>` + template.HTMLEscapeString(rest))
}
