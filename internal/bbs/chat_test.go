package bbs

import (
	"strings"
	"testing"
)

func recv(t *testing.T, m *chatMember) string {
	t.Helper()
	select {
	case line := <-m.out:
		return line.text
	default:
		t.Fatalf("%s received nothing", m.handle)
		return ""
	}
}

func TestChatHubRoomsAndBlocking(t *testing.T) {
	h := newChatHub()
	alice := newChatMember(1, "Alice", nil)
	bob := newChatMember(2, "Bob", map[int64]bool{3: true})
	carol := newChatMember(3, "Carol", nil)

	h.join(alice, "main")
	h.join(bob, "main")
	if !strings.Contains(recv(t, alice), "Alice") || !strings.Contains(recv(t, alice), "Bob") {
		t.Fatal("join announcements missing")
	}
	recv(t, bob) // Bob's own join
	h.join(carol, "main")
	recv(t, alice)
	recv(t, bob) // system lines ignore blocks
	recv(t, carol)

	h.say(carol, "hi from carol")
	if got := recv(t, alice); got != "hi from carol" {
		t.Fatalf("alice got %q", got)
	}
	select {
	case line := <-bob.out:
		t.Fatalf("bob blocked carol but got %q", line.text)
	default:
	}
	recv(t, carol) // speakers hear themselves

	if got := h.who("main"); strings.Join(got, ",") != "Alice,Bob,Carol" {
		t.Fatalf("who = %v", got)
	}
	h.join(carol, "retro")
	if counts := h.roomCounts(); counts["main"] != 2 || counts["retro"] != 1 || h.count() != 3 {
		t.Fatalf("counts = %v", counts)
	}
	if h.room(carol) != "retro" {
		t.Fatal("carol should be in retro")
	}
	h.leave(carol)
	h.leave(carol) // leaving twice is harmless
	if _, ok := h.roomCounts()["retro"]; ok {
		t.Fatal("empty rooms should disappear")
	}
}

func TestChatHubDropsLinesForSlowMembers(t *testing.T) {
	h := newChatHub()
	slow := newChatMember(1, "Slow", nil)
	fast := newChatMember(2, "Fast", nil)
	h.join(slow, "main")
	h.join(fast, "main")
	for range chatBacklog * 2 {
		h.say(fast, "spam") // must never block
	}
	if len(slow.out) != chatBacklog {
		t.Fatalf("backlog = %d", len(slow.out))
	}
}

func TestNormalizeRoom(t *testing.T) {
	cases := map[string]string{"#Retro": "retro", " a b!c ": "abc", "x-_1": "x-_1", strings.Repeat("z", 40): strings.Repeat("z", maxRoomNameLen), "!!!": ""}
	for in, want := range cases {
		if got := normalizeRoom(in); got != want {
			t.Errorf("normalizeRoom(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestChatBetweenTwoCallers(t *testing.T) {
	addr, st := startServer(t, Config{})
	mustCreateSysopAndUsers(t, st, "Alice", "Bob")

	a := dial(t, addr, "alice")
	a.enter("alice")
	a.key("C")
	a.expect("Commands:")
	a.expect("Alice joined #main")

	b := dial(t, addr, "bob")
	b.enter("bob")
	b.key("C")
	b.expect("Bob joined #main")
	a.expect("Bob joined #main")

	b.line("hello alice")
	a.expect("<Bob> hello alice")
	b.expect("<Bob> hello alice")

	a.line("/me waves")
	b.expect("* Alice waves")

	a.line("/who")
	a.expect("In #main: Alice, Bob")

	a.line("/join retro")
	a.expect("Alice joined #retro")
	b.expect("Alice left")
	a.line("/rooms")
	a.expect("#main (1)")
	a.expect("#retro (1)")

	a.line("/bogus")
	a.expect("Commands:")
	a.line("/join !!")
	a.expect("Usage: /join")

	// A page shows up in chat right away.
	c := dial(t, addr, "root")
	c.enter("root")
	c.key("P")
	c.expect("Page who")
	c.line("bob")
	c.expect("Message:")
	c.line("dinner is ready")
	c.expect("Paged Bob.")
	b.expect("*** Page from Root: dinner is ready ***")

	b.line("/q")
	b.expect("] Main")
	a.line("/quit")
	a.expect("] Main")
}

func TestPagingRules(t *testing.T) {
	addr, st := startServer(t, Config{})
	users := mustCreateSysopAndUsers(t, st, "Alice", "Bob")
	if err := st.Block(users[2].ID, users[1].ID); err != nil { // Bob blocks Alice
		t.Fatal(err)
	}

	b := dial(t, addr, "bob")
	b.enter("bob")

	a := dial(t, addr, "alice")
	a.enter("alice")
	a.key("P")
	a.expect("Page who")
	a.line("nobody")
	a.expect("nobody isn't online.")
	a.key(" ")
	a.expect("] Main")
	a.key("P")
	a.expect("Page who")
	a.line("alice")
	a.expect("Talking to yourself?")
	a.key(" ")
	a.expect("] Main")
	a.key("P")
	a.expect("Page who")
	a.line("1") // node 1 is Bob
	a.expect("Bob isn't accepting pages from you.")
}
