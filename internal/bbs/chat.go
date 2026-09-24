// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"maps"
	"slices"
	"strings"
	"sync"
)

const (
	defaultRoom     = "main"
	maxRoomNameLen  = 16
	chatBacklog     = 64
	maxChatLineLen  = 200
	chatSystemActor = 0
)

// chatLine is one line delivered to a chat member. text is pipe-coded and
// already safe to print.
type chatLine struct {
	fromID int64
	text   string
}

// chatMember is one caller in the teleconference. blocked is fixed at join.
type chatMember struct {
	userID  int64
	handle  string
	blocked map[int64]bool
	out     chan chatLine

	room string // guarded by chatHub.mu
}

func newChatMember(userID int64, handle string, blocked map[int64]bool) *chatMember {
	return &chatMember{userID: userID, handle: handle, blocked: blocked, out: make(chan chatLine, chatBacklog)}
}

// chatHub routes lines between members of named rooms.
type chatHub struct {
	mu    sync.Mutex
	rooms map[string]map[*chatMember]bool
}

func newChatHub() *chatHub {
	return &chatHub{rooms: make(map[string]map[*chatMember]bool)}
}

// normalizeRoom lower-cases a room name and keeps only [a-z0-9-_].
func normalizeRoom(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "#")) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
		if b.Len() == maxRoomNameLen {
			break
		}
	}
	return b.String()
}

// join moves m into room (leaving any previous room) and announces it.
func (h *chatHub) join(m *chatMember, room string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.leaveLocked(m)
	if h.rooms[room] == nil {
		h.rooms[room] = make(map[*chatMember]bool)
	}
	h.rooms[room][m] = true
	m.room = room
	h.sendLocked(room, chatLine{fromID: chatSystemActor, text: colDim + "*** " + colHandle + safe(m.handle) + colDim + " joined #" + room})
}

func (h *chatHub) leave(m *chatMember) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.leaveLocked(m)
}

func (h *chatHub) leaveLocked(m *chatMember) {
	if m.room == "" {
		return
	}
	room := m.room
	delete(h.rooms[room], m)
	if len(h.rooms[room]) == 0 {
		delete(h.rooms, room)
	}
	m.room = ""
	h.sendLocked(room, chatLine{fromID: chatSystemActor, text: colDim + "*** " + colHandle + safe(m.handle) + colDim + " left"})
}

// say sends a line from m to everyone in its room, including m.
func (h *chatHub) say(m *chatMember, text string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if m.room != "" {
		h.sendLocked(m.room, chatLine{fromID: m.userID, text: text})
	}
}

// sendLocked delivers without blocking; a member too slow to keep up misses
// lines rather than stalling the room.
func (h *chatHub) sendLocked(room string, line chatLine) {
	for member := range h.rooms[room] {
		if line.fromID != chatSystemActor && member.blocked[line.fromID] {
			continue
		}
		select {
		case member.out <- line:
		default:
		}
	}
}

func (h *chatHub) room(m *chatMember) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return m.room
}

// who lists the handles in a room.
func (h *chatHub) who(room string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for m := range h.rooms[room] {
		out = append(out, m.handle)
	}
	slices.Sort(out)
	return out
}

// roomCounts maps room names to member counts.
func (h *chatHub) roomCounts() map[string]int {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[string]int, len(h.rooms))
	for name, members := range h.rooms {
		out[name] = len(members)
	}
	return out
}

func (h *chatHub) count() int {
	total := 0
	for n := range maps.Values(h.roomCounts()) {
		total += n
	}
	return total
}
