// SPDX-License-Identifier: GPL-3.0-or-later

package ftn

import (
	"fmt"
	"strings"
	"time"
)

// Routes is a node's whole routing table: the links it has a session with,
// and the one everything else goes through.
type Routes struct {
	Direct []Addr // nodes (or points) this system calls itself
	Via    Addr   // the uplink for the rest; zero means there is none
}

// Hop is where netmail for dest goes next: dest itself when it is a direct
// link, its boss when that is, and Via otherwise. ok is false when there is
// no route at all.
func (r Routes) Hop(dest Addr) (hop Addr, ok bool) {
	for _, d := range r.Direct {
		if d.Same(dest) {
			return dest, true
		}
	}
	if dest.IsPoint() {
		boss := dest.Boss()
		for _, d := range r.Direct {
			if d.Same(boss) {
				return boss, true
			}
		}
	}
	if r.Via.Zone != 0 {
		return r.Via, true
	}
	return Addr{}, false
}

// ParseAddrList reads "2:5030/1651, 2:410/51" into addresses.
func ParseAddrList(s string) ([]Addr, error) {
	var out []Addr
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f == "" {
			continue
		}
		a, err := ParseAddr(f)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// Contains reports whether addrs holds a (ignoring the domain).
func Contains(addrs []Addr, a Addr) bool {
	for _, x := range addrs {
		if x.Same(a) {
			return true
		}
	}
	return false
}

// AddVia appends the Via line a system adds to netmail it routes (FTS-4009),
// leaving the rest of the message byte for byte as it came.
func AddVia(pm *PackedMessage, by Addr, at time.Time, program string) {
	text := pm.Text
	for len(text) > 0 && text[len(text)-1] == 0 {
		text = text[:len(text)-1]
	}
	if n := len(text); n > 0 && text[n-1] != '\r' {
		text = append(text, '\r')
	}
	via := fmt.Sprintf("\x01Via %s @%s %s\r", by, at.UTC().Format("20060102.150405.UTC"), program)
	pm.Text = append(text, via...)
}

// IsNetmail reports whether pm is netmail rather than echomail.
func (pm PackedMessage) IsNetmail() bool {
	return !strings.HasPrefix(string(pm.Text), "AREA:")
}
