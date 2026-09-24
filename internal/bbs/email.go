package bbs

import (
	"errors"
	"fmt"
	"strings"

	"boar/internal/mailer"
	"boar/internal/store"
)

// MailQueue accepts outgoing email; *mailer.Queue implements it.
type MailQueue interface {
	Enqueue(m mailer.Message) error
}

const replyFooter = "\n\n-- \nReplies to this email are not delivered. Call the BBS to reply."

func (s *Server) emailEnabled() bool { return s.cfg.Mail != nil }

// callHint tells an email reader how to reach the BBS.
func (s *Server) callHint() string {
	if s.cfg.PublicAddress == "" {
		return "Call " + s.cfg.Name + " to read it."
	}
	return "Call " + s.cfg.Name + " at " + s.cfg.PublicAddress + " to read it."
}

func (s *Server) sendEmail(m mailer.Message) error {
	if err := s.cfg.Mail.Enqueue(m); err != nil {
		s.log.Warn("email not queued", "err", err)
		return err
	}
	return nil
}

// emailNewMail tells a recipient about new BBS mail, as they asked: a short
// notice (skipped while they're online and saw it live) or a full copy.
func (s *Server) emailNewMail(to store.User, from string, msg store.Message, online bool) {
	if !s.emailEnabled() || to.Email == "" {
		return
	}
	key := userKey(to.ID)
	var m mailer.Message
	switch to.EmailMode {
	case store.EmailNotice:
		if online || s.limit.emailNotices.blocked(key) {
			return
		}
		s.limit.emailNotices.hit(key)
		m = mailer.Message{
			To:      to.Email,
			Subject: fmt.Sprintf("New mail on %s from %s", s.cfg.Name, from),
			Body:    fmt.Sprintf("%s sent you a message on %s:\n\n  %s\n\n%s", from, s.cfg.Name, msg.Subject, s.callHint()) + replyFooter,
		}
	case store.EmailCopy:
		if s.limit.emailCopies.blocked(key) {
			return
		}
		s.limit.emailCopies.hit(key)
		m = mailer.Message{
			To:      to.Email,
			Subject: fmt.Sprintf("[%s] %s", s.cfg.Name, msg.Subject),
			Body:    fmt.Sprintf("From: %s\nDate: %s\n\n%s", from, longDate(msg.SentAt), msg.Body) + replyFooter,
		}
	default:
		return
	}
	_ = s.sendEmail(m) // failures are logged; the BBS copy was delivered regardless
}

func (s *session) emailSettings() error {
	for {
		if err := s.refreshUser(); err != nil {
			return err
		}
		s.header("Email")
		if stop, err := s.awaitingApproval("Email"); stop || err != nil {
			return err
		}
		if !s.srv.emailEnabled() {
			s.printf("\n  %sThis BBS has no mail server set up, so email is off.\n\n", colDim)
			return s.pause()
		}
		pending, hasPending, err := s.srv.store.PendingEmail(s.user.ID)
		if err != nil {
			return err
		}
		address := colDim + "not set"
		if s.user.Email != "" {
			address = colBright + safe(s.user.Email) + colOK + "  (verified)"
		}
		s.printf("\n  %sAddress  %s: %s\n", colInfo, colDim, address)
		if hasPending {
			s.printf("  %sWaiting  %s: %s%s %s(check your inbox for the code)\n", colInfo, colDim, colValue, safe(pending), colDim)
		}
		s.printf("  %sNew mail %s: %s%s\n", colInfo, colDim, colLabel, s.user.EmailMode)
		s.printf("\n  %sWe only email you, and only about mail sent to you here. Nothing is shown\n  to other callers.\n", colDim)

		words := []string{"Set address"}
		if hasPending {
			words = append(words, "Verify code")
		}
		if s.user.Email != "" {
			words = append(words, "New-mail email", "Remove address")
		}
		words = append(words, "Quit")
		s.print("\n" + actions(words...) + " |08» |15")
		k, err := s.choose(keysOf(words...))
		if err != nil || k == 'Q' {
			return err
		}
		switch k {
		case 'S':
			err = s.setEmailAddress()
		case 'V':
			err = s.verifyEmailCode()
		case 'N':
			err = s.chooseEmailMode()
		case 'R':
			err = s.removeEmail()
		}
		if err != nil {
			return err
		}
	}
}

