package openaiapi

import (
	"fmt"
	"strconv"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// Defaults of Config.
const (
	defaultMaxConcurrent     = 4
	defaultTurnTimeout       = 10 * time.Minute
	defaultBodyLimit         = 2 << 20 // 2 MiB
	defaultCatalogTTL        = 5 * time.Minute
	defaultStreamCommitAfter = 5 * time.Second
	defaultKeepAlive         = 15 * time.Second
	minTurnTimeout           = 10 * time.Second
)

// Config tunes a Gateway. The zero value of any field means its default.
type Config struct {
	// MaxConcurrent turns run at once; more get 429.
	MaxConcurrent int
	// TurnTimeout is the wall-clock cap of one turn.
	TurnTimeout time.Duration
	// BodyLimit is the largest request body accepted, in bytes.
	BodyLimit int64
	// ScratchRoot holds the working folders of the turns: one folder per
	// profile (p-<hash>) with one slot-N folder per concurrency slot, each
	// emptied around every turn. Default ~/.monoagent/workspaces/api.
	ScratchRoot string
	// CatalogTTL is how long the model list is reused.
	CatalogTTL time.Duration
	// StreamCommitAfter is how long a stream may stay silent before the
	// response is committed as a 200 event stream (so keep-alives can flow).
	// Until then a failing turn still gets its real HTTP status.
	StreamCommitAfter time.Duration
	// KeepAlive is the interval of SSE keep-alive comments once committed.
	KeepAlive time.Duration
}

// MaxConcurrentLimit is the most turns a gateway may run at once. Every turn is
// a real agent process, and the limiter allocates a slot per turn.
const MaxConcurrentLimit = 64

func (c Config) withDefaults() (Config, error) {
	if c.MaxConcurrent <= 0 {
		c.MaxConcurrent = defaultMaxConcurrent
	}
	if c.TurnTimeout <= 0 {
		c.TurnTimeout = defaultTurnTimeout
	}
	if c.BodyLimit <= 0 {
		c.BodyLimit = defaultBodyLimit
	}
	if c.CatalogTTL <= 0 {
		c.CatalogTTL = defaultCatalogTTL
	}
	if c.StreamCommitAfter <= 0 {
		c.StreamCommitAfter = defaultStreamCommitAfter
	}
	if c.KeepAlive <= 0 {
		c.KeepAlive = defaultKeepAlive
	}
	if c.ScratchRoot == "" {
		dir, err := monomind.SandboxWorkspaceDir(scratchPurpose)
		if err != nil {
			return Config{}, err
		}
		c.ScratchRoot = dir
	}
	return c, nil
}

// ConfigFromEnv reads MONOAGENT_API_MAX_CONCURRENT (an integer from 1 to
// MaxConcurrentLimit) and MONOAGENT_API_TURN_TIMEOUT (a duration of at least
// 10s, such as 15m). An unset variable keeps the default.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	var c Config
	if v := getenv("MONOAGENT_API_MAX_CONCURRENT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > MaxConcurrentLimit {
			return Config{}, fmt.Errorf("MONOAGENT_API_MAX_CONCURRENT must be an integer from 1 to %d, got %q", MaxConcurrentLimit, v)
		}
		c.MaxConcurrent = n
	}
	if v := getenv("MONOAGENT_API_TURN_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < minTurnTimeout {
			return Config{}, fmt.Errorf("MONOAGENT_API_TURN_TIMEOUT must be a duration of at least %v, such as 15m, got %q", minTurnTimeout, v)
		}
		c.TurnTimeout = d
	}
	return c, nil
}
