package doors

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain doubles as a fake tcp door: with BOAR_TEST_DOOR=tcp the test
// binary connects to the port it's given and plays a tiny echo game.
func TestMain(m *testing.M) {
	if os.Getenv("BOAR_TEST_DOOR") == "tcp" {
		fakeTCPDoor(os.Getenv("BOAR_PORT"))
		return
	}
	os.Exit(m.Run())
}

func fakeTCPDoor(port string) {
	c, err := net.Dial("tcp", "127.0.0.1:"+port)
	if err != nil {
		os.Exit(2)
	}
	defer c.Close()
	fmt.Fprint(c, "tcp door ready\r\n")
	buf := make([]byte, 3)
	if _, err := io.ReadFull(c, buf); err != nil {
		os.Exit(3)
	}
	fmt.Fprintf(c, "got %s\r\n", buf)
}

func TestValidate(t *testing.T) {
	good, err := Validate([]Door{{Key: "boar", Name: "Boar", Command: []string{"x"}}})
	if err != nil || good[0].IO != IOStdio || good[0].MaxMinutes != defaultMaxMinutes {
		t.Fatalf("defaults: %+v, %v", good, err)
	}
	bad := [][]Door{
		{{Key: "Bad Key", Name: "x", Command: []string{"x"}}},
		{{Key: "a", Name: "x", Command: []string{"x"}}, {Key: "a", Name: "y", Command: []string{"y"}}},
		{{Key: "a", Command: []string{"x"}}},
		{{Key: "a", Name: "x"}},
		{{Key: "a", Name: "x", Command: []string{"x"}, IO: "serial"}},
	}
	for i, doors := range bad {
		if _, err := Validate(doors); err == nil {
			t.Errorf("case %d accepted: %+v", i, doors)
		}
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	if doors, err := LoadConfig(filepath.Join(dir, "missing.json")); err != nil || doors != nil {
		t.Fatalf("missing file: %v, %v", doors, err)
	}
	path := filepath.Join(dir, "doors.json")
	if err := os.WriteFile(path, []byte(`[{"key":"boar","name":"Boar Hunt","command":["./boar"],"io":"tcp","max_minutes":5}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	doors, err := LoadConfig(path)
	if err != nil || len(doors) != 1 || doors[0].IO != IOTCP || doors[0].MaxMinutes != 5 {
		t.Fatalf("doors = %+v, %v", doors, err)
	}
	if err := os.WriteFile(path, []byte(`{nope`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("bad JSON accepted")
	}
}

func TestWriteDropFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "node3")
	now := time.Date(2026, 9, 24, 21, 5, 0, 0, time.UTC)
	c := Caller{Node: 3, UserID: 42, Handle: "Kasia K.", Location: "Kraków\r\nEvil", Calls: 7, MinutesLeft: 30,
		ScreenRows: 24, ANSI: true, BBSName: "Boar BBS", SysopName: "Root"}
	if err := WriteDropFiles(dir, c, now); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, DoorSys))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\r\n"), "\r\n")
	if len(lines) != 52 {
		t.Fatalf("DOOR.SYS has %d lines", len(lines))
	}
	checks := map[int]string{4: "3", 10: "Kasia K.", 11: "Krak\xa2wEvil", 14: "", 15: "100", 19: "30", 20: "GR", 26: "42", 36: "Kasia K."}
	for n, want := range checks {
		if lines[n-1] != want {
			t.Errorf("DOOR.SYS line %d = %q, want %q", n, lines[n-1], want)
		}
	}
	info, err := os.ReadFile(filepath.Join(dir, DorInfo))
	if err != nil {
		t.Fatal(err)
	}
	di := strings.Split(strings.TrimSuffix(string(info), "\r\n"), "\r\n")
	if len(di) != 13 || di[0] != "Boar BBS" || di[1] != "ROOT" || di[2] != "NLN" || di[6] != "KASIA" || di[7] != "K." || di[11] != "30" {
		t.Fatalf("DORINFO1.DEF = %q", di)
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 {
		t.Errorf("drop dir mode = %v", st.Mode().Perm())
	}
}

func run(t *testing.T, d Door, vars Vars, input string) string {
	t.Helper()
	p, err := Start(context.Background(), d, vars)
	if err != nil {
		t.Fatal(err)
	}
	if input != "" {
		if _, err := p.Write([]byte(input)); err != nil {
			t.Fatal(err)
		}
	}
	out, _ := io.ReadAll(p)
	<-p.Done()
	return string(out)
}

func TestStdioDoor(t *testing.T) {
	d := must(Validate([]Door{{Key: "sh", Name: "Shell", Command: []string{
		"/bin/sh", "-c", `printf 'hello %s node %s\n' "$1" "$BOAR_NODE"; x=$(head -c 3); printf 'got %s\n' "$x"; echo oops >&2`,
		"door", "{handle}",
	}}}))[0]
	out := run(t, d, Vars{"handle": "Kasia K.", "node": "3"}, "abc")
	if out != "hello Kasia K. node 3\ngot abc\n" {
		t.Fatalf("output = %q", out)
	}
}

func TestDoorEnvironmentIsMinimal(t *testing.T) {
	t.Setenv("BOAR_SMTP_PASSWORD", "hunter22")
	d := must(Validate([]Door{{Key: "env", Name: "Env", Command: []string{"/bin/sh", "-c", "env"}}}))[0]
	out := run(t, d, Vars{"node": "1"}, "")
	if strings.Contains(out, "hunter22") || !strings.Contains(out, "TERM=ansi") || !strings.Contains(out, "BOAR_NODE=1") {
		t.Fatalf("env = %q", out)
	}
}

func TestTCPDoor(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	d := must(Validate([]Door{{Key: "tcp", Name: "TCP", IO: IOTCP, Command: []string{"/usr/bin/env", "BOAR_TEST_DOOR=tcp", "BOAR_PORT={port}", exe}}}))[0]
	out := run(t, d, nil, "xyz")
	if out != "tcp door ready\r\ngot xyz\r\n" {
		t.Fatalf("output = %q", out)
	}
}

func TestTCPDoorThatNeverConnects(t *testing.T) {
	d := must(Validate([]Door{{Key: "tcp", Name: "TCP", IO: IOTCP, Command: []string{"/bin/sh", "-c", "echo broken >&2; exit 3"}}}))[0]
	if _, err := Start(context.Background(), d, nil); err == nil || !strings.Contains(err.Error(), "broken") {
		t.Fatalf("err = %v", err)
	}
}

func TestKillStopsDoorAndChildren(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "survivor")
	d := must(Validate([]Door{{Key: "slow", Name: "Slow", Command: []string{
		"/bin/sh", "-c", `(sleep 1; touch "$1") & sleep 30`, "door", marker,
	}}}))[0]
	p, err := Start(context.Background(), d, nil)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	p.Kill()
	if time.Since(start) > 5*time.Second {
		t.Fatal("Kill took too long")
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a child of the door survived Kill")
	}
}

func TestStartFailsForMissingProgram(t *testing.T) {
	d := must(Validate([]Door{{Key: "none", Name: "None", Command: []string{"/nonexistent/door"}}}))[0]
	if _, err := Start(context.Background(), d, nil); err == nil {
		t.Fatal("expected an error")
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
