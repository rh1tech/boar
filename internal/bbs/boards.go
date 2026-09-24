package bbs

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"boar/internal/store"
	"boar/internal/term"
)

// boardsMenu lists the public message boards.
func (s *session) boardsMenu() error {
	for {
		s.setActivity("Message boards")
		boards, err := s.srv.store.Boards(s.user.ID)
		if err != nil {
			return err
		}
		s.header("Message Boards")
		s.printf("\n%s%4s  %s %s %s\n", colDim, "#", term.Pad("Board", 22), term.Pad("Posts", 12), "About")
		totalNew := 0
		for i, b := range boards {
			totalNew += b.New
			count := fmt.Sprintf("%d", b.Posts)
			if b.New > 0 {
				count += fmt.Sprintf(" %s(%d new)", colAlert, b.New)
			}
			lock := " "
			if b.SysopOnly {
				lock = colSysop + "*"
			}
			s.printf("%s%4d %s%s%s %s %s%s\n", colValue, i+1, lock, colHandle, safe(term.Pad(b.Name, 22)),
				padVisible(colLabel+count, 12), colDim, safe(b.Description))
		}
		s.printf("\n %s*%s = only the sysop posts here\n", colSysop, colDim)

		ans, err := s.prompt(fmt.Sprintf("\n|07Board # |08(|15N|08 = read all %d new, Enter = back)|07: |15", totalNew), 4)
		if err != nil || ans == "" {
			return err
		}
		if strings.EqualFold(ans, "n") {
			if err := s.newScan(boards); err != nil {
				return err
			}
			continue
		}
		n, convErr := strconv.Atoi(ans)
		if convErr != nil || n < 1 || n > len(boards) {
			s.printf("%sNo such board.\n", colAlert)
			if err := s.pause(); err != nil {
				return err
			}
			continue
		}
		if err := s.boardView(boards[n-1].Board); err != nil {
			return err
		}
	}
}

// boardView lists a board's posts and offers reading and posting.
func (s *session) boardView(b store.Board) error {
	for {
		s.setActivity("Reading " + b.Name)
		posts, err := s.srv.store.Posts(b.ID)
		if err != nil {
			return err
		}
		lastRead, err := s.srv.store.LastRead(s.user.ID, b.ID)
		if err != nil {
			return err
		}
		s.header(b.Name)
		if b.Description != "" {
			s.printf("%s %s\n", colDim, safe(b.Description))
		}
		if len(posts) == 0 {
			s.printf("\n  %sNo posts yet. Be the first!\n", colDim)
		} else {
			s.print("\n")
			if _, err := s.page(s.postListing(posts, lastRead), listHeadRows+1); err != nil {
				return err
			}
		}
		ans, err := s.prompt("\n|07Read # |08(|15P|08 = new post, |15N|08 = next unread, Enter = back)|07: |15", 4)
		if err != nil || ans == "" {
			return err
		}
		switch strings.ToUpper(ans) {
		case "P":
			err = s.writePost(b, draftPost{})
		case "N":
			err = s.readPosts(b, unreadPosts(posts, lastRead, s.user.ID), 0)
		default:
			n, convErr := strconv.Atoi(ans)
			if convErr != nil || n < 1 || n > len(posts) {
				s.printf("%sNo such post.\n", colAlert)
				err = s.pause()
			} else {
				err = s.readPosts(b, posts, n-1)
			}
		}
		if err != nil {
			return err
		}
	}
}

func unreadPosts(posts []store.Post, lastRead, me int64) []store.Post {
	var out []store.Post
	for _, p := range posts {
		if p.ID > lastRead && p.AuthorID != me {
			out = append(out, p)
		}
	}
	return out
}

func (s *session) postListing(posts []store.Post, lastRead int64) []string {
	subjW := max(s.width()-33, 10)
	rows := []string{fmt.Sprintf("%s%4s    %s %s Date", colDim, "#", term.Pad("Author", 16), term.Pad("Subject", subjW))}
	for i, p := range posts {
		mark, subjColor := "    ", colLabel
		if p.ID > lastRead && p.AuthorID != s.user.ID {
			mark, subjColor = " "+colAlert+"*"+colLabel+"  ", colBright
		}
		subject := p.Subject
		if p.ReplyTo != 0 {
			subject = "  " + subject // indent replies a little
		}
		rows = append(rows, fmt.Sprintf("%s%4d%s%s%s %s%s %s%s",
			colValue, i+1, mark,
			colHandle, safe(term.Pad(s.handleOf(p.AuthorID), 16)),
			subjColor, safe(term.Pad(subject, subjW)),
			colInfo, shortDate(p.PostedAt)))
	}
	return rows
}

// newScan walks every board with unread posts.
func (s *session) newScan(boards []store.BoardSummary) error {
	found := false
	for _, b := range boards {
		if b.New == 0 {
			continue
		}
		posts, err := s.srv.store.Posts(b.ID)
		if err != nil {
			return err
		}
		lastRead, err := s.srv.store.LastRead(s.user.ID, b.ID)
		if err != nil {
			return err
		}
		unread := unreadPosts(posts, lastRead, s.user.ID)
		if len(unread) == 0 {
			continue
		}
		found = true
		stop, err := s.readPostsUntilQuit(b.Board, unread)
		if err != nil || stop {
			return err
		}
	}
	if !found {
		s.printf("%sNothing new on the boards.\n", colDim)
		return s.pause()
	}
	return nil
}

