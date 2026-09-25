// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"boar/internal/bbs"
	"boar/internal/store"
)

const (
	eventPageSize  = 200
	dashEvents     = 12
	onelinerLimit  = 100
	userListLimit  = 1000
	maxArtPreviewB = 256 << 10
)

func (h *Handler) routes() *http.ServeMux {
	mux := http.NewServeMux()
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", staticHandler(http.FileServerFS(static))))

	mux.HandleFunc("GET /login", h.loginPage)
	mux.HandleFunc("POST /login", h.loginSubmit)
	mux.HandleFunc("POST /logout", h.signedIn(h.logout))

	mux.HandleFunc("GET /{$}", h.signedIn(h.dashboard))
	mux.HandleFunc("POST /kick", h.signedIn(h.kick))
	mux.HandleFunc("POST /broadcast", h.signedIn(h.broadcast))

	mux.HandleFunc("GET /callers", h.signedIn(h.callers))
	mux.HandleFunc("GET /users", h.signedIn(h.users))
	mux.HandleFunc("GET /users/{id}", h.signedIn(h.user))
	mux.HandleFunc("POST /users/{id}/{action}", h.signedIn(h.userAction))

	mux.HandleFunc("GET /events", h.signedIn(h.events))

	mux.HandleFunc("GET /boards", h.signedIn(h.boards))
	mux.HandleFunc("POST /boards", h.signedIn(h.createBoard))
	mux.HandleFunc("GET /boards/{id}", h.signedIn(h.board))
	mux.HandleFunc("POST /boards/{id}/delete", h.signedIn(h.deleteBoard))
	mux.HandleFunc("POST /boards/{id}/posts/{post}/delete", h.signedIn(h.deletePost))

	mux.HandleFunc("GET /news", h.signedIn(h.news))
	mux.HandleFunc("POST /news", h.signedIn(h.addNews))
	mux.HandleFunc("POST /news/{id}/delete", h.signedIn(h.deleteNews))

	mux.HandleFunc("GET /oneliners", h.signedIn(h.oneliners))
	mux.HandleFunc("POST /oneliners/{id}/delete", h.signedIn(h.deleteOneliner))

	mux.HandleFunc("GET /art", h.signedIn(h.art))
	mux.HandleFunc("GET /art/{name}", h.signedIn(h.artPreview))
	return mux
}

// staticHandler lets the stylesheet and fonts be cached: they only change with
// a new binary, and the page links carry no user data.
func staticHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		next.ServeHTTP(w, r)
	})
}

func pathID(q *request, name string) (int64, bool) {
	id, err := strconv.ParseInt(q.r.PathValue(name), 10, 64)
	return id, err == nil && id > 0
}

// ── login ───────────────────────────────────────────────────────────────────

type loginData struct {
	Handle string
	Error  string
}

func (h *Handler) loginPage(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		if s, ok := h.sessions.get(c.Value, h.now()); ok {
			if _, err := h.bbs.Sysop(s.userID); err == nil {
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
		}
	}
	h.render(w, nil, "login", "Sysop login", "", loginData{})
}

func (h *Handler) loginSubmit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil || !sameOrigin(r) {
		http.Error(w, "Bad form.", http.StatusBadRequest)
		return
	}
	handle := strings.TrimSpace(r.PostFormValue("handle"))
	password := r.PostFormValue("password")
	u, wait, err := h.bbs.SysopLogin(handle, password, clientIP(r))
	if err != nil {
		msg := "Login failed. Only sysops can sign in here."
		switch {
		case errors.Is(err, bbs.ErrLoginThrottled):
			msg = "Too many failed logins. Wait a while and try again."
			if wait > 0 {
				msg = fmt.Sprintf("Too many failed logins for that handle. Wait %s and try again.", waitText(wait))
			}
		case !errors.Is(err, bbs.ErrLoginFailed):
			h.log.Error("web login", "err", err)
			msg = "Login is not working right now. The details are in the server log."
		}
		w.WriteHeader(http.StatusUnauthorized)
		h.render(w, nil, "login", "Sysop login", "", loginData{Handle: handle, Error: msg})
		return
	}
	setCookie(w, h.sessions.create(u.ID, h.now()))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func waitText(d time.Duration) string {
	secs := int((d + time.Second - 1) / time.Second)
	return plural(max(secs, 1), "second")
}

func (h *Handler) logout(q *request) {
	if c, err := q.r.Cookie(cookieName); err == nil {
		h.sessions.drop(c.Value)
	}
	clearCookie(q.w)
	http.Redirect(q.w, q.r, "/login", http.StatusSeeOther)
}

// ── dashboard ───────────────────────────────────────────────────────────────

