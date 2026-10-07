// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"context"
	"time"

	"boar/internal/ftntoss"
)

const ftnTossInterval = 30 * time.Second

// RunTosser imports FidoNet echomail from the given inbound directories until
// ctx is cancelled. Empty paths are ignored. Callers can read tossed mail
// from the Echoes menu.
func (s *Server) RunTosser(ctx context.Context, inbounds ...string) error {
	var dirs []string
	for _, d := range inbounds {
		if d != "" {
			dirs = append(dirs, d)
		}
	}
	if len(dirs) == 0 {
		<-ctx.Done()
		return nil
	}
	s.log.Info("ftn tosser running", "inbound", dirs)
	s.tossOnce(dirs)
	t := time.NewTicker(ftnTossInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			s.tossOnce(dirs)
		}
	}
}

func (s *Server) tossOnce(dirs []string) {
	t := ftntoss.Tosser{DB: s.store, Own: s.cfg.FTN.Addresses}
	if s.cfg.Files != "" {
		t.Files = &ftntoss.FileStore{Root: s.cfg.Files}
	}
	st, err := t.TossAll(dirs...)
	if err != nil {
		s.log.Warn("ftn toss failed", "err", err)
		return
	}
	if st.FilesIn > 0 || st.FilesBad > 0 {
		s.log.Info("file echoes tossed", "in", st.FilesIn, "set_aside", st.FilesBad)
	}
	if st.Files > 0 {
		s.log.Info("ftn tossed",
			"files", st.Files, "stored", st.Stored, "netmail", st.Netmail,
			"duplicate", st.Duplicate, "skipped", st.Skipped, "failed", st.Failed)
	}
}
