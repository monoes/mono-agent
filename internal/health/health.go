// Package health is the setup & health-check engine behind
// `monoagentcli doctor` and the GUI's Settings › System health section
// (docs/plans/2026-09-24-setup-and-health-check.md).
//
// A Check inspects one component and returns a Result; a Result may name a
// Fix by ID, which `doctor --fix` / `doctor fix <id>` applies. Checks never
// change anything themselves — only fixes do.
package health

import (
	"context"
	"database/sql"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// SchemaVersion is the `doctor --json` payload version. Bump it on any
// incompatible change; the GUI checks it.
const SchemaVersion = 1

// Status is a check outcome.
type Status string

const (
	StatusOK   Status = "ok"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
	StatusSkip Status = "skip" // not applicable, or a dependency failed
	StatusInfo Status = "info" // nothing to judge, just a fact
)

// Safety says how much consent a fix needs.
type Safety string

const (
	// SafetyAuto: local, idempotent, reversible — applied without asking.
	SafetyAuto Safety = "auto"
	// SafetyConfirm: installs software, changes user config or uses the
	// network — shown and confirmed first.
	SafetyConfirm Safety = "confirm"
	// SafetyManual: needs the user; only the command/instructions are shown.
	SafetyManual Safety = "manual"
)

// Result is one check's outcome, as serialized by `doctor --json`.
type Result struct {
	ID       string   `json:"id"`
	Group    string   `json:"group"`
	Title    string   `json:"title"`
	Status   Status   `json:"status"`
	Summary  string   `json:"summary"`
	Detail   string   `json:"detail,omitempty"`
	Required bool     `json:"required"`
	Features []string `json:"features,omitempty"`
	Fix      *FixInfo `json:"fix,omitempty"`
	Source   string   `json:"source"`
	Millis   int64    `json:"ms"`

	// FixID is set by a check to offer a fix; the runner resolves it into
	// Fix from the fix registry.
	FixID string `json:"-"`
	// Children are extra rows a check reports about the things it found
	// (e.g. one per agent runtime). Each needs its own ID; the runner
	// stamps Group/Source and lists them right after their parent.
	Children []Result `json:"-"`
}

// FixInfo describes a fix to a caller (CLI prompt, GUI button).
type FixInfo struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Safety  Safety `json:"safety"`
	Command string `json:"command,omitempty"`
	// Optional fixes add something the user may not want (e.g. one more
	// agent runtime). `doctor --fix` never applies them; only an explicit
	// `doctor fix <id>` or a GUI button does.
	Optional bool `json:"optional,omitempty"`
}

// Check is one health check.
type Check struct {
	ID       string
	Group    string
	Title    string
	Required bool     // failing it means the app itself is broken
	Features []string // what stops working when it isn't ok
	// DependsOn lists check IDs that must not fail/skip for this one to run.
	DependsOn []string
	// Network marks checks that call out to the network; they only run
	// with Options.Deep.
	Network bool
	// OnDemand checks only run with Options.OnDemand or when named by ID
	// (e.g. per-project monomind checks: `doctor --projects`).
	OnDemand bool
	Timeout  time.Duration // 0 = DefaultTimeout
	Run      func(ctx context.Context, env *Env) Result
}

// Fix repairs what a check found.
//
// A parameterized fix sets ApplyArg instead of Apply and is addressed as
// "<id>:<arg>" (e.g. "runtimes.install:claude"); "{arg}" in its Label and
// Command is replaced by the argument.
type Fix struct {
	FixInfo
	Apply    func(ctx context.Context, env *Env, progress func(line string)) error
	ApplyArg func(ctx context.Context, env *Env, arg string, progress func(line string)) error
}

// DefaultTimeout bounds a single check.
const DefaultTimeout = 10 * time.Second

