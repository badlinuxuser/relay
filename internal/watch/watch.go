// Package watch notices changes on disk by polling. It needs no outside library and works
// the same on Linux and Windows; a second of delay is fine for configuration files.
package watch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Poll calls onChange when sig() starts returning a different value. A change is applied
// only after the value has stayed the same for a moment, so an editor that writes a file
// in two steps does not cause two reloads. It returns when ctx is done.
func Poll(ctx context.Context, every time.Duration, sig func() string, onChange func()) {
	last := sig()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cur := sig()
		if cur == last {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
		if again := sig(); again != cur {
			continue // still being written: the next tick looks again
		}
		last = cur
		onChange()
	}
}

// FileSig describes a file by its modification time and size ("-" when it does not exist).
func FileSig(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return "-"
	}
	return fmt.Sprintf("%d:%d", fi.ModTime().UnixNano(), fi.Size())
}

// DirSig describes a folder of folders: the names of the entries and the signature of
// <entry>/<marker> for each one (for skills: SKILL.md).
func DirSig(dir, marker string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "-"
	}
	var parts []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() { // os.Stat follows symlinks
			continue
		}
		parts = append(parts, e.Name()+"="+FileSig(filepath.Join(p, marker)))
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
}
