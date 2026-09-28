// Package automations holds the source of the official browser automation
// packages published on monoes.me (one directory per package, the layout
// `automation pack` reads). The app no longer embeds or seeds them: users
// install them with `monoagentcli library install automation <id>`.
//
// Only tests and the official-artifacts tooling (scripts/library-official.sh)
// import this package; the CLI and the desktop app must not (see
// cmd/monoagentcli's no-embedded-automations test).
package automations

import (
	"embed"
	"io/fs"
	"path"
	"sync"
	"testing/fstest"
)

//go:embed gemini hackernews instagram linkedin producthunt tiktok x
var files embed.FS

// FS returns the package directories at its root: <id>/automation.json,
// <id>/actions/<action>.json, …
func FS() fs.FS { return files }

// IDs are the official package ids in this directory.
var IDs = []string{"gemini", "hackernews", "instagram", "linkedin", "producthunt", "tiktok", "x"}

var (
	treeOnce sync.Once
	tree     fstest.MapFS
)

// Tree returns the packages under an "automations/" prefix — the layout of
// the former data.AutomationsFS (automations/<id>/automation.json), for
// tests written against it.
func Tree() fstest.MapFS {
	treeOnce.Do(func() {
		tree = fstest.MapFS{}
		_ = fs.WalkDir(files, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := files.ReadFile(p)
			if err != nil {
				return err
			}
			tree[path.Join("automations", p)] = &fstest.MapFile{Data: b, Mode: 0o644}
			return nil
		})
	})
	return tree
}
