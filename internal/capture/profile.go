package capture

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/monoes/mono-agent/internal/profiledir"
)

// Profile-scoped captures.
//
// A capture can say which profile it belongs to (`meta.profile`), and that
// decides which inbox it lands in — and therefore, downstream, which
// knowledge store ingests it. A 'work' page must not turn up in a
// 'personal' search, and the cheapest way to guarantee that is for the two
// never to share a directory in the first place.
//
// The profile id arrives from the browser, so it is untrusted input that
// becomes a filesystem path. It is never joined into one here: every path
// goes through internal/profiledir, whose ValidProfileID rejects separators
// and '..' outright, and this package refuses to build an inbox path for an
// id it rejects. See TestProfileInbox_RejectsTraversal.

// ProfileInboxDir is the inbox's name inside a profile's monomind home.
const ProfileInboxDir = "inbox"

// ProfileInbox returns where a profile's captures land:
// <default profile root>/.monomind/inbox, i.e. the inbox of that profile's
// own monomind home (profiledir.MonomindDir).
//
// Resolved WITHOUT the database, so a profile's `profiles.root_dir`
// override is deliberately not followed. That is a choice, not an
// oversight, and it keeps one rule instead of two:
//
//   - every process that writes a capture must agree on where it goes, and
//     the extension bridge answers this question with no database handle in
//     reach. A rule that needs one would be applied by `capture list` and
//     not by the daemon writing the envelope, which is worse than not
//     following root_dir at all;
//   - the store the capture is ingested INTO does not follow root_dir
//     either — it is keyed by profile id under monomind's own brain
//     directory (see knowledge/profile-store.ts on the monomind side), so
//     an inbox that followed it would be half a promise.
//
// Relocating this data is done for all profiles at once, with the knobs
// that already exist for it: MONOMIND_INBOX/MONOMIND_HOME here and
// MONOMIND_GLOBAL_BRAIN_DIR on the monomind side. They move the unprofiled
// inbox only; a profile's inbox follows $HOME.
//
// An id profiledir will not accept is an error rather than a path — the
// caller must not be handed something it can write to.
func ProfileInbox(profileID string) (string, error) {
	id := strings.TrimSpace(profileID)
	if !profiledir.ValidProfileID(id) {
		return "", fmt.Errorf("capture: unusable profile id %q: must be non-empty and contain no '/', '\\', or '..'", clampID(id))
	}
	dir := profiledir.MonomindDir(nil, id)
	if strings.TrimSpace(dir) == "" {
		// Unreachable while ValidProfileID gates the line above, but a
		// dead path must never become a real one by way of a join.
		return "", fmt.Errorf("capture: no profile root for %q", clampID(id))
	}
	return filepath.Join(dir, ProfileInboxDir), nil
}

// InboxFor is ProfileInbox for a named profile and DefaultInbox() for the
// empty one — "no profile" keeps today's behaviour exactly.
func InboxFor(profileID string) (string, error) {
	if strings.TrimSpace(profileID) == "" {
		return DefaultInbox(), nil
	}
	return ProfileInbox(profileID)
}

// clampID bounds a rejected id before it is quoted into a message or a
// warning that ends up in meta.json: the sender chose it, and it may be
// megabytes long.
func clampID(id string) string {
	const max = 64
	r := []rune(id)
	if len(r) <= max {
		return id
	}
	return string(r[:max]) + "…"
}
