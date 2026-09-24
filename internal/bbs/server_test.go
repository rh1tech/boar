package bbs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"testing"
	"time"

	"boar/internal/store"
)

const expectTimeout = 5 * time.Second

type testServer struct {
	addr string
	st   *store.Store
	srv  *Server
}

func startServer(t *testing.T, cfg Config) (string, *store.Store) {
	t.Helper()
	ts := startServerWith(t, cfg, nil)
	return ts.addr, ts.st
}

func startServerWith(t *testing.T, cfg Config, tweak func(*Server)) testServer {
	t.Helper()
	return startServerFull(t, cfg, store.Config{}, tweak)
}

// startServerFull also takes store settings; Path and KDFIterations are
// filled in.
func startServerFull(t *testing.T, cfg Config, sc store.Config, tweak func(*Server)) testServer {
	t.Helper()
	sc.Path, sc.KDFIterations = filepath.Join(t.TempDir(), "boar.db"), 1000
	st, err := store.Open(sc)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv := New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if tweak != nil {
		tweak(srv)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
		st.Close()
	})
	return testServer{addr: ln.Addr().String(), st: st, srv: srv}
}

// client is a scripted caller.
type client struct {
	t    *testing.T
	name string
	c    net.Conn
	buf  bytes.Buffer
}

func dial(t *testing.T, addr, name string) *client {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &client{t: t, name: name, c: c}
}

// expect reads until sub appears, consumes everything up to it and returns
// what came before it.
func (c *client) expect(sub string) string {
	c.t.Helper()
	deadline := time.Now().Add(expectTimeout)
	tmp := make([]byte, 4096)
	for !bytes.Contains(c.buf.Bytes(), []byte(sub)) {
		if err := c.c.SetReadDeadline(deadline); err != nil {
			c.t.Fatal(err)
		}
		n, err := c.c.Read(tmp)
		c.buf.Write(tmp[:n])
		if err != nil && !bytes.Contains(c.buf.Bytes(), []byte(sub)) {
			c.t.Fatalf("%s: waiting for %q: %v\n--- received ---\n%s", c.name, sub, err, c.buf.String())
		}
	}
	i := bytes.Index(c.buf.Bytes(), []byte(sub))
	before := string(c.buf.Next(i))
	c.buf.Next(len(sub))
	return before
}

func (c *client) key(k string) {
	c.t.Helper()
	if _, err := c.c.Write([]byte(k)); err != nil {
		c.t.Fatal(err)
	}
}

func (c *client) line(s string) { c.t.Helper(); c.key(s + "\r\n") }

// connectPlain picks the no-color ASCII terminal so output is easy to match.
func (c *client) connectPlain() {
	c.t.Helper()
	c.expect("Choice [1]:")
	c.line("3")
	c.expect("Handle (or NEW to register):")
}

func (c *client) login(handle, password string) {
	c.t.Helper()
	c.connectPlain()
	c.line(handle)
	c.expect("Password:")
	c.line(password)
}

// skipWall declines to write on the oneliner wall shown after login.
func (c *client) skipWall() {
	c.t.Helper()
	c.expect("Add a oneliner?")
	c.key("N")
	c.expect("] Main")
}

// enter logs in someone with no new mail or news and lands on the main menu.
func (c *client) enter(handle string) {
	c.t.Helper()
	c.login(handle, "secret12")
	c.expect("No new mail.")
	c.expect("press any key")
	c.key(" ")
	c.skipWall()
}

func (c *client) logoff() {
	c.t.Helper()
	c.key("G")
	c.expect("Log off?")
	c.key("Y")
	c.expect("NO CARRIER")
}

// sendMail goes from the main menu to the mailbox, sends, and comes back.
func (c *client) sendMail(to, subject string, body ...string) {
	c.t.Helper()
	c.key("M")
	c.expect("] Mail")
	c.key("S")
	c.expect("Enter = cancel):")
	c.line(to)
	c.expect("Subject")
	c.line(subject)
	c.writeBody(body...)
	c.expect("Message sent to")
	c.expect("press any key")
	c.key(" ")
	c.expect("] Mail")
	c.key("Q")
	c.expect("] Main")
}

// writeBody types lines into the editor and sends with /s.
func (c *client) writeBody(body ...string) {
	c.t.Helper()
	for i, ln := range body {
		c.expect(fmt.Sprintf("%3d:", i+1))
		c.line(ln)
	}
	c.expect(fmt.Sprintf("%3d:", len(body)+1))
	c.line("/s")
}

