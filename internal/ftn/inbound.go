// SPDX-License-Identifier: GPL-3.0-or-later

package ftn

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxPacketBytes = 16 << 20  // one .pkt
	maxBundleBytes = 256 << 20 // a ZIP day-bundle (rescans can be large)
)

// NamedPacket is a packet together with a label for logs (file name, or
// "bundle.zip:inner.pkt").
type NamedPacket struct {
	Name string
	Pkt  *Packet
}

// EachPacket calls fn for every packet in name: a lone .pkt, or each .pkt
// inside a ZIP mail bundle (.su0, .mo1, …). Unreadable members of a ZIP are
// skipped so one corrupt packet does not block the rest of a rescan.
func EachPacket(name string, fn func(NamedPacket) error) error {
	fi, err := os.Stat(name)
	if err != nil {
		return err
	}
	if fi.Size() > maxBundleBytes {
		return fmt.Errorf("%s: larger than %d bytes", name, maxBundleBytes)
	}

	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()

	head := make([]byte, 4)
	if _, err := io.ReadFull(f, head); err != nil {
		return err
	}
	if !bytes.Equal(head, []byte("PK\x03\x04")) {
		if fi.Size() > maxPacketBytes {
			return fmt.Errorf("%s: larger than %d bytes", name, maxPacketBytes)
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		p, err := ReadPacket(io.LimitReader(f, maxPacketBytes))
		if err != nil {
			return err
		}
		return fn(NamedPacket{filepath.Base(name), p})
	}

	zr, err := zip.OpenReader(name)
	if err != nil {
		return err
	}
	defer zr.Close()

	var lastErr error
	read := 0
	for _, zf := range zr.File {
		if !strings.EqualFold(filepath.Ext(zf.Name), ".pkt") || zf.UncompressedSize64 > maxPacketBytes {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", zf.Name, err)
			continue
		}
		p, err := ReadPacket(io.LimitReader(rc, maxPacketBytes))
		rc.Close()
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", zf.Name, err)
			continue
		}
		read++
		if err := fn(NamedPacket{filepath.Base(name) + ":" + zf.Name, p}); err != nil {
			return err
		}
	}
	if read == 0 && lastErr != nil {
		return lastErr
	}
	return nil
}

// ReadPackets opens a .pkt, or every .pkt inside a ZIP mail bundle.
func ReadPackets(name string) ([]NamedPacket, error) {
	var out []NamedPacket
	err := EachPacket(name, func(np NamedPacket) error {
		out = append(out, np)
		return nil
	})
	return out, err
}

// EachMessage calls fn for every netmail and echomail message in name.
func EachMessage(name string, fn func(Message) error) error {
	return EachPacket(name, func(np NamedPacket) error {
		for _, pm := range np.Pkt.Messages {
			if err := fn(ParseMessage(pm, np.Pkt.From, np.Pkt.To, DefaultCharset(np.Pkt.From))); err != nil {
				return err
			}
		}
		return nil
	})
}

// MessagesInFile parses every netmail and echomail message in name.
func MessagesInFile(name string) ([]Message, error) {
	var out []Message
	err := EachMessage(name, func(m Message) error {
		out = append(out, m)
		return nil
	})
	return out, err
}

// IsMailBundle reports whether name looks like a packed mail bundle or packet
// that binkd would drop in the inbound.
func IsMailBundle(name string) bool {
	base := filepath.Base(name)
	ext := strings.ToLower(filepath.Ext(base))
	if ext == ".pkt" {
		return true
	}
	// Day-of-week bundles: .su0 … .sa9, then .sua … .saz once a mailer has
	// sent ten in a day (and uppercase).
	if len(ext) == 4 {
		day := ext[1:3]
		switch day {
		case "su", "mo", "tu", "we", "th", "fr", "sa":
			c := ext[3]
			return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z')
		}
	}
	return false
}
