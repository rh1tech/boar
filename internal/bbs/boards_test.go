// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import "testing"

func TestBoardsPostReplyAndNewScan(t *testing.T) {
	addr, st := startServer(t, Config{})
	users := mustCreateSysopAndUsers(t, st, "Alice", "Bob")
	alice, bob := users[1], users[2]
	general := must(st.Boards(alice.ID))[1]

	a := dial(t, addr, "alice")
	a.enter("alice")
	a.key("B")
	a.expect("Message Boards")
	a.expect("Announcements")
	a.expect("General")
	a.expect("Board #")

	// Announcements is sysop-only.
	a.line("1")
	a.expect("Read #")
	a.line("p")
	a.expect("Only the sysop posts on Announcements.")
	a.key(" ")
	a.expect("Read #")
	a.line("")
	a.expect("Board #")

	a.line("2")
	a.expect("No posts yet.")
	a.line("p")
	a.expect("Subject")
	a.line("Modems")
	a.writeBody("56k or bust")
	a.expect("Posted to General.")
	a.key(" ")
	a.expect("Modems")
	a.expect("Read #")
	a.line("")
	a.expect("Board #")

	// Bob sees it as new, reads it, and replies.
	b := dial(t, addr, "bob")
	b.enter("bob")
	b.key("B")
	b.expect("(1 new)")
	b.line("n")
	b.expect("By   : Alice")
	b.expect("56k or bust")
	b.key("R")
	b.expect("Quote the post?")
	b.key("N")
	b.expect("Subject [Re: Modems]:")
	b.line("")
	b.writeBody("14.4k forever")
	b.expect("Posted to General.")
	b.key(" ")
	b.expect("[Q] Quit")
	b.key("Q")
	b.expect("Board #")

	posts := must(st.Posts(general.ID))
	if len(posts) != 2 || posts[1].ReplyTo != posts[0].ID || posts[1].AuthorID != bob.ID {
		t.Fatalf("posts = %+v", posts)
	}
	if sum := must(st.Boards(bob.ID))[1]; sum.New != 0 {
		t.Fatalf("bob still has %d new", sum.New)
	}

	// Alice can't delete Bob's post, but can delete her own.
	a.line("2")
	a.expect("Re: Modems")
	a.expect("Read #")
	a.line("2")
	a.expect("14.4k forever")
	a.expect("[M] Mail author")
	a.key("D") // not offered: ignored
	a.key("P")
	a.expect("56k or bust")
	a.expect("[D] Delete")
	a.key("D")
	a.expect("Delete this post for everyone?")
	a.key("Y")
	a.expect("14.4k forever") // moved on to the remaining post
	a.key("Q")
	if n := len(must(st.Posts(general.ID))); n != 1 {
		t.Fatalf("posts left = %d", n)
	}
}

func TestNewScanWithNothingNew(t *testing.T) {
	addr, st := startServer(t, Config{})
	mustCreateSysopAndUsers(t, st, "Alice")
	a := dial(t, addr, "alice")
	a.enter("alice")
	a.key("B")
	a.expect("Board #")
	a.line("n")
	a.expect("Nothing new on the boards.")
}
