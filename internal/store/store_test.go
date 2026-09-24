// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testIter = 1000

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sub", "boar.db")
	s, err := Open(Config{Path: path, KDFIterations: testIter})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func mustUser(t *testing.T, s *Store, handle string) User {
	t.Helper()
	u, err := s.CreateUser(handle, "secret12", "Warsaw")
	if err != nil {
		t.Fatalf("CreateUser(%q): %v", handle, err)
	}
	return u
}

// must unwraps (value, error) results; a panic fails the test with a trace.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestFirstUserIsSysop(t *testing.T) {
	s, _ := openTemp(t)
	a := mustUser(t, s, "Alice")
	b := mustUser(t, s, "Bob")
	if !a.Sysop || b.Sysop {
		t.Fatalf("sysop flags: alice=%v bob=%v", a.Sysop, b.Sysop)
	}
	if a.Handle != "Alice" || a.Location != "Warsaw" || a.CreatedAt.IsZero() {
		t.Fatalf("user = %+v", a)
	}
}

func TestAuthenticate(t *testing.T) {
	s, _ := openTemp(t)
	u := mustUser(t, s, "Alice")
	got, err := s.Authenticate("aLiCe", "secret12")
	if err != nil || got.ID != u.ID {
		t.Fatalf("Authenticate = %+v, %v", got, err)
	}
	if _, err := s.Authenticate("alice", "wrong!!"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("wrong password err = %v", err)
	}
	if _, err := s.Authenticate("nobody", "secret12"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("unknown user err = %v", err)
	}
}

