package osmenu

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrTaken is Install's refusal to replace a bundle of the same name that is
// not this profile's menu.
var ErrTaken = errors.New("the menu's bundle name is taken")

// Outcome says what Install did.
type Outcome string

const (
	Created   Outcome = "installed"
	Unchanged Outcome = "already_installed"
	Updated   Outcome = "updated"
)

// Result is what Install did, and where.
type Result struct {
	Path    string
	Menu    string
	Outcome Outcome
	Removed []string // the profile's older menus (a renamed profile's old name), never nil
}

// Menu is a bundle this package wrote, found in a Services folder.
type Menu struct {
	Path      string
	ProfileID string
	DB        string // the database it files into
	CLI       string
	Version   string
}

// Install writes b into dir, creating dir when it is missing. A menu is this
// profile's when its database and profile id are b's. What is at b's name
// decides: nothing, or this profile's menu, is written over (an exact copy of b
// is left alone); another database's menu, and a bundle this package did not
// write, are replaced only with force; the menu of a profile gone reports as
// deleted is replaced; the menu of another profile of this database is never
// replaced. The profile's menus under other names are removed.
func Install(dir string, b Bundle, force bool, gone func(profileID string) bool) (Result, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Result{}, fmt.Errorf("creating %s: %w", dir, err)
	}
	target := filepath.Join(dir, b.Name)
	res := Result{Path: target, Menu: b.Menu, Outcome: Created, Removed: []string{}}
	at, err := os.Lstat(target)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Result{}, err
	}
	menus, err := List(dir)
	if err != nil {
		return Result{}, err
	}
	var older []string
	for _, m := range menus {
		if m.ProfileID != b.ProfileID || m.DB != b.DBPath {
			continue
		}
		if at != nil {
			// On a case-insensitive disk a name that differs in case only is
			// the target itself.
			if fi, err := os.Lstat(m.Path); err == nil && os.SameFile(fi, at) {
				continue
			}
		}
		older = append(older, m.Path)
	}
	if at != nil {
		var m Menu
		var ok bool
		if at.IsDir() {
			m, ok = readMarker(target)
		}
		switch {
		case ok && m.ProfileID == b.ProfileID && m.DB == b.DBPath:
			if len(older) == 0 && Matches(target, b) {
				res.Outcome = Unchanged
				return res, nil
			}
		case ok && m.DB != b.DBPath:
			if !force {
				return Result{}, fmt.Errorf("%w: %s files into another database, %s (--force replaces it)", ErrTaken, target, m.DB)
			}
		case ok && gone(m.ProfileID):
			// The menu of a deleted profile can only fail: it is replaced.
		case ok:
			return Result{}, fmt.Errorf("%w: %s is the menu of profile %s, whose name reads the same; rename one of the two profiles", ErrTaken, target, m.ProfileID)
		case !force:
			return Result{}, fmt.Errorf("%w: %s was not written by monoagentcli task os install (--force replaces it)", ErrTaken, target)
		}
		res.Outcome = Updated
	}
	if err := write(dir, target, b); err != nil {
		return Result{}, err
	}
	for _, p := range older {
		if err := os.RemoveAll(p); err != nil {
			return res, fmt.Errorf("removing the older menu %s: %w", p, err)
		}
		res.Removed = append(res.Removed, p)
		res.Outcome = Updated
	}
	return res, nil
}

// tmpPrefix starts the temporary folders write uses inside a Services folder;
// a name without ".workflow" is never read as a service.
const tmpPrefix = ".monoagent-menu-"

// rename is os.Rename; a test replaces it to make a move fail.
var rename = os.Rename

