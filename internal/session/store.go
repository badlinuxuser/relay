// Package session хранит историю диалогов (по одному JSON-файлу на сессию).
package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"relay/internal/provider"
)

type Session struct {
	ID        string             `json:"id"`
	Title     string             `json:"title"`
	Created   time.Time          `json:"created"`
	Updated   time.Time          `json:"updated"`
	Mode      string             `json:"mode"`
	Model     string             `json:"model"` // явный выбор модели, "" = по режиму
	Preset    string             `json:"preset"`
	Agent     bool               `json:"agent"`
	Workspace string             `json:"workspace"`
	Messages  []provider.Message `json:"messages"`

	// Run защищает сессию: одновременно идёт только один запрос.
	Run sync.Mutex `json:"-"`
	// AutoTools: инструменты, разрешённые на всю сессию ("always").
	AutoTools map[string]bool `json:"-"`
}

type Store struct {
	dir   string
	mu    sync.Mutex
	cache map[string]*Session
}

func NewStore(dir string) *Store {
	_ = os.MkdirAll(dir, 0o700)
	return &Store{dir: dir, cache: map[string]*Session{}}
}

func newID() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

func (s *Store) Create(mode, workspace string) *Session {
	now := time.Now()
	sess := &Session{
		ID: newID(), Title: "New session", Created: now, Updated: now,
		Mode: mode, Workspace: workspace, AutoTools: map[string]bool{},
	}
	s.mu.Lock()
	s.cache[sess.ID] = sess
	s.mu.Unlock()
	_ = s.Save(sess)
	return sess
}

func (s *Store) Get(id string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.cache[id]; ok {
		return sess, nil
	}
	if strings.ContainsAny(id, `/\.`) {
		return nil, fmt.Errorf("bad session id")
	}
	b, err := os.ReadFile(filepath.Join(s.dir, id+".json"))
	if err != nil {
		return nil, fmt.Errorf("session %q not found", id)
	}
	var sess Session
	if err := json.Unmarshal(b, &sess); err != nil {
		return nil, err
	}
	sess.AutoTools = map[string]bool{}
	s.cache[id] = &sess
	return &sess, nil
}

func (s *Store) Save(sess *Session) error {
	sess.Updated = time.Now()
	b, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.dir, sess.ID+".json"), b, 0o600)
}

type Summary struct {
	ID      string
	Title   string
	Mode    string
	Updated time.Time
}

// List возвращает сессии с диска, новые первыми.
func (s *Store) List() []Summary {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}
	var out []Summary
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		sess, err := s.Get(id)
		if err != nil {
			continue
		}
		out = append(out, Summary{ID: sess.ID, Title: sess.Title, Mode: sess.Mode, Updated: sess.Updated})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}
