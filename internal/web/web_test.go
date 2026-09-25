// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"boar/internal/bbs"
	"boar/internal/store"
)

const testPassword = "correct horse"

type fixture struct {
	t     *testing.T
	h     *Handler
	st    *store.Store
	sysop store.User
	user  store.User
	art   string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(store.Config{Path: filepath.Join(dir, "boar.db"), KDFIterations: 1000, ApproveNewUsers: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	art := filepath.Join(dir, "art")
	if err := os.Mkdir(art, 0o700); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := bbs.New(bbs.Config{Name: "Test BBS", ArtDir: art}, st, log)
	h, err := New(srv, log)
	if err != nil {
		t.Fatal(err)
	}
	sysop, err := st.CreateUser("boss", testPassword, "Here") // the first account is the sysop
	if err != nil {
		t.Fatal(err)
	}
	user, err := st.CreateUser("caller", testPassword, "There")
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, h: h, st: st, sysop: sysop, user: user, art: art}
}

// do sends one request straight into the handler, the way nginx would hand
// it over from loopback.
func (f *fixture) do(method, path string, form url.Values, cookie string, header map[string]string) *httptest.ResponseRecorder {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r := httptest.NewRequest(method, "https://boar.test"+path, body)
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("X-Real-IP", "203.0.113.7")
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", "https://boar.test")
	}
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: cookieName, Value: cookie})
	}
	for k, v := range header {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w
}

func sessionCookie(w *httptest.ResponseRecorder) string {
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieName && c.MaxAge >= 0 {
			return c.Value
		}
	}
	return ""
}

func (f *fixture) login(handle string) string {
	f.t.Helper()
	w := f.do("POST", "/login", url.Values{"handle": {handle}, "password": {testPassword}}, "", nil)
	if w.Code != http.StatusSeeOther {
		f.t.Fatalf("login as %s: status %d, body %s", handle, w.Code, w.Body)
	}
	c := sessionCookie(w)
	if c == "" {
		f.t.Fatal("login set no cookie")
	}
	return c
}

var csrfRE = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

func (f *fixture) csrf(cookie string) string {
	f.t.Helper()
	w := f.do("GET", "/", nil, cookie, nil)
	m := csrfRE.FindStringSubmatch(w.Body.String())
	if m == nil {
		f.t.Fatalf("no csrf token on the dashboard: %d %s", w.Code, w.Body)
	}
	return m[1]
}

func TestSignedOutRequestsGoToLogin(t *testing.T) {
	f := newFixture(t)
	for _, path := range []string{"/", "/users", "/events", "/art"} {
		w := f.do("GET", path, nil, "", nil)
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
			t.Errorf("GET %s signed out: %d → %q", path, w.Code, w.Header().Get("Location"))
		}
	}
}

func TestLoginPageHasNoSignup(t *testing.T) {
	f := newFixture(t)
	body := f.do("GET", "/login", nil, "", nil).Body.String()
	if strings.Contains(strings.ToLower(body), "register") || strings.Contains(body, `href="/signup"`) {
		t.Error("the login page offers registration")
	}
}

func TestSysopLogsIn(t *testing.T) {
	f := newFixture(t)
	cookie := f.login("boss")
	w := f.do("GET", "/", nil, cookie, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "boss") {
		t.Fatalf("dashboard: %d", w.Code)
	}
	events, _ := f.st.Events(store.EventLogin, 5)
	if len(events) == 0 || events[0].Detail != "web" || events[0].IP != "203.0.113.7" {
		t.Errorf("login not logged with the proxied address: %+v", events)
	}
}

func TestOnlySysopsGetIn(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct{ handle, password string }{
		{"caller", testPassword}, // right password, not a sysop
		{"boss", "wrong password"},
		{"nobody", testPassword},
	} {
		w := f.do("POST", "/login", url.Values{"handle": {tc.handle}, "password": {tc.password}}, "", nil)
		if w.Code != http.StatusUnauthorized || sessionCookie(w) != "" {
			t.Errorf("%s/%s: status %d, cookie %q", tc.handle, tc.password, w.Code, sessionCookie(w))
		}
		if !strings.Contains(w.Body.String(), "Login failed. Only sysops can sign in here.") {
			t.Errorf("%s: a different message would tell a guesser something", tc.handle)
		}
	}
}

