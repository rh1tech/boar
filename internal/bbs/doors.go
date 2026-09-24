package bbs

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"boar/internal/doors"
	"boar/internal/term"
)

const (
	doorReadChunk = 4096
	eventDoor     = "door"
	doorSysopName = "Sysop" // doors show this when a caller pages the sysop
)

// visibleDoors lists the doors this caller may open.
func (s *session) visibleDoors() []doors.Door {
	var out []doors.Door
	for _, d := range s.srv.cfg.Doors {
		if !d.SysopOnly || s.user.Sysop {
			out = append(out, d)
		}
	}
	return out
}

// doorLock returns the mutex that keeps a single-node door to one caller.
func (s *Server) doorLock(key string) *sync.Mutex {
	lock, _ := s.doorLocks.LoadOrStore(key, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func (s *session) doorsMenu() error {
	for {
		s.setActivity("Door games")
		list := s.visibleDoors()
		s.header("Doors")
		if len(list) == 0 {
			s.printf("\n  %sNo doors are open today.\n\n", colDim)
			return s.pause()
		}
		s.print("\n")
		for i, d := range list {
			s.printf("%s%4d  %s%s\n", colValue, i+1, colHandle, safe(d.Name))
			if d.Description != "" {
				s.printf("        %s%s\n", colDim, safe(d.Description))
			}
		}
		n, ok, err := s.pickNumber(len(list), "Open door")
		if err != nil || !ok {
			return err
		}
		if err := s.runDoor(list[n-1]); err != nil {
			return err
		}
	}
}

// runDoor writes drop files, starts the door and connects the caller to it
// until it exits, the time limit passes or the caller hangs up.
func (s *session) runDoor(d doors.Door) error {
	if d.SingleNode {
		lock := s.srv.doorLock(d.Key)
		if !lock.TryLock() {
			s.printf("\n%sSomeone else is in %s right now. Try again soon.\n", colAlert, safe(d.Name))
			return s.pause()
		}
		defer lock.Unlock()
	}
	s.setActivity("Playing " + d.Name)
	dropDir, err := filepath.Abs(filepath.Join(s.srv.cfg.DoorsDir, "node"+strconv.Itoa(s.node.id)))
	if err != nil {
		return s.reportError("door drop dir", err)
	}
	_, rows := s.tc.Size()
	caller := doors.Caller{
		Node: s.node.id, UserID: s.user.ID, Handle: s.user.Handle, Location: s.user.Location,
		Sysop: s.user.Sysop, Calls: s.user.Calls, LastLogin: s.user.LastLogin, MinutesLeft: d.MaxMinutes,
		ScreenRows: rows, ANSI: s.color, BBSName: s.srv.cfg.Name, SysopName: doorSysopName,
	}
	if err := doors.WriteDropFiles(dropDir, caller, time.Now()); err != nil {
		return s.reportError("write drop files", err)
	}

	limit := time.Duration(d.MaxMinutes) * s.srv.doorMinute
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	s.print("|CL")
	s.printf("%sOpening %s%s%s... %s(time limit %d min)|RE\r\n", colDim, colBright, safe(d.Name), colDim, colDim, d.MaxMinutes)
	p, err := doors.Start(ctx, d, doors.Vars{
		"node":    strconv.Itoa(s.node.id),
		"dropdir": dropDir,
		"doorsys": filepath.Join(dropDir, doors.DoorSys),
		"dorinfo": filepath.Join(dropDir, doors.DorInfo),
		"handle":  s.user.Handle,
		"userid":  strconv.FormatInt(s.user.ID, 10),
		"minutes": strconv.Itoa(d.MaxMinutes),
	})
	if err != nil {
		s.srv.log.Error("door failed to start", "door", d.Key, "err", err)
		s.printf("\n%sThat door is stuck shut. The sysop has been told.\n", colAlert)
		return s.pause()
	}
	s.srv.event(eventDoor, s.user, s.ip, "opened "+d.Key)
	started := time.Now()
	bridgeErr := s.bridgeDoor(ctx, p)
	p.Kill()
	if stderr := p.Stderr(); stderr != "" {
		s.srv.log.Debug("door stderr", "door", d.Key, "stderr", stderr)
	}
	s.rend = term.NewRenderer(s.color) // the door left the screen in its own state
	if bridgeErr != nil {
		return bridgeErr // the caller hung up
	}
	s.print("|RE\n\n")
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		s.printf("%sTime's up in %s.\n", colAlert, safe(d.Name))
	}
	s.printf("%sBack from %s after %s.\n", colDim, safe(d.Name), shortDuration(time.Since(started)))
	return s.pause()
}

// bridgeDoor copies door output to the caller (on another goroutine) and the
// caller's keys to the door, until the door is done.
func (s *session) bridgeDoor(ctx context.Context, p *doors.Process) error {
	outDone := make(chan struct{})
	go func() {
		defer close(outDone)
		buf := make([]byte, doorReadChunk)
		for {
			n, err := p.Read(buf)
			if n > 0 {
				if _, werr := s.tc.Write(term.TranscodeCP437(buf[:n], s.cs)); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		select {
		case ev := <-s.in.events:
			b, err := s.in.accept(ev)
			if err != nil {
				p.Kill()
				<-outDone
				return err
			}
			if data := s.doorInput(b); len(data) > 0 {
				_, _ = p.Write(data) // a door that stopped reading is about to exit anyway
			}
		case <-outDone:
			return nil
		case <-p.Done():
			<-outDone // let its last screen through
			return nil
		case <-ctx.Done():
			p.Kill()
			<-outDone
			return nil
		}
	}
}

// doorInput turns a key from the caller into what a DOS-era door expects:
// CP437 bytes. Escape sequences (cursor keys) pass through untouched.
func (s *session) doorInput(b byte) []byte {
	if b < 0x80 || s.cs != term.UTF8 {
		return []byte{b}
	}
	r, ok := s.decodeHigh(b)
	if !ok {
		return nil
	}
	return term.Encode(term.CP437, string(r))
}

// doorCountNote is the main-menu hint next to the Doors entry.
func (s *session) doorCountNote() string {
	n := len(s.visibleDoors())
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%s(%d)", colDim, n)
}