type dashData struct {
	Nodes   []bbs.NodeInfo
	Members int
	Waiting []store.User
	Events  []store.Event
}

func (h *Handler) dashboard(q *request) {
	st := h.bbs.Store()
	d := dashData{Nodes: h.bbs.Online()}
	var err error
	if d.Members, err = st.UserCount(); err == nil {
		if d.Waiting, err = st.PendingUsers(); err == nil {
			d.Events, err = st.Events("", dashEvents)
		}
	}
	if err != nil {
		h.log.Error("dashboard", "err", err)
	}
	h.render(q.w, q, "dashboard", "Dashboard", "dashboard", d)
}

func (h *Handler) kick(q *request) {
	id, err := strconv.Atoi(q.r.PostFormValue("node"))
	if err != nil || !h.bbs.KickNode(q.user, q.ip, id) {
		h.done(q, "/", "! That node is not connected any more.")
		return
	}
	h.done(q, "/", fmt.Sprintf("Node %d disconnected.", id))
}

func (h *Handler) broadcast(q *request) {
	if err := h.bbs.Broadcast(q.user, q.ip, q.r.PostFormValue("text")); err != nil {
		h.failed(q, "/", err)
		return
	}
	h.done(q, "/", "Sent to everyone online.")
}

// ── callers and users ───────────────────────────────────────────────────────

func (h *Handler) callers(q *request) {
	pending, err := h.bbs.Store().PendingUsers()
	if err != nil {
		h.log.Error("pending users", "err", err)
	}
	h.render(q.w, q, "callers", "New callers", "callers", pending)
}

type usersData struct {
	Query string
	Users []store.User
	Total int
}

func (h *Handler) users(q *request) {
	all, err := h.bbs.Store().ListUsers(userListLimit)
	if err != nil {
		h.log.Error("list users", "err", err)
	}
	query := strings.TrimSpace(q.r.URL.Query().Get("q"))
	d := usersData{Query: query, Total: len(all)}
	needle := strings.ToLower(query)
	for _, u := range all {
		if needle == "" || strings.Contains(strings.ToLower(u.Handle), needle) || strings.Contains(strings.ToLower(u.Location), needle) {
			d.Users = append(d.Users, u)
		}
	}
	h.render(q.w, q, "users", "Users", "users", d)
}

type userData struct {
	U       store.User
	Self    bool
	Online  []bbs.NodeInfo
	Events  []store.Event
	MinPass int
	MaxPass int
	MaxSubj int
}

func (h *Handler) user(q *request) {
	id, ok := pathID(q, "id")
	if !ok {
		http.NotFound(q.w, q.r)
		return
	}
	u, err := h.bbs.Store().UserByID(id)
	if err != nil {
		h.failed(q, "/users", err)
		return
	}
	d := userData{U: u, Self: u.ID == q.user.ID, MinPass: store.MinPasswordLen, MaxPass: store.MaxPasswordLen, MaxSubj: store.MaxSubjectLen}
	for _, n := range h.bbs.Online() {
		if n.UserID == u.ID {
			d.Online = append(d.Online, n)
		}
	}
	if events, err := h.bbs.Store().Events("", eventPageSize); err == nil {
		for _, e := range events {
			if e.UserID == u.ID || strings.EqualFold(e.Handle, u.Handle) || strings.Contains(e.Detail, u.Handle) {
				d.Events = append(d.Events, e)
				if len(d.Events) == 10 {
					break
				}
			}
		}
	}
	h.render(q.w, q, "user", u.Handle, "users", d)
}

func (h *Handler) userAction(q *request) {
	id, ok := pathID(q, "id")
	if !ok {
		http.NotFound(q.w, q.r)
		return
	}
	back := fmt.Sprintf("/users/%d", id)
	u, err := h.bbs.Store().UserByID(id)
	if err != nil {
		h.failed(q, "/users", err)
		return
	}
	f := q.r.PostFormValue
	switch q.r.PathValue("action") {
	case "approve":
		err = h.bbs.ApproveUser(q.user, q.ip, id)
		if err == nil {
			h.done(q, safeBack(f("back"), back), u.Handle+" approved.")
			return
		}
	case "lock":
		err = h.bbs.SetLocked(q.user, q.ip, id, true)
	case "unlock":
		err = h.bbs.SetLocked(q.user, q.ip, id, false)
	case "promote":
		err = h.bbs.SetSysop(q.user, q.ip, id, true)
	case "demote":
		err = h.bbs.SetSysop(q.user, q.ip, id, false)
		if err == nil {
			h.sessions.dropUser(id)
		}
	case "password":
		if f("password") != f("confirm") {
			h.done(q, back, "! The two passwords are different.")
			return
		}
		err = h.bbs.ResetPassword(q.user, q.ip, id, f("password"))
		if err == nil {
			h.done(q, back, "Password changed. Tell "+u.Handle+" the new one yourself; it is not sent anywhere.")
			return
		}
	case "mail":
		err = h.bbs.SendMail(q.user, q.ip, id, f("subject"), f("body"))
		if err == nil {
			h.done(q, back, "Mail sent to "+u.Handle+".")
			return
		}
	case "delete":
		if f("confirm") != u.Handle {
			h.done(q, back, "! Not deleted: type the handle exactly to confirm.")
			return
		}
		if err = h.bbs.DeleteUser(q.user, q.ip, id); err == nil {
			h.sessions.dropUser(id)
			h.done(q, safeBack(f("back"), "/users"), u.Handle+" deleted.")
			return
		}
	default:
		http.NotFound(q.w, q.r)
		return
	}
	if err != nil {
		h.failed(q, back, err)
		return
	}
	h.done(q, back, "Done.")
}