func mustCreate(t *testing.T, st *store.Store, handle string) store.User {
	t.Helper()
	u, err := st.CreateUser(handle, "secret12", "")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// mustCreateSysopAndUsers makes "Root" (sysop, being first) and then handles.
func mustCreateSysopAndUsers(t *testing.T, st *store.Store, handles ...string) []store.User {
	t.Helper()
	users := []store.User{mustCreate(t, st, "Root")}
	for _, h := range handles {
		users = append(users, mustCreate(t, st, h))
	}
	return users
}

// must unwraps (value, error) results; a panic fails the test with a trace.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestRegisterThroughTelnet(t *testing.T) {
	addr, st := startServer(t, Config{})
	c := dial(t, addr, "alice")
	c.connectPlain()
	c.line("new")
	c.expect("Telnet is not encrypted.")
	c.expect("Choose a handle:")
	c.line("new")
	c.expect("reserved")
	c.line("Alice")
	c.expect("Password:")
	c.line("secret12")
	c.expect("Password again:")
	c.line("secret12")
	c.expect("Location (optional):")
	c.line("Warsaw")
	c.expect("Create account Alice?")
	c.key("Y")
	c.expect("Welcome aboard, Alice!")
	c.expect("You are a sysop.") // the first caller runs the place
	c.expect("press any key")
	c.key(" ")
	c.expect("The wall is empty.")
	c.skipWall()
	c.logoff()

	u := must(st.UserByHandle("alice"))
	if u.Location != "Warsaw" || u.Calls != 1 || !u.Sysop {
		t.Fatalf("stored user = %+v", u)
	}
	signups := must(st.Events(store.EventSignup, 5))
	if len(signups) != 1 || signups[0].Handle != "Alice" {
		t.Fatalf("signup events = %+v", signups)
	}
}

func TestSendAndReadMail(t *testing.T) {
	addr, st := startServer(t, Config{})
	users := mustCreateSysopAndUsers(t, st, "Alice", "Bob")
	alice, bob := users[1], users[2]

	b := dial(t, addr, "bob")
	b.enter("bob")
	b.sendMail("alice", "Hello there", "Hi Alice!", "Second line.")
	b.logoff()

	a := dial(t, addr, "alice")
	a.login("Alice", "secret12")
	a.expect("You have 1 new message.")
	a.expect("Read them now?")
	a.key("Y")
	a.expect("From : Bob")
	a.expect("Subj : Hello there")
	a.expect("Hi Alice!")
	a.expect("Second line.")
	a.expect("[Q]uit")
	a.key("Q")
	a.skipWall()

	if n := must(st.UnreadCount(alice.ID)); n != 0 {
		t.Fatalf("unread after reading = %d", n)
	}

	// Reply with a quote, then delete the original.
	a.key("M")
	a.expect("] Mail")
	a.key("I")
	a.expect("Read message (1-1")
	a.line("1")
	a.expect("[R]eply")
	a.key("R")
	a.expect("Quote the original message?")
	a.key("Y")
	a.expect("Subject [Re: Hello there]:")
	a.line("")
	a.expect("> Hi Alice!")
	a.expect("  5:")
	a.line("Hi Bob, got it.")
	a.expect("  6:")
	a.line("/S")
	a.expect("Message sent to Bob.")
	a.expect("press any key")
	a.key(" ")
	a.expect("[D]elete")
	a.key("D")
	a.expect("Delete this message?")
	a.key("Y")
	a.expect("Nothing here yet.")

	in := must(st.Inbox(bob.ID))
	if len(in) != 1 || in[0].Subject != "Re: Hello there" {
		t.Fatalf("bob inbox = %+v", in)
	}
	wantBody := "On " + shortDate(in[0].SentAt) + ", Bob wrote:\n> Hi Alice!\n> Second line.\n\nHi Bob, got it."
	if in[0].Body != wantBody {
		t.Fatalf("reply body = %q, want %q", in[0].Body, wantBody)
	}
	if len(must(st.Inbox(alice.ID))) != 0 {
		t.Fatal("alice's copy should be deleted")
	}
	if thread := must(st.Thread(in[0].ID, bob.ID)); len(thread) != 2 {
		t.Fatalf("reply should join the thread: %+v", thread)
	}
}

func TestOnlineUserIsNotifiedOfNewMail(t *testing.T) {
	addr, st := startServer(t, Config{})
	mustCreateSysopAndUsers(t, st, "Alice", "Bob")

	a := dial(t, addr, "alice")
	a.enter("alice")

	b := dial(t, addr, "bob")
	b.enter("bob")
	b.sendMail("alice", "Ping", "Are you there?")

	a.key("W")
	a.expect("Bob")
	a.expect("press any key")
	a.key(" ")
	a.expect("(1 new)")
	a.expect("*** New mail from Bob: Ping ***")
}

