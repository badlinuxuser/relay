// Package client это HTTP-клиент к бэкенду Relay для интерфейсов. Умеет найти уже
// запущенный бэкенд (daemon.json) или запустить его сам.
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"relay/internal/config"
	"relay/internal/proto"
)

type Client struct {
	Base  string
	Token string
	// Spawned: бэкенд запущен этим клиентом (его же и останавливаем при выходе).
	Spawned bool

	short  *http.Client
	stream *http.Client
}

func newClient(info proto.DaemonInfo) *Client {
	return &Client{
		Base:   "http://" + info.Addr,
		Token:  info.Token,
		short:  &http.Client{Timeout: 20 * time.Second},
		stream: &http.Client{},
	}
}

func readInfo(home string) (proto.DaemonInfo, error) {
	var info proto.DaemonInfo
	b, err := os.ReadFile(filepath.Join(home, "daemon.json"))
	if err != nil {
		return info, err
	}
	err = json.Unmarshal(b, &info)
	return info, err
}

// health возвращает признак «бэкенд отвечает» и его версию.
func (c *Client) health() (bool, string) {
	hc := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := hc.Get(c.Base + "/api/health")
	if err != nil {
		return false, ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return false, ""
	}
	var v struct {
		Version string `json:"version"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&v)
	return true, v.Version
}

func (c *Client) healthy() bool {
	ok, ver := c.health()
	return ok && ver == proto.Version
}

// Connect подключается к работающему бэкенду или запускает его в фоне
// (тот же исполняемый файл с подкомандой daemon).
func Connect(spawn bool) (*Client, error) {
	home, err := config.Home()
	if err != nil {
		return nil, err
	}
	if info, err := readInfo(home); err == nil {
		c := newClient(info)
		if ok, ver := c.health(); ok {
			if ver == proto.Version {
				return c, nil
			}
			// запущен бэкенд другой версии (например, после обновления): останавливаем
			c.Shutdown()
			time.Sleep(800 * time.Millisecond)
		}
	}
	if !spawn {
		return nil, errors.New("backend is not running (run `relay`, or drop --no-spawn)")
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	_ = os.Remove(filepath.Join(home, "daemon.json"))
	cmd := exec.Command(exe, "daemon")
	detach(cmd)
	logf, lerr := os.OpenFile(filepath.Join(home, "relayd.out"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if lerr == nil {
		cmd.Stdout, cmd.Stderr = logf, logf
		defer logf.Close()
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("cannot start backend: %w", err)
	}
	go func() { _ = cmd.Wait() }()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(150 * time.Millisecond)
		if info, err := readInfo(home); err == nil {
			c := newClient(info)
			if c.healthy() {
				c.Spawned = true
				return c, nil
			}
		}
	}
	msg := "backend did not start in time"
	if b, err := os.ReadFile(filepath.Join(home, "relayd.out")); err == nil && len(bytes.TrimSpace(b)) > 0 {
		msg += ": " + strings.TrimSpace(string(b))
	}
	return nil, errors.New(msg)
}

func (c *Client) newReq(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func apiError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && e.Error.Message != "" {
		return errors.New(e.Error.Message)
	}
	return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
}

func (c *Client) do(method, path string, body, out any) error {
	req, err := c.newReq(context.Background(), method, path, body)
	if err != nil {
		return err
	}
	resp, err := c.short.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return apiError(resp)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// Stream выполняет POST и читает поток SSE, вызывая onEvent для каждого события.
func (c *Client) Stream(ctx context.Context, path string, body any, onEvent func(proto.Event)) error {
	req, err := c.newReq(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	resp, err := c.stream.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return apiError(resp)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var ev proto.Event
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &ev); err != nil {
			continue
		}
		onEvent(ev)
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

func (c *Client) Status() (*proto.StatusResp, error) {
	var st proto.StatusResp
	if err := c.do("GET", "/api/status", nil, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// Models returns every model the user can pick (what the providers report plus
// the models from config.toml). refresh forces the backend to ask the providers again.
func (c *Client) Models(refresh bool) (*proto.ModelsResp, error) {
	path := "/api/models"
	if refresh {
		path += "?refresh=1"
	}
	var r proto.ModelsResp
	if err := c.do("GET", path, nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) CreateSession(mode, workspace string) (*proto.SessionInfo, error) {
	var si proto.SessionInfo
	err := c.do("POST", "/api/sessions", proto.CreateSessionReq{Mode: mode, Workspace: workspace}, &si)
	return &si, err
}

func (c *Client) Sessions() ([]proto.SessionInfo, error) {
	var out []proto.SessionInfo
	err := c.do("GET", "/api/sessions", nil, &out)
	return out, err
}

func (c *Client) GetSession(id string) (*proto.SessionInfo, error) {
	var si proto.SessionInfo
	err := c.do("GET", "/api/sessions/"+id, nil, &si)
	return &si, err
}

func (c *Client) Settings(id string, req proto.SettingsReq) (*proto.SessionInfo, error) {
	var si proto.SessionInfo
	err := c.do("POST", "/api/sessions/"+id+"/settings", req, &si)
	return &si, err
}

func (c *Client) Send(ctx context.Context, id, text string, onEvent func(proto.Event)) error {
	return c.Stream(ctx, "/api/sessions/"+id+"/messages", proto.SendReq{Text: text}, onEvent)
}

func (c *Client) Compare(ctx context.Context, req proto.CompareReq, onEvent func(proto.Event)) error {
	return c.Stream(ctx, "/api/compare", req, onEvent)
}

func (c *Client) Approve(id, decision string) error {
	return c.do("POST", "/api/approvals/"+id, proto.ApprovalReq{Decision: decision}, nil)
}

// Process starts or stops a local server. For "start", args are added to the
// arguments of the config. already is true if the server was running already.
func (c *Client) Process(name, action string, args ...string) (already bool, err error) {
	var body any
	if len(args) > 0 {
		body = proto.ProcessReq{Args: args}
	}
	var r proto.ProcessResp
	if err := c.do("POST", "/api/processes/"+name+"/"+action, body, &r); err != nil {
		return false, err
	}
	return r.Already, nil
}

// Shutdown просит бэкенд завершиться (останавливает и управляемые процессы).
func (c *Client) Shutdown() {
	_ = c.do("POST", "/api/shutdown", nil, nil)
}
