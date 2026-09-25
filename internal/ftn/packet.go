// SPDX-License-Identifier: GPL-3.0-or-later

package ftn

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Packet is one .PKT file: a header naming the two systems, then messages.
type Packet struct {
	From, To Addr
	Password string // up to 8 characters, compared case-insensitively
	Created  time.Time
	Messages []PackedMessage
}

// PackedMessage is a message as it travels inside a packet (FTS-0001 §2.1).
// Its text is kept as raw bytes: the charset is only known once the CHRS
// line in the text has been read, so decoding is Message's job.
type PackedMessage struct {
	OrigNode, OrigNet int
	DestNode, DestNet int
	Attr              Attr
	Cost              int
	DateTime          string // "DD Mon YY  HH:MM:SS", as the sender wrote it
	To, From, Subject []byte
	Text              []byte
}

// Attr is the message attribute word (FTS-0001).
type Attr uint16

const (
	AttrPrivate   Attr = 0x0001
	AttrCrash     Attr = 0x0002
	AttrReceived  Attr = 0x0004
	AttrSent      Attr = 0x0008
	AttrFile      Attr = 0x0010
	AttrInTransit Attr = 0x0020
	AttrOrphan    Attr = 0x0040
	AttrKillSent  Attr = 0x0080
	AttrLocal     Attr = 0x0100
	AttrHold      Attr = 0x0200
)

const (
	headerLen      = 58
	packetVersion  = 2
	messageType    = 2
	capWordType2p  = 0x0001
	maxNameLen     = 36
	maxSubjectLen  = 72
	maxMessageText = 1 << 20 // a sanity bound; real messages are tiny

	// ProductCode is Boar's FTSC product code. It has none assigned; 0xFE is
	// the code the FTSC list keeps for products without one.
	ProductCode = 0xFE
)

var (
	ErrNotPacket = errors.New("not a FidoNet packet")
	le           = binary.LittleEndian
)

// dateLayout is the packed-message date, "01 Jan 26  13:04:05".
const dateLayout = "02 Jan 06  15:04:05"

// FormatDate writes t in the packed-message date form.
func FormatDate(t time.Time) string { return t.Format(dateLayout) }

// ParseDate reads a packed-message date. Senders are loose about the
// padding, so spaces are normalised first.
func ParseDate(s string) (time.Time, bool) {
	fields := strings.Fields(s)
	if len(fields) != 4 {
		return time.Time{}, false
	}
	if len(fields[0]) == 1 {
		fields[0] = "0" + fields[0]
	}
	t, err := time.Parse(dateLayout, fields[0]+" "+fields[1]+" "+fields[2]+"  "+fields[3])
	return t, err == nil
}

// WriteTo encodes p as a Type 2+ packet.
func (p *Packet) WriteTo(w io.Writer) (int64, error) {
	var buf bytes.Buffer
	h := make([]byte, headerLen)
	t := p.Created
	if t.IsZero() {
		t = time.Now()
	}
	put := func(off, v int) { le.PutUint16(h[off:], uint16(v)) }
	put(0, p.From.Node)
	put(2, p.To.Node)
	put(4, t.Year())
	put(6, int(t.Month())-1)
	put(8, t.Day())
	put(10, t.Hour())
	put(12, t.Minute())
	put(14, t.Second())
	put(18, packetVersion)
	put(20, p.From.Net)
	put(22, p.To.Net)
	h[24] = ProductCode & 0xff
	copy(h[26:34], strings.ToUpper(p.Password))
	put(34, p.From.Zone)
	put(36, p.To.Zone)
	le.PutUint16(h[40:], capWordType2p<<8) // byte-swapped copy of the capability word
	put(44, capWordType2p)
	put(46, p.From.Zone)
	put(48, p.To.Zone)
	put(50, p.From.Point)
	put(52, p.To.Point)
	buf.Write(h)
	for i := range p.Messages {
		p.Messages[i].encode(&buf)
	}
	buf.Write([]byte{0, 0})
	n, err := w.Write(buf.Bytes())
	return int64(n), err
}

