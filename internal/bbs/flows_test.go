// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"strings"
	"testing"
)

func TestSettingsPasswordLocationAndTerminal(t *testing.T) {
	addr, st := startServer(t, Config{})
	mustCreateSysopAndUsers(t, st, "Alice")

	c := dial(t, addr, "alice")
	c.enter("alice")
	c.key("S")
	c.expect("] Settings")

	c.key("L")
	c.expect("New location:")
	c.line("Gdansk")
	c.expect("(Gdansk)")
	c.expect("] Settings")

	c.key("P")
	c.expect("Current password:")
	c.line("wrong!!")
	c.expect("New password:")
	c.line("brandnew1")
	c.expect("New password again:")
	c.line("brandnew1")
	c.expect("Current password is incorrect.")
	c.key(" ")
	c.expect("] Settings")

	c.key("P")
	c.expect("Current password:")
	c.line("secret12")
	c.expect("New password:")
	c.line("brandnew1")
	c.expect("New password again:")
	c.line("brandnew2")
	c.expect("Passwords don't match.")
	c.key(" ")
	c.expect("] Settings")

	c.key("P")
	c.expect("Current password:")
	c.line("secret12")
	c.expect("New password:")
	c.line("brandnew1")
	c.expect("New password again:")
	c.line("brandnew1")
	c.expect("Password changed.")
	c.key(" ")
	c.expect("(ASCII, no color)")
	c.key("T")
	c.expect("Choice [1]:")
	c.line("1")
	c.expect("UTF-8 + ANSI color")
	c.expect("\x1b[")

	if _, err := st.Authenticate("alice", "brandnew1"); err != nil {
		t.Fatalf("new password rejected: %v", err)
	}
	if u := must(st.UserByHandle("alice")); u.Location != "Gdansk" {
		t.Fatalf("location = %q", u.Location)
	}
}

