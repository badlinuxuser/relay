package mcp

import (
	"encoding/json"
	"os"
	"sync"
)

// Trust remembers which stdio servers the user has approved. mcp.json can be edited by hand,
// by a script or by another program; a command in it is not run until the user approves
// exactly that command (`relay mcp trust <id>`, or answering yes in `relay mcp add`).
// The approval is tied to the fingerprint, so changing the command asks again.
type Trust struct {
	path string
	mu   sync.Mutex
	ids  map[string]string // server id -> fingerprint
}

func OpenTrust(path string) *Trust {
	t := &Trust{path: path}
	t.Reload()
	return t
}

// Reload rereads the file (the CLI writes it while the daemon is running).
func (t *Trust) Reload() {
	ids := map[string]string{}
	if b, err := os.ReadFile(t.path); err == nil {
		_ = json.Unmarshal(b, &ids)
	}
	t.mu.Lock()
	t.ids = ids
	t.mu.Unlock()
}

func (t *Trust) Trusted(id, fingerprint string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return fingerprint != "" && t.ids[id] == fingerprint
}

func (t *Trust) Allow(id, fingerprint string) error {
	t.mu.Lock()
	t.ids[id] = fingerprint
	b, err := json.MarshalIndent(t.ids, "", "  ")
	t.mu.Unlock()
	if err != nil {
		return err
	}
	return writeAtomic(t.path, append(b, '\n'))
}

func (t *Trust) Forget(id string) error {
	t.mu.Lock()
	delete(t.ids, id)
	b, err := json.MarshalIndent(t.ids, "", "  ")
	t.mu.Unlock()
	if err != nil {
		return err
	}
	return writeAtomic(t.path, append(b, '\n'))
}
