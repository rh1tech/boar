// SPDX-License-Identifier: GPL-3.0-or-later

// Package bbs is the Boar BBS bulletin board: telnet and SSH sessions, menus,
// private mail, public boards, chat and the sysop's tools.
package bbs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"boar/internal/doors"
	"boar/internal/store"
	"boar/internal/telnet"
)

const (
	defaultName        = "Boar BBS"
	defaultMaxNodes    = 32
	defaultIdleTimeout = 15 * time.Minute
	defaultDoorsDir    = "data/doors"
	defaultMaxSignups  = 20

	acceptBackoff = 100 * time.Millisecond

	// loginTimeout caps the time to log in or register, however actively the
	// caller types, so anonymous connections cannot hold nodes forever.
	loginTimeout = 5 * time.Minute

	shutdownMessage = "*** System going down. Call back soon! ***"

	// Connection caps apply before login, so floods of half-open connections
	// can't exhaust memory or file descriptors.
	maxConnsPerIP   = 8
	minConnHeadroom = 16
	ipv6PrefixBits  = 64
	ipv6AddressBits = 128
)

type Config struct {
	Name        string
	MaxNodes    int
	IdleTimeout time.Duration
	ArtDir      string // custom screens; "" for built-in only

	// MaxSignupsPerDay caps new accounts across the whole BBS (0 = 20).
	// Whether they need approval is store.Config.ApproveNewUsers.
	MaxSignupsPerDay int

	// Mail sends email; nil turns email features off. PublicAddress (such
	// as "bbs.example.com:2222") is how emails tell people to call.
	Mail          MailQueue
	PublicAddress string

	// Doors are the external programs callers can open; DoorsDir holds the
	// per-node drop file folders.
	Doors    []doors.Door
	DoorsDir string
}

// limits groups the per-IP and per-user rate limiters. The ones guarding
// logins and signups are saved in the database and survive restarts.
type limits struct {
	loginFailures *rateLimiter  // per IP (IPv6: per /64)
	accounts      *loginBackoff // per handle, from anywhere
	signups       *rateLimiter  // per IP (IPv6: per /64)
	signupsAll    *rateLimiter  // the whole BBS, per day
	mail          *rateLimiter  // per user, counts recipients
	posts         *rateLimiter  // per user
	pages         *rateLimiter  // per user
	chat          *rateLimiter  // per user
	oneliners     *rateLimiter  // per user
	emailVerify   *rateLimiter  // per user: verification codes
	emailTarget   *rateLimiter  // per destination address: verification codes
	emailNotices  *rateLimiter  // per recipient
	emailCopies   *rateLimiter  // per recipient
	emailExport   *rateLimiter  // per user: "email me a copy"
}

// signupsAllKey is the single key of the BBS-wide signup limiter.
const signupsAllKey = "all"

func newLimits(cfg Config, db hitStore, log *slog.Logger) limits {
	persist := func(limit int, window time.Duration, bucket string) *rateLimiter {
		return newPersistentLimiter(limit, window, bucket, db, log)
	}
	return limits{
		loginFailures: persist(5, 15*time.Minute, "login-ip"),
		accounts:      newLoginBackoff(persist(0, backoffWindow, "login-handle")),
		signups:       persist(3, time.Hour, "signup-ip"),
		signupsAll:    persist(cfg.MaxSignupsPerDay, 24*time.Hour, "signup-all"),
		mail:          newRateLimiter(30, time.Hour),
		posts:         newRateLimiter(20, time.Hour),
		pages:         newRateLimiter(6, time.Minute),
		chat:          newRateLimiter(8, 10*time.Second),
		oneliners:     newRateLimiter(3, time.Hour),
		emailVerify:   newRateLimiter(3, time.Hour),
		emailTarget:   persist(3, 24*time.Hour, "email-target"),
		emailNotices:  newRateLimiter(1, 10*time.Minute),
		emailCopies:   newRateLimiter(30, time.Hour),
		emailExport:   newRateLimiter(20, time.Hour),
	}
}

type Server struct {
	cfg   Config
	store *store.Store
	log   *slog.Logger
	nodes *nodeTable
	chat  *chatHub
	limit limits
	conns *connLimiter
	wg    sync.WaitGroup

	doorLocks  sync.Map      // door key -> *sync.Mutex, for single-node doors
	doorMinute time.Duration // length of a door "minute"; shortened in tests

	loginTimeout time.Duration
}

