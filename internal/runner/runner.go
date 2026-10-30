// Package runner запускает и останавливает локальные процессы (Ollama, llama.cpp и т.п.).
package runner

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"relay/internal/config"
	"relay/internal/proto"
)

const maxLog = 200

type Proc struct {
	cfg      config.Process
	mu       sync.Mutex
	cmd      *exec.Cmd
	done     chan struct{}
	state    string
	errMsg   string
	logs     []string
	stopping bool
}

// alive reports whether ready_url already answers, i.e. the server is running
// (started by someone else) and we must not try to start a second copy.
func (p *Proc) alive() bool {
	if p.cfg.ReadyURL == "" {
		return false
	}
	client := &http.Client{Timeout: 400 * time.Millisecond}
	resp, err := client.Get(p.cfg.ReadyURL)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 400
}

func (p *Proc) addLog(line string) {
	p.mu.Lock()
	p.logs = append(p.logs, line)
	if len(p.logs) > maxLog {
		p.logs = p.logs[len(p.logs)-maxLog:]
	}
	p.mu.Unlock()
}

func (p *Proc) Start() error {
	p.mu.Lock()
	if p.state == "starting" || p.state == "ready" {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()
	// Something already listens on ready_url (system service, another terminal):
	// attach to it instead of failing with "address already in use".
	if p.alive() {
		return nil
	}
	p.mu.Lock()
	if p.state == "starting" || p.state == "ready" {
		p.mu.Unlock()
		return nil
	}
	cmd := exec.Command(p.cfg.Command, p.cfg.Args...)
	if p.cfg.Dir != "" {
		cmd.Dir = p.cfg.Dir
	}
	cmd.Env = append(os.Environ(), p.cfg.Env...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		p.state, p.errMsg = "failed", err.Error()
		p.mu.Unlock()
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		p.state, p.errMsg = "failed", err.Error()
		p.mu.Unlock()
		return fmt.Errorf("%s: %w", p.cfg.Name, err)
	}
	done := make(chan struct{})
	p.cmd, p.done = cmd, done
	p.state, p.errMsg, p.stopping = "starting", "", false
	p.mu.Unlock()

	// Читаем вывод до конца и только потом вызываем Wait (так требует os/exec).
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			p.addLog(sc.Text())
		}
		werr := cmd.Wait()
		p.mu.Lock()
		if p.cmd == cmd {
			switch {
			case p.stopping || werr == nil:
				p.state = "stopped"
			default:
				p.state, p.errMsg = "failed", werr.Error()
			}
			p.cmd = nil
		}
		p.mu.Unlock()
		close(done)
	}()
	go p.probe(cmd)
	return nil
}

// probe ждёт готовности процесса (ready_url отвечает 2xx/3xx).
func (p *Proc) probe(cmd *exec.Cmd) {
	if p.cfg.ReadyURL == "" {
		p.mu.Lock()
		if p.cmd == cmd && p.state == "starting" {
			p.state = "ready"
		}
		p.mu.Unlock()
		return
	}
	client := &http.Client{Timeout: time.Second}
	for i := 0; i < 120; i++ {
		p.mu.Lock()
		alive := p.cmd == cmd && p.state == "starting"
		p.mu.Unlock()
		if !alive {
			return
		}
		resp, err := client.Get(p.cfg.ReadyURL)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 400 {
				p.mu.Lock()
				if p.cmd == cmd {
					p.state = "ready"
				}
				p.mu.Unlock()
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	p.mu.Lock()
	if p.cmd == cmd && p.state == "starting" {
		p.errMsg = "not ready after 60s"
	}
	p.mu.Unlock()
}

func (p *Proc) Stop() error {
	p.mu.Lock()
	cmd, done := p.cmd, p.done
	if cmd == nil {
		p.mu.Unlock()
		if p.alive() {
			return fmt.Errorf("%s was not started by Relay, so Relay will not stop it (stop it where you started it, e.g. `systemctl stop %s`)", p.cfg.Name, p.cfg.Name)
		}
		return nil
	}
	p.stopping = true
	p.mu.Unlock()

	_ = cmd.Process.Signal(os.Interrupt) // на Windows вернёт ошибку, тогда сработает Kill
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
	return nil
}

func (p *Proc) info() proto.ProcInfo {
	p.mu.Lock()
	st := p.state
	if st == "" {
		st = "stopped"
	}
	own := p.cmd != nil
	p.mu.Unlock()

	// Not our process, but the port answers: show it as running (external).
	external := false
	if !own && st != "starting" && p.alive() {
		st, external = "ready", true
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	pi := proto.ProcInfo{Name: p.cfg.Name, State: st, External: external}
	if !external {
		pi.Error = p.errMsg
	}
	if p.cmd != nil && p.cmd.Process != nil {
		pi.PID = p.cmd.Process.Pid
	}
	n := len(p.logs)
	if n > 8 {
		n = 8
	}
	pi.Log = append([]string(nil), p.logs[len(p.logs)-n:]...)
	return pi
}

type Manager struct {
	procs map[string]*Proc
	order []string
}

func NewManager(list []config.Process) *Manager {
	m := &Manager{procs: map[string]*Proc{}}
	for _, c := range list {
		m.procs[c.Name] = &Proc{cfg: c, state: "stopped"}
		m.order = append(m.order, c.Name)
	}
	return m
}

func (m *Manager) Start(name string) error {
	p := m.procs[name]
	if p == nil {
		return fmt.Errorf("unknown process %q", name)
	}
	return p.Start()
}

func (m *Manager) Stop(name string) error {
	p := m.procs[name]
	if p == nil {
		return fmt.Errorf("unknown process %q", name)
	}
	return p.Stop()
}

func (m *Manager) StopAll() {
	var wg sync.WaitGroup
	for _, p := range m.procs {
		wg.Add(1)
		go func(p *Proc) {
			defer wg.Done()
			_ = p.Stop()
		}(p)
	}
	wg.Wait()
}

func (m *Manager) Status() []proto.ProcInfo {
	out := make([]proto.ProcInfo, 0, len(m.order))
	for _, name := range m.order {
		out = append(out, m.procs[name].info())
	}
	return out
}
