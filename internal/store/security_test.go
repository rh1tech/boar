package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestApprovalGatesWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "boar.db")
	s, err := Open(Config{Path: path, KDFIterations: testIter, ApproveNewUsers: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root := mustUser(t, s, "Root")
	a := mustUser(t, s, "Alice")
	b := mustUser(t, s, "Bob")
	if !root.Validated || a.Validated {
		t.Fatalf("validated: root=%v alice=%v", root.Validated, a.Validated)
	}
	if _, err := s.SendMessage(a.ID, b.ID, "hi", "hi", 0); !errors.Is(err, ErrNotValidated) {
		t.Fatalf("mail to a caller err = %v", err)
	}
	must(s.SendMessage(a.ID, root.ID, "please approve", "hi", 0)) // the sysop is always reachable
	general := must(s.Boards(a.ID))[1]
	if _, err := s.AddPost(general.ID, a.ID, "s", "b", 0); !errors.Is(err, ErrNotValidated) {
		t.Fatalf("post err = %v", err)
	}
	if _, err := s.AddOneliner(a.ID, "spam"); !errors.Is(err, ErrNotValidated) {
		t.Fatalf("oneliner err = %v", err)
	}
	pending := must(s.PendingUsers())
	if len(pending) != 2 || pending[0].Handle != "Alice" {
		t.Fatalf("pending = %+v", pending)
	}
	if err := s.ValidateUser(b.ID, a.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-sysop validate err = %v", err)
	}
	if err := s.ValidateUser(root.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	must(s.SendMessage(a.ID, b.ID, "hi", "hi", 0))
	if err := s.SetSysop(root.ID, b.ID, true); err != nil {
		t.Fatal(err)
	}
	if u := must(s.UserByID(b.ID)); !u.Validated {
		t.Fatal("promoting should approve too")
	}
	if ids := must(s.SysopIDs()); len(ids) != 2 {
		t.Fatalf("sysops = %v", ids)
	}
}

func TestOpenRegistrationValidatesEveryone(t *testing.T) {
	s, _ := openTemp(t)
	mustUser(t, s, "Root")
	if u := mustUser(t, s, "Alice"); !u.Validated {
		t.Fatal("open registration should validate new users")
	}
}

func TestThrottleHitsPersist(t *testing.T) {
	s, _ := openTemp(t)
	now := time.Now()
	for _, at := range []time.Time{now.Add(-72 * time.Hour), now.Add(-time.Minute), now} {
		if err := s.RecordHit("login", "1.2.3.4", at); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordHit("login", "other", now); err != nil {
		t.Fatal(err)
	}
	hits := must(s.LoadHits("login", now.Add(-time.Hour)))
	if len(hits["1.2.3.4"]) != 2 || len(hits["other"]) != 1 {
		t.Fatalf("hits = %v", hits)
	}
	if err := s.ClearHits("login", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	if hits := must(s.LoadHits("login", now.Add(-time.Hour))); len(hits["1.2.3.4"]) != 0 {
		t.Fatalf("cleared hits = %v", hits)
	}
}

func TestSSHKeys(t *testing.T) {
	s, _ := openTemp(t)
	a := mustUser(t, s, "Alice")
	b := mustUser(t, s, "Bob")
	k := must(s.AddSSHKey(a.ID, "SHA256:aaa", "ssh-ed25519 AAAA", "laptop\x1b"))
	if k.Comment != "laptop" {
		t.Fatalf("comment = %q", k.Comment)
	}
	if _, err := s.AddSSHKey(b.ID, "SHA256:aaa", "ssh-ed25519 AAAA", ""); err == nil {
		t.Fatal("same key on two accounts")
	}
	if u := must(s.UserBySSHKey("SHA256:aaa")); u.ID != a.ID {
		t.Fatalf("owner = %+v", u)
	}
	for i := 1; i < MaxSSHKeys; i++ {
		must(s.AddSSHKey(a.ID, "SHA256:k"+string(rune('a'+i)), "x", ""))
	}
	if _, err := s.AddSSHKey(a.ID, "SHA256:overflow", "x", ""); err == nil {
		t.Fatal("key limit not enforced")
	}
	if err := s.DeleteSSHKey(b.ID, k.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting someone else's key err = %v", err)
	}
	if err := s.DeleteSSHKey(a.ID, k.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UserBySSHKey("SHA256:aaa"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted key still works: %v", err)
	}
	if err := s.DeleteUser(a.ID, a.ID); !errors.Is(err, ErrForbidden) {
		t.Fatal("self delete")
	}
}
