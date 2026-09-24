package bbs

// inputPump reads the terminal on its own goroutine and hands bytes over a
// channel. That lets the session wait for "a key or something else" (a door
// program exiting, for one) instead of blocking in ReadByte.
type inputPump struct {
	events chan inputEvent
	done   chan struct{}
	err    error // first read error; sticky. Only the session goroutine uses it.
}

type inputEvent struct {
	b   byte
	err error
}

func newInputPump(t Terminal) *inputPump {
	p := &inputPump{events: make(chan inputEvent), done: make(chan struct{})}
	go p.run(t)
	return p
}

func (p *inputPump) run(t Terminal) {
	for {
		b, err := t.ReadByte()
		select {
		case p.events <- inputEvent{b, err}:
		case <-p.done:
			return
		}
		if err != nil {
			return
		}
	}
}

// next waits for the next byte or read error. Once reading has failed it
// keeps returning that error.
func (p *inputPump) next() (byte, error) {
	if p.err != nil {
		return 0, p.err
	}
	return p.accept(<-p.events)
}

// accept records an event taken straight off p.events (by a select).
func (p *inputPump) accept(ev inputEvent) (byte, error) {
	if ev.err != nil {
		p.err = ev.err
	}
	return ev.b, ev.err
}

// stop releases the pump goroutine once the session is over. The goroutine
// itself exits when its pending read fails, which closing the terminal causes.
func (p *inputPump) stop() { close(p.done) }