func TestAllNodesBusy(t *testing.T) {
	addr, _ := startServer(t, Config{MaxNodes: 1})
	first := dial(t, addr, "first")
	first.expect("Choice [1]:")
	second := dial(t, addr, "second")
	second.expect("All nodes are busy")
}

func TestFailedLoginsLockOutAddress(t *testing.T) {
	addr, st := startServer(t, Config{})
	mustCreate(t, st, "Alice")
	// Different handles each time, so the per-handle backoff stays out of it.
	guesses := []string{"alice", "bob", "carol", "dave", "erin"}

	c := dial(t, addr, "attacker")
	c.connectPlain()
	for _, h := range guesses[:maxLoginAttempts] {
		c.line(h)
		c.expect("Password:")
		c.line("wrong-password")
		c.expect("Invalid handle or password, or the account is locked.")
	}
	c.expect("Too many attempts. Goodbye.")

	c2 := dial(t, addr, "attacker2")
	c2.connectPlain()
	for _, h := range guesses[maxLoginAttempts:] {
		c2.line(h)
		c2.expect("Password:")
		c2.line("wrong-password")
		c2.expect("Invalid handle or password, or the account is locked.")
	}
	c2.expect("Too many failed logins")

	c3 := dial(t, addr, "attacker3")
	c3.expect("Too many failed logins from your address")

	if failed := must(st.Events(store.EventLoginFailed, 10)); len(failed) != 5 {
		t.Fatalf("failed login events = %d", len(failed))
	}
}

func TestLockedAccountCannotLogIn(t *testing.T) {
	addr, st := startServer(t, Config{})
	users := mustCreateSysopAndUsers(t, st, "Alice")
	if err := st.SetLocked(users[0].ID, users[1].ID, true); err != nil {
		t.Fatal(err)
	}
	c := dial(t, addr, "alice")
	c.login("alice", "secret12")
	c.expect("Invalid handle or password, or the account is locked.") // the right password reveals nothing
}

func TestUTF8TerminalGetsColorAndBoxDrawing(t *testing.T) {
	addr, _ := startServer(t, Config{Name: "Test|BBS"})
	c := dial(t, addr, "color")
	c.expect("Test|BBS")
	c.expect("Choice [1]:")
	c.line("")
	c.expect("\x1b[2J")
	c.expect("█")
	c.expect("\x1b[0;1;33;40m")
	c.expect("Handle")
}

func TestCP437TerminalGetsCP437Bytes(t *testing.T) {
	addr, _ := startServer(t, Config{})
	c := dial(t, addr, "cp437")
	c.expect("Choice [1]:")
	c.line("2")
	c.expect("\xdb\xdb") // two full blocks of the logo in CP437
	c.expect("Handle")
}

func TestLoginTimeoutHangsUpActiveButAnonymousCaller(t *testing.T) {
	ts := startServerWith(t, Config{}, func(s *Server) { s.loginTimeout = 300 * time.Millisecond })
	c := dial(t, ts.addr, "dripper")
	c.expect("Choice [1]:")
	stop := time.After(2 * time.Second)
	for {
		select {
		case <-stop:
			t.Fatal("connection still open after login timeout")
		default:
		}
		_ = c.c.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		if _, err := c.c.Write([]byte("x")); err != nil {
			return // server hung up
		}
		buf := make([]byte, 256)
		if _, err := c.c.Read(buf); err == io.EOF {
			return
		}
	}
}

func TestHandleBackoffSurvivesAddressChanges(t *testing.T) {
	ts := startServerWith(t, Config{}, nil)
	mustCreate(t, ts.st, "Alice")
	c := dial(t, ts.addr, "guesser")
	c.connectPlain()
	for range backoffFree {
		c.line("alice")
		c.expect("Password:")
		c.line("wrong-password")
		c.expect("Invalid handle or password")
	}
	// Another connection (3 failures is under the per-IP limit), and even the
	// right password has to wait.
	c2 := dial(t, ts.addr, "owner")
	c2.connectPlain()
	c2.line("alice")
	c2.expect("Password:")
	c2.line("secret12")
	c2.expect("Too many failed logins for that handle. Wait 2 seconds")
}

func TestDelayGrowsAndIsCapped(t *testing.T) {
	want := map[int]time.Duration{0: 0, 2: 0, 3: 2 * time.Second, 4: 4 * time.Second, 5: 8 * time.Second, 9: time.Minute, 50: time.Minute}
	for n, d := range want {
		if got := delayFor(n); got != d {
			t.Errorf("delayFor(%d) = %v, want %v", n, got, d)
		}
	}
}

