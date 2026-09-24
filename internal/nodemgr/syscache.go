package nodemgr

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// sysCacheFile remembers the system Node's version so Activate, which runs
// on every CLI command, doesn't spawn `node --version` each time. An entry
// holds for as long as the resolved executable's size and mtime are
// unchanged (an upgrade, or a symlink pointed elsewhere, misses it).
const sysCacheFile = ".system-node.json"

type sysCacheEntry struct {
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mtime"`
	Version string    `json:"version"`
}

// cachedNodeVersion is NodeVersion with that cache. It is only written
// once Root exists: probing a system Node must not create ~/.monoagent/node.
func (m *Manager) cachedNodeVersion(ctx context.Context, node string) (string, error) {
	real, err := filepath.EvalSymlinks(node)
	if err != nil {
		return NodeVersion(ctx, node)
	}
	info, err := os.Stat(real)
	if err != nil {
		return NodeVersion(ctx, node)
	}
	path := filepath.Join(m.Root, sysCacheFile)
	cache := map[string]sysCacheEntry{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &cache)
	}
	if e, ok := cache[real]; ok && e.Size == info.Size() && e.ModTime.Equal(info.ModTime()) && isVersion(e.Version) {
		return e.Version, nil
	}
	v, err := NodeVersion(ctx, node)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(m.Root); err == nil {
		if len(cache) >= 32 { // nvm and friends: keep it small
			cache = map[string]sysCacheEntry{}
		}
		cache[real] = sysCacheEntry{Size: info.Size(), ModTime: info.ModTime(), Version: v}
		if b, err := json.Marshal(cache); err == nil {
			// Written whole and renamed, so a concurrent reader never sees
			// half a file.
			if f, err := os.CreateTemp(m.Root, ".system-node-*"); err == nil {
				_, werr := f.Write(b)
				if cerr := f.Close(); werr == nil && cerr == nil {
					werr = os.Rename(f.Name(), path)
				}
				if werr != nil {
					os.Remove(f.Name())
				}
			}
		}
	}
	return v, nil
}
