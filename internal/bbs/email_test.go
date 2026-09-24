package bbs

import (
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"boar/internal/mailer"
	"boar/internal/store"
)

// fakeMail records what the BBS would have emailed.
type fakeMail struct {
	mu   sync.Mutex
	sent []mailer.Message
}

func (f *fakeMail) Enqueue(m mailer.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return nil
}

func (f *fakeMail) all() []mailer.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mailer.Message(nil), f.sent...)
}

// waitFor polls until n messages were sent; delivery hooks run on the
// sender's session goroutine.
func (f *fakeMail) waitFor(t *testing.T, n int) []mailer.Message {
	t.Helper()
	deadline := time.Now().Add(expectTimeout)
	for len(f.all()) < n {
		if time.Now().After(deadline) {
			t.Fatalf("expected %d emails, got %+v", n, f.all())
		}
		time.Sleep(10 * time.Millisecond)
	}
	return f.all()
}

var codePattern = regexp.MustCompile(`\b\d{6}\b`)

func TestEmailVerifyAlertsAndCopies(t *testing.T) {
	mail := &fakeMail{}
	ts := startServerWith(t, Config{Name: "Boar", Mail: mail, PublicAddress: "bbs.example.com:2222"}, nil)
	users := mustCreateSysopAndUsers(t, ts.st, "Alice", "Bob")
	alice := users[1]

	a := dial(t, ts.addr, "alice")
	a.enter("alice")
	a.key("S")
	a.expect("(not set)")
	a.key("E")
	a.expect("[S]et address")
	a.key("S")
	a.expect("Email address:")
	a.line("not-an-address")
	a.expect("doesn't look like an email address")
	a.key(" ")
	a.key("S")
	a.expect("Email address:")
	a.line("alice@example.com")
	a.expect("A code is on its way to alice@example.com.")

	sent := mail.waitFor(t, 1)
	code := codePattern.FindString(sent[0].Subject)
	if sent[0].To != "alice@example.com" || code == "" || !strings.Contains(sent[0].Body, code) {
		t.Fatalf("verification email = %+v", sent[0])
	}
	a.expect("Code from the email")
	a.line("000000")
	a.expect("Wrong code.")
	a.key(" ")
	a.expect("Waiting")
	a.key("V")
	a.expect("Code from the email")
	a.line(code)
	a.expect("Verified!")
	a.key(" ")
	a.expect("alice@example.com  (verified)")
	a.expect("notice only")
	a.key("Q")
	a.expect("] Settings")
	a.key("Q")
	a.expect("] Main")

	// Alice is online: Bob's mail shows up live, so no notice email.
	b := dial(t, ts.addr, "bob")
	b.enter("bob")
	b.sendMail("alice", "While you're here", "hi")
	if n := len(mail.all()); n != 1 {
		t.Fatalf("notice sent although Alice is online: %+v", mail.all()[1:])
	}

	// Offline: a notice, without the message itself.
	a.logoff()
	b.sendMail("alice", "Lunch?", "Pierogi at noon.")
	notice := mail.waitFor(t, 2)[1]
	if notice.To != "alice@example.com" || !strings.Contains(notice.Subject, "from Bob") ||
		!strings.Contains(notice.Body, "Lunch?") || strings.Contains(notice.Body, "Pierogi") ||
		!strings.Contains(notice.Body, "bbs.example.com:2222") {
		t.Fatalf("notice = %+v", notice)
	}

	// Full copies include the body.
	if _, err := ts.st.SetEmailMode(alice.ID, store.EmailCopy); err != nil {
		t.Fatal(err)
	}
	b.sendMail("alice", "Menu", "Barszcz and uszka.")
	cp := mail.waitFor(t, 3)[2]
	if !strings.Contains(cp.Subject, "Menu") || !strings.Contains(cp.Body, "Barszcz and uszka.") || !strings.Contains(cp.Body, "From: Bob") {
		t.Fatalf("copy = %+v", cp)
	}
}

func TestEmailMeACopy(t *testing.T) {
	mail := &fakeMail{}
	ts := startServerWith(t, Config{Mail: mail}, nil)
	users := mustCreateSysopAndUsers(t, ts.st, "Alice", "Bob")
	must(ts.st.SendMessage(users[2].ID, users[1].ID, "Recipe", "Two cups of flour.", 0))

	a := dial(t, ts.addr, "alice")
	a.login("alice", "secret12")
	a.expect("Read them now?")
	a.key("Y")
	a.expect("[E]mail me")
	a.key("E")
	a.expect("Add and verify an email address in Settings first.")
	a.key(" ")

	code := must(ts.st.StartEmailVerification(users[1].ID, "alice@example.com"))
	must(ts.st.ConfirmEmail(users[1].ID, code))
	a.expect("[E]mail me")
	a.key("E")
	a.expect("On its way to alice@example.com.")
	got := mail.waitFor(t, 1)[0]
	if got.To != "alice@example.com" || !strings.Contains(got.Body, "Two cups of flour.") || !strings.Contains(got.Body, "From: Bob") {
		t.Fatalf("copy = %+v", got)
	}
}

func TestEmailOffWithoutMailServer(t *testing.T) {
	addr, st := startServer(t, Config{})
	mustCreateSysopAndUsers(t, st, "Alice")
	a := dial(t, addr, "alice")
	a.enter("alice")
	a.key("S")
	a.expect("Email")
	a.expect("(not available)")
	a.key("E")
	a.expect("no mail server set up")
}

func TestVerificationCodesLimitedPerAddress(t *testing.T) {
	mail := &fakeMail{}
	ts := startServerWith(t, Config{Mail: mail}, func(s *Server) { s.limit.emailTarget = newRateLimiter(1, time.Hour) })
	mustCreateSysopAndUsers(t, ts.st, "Alice", "Bob")
	for i, name := range []string{"alice", "bob"} {
		c := dial(t, ts.addr, name)
		c.enter(name)
		c.key("S")
		c.expect("] Settings")
		c.key("E")
		c.expect("[S]et address")
		c.key("S")
		c.expect("Email address:")
		c.line("victim@example.com")
		if i == 0 {
			c.expect("A code is on its way")
		} else {
			c.expect("Enough codes have gone out for now.")
		}
	}
	if n := len(mail.all()); n != 1 {
		t.Fatalf("sent %d codes to one address", n)
	}
}
