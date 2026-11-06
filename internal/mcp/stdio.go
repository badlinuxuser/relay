package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// stdioTransport runs a local program and talks to it with one JSON message per line.
type stdioTransport struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	errs  *tail

	wmu sync.Mutex // one writer at a time

	mu      sync.Mutex
	next    int64
	pending map[int64]chan rpcMsg
	closed  bool
	reason  string
	done    chan struct{}
}

func newStdio(cfg ServerConfig) (*stdioTransport, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Env = buildEnv(cfg)
	setProcAttr(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("%q was not found: install it or give the full path in \"command\"", cfg.Command)
		}
		return nil, err
	}
	t := &stdioTransport{
		cmd: cmd, stdin: stdin, errs: &tail{},
		pending: map[int64]chan rpcMsg{}, done: make(chan struct{}),
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); t.readLoop(stdout) }()
	go func() { defer wg.Done(); t.readStderr(stderr) }()
	go func() {
		wg.Wait() // Wait must come after the pipes are read to the end
		err := cmd.Wait()
		t.mu.Lock()
		if t.reason == "" && !t.closed {
			t.reason = "the program exited"
			if line := t.errs.last(); line != "" {
				t.reason += ": " + line
			} else if err != nil {
				t.reason += ": " + err.Error()
			}
		}
		t.mu.Unlock()
		t.failAll()
		close(t.done)
	}()
	return t, nil
}

func (t *stdioTransport) Done() <-chan struct{} { return t.done }

func (t *stdioTransport) Reason() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reason
}

func (t *stdioTransport) readStderr(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 16*1024), 1024*1024)
	for sc.Scan() {
		t.errs.add(sc.Text())
	}
}

func (t *stdioTransport) readLoop(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var msg rpcMsg
		if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
			continue // not JSON-RPC (a stray log line on stdout)
		}
		switch {
		case msg.isResponse():
			id, ok := msg.numericID()
			if !ok {
				continue
			}
			t.mu.Lock()
			ch := t.pending[id]
			t.mu.Unlock()
			if ch != nil {
				select {
				case ch <- msg:
				default:
				}
			}
		case msg.isRequest():
			_ = t.write(replyToServer(msg))
		}
	}
}

// failAll wakes everyone who waits for an answer that will never come.
func (t *stdioTransport) failAll() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, ch := range t.pending {
		select {
		case ch <- rpcMsg{Error: &rpcError{Message: exitError(t.reason).Error()}}:
		default:
		}
	}
}

func (t *stdioTransport) write(msg rpcMsg) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	t.wmu.Lock()
	defer t.wmu.Unlock()
	_, err = t.stdin.Write(append(b, '\n'))
	return err
}

func (t *stdioTransport) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, errors.New("the connection is closed")
	}
	select {
	case <-t.done:
		reason := t.reason
		t.mu.Unlock()
		return nil, exitError(reason)
	default:
	}
	t.next++
	id := t.next
	ch := make(chan rpcMsg, 1)
	t.pending[id] = ch
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		delete(t.pending, id)
		t.mu.Unlock()
	}()

	req, err := newRequest(id, method, params)
	if err != nil {
		return nil, err
	}
	if err := t.write(req); err != nil {
		return nil, exitError(t.Reason())
	}
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	case <-ctx.Done():
		// tell the server that we stopped waiting
		if n, err := newNotification("notifications/cancelled", map[string]any{"requestId": id, "reason": "cancelled"}); err == nil {
			_ = t.write(n)
		}
		return nil, ctx.Err()
	}
}

func (t *stdioTransport) Notify(ctx context.Context, method string, params any) error {
	n, err := newNotification(method, params)
	if err != nil {
		return err
	}
	return t.write(n)
}

func (t *stdioTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.mu.Unlock()
	_ = t.stdin.Close() // a well-behaved server exits when its input ends
	select {
	case <-t.done:
		return nil
	case <-time.After(2 * time.Second):
	}
	killTree(t.cmd)
	select {
	case <-t.done:
	case <-time.After(2 * time.Second):
	}
	return nil
}
