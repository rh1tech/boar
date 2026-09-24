package store

import (
	"database/sql"
	"slices"
	"strings"
	"time"

	"boar/internal/term"
)

type Board struct {
	ID          int64
	Name        string
	Description string
	SysopOnly   bool // only sysops may post
	CreatedAt   time.Time
}

// BoardSummary is a board with per-user counters.
type BoardSummary struct {
	Board
	Posts    int
	New      int
	LastPost time.Time
}

type Post struct {
	ID       int64
	BoardID  int64
	AuthorID int64
	ThreadID int64
	ReplyTo  int64
	Subject  string
	Body     string
	PostedAt time.Time
}

const postCols = "id, board_id, author_id, thread_id, reply_to, subject, body, posted_at"

func scanPost(row scanner) (Post, error) {
	var p Post
	var at int64
	err := row.Scan(&p.ID, &p.BoardID, &p.AuthorID, &p.ThreadID, &p.ReplyTo, &p.Subject, &p.Body, &at)
	p.PostedAt = fromNano(at)
	return p, err
}

func scanBoard(row scanner) (Board, error) {
	var b Board
	var at int64
	err := row.Scan(&b.ID, &b.Name, &b.Description, &b.SysopOnly, &at)
	b.CreatedAt = fromNano(at)
	return b, err
}

// Boards lists every board with post and unread counts for userID. The
// caller's own posts never count as new.
func (s *Store) Boards(userID int64) ([]BoardSummary, error) {
	rows, err := s.db.Query(`
		SELECT b.id, b.name, b.description, b.sysop_only, b.created_at,
		       COUNT(p.id),
		       COALESCE(SUM(p.id > COALESCE(r.last_read_id, 0) AND p.author_id != ?), 0),
		       COALESCE(MAX(p.posted_at), 0)
		FROM boards b
		LEFT JOIN posts p ON p.board_id = b.id
		LEFT JOIN board_reads r ON r.board_id = b.id AND r.user_id = ?
		GROUP BY b.id
		ORDER BY b.name_key`, userID, userID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row scanner) (BoardSummary, error) {
		var bs BoardSummary
		var created, last int64
		err := row.Scan(&bs.ID, &bs.Name, &bs.Description, &bs.SysopOnly, &created, &bs.Posts, &bs.New, &last)
		bs.CreatedAt, bs.LastPost = fromNano(created), fromNano(last)
		return bs, err
	})
}

func (s *Store) Board(id int64) (Board, error) {
	b, err := scanBoard(s.db.QueryRow("SELECT id, name, description, sysop_only, created_at FROM boards WHERE id = ?", id))
	return b, notFound(err)
}

// CreateBoard adds a board; only sysops may.
func (s *Store) CreateBoard(actorID int64, name, description string, sysopOnly bool) (Board, error) {
	name = term.Clean(name, MaxBoardNameLen)
	if len([]rune(name)) < MinBoardNameLen {
		return Board{}, invalid("Board names must be %d-%d characters.", MinBoardNameLen, MaxBoardNameLen)
	}
	description = term.Clean(description, MaxBoardDescLen)
	var b Board
	err := s.tx(func(tx *sql.Tx) error {
		if err := requireSysop(tx, actorID); err != nil {
			return err
		}
		key := strings.ToLower(name)
		taken, err := exists(tx, "SELECT 1 FROM boards WHERE name_key = ?", key)
		if err != nil {
			return err
		}
		if taken {
			return invalid("There is already a board called %s.", name)
		}
		res, err := tx.Exec("INSERT INTO boards (name, name_key, description, sysop_only, created_at) VALUES (?, ?, ?, ?, ?)",
			name, key, description, sysopOnly, s.nowNano())
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		b, err = scanBoard(tx.QueryRow("SELECT id, name, description, sysop_only, created_at FROM boards WHERE id = ?", id))
		return err
	})
	return b, err
}

// DeleteBoard removes a board and all of its posts; only sysops may.
func (s *Store) DeleteBoard(actorID, id int64) error {
	return s.sysopDelete(actorID, "DELETE FROM boards WHERE id = ?", id)
}

// Posts returns a board's newest MaxBoardPosts posts, oldest first.
func (s *Store) Posts(boardID int64) ([]Post, error) {
	rows, err := s.db.Query("SELECT "+postCols+" FROM posts WHERE board_id = ? ORDER BY id DESC LIMIT ?", boardID, MaxBoardPosts)
	if err != nil {
		return nil, err
	}
	posts, err := collect(rows, scanPost)
	slices.Reverse(posts)
	return posts, err
}

// AddPost publishes a post. replyTo must be a post on the same board.
func (s *Store) AddPost(boardID, authorID int64, subject, body string, replyTo int64) (Post, error) {
	subject, body, err := cleanLetter(subject, body)
	if err != nil {
		return Post{}, err
	}
	var p Post
	err = s.tx(func(tx *sql.Tx) error {
		author, err := getUser(tx, authorID)
		if err != nil {
			return err
		}
		var sysopOnly bool
		if err := tx.QueryRow("SELECT sysop_only FROM boards WHERE id = ?", boardID).Scan(&sysopOnly); err != nil {
			return notFound(err)
		}
		if sysopOnly && !author.Sysop {
			return ErrForbidden
		}
		if !author.Validated {
			return ErrNotValidated
		}
		var threadID int64
		if replyTo != 0 {
			err := tx.QueryRow("SELECT thread_id FROM posts WHERE id = ? AND board_id = ?", replyTo, boardID).Scan(&threadID)
			if err == sql.ErrNoRows {
				replyTo = 0
			} else if err != nil {
				return err
			}
		}
		res, err := tx.Exec(`INSERT INTO posts (board_id, author_id, thread_id, reply_to, subject, body, posted_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, boardID, authorID, threadID, replyTo, subject, body, s.nowNano())
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if threadID == 0 {
			if _, err := tx.Exec("UPDATE posts SET thread_id = id WHERE id = ?", id); err != nil {
				return err
			}
		}
		p, err = scanPost(tx.QueryRow("SELECT "+postCols+" FROM posts WHERE id = ?", id))
		return err
	})
	return p, err
}

// DeletePost removes a post; only its author or a sysop may.
func (s *Store) DeletePost(postID, userID int64) error {
	return s.tx(func(tx *sql.Tx) error {
		u, err := getUser(tx, userID)
		if err != nil {
			return err
		}
		var authorID int64
		if err := tx.QueryRow("SELECT author_id FROM posts WHERE id = ?", postID).Scan(&authorID); err != nil {
			return notFound(err)
		}
		if authorID != userID && !u.Sysop {
			return ErrForbidden
		}
		_, err = tx.Exec("DELETE FROM posts WHERE id = ?", postID)
		return err
	})
}

// LastRead returns the newest post ID userID has read on a board.
func (s *Store) LastRead(userID, boardID int64) (int64, error) {
	var id int64
	err := s.db.QueryRow("SELECT last_read_id FROM board_reads WHERE user_id = ? AND board_id = ?", userID, boardID).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

// MarkBoardRead records that userID has read everything up to postID.
func (s *Store) MarkBoardRead(userID, boardID, postID int64) error {
	return markBoardRead(s.db, userID, boardID, postID)
}

func markBoardRead(q querier, userID, boardID, postID int64) error {
	_, err := q.Exec(`INSERT INTO board_reads (user_id, board_id, last_read_id) VALUES (?, ?, ?)
		ON CONFLICT (user_id, board_id) DO UPDATE SET last_read_id = max(last_read_id, excluded.last_read_id)`,
		userID, boardID, postID)
	return err
}
