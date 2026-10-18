// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"runtime/debug"
	"slices"
	"time"

	"github.com/coder/websocket"
)

// WebSocketHandler serves the existing BBS over binary UTF-8 WebSocket frames.
// Bind its HTTP server to loopback behind a TLS proxy which sets X-Real-IP.
func (s *Server) WebSocketHandler(ctx context.Context, origins ...string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/terminal/ws" {
			http.NotFound(w, r)
			return
		}
		if origin := r.Header.Get("Origin"); origin == "" || !slices.Contains(origins, origin) {
			http.Error(w, "Origin not allowed", http.StatusForbidden)
			return
		}
		addr, err := net.ResolveTCPAddr("tcp", r.RemoteAddr)
		if err != nil {
			http.Error(w, "Invalid client address", http.StatusBadRequest)
			return
		}
		if addr.IP.IsLoopback() {
			ip := net.ParseIP(r.Header.Get("X-Real-IP"))
			if ip == nil {
				http.Error(w, "Missing client address", http.StatusBadRequest)
				return
			}
			addr = &net.TCPAddr{IP: ip}
		}
		key := limitKey(hostOf(addr))
		if !s.conns.acquire(key) {
			http.Error(w, "Too many connections; try again later", http.StatusTooManyRequests)
			return
		}
		defer s.conns.release(key)
		// The exact origin, including scheme, has already been checked.
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			s.log.Debug("websocket upgrade failed", "err", err)
			return
		}
		nc := websocket.NetConn(ctx, ws, websocket.MessageBinary)
		ws.SetReadLimit(16 << 10)
		defer ws.CloseNow()
		stop := context.AfterFunc(ctx, func() { _ = ws.CloseNow() })
		defer stop()
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("websocket session panic", "panic", v, "stack", string(debug.Stack()))
			}
		}()
		t := &webTerminal{Conn: nc, in: bufio.NewReader(nc), idle: s.cfg.IdleTimeout}
		log := s.log.With("remote", addr.String(), "via", "websocket")
		s.serveCaller(t, addr, true, log)
	})
}

type webTerminal struct {
	net.Conn
	in   *bufio.Reader
	idle time.Duration
}

func (t *webTerminal) ReadByte() (byte, error) {
	if err := t.SetReadDeadline(time.Now().Add(t.idle)); err != nil {
		return 0, err
	}
	return t.in.ReadByte()
}

func (t *webTerminal) Write(p []byte) (int, error) {
	if err := t.SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return 0, err
	}
	return t.Conn.Write(p)
}

func (t *webTerminal) Size() (int, int) { return 80, 25 }
func (t *webTerminal) TermType() string { return "xterm-256color" }
