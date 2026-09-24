package mailer

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// ErrQueueFull is returned when too much mail is already waiting.
var ErrQueueFull = errors.New("mailer: queue full")

// ErrQueueClosed is returned after Close.
var ErrQueueClosed = errors.New("mailer: queue closed")

// defaultRetryDelays are the waits between delivery attempts.
var defaultRetryDelays = []time.Duration{5 * time.Second, 30 * time.Second}

// Queue delivers messages one at a time on a background goroutine.
type Queue struct {
	sender Sender
	log    *slog.Logger
	ch     chan Message
	delays []time.Duration

	mu     sync.Mutex
	closed bool
	stop   context.CancelFunc
	ctx    context.Context
	wg     sync.WaitGroup
}

func NewQueue(sender Sender, log *slog.Logger, size int) *Queue {
	ctx, cancel := context.WithCancel(context.Background())
	q := &Queue{sender: sender, log: log, ch: make(chan Message, size), delays: defaultRetryDelays, ctx: ctx, stop: cancel}
	q.wg.Go(q.run)
	return q
}

// Enqueue schedules m without waiting for delivery.
func (q *Queue) Enqueue(m Message) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return ErrQueueClosed
	}
	select {
	case q.ch <- m:
		return nil
	default:
		return ErrQueueFull
	}
}

// Close stops accepting mail and waits up to grace for the backlog, then
// abandons whatever is left.
func (q *Queue) Close(grace time.Duration) {
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		close(q.ch)
	}
	q.mu.Unlock()
	timer := time.AfterFunc(grace, q.stop)
	defer timer.Stop()
	q.wg.Wait()
	q.stop()
}

func (q *Queue) run() {
	for m := range q.ch {
		q.deliver(m)
	}
}

func (q *Queue) deliver(m Message) {
	for attempt := 0; ; attempt++ {
		err := q.sender.Send(q.ctx, m)
		if err == nil {
			q.log.Info("email sent", "to_domain", domainOf(m.To))
			return
		}
		if attempt >= len(q.delays) || q.ctx.Err() != nil {
			q.log.Error("email failed", "to_domain", domainOf(m.To), "attempts", attempt+1, "err", err)
			return
		}
		q.log.Warn("email attempt failed; retrying", "to_domain", domainOf(m.To), "err", err)
		select {
		case <-time.After(q.delays[attempt]):
		case <-q.ctx.Done():
		}
	}
}

// domainOf keeps full addresses out of the logs.
func domainOf(addr string) string {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == '@' {
			return addr[i+1:]
		}
	}
	return "?"
}
