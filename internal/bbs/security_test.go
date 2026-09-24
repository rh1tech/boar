// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"boar/internal/store"
)

func TestNewCallerWaitsForApproval(t *testing.T) {
	ts := startServerFull(t, Config{}, store.Config{ApproveNewUsers: true}, nil)
	users := mustCreateSysopAndUsers(t, ts.st, "Bob")
	if err := ts.st.ValidateUser(users[0].ID, users[1].ID); err != nil { // Bob is an old hand
		t.Fatal(err)
	}

	root := dial(t, ts.addr, "root")
	root.enter("root")

	c := dial(t, ts.addr, "kasia")
	c.connectPlain()
	c.line("new")
	c.expect("Choose a handle:")
	c.line("Kasia")
	c.expect("Password:")
	c.line("boarpass1")
	c.expect("Password again:")
	c.line("boarpass1")
	c.expect("Location")
	c.line("")
	c.expect("Create account Kasia?")
	c.key("Y")
	c.expect("waiting for sysop approval")
	c.expect("press any key")
	c.key(" ")
	c.expect("Oneliner Wall") // shown, but no offer to write while waiting
	c.expect("press any key")
	c.key(" ")
	if menu := c.expect("] Main"); !strings.Contains(menu, "waiting for sysop approval") {
		t.Fatal("main menu should say the account is waiting")
	}

	// Everything that writes is closed, except mail to the sysop.
	for _, key := range []string{"C", "P", "D"} {
		c.key(key)
		c.expect("opens up once a sysop approves your account.")
		c.key(" ")
		c.expect("] Main")
	}
	c.key("M")
	c.expect("] Mail")
	c.key("S")
	c.expect("Enter = cancel):")
	c.line("bob")
	c.expect("you can only mail the sysop.")
	c.line("root")
	c.expect("Subject")
	c.line("Hello")
	c.writeBody("Please let me in.")
	c.expect("Message sent to Root.")
	c.key(" ")
	c.key("Q")
	c.expect("] Main")

	// The sysop was told, and approves from the queue.
	root.key("W")
	root.expect("press any key")
	root.key(" ")
	root.expect("(1 waiting)") // the menu, then its pending notices
	root.expect("New caller Kasia is waiting for approval")
	root.key("!")
	root.expect("New callers")
	root.key("V")
	root.expect("Kasia")
	root.line("1")
	root.expect("Approved  : NOT YET")
	root.key("A")
	root.expect("Nobody is waiting for approval.")
	c.key("W")
	c.expect("press any key")
	c.key(" ")
	c.expect("A sysop approved your account.")

	u := must(ts.st.UserByHandle("kasia"))
	if !u.Validated {
		t.Fatal("not approved")
	}
	if audit := must(ts.st.Events(store.EventSysop, 1)); len(audit) != 1 || audit[0].Detail != "approved Kasia" {
		t.Fatalf("audit = %+v", audit)
	}
}

func TestDailySignupCap(t *testing.T) {
	ts := startServerWith(t, Config{MaxSignupsPerDay: 1}, nil)
	ts.srv.limit.signupsAll.hit(signupsAllKey)
	c := dial(t, ts.addr, "late")
	c.connectPlain()
	c.line("new")
	c.expect("We've had a lot of new callers today.")
}

func newSSHKey(t *testing.T) (ssh.Signer, string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signer, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) + " kasia@laptop"
}

func TestSSHKeySignsInWithoutPassword(t *testing.T) {
	addr, st, _, hostKey := startSSHServerFull(t)
	users := mustCreateSysopAndUsers(t, st, "Alice")
	signer, line := newSSHKey(t)

	// Add the key through Settings, like a caller would.
	c := dialSSH(t, addr, hostKey, "dumb")
	c.expect("Handle")
	c.send("alice\r")
	c.expect("Password:")
	c.send("secret12\r")
	c.expect("No new mail.")
	c.send(" ")
	c.expect("Add a oneliner?")
	c.send("n")
	c.expect("] Main")
	c.send("S")
	c.expect("SSH keys")
	c.send("K")
	c.expect("No keys yet.")
	c.send("A")
	c.expect("Public key:")
	c.send("ssh-dss AAAAnotakey\r")
	c.expect("doesn't look like a public key")
	c.send(" ")
	c.send("A")
	c.expect("Public key:")
	c.send(line + "\r")
	c.expect("Key added: SHA256:")
	c.send(" ")
	c.expect("kasia@laptop")

	// Now the key alone gets Alice in.
	k := dialSSHWith(t, addr, hostKey, "dumb", ssh.PublicKeys(signer))
	k.expect("Signed in with your SSH key as Alice.")
	k.send(" ")
	k.expect("No new mail.")
	logins := must(st.Events(store.EventLogin, 1))
	if !strings.Contains(logins[0].Detail, "via ssh key") {
		t.Fatalf("login event = %+v", logins[0])
	}

	// A locked account can't use its key either.
	if err := st.SetLocked(users[0].ID, users[1].ID, true); err != nil {
		t.Fatal(err)
	}
	l := dialSSHWith(t, addr, hostKey, "dumb", ssh.PublicKeys(signer))
	l.expect("This account is locked.")
}

func TestUnknownSSHKeyFallsBackToLogin(t *testing.T) {
	addr, _, hostKey := startSSHServer(t)
	stranger, _ := newSSHKey(t)
	c := dialSSHWith(t, addr, hostKey, "dumb", ssh.PublicKeys(stranger), ssh.KeyboardInteractive(noAnswers))
	c.expect("Handle (or NEW to register):")
}

func TestParseSSHKeyRejectsWeakKeys(t *testing.T) {
	if _, _, err := parseSSHKey("ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAAAgQC7"); err == nil {
		t.Fatal("garbage accepted")
	}
	_, line := newSSHKey(t)
	if _, comment, err := parseSSHKey("  " + line + "  "); err != nil || comment != "kasia@laptop" {
		t.Fatalf("good key: %q %v", comment, err)
	}
}
