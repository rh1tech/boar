// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"crypto/rsa"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"

	"boar/internal/store"
	"boar/internal/term"
)

const (
	maxKeyLineLen = 2000
	minRSABits    = 2048
)

// parseSSHKey reads one authorized_keys line and rejects weak key types.
func parseSSHKey(line string) (key ssh.PublicKey, comment string, err error) {
	key, comment, _, _, err = ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(line)))
	if err != nil {
		return nil, "", &store.InputError{Msg: "That doesn't look like a public key (it starts with ssh-ed25519, ssh-rsa, ...)."}
	}
	switch key.Type() {
	case ssh.KeyAlgoDSA:
		return nil, "", &store.InputError{Msg: "DSA keys are too weak. Use ed25519 instead."}
	case ssh.KeyAlgoRSA:
		ck, ok := key.(ssh.CryptoPublicKey)
		if !ok {
			return nil, "", &store.InputError{Msg: "Unreadable RSA key."}
		}
		if rk, ok := ck.CryptoPublicKey().(*rsa.PublicKey); !ok || rk.N.BitLen() < minRSABits {
			return nil, "", &store.InputError{Msg: "RSA keys must be at least 2048 bits. ed25519 is better still."}
		}
	}
	return key, comment, nil
}

// manageSSHKeys lists the caller's keys and lets them add or remove one.
func (s *session) manageSSHKeys() error {
	for {
		keys, err := s.srv.store.SSHKeys(s.user.ID)
		if err != nil {
			return err
		}
		s.header("SSH Keys")
		lines := []string{
			colDim + "With a key here, ssh signs you in without a password. Paste the",
			colDim + "contents of " + colLabel + "~/.ssh/id_ed25519.pub" + colDim + " (never the private key).",
			separator,
		}
		if len(keys) == 0 {
			lines = append(lines, colLabel+"No keys yet.")
		}
		for i, k := range keys {
			lines = append(lines, fmt.Sprintf("%s%d  %s%s %s%s", colValue, i+1, colLabel, safe(k.Fingerprint), colDim, safe(k.Comment)))
		}
		s.box(fmt.Sprintf("Your keys (%d of %d)", len(keys), store.MaxSSHKeys), lines...)
		words := []string{"Add", "Remove", "Quit"}
		if len(keys) == 0 {
			words = []string{"Add", "Quit"}
		}
		c, err := s.actionPrompt("", words...)
		if err != nil || c == 'Q' {
			return err
		}
		if c == 'A' {
			err = s.addSSHKey()
		} else {
			err = s.removeSSHKey(keys)
		}
		if err != nil {
			return err
		}
	}
}

func (s *session) addSSHKey() error {
	line, err := s.prompt("|07Public key|08: |15", maxKeyLineLen)
	if err != nil || line == "" {
		return err
	}
	key, comment, err := parseSSHKey(line)
	if err != nil {
		return s.reportError("parse ssh key", err)
	}
	authorized := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
	k, err := s.srv.store.AddSSHKey(s.user.ID, ssh.FingerprintSHA256(key), authorized, comment)
	if err != nil {
		return s.reportError("add ssh key", err)
	}
	s.srv.log.Info("ssh key added", "user", s.user.Handle, "fingerprint", k.Fingerprint)
	s.printf("%sKey added: %s\n", colOK, safe(term.Truncate(k.Fingerprint, s.width()-12)))
	return s.pause()
}

func (s *session) removeSSHKey(keys []store.SSHKey) error {
	if len(keys) == 0 {
		return nil
	}
	n, ok, err := s.pickNumber(len(keys), "Remove key")
	if err != nil || !ok {
		return err
	}
	if err := s.srv.store.DeleteSSHKey(s.user.ID, keys[n-1].ID); err != nil {
		return s.reportError("remove ssh key", err)
	}
	s.srv.log.Info("ssh key removed", "user", s.user.Handle, "key", strconv.FormatInt(keys[n-1].ID, 10))
	return nil
}
