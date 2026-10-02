package openaiapi

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
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
	defaultAutoTimeout       = 8 * time.Second
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
	// AutoTimeout is how long Jev gets to pick the model of a request for
	// "auto"; past it the rule picks.
	AutoTimeout time.Duration
	// ImageRuntimes are the runtimes whose models can generate images, in the
	// order the first installed one is looked for. nil means defaultImageRuntimes,
	// and a list with nothing in it switches image generation off; read it through
	// ImageRuntimeList.
	ImageRuntimes []string
	// ToolRuntimes are the runtimes that serve tool calling. Empty means
	// defaultToolRuntimes; read it through ToolRuntimeList.
	ToolRuntimes []string
}

// defaultImageRuntimes make images when MONOAGENT_API_IMAGE_RUNTIMES is not
// set. monomind does not report image output, so which runtimes make images is
// a list the operator can change, not knowledge of any CLI (D7). Read only.
var defaultImageRuntimes = []string{"codex", "antigravity"}

// ImageRuntimeList is the runtimes whose models can generate images: the
// configured ones, or the defaults when none were configured. A list configured
// with nothing in it is empty, not the default. The result is read only.
func (c Config) ImageRuntimeList() []string {
	if c.ImageRuntimes == nil {
		return defaultImageRuntimes
	}
	return c.ImageRuntimes
}

// ImagesOff reports whether image generation is switched off: the list is empty,
// which MONOAGENT_API_IMAGE_RUNTIMES=none makes it.
func (c Config) ImagesOff() bool { return len(c.ImageRuntimeList()) == 0 }

// CanMakeImages reports whether m can generate an image: its runtime is in the
// image list and it can write the file, which a chat-only runtime has no native
// tool to do, however the operator lists it.
func (c Config) CanMakeImages(m ModelInfo) bool {
	return m.Class >= Sandboxed && slices.Contains(c.ImageRuntimeList(), m.Runtime)
}

// Capabilities is what GET /v1/models says a model can do: text, and image for
// a model that can generate images.
func (c Config) Capabilities(m ModelInfo) []string {
	if c.CanMakeImages(m) {
		return []string{"text", "image"}
	}
	return []string{"text"}
}

// ParseImageRuntimes reads MONOAGENT_API_IMAGE_RUNTIMES: runtime ids separated
// by commas, in the order the first installed one is looked for. Case and
// spaces do not matter, "agy" means antigravity and a repeat counts once. An
// empty value is the default list, and "none" alone is the off switch: a list
// with nothing in it, so that no model makes images.
func ParseImageRuntimes(v string) ([]string, error) {
	if strings.TrimSpace(v) == "" {
		return slices.Clone(defaultImageRuntimes), nil
	}
	if strings.EqualFold(strings.TrimSpace(v), "none") {
		return []string{}, nil
	}
	var out []string
	for _, part := range strings.Split(v, ",") {
		rt := strings.ToLower(strings.TrimSpace(part))
		if rt == "none" {
			return nil, fmt.Errorf("MONOAGENT_API_IMAGE_RUNTIMES: none switches image generation off and is not a runtime to list with others, got %q", v)
		}
		if alias, ok := aliases[rt]; ok {
			rt = alias
		}
		if !runtimeRE.MatchString(rt) {
			return nil, fmt.Errorf("MONOAGENT_API_IMAGE_RUNTIMES must be a comma-separated list of runtime ids, such as codex,antigravity, got %q", v)
		}
		if !slices.Contains(out, rt) {
			out = append(out, rt)
		}
	}
	return out, nil
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
	if c.AutoTimeout <= 0 {
		c.AutoTimeout = defaultAutoTimeout
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
// MaxConcurrentLimit), MONOAGENT_API_TURN_TIMEOUT (a duration of at least 10s,
// such as 15m), MONOAGENT_API_IMAGE_RUNTIMES (see ParseImageRuntimes) and
// MONOAGENT_API_TOOL_RUNTIMES (see ParseToolRuntimes). An unset variable keeps
// the default.
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
	if v := getenv("MONOAGENT_API_IMAGE_RUNTIMES"); v != "" {
		list, err := ParseImageRuntimes(v)
		if err != nil {
			return Config{}, err
		}
		c.ImageRuntimes = list
	}
	if v := getenv("MONOAGENT_API_TOOL_RUNTIMES"); v != "" {
		list, err := ParseToolRuntimes(v)
		if err != nil {
			return Config{}, err
		}
		c.ToolRuntimes = list
	}
	return c, nil
}