// readPostsUntilQuit reads posts and reports whether the caller quit early.
func (s *session) readPostsUntilQuit(b store.Board, posts []store.Post) (bool, error) {
	quit := false
	err := s.readPostsWith(b, posts, 0, func() { quit = true })
	return quit, err
}

func (s *session) readPosts(b store.Board, posts []store.Post, idx int) error {
	if len(posts) == 0 {
		s.printf("%sNothing unread here.\n", colDim)
		return s.pause()
	}
	return s.readPostsWith(b, posts, idx, func() {})
}

// readPostsWith shows posts[idx], moving through the list; onQuit runs if
// the caller quits before the end.
func (s *session) readPostsWith(b store.Board, posts []store.Post, idx int, onQuit func()) error {
	for idx >= 0 && idx < len(posts) {
		p := posts[idx]
		if err := s.srv.store.MarkBoardRead(s.user.ID, b.ID, p.ID); err != nil {
			s.srv.log.Warn("mark board read failed", "board", b.ID, "err", err)
		}
		title := fmt.Sprintf("%s · %d of %d", b.Name, idx+1, len(posts))
		if err := s.showLetter(letter{title: title, body: p.Body, fields: []field{
			{"By", s.handleOf(p.AuthorID), colBright},
			{"Date", longDate(p.PostedAt), colLabel},
			{"Subj", p.Subject, colKey},
		}}); err != nil {
			return err
		}
		s.flushNotices()
		words := []string{"Reply", "Mail author", "Next", "Prev"}
		if p.AuthorID == s.user.ID || s.user.Sysop {
			words = append(words, "Delete")
		}
		words = append(words, "Quit")
		s.print("\n" + actions(words...) + " |08» |15")
		k, err := s.choose(keysOf(words...) + "\r")
		if err != nil {
			return err
		}
		switch k {
		case 'N', keyEnter:
			idx++
		case 'P':
			idx = max(idx-1, 0)
		case 'R':
			err = s.replyPost(b, p)
		case 'M':
			err = s.mailAuthor(p)
		case 'D':
			var deleted bool
			if deleted, err = s.deletePost(p); deleted {
				posts = slices.Concat(posts[:idx], posts[idx+1:])
				idx = min(idx, len(posts)-1)
			}
		case 'Q':
			onQuit()
			return nil
		}
		if err != nil {
			return err
		}
	}
	return nil
}

type draftPost struct {
	subject string
	replyTo int64
	quote   []string
}

func (s *session) writePost(b store.Board, d draftPost) error {
	if stop, err := s.awaitingApproval("Posting"); stop || err != nil {
		return err
	}
	if b.SysopOnly && !s.user.Sysop {
		s.printf("%sOnly the sysop posts on %s.\n", colAlert, safe(b.Name))
		return s.pause()
	}
	if !s.user.Sysop && s.srv.limit.posts.blocked(userKey(s.user.ID)) {
		s.printf("%sYou've posted a lot lately. Take a breather and try again later.\n", colAlert)
		return s.pause()
	}
	s.setActivity("Posting on " + b.Name)
	s.header("Post to " + b.Name)
	s.print("\n")
	subject, err := s.askSubject(d.subject)
	if err != nil {
		return err
	}
	if subject == "" {
		return nil
	}
	body, ok, err := s.editor(d.quote)
	if err != nil {
		return err
	}
	if !ok {
		s.printf("\n%sPost discarded.\n", colDim)
		return s.pause()
	}
	if _, err := s.srv.store.AddPost(b.ID, s.user.ID, subject, body, d.replyTo); err != nil {
		return s.reportError("add post", err)
	}
	s.srv.limit.posts.hit(userKey(s.user.ID))
	s.printf("\n%sPosted to %s.\n", colOK, safe(b.Name))
	return s.pause()
}

func (s *session) replyPost(b store.Board, p store.Post) error {
	quote, err := s.yesNo("Quote the post?", true)
	if err != nil {
		return err
	}
	var lines []string
	if quote {
		lines = quoteLines(s.handleOf(p.AuthorID), p.PostedAt, p.Body, editorWidth(s.width())-2)
	}
	return s.writePost(b, draftPost{subject: prefixed("Re: ", p.Subject), replyTo: p.ID, quote: lines})
}

// mailAuthor starts a private reply to a public post.
func (s *session) mailAuthor(p store.Post) error {
	author, err := s.srv.store.UserByID(p.AuthorID)
	if errors.Is(err, store.ErrNotFound) {
		s.printf("%sThat caller no longer exists.\n", colAlert)
		return s.pause()
	}
	if err != nil {
		return err
	}
	return s.compose(draft{to: []store.User{author}, subject: prefixed("Re: ", p.Subject)})
}

func (s *session) deletePost(p store.Post) (bool, error) {
	ok, err := s.yesNo("Delete this post for everyone?", false)
	if err != nil || !ok {
		return false, err
	}
	if err := s.srv.store.DeletePost(p.ID, s.user.ID); err != nil {
		return false, s.reportError("delete post", err)
	}
	return true, nil
}
