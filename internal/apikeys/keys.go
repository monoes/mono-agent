// Package apikeys issues and verifies the API keys of the OpenAI-compatible
// HTTP API. A key belongs to one profile; only its SHA-256 is stored.
package apikeys

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// KeyPrefix starts every key. It is recognisable by secret scanners and by
// people, and distinguishes a key from the legacy HTTP API token.
const KeyPrefix = "sk-ma-"

const (
	keyBytes      = 32                         // random bytes in a key
	keyLen        = len(KeyPrefix) + 43        // 43 = base64url of 32 bytes, unpadded
	prefixLen     = len(KeyPrefix) + 6         // characters kept for display
	idRandom      = 12                         // base32 characters after "key_"
	timeFmt       = "2006-01-02T15:04:05.000Z" // stored timestamps, UTC
	idPrefix      = "key_"
	lastUsedEvery = time.Minute // last_used_at is written at most this often per key
)

var (
	// ErrNotFound means no such key in the profile. Another profile's key is
	// reported the same way, never as "forbidden".
	ErrNotFound = errors.New("api key not found")
	// ErrInvalidKey is every authentication failure: malformed, unknown or
	// revoked. The caller can't tell which.
	ErrInvalidKey = errors.New("invalid api key")
	// ErrNameTaken means an active key of the profile already has the name.
	ErrNameTaken = errors.New("an active key with that name already exists in this profile")
	// ErrInvalidName means the name breaks the naming rule.
	ErrInvalidName = errors.New("key name must be 1-64 characters: letters, digits, space, '.', '_' or '-', starting with a letter or digit, not the shape of a key id (key_ followed by 12 characters from a-z and 2-7), and not holding sk-ma-, the start of every API key")
)

var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`)

// idShapeRE is what a key id looks like. A key may not be named like one: Get
// resolves an id before a name, so the name would shadow the other key.
var idShapeRE = regexp.MustCompile(`(?i)^` + idPrefix + `[a-z2-7]{12}$`)

// Key is a key's metadata. The key itself is never part of it.
type Key struct {
	ID         string     `json:"id"`
	ProfileID  string     `json:"profile_id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Context    bool       `json:"context"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

// Update changes an active key. nil fields stay as they are.
type Update struct {
	Name    *string
	Context *bool
}

// IsEmpty reports whether the update sets nothing, which a front end refuses as a
// request for no change. A field set to its zero value (context off) is set.
func (u Update) IsEmpty() bool { return u.Name == nil && u.Context == nil }

// GenerateKey returns a new random key: KeyPrefix plus 32 random bytes in
// unpadded base64url.
func GenerateKey() (string, error) {
	b := make([]byte, keyBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating api key: %w", err)
	}
	return KeyPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// HashKey is the stored form of a key: the hex SHA-256 of its text.
func HashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// looksLikeKey is the cheap shape check run before any database lookup.
func looksLikeKey(token string) bool {
	return len(token) == keyLen && strings.HasPrefix(token, KeyPrefix)
}

func newID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating key id: %w", err)
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	return idPrefix + strings.ToLower(enc)[:idRandom], nil
}

// holdsKey reports whether a name holds KeyPrefix, in any case: a key, or the
// start of one, pasted where a name goes. The alphabet of a name has every
// character a key is made of, and a name is stored in clear and listed by every
// front end, so a key in one would be shown to whoever can list keys, where only
// its hash is meant to be kept. A key cut short is no better, hence the prefix
// and not a whole key.
func holdsKey(name string) bool { return strings.Contains(strings.ToLower(name), KeyPrefix) }

func validName(name string) bool {
	return nameRE.MatchString(name) && !idShapeRE.MatchString(name) && !holdsKey(name)
}