func (m *PackedMessage) encode(buf *bytes.Buffer) {
	var h [14]byte
	le.PutUint16(h[0:], messageType)
	le.PutUint16(h[2:], uint16(m.OrigNode))
	le.PutUint16(h[4:], uint16(m.DestNode))
	le.PutUint16(h[6:], uint16(m.OrigNet))
	le.PutUint16(h[8:], uint16(m.DestNet))
	le.PutUint16(h[10:], uint16(m.Attr))
	le.PutUint16(h[12:], uint16(m.Cost))
	buf.Write(h[:])
	date := make([]byte, 20)
	copy(date, m.DateTime)
	date[19] = 0
	buf.Write(date)
	buf.Write(clip(m.To, maxNameLen-1))
	buf.WriteByte(0)
	buf.Write(clip(m.From, maxNameLen-1))
	buf.WriteByte(0)
	buf.Write(clip(m.Subject, maxSubjectLen-1))
	buf.WriteByte(0)
	buf.Write(bytes.ReplaceAll(m.Text, []byte{0}, nil))
	buf.WriteByte(0)
}

func clip(b []byte, n int) []byte {
	b = bytes.ReplaceAll(b, []byte{0}, nil)
	if len(b) > n {
		return b[:n]
	}
	return b
}

// ReadPacket decodes a Type 2 or Type 2+ packet.
func ReadPacket(r io.Reader) (*Packet, error) {
	br := bufio.NewReader(r)
	h := make([]byte, headerLen)
	if _, err := io.ReadFull(br, h); err != nil {
		return nil, fmt.Errorf("%w: short header", ErrNotPacket)
	}
	get := func(off int) int { return int(le.Uint16(h[off:])) }
	if get(18) != packetVersion {
		return nil, fmt.Errorf("%w: version %d", ErrNotPacket, get(18))
	}
	p := &Packet{Password: strings.TrimRight(string(h[26:34]), "\x00 ")}
	p.From = Addr{Zone: get(34), Net: get(20), Node: get(0)}
	p.To = Addr{Zone: get(36), Net: get(22), Node: get(2)}
	year, month := get(4), get(6)
	if year > 1980 && month < 12 {
		p.Created = time.Date(year, time.Month(month+1), max(get(8), 1), get(10), get(12), get(14), 0, time.UTC)
	}
	cw, cwCopy := get(44), get(40)
	if cw&capWordType2p != 0 && cw == (cwCopy>>8|cwCopy<<8)&0xffff {
		// Type 2+: zones and points in the extended fields.
		if z := get(46); z != 0 {
			p.From.Zone = z
		}
		if z := get(48); z != 0 {
			p.To.Zone = z
		}
		p.From.Point, p.To.Point = get(50), get(52)
		// FSC-0048 points put 0xFFFF in the net field and the real net in AuxNet.
		if p.From.Net == 0xffff && p.From.Point != 0 && get(38) != 0 {
			p.From.Net = get(38)
		}
	}
	for {
		var kind [2]byte
		if _, err := io.ReadFull(br, kind[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return p, nil // a missing terminator is tolerated
			}
			return nil, err
		}
		switch le.Uint16(kind[:]) {
		case 0:
			return p, nil
		case messageType:
		default:
			return p, fmt.Errorf("%w: bad message type after %d messages", ErrNotPacket, len(p.Messages))
		}
		m, err := decodeMessage(br)
		if err != nil {
			return p, err
		}
		p.Messages = append(p.Messages, m)
	}
}

func decodeMessage(br *bufio.Reader) (PackedMessage, error) {
	var h [32]byte // 12 bytes of fields after the type, then the 20-byte date
	if _, err := io.ReadFull(br, h[:]); err != nil {
		return PackedMessage{}, fmt.Errorf("%w: truncated message header", ErrNotPacket)
	}
	m := PackedMessage{
		OrigNode: int(le.Uint16(h[0:])), DestNode: int(le.Uint16(h[2:])),
		OrigNet: int(le.Uint16(h[4:])), DestNet: int(le.Uint16(h[6:])),
		Attr: Attr(le.Uint16(h[8:])), Cost: int(le.Uint16(h[10:])),
		DateTime: string(bytes.TrimRight(h[12:32], "\x00")),
	}
	var err error
	if m.To, err = readZ(br, maxNameLen); err != nil {
		return m, err
	}
	if m.From, err = readZ(br, maxNameLen); err != nil {
		return m, err
	}
	if m.Subject, err = readZ(br, maxSubjectLen); err != nil {
		return m, err
	}
	m.Text, err = readZ(br, maxMessageText)
	return m, err
}

// readZ reads a NUL-terminated field of at most limit bytes.
func readZ(br *bufio.Reader, limit int) ([]byte, error) {
	var out []byte
	for {
		c, err := br.ReadByte()
		if err != nil {
			return out, fmt.Errorf("%w: unterminated field", ErrNotPacket)
		}
		if c == 0 {
			return out, nil
		}
		if len(out) >= limit {
			return out, fmt.Errorf("%w: field longer than %d bytes", ErrNotPacket, limit)
		}
		out = append(out, c)
	}
}
