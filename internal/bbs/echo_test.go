// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"strings"
	"testing"
	"time"

	"boar/internal/ftn"
)

func TestEchoMenuVisibility(t *testing.T) {
	addr, st := startServer(t, Config{Name: "Test BBS", IdleTimeout: time.Minute})
	mustCreateSysopAndUsers(t, st, "Alice")
	for _, tag := range []string{"LINUX", "SPB.SYSOP"} {
		if _, err := st.ImportEcho(ftn.Message{
			Area: tag, MsgID: tag, From: "Test", To: "All",
			Subject: tag + " subject", Body: tag + " body", Date: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, handle := range []string{"Alice", "Root"} {
		t.Run(handle, func(t *testing.T) {
			c := dial(t, addr, handle)
			c.enter(handle)
			c.key("E")
			listing := c.expect("Area #")
			if !strings.Contains(listing, "LINUX") {
				t.Fatal("public echo missing")
			}
			if got := strings.Contains(listing, "SPB.SYSOP"); got != (handle == "Root") {
				t.Fatalf("restricted echo visibility for %s: %v", handle, got)
			}
			c.expect("> ")
			c.line("N")
			c.expect("LINUX body")
			c.key("N")
			if handle == "Root" {
				c.expect("SPB.SYSOP body")
				c.key("N")
			}
			scan := c.expect("Area #")
			if handle == "Alice" && strings.Contains(scan, "SPB.SYSOP") {
				t.Fatal("unread scan leaked restricted echo")
			}
			c.expect("> ")
			c.line("")
			c.expect("] Main")
			c.logoff()
		})
	}
}
