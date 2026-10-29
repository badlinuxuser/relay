package provider

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const keyCooldown = 2 * time.Minute

// Keys управляет несколькими ключами одного провайдера (имена env-переменных).
// Ключ, упёршийся в лимит, отдыхает keyCooldown, пока используется следующий.
type Keys struct {
	mu    sync.Mutex
	envs  []string
	until map[int]time.Time
	cur   int
}

func NewKeys(envs []string) *Keys {
	return &Keys{envs: envs, until: map[int]time.Time{}}
}

func (k *Keys) Count() int { return len(k.envs) }

// Available возвращает число ключей, которые заданы и не на паузе.
func (k *Keys) Available() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	n := 0
	now := time.Now()
	for i, e := range k.envs {
		if os.Getenv(e) != "" && !now.Before(k.until[i]) {
			n++
		}
	}
	return n
}

// Get возвращает ключ и его индекс. Для провайдера без ключей (локальный
// сервер) возвращает пустую строку и ok = true.
func (k *Keys) Get() (key string, idx int, ok bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if len(k.envs) == 0 {
		return "", 0, true
	}
	now := time.Now()
	for off := 0; off < len(k.envs); off++ {
		i := (k.cur + off) % len(k.envs)
		v := os.Getenv(k.envs[i])
		if v != "" && !now.Before(k.until[i]) {
			k.cur = i
			return v, i, true
		}
	}
	return "", 0, false
}

func (k *Keys) Cooldown(i int) {
	k.mu.Lock()
	k.until[i] = time.Now().Add(keyCooldown)
	k.mu.Unlock()
}

func (k *Keys) anySet() bool {
	for _, e := range k.envs {
		if os.Getenv(e) != "" {
			return true
		}
	}
	return false
}

// DoWithKeys выполняет attempt с ключом; при лимите переключается на следующий.
func DoWithKeys(k *Keys, name string, emit func(Event) error, attempt func(key string) error) error {
	tried := 0
	for {
		key, idx, ok := k.Get()
		if !ok {
			if !k.anySet() {
				return &Error{Provider: name, Retryable: true,
					Msg: "API key not set (env: " + strings.Join(k.envs, ", ") + ")"}
			}
			return &Error{Provider: name, RateLimited: true, Retryable: true,
				Msg: "all API keys are rate limited"}
		}
		err := attempt(key)
		var pe *Error
		if err != nil && errors.As(err, &pe) && pe.RateLimited {
			k.Cooldown(idx)
			tried++
			if k.Count() > 1 && tried < k.Count() {
				_ = emit(Event{Kind: EvNotice, Tag: "key_switch",
					Text: fmt.Sprintf("%s: key %d hit its limit, switching to the next one", name, idx+1)})
				continue
			}
		}
		return err
	}
}

// httpError превращает неуспешный HTTP-ответ в *Error.
func httpError(name string, resp *http.Response) *Error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	msg := strings.TrimSpace(string(b))
	low := strings.ToLower(msg)
	e := &Error{Provider: name, Status: resp.StatusCode,
		Msg: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, msg)}
	e.RateLimited = resp.StatusCode == 429 || resp.StatusCode == 402 ||
		strings.Contains(low, "rate limit") || strings.Contains(low, "quota") ||
		strings.Contains(low, "credit balance") || strings.Contains(low, "insufficient")
	e.Retryable = e.RateLimited || resp.StatusCode >= 500 ||
		resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 404
	return e
}
