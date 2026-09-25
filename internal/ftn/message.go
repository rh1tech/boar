// SPDX-License-Identifier: GPL-3.0-or-later

package ftn

import (
	"bytes"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Message is a netmail or echomail message with its text decoded to UTF-8
// and its FTN machinery (control lines, SEEN-BY, PATH) pulled out of the body.
type Message struct {
	Area    string // echo tag; "" for netmail
	From    string
	To      string
	Subject string
	Date    time.Time
	Orig    Addr
	Dest    Addr // netmail only; echomail is addressed by Area
	Attr    Attr

	MsgID   string // "2:410/9 1a2b3c4d"
	Reply   string // MsgID of the message this answers
	Charset Charset
	TZUTC   string // "0200" or "-0500"

	// Body is the text the reader sees, lines joined with "\n". For echomail
	// it ends with the tearline and origin line.
	Body string

	SeenBy  []Net2D
	Path    []Net2D
	Kludges []Kludge // control lines not modelled above, in order
}

// Kludge is a control line: ^A NAME: value.
type Kludge struct{ Name, Value string }

// ParseMessage decodes pm, which arrived in a packet from pktFrom to pktTo.
// defaultCS is used when the message carries no CHRS line.
func ParseMessage(pm PackedMessage, pktFrom, pktTo Addr, defaultCS Charset) Message {
	m := Message{
		Attr: pm.Attr, Charset: defaultCS,
		Orig: Addr{Zone: pktFrom.Zone, Net: pm.OrigNet, Node: pm.OrigNode},
		Dest: Addr{Zone: pktTo.Zone, Net: pm.DestNet, Node: pm.DestNode},
	}
	if t, ok := ParseDate(pm.DateTime); ok {
		m.Date = t
	}
	lines := bytes.Split(bytes.ReplaceAll(pm.Text, []byte("\n"), nil), []byte("\r"))
	if len(lines) > 0 && bytes.HasPrefix(lines[0], []byte("AREA:")) {
		m.Area = strings.ToUpper(strings.TrimSpace(string(lines[0][5:])))
		lines = lines[1:]
	}

	// Control lines are ASCII, so they can be read before the charset is known.
	var body [][]byte
	var fmpt, topt int
	for _, l := range lines {
		switch {
		case len(l) > 0 && l[0] == 1:
			name, value := splitKludge(string(l[1:]))
			switch name {
			case "INTL":
				m.applyINTL(value)
			case "FMPT":
				fmpt, _ = strconv.Atoi(value)
			case "TOPT":
				topt, _ = strconv.Atoi(value)
			case "MSGID":
				m.MsgID = value
			case "REPLY":
				m.Reply = value
			case "CHRS", "CHARSET":
				if cs, ok := ParseCHRS(value); ok {
					m.Charset = cs
				}
			case "TZUTC":
				m.TZUTC = value
			case "PATH":
				m.Path = appendNets(m.Path, value)
			default:
				m.Kludges = append(m.Kludges, Kludge{name, value})
			}
		case bytes.HasPrefix(l, []byte("SEEN-BY:")):
			m.SeenBy = appendNets(m.SeenBy, string(l[8:]))
		default:
			body = append(body, l)
		}
	}
	m.Orig.Point, m.Dest.Point = fmpt, topt
	for len(body) > 0 && len(bytes.TrimSpace(body[len(body)-1])) == 0 {
		body = body[:len(body)-1]
	}

	m.From = strings.TrimSpace(Decode(pm.From, m.Charset))
	m.To = strings.TrimSpace(Decode(pm.To, m.Charset))
	m.Subject = strings.TrimSpace(Decode(pm.Subject, m.Charset))
	m.Body = Decode(bytes.Join(body, []byte("\n")), m.Charset)
	if m.Area != "" {
		if a, ok := OriginAddr(m.Body); ok {
			m.Orig = a
		} else if a, ok := msgidAddr(m.MsgID); ok {
			m.Orig = a
		}
	}
	return m
}

func splitKludge(s string) (string, string) {
	if name, value, ok := strings.Cut(s, ":"); ok && !strings.ContainsAny(name, " ") {
		return strings.ToUpper(name), strings.TrimSpace(value)
	}
	name, value, _ := strings.Cut(s, " ")
	return strings.ToUpper(name), strings.TrimSpace(value)
}

// applyINTL reads "INTL dz:dn/dd oz:on/od", which carries the zones netmail
// headers have no room for.
func (m *Message) applyINTL(v string) {
	f := strings.Fields(v)
	if len(f) != 2 {
		return
	}
	if d, err := ParseAddr(f[0]); err == nil {
		m.Dest.Zone, m.Dest.Net, m.Dest.Node = d.Zone, d.Net, d.Node
	}
	if o, err := ParseAddr(f[1]); err == nil {
		m.Orig.Zone, m.Orig.Net, m.Orig.Node = o.Zone, o.Net, o.Node
	}
}

// OriginAddr finds the address in the last origin line of an echomail body:
// " * Origin: Boar BBS (2:410/9)".
func OriginAddr(body string) (Addr, bool) {
	i := strings.LastIndex(body, " * Origin:")
	if i < 0 {
		return Addr{}, false
	}
	line := body[i:]
	if nl := strings.IndexByte(line, '\n'); nl >= 0 {
		line = line[:nl]
	}
	open, close := strings.LastIndexByte(line, '('), strings.LastIndexByte(line, ')')
	if open < 0 || close < open {
		return Addr{}, false
	}
	a, err := ParseAddr(line[open+1 : close])
	return a, err == nil
}

func msgidAddr(msgid string) (Addr, bool) {
	f := strings.Fields(msgid)
	if len(f) != 2 {
		return Addr{}, false
	}
	a, err := ParseAddr(f[0])
	return a, err == nil
}

// appendNets reads the "net/node node net/node" shorthand of SEEN-BY and PATH.
func appendNets(to []Net2D, s string) []Net2D {
	net := 0
	for _, f := range strings.Fields(s) {
		nodePart := f
		if n, node, ok := strings.Cut(f, "/"); ok {
			v, err := strconv.Atoi(n)
			if err != nil {
				continue
			}
			net, nodePart = v, node
		}
		node, err := strconv.Atoi(nodePart)
		if err != nil || net == 0 {
			continue
		}
		to = append(to, Net2D{net, node})
	}
	return to
}

// FormatNets writes SEEN-BY or PATH lines: prefix, then the shorthand,
// wrapped before 80 columns. Sort SEEN-BY first; PATH keeps its order.
func FormatNets(prefix string, nets []Net2D) []string {
	var lines []string
	line, net := prefix, -1
	for _, n := range nets {
		tok := strconv.Itoa(n.Node)
		if n.Net != net {
			tok = fmt.Sprintf("%d/%d", n.Net, n.Node)
		}
		if len(line)+1+len(tok) > 79 && line != prefix {
			lines = append(lines, line)
			line = prefix
			tok = fmt.Sprintf("%d/%d", n.Net, n.Node) // a new line restates the net
		}
		line += " " + tok
		net = n.Net
	}
	if line != prefix {
		lines = append(lines, line)
	}
	return lines
}

// SortNets orders SEEN-BY entries and drops duplicates.
func SortNets(nets []Net2D) []Net2D {
	out := slices.Clone(nets)
	slices.SortFunc(out, func(a, b Net2D) int {
		if a.Net != b.Net {
			return a.Net - b.Net
		}
		return a.Node - b.Node
	})
	return slices.Compact(out)
}

// Pack encodes m for a packet. Netmail gets INTL/FMPT/TOPT so the full 4D
// addresses survive; echomail gets its AREA line, SEEN-BY and PATH.
func (m *Message) Pack() PackedMessage {
	pm := PackedMessage{
		OrigNode: m.Orig.Node, OrigNet: m.Orig.Net,
		DestNode: m.Dest.Node, DestNet: m.Dest.Net,
		Attr:     m.Attr,
		DateTime: FormatDate(m.Date),
		To:       Encode(m.To, m.Charset),
		From:     Encode(m.From, m.Charset),
		Subject:  Encode(m.Subject, m.Charset),
	}
	var b bytes.Buffer
	kludge := func(name, value string) {
		if value != "" {
			b.WriteString("\x01" + name + ": " + value + "\r")
		}
	}
	if m.Area != "" {
		b.WriteString("AREA:" + m.Area + "\r")
	} else {
		b.WriteString(fmt.Sprintf("\x01INTL %d:%d/%d %d:%d/%d\r",
			m.Dest.Zone, m.Dest.Net, m.Dest.Node, m.Orig.Zone, m.Orig.Net, m.Orig.Node))
		if m.Orig.Point != 0 {
			b.WriteString(fmt.Sprintf("\x01FMPT %d\r", m.Orig.Point))
		}
		if m.Dest.Point != 0 {
			b.WriteString(fmt.Sprintf("\x01TOPT %d\r", m.Dest.Point))
		}
	}
	kludge("MSGID", m.MsgID)
	kludge("REPLY", m.Reply)
	kludge("CHRS", m.Charset.CHRS())
	kludge("TZUTC", m.TZUTC)
	for _, k := range m.Kludges {
		kludge(k.Name, k.Value)
	}
	body := strings.ReplaceAll(strings.ReplaceAll(m.Body, "\r\n", "\n"), "\r", "\n")
	b.Write(Encode(strings.ReplaceAll(body, "\n", "\r"), m.Charset))
	b.WriteString("\r")
	if m.Area != "" {
		for _, l := range FormatNets("SEEN-BY:", SortNets(m.SeenBy)) {
			b.WriteString(l + "\r")
		}
		for _, l := range FormatNets("\x01PATH:", m.Path) {
			b.WriteString(l + "\r")
		}
	}
	pm.Text = b.Bytes()
	return pm
}

// TZUTC formats a zone offset the way the TZUTC line wants it (FTS-4008):
// "0200", "-0500", with no plus sign.
func TZUTC(t time.Time) string {
	_, off := t.Zone()
	sign := ""
	if off < 0 {
		sign, off = "-", -off
	}
	return fmt.Sprintf("%s%02d%02d", sign, off/3600, off%3600/60)
}

// EchoFooter is the tearline and origin line an echomail message ends with.
// The origin line is kept under 80 columns by shortening the text, never the
// address (FTS-0004).
func EchoFooter(tear, origin string, a Addr) string {
	addr := " (" + a.String() + ")"
	room := 79 - len(" * Origin: ") - len(addr)
	if r := []rune(origin); len(r) > room {
		origin = string(r[:max(room, 0)])
	}
	return "--- " + tear + "\n * Origin: " + origin + addr
}
