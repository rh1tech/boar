package doors

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	connectTimeout = 30 * time.Second // for a tcp door to dial in
	killGrace      = 3 * time.Second
	maxStderr      = 4 << 10
)

// Process is a running door: read its output, write the caller's keys, and
// wait for it to finish.
type Process struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    io.Reader
	outR   *os.File // stdio doors: our end of the stdout pipe
	outW   *os.File // stdio doors: the program's end, closed here after start
	conn   net.Conn // tcp doors only
	stderr *limitedBuffer
	done   chan struct{}
	err    error
	close  sync.Once
}

// Vars fills in {name} placeholders in a door's command and directory.
type Vars map[string]string

func (v Vars) expand(s string) string {
	for k, val := range v {
		s = strings.ReplaceAll(s, "{"+k+"}", val)
	}
	return s
}

// Start launches a door. It stops when ctx ends (hang-up or time limit).
// For tcp doors it waits for the program to connect to the {port} it was given.
func Start(ctx context.Context, d Door, vars Vars) (*Process, error) {
	var ln net.Listener
	if d.IO == IOTCP {
		var err error
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("doors: listen: %w", err)
		}
		defer ln.Close()
		vars = mergeVars(vars, Vars{"port": strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)})
	}
	args := make([]string, len(d.Command))
	for i, a := range d.Command {
		args[i] = vars.expand(a)
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = vars.expand(d.Dir)
	cmd.Env = doorEnv(vars)
	cmd.WaitDelay = killGrace
	configureKill(cmd)
	p := &Process{cmd: cmd, stderr: &limitedBuffer{limit: maxStderr}, done: make(chan struct{})}
	cmd.Stderr = p.stderr

	if d.IO == IOStdio {
		if err := p.pipeStdio(); err != nil {
			return nil, err
		}
	}
	if err := cmd.Start(); err != nil {
		p.closePipes()
		return nil, fmt.Errorf("doors: start %s: %w", d.Key, err)
	}
	if p.outW != nil {
		_ = p.outW.Close() // the child has its copy; EOF arrives when it exits
	}
	go func() {
		p.err = cmd.Wait()
		close(p.done)
	}()
	if d.IO == IOTCP {
		if err := p.acceptDoor(ctx, ln); err != nil {
			p.Kill()
			return nil, err
		}
	}
	return p, nil
}

// pipeStdio wires stdin and stdout. Stdout uses a plain OS pipe rather
// than StdoutPipe, which Wait closes as soon as the program exits and could
// cut off its last screen before we read it.
func (p *Process) pipeStdio() error {
	in, err := p.cmd.StdinPipe()
	if err != nil {
		return err
	}
	r, w, err := os.Pipe()
	if err != nil {
		return err
	}
	p.cmd.Stdout = w
	p.in, p.out, p.outR, p.outW = in, r, r, w
	return nil
}

func (p *Process) closePipes() {
	for _, f := range []*os.File{p.outR, p.outW} {
		if f != nil {
			_ = f.Close()
		}
	}
}

// acceptDoor waits for the program to connect back, or to die trying.
func (p *Process) acceptDoor(ctx context.Context, ln net.Listener) error {
	type result struct {
		c   net.Conn
		err error
	}
	accepted := make(chan result, 1)
	go func() {
		c, err := ln.Accept()
		accepted <- result{c, err}
	}()
	select {
	case r := <-accepted:
		if r.err != nil {
			return fmt.Errorf("doors: accept: %w", r.err)
		}
		p.conn, p.in, p.out = r.c, r.c, r.c
		return nil
	case <-p.done:
		return fmt.Errorf("doors: program exited before connecting: %v %s", p.err, p.Stderr())
	case <-time.After(connectTimeout):
		return errors.New("doors: program never connected")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func mergeVars(a, b Vars) Vars {
	out := make(Vars, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// doorEnv passes a minimal environment: no secrets from the BBS process.
func doorEnv(vars Vars) []string {
	env := []string{"TERM=ansi", "LANG=C"}
	for _, k := range []string{"PATH", "HOME", "TMPDIR"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	for k, v := range vars {
		env = append(env, "BOAR_"+strings.ToUpper(k)+"="+v)
	}
	return env
}

func (p *Process) Read(b []byte) (int, error)  { return p.out.Read(b) }
func (p *Process) Write(b []byte) (int, error) { return p.in.Write(b) }

// Done is closed when the program has exited.
func (p *Process) Done() <-chan struct{} { return p.done }

// Err is the program's exit error, valid after Done.
func (p *Process) Err() error { return p.err }

// Stderr returns what the program wrote to stderr (first few KB).
func (p *Process) Stderr() string { return strings.TrimSpace(p.stderr.String()) }

// Kill ends the program and closes its connections.
func (p *Process) Kill() {
	p.close.Do(func() {
		if p.cmd.Process != nil {
			_ = p.cmd.Cancel() // kills the whole process group; see configureKill
		}
		if p.in != nil {
			_ = p.in.Close()
		}
		if p.conn != nil {
			_ = p.conn.Close()
		}
	})
	<-p.done
	if p.outR != nil {
		_ = p.outR.Close() // unblocks a reader if a stray child still holds the pipe
	}
}

// limitedBuffer keeps the first limit bytes written to it.
type limitedBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if room := l.limit - l.buf.Len(); room > 0 {
		l.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (l *limitedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}
