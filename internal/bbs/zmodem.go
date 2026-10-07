// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"context"
	"io"
	"os"
	"sync"
	"time"

	"github.com/xx25/go-zmodem"
)

const (
	// zmodemIdle is how long the receiver may say nothing before the
	// transfer is given up: long enough for a client to open its download
	// dialog, short enough that a caller who never started one is not stuck.
	zmodemIdle = 60 * time.Second
	// zmodemSettle is how long leftovers from the receiver ("OO", stray
	// headers) are swallowed after a transfer, so they are not taken as keys.
	zmodemSettle = 1500 * time.Millisecond
)

// zmodemPort is the session's connection as the ZMODEM library sees it:
// reads come from the session's input pump, so the pump keeps running and
// the session gets its keyboard back afterwards; writes go straight to the
// transport, past the screen's charset and colour handling.
type zmodemPort struct {
	s        *session
	mu       sync.Mutex
	deadline time.Time
}

func (p *zmodemPort) SetReadDeadline(t time.Time) error {
	p.mu.Lock()
	p.deadline = t
	p.mu.Unlock()
	return nil
}

func (p *zmodemPort) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	if p.s.in.err != nil {
		return 0, p.s.in.err
	}
	p.mu.Lock()
	deadline := p.deadline
	p.mu.Unlock()
	var timeout <-chan time.Time
	if !deadline.IsZero() {
		d := time.Until(deadline)
		if d <= 0 {
			return 0, os.ErrDeadlineExceeded
		}
		t := time.NewTimer(d)
		defer t.Stop()
		timeout = t.C
	}
	select {
	case ev := <-p.s.in.events:
		c, err := p.s.in.accept(ev)
		if err != nil {
			return 0, err
		}
		b[0] = c
	case <-timeout:
		return 0, os.ErrDeadlineExceeded
	}
	n := 1
	for n < len(b) {
		select {
		case ev := <-p.s.in.events:
			c, err := p.s.in.accept(ev)
			if err != nil {
				return n, nil // the error is sticky; the next Read returns it
			}
			b[n] = c
			n++
		default:
			return n, nil
		}
	}
	return n, nil
}

func (p *zmodemPort) Write(b []byte) (int, error) { return p.s.tc.Write(b) }

// binaryTransport is a transport that must be told about a binary transfer
// (Telnet). SSH channels are clean already.
type binaryTransport interface{ SetBinary(on bool) error }

// zmodemOne sends one file and reports what happened to it.
type zmodemOne struct {
	offer *zmodem.FileOffer
	given bool
	sent  int64
	err   error
	done  bool
}

func (h *zmodemOne) NextFile() *zmodem.FileOffer {
	if h.given {
		return nil
	}
	h.given = true
	return h.offer
}

func (h *zmodemOne) AcceptFile(zmodem.FileInfo) (io.WriteCloser, int64, error) {
	return nil, 0, zmodem.ErrSkip // we only send
}

func (h *zmodemOne) FileProgress(_ zmodem.FileInfo, n int64) { h.sent = n }

func (h *zmodemOne) FileCompleted(_ zmodem.FileInfo, n int64, err error) {
	h.sent, h.err, h.done = n, err, true
}

// zmodemSend sends one file to the caller with ZMODEM. The caller's client
// starts receiving on the ZRQINIT it sees (most do on their own).
func (s *session) zmodemSend(name string, f *os.File, size int64, mod time.Time) (sent int64, err error) {
	if bt, ok := s.tc.(binaryTransport); ok {
		if err := bt.SetBinary(true); err != nil {
			return 0, err
		}
		defer func() { _ = bt.SetBinary(false) }()
	}
	h := &zmodemOne{offer: &zmodem.FileOffer{Name: name, Size: size, ModTime: mod, Mode: 0o644, Reader: f}}
	cfg := &zmodem.Config{
		Use32BitCRC:  true,
		MaxBlockSize: 1024,
		// Telnet clients and terminal emulators eat some control characters;
		// escaping all of them costs a little speed and loses nothing.
		EscapeMode:  zmodem.EscapeAll,
		RecvTimeout: zmodemIdle,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()
	err = zmodem.NewSession(&zmodemPort{s: s}, h, cfg).Send(ctx)
	s.drainInput(zmodemSettle)
	if err == nil && h.done && h.err != nil {
		err = h.err
	}
	if err == nil && !h.done {
		err = io.ErrUnexpectedEOF
	}
	return h.sent, err
}

// drainInput discards whatever the caller's side sends for d.
func (s *session) drainInput(d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	for {
		select {
		case ev := <-s.in.events:
			if _, err := s.in.accept(ev); err != nil {
				return
			}
		case <-t.C:
			return
		}
	}
}
