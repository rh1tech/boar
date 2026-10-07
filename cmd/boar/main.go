// SPDX-License-Identifier: GPL-3.0-or-later

// Command boar runs Boar BBS over telnet and SSH.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"boar/internal/bbs"
	"boar/internal/doors"
	"boar/internal/filelink"
	"boar/internal/ftn"
	"boar/internal/mailer"
	"boar/internal/store"
	"boar/internal/web"
)

const maxNodesLimit = 999

type options struct {
	telnetAddr string
	sshAddr    string
	hostKey    string
	dataPath   string
	artDir     string
	doorsFile  string
	doorsDir   string
	name       string
	sysop      string
	maxNodes   int
	approve    bool
	maxSignups int
	idle       time.Duration
	verbose    bool

	smtpHost   string
	smtpPort   int
	smtpUser   string
	smtpFrom   string
	publicAddr string

	webAddr string

	ftnInbound  string
	ftnInsecure string
	ftnAddress  string
	ftnOutbound string
	ftnZone     int
	ftnDirect   string
	ftnVia      string
	ftnSysop    string

	files        string
	filesWeb     string
	filesURL     string
	filesKey     string
	filesLinkTTL time.Duration
}

// smtpPasswordEnv holds the SMTP password, kept out of flags so it can't
// leak through the process list or shell history.
const smtpPasswordEnv = "BOAR_SMTP_PASSWORD"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "boar:", err)
		os.Exit(1)
	}
}

func parseFlags() (options, error) {
	var o options
	flag.StringVar(&o.telnetAddr, "telnet", ":2323", `telnet listen address ("" disables telnet)`)
	flag.StringVar(&o.sshAddr, "ssh", ":2222", `SSH listen address ("" disables SSH)`)
	flag.StringVar(&o.hostKey, "host-key", "data/ssh_host_ed25519_key", "SSH host key (generated if missing)")
	flag.StringVar(&o.dataPath, "data", "data/boar.db", "SQLite database file (created if missing)")
	flag.StringVar(&o.artDir, "art", "data/art", "folder of custom screens (NAME.ans or NAME.txt)")
	flag.StringVar(&o.doorsFile, "doors", "data/doors.json", "door games config (see doors.example.json)")
	flag.StringVar(&o.doorsDir, "doors-dir", "data/doors", "where per-node drop files are written")
	flag.StringVar(&o.name, "name", "Boar BBS", "BBS name shown to callers")
	flag.StringVar(&o.sysop, "sysop", "", "promote this existing user to sysop at startup")
	flag.IntVar(&o.maxNodes, "nodes", 32, "maximum simultaneous callers")
	flag.BoolVar(&o.approve, "approve-new-users", true, "new callers can only read (and mail the sysop) until a sysop approves them")
	flag.IntVar(&o.maxSignups, "max-signups", 20, "new accounts allowed per day across the whole BBS")
	flag.DurationVar(&o.idle, "idle", 15*time.Minute, "hang up on callers idle this long")
	flag.BoolVar(&o.verbose, "v", false, "verbose logging")
	flag.StringVar(&o.smtpHost, "smtp-host", "", `SMTP relay for email features ("" turns email off)`)
	flag.IntVar(&o.smtpPort, "smtp-port", 587, "SMTP port (465 = implicit TLS, otherwise STARTTLS)")
	flag.StringVar(&o.smtpUser, "smtp-user", "", "SMTP user name (password from $"+smtpPasswordEnv+")")
	flag.StringVar(&o.smtpFrom, "smtp-from", "", "address emails are sent from")
	flag.StringVar(&o.publicAddr, "public-address", "", `how emails tell people to call, e.g. "bbs.example.com:2222"`)
	flag.StringVar(&o.webAddr, "web", "", `sysop web interface address, e.g. "127.0.0.1:8023" ("" disables it; put a TLS proxy in front)`)
	flag.StringVar(&o.ftnInbound, "ftn-inbound", "", `FidoNet secure inbound to toss echomail from ("" disables the tosser)`)
	flag.StringVar(&o.ftnInsecure, "ftn-inbound-nonsecure", "", "FidoNet non-secure inbound (used with -ftn-inbound)")
	flag.StringVar(&o.ftnAddress, "ftn-address", "", `this node's FidoNet addresses, main one first, e.g. "2:5030/1651,2:410/51" (netmail for them is the BBS's)`)
	flag.StringVar(&o.ftnOutbound, "ftn-outbound", "", `binkd's outbound for the default zone, e.g. "/var/spool/ftn/out" ("" = netmail cannot be sent)`)
	flag.IntVar(&o.ftnZone, "ftn-zone", 2, "the default zone, the one -ftn-outbound itself holds")
	flag.StringVar(&o.ftnDirect, "ftn-direct", "", "FidoNet nodes binkd calls itself, comma separated")
	flag.StringVar(&o.ftnVia, "ftn-via", "", "where netmail for every other node goes")
	flag.StringVar(&o.ftnSysop, "ftn-sysop-name", "", "the real name sysops sign netmail with")
	flag.StringVar(&o.files, "files", "", `where file-echo files are kept, e.g. "/var/lib/boar/files" ("" turns file areas off)`)
	flag.StringVar(&o.filesWeb, "files-web", "", `download server address for file links, e.g. "127.0.0.1:8024" (put a TLS proxy in front)`)
	flag.StringVar(&o.filesURL, "files-url", "", `public prefix of a download link, e.g. "https://bbs.example.com/f/"`)
	flag.StringVar(&o.filesKey, "files-key", "", "signing key for download links (created if missing; default next to -data)")
	flag.DurationVar(&o.filesLinkTTL, "files-link-ttl", 24*time.Hour, "how long a download link works")
	flag.Parse()

	switch {
	case o.maxNodes < 1 || o.maxNodes > maxNodesLimit:
		return o, fmt.Errorf("-nodes must be between 1 and %d", maxNodesLimit)
	case o.idle < time.Minute:
		return o, errors.New("-idle must be at least 1m")
	case o.telnetAddr == "" && o.sshAddr == "":
		return o, errors.New("enable at least one of -telnet and -ssh")
	case o.smtpHost != "" && o.smtpFrom == "":
		return o, errors.New("-smtp-from is required with -smtp-host")
	case (o.filesWeb != "" || o.filesURL != "") && (o.files == "" || o.filesWeb == "" || o.filesURL == ""):
		return o, errors.New("-files-web and -files-url go together, and need -files")
	}
	if o.filesKey == "" {
		o.filesKey = filepath.Join(filepath.Dir(o.dataPath), "files.key")
	}
	return o, nil
}

