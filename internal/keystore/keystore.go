// Package keystore хранит API-ключи провайдеров в <home>/keys.json (права 0600).
// Ключи переживают перезапуски и не зависят от переменных окружения.
package keystore

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
)

type Entry struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	Key   string `json:"key"`
}

// Masked это безопасное представление ключа для интерфейса.
type Masked struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	Hint  string `json:"hint"`
}

type Store struct {
	mu   sync.Mutex
	path string
	data map[string][]Entry
}

// Open загружает хранилище; отсутствие файла не ошибка.
func Open(path string) *Store {
	s := &Store{path: path, data: map[string][]Entry{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &s.data)
	}
	if s.data == nil {
		s.data = map[string][]Entry{}
	}
	return s
}

func hint(key string) string {
	if len(key) <= 8 {
		return "…"
	}
	return "…" + key[len(key)-4:]
}

// Values возвращает значения ключей провайдера (реализует provider.Source).
func (s *Store) Values(provider string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.data[provider]))
	for _, e := range s.data[provider] {
		out = append(out, e.Key)
	}
	return out
}

func (s *Store) List(provider string) []Masked {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Masked, 0, len(s.data[provider]))
	for _, e := range s.data[provider] {
		out = append(out, Masked{ID: e.ID, Label: e.Label, Hint: hint(e.Key)})
	}
	return out
}

func (s *Store) Add(provider, label, key string) (Masked, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return Masked{}, errors.New("key is empty")
	}
	if strings.ContainsAny(key, " \t\r\n") {
		return Masked{}, errors.New("key must not contain spaces or line breaks")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.data[provider] {
		if e.Key == key {
			return Masked{}, errors.New("this key is already saved")
		}
	}
	var raw [4]byte
	_, _ = rand.Read(raw[:])
	e := Entry{ID: hex.EncodeToString(raw[:]), Label: strings.TrimSpace(label), Key: key}
	s.data[provider] = append(s.data[provider], e)
	if err := s.save(); err != nil {
		s.data[provider] = s.data[provider][:len(s.data[provider])-1]
		return Masked{}, err
	}
	return Masked{ID: e.ID, Label: e.Label, Hint: hint(key)}, nil
}

func (s *Store) Delete(provider, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.data[provider]
	for i, e := range list {
		if e.ID == id {
			s.data[provider] = append(list[:i:i], list[i+1:]...)
			if len(s.data[provider]) == 0 {
				delete(s.data, provider)
			}
			_ = s.save()
			return true
		}
	}
	return false
}

// save пишет файл атомарно (временный файл и переименование).
func (s *Store) save() error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
