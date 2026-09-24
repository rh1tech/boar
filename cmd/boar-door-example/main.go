// SPDX-License-Identifier: GPL-3.0-or-later

// Command boar-door-example is a tiny door game, "Boar Hunt", that shows how
// a door talks to the BBS: it reads the caller's name from DOOR.SYS, then
// plays over stdin/stdout in CP437 with ANSI colors, echoing its own input
// like DOS doors do.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
)

const (
	forestSize = 100
	maxTries   = 7
	aliasLine  = 36 // DOOR.SYS line with the caller's alias
	maxInput   = 8
)

// ANSI colors and CP437 glyphs, written as raw bytes like a DOS door would.
const (
	reset  = "\x1b[0m"
	brown  = "\x1b[0;33m"
	yellow = "\x1b[1;33m"
	green  = "\x1b[0;32m"
	bright = "\x1b[1;37m"
	gray   = "\x1b[1;30m"
	red    = "\x1b[1;31m"
	block  = "\xdb"
	shade  = "\xb1"
	cls    = "\x1b[2J\x1b[H"
)

func main() {
	dropfile := flag.String("dropfile", "", "path to DOOR.SYS")
	flag.Parse()
	g := &game{
		in:   bufio.NewReader(os.Stdin),
		out:  os.Stdout,
		name: aliasFrom(*dropfile),
		pick: func() int { return rand.IntN(forestSize) + 1 },
	}
	if err := g.run(); err != nil && err != io.EOF {
		fmt.Fprintln(os.Stderr, "boar hunt:", err)
		os.Exit(1)
	}
}

// aliasFrom reads the caller's alias from a DOOR.SYS file.
func aliasFrom(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "stranger"
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) < aliasLine {
		return "stranger"
	}
	if alias := strings.TrimSpace(lines[aliasLine-1]); alias != "" {
		return alias
	}
	return "stranger"
}

type game struct {
	in   *bufio.Reader
	out  io.Writer
	name string
	pick func() int
}

func (g *game) say(format string, args ...any) {
	fmt.Fprintf(g.out, strings.ReplaceAll(format, "\n", "\r\n"), args...)
}

func (g *game) run() error {
	g.say(cls + green + strings.Repeat(shade, 40) + "\n")
	g.say(brown + "  " + block + block + yellow + " BOAR HUNT " + brown + block + block + gray + "  a Boar BBS door\n")
	g.say(green + strings.Repeat(shade, 40) + reset + "\n\n")
	g.say("Welcome, %s%s%s. A wild boar hides somewhere in the forest,\n", bright, g.name, reset)
	g.say("between trees %s1%s and %s%d%s. You have %d guesses.\n\n", bright, reset, bright, forestSize, reset, maxTries)
	for {
		won, err := g.round()
		if err != nil {
			return err
		}
		if won {
			g.say(yellow + "The boar grunts respectfully and trots off.\n" + reset)
		}
		g.say("\nHunt again? (y/n) ")
		ans, err := g.readLine()
		if err != nil {
			return err
		}
		if !strings.HasPrefix(strings.ToLower(ans), "y") {
			g.say("\n%sGood hunting, %s. Back to the BBS...%s\n", green, g.name, reset)
			return nil
		}
		g.say("\n")
	}
}

// round plays one hunt and reports whether the boar was found.
func (g *game) round() (bool, error) {
	boar := g.pick()
	for try := 1; try <= maxTries; try++ {
		g.say("%sGuess %d of %d%s, which tree? ", gray, try, maxTries, reset)
		line, err := g.readLine()
		if err != nil {
			return false, err
		}
		guess, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil || guess < 1 || guess > forestSize {
			g.say("%sThat's not a tree in this forest.%s\n", red, reset)
			try--
			continue
		}
		switch {
		case guess == boar:
			g.say("\n%s%s%s You found the boar at tree %d in %d guesses!%s\n", yellow, block+block, bright, boar, try, reset)
			return true, nil
		case guess < boar:
			g.say("Rustling further %snorth%s (higher).\n", bright, reset)
		default:
			g.say("Rustling further %ssouth%s (lower).\n", bright, reset)
		}
	}
	g.say("\n%sThe boar slipped away. It was at tree %d.%s\n", red, boar, reset)
	return false, nil
}

// readLine reads up to Enter, echoing keys and handling backspace, since a
// door is responsible for its own echo.
func (g *game) readLine() (string, error) {
	var buf []byte
	for {
		b, err := g.in.ReadByte()
		if err != nil {
			return "", err
		}
		switch {
		case b == '\r' || b == '\n':
			g.say("\n")
			return string(buf), nil
		case (b == 8 || b == 127) && len(buf) > 0:
			buf = buf[:len(buf)-1]
			g.say("\b \b")
		case b >= ' ' && b < 127 && len(buf) < maxInput:
			buf = append(buf, b)
			g.say("%c", b)
		}
	}
}
