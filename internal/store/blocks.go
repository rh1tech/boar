// SPDX-License-Identifier: GPL-3.0-or-later

package store

// Block stops blockedID from mailing, paging or chatting at userID.
func (s *Store) Block(userID, blockedID int64) error {
	if userID == blockedID {
		return invalid("You can't block yourself.")
	}
	if _, err := s.UserByID(blockedID); err != nil {
		return err
	}
	_, err := s.db.Exec("INSERT OR IGNORE INTO blocks (user_id, blocked_id) VALUES (?, ?)", userID, blockedID)
	return err
}

func (s *Store) Unblock(userID, blockedID int64) error {
	_, err := s.db.Exec("DELETE FROM blocks WHERE user_id = ? AND blocked_id = ?", userID, blockedID)
	return err
}

// BlockedUsers lists who userID has blocked, by handle.
func (s *Store) BlockedUsers(userID int64) ([]User, error) {
	rows, err := s.db.Query("SELECT "+userColsOf("u")+` FROM blocks b JOIN users u ON u.id = b.blocked_id
		WHERE b.user_id = ? ORDER BY u.handle_key`, userID)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanUser)
}

// IsBlocked reports whether userID has blocked otherID.
func (s *Store) IsBlocked(userID, otherID int64) (bool, error) {
	return isBlocked(s.db, userID, otherID)
}

func isBlocked(q querier, userID, otherID int64) (bool, error) {
	return exists(q, "SELECT 1 FROM blocks WHERE user_id = ? AND blocked_id = ?", userID, otherID)
}
