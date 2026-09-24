// Package telnet implements the part of the Telnet protocol a BBS needs:
// negotiation for character-at-a-time mode with server-side echo, window
// size reporting (NAWS, RFC 1073), IAC escaping and CR/LF normalisation.
package telnet

import (
	"bufio"
	"bytes"
	"net"
	"sync"
	"time"
)

// Telnet commands and options (RFC 854, 857, 858, 1073).
const (
	SE   byte = 240
	SB   byte = 250
	WILL byte = 251
	WONT byte = 252
	DO   byte = 253
	DONT byte = 254
	IAC  byte = 255

	OptEcho byte = 1
	OptSGA  byte = 3
	OptNAWS byte = 31
)

const (
	DefaultWidth  = 80
	DefaultHeight = 24

	minWidth, maxWidth   = 20, 512
	minHeight, maxHeight = 5, 512

	maxSubneg    = 64
	writeTimeout = 30 * time.Second
)

// Conn is a server-side Telnet connection. ReadByte must be called from a
// single goroutine; Write is safe for concurrent use.
type Conn struct {
	nc      net.Conn
	r       *bufio.Reader
	lastCR  bool
	refused map[[2]byte]bool

	wmu sync.Mutex

	smu           sync.Mutex
	width, height int
}

// New wraps nc. If idle > 0, a read that waits longer than idle fails with
// os.ErrDeadlineExceeded.
func New(nc net.Conn, idle time.Duration) *Conn {
	return &Conn{
		nc:      nc,
		r:       bufio.NewReader(idleReader{nc: nc, idle: idle}),
		refused: make(map[[2]byte]bool),
		width:   DefaultWidth,
		height:  DefaultHeight,
	}
}

// Negotiate asks the client for character mode, server echo and window size.
func (c *Conn) Negotiate() error {
	_, err := c.writeRaw([]byte{
		IAC, WILL, OptEcho,
		IAC, WILL, OptSGA,
		IAC, DO, OptSGA,
		IAC, DO, OptNAWS,
	})
	return err
}

// ReadByte returns the next data byte, handling protocol commands inline.
// CR LF and CR NUL are both reduced to a single CR.
func (c *Conn) ReadByte() (byte, error) {
	for {
		b, err := c.r.ReadByte()
		if err != nil {
			return 0, err
		}
		if b == IAC {
			data, isData, err := c.readCommand()
			if err != nil {
				return 0, err
			}
			if !isData {
				continue
			}
			c.lastCR = false
			return data, nil
		}
		if c.lastCR {
			c.lastCR = false
			if b == '\n' || b == 0 {
				continue
			}
		}
		c.lastCR = b == '\r'
		return b, nil
	}
}

// readCommand handles the bytes after an IAC. It reports isData for an
// escaped 0xFF data byte.
func (c *Conn) readCommand() (data byte, isData bool, err error) {
	cmd, err := c.r.ReadByte()
	if err != nil {
		return 0, false, err
	}
	switch cmd {
	case IAC:
		return IAC, true, nil
	case DO, DONT, WILL, WONT:
		opt, err := c.r.ReadByte()
		if err != nil {
			return 0, false, err
		}
		return 0, false, c.negotiate(cmd, opt)
	case SB:
		return 0, false, c.readSubneg()
	default: // NOP, GA, AYT and friends carry no data for us.
		return 0, false, nil
	}
}

// negotiate accepts the options we asked for and refuses everything else,
// replying at most once per option to avoid negotiation loops.
func (c *Conn) negotiate(cmd, opt byte) error {
	var reply byte
	switch cmd {
	case DO:
		if opt == OptEcho || opt == OptSGA {
			return nil
		}
		reply = WONT
	case WILL:
		if opt == OptNAWS || opt == OptSGA {
			return nil
		}
		reply = DONT
	default:
		return nil
	}
	key := [2]byte{reply, opt}
	if c.refused[key] {
		return nil
	}
	c.refused[key] = true
	_, err := c.writeRaw([]byte{IAC, reply, opt})
	return err
}

func (c *Conn) readSubneg() error {
	buf := make([]byte, 0, 16)
	for {
		b, err := c.r.ReadByte()
		if err != nil {
			return err
		}
		if b == IAC {
			next, err := c.r.ReadByte()
			if err != nil {
				return err
			}
			if next == SE {
				break
			}
			if next != IAC {
				continue // malformed; skip it
			}
		}
		if len(buf) < maxSubneg {
			buf = append(buf, b)
		}
	}
	if len(buf) >= 5 && buf[0] == OptNAWS {
		c.setSize(int(buf[1])<<8|int(buf[2]), int(buf[3])<<8|int(buf[4]))
	}
	return nil
}

func (c *Conn) setSize(w, h int) {
	c.smu.Lock()
	defer c.smu.Unlock()
	if w > 0 {
		c.width = min(max(w, minWidth), maxWidth)
	}
	if h > 0 {
		c.height = min(max(h, minHeight), maxHeight)
	}
}

// Size reports the client's window size, or 80x24 if it never said.
func (c *Conn) Size() (width, height int) {
	c.smu.Lock()
	defer c.smu.Unlock()
	return c.width, c.height
}

// Write sends data, escaping 0xFF bytes as IAC IAC.
func (c *Conn) Write(p []byte) (int, error) {
	out := p
	if bytes.IndexByte(p, IAC) >= 0 {
		out = make([]byte, 0, len(p)+8)
		for _, b := range p {
			out = append(out, b)
			if b == IAC {
				out = append(out, IAC)
			}
		}
	}
	if _, err := c.writeRaw(out); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *Conn) writeRaw(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if err := c.nc.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return 0, err
	}
	return c.nc.Write(p)
}

func (c *Conn) Close() error         { return c.nc.Close() }
func (c *Conn) RemoteAddr() net.Addr { return c.nc.RemoteAddr() }

// idleReader refreshes the read deadline before every network read.
type idleReader struct {
	nc   net.Conn
	idle time.Duration
}

func (r idleReader) Read(p []byte) (int, error) {
	if r.idle > 0 {
		if err := r.nc.SetReadDeadline(time.Now().Add(r.idle)); err != nil {
			return 0, err
		}
	}
	return r.nc.Read(p)
}
