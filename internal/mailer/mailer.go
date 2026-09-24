// SPDX-License-Identifier: GPL-3.0-or-later

// Package mailer sends plain-text email through an SMTP relay, from a
// bounded background queue so a slow mail server never stalls a caller.
package mailer

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"boar/internal/term"
)

// Message is one outgoing email. Body is plain text with \n line endings.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Sender delivers a message or reports why it couldn't.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

type SMTPConfig struct {
	Host     string
	Port     int    // 465 means implicit TLS; anything else uses STARTTLS
	Username string // "" skips authentication
	Password string
	From     string // envelope and header sender address
	FromName string // display name, e.g. the BBS name
}

const (
	implicitTLSPort = 465
	sendTimeout     = 30 * time.Second
)

// SMTPSender talks to a relay such as Gmail, Fastmail or a local Postfix.
type SMTPSender struct {
	cfg SMTPConfig
	now func() time.Time
}

func NewSMTPSender(cfg SMTPConfig) (*SMTPSender, error) {
	if cfg.Host == "" || cfg.Port <= 0 {
		return nil, errors.New("mailer: SMTP host and port are required")
	}
	if _, err := mail.ParseAddress(cfg.From); err != nil {
		return nil, fmt.Errorf("mailer: bad from address %q: %w", cfg.From, err)
	}
	return &SMTPSender{cfg: cfg, now: time.Now}, nil
}

func (s *SMTPSender) Send(ctx context.Context, m Message) error {
	if _, err := mail.ParseAddress(m.To); err != nil || strings.ContainsAny(m.To, "\r\n") {
		return fmt.Errorf("mailer: bad recipient %q", m.To)
	}
	msg, err := buildMessage(s.cfg.From, s.cfg.FromName, m, s.now())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	c, err := s.dial(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	return s.transact(c, m.To, msg)
}

func (s *SMTPSender) dial(ctx context.Context) (*smtp.Client, error) {
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	tlsCfg := &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}
	var conn net.Conn
	var err error
	if s.cfg.Port == implicitTLSPort {
		conn, err = (&tls.Dialer{Config: tlsCfg}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("mailer: connect %s: %w", addr, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			conn.Close()
			return nil, err
		}
	}
	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("mailer: %w", err)
	}
	// A relay on this machine is reached without leaving it, and usually has
	// a self-signed certificate, so it gets no STARTTLS. Remote relays must
	// offer TLS with a valid certificate.
	if s.cfg.Port != implicitTLSPort && !isLoopback(s.cfg.Host) {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsCfg); err != nil {
				c.Close()
				return nil, fmt.Errorf("mailer: STARTTLS: %w", err)
			}
		} else {
			// Codes and message copies are private too, not just the password.
			c.Close()
			return nil, errors.New("mailer: server offers no TLS; refusing to send mail in the clear")
		}
	}
	return c, nil
}

func (s *SMTPSender) transact(c *smtp.Client, to string, msg []byte) error {
	if s.cfg.Username != "" {
		// PlainAuth itself refuses unencrypted connections except to localhost.
		if err := c.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			return fmt.Errorf("mailer: auth: %w", err)
		}
	}
	if err := c.Mail(s.cfg.From); err != nil {
		return fmt.Errorf("mailer: MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("mailer: RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mailer: DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("mailer: write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mailer: send: %w", err)
	}
	return c.Quit()
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// buildMessage renders headers and a quoted-printable UTF-8 body. Header
// values are stripped of control characters, so nothing a caller typed can
// add headers of its own.
func buildMessage(from, fromName string, m Message, now time.Time) ([]byte, error) {
	var b bytes.Buffer
	sender := (&mail.Address{Name: term.StripControl(fromName), Address: from}).String()
	recipient := (&mail.Address{Address: m.To}).String()
	domain := from[strings.LastIndexByte(from, '@')+1:]
	id := make([]byte, 12)
	if _, err := rand.Read(id); err != nil {
		return nil, fmt.Errorf("mailer: message id: %w", err)
	}
	headers := [][2]string{
		{"From", sender},
		{"To", recipient},
		{"Subject", mime.QEncoding.Encode("utf-8", term.StripControl(m.Subject))},
		{"Date", now.Format(time.RFC1123Z)},
		{"Message-ID", "<" + hex.EncodeToString(id) + "@" + domain + ">"},
		{"MIME-Version", "1.0"},
		{"Content-Type", "text/plain; charset=utf-8"},
		{"Content-Transfer-Encoding", "quoted-printable"},
		{"Auto-Submitted", "auto-generated"},
	}
	for _, h := range headers {
		fmt.Fprintf(&b, "%s: %s\r\n", h[0], h[1])
	}
	b.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&b)
	body := strings.ReplaceAll(strings.ReplaceAll(m.Body, "\r\n", "\n"), "\n", "\r\n")
	if _, err := qp.Write([]byte(body)); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
