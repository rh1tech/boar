package bbs

import (
	"errors"
	"maps"
	"slices"
	"sync"
	"time"

	"boar/internal/store"
)

const maxPendingNotices = 20

var (
	errNodesBusy    = errors.New("All nodes are busy. Please call back later.")
	errShuttingDown = errors.New("The system is going down. Call back soon!")
)

// node is one connected caller. Fields below mu may be read by other
// sessions (who's online, pages, mail notifications).
type node struct {
	id     int
	t      Terminal
	since  time.Time
	notify chan struct{} // signalled when a notice arrives

	mu       sync.Mutex
	userID   int64
	handle   string
	location string
	activity string
	secure   bool
	notices  []string
}

// NodeInfo is a point-in-time copy of a node for display.
type NodeInfo struct {
	ID       int
	UserID   int64
	Handle   string
	Location string
	Activity string
	Secure   bool
	Since    time.Time
}

func (n *node) setUser(u store.User) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.userID, n.handle, n.location = u.ID, u.Handle, u.Location
}

func (n *node) setActivity(a string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.activity = a
}

func (n *node) addNotice(text string) {
	n.mu.Lock()
	if len(n.notices) < maxPendingNotices {
		n.notices = append(n.notices, text)
	}
	n.mu.Unlock()
	select {
	case n.notify <- struct{}{}:
	default: // a signal is already pending
	}
}

// takeNotices returns and clears pending notices.
func (n *node) takeNotices() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := n.notices
	n.notices = nil
	return out
}

func (n *node) info() NodeInfo {
	n.mu.Lock()
	defer n.mu.Unlock()
	return NodeInfo{ID: n.id, UserID: n.userID, Handle: n.handle, Location: n.location,
		Activity: n.activity, Secure: n.secure, Since: n.since}
}

// nodeTable hands out node numbers 1..max. Lock order: table, then node.
type nodeTable struct {
	mu      sync.Mutex
	max     int
	nodes   map[int]*node
	closing bool
}

func newNodeTable(max int) *nodeTable {
	return &nodeTable{max: max, nodes: make(map[int]*node)}
}

// acquire reserves the lowest free node number.
func (t *nodeTable) acquire(term Terminal, secure bool) (*node, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closing {
		return nil, errShuttingDown
	}
	for id := 1; id <= t.max; id++ {
		if _, used := t.nodes[id]; !used {
			n := &node{id: id, t: term, secure: secure, since: time.Now(), activity: "Logging in", notify: make(chan struct{}, 1)}
			t.nodes[id] = n
			return n, nil
		}
	}
	return nil, errNodesBusy
}

func (t *nodeTable) release(n *node) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.nodes, n.id)
}

func (t *nodeTable) online() []NodeInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]NodeInfo, 0, len(t.nodes))
	for _, n := range t.nodes {
		out = append(out, n.info())
	}
	slices.SortFunc(out, func(a, b NodeInfo) int { return a.ID - b.ID })
	return out
}

// each calls fn for every node while holding the table lock.
func (t *nodeTable) each(fn func(*node)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, n := range t.nodes {
		fn(n)
	}
}

// notify queues text for every node where userID is logged in and reports
// how many nodes it reached. Sessions show notices at their next prompt
// (or immediately, in chat).
func (t *nodeTable) notify(userID int64, text string) int {
	reached := 0
	t.each(func(n *node) {
		if n.info().UserID == userID {
			n.addNotice(text)
			reached++
		}
	})
	return reached
}

// broadcast queues text for every logged-in node except skipNode.
func (t *nodeTable) broadcast(text string, skipNode int) {
	t.each(func(n *node) {
		if n.id != skipNode && n.info().UserID != 0 {
			n.addNotice(text)
		}
	})
}

// kick hangs up one node. It reports whether the node existed.
func (t *nodeTable) kick(nodeID int, msg string) bool {
	t.mu.Lock()
	n, ok := t.nodes[nodeID]
	t.mu.Unlock()
	if ok {
		hangUpNode(n, msg)
	}
	return ok
}

// kickUser hangs up every node userID is on and reports how many.
func (t *nodeTable) kickUser(userID int64, msg string) int {
	var victims []*node
	t.each(func(n *node) {
		if n.info().UserID == userID {
			victims = append(victims, n)
		}
	})
	hangUpAll(victims, msg)
	return len(victims)
}

// closeAll tells every caller the system is going down, hangs up, and
// refuses new nodes from then on.
func (t *nodeTable) closeAll(msg string) {
	t.mu.Lock()
	t.closing = true
	all := slices.Collect(maps.Values(t.nodes))
	t.mu.Unlock()
	hangUpAll(all, msg)
}

// hangUpAll hangs up nodes in parallel and outside any lock, so one caller
// that stopped reading can't hold up the others (writes can block until the
// transport's write timeout).
func hangUpAll(nodes []*node, msg string) {
	var wg sync.WaitGroup
	for _, n := range nodes {
		wg.Go(func() { hangUpNode(n, msg) })
	}
	wg.Wait()
}

func hangUpNode(n *node, msg string) {
	_, _ = n.t.Write([]byte("\r\n\r\n" + msg + "\r\n")) // best effort: we hang up regardless
	_ = n.t.Close()
}
