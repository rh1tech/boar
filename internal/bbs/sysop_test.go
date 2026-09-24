package bbs

import (
	"strings"
	"testing"

	"boar/internal/store"
)

// sysop logs in as Root (the first user, hence sysop) and opens the menu.
func sysop(t *testing.T, addr string) *client {
	t.Helper()
	c := dial(t, addr, "root")
	c.enter("root")
	c.key("!")
	c.expect("] Sysop")
	return c
}

func TestSysopMenuHiddenFromCallers(t *testing.T) {
	addr, st := startServer(t, Config{})
	mustCreateSysopAndUsers(t, st, "Alice")
	a := dial(t, addr, "alice")
	a.enter("alice")
	a.key("!") // not a valid key for Alice: ignored
	a.key("W")
	a.expect("Who's Online")
}

func TestSysopLocksOnlineUserAndResetsPassword(t *testing.T) {
	addr, st := startServer(t, Config{})
	users := mustCreateSysopAndUsers(t, st, "Alice")
	alice := users[1]

	a := dial(t, addr, "alice")
	a.enter("alice")

	s := sysop(t, addr)
	s.key("U")
	s.expect("Handle")
	s.line("nobody")
	s.expect("Not found.")
	s.key(" ")
	s.line("alice")
	s.expect("Online    : 1 node")
	s.expect("[L]ock/unlock")

	s.key("P")
	s.expect("New password for Alice:")
	s.line("reset123")
	s.expect("New password for Alice again:")
	s.line("reset123")
	s.expect("Password reset.")
	s.key(" ")
	must(st.Authenticate("alice", "reset123"))

	s.key("L")
	s.expect("Lock Alice?")
	s.key("Y")
	a.expect("Your account has been locked by the sysop.")
	s.expect("Locked    : LOCKED")
	if u := must(st.UserByID(alice.ID)); !u.Locked {
		t.Fatal("not locked")
	}

	s.key("L")
	s.expect("Unlock Alice?")
	s.key("Y")
	s.expect("Locked    : no")

	s.key("S")
	s.expect("Make Alice a sysop?")
	s.key("Y")
	s.expect("Sysop     : yes")
	s.key("Q")
	s.expect("] Sysop")

	s.key("E")
	s.expect("Event Log")
	s.expect("promoted Alice to sysop")
	s.expect("unlocked Alice")
	s.expect("locked Alice")
	s.expect("reset password of Alice")
}

func TestSysopCannotLockOrDeleteThemselves(t *testing.T) {
	addr, st := startServer(t, Config{})
	mustCreateSysopAndUsers(t, st)
	s := sysop(t, addr)
	s.key("U")
	s.expect("Handle")
	s.line("root")
	s.expect("[P]assword reset")
	s.key("D") // not offered for yourself: ignored
	s.key("L")
	s.key("Q")
	s.expect("] Sysop")
	if u := must(st.UserByHandle("root")); u.Locked {
		t.Fatal("sysop locked themselves")
	}
}

func TestSysopDeletesUser(t *testing.T) {
	addr, st := startServer(t, Config{})
	mustCreateSysopAndUsers(t, st, "Alice")
	s := sysop(t, addr)
	s.key("U")
	s.expect("Handle")
	s.line("alice")
	s.expect("[D]elete")
	s.key("D")
	s.expect("Type the handle to confirm:")
	s.line("alic")
	s.expect("Not deleted.")
	s.key(" ")
	s.expect("[D]elete")
	s.key("D")
	s.expect("Type the handle to confirm:")
	s.line("Alice")
	s.expect("Alice deleted.")
	s.key(" ")
	s.expect("] Sysop")
	if _, err := st.UserByHandle("alice"); err == nil {
		t.Fatal("user still exists")
	}
}

func TestSysopKickAndBroadcast(t *testing.T) {
	addr, st := startServer(t, Config{})
	mustCreateSysopAndUsers(t, st, "Alice", "Bob")
	a := dial(t, addr, "alice")
	a.enter("alice") // node 1
	b := dial(t, addr, "bob")
	b.enter("bob") // node 2

	s := sysop(t, addr) // node 3
	s.key("B")
	s.expect("Broadcast")
	s.line("Backup at midnight")
	s.expect("Sent to everyone online.")
	s.key(" ")
	b.key("W")
	b.expect("press any key")
	b.key(" ")
	b.expect("*** Sysop Root: Backup at midnight ***")

	s.key("K")
	s.expect("Node #")
	s.line("3")
	s.expect("That's you.")
	s.key(" ")
	s.key("K")
	s.expect("Node #")
	s.line("1")
	s.expect("Disconnect node 1?")
	s.key("Y")
	a.expect("You have been disconnected by the sysop.")
	s.expect("Node 1 disconnected.")
	s.key(" ")
	s.key("K")
	s.expect("Node #")
	s.line("9")
	s.expect("Disconnect node 9?")
	s.key("Y")
	s.expect("No such node.")
}

func TestSysopBoardsNewsAndOneliners(t *testing.T) {
	addr, st := startServer(t, Config{})
	users := mustCreateSysopAndUsers(t, st, "Alice")
	must(st.AddOneliner(users[1].ID, "boar power"))

	s := sysop(t, addr)
	s.key("M")
	s.expect("Manage Boards")
	s.key("C")
	s.expect("Board name:")
	s.line("Retro Gaming")
	s.expect("Description:")
	s.line("Doors and more")
	s.expect("Only sysops may post?")
	s.key("N")
	s.expect("Retro Gaming")
	s.key("D")
	s.expect("Delete board")
	s.line("3")
	s.expect("Delete Retro Gaming and its 0 posts?")
	s.key("Y")
	s.expect("[C]reate")
	s.key("Q")
	s.expect("] Sysop")

	s.key("N")
	s.expect("Manage News")
	s.key("A")
	s.expect("Title:")
	s.line("Welcome to Boar")
	s.writeBody("Be excellent to each other.")
	s.expect("Welcome to Boar")
	s.key("Q")
	s.expect("] Sysop")

	s.key("O")
	s.expect("boar power")
	s.line("1")
	s.expect("The wall is empty.")
	s.key(" ")
	s.expect("] Sysop")
	if n := len(must(st.Oneliners(10))); n != 0 {
		t.Fatalf("oneliners left = %d", n)
	}

	// Alice sees the news at login and adds a oneliner.
	a := dial(t, addr, "alice")
	a.login("alice", "secret12")
	a.expect("There is 1 new bulletin.")
	a.key("Y")
	a.expect("Welcome to Boar")
	a.line("1")
	a.expect("Be excellent to each other.")
	a.key(" ")
	a.expect("Read bulletin")
	a.line("")
	a.expect("No new mail.")
	a.key(" ")
	a.expect("Add a oneliner?")
	a.key("Y")
	a.line("hello wall")
	a.expect("] Main")
	a.key("L")
	a.expect("Last Callers")
	a.expect("Alice")
	a.expect("via telnet")

	wall := must(st.Oneliners(10))
	if len(wall) != 1 || wall[0].Text != "hello wall" {
		t.Fatalf("wall = %+v", wall)
	}
	boards := must(st.Boards(users[0].ID))
	if len(boards) != 2 {
		t.Fatalf("boards = %+v", boards)
	}
	var kinds []string
	for _, e := range must(st.Events(store.EventSysop, 10)) {
		kinds = append(kinds, e.Detail)
	}
	joined := strings.Join(kinds, "|")
	for _, want := range []string{"created board", "deleted board", "posted bulletin", "deleted oneliner"} {
		if !strings.Contains(joined, want) {
			t.Errorf("audit log missing %q: %s", want, joined)
		}
	}
}