// Env is everything checks and fixes touch, injected so they can be tested
// without a real machine.
type Env struct {
	Home       string // user home directory
	DataDir    string // ~/.monoagent
	DBPath     string
	Version    string // monoagentcli version
	Executable string // path of the running binary
	ProfileID  string // active profile ("" = resolve from the DB)

	// DB is the opened (not migrated) database, nil when it doesn't exist
	// yet or failed to open (DBErr).
	DB    *sql.DB
	DBErr error

	// Hooks — nil means "not available here", and the check that needs it
	// reports skip.
	PendingMigrations func(ctx context.Context) ([]string, error)
	QuickCheck        func(ctx context.Context) (string, error)
	Migrate           func(ctx context.Context, progress func(string)) error
	VaultState        func(ctx context.Context, profileID string) (string, error)
	ProfileRoot       func(profileID string) string
	EnsureProfile     func(profileID string) error
	LoginPath         func(ctx context.Context) (string, error)
	FreeBytes         func(path string) (uint64, error)
	LatestVersion     func(ctx context.Context) (string, error)

	// Node.js: the first node on PATH that isn't monoagent's managed one,
	// the active managed version, and installing a managed one.
	SystemNode  func(ctx context.Context) (path, version string, found bool)
	ManagedNode func() (version, path string, ok bool)
	InstallNode func(ctx context.Context, progress func(string)) error

	// monomind: locate the binary, handshake with it (callers memoize —
	// several checks ask), scan agent runtimes, install/upgrade it, and
	// run `monomind init` in a folder.
	FindMonomind        func() (string, error)
	MonomindHandshake   func(ctx context.Context) (*monomind.VersionInfo, error)
	ScanRuntimes        func(ctx context.Context) (*monomind.ScanResult, error)
	InstallMonomind     func(ctx context.Context, progress func(string)) error
	InitMonomindProfile func(ctx context.Context, root string, progress func(string)) error
	// MonomindDoctor runs `monomind doctor --json` in dir (rev 9); nil
	// when unavailable. MonomindProjects lists the monomind projects to
	// check inside the active profile's folder (paths relative to it).
	MonomindDoctor   func(ctx context.Context, opts monomind.DoctorOptions) (*monomind.DoctorReport, error)
	MonomindProjects func() []string

	// InstallRuntime installs one agent runtime by its scan id.
	InstallRuntime func(ctx context.Context, id string, progress func(string)) error

	// Browser and the MonoAgent extension bridge.
	FindBrowser        func() string
	ExtensionInstalled func() bool
	ExtensionDir       func() string
	Bridge             func(ctx context.Context) (BridgeInfo, bool)

	// Background services.
	Daemon           func(ctx context.Context) DaemonInfo
	APIHealth        func(ctx context.Context, addr string) error
	AutostartStatus  func(ctx context.Context) (installed bool, where string)
	InstallAutostart func(ctx context.Context, progress func(string)) error
	StartDaemon      func(ctx context.Context, progress func(string)) error

	// Agent-tool integrations (Claude Code).
	ClaudeSkills        func() (claudeFound bool, missing, stale []string)
	InstallClaudeSkills func() error
	MCPRegistration     func() (claudeFound, registered bool, where string)
	RegisterMCP         func(ctx context.Context, progress func(string)) error

	// Accounts of the active profile.
	Connections       func(ctx context.Context) ([]ConnectionInfo, error)
	TestConnection    func(ctx context.Context, id string) error
	RefreshConnection func(ctx context.Context, id string) error // silent refresh_token exchange only
	AIProviders       func(ctx context.Context) ([]ProviderInfo, error)
	TestAIProvider    func(ctx context.Context, id string) error
	LoginSessions     func(ctx context.Context) ([]SessionInfo, error)
}

// ConnectionInfo is a saved connection, without any secret.
type ConnectionInfo struct {
	ID, Platform, Label, Method string
	HasRefreshToken             bool
}

// ProviderInfo is an AI connection (legacy provider), without its key.
type ProviderInfo struct {
	ID, Name, ProviderID, Model string
}

// SessionInfo is a saved platform login session.
type SessionInfo struct {
	Platform, Username string
	Expiry             time.Time
}

// BridgeInfo is what the running extension bridge reports about itself.
type BridgeInfo struct {
	Addr      string
	Status    string // connected | waiting | unpaired
	PID       int
	Version   string
	UptimeSec int64
}

// DaemonInfo is the workflow daemon's heartbeat.
type DaemonInfo struct {
	Running bool
	PID     int
	APIAddr string
	AgeMS   int64
}

// Report is the `doctor --json` payload.
type Report struct {
	V                int            `json:"v"`
	GeneratedAt      time.Time      `json:"generated_at"`
	MonoagentVersion string         `json:"monoagent_version"`
	ProfileID        string         `json:"profile_id"`
	Deep             bool           `json:"deep"`
	Summary          map[Status]int `json:"summary"`
	Results          []Result       `json:"results"`
}

// RequiredFailures counts failed required checks — non-zero means the app
// can't work as installed.
func (r *Report) RequiredFailures() int {
	n := 0
	for _, res := range r.Results {
		if res.Required && res.Status == StatusFail {
			n++
		}
	}
	return n
}