func TestHandlesAreCaseInsensitivelyUnique(t *testing.T) {
	s, _ := openTemp(t)
	mustUser(t, s, "Alice")
	if _, err := s.CreateUser("ALICE", "secret12", ""); !errors.Is(err, ErrHandleTaken) {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateHandle(t *testing.T) {
	for _, h := range []string{"Bob", "Kasia K.", "dark_boar-99"} {
		if err := ValidateHandle(h); err != nil {
			t.Errorf("ValidateHandle(%q) = %v", h, err)
		}
	}
	bad := []string{"ab", strings.Repeat("a", 21), "1abc", "a  b", "abc ", "new", "NEW", "Żaba", "a|b", "a\x1bb"}
	for _, h := range bad {
		var ie *InputError
		if err := ValidateHandle(h); !errors.As(err, &ie) {
			t.Errorf("ValidateHandle(%q) = %v, want InputError", h, err)
		}
	}
}

func TestPersistsAcrossReopen(t *testing.T) {
	s, path := openTemp(t)
	a := mustUser(t, s, "Alice")
	b := mustUser(t, s, "Bob")
	must(s.SendMessage(b.ID, a.ID, "Hi", "Hello!", 0))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("db permissions = %v, want 0600", perm)
	}

	s2, err := Open(Config{Path: path, KDFIterations: testIter})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if _, err := s2.Authenticate("bob", "secret12"); err != nil {
		t.Fatalf("auth after reopen: %v", err)
	}
	inbox := must(s2.Inbox(a.ID))
	if len(inbox) != 1 || inbox[0].Subject != "Hi" {
		t.Fatalf("inbox after reopen = %+v", inbox)
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	s, path := openTemp(t)
	if _, err := s.db.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := Open(Config{Path: path}); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("err = %v", err)
	}
}

func TestMailLifecycle(t *testing.T) {
	s, _ := openTemp(t)
	a := mustUser(t, s, "Alice")
	b := mustUser(t, s, "Bob")

	m := must(s.SendMessage(a.ID, b.ID, "  Lunch?\x1b[2J ", "line one  \r\n\x1b[31mline two\n\n", 0))
	if m.Subject != "Lunch?[2J" || m.Body != "line one\n[31mline two" || m.ThreadID != m.ID {
		t.Fatalf("message = %+v", m)
	}
	if n := must(s.UnreadCount(b.ID)); n != 1 {
		t.Fatalf("unread = %d", n)
	}
	if err := s.MarkRead(m.ID, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("sender MarkRead err = %v", err)
	}
	if err := s.MarkRead(m.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkRead(m.ID, b.ID); err != nil {
		t.Fatalf("second MarkRead err = %v", err)
	}
	if n := must(s.UnreadCount(b.ID)); n != 0 {
		t.Fatalf("unread after read = %d", n)
	}
	if out := must(s.Outbox(a.ID)); len(out) != 1 || !out[0].IsRead() {
		t.Fatalf("outbox should show the read receipt: %+v", out)
	}

	if err := s.DeleteMessage(m.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if len(must(s.Inbox(b.ID))) != 0 || len(must(s.Outbox(a.ID))) != 1 {
		t.Fatal("recipient delete should keep sender's copy")
	}
	if err := s.DeleteMessage(m.ID, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double delete err = %v", err)
	}
	if err := s.DeleteMessage(m.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&n); err != nil || n != 0 {
		t.Fatalf("message should be purged, count=%d err=%v", n, err)
	}
}

func TestThreads(t *testing.T) {
	s, _ := openTemp(t)
	a := mustUser(t, s, "Alice")
	b := mustUser(t, s, "Bob")
	c := mustUser(t, s, "Carol")

	m1 := must(s.SendMessage(a.ID, b.ID, "Plan", "one", 0))
	m2 := must(s.SendMessage(b.ID, a.ID, "Re: Plan", "two", m1.ID))
	m3 := must(s.SendMessage(a.ID, b.ID, "Re: Plan", "three", m2.ID))
	if m2.ThreadID != m1.ID || m3.ThreadID != m1.ID {
		t.Fatalf("thread ids: %d %d want %d", m2.ThreadID, m3.ThreadID, m1.ID)
	}
	thread := must(s.Thread(m3.ID, b.ID))
	if len(thread) != 3 || thread[0].ID != m1.ID {
		t.Fatalf("thread = %+v", thread)
	}
	// Carol can't attach her message to a thread she can't see.
	m4 := must(s.SendMessage(c.ID, a.ID, "Sneaky", "x", m1.ID))
	if m4.ThreadID != m4.ID || m4.ReplyTo != 0 {
		t.Fatalf("foreign reply joined thread: %+v", m4)
	}
	if _, err := s.Thread(m1.ID, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider thread err = %v", err)
	}
}

func TestBlocking(t *testing.T) {
	s, _ := openTemp(t)
	sysop := mustUser(t, s, "Root")
	a := mustUser(t, s, "Alice")
	b := mustUser(t, s, "Bob")

	if err := s.Block(a.ID, a.ID); err == nil {
		t.Fatal("self-block allowed")
	}
	if err := s.Block(a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Block(a.ID, b.ID); err != nil {
		t.Fatalf("repeat block: %v", err)
	}
	if _, err := s.SendMessage(b.ID, a.ID, "hi", "hi", 0); !errors.Is(err, ErrBlocked) {
		t.Fatalf("blocked send err = %v", err)
	}
	if err := s.Block(a.ID, sysop.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SendMessage(sysop.ID, a.ID, "notice", "sysop mail gets through", 0); err != nil {
		t.Fatalf("sysop mail blocked: %v", err)
	}
	blocked := must(s.BlockedUsers(a.ID))
	if len(blocked) != 2 || blocked[0].Handle != "Bob" {
		t.Fatalf("blocked = %+v", blocked)
	}
	if ok := must(s.IsBlocked(a.ID, b.ID)); !ok {
		t.Fatal("IsBlocked")
	}
	if err := s.Unblock(a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	must(s.SendMessage(b.ID, a.ID, "hi", "hi", 0))
}

func TestSearchMail(t *testing.T) {
	s, _ := openTemp(t)
	a := mustUser(t, s, "Alice")
	b := mustUser(t, s, "Bob")
	c := mustUser(t, s, "Carol")
	must(s.SendMessage(b.ID, a.ID, "Modem talk", "14.4k forever", 0))
	must(s.SendMessage(a.ID, c.ID, "Pizza", "Friday?", 0))
	must(s.SendMessage(b.ID, c.ID, "Private", "modem secrets", 0))

	cases := map[string]int{"MODEM": 1, "friday": 1, "carol": 1, "bob": 1, "zzz": 0, " ": 0}
	for q, want := range cases {
		if got := must(s.SearchMail(a.ID, q)); len(got) != want {
			t.Errorf("SearchMail(%q) = %d results, want %d", q, len(got), want)
		}
	}
}

func TestSendValidation(t *testing.T) {
	s, _ := openTemp(t)
	a := mustUser(t, s, "Alice")
	var ie *InputError
	if _, err := s.SendMessage(a.ID, a.ID, " ", "body", 0); !errors.As(err, &ie) {
		t.Errorf("empty subject err = %v", err)
	}
	if _, err := s.SendMessage(a.ID, a.ID, "s", "\n \n", 0); !errors.As(err, &ie) {
		t.Errorf("empty body err = %v", err)
	}
	if _, err := s.SendMessage(a.ID, a.ID, "s", strings.Repeat("x", MaxBodyBytes+1), 0); !errors.As(err, &ie) {
		t.Errorf("huge body err = %v", err)
	}
	if _, err := s.SendMessage(a.ID, 99, "s", "b", 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown recipient err = %v", err)
	}
}

func TestMailboxFull(t *testing.T) {
	s, _ := openTemp(t)
	a := mustUser(t, s, "Alice")
	b := mustUser(t, s, "Bob")
	if _, err := s.db.Exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < ?)
		INSERT INTO messages (from_id, to_id, thread_id, subject, body, sent_at) SELECT ?, ?, 0, 's', 'b', 1 FROM n`,
		MaxInbox, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SendMessage(a.ID, b.ID, "s", "b", 0); !errors.Is(err, ErrMailboxFull) {
		t.Fatalf("err = %v", err)
	}
}

func TestAccountManagement(t *testing.T) {
	s, _ := openTemp(t)
	a := mustUser(t, s, "Alice")
	b := mustUser(t, s, "Bob")

	u, prev, err := s.RecordLogin(b.ID)
	if err != nil || u.Calls != 1 || !prev.IsZero() || u.LastLogin.IsZero() {
		t.Fatalf("first login = %+v prev=%v err=%v", u, prev, err)
	}
	u2, prev2, _ := s.RecordLogin(b.ID)
	if u2.Calls != 2 || !prev2.Equal(u.LastLogin) {
		t.Fatalf("second login = %+v prev=%v", u2, prev2)
	}

	if err := s.ChangePassword(b.ID, "nope!!", "newpass1"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("bad current password err = %v", err)
	}
	if err := s.ChangePassword(b.ID, "secret12", "newpass1"); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetPassword(a.ID, b.ID, "sysopset1"); err != nil {
		t.Fatal(err)
	}
	must(s.Authenticate("bob", "sysopset1"))

	loc := must(s.SetLocation(b.ID, "  Kraków\x07  "))
	if loc.Location != "Kraków" {
		t.Fatalf("location = %q", loc.Location)
	}
	if err := s.SetLocked(a.ID, b.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSysop(a.ID, b.ID, true); err != nil {
		t.Fatal(err)
	}
	got := must(s.UserByHandle("BOB"))
	if !got.Locked || !got.Sysop {
		t.Fatalf("flags = %+v", got)
	}
	if err := s.SetLocked(a.ID, 999, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user err = %v", err)
	}

	must(s.SendMessage(a.ID, b.ID, "to bob", "x", 0))
	sent := must(s.SendMessage(b.ID, a.ID, "from bob", "x", 0))
	if err := s.Block(a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UserByID(b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("user not deleted")
	}
	if in := must(s.Inbox(a.ID)); len(in) != 1 || in[0].ID != sent.ID {
		t.Fatalf("mail from deleted user should stay: %+v", in)
	}
	if len(must(s.BlockedUsers(a.ID))) != 0 {
		t.Fatal("blocks should cascade")
	}
	if err := s.DeleteUser(a.ID, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double delete err = %v", err)
	}
	must(s.CreateUser("bob", "secret12", ""))
}

func TestUsersSortedByHandle(t *testing.T) {
	s, _ := openTemp(t)
	mustUser(t, s, "zed")
	mustUser(t, s, "Alice")
	mustUser(t, s, "bob")
	var got []string
	for _, u := range must(s.Users()) {
		got = append(got, u.Handle)
	}
	if strings.Join(got, ",") != "Alice,bob,zed" {
		t.Fatalf("order = %v", got)
	}
	if n := must(s.UserCount()); n != 3 {
		t.Fatalf("UserCount = %d", n)
	}
}

func TestBoards(t *testing.T) {
	s, _ := openTemp(t)
	sysop := mustUser(t, s, "Root")
	a := mustUser(t, s, "Alice")
	b := mustUser(t, s, "Bob")

	boards := must(s.Boards(a.ID))
	if len(boards) != 2 || boards[0].Name != "Announcements" || !boards[0].SysopOnly {
		t.Fatalf("seeded boards = %+v", boards)
	}
	general := boards[1]
	if _, err := s.AddPost(boards[0].ID, a.ID, "hi", "hi", 0); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-sysop announcement err = %v", err)
	}
	must(s.AddPost(boards[0].ID, sysop.ID, "Welcome", "Hello all", 0))

	p1 := must(s.AddPost(general.ID, a.ID, "Modems", "56k or bust", 0))
	p2 := must(s.AddPost(general.ID, b.ID, "Re: Modems", "14.4k", p1.ID))
	if p2.ThreadID != p1.ID {
		t.Fatalf("reply thread = %d", p2.ThreadID)
	}

	sum := must(s.Boards(b.ID))[1]
	if sum.Posts != 2 || sum.New != 1 || sum.LastPost.IsZero() {
		t.Fatalf("bob's summary = %+v (own posts should not count as new)", sum)
	}
	if err := s.MarkBoardRead(b.ID, general.ID, p1.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkBoardRead(b.ID, general.ID, 0); err != nil {
		t.Fatal(err)
	}
	if last := must(s.LastRead(b.ID, general.ID)); last != p1.ID {
		t.Fatalf("last read went backwards: %d", last)
	}

	if err := s.DeletePost(p1.ID, b.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("delete someone else's post err = %v", err)
	}
	if err := s.DeletePost(p2.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePost(p1.ID, sysop.ID); err != nil {
		t.Fatal(err)
	}
	if n := len(must(s.Posts(general.ID))); n != 0 {
		t.Fatalf("posts left = %d", n)
	}

	nb := must(s.CreateBoard(sysop.ID, " Retro  Gaming ", "Doors & more", false))
	if _, err := s.CreateBoard(sysop.ID, "retro  gaming", "", false); err == nil {
		t.Fatal("duplicate board allowed")
	}
	if _, err := s.CreateBoard(sysop.ID, "x", "", false); err == nil {
		t.Fatal("too-short board name allowed")
	}
	if got := must(s.Board(nb.ID)); got.Description != "Doors & more" {
		t.Fatalf("board = %+v", got)
	}
	must(s.AddPost(nb.ID, a.ID, "LORD", "is back", 0))
	if err := s.DeleteBoard(sysop.ID, nb.ID); err != nil {
		t.Fatal(err)
	}
	if n := len(must(s.Posts(nb.ID))); n != 0 {
		t.Fatal("posts should go with their board")
	}
}

func TestBulletinsAndOneliners(t *testing.T) {
	s, _ := openTemp(t)
	sysop := mustUser(t, s, "Root")
	a := mustUser(t, s, "Alice")
	before := time.Now()

	if _, err := s.AddBulletin(a.ID, "Hi", "x"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-sysop bulletin err = %v", err)
	}
	b1 := must(s.AddBulletin(sysop.ID, "Welcome", "Rules: be nice."))
	must(s.AddBulletin(sysop.ID, "Downtime", "Saturday."))
	if n := must(s.BulletinsSince(before)); n != 2 {
		t.Fatalf("since = %d", n)
	}
	list := must(s.Bulletins())
	if len(list) != 2 || list[0].Title != "Downtime" {
		t.Fatalf("bulletins = %+v", list)
	}
	if err := s.DeleteBulletin(sysop.ID, b1.ID); err != nil {
		t.Fatal(err)
	}

	for i := range 4 {
		must(s.AddOneliner(a.ID, strings.Repeat("x", i+1)))
	}
	if _, err := s.AddOneliner(a.ID, "  \x1b "); err == nil {
		t.Fatal("empty oneliner allowed")
	}
	long := must(s.AddOneliner(a.ID, strings.Repeat("y", 200)))
	if len(long.Text) != MaxOnelinerLen || long.Author != "Alice" {
		t.Fatalf("oneliner = %+v", long)
	}
	wall := must(s.Oneliners(3))
	if len(wall) != 3 || wall[0].Text != "xxx" || wall[2].ID != long.ID {
		t.Fatalf("wall = %+v", wall)
	}
	if err := s.DeleteOneliner(a.ID, long.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-sysop delete err = %v", err)
	}
	if err := s.DeleteOneliner(sysop.ID, long.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteOneliner(sysop.ID, long.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double delete err = %v", err)
	}
}

func TestEvents(t *testing.T) {
	s, _ := openTemp(t)
	for i, kind := range []string{EventLogin, EventLoginFailed, EventLogin} {
		if err := s.LogEvent(Event{Kind: kind, UserID: int64(i), Handle: "h", IP: "1.2.3.4"}); err != nil {
			t.Fatal(err)
		}
	}
	all := must(s.Events("", 10))
	if len(all) != 3 || all[0].UserID != 2 || all[0].At.IsZero() {
		t.Fatalf("events = %+v", all)
	}
	if logins := must(s.Events(EventLogin, 10)); len(logins) != 2 {
		t.Fatalf("logins = %+v", logins)
	}
}

func TestAdminActionsRequireSysop(t *testing.T) {
	s, _ := openTemp(t)
	root := mustUser(t, s, "Root")
	a := mustUser(t, s, "Alice")
	b := mustUser(t, s, "Bob")
	board := must(s.Boards(a.ID))[0]
	bull := must(s.AddBulletin(root.ID, "News", "body"))

	forbidden := map[string]error{
		"lock":         s.SetLocked(a.ID, b.ID, true),
		"promote":      s.SetSysop(a.ID, a.ID, true),
		"reset":        s.ResetPassword(a.ID, b.ID, "stolen123"),
		"delete user":  s.DeleteUser(a.ID, b.ID),
		"delete board": s.DeleteBoard(a.ID, board.ID),
		"delete news":  s.DeleteBulletin(a.ID, bull.ID),
		"lock self":    s.SetLocked(root.ID, root.ID, true),
		"demote self":  s.SetSysop(root.ID, root.ID, false),
		"delete self":  s.DeleteUser(root.ID, root.ID),
	}
	if _, err := s.CreateBoard(a.ID, "Hax", "", false); !errors.Is(err, ErrForbidden) {
		t.Errorf("create board err = %v", err)
	}
	for what, err := range forbidden {
		if !errors.Is(err, ErrForbidden) {
			t.Errorf("%s: err = %v, want ErrForbidden", what, err)
		}
	}
	if got := must(s.UserByID(b.ID)); got.Locked {
		t.Fatal("non-sysop managed to lock someone")
	}

	u := must(s.BootstrapSysop("alice"))
	if !u.Sysop {
		t.Fatal("bootstrap did not promote")
	}
	if _, err := s.BootstrapSysop("nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bootstrap unknown err = %v", err)
	}
}

func TestPostsListingIsCapped(t *testing.T) {
	s, _ := openTemp(t)
	a := mustUser(t, s, "Alice")
	general := must(s.Boards(a.ID))[1]
	if _, err := s.db.Exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < ?)
		INSERT INTO posts (board_id, author_id, thread_id, subject, body, posted_at) SELECT ?, ?, 0, 's' || i, 'b', i FROM n`,
		MaxBoardPosts+10, general.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	posts := must(s.Posts(general.ID))
	if len(posts) != MaxBoardPosts || posts[len(posts)-1].Subject != fmt.Sprintf("s%d", MaxBoardPosts+10) {
		t.Fatalf("got %d posts, last %q", len(posts), posts[len(posts)-1].Subject)
	}
	if n := len(must(s.ListUsers(0))); n != 0 {
		t.Fatalf("ListUsers(0) = %d", n)
	}
}
