// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
)

// sshSetupTimeout bounds the handshake plus the wait for a shell request, so
// half-open SSH connections cannot pile up before they ever take a node.
const sshSetupTimeout = 30 * time.Second

// keyUserExtension carries the account a public key matched from the SSH
// handshake to the session.
const keyUserExtension = "boar-user-id"

// maxSSHAuthTries allows for agents holding several keys before the client
// falls back to keyboard-interactive.
const maxSSHAuthTries = 12

// ServeSSH accepts SSH callers until ctx is cancelled.
//
// A public key registered in Settings signs the caller straight in. Any
// other client is let through with keyboard-interactive or password auth
// (whatever it sends is not checked) and then logs in to the BBS itself,
// inside the encrypted channel. "none" auth is refused so that clients
// offer their keys first.
func (s *Server) ServeSSH(ctx context.Context, ln net.Listener, hostKey ssh.Signer) error {
	open := func(ssh.ConnMetadata) (*ssh.Permissions, error) { return &ssh.Permissions{}, nil }
	cfg := &ssh.ServerConfig{
		ServerVersion: "SSH-2.0-BoarBBS",
		MaxAuthTries:  maxSSHAuthTries,
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			u, err := s.store.UserBySSHKey(ssh.FingerprintSHA256(key))
			if err != nil {
				return nil, errors.New("unknown key")
			}
			return &ssh.Permissions{Extensions: map[string]string{keyUserExtension: strconv.FormatInt(u.ID, 10)}}, nil
		},
		KeyboardInteractiveCallback: func(c ssh.ConnMetadata, _ ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
			return open(c)
		},
		PasswordCallback: func(c ssh.ConnMetadata, _ []byte) (*ssh.Permissions, error) { return open(c) },
	}
	cfg.AddHostKey(hostKey)
	return s.acceptLoop(ctx, ln, func(nc net.Conn) { s.handleSSH(nc, cfg) })
}

func (s *Server) handleSSH(nc net.Conn, cfg *ssh.ServerConfig) {
	log := s.log.With("remote", nc.RemoteAddr().String(), "via", "ssh")
	defer nc.Close()
	if err := nc.SetDeadline(time.Now().Add(sshSetupTimeout)); err != nil {
		return
	}
	sconn, chans, reqs, err := ssh.NewServerConn(nc, cfg)
	if err != nil {
		log.Debug("ssh handshake failed", "err", err)
		return
	}
	defer sconn.Close()
	go ssh.DiscardRequests(reqs)

	for nch := range chans {
		if nch.ChannelType() != "session" {
			_ = nch.Reject(ssh.UnknownChannelType, "only interactive sessions are supported")
			continue
		}
		ch, chReqs, err := nch.Accept()
		if err != nil {
			log.Debug("ssh channel accept failed", "err", err)
			return
		}
		t := newSSHTerm(ch, s.cfg.IdleTimeout)
		if sconn.Permissions != nil {
			t.userID, _ = strconv.ParseInt(sconn.Permissions.Extensions[keyUserExtension], 10, 64)
		}
		ready := make(chan struct{})
		go t.handleRequests(chReqs, ready)
		select {
		case <-ready:
		case <-time.After(sshSetupTimeout):
			_ = t.Close()
			return
		}
		if err := nc.SetDeadline(time.Time{}); err != nil {
			return
		}
		go rejectExtraChannels(chans)
		s.serveCaller(t, nc.RemoteAddr(), true, log)
		_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0})) // courtesy; we hang up anyway
		_ = t.Close()
		return
	}
}

func rejectExtraChannels(chans <-chan ssh.NewChannel) {
	for nch := range chans {
		_ = nch.Reject(ssh.ResourceShortage, "one session per connection")
	}
}

// LoadOrCreateHostKey reads an OpenSSH private key from path, generating an
// Ed25519 key there on first run.
func LoadOrCreateHostKey(path string) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		data, err = generateHostKey(path)
	}
	if err != nil {
		return nil, fmt.Errorf("host key %s: %w", path, err)
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("host key %s: %w", path, err)
	}
	return signer, nil
}

func generateHostKey(path string) ([]byte, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(priv, "boar host key")
	if err != nil {
		return nil, err
	}
	data := pem.EncodeToMemory(block)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return nil, err
	}
	return data, nil
}