// safeBack only follows a return path within this site.
func safeBack(back, fallback string) string {
	if strings.HasPrefix(back, "/") && !strings.HasPrefix(back, "//") {
		return back
	}
	return fallback
}

// ── event log ───────────────────────────────────────────────────────────────

type eventsData struct {
	Kind   string
	Kinds  []string
	Events []store.Event
}

func (h *Handler) events(q *request) {
	kinds := []string{store.EventLogin, store.EventLoginFailed, store.EventLocked, store.EventSignup, store.EventSysop}
	kind := q.r.URL.Query().Get("kind")
	if !slices.Contains(kinds, kind) {
		kind = ""
	}
	events, err := h.bbs.Store().Events(kind, eventPageSize)
	if err != nil {
		h.log.Error("events", "err", err)
	}
	h.render(q.w, q, "events", "Event log", "events", eventsData{Kind: kind, Kinds: kinds, Events: events})
}

// ── boards ──────────────────────────────────────────────────────────────────

type boardsData struct {
	Boards  []store.BoardSummary
	MaxName int
	MaxDesc int
}

func (h *Handler) boards(q *request) {
	boards, err := h.bbs.Store().Boards(q.user.ID)
	if err != nil {
		h.log.Error("boards", "err", err)
	}
	h.render(q.w, q, "boards", "Message boards", "boards", boardsData{Boards: boards, MaxName: store.MaxBoardNameLen, MaxDesc: store.MaxBoardDescLen})
}

func (h *Handler) createBoard(q *request) {
	f := q.r.PostFormValue
	if err := h.bbs.CreateBoard(q.user, q.ip, f("name"), f("description"), f("sysop_only") == "on"); err != nil {
		h.failed(q, "/boards", err)
		return
	}
	h.done(q, "/boards", "Board created.")
}

type postView struct {
	store.Post
	Author string
}

type boardData struct {
	Board store.Board
	Posts []postView
}

func (h *Handler) board(q *request) {
	id, ok := pathID(q, "id")
	if !ok {
		http.NotFound(q.w, q.r)
		return
	}
	st := h.bbs.Store()
	b, err := st.Board(id)
	if err != nil {
		h.failed(q, "/boards", err)
		return
	}
	posts, err := st.Posts(id)
	if err != nil {
		h.log.Error("posts", "err", err)
	}
	names := h.handles()
	d := boardData{Board: b}
	for i := len(posts) - 1; i >= 0; i-- { // newest first
		d.Posts = append(d.Posts, postView{Post: posts[i], Author: nameOf(names, posts[i].AuthorID)})
	}
	h.render(q.w, q, "board", b.Name, "boards", d)
}

func (h *Handler) deleteBoard(q *request) {
	id, ok := pathID(q, "id")
	if !ok {
		http.NotFound(q.w, q.r)
		return
	}
	b, err := h.bbs.Store().Board(id)
	if err != nil {
		h.failed(q, "/boards", err)
		return
	}
	back := fmt.Sprintf("/boards/%d", id)
	if q.r.PostFormValue("confirm") != b.Name {
		h.done(q, back, "! Not deleted: type the board name exactly to confirm.")
		return
	}
	if err := h.bbs.DeleteBoard(q.user, q.ip, id); err != nil {
		h.failed(q, back, err)
		return
	}
	h.done(q, "/boards", b.Name+" deleted.")
}

