// SPDX-License-Identifier: GPL-3.0-or-later

package mailer

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSMTP accepts one plain-text SMTP session and records what it saw.
type fakeSMTP struct {
	addr    string
	auth    chan string
	rcpt    chan string
	data    chan string
	rejectR bool
	tls     bool // advertise STARTTLS (and fail it, having no certificate)
}

func startFakeSMTP(t *testing.T, rejectRcpt bool) *fakeSMTP {
	t.Helper()
	return startFakeSMTPWith(t, rejectRcpt, false)
}

func startFakeSMTPWith(t *testing.T, rejectRcpt, offerTLS bool) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	f := &fakeSMTP{addr: ln.Addr().String(), auth: make(chan string, 1), rcpt: make(chan string, 1), data: make(chan string, 1), rejectR: rejectRcpt, tls: offerTLS}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		f.serve(conn)
	}()
	return f
}

func (f *fakeSMTP) serve(conn net.Conn) {
	r := bufio.NewReader(conn)
	say := func(s string) { _, _ = io.WriteString(conn, s+"\r\n") }
	say("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			say("250-fake")
			if f.tls {
				say("250-STARTTLS")
			}
			say("250 AUTH PLAIN")
		case cmd == "STARTTLS":
			say("454 TLS not available")
		case strings.HasPrefix(cmd, "AUTH PLAIN"):
			raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(line[len("AUTH PLAIN"):]))
			f.auth <- string(raw)
			say("235 ok")
		case strings.HasPrefix(cmd, "MAIL FROM"):
			say("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO"):
			if f.rejectR {
				say("550 no such user")
				continue
			}
			f.rcpt <- strings.TrimSpace(line)
			say("250 ok")
		case cmd == "DATA":
			say("354 go ahead")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			f.data <- b.String()
			say("250 queued")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

func testSender(t *testing.T, addr string) *SMTPSender {
	t.Helper()
	host, port, _ := net.SplitHostPort(addr)
	var p int
	for _, c := range port {
		p = p*10 + int(c-'0')
	}
	s, err := NewSMTPSender(SMTPConfig{Host: host, Port: p, Username: "bbs", Password: "hunter22", From: "bbs@example.com", FromName: "Boar BBS"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSMTPSend(t *testing.T) {
	f := startFakeSMTP(t, false)
	s := testSender(t, f.addr)
	err := s.Send(context.Background(), Message{
		To:      "kasia@example.com",
		Subject: "Nowa wiadomość\r\nBcc: evil@example.com",
		Body:    "Cześć!\nLine two.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if auth := <-f.auth; auth != "\x00bbs\x00hunter22" {
		t.Errorf("auth = %q", auth)
	}
	if rcpt := <-f.rcpt; rcpt != "RCPT TO:<kasia@example.com>" {
		t.Errorf("rcpt = %q", rcpt)
	}
	raw := <-f.data
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, raw)
	}
	if bcc := msg.Header.Get("Bcc"); bcc != "" || strings.Contains(raw, "\r\nBcc:") {
		t.Fatalf("header injection got through:\n%s", raw)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || subject != "Nowa wiadomośćBcc: evil@example.com" {
		t.Errorf("subject = %q (%v)", subject, err)
	}
	if from := msg.Header.Get("From"); !strings.Contains(from, "bbs@example.com") || !strings.Contains(from, "Boar BBS") {
		t.Errorf("from = %q", from)
	}
	for _, h := range []string{"Date", "Message-Id", "Auto-Submitted"} {
		if msg.Header.Get(h) == "" {
			t.Errorf("missing %s header", h)
		}
	}
	body, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if err != nil || strings.TrimRight(string(body), "\r\n") != "Cześć!\r\nLine two." { // SMTP ends DATA with CRLF
		t.Errorf("body = %q (%v)", body, err)
	}
}

func TestSMTPRejectedRecipient(t *testing.T) {
	f := startFakeSMTP(t, true)
	s := testSender(t, f.addr)
	if err := s.Send(context.Background(), Message{To: "nobody@example.com", Subject: "x", Body: "y"}); err == nil {
		t.Fatal("expected RCPT failure")
	}
}

func TestSMTPRefusesBadRecipientBeforeDialing(t *testing.T) {
	s, err := NewSMTPSender(SMTPConfig{Host: "127.0.0.1", Port: 1, From: "bbs@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	for _, to := range []string{"not an address", "a@example.com\r\nRCPT TO:<b@example.com>"} {
		if err := s.Send(context.Background(), Message{To: to}); err == nil || !strings.Contains(err.Error(), "bad recipient") {
			t.Errorf("Send(%q) err = %v", to, err)
		}
	}
}

func TestNewSMTPSenderValidates(t *testing.T) {
	if _, err := NewSMTPSender(SMTPConfig{Port: 587, From: "a@b.c"}); err == nil {
		t.Error("missing host accepted")
	}
	if _, err := NewSMTPSender(SMTPConfig{Host: "h", Port: 587, From: "nope"}); err == nil {
		t.Error("bad from accepted")
	}
}

func TestIsLoopback(t *testing.T) {
	for host, want := range map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true, "smtp.gmail.com": false, "10.0.0.1": false} {
		if got := isLoopback(host); got != want {
			t.Errorf("isLoopback(%q) = %v", host, got)
		}
	}
}

// flakySender fails the first n sends.
type flakySender struct {
	failures atomic.Int32
	mu       sync.Mutex
	sent     []Message
}

func (f *flakySender) Send(ctx context.Context, m Message) error {
	if f.failures.Add(-1) >= 0 {
		return errors.New("try again")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return nil
}

func TestQueueRetriesAndDrainsOnClose(t *testing.T) {
	sender := &flakySender{}
	sender.failures.Store(2)
	q := NewQueue(sender, slog.New(slog.NewTextHandler(io.Discard, nil)), 4)
	q.delays = []time.Duration{time.Millisecond, time.Millisecond}
	if err := q.Enqueue(Message{To: "a@example.com"}); err != nil {
		t.Fatal(err)
	}
	q.Close(5 * time.Second)
	if len(sender.sent) != 1 {
		t.Fatalf("sent = %v", sender.sent)
	}
	if err := q.Enqueue(Message{To: "b@example.com"}); !errors.Is(err, ErrQueueClosed) {
		t.Fatalf("enqueue after close err = %v", err)
	}
}

// blockingSender never returns until its context ends.
type blockingSender struct{}

func (blockingSender) Send(ctx context.Context, m Message) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestQueueFullAndCloseGivesUp(t *testing.T) {
	q := NewQueue(blockingSender{}, slog.New(slog.NewTextHandler(io.Discard, nil)), 1)
	_ = q.Enqueue(Message{To: "a@example.com"}) // taken by the worker, which then blocks
	deadline := time.Now().Add(time.Second)
	for q.Enqueue(Message{To: "b@example.com"}) != nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond) // wait for the worker to pick up the first
	}
	if err := q.Enqueue(Message{To: "c@example.com"}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("err = %v, want ErrQueueFull", err)
	}
	start := time.Now()
	q.Close(50 * time.Millisecond)
	if time.Since(start) > 2*time.Second {
		t.Fatal("Close did not give up on a stuck sender")
	}
}

func TestDomainOf(t *testing.T) {
	if domainOf("kasia@example.com") != "example.com" || domainOf("nope") != "?" {
		t.Fatal("domainOf")
	}
}

func TestLoopbackRelaySkipsSTARTTLS(t *testing.T) {
	f := startFakeSMTPWith(t, false, true)
	s := testSender(t, f.addr)
	if err := s.Send(context.Background(), Message{To: "kasia@example.com", Subject: "x", Body: "y"}); err != nil {
		t.Fatalf("send via a loopback relay offering STARTTLS: %v", err)
	}
	<-f.auth
	<-f.rcpt
	<-f.data
}