func TestEditorCommandsOutboxAndReadReceipts(t *testing.T) {
	addr, st := startServer(t, Config{})
	users := mustCreateSysopAndUsers(t, st, "Alice", "Bob")
	bob := users[2]

	c := dial(t, addr, "alice")
	c.enter("alice")
	c.key("M")
	c.expect("] Mail")

	// Unknown recipient, then the user list, then Bob.
	c.key("S")
	c.expect("Enter = cancel):")
	c.line("nobody, bob")
	c.expect("Nobody here goes by nobody.")
	c.line("?")
	c.expect("Bob")
	c.line("bob")
	c.expect("Subject")
	c.line("Draft")
	c.expect("  1:")
	c.line("/s")
	c.expect("Nothing to send yet.")
	c.line("first")
	c.expect("  2:")
	c.line("oops")
	c.expect("  3:")
	c.line("/d")
	c.expect("Line 2 deleted.")
	c.line("/l")
	c.expect("  1: first")
	c.line("/?")
	c.expect("/A abort")
	c.line("/c")
	c.expect("Clear the whole message?")
	c.key("N")
	c.line("/a")
	c.expect("Discard this message?")
	c.key("Y")
	c.expect("Message discarded.")
	c.key(" ")
	c.expect("] Mail")
	if n := len(must(st.Inbox(bob.ID))); n != 0 {
		t.Fatalf("aborted message was sent (%d)", n)
	}

	c.key("S")
	c.expect("Enter = cancel):")
	c.line("bob")
	c.expect("Subject")
	c.line("Real one")
	c.writeBody("body text")
	c.expect("Message sent to Bob.")
	c.key(" ")
	c.expect("] Mail")

	// Unread: no receipt yet.
	c.key("O")
	c.expect("Real one")
	c.expect("Read message (1-1")
	c.line("9")
	c.expect("No such number.")
	c.line("1")
	c.expect("To   : Bob")
	c.expect("Read : not yet")
	c.expect("body text")
	c.expect("[Q] Quit")
	c.key("Q")
	c.expect("Read message (1-1")

	// Bob reads it; the outbox now shows a receipt.
	m := must(st.Outbox(users[1].ID))[0]
	if err := st.MarkRead(m.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	c.line("1")
	c.expect("Read : ")
	c.expect("[Q] Quit")
	c.key("Q")
	c.expect("Read message (1-1")
	c.line("")
	c.expect("] Mail")
	c.key("Q")
	c.expect("] Main")
	c.key("U")
	c.expect("Bob")
	c.expect("press any key")
	c.key(" ")
	c.logoff()
}

func TestMultipleRecipientsSearchThreadAndForward(t *testing.T) {
	addr, st := startServer(t, Config{})
	users := mustCreateSysopAndUsers(t, st, "Alice", "Bob", "Carol")
	alice, bob, carol := users[1], users[2], users[3]

	first := must(st.SendMessage(bob.ID, alice.ID, "Modem meetup", "Bring your 14.4k", 0))
	must(st.SendMessage(alice.ID, bob.ID, "Re: Modem meetup", "Will do", first.ID))

	c := dial(t, addr, "alice")
	c.login("alice", "secret12")
	c.expect("Read them now?")
	c.key("N")
	c.skipWall()

	// One message, two recipients.
	c.sendMail("bob, carol", "Party", "Saturday!")
	if len(must(st.Inbox(bob.ID))) != 2 || len(must(st.Inbox(carol.ID))) != 1 {
		t.Fatal("both recipients should get a copy")
	}

	// Only sysops may mail everyone.
	c.key("M")
	c.expect("] Mail")
	c.key("S")
	c.expect("Enter = cancel):")
	c.line("all")
	c.expect("Only sysops can mail everyone.")
	c.line("")
	c.expect("] Mail")

	// Search finds the thread; T shows both sides of it.
	c.key("F")
	c.expect("Search for")
	c.line("modem")
	c.expect("< Bob") // « in ASCII
	c.expect("> Bob") // » in ASCII
	c.expect("Read message (1-2")
	c.line("1")
	c.expect("Bring your 14.4k")
	c.key("T")
	c.expect("Thread . 1 of 2")
	c.key("N")
	c.expect("Thread . 2 of 2")
	c.expect("Will do")
	c.key("Q")
	c.expect("Search: modem . 1 of 2")

	// Forward the first message to Carol.
	c.key("F")
	c.expect("Enter = cancel):")
	c.line("carol")
	c.expect("Subject [Fwd: Modem meetup]:")
	c.line("")
	c.expect("----- Forwarded message -----")
	c.expect("Bring your 14.4k")
	c.line("FYI")
	c.line("/s")
	c.expect("Message sent to Carol.")

	fwd := must(st.Inbox(carol.ID))
	last := fwd[len(fwd)-1]
	if last.Subject != "Fwd: Modem meetup" || !strings.Contains(last.Body, "From: Bob") || !strings.HasSuffix(last.Body, "FYI") {
		t.Fatalf("forwarded = %q / %q", last.Subject, last.Body)
	}
}

func TestBlockedCallerCannotMail(t *testing.T) {
	addr, st := startServer(t, Config{})
	users := mustCreateSysopAndUsers(t, st, "Alice", "Bob")
	alice := users[1]

	a := dial(t, addr, "alice")
	a.enter("alice")
	a.key("S")
	a.expect("(0 callers)")
	a.key("B")
	a.expect("[B] Block")
	a.key("B")
	a.expect("Handle:")
	a.line("bob")
	a.expect("Bob")
	a.expect("[B] Block")
	a.key("Q")
	a.expect("(1 caller)")

	b := dial(t, addr, "bob")
	b.enter("bob")
	b.key("M")
	b.expect("] Mail")
	b.key("S")
	b.expect("Enter = cancel):")
	b.line("alice")
	b.expect("Subject")
	b.line("Hi")
	b.writeBody("hello?")
	b.expect("Alice: That caller is not accepting your mail.")
	if n := len(must(st.Inbox(alice.ID))); n != 0 {
		t.Fatalf("blocked mail delivered: %d", n)
	}

	// Unblock and it goes through.
	a.key("B")
	a.expect("[B] Block")
	a.key("U")
	a.expect("Handle:")
	a.line("bob")
	a.expect("Nobody is blocked.")
	if blocked := must(st.IsBlocked(alice.ID, users[2].ID)); blocked {
		t.Fatal("still blocked")
	}
}
