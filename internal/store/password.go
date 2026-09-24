package store

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
)

const (
	// DefaultKDFIterations follows the OWASP 2023 recommendation for
	// PBKDF2-HMAC-SHA256.
	DefaultKDFIterations = 600_000

	saltLen = 16
	keyLen  = 32
)

// dummySalt lets failed lookups cost the same as real password checks.
var dummySalt = make([]byte, saltLen)

type credential struct {
	hash []byte
	salt []byte
	iter int
}

func newCredential(password string, iter int) (credential, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return credential{}, fmt.Errorf("store: generate salt: %w", err)
	}
	hash, err := deriveKey(password, salt, iter)
	if err != nil {
		return credential{}, err
	}
	return credential{hash: hash, salt: salt, iter: iter}, nil
}

func (c credential) matches(password string) (bool, error) {
	if c.iter <= 0 || len(c.salt) == 0 {
		return false, errors.New("store: corrupt credential")
	}
	key, err := deriveKey(password, c.salt, c.iter)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(key, c.hash) == 1, nil
}

func deriveKey(password string, salt []byte, iter int) ([]byte, error) {
	key, err := pbkdf2.Key(sha256.New, password, salt, iter, keyLen)
	if err != nil {
		return nil, fmt.Errorf("store: derive key: %w", err)
	}
	return key, nil
}