func (s *session) setEmailAddress() error {
	addr, err := s.prompt("\n|07Email address|08: |15", store.MaxEmailLen)
	if err != nil || addr == "" {
		return err
	}
	if err := store.ValidateEmail(addr); err != nil {
		return s.reportError("validate email", err)
	}
	key := userKey(s.user.ID)
	// Limit per address too, so many accounts can't flood a stranger's inbox.
	target := "email:" + strings.ToLower(strings.TrimSpace(addr))
	if s.srv.limit.emailVerify.blocked(key) || s.srv.limit.emailTarget.blocked(target) {
		s.printf("%sEnough codes have gone out for now. Try again later.\n", colAlert)
		return s.pause()
	}
	code, err := s.srv.store.StartEmailVerification(s.user.ID, addr)
	if err != nil {
		return s.reportError("start email verification", err)
	}
	s.srv.limit.emailVerify.hit(key)
	s.srv.limit.emailTarget.hit(target)
	err = s.srv.sendEmail(mailer.Message{
		To:      strings.TrimSpace(addr),
		Subject: "Your " + s.srv.cfg.Name + " code: " + code,
		Body: fmt.Sprintf("Someone (hopefully you, %s) asked to use this address on %s.\n\n"+
			"Your code is %s. It works for %d minutes.\n\n"+
			"If this wasn't you, ignore this email and nothing will happen.",
			s.user.Handle, s.srv.cfg.Name, code, int(store.EmailCodeTTL.Minutes())) + replyFooter,
	})
	if err != nil {
		s.printf("%sThe mail system is busy. Try again in a few minutes.\n", colAlert)
		return s.pause()
	}
	s.srv.log.Info("email verification sent", "user", s.user.Handle)
	s.printf("%sA code is on its way to %s.\n", colOK, safe(strings.TrimSpace(addr)))
	return s.verifyEmailCode()
}

func (s *session) verifyEmailCode() error {
	code, err := s.prompt("|07Code from the email |08(Enter = later)|07: |15", 10)
	if err != nil || code == "" {
		return err
	}
	u, err := s.srv.store.ConfirmEmail(s.user.ID, code)
	var ie *store.InputError
	if errors.As(err, &ie) {
		s.printf("%s%s\n", colAlert, safe(ie.Msg))
		return s.pause()
	}
	if err != nil {
		return s.reportError("confirm email", err)
	}
	s.user = u
	s.printf("%sVerified! You'll get a short notice when new mail arrives while you're away.\n", colOK)
	return s.pause()
}

func (s *session) chooseEmailMode() error {
	s.printf("\n  %s1%s) Off\n  %s2%s) Notice only %s(who wrote and the subject, when you're offline)\n  %s3%s) Full copy of each message\n",
		colBright, colLabel, colBright, colLabel, colDim, colBright, colLabel)
	s.print("|07Choice|08: |15")
	k, err := s.choose("123")
	if err != nil {
		return err
	}
	mode := store.EmailMode(k - '1')
	u, err := s.srv.store.SetEmailMode(s.user.ID, mode)
	if err != nil {
		return s.reportError("set email mode", err)
	}
	s.user = u
	return nil
}

func (s *session) removeEmail() error {
	ok, err := s.yesNo("Forget your email address?", false)
	if err != nil || !ok {
		return err
	}
	u, err := s.srv.store.RemoveEmail(s.user.ID)
	if err != nil {
		return s.reportError("remove email", err)
	}
	s.user = u
	return nil
}

// emailMeCopy sends a message the caller is reading to their own address.
func (s *session) emailMeCopy(m store.Message) error {
	if err := s.refreshUser(); err != nil {
		return err
	}
	if s.user.Email == "" {
		s.printf("%sAdd and verify an email address in Settings first.\n", colAlert)
		return s.pause()
	}
	key := userKey(s.user.ID)
	if s.srv.limit.emailExport.blocked(key) {
		s.printf("%sThat's plenty of email for now. Try again later.\n", colAlert)
		return s.pause()
	}
	s.srv.limit.emailExport.hit(key)
	err := s.srv.sendEmail(mailer.Message{
		To:      s.user.Email,
		Subject: fmt.Sprintf("[%s] %s", s.srv.cfg.Name, m.Subject),
		Body: fmt.Sprintf("From: %s\nTo: %s\nDate: %s\n\n%s", s.handleOf(m.FromID), s.handleOf(m.ToID),
			longDate(m.SentAt), m.Body) + replyFooter,
	})
	if err != nil {
		s.printf("%sThe mail system is busy. Try again in a few minutes.\n", colAlert)
		return s.pause()
	}
	s.printf("%sOn its way to %s.\n", colOK, safe(s.user.Email))
	return s.pause()
}
