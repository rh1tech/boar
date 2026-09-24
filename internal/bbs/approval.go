package bbs

import (
	"fmt"

	"boar/internal/store"
	"boar/internal/term"
)

// notifySysops sends a notice to every sysop who is online.
func (s *Server) notifySysops(text string) {
	ids, err := s.store.SysopIDs()
	if err != nil {
		s.log.Warn("listing sysops for a notice", "err", err)
		return
	}
	for _, id := range ids {
		s.nodes.notify(id, text)
	}
}

// awaitingApproval tells a caller who isn't approved yet that what they
// tried needs approval. It reports true when they were stopped.
func (s *session) awaitingApproval(what string) (bool, error) {
	if s.user.Validated {
		return false, nil
	}
	s.printf("\n%s%s opens up once a sysop approves your account.\n", colAlert, what)
	s.printf("%sUntil then you can read the boards and news, and mail the sysop.\n", colDim)
	return true, s.pause()
}

func (s *session) approvalNotice() {
	if !s.user.Validated {
		s.printf("\n  %sYour account is waiting for sysop approval.%s You can read the boards\n  and news and mail the sysop meanwhile.\n", colAlert, colDim)
	}
}

// pendingNote is the "(2 waiting)" hint shown to sysops.
func (s *session) pendingNote() string {
	pending, err := s.srv.store.PendingUsers()
	if err != nil || len(pending) == 0 {
		return ""
	}
	return fmt.Sprintf("%s(%d waiting)", colAlert, len(pending))
}

// sysopApprovals walks the queue of new accounts.
func (s *session) sysopApprovals() error {
	for {
		pending, err := s.srv.store.PendingUsers()
		if err != nil {
			return err
		}
		s.header("New Callers")
		if len(pending) == 0 {
			s.printf("\n  %sNobody is waiting for approval.\n\n", colDim)
			return s.pause()
		}
		s.printf("\n%s%4s  %s %s %s\n", colDim, "#", term.Pad("Handle", 20), term.Pad("Location", 20), "Signed up")
		for i, u := range pending {
			s.printf("%s%4d  %s%s %s%s %s%s\n", colValue, i+1, colHandle, safe(term.Pad(u.Handle, 20)),
				colLabel, safe(term.Pad(u.Location, 20)), colInfo, ago(u.CreatedAt))
		}
		n, ok, err := s.pickNumber(len(pending), "Review caller")
		if err != nil || !ok {
			return err
		}
		if err := s.reviewCaller(pending[n-1]); err != nil {
			return err
		}
	}
}

func (s *session) reviewCaller(u store.User) error {
	s.showAccount(u)
	s.print("\n" + actions("Approve", "Reject and delete", "Mail them", "Skip") + " |08» |15")
	k, err := s.choose("ARMS")
	if err != nil || k == 'S' {
		return err
	}
	if ok, err := s.requireSysop(); err != nil || !ok {
		return err
	}
	switch k {
	case 'A':
		if err := s.srv.store.ValidateUser(s.user.ID, u.ID); err != nil {
			return s.reportError("approve user", err)
		}
		s.audit("approved %s", u.Handle)
		s.srv.nodes.notify(u.ID, "A sysop approved your account. Welcome in!")
	case 'R':
		_, err = s.sysopDeleteUser(u)
	case 'M':
		err = s.compose(draft{to: []store.User{u}})
	}
	return err
}