func (h *Handler) deletePost(q *request) {
	boardID, ok1 := pathID(q, "id")
	postID, ok2 := pathID(q, "post")
	if !ok1 || !ok2 {
		http.NotFound(q.w, q.r)
		return
	}
	back := fmt.Sprintf("/boards/%d", boardID)
	st := h.bbs.Store()
	b, err := st.Board(boardID)
	if err != nil {
		h.failed(q, "/boards", err)
		return
	}
	posts, err := st.Posts(boardID)
	if err != nil {
		h.failed(q, back, err)
		return
	}
	i := slices.IndexFunc(posts, func(p store.Post) bool { return p.ID == postID })
	if i < 0 {
		h.failed(q, back, store.ErrNotFound)
		return
	}
	if err := h.bbs.DeletePost(q.user, q.ip, b.Name, posts[i]); err != nil {
		h.failed(q, back, err)
		return
	}
	h.done(q, back, "Post deleted.")
}

// ── news and the wall ───────────────────────────────────────────────────────

type bulletinView struct {
	store.Bulletin
	Author string
}

type newsData struct {
	Bulletins []bulletinView
	MaxTitle  int
}

func (h *Handler) news(q *request) {
	bs, err := h.bbs.Store().Bulletins()
	if err != nil {
		h.log.Error("bulletins", "err", err)
	}
	names := h.handles()
	d := newsData{MaxTitle: store.MaxSubjectLen}
	for _, b := range bs {
		d.Bulletins = append(d.Bulletins, bulletinView{Bulletin: b, Author: nameOf(names, b.AuthorID)})
	}
	h.render(q.w, q, "news", "News bulletins", "news", d)
}

func (h *Handler) addNews(q *request) {
	f := q.r.PostFormValue
	if err := h.bbs.AddBulletin(q.user, q.ip, f("title"), f("body")); err != nil {
		h.failed(q, "/news", err)
		return
	}
	h.done(q, "/news", "Bulletin posted, and everyone online was told.")
}

func (h *Handler) deleteNews(q *request) {
	id, ok := pathID(q, "id")
	if !ok {
		http.NotFound(q.w, q.r)
		return
	}
	bs, err := h.bbs.Store().Bulletins()
	if err != nil {
		h.failed(q, "/news", err)
		return
	}
	i := slices.IndexFunc(bs, func(b store.Bulletin) bool { return b.ID == id })
	if i < 0 {
		h.failed(q, "/news", store.ErrNotFound)
		return
	}
	if err := h.bbs.DeleteBulletin(q.user, q.ip, bs[i]); err != nil {
		h.failed(q, "/news", err)
		return
	}
	h.done(q, "/news", "Bulletin deleted.")
}

func (h *Handler) oneliners(q *request) {
	lines, err := h.bbs.Store().Oneliners(onelinerLimit)
	if err != nil {
		h.log.Error("oneliners", "err", err)
	}
	h.render(q.w, q, "oneliners", "Oneliner wall", "oneliners", lines)
}

func (h *Handler) deleteOneliner(q *request) {
	id, ok := pathID(q, "id")
	if !ok {
		http.NotFound(q.w, q.r)
		return
	}
	lines, err := h.bbs.Store().Oneliners(onelinerLimit)
	if err != nil {
		h.failed(q, "/oneliners", err)
		return
	}
	i := slices.IndexFunc(lines, func(o store.Oneliner) bool { return o.ID == id })
	if i < 0 {
		h.failed(q, "/oneliners", store.ErrNotFound)
		return
	}
	if err := h.bbs.DeleteOneliner(q.user, q.ip, lines[i]); err != nil {
		h.failed(q, "/oneliners", err)
		return
	}
	h.done(q, "/oneliners", "Line removed.")
}

// ── custom art ──────────────────────────────────────────────────────────────

func (h *Handler) art(q *request) {
	h.render(q.w, q, "art", "Custom art", "art", h.bbs.ArtScreens())
}

type artData struct {
	Name   string
	Screen template.HTML
}

func (h *Handler) artPreview(q *request) {
	name := q.r.PathValue("name")
	path, ok := h.bbs.ArtFile(name)
	if !ok {
		http.NotFound(q.w, q.r)
		return
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() > maxArtPreviewB {
		h.done(q, "/art", "! That file is missing or too large to preview.")
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		h.failed(q, "/art", err)
		return
	}
	var screen template.HTML
	if strings.HasSuffix(name, ".ans") {
		screen = renderANS(data)
	} else {
		screen = renderPipeText(string(data))
	}
	h.render(q.w, q, "artview", name, "art", artData{Name: name, Screen: screen})
}

// ── helpers ─────────────────────────────────────────────────────────────────

func (h *Handler) handles() map[int64]string {
	users, err := h.bbs.Store().Users()
	if err != nil {
		h.log.Error("users", "err", err)
		return nil
	}
	m := make(map[int64]string, len(users))
	for _, u := range users {
		m[u.ID] = u.Handle
	}
	return m
}

func nameOf(names map[int64]string, id int64) string {
	if n, ok := names[id]; ok {
		return n
	}
	return "(deleted)"
}
