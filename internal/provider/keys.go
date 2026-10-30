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

// Source поставляет сохранённые ключи провайдера (хранилище ключей).
type Source interface {
	Values(provider string) []string
}

// Keys управляет ключами одного провайдера: сохранённые в хранилище плюс
// (необязательно) переменные окружения из конфигурации. Ключ, упёршийся в лимит,
// отдыхает keyCooldown, пока используется следующий.
type Keys struct {
	name     string
	envs     []string
	src      Source
	needsKey bool

	mu    sync.Mutex
	until map[string]time.Time
	last  string
}

func NewKeys(name string, envs []string, src Source, needsKey bool) *Keys {
	return &Keys{name: name, envs: envs, src: src, needsKey: needsKey, until: map[string]time.Time{}}
}

func (k *Keys) list() []string {
	var out []string
	seen := map[string]bool{}
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	if k.src != nil {
		for _, v := range k.src.Values(k.name) {
			add(v)
		}
	}
	for _, e := range k.envs {
		add(os.Getenv(e))
	}
	return out
}

func (k *Keys) NeedsKey() bool { return k.needsKey }

// Any returns the first configured key regardless of cooldowns, or "" if there is none.
// It is used for read-only calls such as listing a provider's models.
func (k *Keys) Any() string {
	list := k.list()
	if len(list) == 0 {
		return ""
	}
	return list[0]
}

func (k *Keys) Count() int { return len(k.list()) }

// Available возвращает число ключей, которые сейчас не на паузе.
func (k *Keys) Available() int {
	list := k.list()
	k.mu.Lock()
	defer k.mu.Unlock()
	n := 0
	now := time.Now()
	for _, v := range list {
		if !now.Before(k.until[v]) {
			n++
		}
	}
	return n
}

// Get возвращает ключ. Для локального провайдера без ключей возвращает пустую
// строку и ok = true. Пока ключ не упёрся в лимит, используется один и тот же.
func (k *Keys) Get() (string, bool) {
	list := k.list()
	if len(list) == 0 {
		return "", !k.needsKey
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	start := 0
	for i, v := range list {
		if v == k.last {
			start = i
			break
		}
	}
	now := time.Now()
	for off := 0; off < len(list); off++ {
		v := list[(start+off)%len(list)]
		if !now.Before(k.until[v]) {
			k.last = v
			return v, true
		}
	}
	return "", false
}

func (k *Keys) Cooldown(key string) {
	k.mu.Lock()
	k.until[key] = time.Now().Add(keyCooldown)
	k.mu.Unlock()
}

// DoWithKeys выполняет attempt с ключом; при лимите переключается на следующий.
func DoWithKeys(k *Keys, name string, emit func(Event) error, attempt func(key string) error) error {
	tried := 0
	for {
		key, ok := k.Get()
		if !ok {
			if k.Count() == 0 {
				return &Error{Provider: name, NoKey: true, Retryable: true,
					Msg: "no API key for " + name + ". Add one: run `relay keys`"}
			}
			return &Error{Provider: name, RateLimited: true, Retryable: true,
				Msg: "all API keys are rate limited"}
		}
		err := attempt(key)
		var pe *Error
		if err != nil && errors.As(err, &pe) && pe.RateLimited {
			k.Cooldown(key)
			tried++
			if tried < k.Count() {
				_ = emit(Event{Kind: EvNotice, Tag: "key_switch",
					Text: fmt.Sprintf("%s: a key hit its limit, switching to the next one", name)})
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