func TestBackoffReservesAndClearsOnSuccess(t *testing.T) {
	b := newLoginBackoff(newRateLimiter(0, backoffWindow))
	if _, ok := b.begin("k"); !ok {
		t.Fatal("first attempt refused")
	}
	if _, ok := b.begin("k"); ok {
		t.Fatal("parallel attempt allowed while one is in flight")
	}
	b.end("k", false)
	for range backoffFree - 1 {
		b.begin("k")
		b.end("k", false)
	}
	if wait, ok := b.begin("k"); ok || wait <= 0 {
		t.Fatalf("backoff not applied: %v %v", wait, ok)
	}
	b.fails.reset("k") // what a success does
	if _, ok := b.begin("k"); !ok {
		t.Fatal("reset didn't clear the backoff")
	}
	b.release("k")
}

func TestLimitsSurviveRestart(t *testing.T) {
	st, err := store.Open(store.Config{Path: filepath.Join(t.TempDir(), "boar.db"), KDFIterations: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	first := newLimits(Config{MaxSignupsPerDay: 2}, st, log)
	for range 5 {
		first.loginFailures.hit("203.0.113.9")
	}
	first.signupsAll.hit(signupsAllKey)
	first.signupsAll.hit(signupsAllKey)

	again := newLimits(Config{MaxSignupsPerDay: 2}, st, log) // as after a restart
	if !again.loginFailures.blocked("203.0.113.9") {
		t.Fatal("IP block forgotten on restart")
	}
	if !again.signupsAll.blocked(signupsAllKey) {
		t.Fatal("daily signup cap forgotten on restart")
	}
}

func TestLimitKeyGroupsIPv6ByPrefix(t *testing.T) {
	cases := map[string]string{
		"203.0.113.7":          "203.0.113.7",
		"2001:db8:1:2:aaaa::1": "2001:db8:1:2::/64",
		"2001:db8:1:2:bbbb::9": "2001:db8:1:2::/64",
		"::ffff:198.51.100.1":  "::ffff:198.51.100.1",
		"not-an-ip":            "not-an-ip",
	}
	for in, want := range cases {
		if got := limitKey(in); got != want {
			t.Errorf("limitKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestConnectionCaps(t *testing.T) {
	c := newConnLimiter(3, 2)
	if !c.acquire("a") || !c.acquire("a") {
		t.Fatal("first two from a should fit")
	}
	if c.acquire("a") {
		t.Fatal("per-IP cap not applied")
	}
	if !c.acquire("b") || c.acquire("c") {
		t.Fatal("total cap not applied")
	}
	c.release("a")
	if !c.acquire("c") {
		t.Fatal("released slot not reusable")
	}
	c.release("a")
	c.release("b")
	c.release("c")
	if c.total != 0 || len(c.perIP) != 0 {
		t.Fatalf("leaked slots: total=%d perIP=%v", c.total, c.perIP)
	}
}

func TestExcessConnectionsAreDropped(t *testing.T) {
	ts := startServerWith(t, Config{}, func(s *Server) { s.conns = newConnLimiter(1, 1) })
	first := dial(t, ts.addr, "first")
	first.expect("Choice [1]:")
	second := dial(t, ts.addr, "second")
	_ = second.c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if n, err := second.c.Read(make([]byte, 64)); err != io.EOF {
		t.Fatalf("second connection read %d bytes, err %v; want EOF", n, err)
	}
}

// stuckTerminal never finishes a write until closed, like a client that
// stopped reading.
type stuckTerminal struct {
	closed chan struct{}
}

func (s *stuckTerminal) ReadByte() (byte, error) { <-s.closed; return 0, io.EOF }
func (s *stuckTerminal) Write(p []byte) (int, error) {
	<-s.closed
	return 0, io.ErrClosedPipe
}
func (s *stuckTerminal) Size() (int, int) { return 80, 24 }
func (s *stuckTerminal) Close() error {
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
	return nil
}

func TestCloseAllDoesNotHoldTheTableLock(t *testing.T) {
	nt := newNodeTable(4)
	stuck := &stuckTerminal{closed: make(chan struct{})}
	if _, err := nt.acquire(stuck, false); err != nil {
		t.Fatal(err)
	}
	go nt.closeAll("bye") // blocks writing to the stuck terminal

	// Other sessions must still be able to use the table meanwhile.
	done := make(chan struct{})
	go func() {
		nt.online()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("node table locked while hanging up a stuck caller")
	}
	stuck.Close()
}
