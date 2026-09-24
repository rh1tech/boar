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
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"boar/internal/bbs"
	"boar/internal/doors"
	"boar/internal/mailer"
	"boar/internal/store"
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
