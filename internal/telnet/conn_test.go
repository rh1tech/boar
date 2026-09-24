// SPDX-License-Identifier: GPL-3.0-or-later

package telnet

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

func pipe(t *testing.T) (*Conn, net.Conn) {
	t.Helper()
	server, client := net.Pipe()
	t.Cleanup(func() {
		server.Close()
		client.Close()
	})
	return New(server, 0), client
}

func send(client net.Conn, p []byte) {
	go func() { _, _ = client.Write(p) }()
}

func readAll(t *testing.T, c *Conn, n int) []byte {
	t.Helper()
	out := make([]byte, 0, n)
	for len(out) < n {
		b, err := c.ReadByte()
		if err != nil {
			t.Fatalf("ReadByte after %q: %v", out, err)
		}
		out = append(out, b)
	}
	return out
}

func TestNegotiationIsStrippedAndNAWSRecorded(t *testing.T) {
	c, client := pipe(t)
	send(client, []byte{
		IAC, WILL, OptNAWS,
		IAC, SB, OptNAWS, 0, 132, 0, 50, IAC, SE,
		IAC, DO, OptEcho,
		'h', 'i',
	})
	if got := readAll(t, c, 2); string(got) != "hi" {
		t.Fatalf("data = %q", got)
	}
	if w, h := c.Size(); w != 132 || h != 50 {
		t.Fatalf("size = %dx%d, want 132x50", w, h)
	}
}

func TestNAWSIsClampedAndZeroIgnored(t *testing.T) {
	c, client := pipe(t)
	send(client, []byte{IAC, SB, OptNAWS, 0, 0, 0xFF, 0xFF, 0xFF, 0xFF, IAC, SE, 'x'})
	readAll(t, c, 1)
	if w, h := c.Size(); w != DefaultWidth || h != maxHeight {
		t.Fatalf("size = %dx%d", w, h)
	}
}

func TestCRNormalisation(t *testing.T) {
	c, client := pipe(t)
	send(client, []byte("a\r\nb\r\x00c\rd\ne"))
	if got := readAll(t, c, 9); string(got) != "a\rb\rc\rd\ne" {
		t.Fatalf("got %q", got)
	}
}

func TestEscapedIACIsData(t *testing.T) {
	c, client := pipe(t)
	send(client, []byte{'a', IAC, IAC, 'b'})
	if got := readAll(t, c, 3); !bytes.Equal(got, []byte{'a', 0xFF, 'b'}) {
		t.Fatalf("got %v", got)
	}
}

func TestUnknownOptionRefusedOnce(t *testing.T) {
	c, client := pipe(t)
	const ttype = 24
	replies := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 3)
		_, err := io.ReadFull(client, buf)
		if err != nil {
			replies <- nil
			return
		}
		replies <- buf
	}()
	send(client, []byte{IAC, DO, ttype, IAC, DO, ttype, 'z'})
	if got := readAll(t, c, 1); string(got) != "z" {
		t.Fatalf("data = %q", got)
	}
	if got := <-replies; !bytes.Equal(got, []byte{IAC, WONT, ttype}) {
		t.Fatalf("reply = %v", got)
	}
}

func TestWriteEscapesIAC(t *testing.T) {
	c, client := pipe(t)
	go func() { _, _ = c.Write([]byte{1, 0xFF, 2}) }()
	buf := make([]byte, 4)
	if _, err := io.ReadFull(client, buf); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf, []byte{1, 0xFF, 0xFF, 2}) {
		t.Fatalf("wire = %v", buf)
	}
}

func TestIdleTimeout(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	c := New(server, 20*time.Millisecond)
	if _, err := c.ReadByte(); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
}