func TestLockedSysopCannotLogIn(t *testing.T) {
	f := newFixture(t)
	other, _ := f.st.CreateUser("second", testPassword, "")
	if err := f.st.SetSysop(f.sysop.ID, other.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetLocked(f.sysop.ID, other.ID, true); err != nil {
		t.Fatal(err)
	}
	w := f.do("POST", "/login", url.Values{"handle": {"second"}, "password": {testPassword}}, "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("locked sysop got in: %d", w.Code)
	}
}

func TestRepeatedFailuresAreThrottled(t *testing.T) {
	f := newFixture(t)
	// Different handles each time: the per-handle backoff would otherwise
	// answer first, and this is about the per-address limit.
	for i := range 5 {
		f.do("POST", "/login", url.Values{"handle": {"guess" + strconv.Itoa(i)}, "password": {"x"}}, "", nil)
	}
	w := f.do("POST", "/login", url.Values{"handle": {"boss"}, "password": {testPassword}}, "", nil)
	if w.Code == http.StatusSeeOther || !strings.Contains(w.Body.String(), "Too many failed logins") {
		t.Errorf("sixth try from the same address was not throttled: %d", w.Code)
	}
}

func TestPostsNeedTheFormToken(t *testing.T) {
	f := newFixture(t)
	cookie := f.login("boss")
	token := f.csrf(cookie)
	path := "/users/" + itoa64(f.user.ID) + "/approve"

	if w := f.do("POST", path, url.Values{}, cookie, nil); w.Code != http.StatusForbidden {
		t.Errorf("no token: %d", w.Code)
	}
	if w := f.do("POST", path, url.Values{"csrf": {"forged"}}, cookie, nil); w.Code != http.StatusForbidden {
		t.Errorf("wrong token: %d", w.Code)
	}
	cross := map[string]string{"Origin": "https://evil.example"}
	if w := f.do("POST", path, url.Values{"csrf": {token}}, cookie, cross); w.Code != http.StatusForbidden {
		t.Errorf("cross-site: %d", w.Code)
	}
	u, _ := f.st.UserByID(f.user.ID)
	if u.Validated {
		t.Fatal("a rejected request changed the account")
	}
	if w := f.do("POST", path, url.Values{"csrf": {token}}, cookie, nil); w.Code != http.StatusSeeOther {
		t.Errorf("good request: %d", w.Code)
	}
	if u, _ := f.st.UserByID(f.user.ID); !u.Validated {
		t.Error("approve did nothing")
	}
	events, _ := f.st.Events(store.EventSysop, 1)
	if len(events) == 0 || events[0].Detail != "approved caller (web)" {
		t.Errorf("approval not audited: %+v", events)
	}
}

func TestDemotionEndsTheSession(t *testing.T) {
	f := newFixture(t)
	other, _ := f.st.CreateUser("second", testPassword, "")
	if err := f.st.SetSysop(f.sysop.ID, other.ID, true); err != nil {
		t.Fatal(err)
	}
	cookie := f.login("second")
	if w := f.do("GET", "/", nil, cookie, nil); w.Code != http.StatusOK {
		t.Fatalf("dashboard: %d", w.Code)
	}
	if err := f.st.SetSysop(f.sysop.ID, other.ID, false); err != nil {
		t.Fatal(err)
	}
	if w := f.do("GET", "/", nil, cookie, nil); w.Code != http.StatusSeeOther {
		t.Errorf("demoted sysop still gets pages: %d", w.Code)
	}
	if err := f.st.SetSysop(f.sysop.ID, other.ID, true); err != nil {
		t.Fatal(err)
	}
	if w := f.do("GET", "/", nil, cookie, nil); w.Code != http.StatusSeeOther {
		t.Error("the old session came back after re-promotion")
	}
}

func TestDeleteNeedsTheExactHandle(t *testing.T) {
	f := newFixture(t)
	cookie := f.login("boss")
	token := f.csrf(cookie)
	path := "/users/" + itoa64(f.user.ID) + "/delete"
	f.do("POST", path, url.Values{"csrf": {token}, "confirm": {"Caller"}}, cookie, nil)
	if _, err := f.st.UserByID(f.user.ID); err != nil {
		t.Fatal("deleted with the wrong confirmation")
	}
	f.do("POST", path, url.Values{"csrf": {token}, "confirm": {"caller"}}, cookie, nil)
	if _, err := f.st.UserByID(f.user.ID); err == nil {
		t.Error("not deleted with the right confirmation")
	}
}

func TestCannotLockYourself(t *testing.T) {
	f := newFixture(t)
	cookie := f.login("boss")
	token := f.csrf(cookie)
	f.do("POST", "/users/"+itoa64(f.sysop.ID)+"/lock", url.Values{"csrf": {token}}, cookie, nil)
	if u, _ := f.st.UserByID(f.sysop.ID); u.Locked {
		t.Error("a sysop locked themselves out")
	}
}

func TestSecurityHeaders(t *testing.T) {
	f := newFixture(t)
	w := f.do("GET", "/login", nil, "", nil)
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") || strings.Contains(csp, "script-src") {
		t.Errorf("CSP should allow no scripts at all: %q", csp)
	}
	if w.Header().Get("X-Frame-Options") != "DENY" {
		t.Error("missing X-Frame-Options")
	}
	c := f.do("POST", "/login", url.Values{"handle": {"boss"}, "password": {testPassword}}, "", nil).Result().Cookies()[0]
	if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Errorf("session cookie flags: %+v", c)
	}
}

func TestArtPreviewOnlyServesArtFiles(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(filepath.Join(f.art, "welcome.ans"), []byte("\x1b[1;33mHi\x1b[0m <b>"), 0o600); err != nil {
		t.Fatal(err)
	}
	cookie := f.login("boss")
	w := f.do("GET", "/art/welcome.ans", nil, cookie, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `<span class="f11 b0">Hi</span>`) {
		t.Errorf("preview: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "<b>") && !strings.Contains(w.Body.String(), "&lt;b&gt;") {
		t.Error("art text was not escaped")
	}
	for _, name := range []string{"..%2F..%2Fetc%2Fpasswd", "boar.db", "welcome.ans.bak"} {
		if w := f.do("GET", "/art/"+name, nil, cookie, nil); w.Code != http.StatusNotFound {
			t.Errorf("GET /art/%s: %d", name, w.Code)
		}
	}
}

func TestRenderANSI(t *testing.T) {
	got := string(renderANS([]byte("A\x1b[3CB\r\n\x1b[31;44mC")))
	want := "<span class=\"f7 b0\">A   B</span>\n<span class=\"f1 b4\">C</span>\n"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
	// Pipe codes count in DOS order, where 14 is yellow; that is ANSI 3, bright.
	pipe := string(renderPipeText("|14Hey"))
	if !strings.Contains(pipe, `<span class="f11 b0">Hey</span>`) {
		t.Errorf("pipe codes: %q", pipe)
	}
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