func run() error {
	o, err := parseFlags()
	if err != nil {
		return err
	}
	level := slog.LevelInfo
	if o.verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	st, err := store.Open(store.Config{Path: o.dataPath, ApproveNewUsers: o.approve})
	if err != nil {
		return err
	}
	defer st.Close()
	if o.sysop != "" {
		if err := promote(st, o.sysop, log); err != nil {
			return err
		}
	}

	doorList, err := doors.LoadConfig(o.doorsFile)
	if err != nil {
		return err
	}
	if len(doorList) > 0 {
		log.Info("doors loaded", "count", len(doorList), "config", o.doorsFile)
	}
	cfg := bbs.Config{Name: o.name, MaxNodes: o.maxNodes, MaxSignupsPerDay: o.maxSignups, IdleTimeout: o.idle, ArtDir: o.artDir,
		PublicAddress: o.publicAddr, Doors: doorList, DoorsDir: o.doorsDir}
	if cfg.FTN, err = ftnConfig(o); err != nil {
		return err
	}
	cfg.Files = o.files
	var linkKey []byte
	if o.filesWeb != "" {
		if linkKey, err = filelink.LoadKey(o.filesKey); err != nil {
			return err
		}
		cfg.FileLinks = bbs.FileLinkConfig{URL: o.filesURL, Key: linkKey, TTL: o.filesLinkTTL}
	}
	if o.smtpHost != "" {
		queue, err := newMailQueue(o, log)
		if err != nil {
			return err
		}
		defer queue.Close(mailDrainTime)
		cfg.Mail = queue
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv := bbs.New(cfg, st, log)

	var servers []func() error
	if o.telnetAddr != "" {
		ln, err := net.Listen("tcp", o.telnetAddr)
		if err != nil {
			return err
		}
		log.Info("telnet listening", "addr", ln.Addr().String())
		servers = append(servers, func() error { return srv.Serve(ctx, ln) })
	}
	if o.sshAddr != "" {
		key, err := bbs.LoadOrCreateHostKey(o.hostKey)
		if err != nil {
			return err
		}
		ln, err := net.Listen("tcp", o.sshAddr)
		if err != nil {
			return err
		}
		log.Info("ssh listening", "addr", ln.Addr().String(), "host_key", o.hostKey)
		servers = append(servers, func() error { return srv.ServeSSH(ctx, ln, key) })
	}
	if o.webAddr != "" {
		serve, err := webServer(ctx, srv, o.webAddr, log)
		if err != nil {
			return err
		}
		servers = append(servers, serve)
	}
	if o.filesWeb != "" {
		serve, err := filesServer(ctx, o.filesWeb, filelink.Handler(linkKey, o.files, st, log), log)
		if err != nil {
			return err
		}
		servers = append(servers, serve)
	}
	if o.ftnInbound != "" || o.ftnInsecure != "" {
		in, insecure := o.ftnInbound, o.ftnInsecure
		servers = append(servers, func() error {
			return srv.RunTosser(ctx, in, insecure)
		})
	}
	log.Info("boar is up", "data", o.dataPath, "nodes", o.maxNodes)
	err = serveAll(servers, stop)
	log.Info("boar is down")
	return err
}

// serveAll runs every server; if one fails, stop shuts the others down.
func serveAll(servers []func() error, stop context.CancelFunc) error {
	var wg sync.WaitGroup
	errs := make([]error, len(servers))
	for i, serve := range servers {
		wg.Go(func() {
			if errs[i] = serve(); errs[i] != nil {
				stop()
			}
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}

// webServer starts the sysop web interface. It is plain HTTP and meant for
// loopback: the proxy in front of it terminates TLS, and the session cookie is
// marked Secure, so it is never sent over an unencrypted link from a browser.
func webServer(ctx context.Context, srv *bbs.Server, addr string, log *slog.Logger) (func() error, error) {
	handler, err := web.New(srv, log)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	hs := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    32 << 10,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	log.Info("sysop web interface listening", "addr", ln.Addr().String())
	return func() error {
		go func() {
			<-ctx.Done()
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = hs.Shutdown(shutdown)
		}()
		if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}, nil
}

func promote(st *store.Store, handle string, log *slog.Logger) error {
	u, err := st.BootstrapSysop(handle)
	if err != nil {
		return fmt.Errorf("-sysop %q: %w", handle, err)
	}
	log.Info("promoted to sysop", "handle", u.Handle)
	return nil
}

const (
	mailQueueSize = 256
	mailDrainTime = 10 * time.Second
)

func newMailQueue(o options, log *slog.Logger) (*mailer.Queue, error) {
	sender, err := mailer.NewSMTPSender(mailer.SMTPConfig{
		Host:     o.smtpHost,
		Port:     o.smtpPort,
		Username: o.smtpUser,
		Password: os.Getenv(smtpPasswordEnv),
		From:     o.smtpFrom,
		FromName: o.name,
	})
	if err != nil {
		return nil, err
	}
	log.Info("email enabled", "smtp", o.smtpHost, "port", o.smtpPort)
	return mailer.NewQueue(sender, log, mailQueueSize), nil
}

// ftnConfig turns the -ftn-* flags into the BBS's FidoNet setup.
func ftnConfig(o options) (bbs.FTNConfig, error) {
	var c bbs.FTNConfig
	var err error
	if c.Addresses, err = ftn.ParseAddrList(o.ftnAddress); err != nil {
		return c, fmt.Errorf("-ftn-address: %w", err)
	}
	if c.Routes.Direct, err = ftn.ParseAddrList(o.ftnDirect); err != nil {
		return c, fmt.Errorf("-ftn-direct: %w", err)
	}
	if o.ftnVia != "" {
		if c.Routes.Via, err = ftn.ParseAddr(o.ftnVia); err != nil {
			return c, fmt.Errorf("-ftn-via: %w", err)
		}
	}
	if o.ftnOutbound != "" {
		if len(c.Addresses) == 0 {
			return c, errors.New("-ftn-outbound needs -ftn-address")
		}
		c.Outbound = ftn.Outbound{Root: o.ftnOutbound, DefaultZone: o.ftnZone}
	}
	c.SysopName = o.ftnSysop
	return c, nil
}

// filesServer starts the download server for file links. Like the sysop
// interface it is plain HTTP for loopback, behind a TLS proxy. There is no
// write timeout: a large file over a slow line takes as long as it takes.
func filesServer(ctx context.Context, addr string, handler http.Handler, log *slog.Logger) (func() error, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	hs := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	log.Info("download server listening", "addr", ln.Addr().String())
	return func() error {
		go func() {
			<-ctx.Done()
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = hs.Shutdown(shutdown)
		}()
		if err := hs.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}, nil
}