// write builds b in a temporary folder inside dir and then moves it to
// target, so that the Services menu never reads a half-written bundle. A
// bundle already at target is moved aside first and put back if the move in
// fails.
func write(dir, target string, b Bundle) error {
	tmp, err := os.MkdirTemp(dir, tmpPrefix)
	if err != nil {
		return fmt.Errorf("writing the menu: %w", err)
	}
	defer os.RemoveAll(tmp) // a no-op once tmp has been moved
	if err := os.Chmod(tmp, 0o755); err != nil {
		return fmt.Errorf("writing the menu: %w", err)
	}
	for _, f := range []struct {
		rel  string
		data []byte
	}{{InfoPath, b.Info}, {DocumentPath, b.Document}} {
		p := filepath.Join(tmp, filepath.FromSlash(f.rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return fmt.Errorf("writing the menu: %w", err)
		}
		if err := os.WriteFile(p, f.data, 0o644); err != nil {
			return fmt.Errorf("writing the menu: %w", err)
		}
	}
	aside := ""
	if _, err := os.Lstat(target); err == nil {
		aside = tmp + "-old"
		if err := rename(target, aside); err != nil {
			return fmt.Errorf("replacing %s: %w", target, err)
		}
	}
	if err := rename(tmp, target); err != nil {
		if aside != "" {
			_ = rename(aside, target) // the old menu goes back
		}
		return fmt.Errorf("writing the menu: %w", err)
	}
	if aside != "" {
		_ = os.RemoveAll(aside) // the new menu is in place; Remove sweeps what stays
	}
	return nil
}

// List returns the managed bundles in dir (never nil): real folders named
// *.workflow whose Info.plist carries a profile id marker at its top level. A
// folder that does not exist holds none.
func List(dir string) ([]Menu, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return []Menu{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	menus := []Menu{}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasSuffix(e.Name(), ".workflow") {
			continue // a symlink is not a folder here: it is never managed
		}
		if m, ok := readMarker(filepath.Join(dir, e.Name())); ok {
			menus = append(menus, m)
		}
	}
	return menus, nil
}

// Remove removes the managed bundles of profile profileID of database db from
// dir and returns their paths (never nil). A bundle this package did not write,
// or another database's, is never touched; the temporary folders an
// interrupted write left behind are removed too.
func Remove(dir, db, profileID string) ([]string, error) {
	menus, err := List(dir)
	if err != nil {
		return nil, err
	}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), tmpPrefix) {
				_ = os.RemoveAll(filepath.Join(dir, e.Name()))
			}
		}
	}
	removed := []string{}
	for _, m := range menus {
		if m.ProfileID != profileID || m.DB != db {
			continue
		}
		if err := os.RemoveAll(m.Path); err != nil {
			return removed, fmt.Errorf("removing %s: %w", m.Path, err)
		}
		removed = append(removed, m.Path)
	}
	return removed, nil
}

// Matches reports whether the bundle at path is exactly b: the same folder
// name and the same two files.
func Matches(path string, b Bundle) bool {
	if filepath.Base(path) != b.Name {
		return false
	}
	info, err1 := os.ReadFile(filepath.Join(path, filepath.FromSlash(InfoPath)))
	doc, err2 := os.ReadFile(filepath.Join(path, filepath.FromSlash(DocumentPath)))
	return err1 == nil && err2 == nil && bytes.Equal(info, b.Info) && bytes.Equal(doc, b.Document)
}

// readMarker reads the marker keys of the bundle at path. ok is false when the
// bundle has no XML Info.plist with a profile id at its top level: not a
// bundle this package manages.
func readMarker(path string) (Menu, bool) {
	data, err := os.ReadFile(filepath.Join(path, filepath.FromSlash(InfoPath)))
	if err != nil {
		return Menu{}, false
	}
	keys := topLevelStrings(data)
	m := Menu{Path: path, ProfileID: keys[KeyProfileID], DB: keys[KeyDB], CLI: keys[KeyCLI], Version: keys[KeyVersion]}
	return m, m.ProfileID != ""
}

// topLevelStrings returns the string values of the top-level dict of an XML
// property list, by key; nested values are skipped. A file that is not XML
// gives what was read before the error.
func topLevelStrings(data []byte) map[string]string {
	out := map[string]string{}
	d := xml.NewDecoder(bytes.NewReader(data))
	depth, key := 0, ""
	for {
		tok, err := d.Token()
		if err != nil {
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth != 3 { // 1 is <plist>, 2 the top dict, 3 its keys and values
				continue
			}
			switch t.Name.Local {
			case "key", "string":
				var s string
				if d.DecodeElement(&s, &t) != nil {
					return out
				}
				depth-- // DecodeElement also read the end element
				if t.Name.Local == "key" {
					key = s
					continue
				}
				if key != "" {
					out[key] = s
				}
			}
			key = ""
		case xml.EndElement:
			depth--
		}
	}
}
