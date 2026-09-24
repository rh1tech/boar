package store

import (
	"errors"
	"testing"
	"time"
)

func TestValidateEmail(t *testing.T) {
	for _, ok := range []string{"kasia@example.com", "a.b+tag@mail.example.org"} {
		if err := ValidateEmail(ok); err != nil {
			t.Errorf("ValidateEmail(%q) = %v", ok, err)
		}
	}
	bad := []string{"", "nope", "a@b", "Kasia <k@example.com>", "k@example.com\r\nBcc: x@evil.com", "a b@example.com", "\"x\"@example.com"}
	for _, addr := range bad {
		var ie *InputError
		if err := ValidateEmail(addr); !errors.As(err, &ie) {
			t.Errorf("ValidateEmail(%q) = %v, want InputError", addr, err)
		}
	}
}

func TestEmailVerificationFlow(t *testing.T) {
	s, _ := openTemp(t)
	a := mustUser(t, s, "Alice")

	if _, err := s.ConfirmEmail(a.ID, "123456"); err == nil {
		t.Fatal("confirmed without a pending address")
	}
	code := must(s.StartEmailVerification(a.ID, " alice@example.com "))
	if len(code) != emailCodeDigits {
		t.Fatalf("code = %q", code)
	}
	if addr, ok := must2(s.PendingEmail(a.ID)); !ok || addr != "alice@example.com" {
		t.Fatalf("pending = %q %v", addr, ok)
	}
	if _, err := s.ConfirmEmail(a.ID, "nope"); err == nil || err.Error() != "Wrong code." {
		t.Fatalf("wrong code err = %v", err)
	}
	u, err := s.ConfirmEmail(a.ID, code)
	if err != nil || u.Email != "alice@example.com" || u.EmailMode != EmailNotice {
		t.Fatalf("confirm = %+v, %v", u, err)
	}
	if _, ok := must2(s.PendingEmail(a.ID)); ok {
		t.Fatal("pending should be cleared")
	}

	u = must(s.SetEmailMode(a.ID, EmailCopy))
	if u.EmailMode != EmailCopy || u.EmailMode.String() != "full copy" {
		t.Fatalf("mode = %v", u.EmailMode)
	}
	if _, err := s.SetEmailMode(a.ID, EmailMode(9)); err == nil {
		t.Fatal("bogus mode accepted")
	}
	u = must(s.RemoveEmail(a.ID))
	if u.Email != "" || u.EmailMode != EmailOff {
		t.Fatalf("after remove = %+v", u)
	}
	if _, err := s.SetEmailMode(a.ID, EmailNotice); err == nil {
		t.Fatal("mode set without an address")
	}
}

func TestEmailCodeExpiresAndLocksAfterGuesses(t *testing.T) {
	s, _ := openTemp(t)
	a := mustUser(t, s, "Alice")
	now := time.Now()
	s.now = func() time.Time { return now }

	code := must(s.StartEmailVerification(a.ID, "alice@example.com"))
	for range maxEmailCodeTries {
		if _, err := s.ConfirmEmail(a.ID, "000000x"); err == nil {
			t.Fatal("wrong code accepted")
		}
	}
	if _, err := s.ConfirmEmail(a.ID, code); err == nil || err.Error() != "That code has expired. Ask for a new one." {
		t.Fatalf("code should be dead after too many guesses, err = %v", err)
	}

	code = must(s.StartEmailVerification(a.ID, "alice@example.com"))
	now = now.Add(EmailCodeTTL + time.Second)
	if _, ok := must2(s.PendingEmail(a.ID)); ok {
		t.Fatal("expired code still pending")
	}
	if _, err := s.ConfirmEmail(a.ID, code); err == nil {
		t.Fatal("expired code accepted")
	}
	if u := must(s.UserByID(a.ID)); u.Email != "" {
		t.Fatalf("email set despite failure: %q", u.Email)
	}
}

func TestMigrationUpgradesExistingDatabase(t *testing.T) {
	s, path := openTemp(t)
	a := mustUser(t, s, "Alice")
	// Roll the schema back to v1 as an older build would have left it.
	for _, stmt := range []string{
		"DROP TABLE ssh_keys",
		"DROP TABLE throttle_hits",
		"ALTER TABLE users DROP COLUMN is_validated",
		"DROP INDEX users_email",
		"DROP TABLE email_verifications",
		"ALTER TABLE users DROP COLUMN email_mode",
		"ALTER TABLE users DROP COLUMN email",
		"PRAGMA user_version = 1",
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	s.Close()

	s2, err := Open(Config{Path: path, KDFIterations: testIter})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	u := must(s2.UserByID(a.ID))
	if u.Handle != "Alice" || u.Email != "" {
		t.Fatalf("user after upgrade = %+v", u)
	}
	must(s2.StartEmailVerification(a.ID, "alice@example.com"))
}

func must2[A, B any](a A, b B, err error) (A, B) {
	if err != nil {
		panic(err)
	}
	return a, b
}

func TestVerifiedEmailBelongsToOneAccount(t *testing.T) {
	s, _ := openTemp(t)
	a := mustUser(t, s, "Alice")
	b := mustUser(t, s, "Bob")
	must(s.ConfirmEmail(a.ID, must(s.StartEmailVerification(a.ID, "shared@example.com"))))
	code := must(s.StartEmailVerification(b.ID, "SHARED@example.com"))
	if _, err := s.ConfirmEmail(b.ID, code); err == nil || err.Error() != "That address already belongs to another account." {
		t.Fatalf("err = %v", err)
	}
	if u := must(s.UserByID(b.ID)); u.Email != "" {
		t.Fatalf("bob got the address: %+v", u)
	}
}