func New(cfg Config, st *store.Store, log *slog.Logger) *Server {
	if cfg.Name == "" {
		cfg.Name = defaultName
	}
	if cfg.MaxNodes <= 0 {
		cfg.MaxNodes = defaultMaxNodes
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = defaultIdleTimeout
	}
	if cfg.MaxSignupsPerDay <= 0 {
		cfg.MaxSignupsPerDay = defaultMaxSignups
	}
	if cfg.DoorsDir == "" {
		cfg.DoorsDir = defaultDoorsDir
	}
	return &Server{
		cfg:          cfg,
		store:        st,
		log:          log,
		nodes:        newNodeTable(cfg.MaxNodes),
		chat:         newChatHub(),
		limit:        newLimits(cfg, st, log),
		conns:        newConnLimiter(cfg.MaxNodes+max(cfg.MaxNodes, minConnHeadroom), maxConnsPerIP),
		loginTimeout: loginTimeout,
		doorMinute:   time.Minute,
	}
}

// Serve accepts telnet callers until ctx is cancelled.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	return s.acceptLoop(ctx, ln, s.handleTelnet)
}

// acceptLoop runs handle for each connection until ctx is cancelled, then
// hangs up on every caller and waits for their sessions to finish.
func (s *Server) acceptLoop(ctx context.Context, ln net.Listener, handle func(net.Conn)) error {
	stop := context.AfterFunc(ctx, func() {
		_ = ln.Close() // unblocks Accept; the loop below notices ctx is done
		s.nodes.closeAll(shutdownMessage)
	})
	defer stop()

	for {
		nc, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				s.wg.Wait()
				return nil
			}
			s.log.Warn("accept failed", "err", err)
			time.Sleep(acceptBackoff)
			continue
		}
		key := limitKey(hostOf(nc.RemoteAddr()))
		if !s.conns.acquire(key) {
			s.log.Debug("connection refused: too many connections", "remote", nc.RemoteAddr().String())
			_ = nc.Close()
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer s.conns.release(key)
			defer func() {
				if r := recover(); r != nil {
					s.log.Error("session panic", "panic", r, "stack", string(debug.Stack()))
				}
			}()
			handle(nc)
		}()
	}
}

func (s *Server) handleTelnet(nc net.Conn) {
	log := s.log.With("remote", nc.RemoteAddr().String(), "via", "telnet")
	tc := telnet.New(nc, s.cfg.IdleTimeout)
	defer tc.Close()
	if err := tc.Negotiate(); err != nil {
		log.Debug("negotiation failed", "err", err)
		return
	}
	s.serveCaller(tc, nc.RemoteAddr(), false, log)
}

// serveCaller runs one caller's session on any transport.
func (s *Server) serveCaller(t Terminal, addr net.Addr, secure bool, log *slog.Logger) {
	ip := hostOf(addr)
	if s.limit.loginFailures.blocked(limitKey(ip)) {
		hangUp(t, log, "Too many failed logins from your address. Try again later.")
		return
	}
	n, err := s.nodes.acquire(t, secure)
	if err != nil {
		hangUp(t, log, err.Error())
		return
	}
	defer s.nodes.release(n)
	log = log.With("node", n.id)
	log.Info("connect")

	sess := newSession(s, t, n, ip, secure)
	defer sess.in.stop()
	loginTimer := time.AfterFunc(s.loginTimeout, func() {
		if !sess.loggedIn.Load() {
			log.Info("login timeout")
			_ = t.Close() // the session's next read fails and it unwinds
		}
	})
	defer loginTimer.Stop()

	err = sess.run()
	switch {
	case err == nil, errors.Is(err, io.EOF), errors.Is(err, net.ErrClosed):
		log.Info("disconnect")
	case errors.Is(err, os.ErrDeadlineExceeded):
		hangUp(t, log, "Idle too long, disconnecting.")
	default:
		log.Warn("session ended with error", "err", err)
	}
}

func hangUp(t Terminal, log *slog.Logger, msg string) {
	log.Info("hang up", "reason", msg)
	if _, err := t.Write([]byte("\r\n" + msg + "\r\n")); err != nil {
		log.Debug("hang-up message not delivered", "err", err)
	}
}

// event records something in the persistent event log.
func (s *Server) event(kind string, u store.User, ip, detail string) {
	e := store.Event{Kind: kind, UserID: u.ID, Handle: u.Handle, IP: ip, Detail: detail}
	if err := s.store.LogEvent(e); err != nil {
		s.log.Error("event log write failed", "kind", kind, "err", err)
	}
}

func hostOf(addr net.Addr) string {
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	return host
}

func userKey(id int64) string { return strconv.FormatInt(id, 10) }

// limitKey is the rate-limit key for an address: the IP itself, or its /64
// for IPv6, since one subscriber usually holds a whole /64.
func limitKey(host string) string {
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() != nil {
		return host
	}
	return ip.Mask(net.CIDRMask(ipv6PrefixBits, ipv6AddressBits)).String() + "/64"
}
