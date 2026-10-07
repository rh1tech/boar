// SPDX-License-Identifier: GPL-3.0-or-later

// Package filelink makes and checks the download links the BBS hands out for
// file-echo files: who may fetch which file until when, signed so that the
// link cannot be edited into another file or a later expiry, and so that the
// web server needs no session of its own.
package filelink

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"os"
	"strings"
	"time"
)

const (
	keyBytes = 32
	sigBytes = 16
)

// Link is what a token grants.
type Link struct {
	FileID, UserID int64
	Expires        time.Time
}

var b64 = base64.RawURLEncoding

// Sign returns the token for l.
func Sign(key []byte, l Link) string {
	var p [24]byte
	binary.BigEndian.PutUint64(p[0:], uint64(l.FileID))
	binary.BigEndian.PutUint64(p[8:], uint64(l.UserID))
	binary.BigEndian.PutUint64(p[16:], uint64(l.Expires.Unix()))
	return b64.EncodeToString(p[:]) + "." + b64.EncodeToString(mac(key, p[:]))
}

// ErrInvalid covers every way a token can be wrong: the web server says the
// same thing for all of them.
var ErrInvalid = errors.New("filelink: invalid or expired link")

// Verify checks a token and returns what it grants.
func Verify(key []byte, token string, now time.Time) (Link, error) {
	payload, sig, ok := strings.Cut(token, ".")
	if !ok {
		return Link{}, ErrInvalid
	}
	p, err := b64.DecodeString(payload)
	if err != nil || len(p) != 24 {
		return Link{}, ErrInvalid
	}
	s, err := b64.DecodeString(sig)
	if err != nil || !hmac.Equal(s, mac(key, p)) {
		return Link{}, ErrInvalid
	}
	l := Link{
		FileID:  int64(binary.BigEndian.Uint64(p[0:])),
		UserID:  int64(binary.BigEndian.Uint64(p[8:])),
		Expires: time.Unix(int64(binary.BigEndian.Uint64(p[16:])), 0),
	}
	if !now.Before(l.Expires) {
		return Link{}, ErrInvalid
	}
	return l, nil
}

func mac(key, payload []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte("boar-filelink-v1\x00"))
	h.Write(payload)
	return h.Sum(nil)[:sigBytes]
}

// LoadKey reads the signing key at path, creating it (mode 0600) the first
// time. Removing the file revokes every link handed out.
func LoadKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err == nil {
		if len(key) < keyBytes {
			return nil, errors.New("filelink: " + path + " is too short to be a key")
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key = make([]byte, keyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(key); err != nil {
		f.Close()
		return nil, err
	}
	return key, f.Close()
}
