// Package tracesig signs the chain traces mono-agent sends out over HTTP
// and verifies them when they come back in, so a loop that leaves through
// an HTTP request and returns through a webhook stays on its chain and is
// stopped by the hop limit (U10).
//
// Anyone can write a `trace` object into a request body, so an unsigned
// trace from outside is never trusted (a caller could join someone else's
// chain, or keep a loop on a chain of its choosing). A signed one can only
// have come from this machine: the key lives in ~/.monoagent/trace.key and
// never leaves it. The signature covers the chain, the hop and an expiry
// together, so a replayed token can put a run on its chain at that hop or
// later, never lower, and only until it expires (TTL).
package tracesig

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Header carries a signed trace on an HTTP request.
const Header = "X-Monoagent-Trace"

// version is the token format. v1 tokens (no expiry) are not accepted: a
// v1 token in flight across an upgrade starts a fresh chain, which is safe.
const version = "v2"

// TTL is how long a token verifies after it was signed. A request to a
// local webhook comes back in milliseconds; the hour is for an outside
// system that returns the header later (propagate_trace). Past it a
// request starts a fresh chain, as an unsigned one does: a leaked or logged
// token cannot be replayed onto its chain for ever.
const TTL = time.Hour

// maxSkew is how far in the future a token's expiry may lie beyond TTL:
// processes on one machine share its clock, so a token that claims more
// was not signed by Sign.
const maxSkew = time.Minute

// now is the clock tokens are signed and checked against (tests move it).
var now = time.Now

// keyFile is the key's name under ~/.monoagent.
const keyFile = "trace.key"

var chainRe = regexp.MustCompile(`^chn_[A-Za-z0-9_-]+$`)

// Sign returns the token for chain at hop under key, valid for TTL:
// v2.<chain>.<hop>.<expiry unix seconds>.<mac>.
func Sign(key []byte, chain string, hop int) (string, error) {
	if !chainRe.MatchString(chain) {
		return "", fmt.Errorf("tracesig: not a chain id: %q", chain)
	}
	if hop < 0 {
		return "", fmt.Errorf("tracesig: negative hop %d", hop)
	}
	h := strconv.Itoa(hop)
	exp := strconv.FormatInt(now().Add(TTL).Unix(), 10)
	return version + "." + chain + "." + h + "." + exp + "." + mac(key, chain, h, exp), nil
}

// Verify returns the chain and hop of a token signed with key that has not
// expired.
func Verify(key []byte, token string) (chain string, hop int, ok bool) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 5 || parts[0] != version || !chainRe.MatchString(parts[1]) {
		return "", 0, false
	}
	hop, err := strconv.Atoi(parts[2])
	if err != nil || hop < 0 || strconv.Itoa(hop) != parts[2] {
		return "", 0, false
	}
	exp, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil || strconv.FormatInt(exp, 10) != parts[3] {
		return "", 0, false
	}
	want := mac(key, parts[1], parts[2], parts[3])
	if !hmac.Equal([]byte(want), []byte(parts[4])) {
		return "", 0, false
	}
	t := now()
	if t.Unix() >= exp || exp > t.Add(TTL+maxSkew).Unix() {
		return "", 0, false
	}
	return parts[1], hop, true
}

func mac(key []byte, chain, hop, exp string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(version + "|" + chain + "|" + hop + "|" + exp))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

var (
	keyMu    sync.Mutex
	keyCache = map[string][]byte{} // key path → key
)

// Key returns this machine's trace key, ~/.monoagent/trace.key, creating
// it (32 random bytes, mode 0600) on first use. Every process on the
// machine (the daemon, a CLI run) reads the same file, so a token one signs
// another verifies.
func Key() ([]byte, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("tracesig: home directory: %w", err)
	}
	return keyAt(filepath.Join(home, ".monoagent", keyFile))
}

func keyAt(path string) ([]byte, error) {
	keyMu.Lock()
	defer keyMu.Unlock()
	if k, ok := keyCache[path]; ok {
		return k, nil
	}
	k, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		k, err = create(path)
	} else if err == nil {
		err = checkPrivate(path)
	}
	if err != nil {
		return nil, fmt.Errorf("tracesig: key: %w", err)
	}
	if len(k) < 32 {
		return nil, fmt.Errorf("tracesig: key %s is too short (%d bytes); if no process is creating it right now, delete it and a new one is made", path, len(k))
	}
	keyCache[path] = k
	return k, nil
}

// create writes a new key to a temporary file and links it into place.
// The link is atomic and fails if the key already exists, so processes
// racing to create it all end up with the same, complete key: a loser reads
// the winner's, never a half-written file.
func create(path string) ([]byte, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), keyFile+".tmp-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return nil, err
	}
	if _, err := tmp.Write(k); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Link(tmp.Name(), path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return os.ReadFile(path)
		}
		// Some filesystems (FUSE, network mounts) have no hard links. An
		// exclusive create still lets only one process win; a loser that
		// reads before the winner finished writing gets a short key, which
		// keyAt rejects without caching, so the next call reads it whole.
		return createExclusive(path, k)
	}
	return k, nil
}

func createExclusive(path string, k []byte) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(k); err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return nil, err
	}
	return k, nil
}

// NewChainID returns a fresh chain id, in the shape orggrant.NewChainID
// makes (chn_ and 20 lowercase base32 characters). This package cannot
// import orggrant, which depends on internal/workflow.
func NewChainID() string {
	b := make([]byte, 13)
	if _, err := rand.Read(b); err != nil {
		panic("tracesig: crypto/rand failed: " + err.Error())
	}
	return "chn_" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))[:20]
}
