// SPDX-License-Identifier: GPL-3.0-or-later

// Package ftn is FidoNet Technology Network plumbing: addresses, packets,
// control lines and character sets. It knows nothing about the BBS; the BBS
// uses it to turn stored mail into packets and back.
//
// References are FTSC documents (http://ftsc.org/docs/): FTS-0001 for packets,
// FSC-0039/FSC-0048 for the Type 2+ header, FTS-4000/4001/4008/4009 and
// FTS-0009 for control lines, FTS-0004 for echomail, FTS-5003 for CHRS.
package ftn

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Addr is a 4D FidoNet address, zone:net/node.point, with an optional domain
// ("fidonet", "fsxnet") for 5D contexts such as binkd configs.
type Addr struct {
	Zone, Net, Node, Point int
	Domain                 string
}

var errBadAddr = errors.New("not a FidoNet address")

// ParseAddr reads "2:410/9", "2:410/9.1" or "2:410/9.1@fidonet".
func ParseAddr(s string) (Addr, error) {
	var a Addr
	s = strings.TrimSpace(s)
	if at := strings.IndexByte(s, '@'); at >= 0 {
		a.Domain = strings.ToLower(s[at+1:])
		s = s[:at]
	}
	colon := strings.IndexByte(s, ':')
	slash := strings.IndexByte(s, '/')
	if colon <= 0 || slash < colon+2 {
		return Addr{}, fmt.Errorf("%q: %w", s, errBadAddr)
	}
	nodePart := s[slash+1:]
	point := "0"
	if dot := strings.IndexByte(nodePart, '.'); dot >= 0 {
		nodePart, point = nodePart[:dot], nodePart[dot+1:]
	}
	parts := []string{s[:colon], s[colon+1 : slash], nodePart, point}
	nums := make([]int, 4)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 65535 {
			return Addr{}, fmt.Errorf("%q: %w", s, errBadAddr)
		}
		nums[i] = n
	}
	if nums[0] == 0 {
		return Addr{}, fmt.Errorf("%q: zone 0: %w", s, errBadAddr)
	}
	a.Zone, a.Net, a.Node, a.Point = nums[0], nums[1], nums[2], nums[3]
	return a, nil
}

// MustParseAddr is ParseAddr for constants in tests and defaults.
func MustParseAddr(s string) Addr {
	a, err := ParseAddr(s)
	if err != nil {
		panic(err)
	}
	return a
}

// String is the 4D form, without the point when it is zero and without the
// domain: the form origin lines and MSGIDs use.
func (a Addr) String() string {
	s := fmt.Sprintf("%d:%d/%d", a.Zone, a.Net, a.Node)
	if a.Point != 0 {
		s += fmt.Sprintf(".%d", a.Point)
	}
	return s
}

// String5D adds the domain, for binkd and other 5D contexts.
func (a Addr) String5D() string {
	if a.Domain == "" {
		return a.String()
	}
	return a.String() + "@" + a.Domain
}

// Boss is the node a point belongs to (the address itself for a node).
func (a Addr) Boss() Addr {
	a.Point = 0
	return a
}

// IsPoint reports whether the address is a point.
func (a Addr) IsPoint() bool { return a.Point != 0 }

// Same compares addresses, ignoring the domain.
func (a Addr) Same(b Addr) bool {
	return a.Zone == b.Zone && a.Net == b.Net && a.Node == b.Node && a.Point == b.Point
}

// Net2D is the "net/node" pair SEEN-BY and PATH lines are made of.
type Net2D struct{ Net, Node int }
