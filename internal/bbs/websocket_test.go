// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestWebSocketOriginAndProxyAddress(t *testing.T) {
	ts := startServerWith(t, Config{}, nil)
	h := ts.srv.WebSocketHandler(context.Background(), "https://boar.rh1.tech", "https://boar.rh1.ru")
	for _, tc := range []struct {
		name, path, origin, ip string
		want                   int
	}{
		{"missing origin", "/terminal/ws", "", "192.0.2.1", 403},
		{"foreign origin", "/terminal/ws", "https://evil.example", "192.0.2.1", 403},
		{"wrong scheme", "/terminal/ws", "http://boar.rh1.tech", "192.0.2.1", 403},
		{"Russian wrong scheme", "/terminal/ws", "http://boar.rh1.ru", "192.0.2.1", 403},
		{"Russian subdomain", "/terminal/ws", "https://evil.boar.rh1.ru", "192.0.2.1", 403},
		{"Russian exact origin", "/terminal/ws", "https://boar.rh1.ru", "", 400},
		{"missing IP", "/terminal/ws", "https://boar.rh1.tech", "", 400},
		{"invalid IP", "/terminal/ws", "https://boar.rh1.tech", "not-an-ip", 400},
		{"other path", "/admin", "https://boar.rh1.tech", "192.0.2.1", 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", tc.path, nil)
			r.RemoteAddr = "127.0.0.1:1234"
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("X-Real-IP", tc.ip)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

func TestWebSocketCallerAndLimits(t *testing.T) {
	ts := startServerWith(t, Config{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hs := httptest.NewServer(ts.srv.WebSocketHandler(ctx, "https://boar.rh1.tech", "https://boar.rh1.ru"))
	defer hs.Close()
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/terminal/ws"
	headers := http.Header{"Origin": {"https://boar.rh1.tech"}, "X-Real-Ip": {"192.0.2.9"}}
	for i := 0; i < maxConnsPerIP; i++ {
		if i%2 == 1 {
			headers.Set("Origin", "https://boar.rh1.ru")
		} else {
			headers.Set("Origin", "https://boar.rh1.tech")
		}
		ws, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: headers})
		if err != nil {
			t.Fatal(err)
		}
		defer ws.CloseNow()
		c := &client{t: t, name: "browser", c: websocket.NetConn(ctx, ws, websocket.MessageBinary)}
		c.expect("Handle")
		if i == 0 {
			c.key("NEW\r")
			c.expect("handle")
		}
	}
	_, response, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: headers})
	if err == nil || response == nil || response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected per-IP limit, got response %v, error %v", response, err)
	}
	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ts.srv.conns.acquire("192.0.2.9") {
			ts.srv.conns.release("192.0.2.9")
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("cancelled browser sessions did not release connection limits")
}
