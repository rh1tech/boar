// SPDX-License-Identifier: GPL-3.0-or-later

package ftn

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Binkley-style outbound (FTS-5005): the directory layout binkd reads mail
// to send from. Packets for 2:410/0 in the default zone go to
// out/019a0000.?ut, other zones to out.00z/, points to 019a0009.pnt/0000000p.?ut.

// Flavour is how urgently a mailer delivers: crash calls now, hold waits to
// be polled, normal goes out on the next scheduled call.
type Flavour byte

const (
	Normal Flavour = 'o'
	Crash  Flavour = 'c'
	Hold   Flavour = 'h'
	Direct Flavour = 'd'
)

// ErrBusy means the mailer is in a session with that node right now.
var ErrBusy = errors.New("outbound is busy")

// Outbound is one domain's outbound tree.
type Outbound struct {
	Root        string // e.g. /var/spool/ftn/out
	DefaultZone int    // the zone that lives in Root itself
	FileMode    fs.FileMode
}

func (o Outbound) mode() fs.FileMode {
	if o.FileMode == 0 {
		return 0o660 // mailer and BBS share a group
	}
	return o.FileMode
}

// base is the directory and file stem for a, without extension.
func (o Outbound) base(a Addr) (string, string) {
	dir := o.Root
	if a.Zone != o.DefaultZone {
		dir = fmt.Sprintf("%s.%03x", o.Root, a.Zone)
	}
	stem := fmt.Sprintf("%04x%04x", a.Net, a.Node)
	if a.IsPoint() {
		return filepath.Join(dir, stem+".pnt"), fmt.Sprintf("%08x", a.Point)
	}
	return dir, stem
}

// PacketPath is where an uncompressed packet for a waits: the .?ut file.
func (o Outbound) PacketPath(a Addr, f Flavour) string {
	dir, stem := o.base(a)
	if f == Normal {
		return filepath.Join(dir, stem+".out")
	}
	return filepath.Join(dir, stem+"."+string(f)+"ut")
}

// Queue adds messages for dest to its outbound packet, creating the packet
// when there is none. The node's .bsy flag is held while the file changes, so
// the mailer never sends a half-written packet.
func (o Outbound) Queue(dest Addr, f Flavour, header Packet, msgs []PackedMessage) error {
	dir, stem := o.base(dest)
	if err := os.MkdirAll(dir, 0o2770); err != nil {
		return err
	}
	bsy := filepath.Join(dir, stem+".bsy")
	lock, err := os.OpenFile(bsy, os.O_CREATE|os.O_EXCL|os.O_WRONLY, o.mode())
	if errors.Is(err, fs.ErrExist) {
		return ErrBusy
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(lock, "%d boar\n", os.Getpid())
	lock.Close()
	defer os.Remove(bsy)

	path := o.PacketPath(dest, f)
	pkt := header
	if data, err := os.Open(path); err == nil {
		existing, rerr := ReadPacket(data)
		data.Close()
		if rerr != nil {
			return fmt.Errorf("existing packet %s: %w", path, rerr)
		}
		pkt = *existing
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	pkt.Messages = append(pkt.Messages, msgs...)

	tmp, err := os.CreateTemp(dir, stem+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed
	if _, err := pkt.WriteTo(tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(o.mode()); err != nil { // not limited by the process umask
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
