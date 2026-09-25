package recording

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/monoes/mono-agent/internal/capture"
	"github.com/monoes/mono-agent/internal/profiledir"
)

// Which inboxes List and Find read.
//
// A recording lands where a capture would: the inbox of the profile the
// extension named, or the default (unprofiled) inbox. The CLI reading them
// back has no say in where they went, so by default it reads every place one
// can be — the default inbox plus each profile's inbox under
// ~/.monoagent/profiles — without needing the database. `--profile` narrows
// that to one profile (SetProfile); tests point it anywhere (SetInboxes).

var scope struct {
	mu      sync.Mutex
	profile string
	inboxes []string
}

// SetProfile narrows List/Find to one profile's inbox. "" restores the
// default (every inbox).
func SetProfile(id string) error {
	id = strings.TrimSpace(id)
	if id != "" && !profiledir.ValidProfileID(id) {
		return fmt.Errorf("recording: unusable profile id %q", id)
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	scope.profile = id
	return nil
}

// SetInboxes overrides the inboxes read, outright. No arguments restores
// the default.
func SetInboxes(dirs ...string) {
	scope.mu.Lock()
	defer scope.mu.Unlock()
	scope.inboxes = append([]string(nil), dirs...)
}

// Inboxes returns the inbox directories List and Find read, in order.
func Inboxes() []string {
	scope.mu.Lock()
	override, profile := scope.inboxes, scope.profile
	scope.mu.Unlock()
	if len(override) > 0 {
		return append([]string(nil), override...)
	}
	if profile != "" {
		dir, err := capture.ProfileInbox(profile)
		if err != nil {
			return nil
		}
		return []string{dir}
	}
	return allInboxes()
}

// allInboxes is the default inbox plus every profile inbox that exists on
// disk.
func allInboxes() []string {
	out := []string{capture.DefaultInbox()}
	home, err := os.UserHomeDir()
	if err != nil {
		return out
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".monoagent", "profiles", "*", ".monomind", capture.ProfileInboxDir))
	sort.Strings(matches)
	seen := map[string]bool{filepath.Clean(out[0]): true}
	for _, m := range matches {
		id := filepath.Base(filepath.Dir(filepath.Dir(m)))
		if !profiledir.ValidProfileID(id) || seen[filepath.Clean(m)] {
			continue
		}
		seen[filepath.Clean(m)] = true
		out = append(out, m)
	}
	return out
}

// profileOfInbox returns the profile id whose inbox dir is, or "".
func profileOfInbox(dir string) string {
	if filepath.Base(dir) != capture.ProfileInboxDir || filepath.Base(filepath.Dir(dir)) != ".monomind" {
		return ""
	}
	id := filepath.Base(filepath.Dir(filepath.Dir(dir)))
	if filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(dir)))) != "profiles" {
		return ""
	}
	return id
}

// safeID matches identifiers that come from the extension or a command line
// and end up in a path or an argv: recording ids, event ids, envelope
// directory names.
var safeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// ValidID reports whether id is safe as a path component and argv word.
func ValidID(id string) bool {
	return safeID.MatchString(id) && !strings.Contains(id, "..")
}

// DraftsDir is where `record analyze` writes drafts:
// ~/.monoagent/recording-drafts (contracts §5).
func DraftsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".monoagent", "recording-drafts"), nil
}

// ResolveDraftDir turns a caller-supplied draft reference — a draft name or
// a path — into a directory that is guaranteed to sit inside DraftsDir,
// symlinks resolved. Anything else is refused.
func ResolveDraftDir(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	root, err := DraftsDir()
	if err != nil {
		return "", err
	}
	if ref == "" || strings.ContainsRune(ref, 0) || strings.HasPrefix(ref, "-") {
		return "", fmt.Errorf("invalid draft %q", ref)
	}
	dir := ref
	if !filepath.IsAbs(ref) {
		if !ValidID(ref) {
			return "", fmt.Errorf("invalid draft %q: give a draft name or an absolute path", ref)
		}
		dir = filepath.Join(root, ref)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("no drafts yet: %w", err)
	}
	realDir, err := filepath.EvalSymlinks(filepath.Clean(dir))
	if err != nil {
		return "", fmt.Errorf("draft %q: %w", ref, err)
	}
	rel, err := filepath.Rel(realRoot, realDir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("draft %q is outside %s", ref, root)
	}
	if fi, err := os.Stat(realDir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("draft %q is not a directory", ref)
	}
	return realDir, nil
}
