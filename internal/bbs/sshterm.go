// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"errors"
	"io"
	"os"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	sshReadChunk    = 1024
	sshReadBacklog  = 8
	sshWriteTimeout = 30 * time.Second
)

// sshTerm adapts an SSH session channel to Terminal. A reader goroutine
// feeds a small channel so ReadByte can enforce the idle timeout.
type sshTerm struct {
	ch   ssh.Channel
	idle time.Duration

	data    chan []byte
	readErr error // set before data is closed
	buf     []byte

	done      chan struct{}
	closeOnce sync.Once
	wmu       sync.Mutex

	mu            sync.Mutex
	width, height int
	termType      string

	userID int64 // account matched by public key, or 0
}

func (t *sshTerm) KeyUserID() int64 { return t.userID }

func newSSHTerm(ch ssh.Channel, idle time.Duration) *sshTerm {
	t := &sshTerm{
		ch:     ch,
		idle:   idle,
		data:   make(chan []byte, sshReadBacklog),
		done:   make(chan struct{}),
		width:  80,
		height: 24,
	}
	go t.readLoop()
	return t
}

func (t *sshTerm) readLoop() {
	defer close(t.data)
	for {
		chunk := make([]byte, sshReadChunk)
		n, err := t.ch.Read(chunk)
		if n > 0 {
			select {
			case t.data <- chunk[:n]:
			case <-t.done:
				return
			}
		}
		if err != nil {
			t.readErr = err
			return
		}
	}
}

func (t *sshTerm) ReadByte() (byte, error) {
	if len(t.buf) == 0 {
		chunk, err := t.next()
		if err != nil {
			return 0, err
		}
		t.buf = chunk
	}
	b := t.buf[0]
	t.buf = t.buf[1:]
	return b, nil
}

// next waits for the next chunk of input, up to the idle timeout.
func (t *sshTerm) next() ([]byte, error) {
	var timeout <-chan time.Time
	if t.idle > 0 {
		timer := time.NewTimer(t.idle)
		defer timer.Stop()
		timeout = timer.C
	}
	select {
	case chunk, ok := <-t.data:
		if !ok {
			if t.readErr == nil || errors.Is(t.readErr, io.EOF) {
				return nil, io.EOF
			}
			return nil, t.readErr
		}
		return chunk, nil
	case <-timeout:
		return nil, os.ErrDeadlineExceeded
	}
}

// Write hangs up on clients that stop reading instead of blocking forever
// on a full SSH window.
func (t *sshTerm) Write(p []byte) (int, error) {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	watchdog := time.AfterFunc(sshWriteTimeout, func() { _ = t.Close() })
	defer watchdog.Stop()
	return t.ch.Write(p)
}

func (t *sshTerm) Close() error {
	var err error
	t.closeOnce.Do(func() {
		close(t.done)
		err = t.ch.Close()
	})
	return err
}

func (t *sshTerm) Size() (int, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.width, t.height
}

func (t *sshTerm) TermType() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.termType
}

func (t *sshTerm) setSize(cols, rows uint32) {
	if cols == 0 || rows == 0 {
		return
	}
	w, h := clampSize(int(min(cols, maxTermWidth)), int(min(rows, maxTermHeight)))
	t.mu.Lock()
	defer t.mu.Unlock()
	t.width, t.height = w, h
}

// handleRequests answers session requests. It closes ready when the client
// asks for a shell.
func (t *sshTerm) handleRequests(reqs <-chan *ssh.Request, ready chan<- struct{}) {
	started := false
	for req := range reqs {
		ok := false
		switch req.Type {
		case "pty-req":
			var pty struct {
				Term                 string
				Cols, Rows, PxW, PxH uint32
				Modes                string
			}
			if ssh.Unmarshal(req.Payload, &pty) == nil {
				t.mu.Lock()
				t.termType = pty.Term
				t.mu.Unlock()
				t.setSize(pty.Cols, pty.Rows)
				ok = true
			}
		case "window-change":
			var wc struct{ Cols, Rows, PxW, PxH uint32 }
			if ssh.Unmarshal(req.Payload, &wc) == nil {
				t.setSize(wc.Cols, wc.Rows)
				ok = true
			}
		case "shell":
			ok = !started
			if !started {
				started = true
				close(ready)
			}
		case "env":
			ok = true // accepted and ignored
		}
		if err := req.Reply(ok, nil); err != nil {
			return
		}
	}
}
