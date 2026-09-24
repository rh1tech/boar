package bbs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"boar/internal/doors"
)

func mustDoors(t *testing.T, list ...doors.Door) []doors.Door {
	t.Helper()
	valid, err := doors.Validate(list)
	if err != nil {
		t.Fatal(err)
	}
	return valid
}

func shDoor(key, script string, extra ...string) doors.Door {
	return doors.Door{Key: key, Name: "Door " + key, Command: append([]string{"/bin/sh", "-c", script, key}, extra...)}
}

// enterDoors logs in (UTF-8, color) and opens the doors menu.
func enterDoors(t *testing.T, addr string) *client {
	t.Helper()
	c := dial(t, addr, "alice")
	c.expect("Choice [1]:")
	c.line("1")
	c.expect("Handle")
	c.line("alice")
	c.expect("Password") // color codes follow, before the colon
	c.line("secret12")
	c.expect("No new mail.")
	c.key(" ")
	c.expect("Add a oneliner?")
	c.key("N")
	c.expect("Main")
	c.key("D")
	c.expect("Doors") // the title bar; the list follows
	return c
}

func TestDoorGetsDropFileAndCP437(t *testing.T) {
	// The door greets the caller by the DOOR.SYS alias, draws a CP437 block,
	// then reports the bytes it received as hex.
	script := `alias=$(sed -n 36p "$1" | tr -d '\r'); printf 'Hi %s \333\r\n' "$alias"; x=$(head -c 2); printf 'got:'; printf '%s' "$x" | od -An -tx1 | tr -s ' '`
	dir := t.TempDir()
	ts := startServerWith(t, Config{Doors: mustDoors(t, shDoor("hello", script, "{doorsys}")), DoorsDir: dir}, nil)
	mustCreateSysopAndUsers(t, ts.st, "Alice")

	c := enterDoors(t, ts.addr)
	c.expect("Door hello")
	c.expect("Open door")
	c.line("1")
	c.expect("Hi Alice █") // CP437 0xDB arrives as UTF-8
	c.key("éA")            // UTF-8 é must reach the door as CP437 0x82
	c.expect("got: 82 41")
	c.expect("Back from Door hello")
	c.key(" ")
	c.expect("Open door")

	if _, err := os.Stat(filepath.Join(dir, "node1", doors.DoorSys)); err != nil {
		t.Fatalf("drop file missing: %v", err)
	}
	if opened := must(ts.st.Events(eventDoor, 5)); len(opened) != 1 || opened[0].Detail != "opened hello" {
		t.Fatalf("door events = %+v", opened)
	}
}

func TestDoorTimeLimit(t *testing.T) {
	slow := shDoor("slow", "echo waiting; sleep 30")
	slow.MaxMinutes = 1
	ts := startServerWith(t, Config{Doors: mustDoors(t, slow), DoorsDir: t.TempDir()},
		func(s *Server) { s.doorMinute = 200 * time.Millisecond })
	mustCreateSysopAndUsers(t, ts.st, "Alice")
	c := enterDoors(t, ts.addr)
	c.line("1")
	c.expect("waiting")
	c.expect("Time's up in Door slow.")
}

func TestSingleNodeDoorIsBusy(t *testing.T) {
	d := shDoor("solo", "echo hi")
	d.SingleNode = true
	ts := startServerWith(t, Config{Doors: mustDoors(t, d), DoorsDir: t.TempDir()}, nil)
	mustCreateSysopAndUsers(t, ts.st, "Alice")
	lock := ts.srv.doorLock("solo")
	lock.Lock() // someone else is playing
	defer lock.Unlock()
	c := enterDoors(t, ts.addr)
	c.line("1")
	c.expect("Someone else is in Door solo right now.")
}

func TestBrokenDoorAndSysopOnlyDoors(t *testing.T) {
	broken := doors.Door{Key: "broken", Name: "Broken", Command: []string{"/nonexistent/door"}}
	secret := shDoor("secret", "echo sysop stuff")
	secret.SysopOnly = true
	ts := startServerWith(t, Config{Doors: mustDoors(t, broken, secret), DoorsDir: t.TempDir()}, nil)
	mustCreateSysopAndUsers(t, ts.st, "Alice")
	c := enterDoors(t, ts.addr)
	if listing := c.expect("Open door"); !strings.Contains(listing, "Broken") || strings.Contains(listing, "Door secret") {
		t.Fatalf("doors listing = %q", listing)
	}
	c.line("1")
	c.expect("That door is stuck shut.")
}

func TestHangingUpInsideADoorEndsIt(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "still-running")
	d := shDoor("trap", `echo trapped; sleep 2; touch "$1"`, marker)
	ts := startServerWith(t, Config{Doors: mustDoors(t, d), DoorsDir: t.TempDir()}, nil)
	mustCreateSysopAndUsers(t, ts.st, "Alice")
	c := enterDoors(t, ts.addr)
	c.line("1")
	c.expect("trapped")
	c.c.Close()
	time.Sleep(2500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("door kept running after the caller hung up")
	}
}

func TestBoarHuntExampleDoor(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the example door")
	}
	bin := filepath.Join(t.TempDir(), "boarhunt")
	build := exec.Command("go", "build", "-o", bin, "boar/cmd/boar-door-example")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build example door: %v\n%s", err, out)
	}
	d := doors.Door{Key: "boarhunt", Name: "Boar Hunt", Command: []string{bin, "-dropfile", "{doorsys}"}}
	ts := startServerWith(t, Config{Doors: mustDoors(t, d), DoorsDir: t.TempDir()}, nil)
	mustCreateSysopAndUsers(t, ts.st, "Alice")
	c := enterDoors(t, ts.addr)
	c.line("1")
	c.expect("BOAR HUNT")
	c.expect("Welcome, \x1b[1;37mAlice")
	c.line("zz")
	c.expect("not a tree")
	for range 7 { // the boar is almost never at tree 1; either way the round ends
		c.line("1")
		if strings.Contains(c.expect("which tree?"), "found the boar") {
			break
		}
	}
	c.expect("Hunt again?")
	c.line("n")
	c.expect("Good hunting, Alice")
	c.expect("Back from Boar Hunt")
}
