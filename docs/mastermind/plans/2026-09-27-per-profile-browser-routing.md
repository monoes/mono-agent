# Per-Profile Browser Routing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `Skill("mastermind-execute")` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Each browser profile's MonoAgent Bridge extension can be bound to one monoagent profile. Every browser action a monoagent profile runs goes to that browser, so two profiles can drive two accounts on the same platform at the same time. The dashboard gets a **This profile / All profiles** toggle. "All profiles" shows everything from every profile, with each row labelled by its profile. "This profile" is today's dashboard, unchanged.

**Architecture:** The bridge (`internal/extension.Server`) keeps one socket per extension instance instead of one socket total. Each extension generates an instance id once and reports it, its bound profile and a label in the auth frame. Commands carry a `Target` (profile or instance) that the bridge resolves to one socket. The CLI narrows its bridge to the run's profile (`ProfileBridge`). The first tab a bridge opens pins it to one browser, so a whole run stays in that browser. The binding lives in the extension's `chrome.storage.local`. It can be changed from the side panel, the CLI (`extension bind`) or the desktop app (Settings → Browsers).

**Dashboard architecture:** The dashboard reads four CLI commands (`summary`, `org summary`, `workflow list`, `workflow executions --all`). Each gains `--all-profiles`, following the convention `capture list --all-profiles` already set. That flag runs the existing per-profile code once per profile and merges the results (`summary.Merge`), tagging list rows with `profile_id`/`profile_name`. No SQL changes. In global view, rows from other profiles show a profile chip and a "Switch to <profile>" button instead of run/stop/toggle. Those actions are profile-scoped in the app, so they stay limited to the active profile. The toggle is remembered per window (localStorage).

**Tech Stack:** Go 1.26 (gorilla/websocket, cobra), MV3 Chrome extension (plain JS, `node --test`), Wails desktop app (React + vitest).

## Global Constraints

- Module path `github.com/monoes/mono-agent`; Go files stay under 500 lines. Put new code in new files; don't grow `server.go` (856 lines) or `sidepanel.js` (1184 lines).
- **Never run a branch build of `monoagentcli` against the real HOME.** Every CLI call made while building this uses `HOME=~/scratch/per-profile/home`, because any command applies migrations to `~/.monoagent/monoagent.db`.
- Temp dirs: `export TMPDIR=/home/monoes/scratch/agent-tmp GOTMPDIR=/home/monoes/scratch/agent-tmp` at the top of every build/test shell. Never use `/tmp`.
- Never test against the user's bridge on 9222 (`monoagent-bridge.service`, their Edge is attached). Test bridges run with `MONOAGENT_EXTENSION_PORT=9232` and a scratch HOME.
- Kill background processes by PID file, never `pkill -f` (it matches the calling shell).
- The repo's pre-bash hook blocks `rm -rf` text. Move scratch dirs to `~/scratch/tmp-evicted-<date>/` instead.
- Commits: conventional style `type(scope): description`. Author is the global git config. **No `Co-Authored-By`, no "Generated with Claude Code".**
- Work on branch `feat/per-profile-browsers` in a worktree (`git worktree add ../mono-agent-ppb -b feat/per-profile-browsers origin/master`). The main checkout has unrelated uncommitted `wails-app/` changes, and the user's Edge loads the extension from it. Ship everything as **one PR** (every master push is a release).
- Backwards compatibility is required in all four directions: an old extension with the new bridge, the new extension with an old bridge, an old CLI with the new bridge, and the new CLI with an old bridge.
- Routing rule (the product decision this plan implements). A command for profile P goes to:
  1. the newest browser bound to P;
  2. otherwise the newest **unbound** browser (the "default browser" everyone has today);
  3. never a browser bound to a *different* profile. That browser is logged into the other profile's accounts, so this case returns an error instead.

  A command with no profile goes to the newest unbound browser, else the newest browser.

---

## File map

| File | Status | Responsibility |
|---|---|---|
| `internal/extension/conns.go` | new | `extConn`, `Target`, `ConnInfo`, connection registry, `resolve`, errors |
| `internal/extension/route.go` | new | `SendCommandTo`, targeted writes, `CreateTabFor`/`CloseTabFor`, `parseTabID`, binding frame + `set_binding` bookkeeping |
| `internal/extension/relay_routes.go` | new | `/monoagent/resolve`, `/monoagent/browsers`, `routeError` shared by server and client |
| `internal/extension/profile_bridge.go` | new | `ProfileBridge` (routes and pins by profile), `ForProfile` on both bridges |
| `internal/extension/server.go` | modify | auth frame v2, socket-per-browser lifecycle, relay reads target |
| `internal/extension/{capture,request,recording,cdp,status,profile_list,remote,protocol}.go` | modify | origin-aware replies, targeted capture/CDP, `Browsers` count, remote targeting |
| `internal/browser/provider.go` | modify | `ProfileRouter` interface |
| `cmd/monoagentcli/browser_profile.go` | new | `bridgeForProfile`, `setupProfileBridge`, `browserProfile`, `routeHint` |
| `cmd/monoagentcli/workflow.go` | modify | lazy provider routes per run profile, no global wait lock |
| `cmd/monoagentcli/extension_browsers.go` | new | `extension browsers`, `extension bind`, `extension unbind` |
| 8 call sites in `cmd/monoagentcli/` | modify | use `setupProfileBridge` |
| `chrome-extension/browser_binding.js` (+test) | new | instance id, binding storage, auth fields, frames |
| `chrome-extension/sidepanel_binding.js` | new | side panel "Automations in this browser" control |
| `chrome-extension/{background.js,sidepanel.html,sidepanel.js,manifest.json}` | modify | wire it in; version 1.5.0 |
| `wails-app/app_browsers.go` (+test) | new | `GetBrowsers`, `BindBrowser` → CLI |
| `wails-app/frontend/src/components/settings/BrowserBindingsSection.jsx` (+test) | new | Settings → Browsers |
| `docs/BROWSER_PROFILES.md`, `README.md`, `docs/security/threat-model.md`, `CHANGELOG.md` | new/modify | docs |
| `scripts/e2e/per-profile-browsers.sh`, `scripts/e2e/set-extension-storage.mjs` | new | two-browser end-to-end check |
| `internal/summary/global.go` (+test) | new | `Merge` per-profile summaries into a global one; `SortRecent`; profile headlines |
| `internal/summary/{summary,workflows,system}.go` | modify | `Scope`, `Profiles`; `profile_id`/`profile_name` on exec, schedule and session rows |
| `cmd/monoagentcli/summary_all.go` | new | `summaryOptions` (extracted), `buildGlobalSummary`, section split |
| `cmd/monoagentcli/{summary,workflow,org_summary}.go` | modify | `--all-profiles` on `summary`, `workflow list`, `workflow executions --all`, `org summary` |
| `wails-app/app_dashboard_scope.go` (+test) | new | global variants of the four dashboard bindings |
| `wails-app/frontend/src/pages/dashboard/{scope.js,ProfileChip.jsx,ProfilesCard.jsx,profileSwitch.js}` (+tests) | new | toggle persistence, row chips, per-profile card, switch-and-reload |
| `wails-app/frontend/src/pages/{Dashboard.jsx,dashboard/*Card.jsx,dashboard/useDashboardData.js}`, `services/api.js`, `locales/{en,es}.json` | modify | toggle, scoped loading, chips, i18n |

---

### Task 1: The bridge holds one socket per browser

**Files:**
- Create: `internal/extension/conns.go`
- Modify: `internal/extension/server.go` (struct, `IsConnected`, `Close`, `authFrame`, `authenticate`, `handleWS`, `pingLoop`, `readLoop`)
- Modify: `internal/extension/capture.go` (`writeCommand`, `dispatch`)
- Modify: `internal/extension/request.go` (`serveRequest`, `runHandler`, `replyError`, `writeReply`, `Request`)
- Modify: `internal/extension/recording.go` (`serveRecording`, `finishRecording`, `ackRecording`)
- Modify: `internal/extension/status.go` (`Status.Browsers`)
- Modify: `internal/extension/capture_test.go:512,516`
- Test: `internal/extension/conns_test.go`

**Interfaces:**
- Produces: `type Target struct{ Profile, Instance string }`; `type ConnInfo struct{ Instance, Profile, Label, Version string; Legacy, Conflict bool; ConnectedAt time.Time }`; `var ErrNoExtension, ErrBrowserGone error`; `type NoBrowserError struct{ Profile string; BoundTo []string }`; `(*Server).resolve(Target) (*extConn, error)`; `(*Server).ResolveTarget(Target) (ConnInfo, error)`; `(*Server).Browsers() []ConnInfo`; `(*extConn).write([]byte) error`; `(*extConn).setBinding(profile, label string)`; `(*extConn).boundProfile() string`; `(*extConn).info() ConnInfo`; `authFrame` gains `Instance, Profile, Label, Version`; `Request.Origin ConnInfo` (`json:"-"`); `Status.Browsers int`.

- [ ] **Step 1: Write the failing tests**

Create `internal/extension/conns_test.go`:

```go
package extension

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// dialBrowser connects one fake extension that reports an instance id, a
// bound profile and a label — what extension ≥1.5 sends — and completes
// the auth handshake with the on-disk token.
func dialBrowser(t *testing.T, wsURL, instance, profile, label string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	tok, err := CurrentToken()
	if err != nil {
		t.Fatalf("CurrentToken: %v", err)
	}
	if err := conn.WriteJSON(authFrame{Type: "auth", Token: tok, Instance: instance, Profile: profile, Label: label}); err != nil {
		t.Fatalf("write auth frame: %v", err)
	}
	return conn
}

// waitBrowsers polls until exactly n browsers are connected.
func waitBrowsers(t *testing.T, srv *Server, n int) []ConnInfo {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got := srv.Browsers()
		if len(got) == n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("browsers = %d (%+v), want %d", len(got), got, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

const (
	instA = "aaaaaaaa-0000-4000-8000-000000000001"
	instB = "bbbbbbbb-0000-4000-8000-000000000002"
	instC = "cccccccc-0000-4000-8000-000000000003"
)

func TestTwoBrowsersStayConnected(t *testing.T) {
	srv, base, wsURL, _ := startStatusTestServer(t)
	dialBrowser(t, wsURL, instA, "p-a", "Edge Work")
	waitBrowsers(t, srv, 1)
	dialBrowser(t, wsURL, instB, "p-b", "Chrome Home")
	got := waitBrowsers(t, srv, 2)

	// Newest first.
	if got[0].Instance != instB || got[1].Instance != instA {
		t.Fatalf("order = %s, %s; want B then A", got[0].Instance, got[1].Instance)
	}
	if got[1].Profile != "p-a" || got[1].Label != "Edge Work" {
		t.Fatalf("A = %+v", got[1])
	}
	if st := fetchHealth(t, base); !st.Connected || st.Browsers != 2 {
		t.Fatalf("health = %+v, want connected with 2 browsers", st)
	}
}

func TestSameInstanceReplacesItself(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	first := dialBrowser(t, wsURL, instA, "p-a", "")
	waitBrowsers(t, srv, 1)
	dialBrowser(t, wsURL, instA, "p-a", "")
	// The old socket is closed by the server once its replacement is in.
	_ = first.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, _, err := first.ReadMessage(); err != nil {
			break
		}
	}
	waitBrowsers(t, srv, 1)
}

func TestLegacyExtensionsStillReplaceEachOther(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	dialAndAuthenticate(t, wsURL)
	waitBrowsers(t, srv, 1)
	dialAndAuthenticate(t, wsURL)
	time.Sleep(100 * time.Millisecond)
	got := waitBrowsers(t, srv, 1)
	if !got[0].Legacy || got[0].Instance != legacyInstanceID {
		t.Fatalf("legacy browser = %+v", got[0])
	}
}

func TestInvalidBindingIsDropped(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	dialBrowser(t, wsURL, instA, "../etc", "x")
	got := waitBrowsers(t, srv, 1)
	if got[0].Profile != "" {
		t.Fatalf("profile = %q, want it dropped", got[0].Profile)
	}
}

func TestShortInstanceIDCountsAsLegacy(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	dialBrowser(t, wsURL, "abc", "p-a", "")
	got := waitBrowsers(t, srv, 1)
	if !got[0].Legacy || got[0].Profile != "p-a" {
		t.Fatalf("got %+v, want legacy slot keeping its profile", got[0])
	}
}

func TestResolveRules(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	dialBrowser(t, wsURL, instA, "p-a", "")
	waitBrowsers(t, srv, 1)
	b := dialBrowser(t, wsURL, instB, "", "")
	waitBrowsers(t, srv, 2)
	dialBrowser(t, wsURL, instC, "p-c", "")
	waitBrowsers(t, srv, 3)

	cases := []struct {
		name string
		t    Target
		want string
	}{
		{"bound profile", Target{Profile: "p-a"}, instA},
		{"unbound fallback", Target{Profile: "p-b"}, instB},
		{"no target prefers unbound", Target{}, instB},
		{"instance", Target{Instance: instC}, instC},
		{"instance beats profile", Target{Profile: "p-a", Instance: instC}, instC},
	}
	for _, tc := range cases {
		info, err := srv.ResolveTarget(tc.t)
		if err != nil || info.Instance != tc.want {
			t.Errorf("%s: got %q, %v; want %q", tc.name, info.Instance, err, tc.want)
		}
	}

	if _, err := srv.ResolveTarget(Target{Instance: "dddddddd-0000-4000-8000-000000000004"}); !errors.Is(err, ErrBrowserGone) {
		t.Errorf("missing instance: err = %v, want ErrBrowserGone", err)
	}

	_ = b.Close()
	waitBrowsers(t, srv, 2)
	_, err := srv.ResolveTarget(Target{Profile: "p-b"})
	var nb *NoBrowserError
	if !errors.As(err, &nb) || nb.Profile != "p-b" || len(nb.BoundTo) != 2 || nb.BoundTo[0] != "p-a" || nb.BoundTo[1] != "p-c" {
		t.Fatalf("no unbound browser left: err = %v", err)
	}
	// With no unbound browser, an untargeted command takes the newest one.
	if info, err := srv.ResolveTarget(Target{}); err != nil || info.Instance != instC {
		t.Fatalf("untargeted = %q, %v; want newest (C)", info.Instance, err)
	}
}

func TestResolveWithNothingConnected(t *testing.T) {
	srv, _, _, _ := startStatusTestServer(t)
	if _, err := srv.ResolveTarget(Target{Profile: "p-a"}); !errors.Is(err, ErrNoExtension) {
		t.Fatalf("err = %v, want ErrNoExtension", err)
	}
}

func TestConflictIsFlagged(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	dialBrowser(t, wsURL, instA, "p-a", "")
	waitBrowsers(t, srv, 1)
	dialBrowser(t, wsURL, instB, "p-a", "")
	got := waitBrowsers(t, srv, 2)
	if !got[0].Conflict || !got[1].Conflict {
		t.Fatalf("both should be flagged: %+v", got)
	}
	if info, _ := srv.ResolveTarget(Target{Profile: "p-a"}); info.Instance != instB {
		t.Fatalf("conflict resolves to %q, want the newest (B)", info.Instance)
	}
}

func TestRequestReplyGoesBackToTheAskingBrowser(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	srv.HandleRequest("test.origin", func(_ context.Context, req *Request, _ ProgressFunc) (any, error) {
		return map[string]string{"asker": req.Origin.Instance, "profile": req.Origin.Profile}, nil
	})
	a := dialBrowser(t, wsURL, instA, "p-a", "")
	waitBrowsers(t, srv, 1)
	b := dialBrowser(t, wsURL, instB, "p-b", "")
	waitBrowsers(t, srv, 2)

	if err := b.WriteJSON(map[string]any{"kind": "request", "id": "r1", "method": "test.origin"}); err != nil {
		t.Fatal(err)
	}
	_ = b.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, msg, err := b.ReadMessage()
	if err != nil {
		t.Fatalf("B read reply: %v", err)
	}
	var reply struct {
		ID   string            `json:"id"`
		OK   bool              `json:"ok"`
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(msg, &reply); err != nil || reply.ID != "r1" || !reply.OK ||
		reply.Data["asker"] != instB || reply.Data["profile"] != "p-b" {
		t.Fatalf("reply = %s (%v)", msg, err)
	}
	_ = a.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, msg, err := a.ReadMessage(); err == nil {
		t.Fatalf("A received %s; the reply belongs to B", msg)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export TMPDIR=/home/monoes/scratch/agent-tmp GOTMPDIR=/home/monoes/scratch/agent-tmp && go test ./internal/extension/ -run 'TestTwoBrowsers|TestSameInstance|TestLegacyExtensions|TestInvalidBinding|TestShortInstance|TestResolve|TestConflict|TestRequestReplyGoesBack' 2>&1 | head -20`
Expected: build failure: `unknown field Instance in struct literal of type authFrame`, `srv.Browsers undefined`, `undefined: legacyInstanceID`.

- [ ] **Step 3: Create `internal/extension/conns.go`**

```go
package extension

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/monoes/mono-agent/internal/profiledir"
)

// One bridge, several browsers.
//
// Every browser profile with the extension installed runs its own copy of
// it: its own service worker, its own chrome.storage, its own socket to this
// server. The server used to keep exactly one of those sockets, and a second
// one knocked the first off. So two browser profiles could never be driven
// at once, and since a browser profile holds one cookie jar per site,
// neither could two accounts on the same site.
//
// Now each socket is kept under the instance id its extension generated
// once and reports in its auth frame, along with the monoagent profile the
// user bound that browser to. A command goes to one browser, chosen by a
// Target (see resolve). An extension too old to report an instance id is
// kept under legacyInstanceID. That is exactly the old single-socket
// behaviour, for everyone who has not updated the extension.

// legacyInstanceID keys the socket of an extension that sent no usable
// instance id. There is one such slot, so two old extensions still replace
// each other the way every extension used to.
const legacyInstanceID = "legacy"

// validInstanceID bounds what an extension may call itself. The id shows
// up in URLs, logs and CLI output, so it is held to a UUID-ish alphabet.
var validInstanceID = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

// maxLabelLen bounds the user-chosen browser label.
const maxLabelLen = 60

// Target names the browser a command is for. Instance wins when set. A
// Profile resolves to the browser bound to that monoagent profile. The zero
// Target is "the browser a caller that knows nothing about profiles would
// have reached before", i.e. the default browser.
type Target struct {
	Profile  string
	Instance string
}

// ConnInfo describes one connected browser.
type ConnInfo struct {
	Instance    string    `json:"instance"`
	Profile     string    `json:"profile,omitempty"`
	Label       string    `json:"label,omitempty"`
	Version     string    `json:"version,omitempty"`
	Legacy      bool      `json:"legacy,omitempty"`
	ConnectedAt time.Time `json:"connectedAt"`
	// Conflict: another connected browser is bound to the same profile.
	// Only the newest of them is used.
	Conflict bool `json:"conflict,omitempty"`
}

// ErrNoExtension means no browser is connected at all. The words are the
// ones every caller already knows from before there were several.
var ErrNoExtension = errors.New("no extension connected")

// ErrBrowserGone means a specific browser was asked for and is not
// connected, e.g. it was closed in the middle of a run.
var ErrBrowserGone = errors.New("that browser is not connected")

// NoBrowserError means browsers are connected, but none may run this
// profile: each one is bound to some other profile, and there is no
// unbound (default) browser to fall back to.
type NoBrowserError struct {
	Profile string
	BoundTo []string // the profiles the connected browsers ARE bound to
}

func (e *NoBrowserError) Error() string {
	return fmt.Sprintf("no browser is set up for profile %q (the connected browsers run profiles %s) — "+
		"open the MonoAgent Bridge side panel in the browser this profile should use and choose it under "+
		"\"Automations in this browser\", or run `monoagentcli extension bind <browser> %s`",
		e.Profile, strings.Join(e.BoundTo, ", "), e.Profile)
}

// extConn is one browser's socket.
type extConn struct {
	id          string
	legacy      bool
	version     string
	connectedAt time.Time

	ws      *websocket.Conn
	writeMu sync.Mutex // gorilla/websocket forbids concurrent writers, per socket

	mu      sync.Mutex // guards profile and label, which change while connected
	profile string
	label   string
}

func newExtConn(ws *websocket.Conn, auth authFrame, now time.Time) *extConn {
	c := &extConn{id: legacyInstanceID, legacy: true, ws: ws, connectedAt: now}
	if validInstanceID.MatchString(auth.Instance) {
		c.id, c.legacy = auth.Instance, false
	}
	c.version = cleanLabel(auth.Version)
	c.setBinding(auth.Profile, auth.Label)
	return c
}

// setBinding records which profile the browser says it runs. An id that
// profiledir would refuse is dropped rather than trusted, because the id is
// used as a directory name further down.
func (c *extConn) setBinding(profile, label string) {
	profile = strings.TrimSpace(profile)
	if profile != "" && !profiledir.ValidProfileID(profile) {
		profile = ""
	}
	c.mu.Lock()
	c.profile = profile
	c.label = cleanLabel(label)
	c.mu.Unlock()
}

func cleanLabel(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxLabelLen {
		s = string(r[:maxLabelLen])
	}
	return s
}

func (c *extConn) boundProfile() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.profile
}

func (c *extConn) write(data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.ws.WriteMessage(websocket.TextMessage, data)
}

func (c *extConn) ping() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.ws.WriteMessage(websocket.PingMessage, nil)
}

func (c *extConn) info() ConnInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return ConnInfo{
		Instance: c.id, Profile: c.profile, Label: c.label, Version: c.version,
		Legacy: c.legacy, ConnectedAt: c.connectedAt,
	}
}

// installConn makes c the socket for its browser and closes the one it
// replaces. That is routine: an MV3 service worker that respawns before its
// previous socket was reaped reconnects on top of itself many times a day.
func (s *Server) installConn(c *extConn) {
	s.connMu.Lock()
	if s.conns == nil {
		s.conns = make(map[string]*extConn)
	}
	old := s.conns[c.id]
	s.conns[c.id] = c
	s.connMu.Unlock()
	if old != nil {
		s.logger.Info().Str("instance", c.id).Msg("replacing the previous connection from this browser")
		_ = old.ws.Close()
	}
}

// removeConn forgets c, unless a newer socket from the same browser has
// already taken its slot.
func (s *Server) removeConn(c *extConn) {
	s.connMu.Lock()
	if s.conns[c.id] == c {
		delete(s.conns, c.id)
	}
	s.connMu.Unlock()
}

// connList is every connected browser, newest first.
func (s *Server) connList() []*extConn {
	s.connMu.Lock()
	out := make([]*extConn, 0, len(s.conns))
	for _, c := range s.conns {
		out = append(out, c)
	}
	s.connMu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].connectedAt.After(out[j].connectedAt) })
	return out
}

// resolve picks the browser a command for t goes to:
//
//   - an Instance: that browser, or ErrBrowserGone;
//   - a Profile: the newest browser bound to it; failing that, the newest
//     unbound browser (the "default browser" everyone had before binding
//     existed); never a browser bound to a DIFFERENT profile, because that
//     browser is logged into the other profile's accounts;
//   - neither: the newest unbound browser, else the newest browser, which
//     is what an older client that sends no target always got.
func (s *Server) resolve(t Target) (*extConn, error) {
	conns := s.connList()
	if len(conns) == 0 {
		return nil, ErrNoExtension
	}
	if t.Instance != "" {
		for _, c := range conns {
			if c.id == t.Instance {
				return c, nil
			}
		}
		return nil, fmt.Errorf("%w: %s", ErrBrowserGone, t.Instance)
	}
	var unbound *extConn
	var boundTo []string
	for _, c := range conns {
		p := c.boundProfile()
		if t.Profile != "" && p == t.Profile {
			return c, nil
		}
		if p == "" {
			if unbound == nil {
				unbound = c
			}
		} else {
			boundTo = append(boundTo, p)
		}
	}
	if unbound != nil {
		return unbound, nil
	}
	if t.Profile == "" {
		return conns[0], nil
	}
	sort.Strings(boundTo)
	return nil, &NoBrowserError{Profile: t.Profile, BoundTo: slices.Compact(boundTo)}
}

// ResolveTarget reports which browser t resolves to right now.
func (s *Server) ResolveTarget(t Target) (ConnInfo, error) {
	c, err := s.resolve(t)
	if err != nil {
		return ConnInfo{}, err
	}
	return c.info(), nil
}

// Browsers lists the connected browsers, newest first. Two browsers bound
// to the same profile are both flagged Conflict.
func (s *Server) Browsers() []ConnInfo {
	conns := s.connList()
	infos := make([]ConnInfo, len(conns))
	perProfile := map[string]int{}
	for i, c := range conns {
		infos[i] = c.info()
		if p := infos[i].Profile; p != "" {
			perProfile[p]++
		}
	}
	for i := range infos {
		if p := infos[i].Profile; p != "" && perProfile[p] > 1 {
			infos[i].Conflict = true
		}
	}
	return infos
}
```

- [ ] **Step 4: Rewire `server.go` to the registry**

In the `Server` struct, replace these two lines:

```go
	conn    *websocket.Conn
	connMu  sync.Mutex
	writeMu sync.Mutex // serializes writes; gorilla/websocket forbids concurrent writers
```

with:

```go
	// conns holds one socket per connected browser, keyed by the instance
	// id its extension reports (see conns.go). Guarded by connMu.
	conns  map[string]*extConn
	connMu sync.Mutex
```

Replace `IsConnected`:

```go
// IsConnected reports whether at least one browser's extension is
// connected. Which one a command reaches is resolve's business.
func (s *Server) IsConnected() bool {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	return len(s.conns) > 0
}
```

In `Close`, replace everything after `s.waitRecordingReaper()` with:

```go
	s.connMu.Lock()
	conns := s.conns
	s.conns = nil
	s.connMu.Unlock()

	var firstErr error
	for _, c := range conns {
		if err := c.ws.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
```

Replace the `authFrame` struct:

```go
// authFrame is the first message an extension socket must send after
// upgrade. Anything else — wrong type, wrong/missing token, or no message
// within authTimeout — gets the connection closed without ever being
// installed, so an unauthenticated client can never replace an
// already-authenticated one.
//
// Instance, Profile, Label and Version come from extension ≥1.5 (see
// chrome-extension/browser_binding.js). An older extension omits them and
// lands in the legacy slot; an older bridge ignores them.
type authFrame struct {
	Type     string `json:"type"`
	Token    string `json:"token"`
	Instance string `json:"instance,omitempty"`
	Profile  string `json:"profile,omitempty"`
	Label    string `json:"label,omitempty"`
	Version  string `json:"version,omitempty"`
}
```

Change `authenticate` to return the frame. Its signature becomes `func (s *Server) authenticate(conn *websocket.Conn) (authFrame, bool)`. Every `return false` becomes `return authFrame{}, false`, and the final `return true` becomes `return auth, true`. The doc comment's "It never touches s.conn" becomes "It never installs the connection".

In `handleWS`, replace everything from `if !s.authenticate(conn) {` to the end of the function with:

```go
	auth, ok := s.authenticate(conn)
	if !ok {
		_ = conn.Close()
		return
	}

	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	// Only reached after authenticate succeeds, so an unauthenticated
	// socket never replaces anything.
	c := newExtConn(conn, auth, time.Now())
	s.installConn(c)

	s.connOnce.Do(func() { close(s.connected) })
	s.logger.Info().Str("remote", conn.RemoteAddr().String()).Str("instance", c.id).
		Str("profile", c.boundProfile()).Msg("extension connected")

	done := make(chan struct{})
	go s.pingLoop(c, done)
	s.readLoop(c)
	close(done)
```

Replace `pingLoop`:

```go
// pingLoop periodically pings one browser's socket so a dead peer is
// detected via the read deadline in handleWS/readLoop instead of hanging
// indefinitely.
func (s *Server) pingLoop(c *extConn, done <-chan struct{}) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			// Piggybacked on the ping: a capture whose chunks stopped
			// arriving has nobody waiting on it when it was the extension
			// flushing its queue, so something has to time it out.
			s.sweepCaptures()
			if err := c.ping(); err != nil {
				return
			}
		case <-done:
			return
		}
	}
}
```

In `readLoop`, change the signature to `func (s *Server) readLoop(c *extConn)`. Replace its deferred cleanup with:

```go
	defer func() {
		s.removeConn(c)
		_ = c.ws.Close()
		s.logger.Info().Str("instance", c.id).Msg("extension disconnected")
	}()
```

Change `conn.ReadMessage()` to `c.ws.ReadMessage()`. Change `s.serveRequest(msg)` to `s.serveRequest(c, msg)`, `s.serveRecording(msg)` to `s.serveRecording(c, msg)`, and `s.dispatch(&resp)` to `s.dispatch(c, &resp)`.

- [ ] **Step 5: Route the existing write paths through the registry**

`capture.go`: replace `writeCommand`:

```go
// writeCommand marshals and writes one command to the default browser.
func (s *Server) writeCommand(cmd *Command) error {
	data, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("marshal command: %w", err)
	}
	c, err := s.resolve(Target{})
	if err != nil {
		return err
	}
	if err := c.write(data); err != nil {
		return fmt.Errorf("write command: %w", err)
	}
	return nil
}
```

`capture.go`: change `func (s *Server) dispatch(resp *Response)` to `func (s *Server) dispatch(c *extConn, resp *Response)`. The body stays the same (Task 3 uses `c`). In `capture_test.go` lines 512 and 516, change `srv.dispatch(&Response{` to `srv.dispatch(nil, &Response{`.

`request.go`: add to `Request`:

```go
	// Origin is the browser that asked, filled in by the server and never
	// read off the wire. A handler uses it to answer "for this browser",
	// e.g. profile.list defaulting to the profile the browser is bound to.
	Origin ConnInfo `json:"-"`
```

Then thread the asking browser through:
- `func (s *Server) serveRequest(c *extConn, msg []byte)`. After the `req.ID == ""` check, add `req.Origin = c.info()`. Every `s.replyError(req.ID, …)` becomes `s.replyError(c, req.ID, …)`, and `s.runHandler(handler, &req)` becomes `s.runHandler(c, handler, &req)`.
- `func (s *Server) runHandler(c *extConn, handler RequestHandler, req *Request)`. Every `s.writeReply(&Reply{…})` becomes `s.writeReply(c, &Reply{…})`, and `s.replyError(req.ID, …)` becomes `s.replyError(c, req.ID, …)`.
- `func (s *Server) replyError(c *extConn, id, code string, err error)` calls `s.writeReply(c, …)`.
- Replace `writeReply`:

```go
// writeReply writes one frame back to the browser that asked. Failures are
// logged and swallowed: the socket being gone is the ordinary case (the
// browser closed, the worker was suspended), not an error anyone can act on.
func (s *Server) writeReply(c *extConn, reply *Reply) {
	data, err := json.Marshal(reply)
	if err != nil {
		s.logger.Error().Err(err).Msg("marshal reply")
		return
	}
	if c == nil {
		return
	}
	if err := c.write(data); err != nil {
		s.logger.Debug().Err(err).Str("id", reply.ID).Msg("could not write reply")
	}
}
```

`recording.go`: `serveRecording(c *extConn, msg []byte)` passes `c` to `ackRecording(c, …)` and to `go s.finishRecording(c, ing, out.Stopped, f.ID)`. `finishRecording(c *extConn, ing …, st …, ackID string)` passes `c` to each `ackRecording`. The recording reaper's two calls (`recording.go:240` and `:251`, `s.finishRecording(ing, st, "")`) become `s.finishRecording(nil, ing, st, "")`: nobody is waiting on an ack for a reaped recording. Replace `ackRecording`:

```go
// ackRecording writes a Response for a frame that carried an id, on the
// socket the frame came in on.
func (s *Server) ackRecording(c *extConn, id string, ok bool, data any, errMsg string) {
	if id == "" || c == nil {
		return
	}
	blob, err := json.Marshal(&Response{ID: id, Success: ok, Data: data, Error: errMsg, Type: RecordingAckType})
	if err != nil {
		return
	}
	if err := c.write(blob); err != nil {
		s.logger.Debug().Err(err).Str("id", id).Msg("could not ack recording frame")
	}
}
```

`status.go`: add to `Status` after `InFlight`:

```go
	// Browsers is how many browser profiles are attached right now. Only
	// the count is public here; which profile each one runs is behind the
	// relay token, at /monoagent/browsers.
	Browsers int `json:"browsers"`
```

In `(*Server).Status()`, add `Browsers: len(s.connList()),` to the `st := Status{…}` literal.

Remove the now-unused `"github.com/gorilla/websocket"` imports only where the compiler reports them.

- [ ] **Step 6: Run the new tests and the whole package**

Run: `export TMPDIR=/home/monoes/scratch/agent-tmp GOTMPDIR=/home/monoes/scratch/agent-tmp && go build ./... && go test ./internal/extension/ 2>&1 | tail -20`
Expected: `ok  github.com/monoes/mono-agent/internal/extension`. All old tests (single-extension, auth, capture, CDP, recording) still pass, because a fake extension that sends no instance id lands in the legacy slot and behaves exactly as before.

- [ ] **Step 7: Commit**

```bash
git add internal/extension/
git commit -m "feat(bridge): hold one extension socket per browser profile"
```

---

### Task 2: Targeted commands, binding updates, bound-profile default

**Files:**
- Create: `internal/extension/route.go`
- Modify: `internal/extension/server.go` (`SendCommand`, `CreateTab`, `CloseTab`, `readLoop` switch)
- Modify: `internal/extension/capture.go` (delete `writeCommand`; `CapturePage` uses `req.Target`; `CaptureRequest.Target`)
- Modify: `internal/extension/protocol.go` (`CmdSetBinding`, `KindBinding`)
- Modify: `internal/extension/profile_list.go`
- Test: `internal/extension/route_test.go`

**Interfaces:**
- Consumes: Task 1 registry.
- Produces: `(*Server).SendCommandTo(t Target, cmd *Command, timeout time.Duration) (*Response, error)`; `(*Server).CreateTabFor(t Target, url string) (int, error)`; `(*Server).CloseTabFor(t Target, tabID int) error`; `parseTabID(*Response) (int, error)`; `const CmdSetBinding = "set_binding"`; `const KindBinding = "binding"`; `CaptureRequest.Target Target`; `listProfiles(ctx, src, bound string)`.

- [ ] **Step 1: Write the failing tests**

Create `internal/extension/route_test.go`:

```go
package extension

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/monoes/mono-agent/internal/profiledir"
)

// answerNext reads the next command on conn and answers it with data after
// delay. It returns the command so the test can check what arrived where.
func answerNext(t *testing.T, conn *websocket.Conn, delay time.Duration, data any) *Command {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Errorf("read command: %v", err)
		return nil
	}
	var cmd Command
	if err := json.Unmarshal(msg, &cmd); err != nil {
		t.Errorf("decode command: %v", err)
		return nil
	}
	time.Sleep(delay)
	if err := conn.WriteJSON(Response{ID: cmd.ID, Success: true, Data: data}); err != nil {
		t.Errorf("write response: %v", err)
	}
	return &cmd
}

// expectSilence fails if conn receives anything within d.
func expectSilence(t *testing.T, conn *websocket.Conn, d time.Duration) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(d))
	if _, msg, err := conn.ReadMessage(); err == nil {
		t.Fatalf("unexpected frame: %s", msg)
	}
}

func TestSendCommandToRoutesByProfile(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	a := dialBrowser(t, wsURL, instA, "p-a", "")
	waitBrowsers(t, srv, 1)
	b := dialBrowser(t, wsURL, instB, "p-b", "")
	waitBrowsers(t, srv, 2)

	done := make(chan int, 1)
	go func() {
		id, err := srv.CreateTabFor(Target{Profile: "p-b"}, "https://example.com")
		if err != nil {
			t.Errorf("CreateTabFor: %v", err)
		}
		done <- id
	}()
	cmd := answerNext(t, b, 0, map[string]any{"tabId": 42})
	if cmd == nil || cmd.Type != CmdCreateTab {
		t.Fatalf("B got %+v, want create_tab", cmd)
	}
	if id := <-done; id != 42 {
		t.Fatalf("tab id = %d, want 42", id)
	}
	expectSilence(t, a, 200*time.Millisecond)
}

func TestTwoBrowsersRunInParallel(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	a := dialBrowser(t, wsURL, instA, "p-a", "")
	waitBrowsers(t, srv, 1)
	b := dialBrowser(t, wsURL, instB, "p-b", "")
	waitBrowsers(t, srv, 2)

	const slow = 300 * time.Millisecond
	go answerNext(t, a, slow, map[string]any{"tabId": 1})
	go answerNext(t, b, slow, map[string]any{"tabId": 2})

	start := time.Now()
	var wg sync.WaitGroup
	for _, p := range []string{"p-a", "p-b"} {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			if _, err := srv.CreateTabFor(Target{Profile: p}, "https://example.com"); err != nil {
				t.Errorf("%s: %v", p, err)
			}
		}(p)
	}
	wg.Wait()
	if elapsed := time.Since(start); elapsed > 2*slow-50*time.Millisecond {
		t.Fatalf("two browsers took %s — they ran one after the other", elapsed)
	}
}

func TestSetBindingUpdatesTheServersView(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	a := dialBrowser(t, wsURL, instA, "p-a", "Old")
	waitBrowsers(t, srv, 1)

	errc := make(chan error, 1)
	go func() {
		_, err := srv.SendCommandTo(Target{Instance: instA}, &Command{
			Type: CmdSetBinding, Params: map[string]interface{}{"profile": "p-z", "label": "New"},
		}, 5*time.Second)
		errc <- err
	}()
	cmd := answerNext(t, a, 0, map[string]any{"profile": "p-z"})
	if cmd == nil || cmd.Type != CmdSetBinding {
		t.Fatalf("A got %+v", cmd)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	got := srv.Browsers()[0]
	if got.Profile != "p-z" || got.Label != "New" {
		t.Fatalf("after set_binding: %+v", got)
	}
}

func TestBindingFrameUpdatesTheServersView(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	a := dialBrowser(t, wsURL, instA, "", "")
	waitBrowsers(t, srv, 1)
	if err := a.WriteJSON(map[string]string{"kind": KindBinding, "profile": "p-q", "label": "Work Edge"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		got := srv.Browsers()[0]
		if got.Profile == "p-q" && got.Label == "Work Edge" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("binding frame not applied: %+v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCapturePageGoesToTheTargetBrowser(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	srv.SetCaptureInbox(t.TempDir())
	a := dialBrowser(t, wsURL, instA, "p-a", "")
	waitBrowsers(t, srv, 1)
	b := dialBrowser(t, wsURL, instB, "p-b", "")
	waitBrowsers(t, srv, 2)

	errc := make(chan error, 1)
	go func() {
		_, err := srv.CapturePage(CaptureRequest{Target: Target{Profile: "p-a"}, Timeout: 5 * time.Second})
		errc <- err
	}()
	_ = a.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := a.ReadMessage()
	if err != nil {
		t.Fatalf("A read: %v", err)
	}
	var cmd Command
	_ = json.Unmarshal(msg, &cmd)
	if cmd.Type != CmdPageCapture {
		t.Fatalf("A got %s", msg)
	}
	_ = a.WriteJSON(Response{ID: cmd.ID, Success: false, Error: "stop here", Type: CmdPageCapture})
	if err := <-errc; err == nil {
		t.Fatal("capture should report the extension's error")
	}
	expectSilence(t, b, 200*time.Millisecond)
}

func TestNoBrowserErrorReachesTheCaller(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	dialBrowser(t, wsURL, instA, "p-a", "")
	waitBrowsers(t, srv, 1)
	_, err := srv.CreateTabFor(Target{Profile: "p-b"}, "https://example.com")
	var nb *NoBrowserError
	if !errors.As(err, &nb) {
		t.Fatalf("err = %v, want *NoBrowserError", err)
	}
}

func TestProfileListDefaultsToTheBoundProfile(t *testing.T) {
	src := func(context.Context) ([]profiledir.Profile, error) {
		return []profiledir.Profile{
			{ID: "p-home", Name: "Personal"},
			{ID: "p-work", Name: "Work", Default: true},
		}, nil
	}
	got, err := listProfiles(context.Background(), src, "p-home")
	if err != nil {
		t.Fatal(err)
	}
	if got.Default != "p-home" || !got.Profiles[0].Default || got.Profiles[1].Default {
		t.Fatalf("bound browser should default to its profile: %+v", got)
	}
	unbound, _ := listProfiles(context.Background(), src, "")
	if unbound.Default != "p-work" {
		t.Fatalf("unbound browser keeps monoagent's default: %+v", unbound)
	}
	stale, _ := listProfiles(context.Background(), src, "p-gone")
	if stale.Default != "p-work" {
		t.Fatalf("a binding to a deleted profile must not win: %+v", stale)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `export TMPDIR=/home/monoes/scratch/agent-tmp GOTMPDIR=/home/monoes/scratch/agent-tmp && go test ./internal/extension/ -run 'TestSendCommandTo|TestTwoBrowsersRun|TestSetBinding|TestBindingFrame|TestCapturePageGoes|TestNoBrowserError|TestProfileListDefaults' 2>&1 | head`
Expected: build failure: `srv.CreateTabFor undefined`, `undefined: CmdSetBinding`, `unknown field Target`.

- [ ] **Step 3: Protocol constants**

In `protocol.go`, add inside the `const ( … CmdRace = "race" )` block:

```go
	// CmdSetBinding asks one browser's extension to bind itself to a
	// monoagent profile (params: profile, optional label). Sent by
	// `monoagentcli extension bind` and the app's Settings → Browsers.
	CmdSetBinding = "set_binding"
```

Next to `KindRecording`, add:

```go
// KindBinding is a push from the extension saying its binding changed (the
// user picked another profile in the side panel). Not acked: the next
// command routed by it is the acknowledgement.
const KindBinding = "binding"
```

- [ ] **Step 4: Create `internal/extension/route.go`**

```go
package extension

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// SendCommandTo sends a command to the browser t resolves to and waits for
// its response. If the response reports failure, an error is returned.
func (s *Server) SendCommandTo(t Target, cmd *Command, timeout time.Duration) (*Response, error) {
	if cmd.ID == "" {
		cmd.ID = uuid.New().String()
	}
	c, err := s.resolve(t)
	if err != nil {
		return nil, err
	}

	ch := make(chan *Response, 1)
	s.pendMu.Lock()
	s.pending[cmd.ID] = ch
	s.pendMu.Unlock()
	defer func() {
		s.pendMu.Lock()
		delete(s.pending, cmd.ID)
		s.pendMu.Unlock()
	}()

	if err := s.writeCommandTo(c, cmd); err != nil {
		return nil, err
	}
	s.logger.Debug().Str("id", cmd.ID).Str("type", cmd.Type).Str("instance", c.id).Msg("command sent")

	select {
	case resp := <-ch:
		if !resp.Success {
			return resp, fmt.Errorf("extension error: %s", resp.Error)
		}
		if cmd.Type == CmdSetBinding {
			// The extension stored it; mirror it here so the very next
			// command routes by it rather than by the auth frame from
			// whenever this socket opened.
			c.applyBindingParams(cmd.Params)
		}
		return resp, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("command %s timed out after %s", cmd.Type, timeout)
	}
}

// writeCommand marshals cmd and writes it to the browser t resolves to.
func (s *Server) writeCommand(t Target, cmd *Command) error {
	c, err := s.resolve(t)
	if err != nil {
		return err
	}
	return s.writeCommandTo(c, cmd)
}

func (s *Server) writeCommandTo(c *extConn, cmd *Command) error {
	data, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("marshal command: %w", err)
	}
	if err := c.write(data); err != nil {
		return fmt.Errorf("write command: %w", err)
	}
	return nil
}

// CreateTabFor opens a tab at url in the browser t resolves to.
func (s *Server) CreateTabFor(t Target, url string) (int, error) {
	resp, err := s.SendCommandTo(t, &Command{
		Type:   CmdCreateTab,
		Params: map[string]interface{}{"url": url},
	}, 30*time.Second)
	if err != nil {
		return 0, err
	}
	return parseTabID(resp)
}

// CloseTabFor closes a tab in the browser t resolves to.
func (s *Server) CloseTabFor(t Target, tabID int) error {
	_, err := s.SendCommandTo(t, &Command{Type: CmdCloseTab, TabID: tabID}, 30*time.Second)
	return err
}

// parseTabID reads create_tab's answer.
func parseTabID(resp *Response) (int, error) {
	dataMap, _ := resp.Data.(map[string]interface{})
	if dataMap == nil {
		return 0, fmt.Errorf("create_tab response missing data")
	}
	tabIDRaw, ok := dataMap["tabId"]
	if !ok {
		return 0, fmt.Errorf("create_tab response missing tabId")
	}
	tabID, ok := tabIDRaw.(float64)
	if !ok {
		return 0, fmt.Errorf("tabId is not a number: %T", tabIDRaw)
	}
	return int(tabID), nil
}

// bindingFrame is the extension's kind:"binding" push.
type bindingFrame struct {
	Kind    string `json:"kind"`
	Profile string `json:"profile"`
	Label   string `json:"label"`
}

// serveBinding applies a binding the user changed in the side panel.
func (s *Server) serveBinding(c *extConn, msg []byte) {
	var f bindingFrame
	if err := json.Unmarshal(msg, &f); err != nil {
		s.logger.Warn().Err(err).Msg("invalid binding frame")
		return
	}
	c.setBinding(f.Profile, f.Label)
	s.logger.Info().Str("instance", c.id).Str("profile", c.boundProfile()).Msg("browser binding changed")
}

// applyBindingParams mirrors a set_binding the extension accepted. A
// missing label keeps the current one.
func (c *extConn) applyBindingParams(p map[string]interface{}) {
	c.mu.Lock()
	label := c.label
	c.mu.Unlock()
	if l, ok := p["label"].(string); ok {
		label = l
	}
	profile, _ := p["profile"].(string)
	c.setBinding(profile, label)
}
```

- [ ] **Step 5: Point the old entry points at the targeted ones**

In `server.go`, replace the bodies of `SendCommand`, `CreateTab` and `CloseTab`:

```go
// SendCommand sends a command to the default browser (see resolve) and
// waits for the matching response.
func (s *Server) SendCommand(cmd *Command, timeout time.Duration) (*Response, error) {
	return s.SendCommandTo(Target{}, cmd, timeout)
}

// CreateTab opens a tab in the default browser.
func (s *Server) CreateTab(url string) (int, error) { return s.CreateTabFor(Target{}, url) }

// CloseTab closes a tab in the default browser.
func (s *Server) CloseTab(tabID int) error { return s.CloseTabFor(Target{}, tabID) }
```

Remove the `uuid` import from `server.go` if it is no longer used there.

In `readLoop`'s `switch frameKind(msg)`, add:

```go
		case KindBinding:
			s.serveBinding(c, msg)
			continue
```

In `capture.go`: delete the Task-1 `writeCommand` (it now lives in `route.go` with a target). Add to `CaptureRequest` after `Inbox string`:

```go
	// Target picks the browser to capture in. The zero Target is the
	// default browser. Like Inbox, it rides the relay hop as query
	// parameters and never reaches the extension.
	Target Target
```

In `CapturePage`, change `if err := s.writeCommand(cmd); err != nil {` to `if err := s.writeCommand(req.Target, cmd); err != nil {`.

- [ ] **Step 6: profile.list defaults to the browser's bound profile**

In `profile_list.go`, change the handler registration to:

```go
	s.HandleRequest(MethodProfileList, func(ctx context.Context, req *Request, _ ProgressFunc) (any, error) {
		return listProfiles(ctx, src, req.Origin.Profile)
	})
```

and replace `listProfiles`:

```go
// listProfiles is the handler's body, kept separate so a test can call it
// without a server. bound is the profile the asking browser is bound to.
// When that profile exists it becomes the default, so the capture picker
// and the automations agree on which brain this browser belongs to.
func listProfiles(ctx context.Context, src ProfileSource, bound string) (*ProfileList, error) {
	profiles, err := src(ctx)
	if err != nil {
		// Not an error the user did anything about: no database yet, a
		// locked one, a CLI built without profiles. The picker falls back
		// to saving the way it did before.
		return nil, Unavailable("%s", fmt.Sprintf("cannot list profiles: %v", err))
	}
	out := &ProfileList{Profiles: profiles}
	if out.Profiles == nil {
		// A picker renders an empty list; it cannot render a null.
		out.Profiles = []profiledir.Profile{}
	}
	if bound != "" && slices.ContainsFunc(out.Profiles, func(p profiledir.Profile) bool { return p.ID == bound }) {
		for i := range out.Profiles {
			out.Profiles[i].Default = out.Profiles[i].ID == bound
		}
	}
	for _, p := range out.Profiles {
		if p.Default {
			out.Default = p.ID
			break
		}
	}
	return out, nil
}
```

Add `"slices"` to its imports.

- [ ] **Step 7: Run the package tests**

Run: `export TMPDIR=/home/monoes/scratch/agent-tmp GOTMPDIR=/home/monoes/scratch/agent-tmp && go build ./... && go test ./internal/extension/ 2>&1 | tail -5`
Expected: `ok  github.com/monoes/mono-agent/internal/extension`.

- [ ] **Step 8: Commit**

```bash
git add internal/extension/
git commit -m "feat(bridge): route commands and captures to a target browser"
```

---

### Task 3: Targeting over the relay, the routing endpoints, and CDP

**Files:**
- Create: `internal/extension/relay_routes.go`
- Modify: `internal/extension/server.go` (`Start` mux, `handleRelay`)
- Modify: `internal/extension/capture.go` (`serveRelayCapture`, `RemoteSender.CapturePage`)
- Modify: `internal/extension/remote.go` (targeted `RemoteSender`)
- Modify: `internal/extension/cdp.go` (client pinned to one browser; scoped events)
- Test: `internal/extension/relay_routes_test.go`

**Interfaces:**
- Consumes: Task 2 `SendCommandTo`, `ResolveTarget`, `Browsers`.
- Produces: `GET /monoagent/resolve?profile=&instance=` (token) → `ConnInfo` | 404 `{code:"no_browser"|"browser_gone"}` | 503 `{code:"no_extension"}`; `GET /monoagent/browsers` (token) → `{"browsers":[ConnInfo]}`; relay and CDP accept `profile`/`instance` query params; `var ErrBridgeTooOld error`; `(*RemoteSender).SendCommandTo`, `.ResolveTarget`, `.Browsers() ([]ConnInfo, error)`, `.CreateTabFor`, `.CloseTabFor`, `.SetBinding(instance, profile, label string) error`.

- [ ] **Step 1: Write the failing tests**

Create `internal/extension/relay_routes_test.go`:

```go
package extension

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestRemoteSenderRoutesByProfile(t *testing.T) {
	srv, base, wsURL, _ := startStatusTestServer(t)
	a := dialBrowser(t, wsURL, instA, "p-a", "")
	waitBrowsers(t, srv, 1)
	b := dialBrowser(t, wsURL, instB, "p-b", "")
	waitBrowsers(t, srv, 2)

	r := NewRemoteSender(base)
	done := make(chan int, 1)
	go func() {
		id, err := r.CreateTabFor(Target{Profile: "p-b"}, "https://example.com")
		if err != nil {
			t.Errorf("CreateTabFor: %v", err)
		}
		done <- id
	}()
	if cmd := answerNext(t, b, 0, map[string]any{"tabId": 9}); cmd == nil || cmd.Type != CmdCreateTab {
		t.Fatalf("B got %+v", cmd)
	}
	if id := <-done; id != 9 {
		t.Fatalf("tab = %d", id)
	}
	expectSilence(t, a, 200*time.Millisecond)
}

func TestRemoteResolveAndBrowsers(t *testing.T) {
	srv, base, wsURL, _ := startStatusTestServer(t)
	r := NewRemoteSender(base)
	if _, err := r.ResolveTarget(Target{Profile: "p-a"}); !errors.Is(err, ErrNoExtension) {
		t.Fatalf("empty bridge: err = %v, want ErrNoExtension", err)
	}

	dialBrowser(t, wsURL, instA, "p-a", "Edge Work")
	waitBrowsers(t, srv, 1)
	dialBrowser(t, wsURL, instB, "p-b", "")
	waitBrowsers(t, srv, 2)

	info, err := r.ResolveTarget(Target{Profile: "p-a"})
	if err != nil || info.Instance != instA || info.Label != "Edge Work" {
		t.Fatalf("resolve p-a = %+v, %v", info, err)
	}
	_, err = r.ResolveTarget(Target{Profile: "p-x"})
	var nb *NoBrowserError
	if !errors.As(err, &nb) || nb.Profile != "p-x" || strings.Join(nb.BoundTo, ",") != "p-a,p-b" {
		t.Fatalf("resolve p-x err = %v", err)
	}
	if _, err := r.ResolveTarget(Target{Instance: instC}); !errors.Is(err, ErrBrowserGone) {
		t.Fatalf("resolve gone instance err = %v", err)
	}

	list, err := r.Browsers()
	if err != nil || len(list) != 2 {
		t.Fatalf("Browsers = %+v, %v", list, err)
	}
}

func TestRoutingEndpointsNeedTheToken(t *testing.T) {
	_, base, _, _ := startStatusTestServer(t)
	for _, path := range []string{"/monoagent/browsers", "/monoagent/resolve?profile=p-a"} {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s without token = %d, want 401", path, resp.StatusCode)
		}
	}
}

func TestRemoteResolveOnAnOldBridge(t *testing.T) {
	old := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(old.Close)
	r := &RemoteSender{baseURL: old.URL, client: &http.Client{}}
	if _, err := r.ResolveTarget(Target{Profile: "p-a"}); !errors.Is(err, ErrBridgeTooOld) {
		t.Fatalf("err = %v, want ErrBridgeTooOld", err)
	}
	if _, err := r.Browsers(); !errors.Is(err, ErrBridgeTooOld) {
		t.Fatalf("Browsers err = %v, want ErrBridgeTooOld", err)
	}
}

func TestCdpClientIsPinnedToOneBrowser(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	a := dialBrowser(t, wsURL, instA, "p-a", "")
	waitBrowsers(t, srv, 1)
	b := dialBrowser(t, wsURL, instB, "p-b", "")
	waitBrowsers(t, srv, 2)

	tok, _ := CurrentToken()
	cdpURL := strings.Replace(wsURL, "/monoagent", "/monoagent/cdp?profile=p-b", 1)
	client, _, err := websocket.DefaultDialer.Dial(cdpURL, http.Header{tokenHeader: {tok}})
	if err != nil {
		t.Fatalf("dial cdp: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if err := client.WriteJSON(Command{ID: "c1", Type: CmdCdpAttach, TabID: 5}); err != nil {
		t.Fatal(err)
	}
	if cmd := answerNext(t, b, 0, map[string]any{"tabId": 5}); cmd == nil || cmd.Type != CmdCdpAttach {
		t.Fatalf("B got %+v, want cdp_attach", cmd)
	}
	_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := client.ReadMessage(); err != nil { // the attach reply
		t.Fatalf("attach reply: %v", err)
	}

	// An event from A (same tab id, other browser) must not reach this client.
	_ = a.WriteJSON(Response{Type: CdpEventType, Success: true, Data: map[string]any{"from": "A"}})
	_ = b.WriteJSON(Response{Type: CdpEventType, Success: true, Data: map[string]any{"from": "B"}})
	_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, msg, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("event: %v", err)
	}
	var ev struct {
		Data map[string]string `json:"data"`
	}
	_ = json.Unmarshal(msg, &ev)
	if ev.Data["from"] != "B" {
		t.Fatalf("client got %s, want only B's event", msg)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `export TMPDIR=/home/monoes/scratch/agent-tmp GOTMPDIR=/home/monoes/scratch/agent-tmp && go test ./internal/extension/ -run 'TestRemoteSenderRoutes|TestRemoteResolve|TestRoutingEndpoints|TestCdpClientIsPinned' 2>&1 | head`
Expected: build failure: `r.CreateTabFor undefined`, `undefined: ErrBridgeTooOld`.

- [ ] **Step 3: Create `internal/extension/relay_routes.go`**

```go
package extension

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// The routing endpoints: which browsers are attached, and which one a
// profile resolves to. Both need the relay token, because the answer names
// the user's profiles and browser labels. /monoagent/health, which is open,
// only says how many browsers there are.

// routeError is how the bridge reports a routing failure over HTTP, and how
// RemoteSender turns it back into the same Go error.
type routeError struct {
	Code    string   `json:"code"`
	Message string   `json:"error"`
	Profile string   `json:"profile,omitempty"`
	BoundTo []string `json:"boundTo,omitempty"`
}

const (
	routeCodeNoExtension = "no_extension"
	routeCodeNoBrowser   = "no_browser"
	routeCodeBrowserGone = "browser_gone"
)

func routeErrorFor(err error) (int, routeError) {
	var nb *NoBrowserError
	switch {
	case errors.As(err, &nb):
		return http.StatusNotFound, routeError{Code: routeCodeNoBrowser, Message: err.Error(), Profile: nb.Profile, BoundTo: nb.BoundTo}
	case errors.Is(err, ErrNoExtension):
		return http.StatusServiceUnavailable, routeError{Code: routeCodeNoExtension, Message: err.Error()}
	default:
		return http.StatusNotFound, routeError{Code: routeCodeBrowserGone, Message: err.Error()}
	}
}

func (e routeError) err() error {
	switch e.Code {
	case routeCodeNoBrowser:
		return &NoBrowserError{Profile: e.Profile, BoundTo: e.BoundTo}
	case routeCodeNoExtension:
		return ErrNoExtension
	default:
		return fmt.Errorf("%w (%s)", ErrBrowserGone, e.Message)
	}
}

// targetFromQuery reads the profile/instance a relayed request is for.
func targetFromQuery(q url.Values) Target {
	return Target{Profile: strings.TrimSpace(q.Get("profile")), Instance: strings.TrimSpace(q.Get("instance"))}
}

// targetValues is the inverse, for the client side.
func targetValues(t Target) url.Values {
	v := url.Values{}
	if t.Profile != "" {
		v.Set("profile", t.Profile)
	}
	if t.Instance != "" {
		v.Set("instance", t.Instance)
	}
	return v
}

// routeAllowed applies the relay's gate: same machine, right token.
func (s *Server) routeAllowed(w http.ResponseWriter, r *http.Request) bool {
	if !checkOrigin(r) || !loopbackHost(r.Host) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	if s.token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get(tokenHeader)), []byte(s.token)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

// handleResolve serves GET /monoagent/resolve.
func (s *Server) handleResolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.routeAllowed(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	info, err := s.ResolveTarget(targetFromQuery(r.URL.Query()))
	if err != nil {
		code, body := routeErrorFor(err)
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(body)
		return
	}
	_ = json.NewEncoder(w).Encode(info)
}

// handleBrowsers serves GET /monoagent/browsers.
func (s *Server) handleBrowsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.routeAllowed(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"browsers": s.Browsers()})
}
```

- [ ] **Step 4: Server side of the relay**

In `Start`, add to the mux:

```go
	mux.HandleFunc("/monoagent/resolve", s.handleResolve)
	mux.HandleFunc("/monoagent/browsers", s.handleBrowsers)
```

In `handleRelay`, add after the timeout parsing:

```go
	// Which browser, for callers new enough to say. An older caller sends
	// neither parameter and gets the default browser, as it always did.
	target := targetFromQuery(r.URL.Query())
```

Then change `s.serveRelayCapture(w, &cmd, timeout, r.URL.Query().Get("inbox"))` to `s.serveRelayCapture(w, target, &cmd, timeout, r.URL.Query().Get("inbox"))`, and `resp, err := s.SendCommand(&cmd, timeout)` to `resp, err := s.SendCommandTo(target, &cmd, timeout)`.

In `capture.go`, change `serveRelayCapture` to take the target:

```go
func (s *Server) serveRelayCapture(w http.ResponseWriter, target Target, cmd *Command, timeout time.Duration, inbox string) {
	req := captureRequestFromCommand(cmd, timeout, inbox)
	req.Target = target
	res, err := s.CapturePage(req)
```

(the rest of the function is unchanged).

- [ ] **Step 5: Client side (`remote.go`)**

Add `ErrBridgeTooOld` after `ErrRelayUnauthorized`:

```go
// ErrBridgeTooOld means the bridge answering predates multi-browser routing
// (no /monoagent/resolve). Commands still work but go to its one browser;
// restarting the bridge (the daemon, or `monoagentcli extension serve`)
// on the current version turns routing on.
var ErrBridgeTooOld = errors.New("the running bridge predates per-profile browsers — restart it (the daemon or `monoagentcli extension serve`) to route by profile")

// routeProbeTimeout bounds one resolve/browsers request: loopback, and the
// bridge answers from memory.
const routeProbeTimeout = 2 * time.Second
```

Replace `SendCommand` with `SendCommandTo` plus a wrapper. Only the first lines change; the body from `ctx, cancel :=` on stays as it is:

```go
func (r *RemoteSender) SendCommand(cmd *Command, timeout time.Duration) (*Response, error) {
	return r.SendCommandTo(Target{}, cmd, timeout)
}

// SendCommandTo relays cmd to the browser t resolves to. An old bridge
// ignores the target parameters and uses its one browser.
func (r *RemoteSender) SendCommandTo(t Target, cmd *Command, timeout time.Duration) (*Response, error) {
	body, err := json.Marshal(cmd)
	if err != nil {
		return nil, fmt.Errorf("marshal command: %w", err)
	}
	if timeout <= 0 {
		timeout = defaultTimeout // what handleRelay applies to a missing timeout
	}
	url := fmt.Sprintf("%s/monoagent/relay?timeout_ms=%d", r.baseURL, timeout.Milliseconds())
	if enc := targetValues(t).Encode(); enc != "" {
		url += "&" + enc
	}
	// … unchanged from `ctx, cancel := context.WithTimeout(` to the end …
}
```

Replace `CreateTab`/`CloseTab` with targeted versions and add the routing calls:

```go
// CreateTab asks the remote server's default browser to open a new tab.
func (r *RemoteSender) CreateTab(url string) (int, error) { return r.CreateTabFor(Target{}, url) }

// CreateTabFor opens a tab in the browser t resolves to.
func (r *RemoteSender) CreateTabFor(t Target, url string) (int, error) {
	resp, err := r.SendCommandTo(t, &Command{
		Type:   CmdCreateTab,
		Params: map[string]interface{}{"url": url},
	}, 30*time.Second)
	if err != nil {
		return 0, err
	}
	return parseTabID(resp)
}

// CloseTab asks the remote server's default browser to close a tab.
func (r *RemoteSender) CloseTab(tabID int) error { return r.CloseTabFor(Target{}, tabID) }

// CloseTabFor closes a tab in the browser t resolves to.
func (r *RemoteSender) CloseTabFor(t Target, tabID int) error {
	_, err := r.SendCommandTo(t, &Command{Type: CmdCloseTab, TabID: tabID}, 30*time.Second)
	return err
}

// ResolveTarget asks the bridge which browser t resolves to right now.
func (r *RemoteSender) ResolveTarget(t Target) (ConnInfo, error) {
	var info ConnInfo
	err := r.getRoute("/monoagent/resolve?"+targetValues(t).Encode(), &info)
	return info, err
}

// Browsers lists the browsers attached to the bridge, newest first.
func (r *RemoteSender) Browsers() ([]ConnInfo, error) {
	var body struct {
		Browsers []ConnInfo `json:"browsers"`
	}
	err := r.getRoute("/monoagent/browsers", &body)
	return body.Browsers, err
}

// SetBinding binds one browser to a monoagent profile ("" unbinds it). An
// empty label leaves the browser's label alone.
func (r *RemoteSender) SetBinding(instance, profile, label string) error {
	params := map[string]interface{}{"profile": profile}
	if label != "" {
		params["label"] = label
	}
	_, err := r.SendCommandTo(Target{Instance: instance}, &Command{Type: CmdSetBinding, Params: params}, 10*time.Second)
	return err
}

// getRoute does one authenticated GET against a routing endpoint and turns
// its error body back into the error the bridge raised.
func (r *RemoteSender) getRoute(path string, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), routeProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set(tokenHeader, r.token)
	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("bridge request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusOK:
		return json.Unmarshal(body, out)
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrRelayUnauthorized
	}
	var re routeError
	if json.Unmarshal(body, &re) != nil || re.Code == "" {
		if resp.StatusCode == http.StatusNotFound {
			return ErrBridgeTooOld // Go's mux 404: no such endpoint
		}
		return fmt.Errorf("bridge answered %s: %s", resp.Status, previewBody(body))
	}
	return re.err()
}
```

In `capture.go`'s `(*RemoteSender).CapturePage`, after the `if req.Inbox != ""` block add:

```go
	if enc := targetValues(req.Target).Encode(); enc != "" {
		url += "&" + enc
	}
```

- [ ] **Step 6: CDP clients are pinned to one browser**

In `cdp.go`, add a field to `cdpClient`:

```go
	// target is the one browser this client drives. It is resolved once, at
	// connect, so every command and every event stays in one browser even
	// though tab ids repeat across browsers. The zero Target (nothing was
	// connected yet) resolves per command, and events from any browser
	// reach it, which is the pre-routing behaviour.
	target Target
```

In `handleCdpSocket`, after the token check and before `upgrader.Upgrade`:

```go
	target := targetFromQuery(r.URL.Query())
	if c, err := s.resolve(target); err == nil {
		target = Target{Instance: c.id}
	} else if !errors.Is(err, ErrNoExtension) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
```

After `client := newCdpClient(conn)`, add `client.target = target`. In the deferred detach loop, replace `s.SendCommand(&Command{Type: CmdCdpDetach, TabID: id}, cdpCommandTimeout)` with `s.SendCommandTo(client.target, &Command{Type: CmdCdpDetach, TabID: id}, cdpCommandTimeout)`. In `relayCdpCommand`, replace `resp, err := s.SendCommand(cmd, cdpCommandTimeout)` with `resp, err := s.SendCommandTo(client.target, cmd, cdpCommandTimeout)`.

Change `fanoutCdpEvent` to take the origin, and filter:

```go
func (s *Server) fanoutCdpEvent(origin *extConn, resp *Response) {
	s.cdpMu.Lock()
	clients := make([]*cdpClient, 0, len(s.cdpClients))
	for c := range s.cdpClients {
		// A client pinned to another browser must not see this one's
		// events: tab 5 here is not its tab 5.
		if origin != nil && c.target.Instance != "" && c.target.Instance != origin.id {
			continue
		}
		clients = append(clients, c)
	}
	s.cdpMu.Unlock()
	// … rest unchanged …
```

In `dispatch`, change `s.fanoutCdpEvent(resp)` to `s.fanoutCdpEvent(c, resp)`. Add `"errors"` to `cdp.go`'s imports. Update any `fanoutCdpEvent(` call in `cdp_test.go`/`cdp_stall_test.go` to pass `nil` first (`grep -n "fanoutCdpEvent(" internal/extension/*_test.go`).

- [ ] **Step 7: Run the package tests**

Run: `export TMPDIR=/home/monoes/scratch/agent-tmp GOTMPDIR=/home/monoes/scratch/agent-tmp && go build ./... && go vet ./internal/extension/ && go test ./internal/extension/ 2>&1 | tail -5`
Expected: `ok`.

- [ ] **Step 8: Commit**

```bash
git add internal/extension/
git commit -m "feat(bridge): route relayed commands, captures and CDP by profile"
```

---

### Task 4: `ProfileBridge`: a bridge narrowed to one profile's browser

**Files:**
- Modify: `internal/browser/provider.go` (add `ProfileRouter`)
- Create: `internal/extension/profile_bridge.go`
- Test: `internal/extension/profile_bridge_test.go`

**Interfaces:**
- Consumes: `SendCommandTo`, `ResolveTarget`, `IsConnected` on both `*Server` and `*RemoteSender`; `ErrBridgeTooOld`; `parseTabID`; `CaptureRequest.Target`.
- Produces: `browser.ProfileRouter { ForProfile(profileID string) ExtensionBridge }`; `(*ServerBridge).ForProfile`, `(*RemoteBridge).ForProfile`; `*ProfileBridge` with `IsConnected`, `CreateTab`, `CloseTab`, `NewPage`, `CapturePage`, `Route() (ConnInfo, error)`, `Profile() string`, `Addr()`, `PairingURL()`.

- [ ] **Step 1: Write the failing tests**

Create `internal/extension/profile_bridge_test.go`:

```go
package extension

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestProfileBridgePinsItsBrowser(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	a := dialBrowser(t, wsURL, instA, "p-a", "")
	waitBrowsers(t, srv, 1)

	pb := (&ServerBridge{Server: srv}).ForProfile("p-a").(*ProfileBridge)
	if !pb.IsConnected() {
		t.Fatal("p-a's browser is connected")
	}
	tabc := make(chan int, 1)
	go func() {
		id, err := pb.CreateTab("https://example.com")
		if err != nil {
			t.Errorf("CreateTab: %v", err)
		}
		tabc <- id
	}()
	answerNext(t, a, 0, map[string]any{"tabId": 5})
	tab := <-tabc

	// The user rebinds A mid-run. The run's tab is still in A, so its
	// commands must keep going there.
	_ = a.WriteJSON(map[string]string{"kind": KindBinding, "profile": "p-other"})
	time.Sleep(50 * time.Millisecond)

	page := pb.NewPage(tab)
	errc := make(chan error, 1)
	go func() { errc <- page.Navigate("https://example.com/next") }()
	cmd := answerNext(t, a, 0, map[string]any{})
	if cmd == nil || cmd.Type != CmdNavigate || cmd.TabID != 5 {
		t.Fatalf("A got %+v, want navigate on tab 5", cmd)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func TestProfileBridgeSaysWhyItIsNotConnected(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	dialBrowser(t, wsURL, instA, "p-a", "")
	waitBrowsers(t, srv, 1)
	pb := (&ServerBridge{Server: srv}).ForProfile("p-b").(*ProfileBridge)
	if pb.IsConnected() {
		t.Fatal("no browser may run p-b")
	}
	_, err := pb.Route()
	var nb *NoBrowserError
	if !errors.As(err, &nb) || nb.Profile != "p-b" {
		t.Fatalf("Route err = %v", err)
	}
	if _, err := pb.CreateTab("https://example.com"); !errors.As(err, &nb) {
		t.Fatalf("CreateTab err = %v, want the same explanation", err)
	}
}

// oldBridge is a relay from before routing: /monoagent/resolve is a 404,
// the relay ignores target parameters.
type oldBridge struct {
	mu      sync.Mutex
	queries []string
}

func (o *oldBridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/monoagent/health":
		_ = json.NewEncoder(w).Encode(Status{Service: ServiceName, Status: StatusConnected, Connected: true})
	case "/monoagent/relay":
		o.mu.Lock()
		o.queries = append(o.queries, r.URL.RawQuery)
		o.mu.Unlock()
		var cmd Command
		_ = json.NewDecoder(r.Body).Decode(&cmd)
		_ = json.NewEncoder(w).Encode(Response{ID: cmd.ID, Success: true, Data: map[string]any{"tabId": 3}})
	default:
		http.NotFound(w, r)
	}
}

func TestProfileBridgeFallsBackOnAnOldBridge(t *testing.T) {
	old := &oldBridge{}
	ts := httptest.NewServer(old)
	t.Cleanup(ts.Close)
	pb := (&RemoteBridge{Sender: &RemoteSender{baseURL: ts.URL, client: &http.Client{}}}).ForProfile("p-a").(*ProfileBridge)
	if !pb.IsConnected() {
		t.Fatal("an old bridge with its one browser attached counts as connected")
	}
	id, err := pb.CreateTab("https://example.com")
	if err != nil || id != 3 {
		t.Fatalf("CreateTab = %d, %v", id, err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `export TMPDIR=/home/monoes/scratch/agent-tmp GOTMPDIR=/home/monoes/scratch/agent-tmp && go test ./internal/extension/ -run TestProfileBridge 2>&1 | head -5`
Expected: build failure: `ForProfile undefined`.

- [ ] **Step 3: Add `ProfileRouter` to `internal/browser/provider.go`**

After the `ExtensionBridge` interface:

```go
// ProfileRouter is an ExtensionBridge that can reach several browsers, one
// per monoagent profile. ForProfile returns a bridge that only ever drives
// the browser bound to profileID (or the default browser when none is), so
// a run for one profile never opens tabs in another profile's accounts.
type ProfileRouter interface {
	ForProfile(profileID string) ExtensionBridge
}
```

- [ ] **Step 4: Create `internal/extension/profile_bridge.go`**

```go
package extension

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/monoes/mono-agent/internal/browser"
	"github.com/monoes/mono-agent/internal/capture"
)

// targetSender is what both the bridge-owning *Server and the relaying
// *RemoteSender offer: commands to a chosen browser, and the question of
// which browser that is.
type targetSender interface {
	commandSender
	SendCommandTo(t Target, cmd *Command, timeout time.Duration) (*Response, error)
	ResolveTarget(t Target) (ConnInfo, error)
	IsConnected() bool
}

var (
	_ targetSender          = (*Server)(nil)
	_ targetSender          = (*RemoteSender)(nil)
	_ browser.ProfileRouter = (*ServerBridge)(nil)
	_ browser.ProfileRouter = (*RemoteBridge)(nil)
	_ browser.ExtensionBridge = (*ProfileBridge)(nil)
	_ Capturer              = (*ProfileBridge)(nil)
)

// ForProfile narrows this bridge to the browser bound to profileID.
func (b *ServerBridge) ForProfile(profileID string) browser.ExtensionBridge {
	return &ProfileBridge{via: b.Server, parent: b, profile: profileID}
}

// ForProfile narrows this bridge to the browser bound to profileID.
func (b *RemoteBridge) ForProfile(profileID string) browser.ExtensionBridge {
	return &ProfileBridge{via: b.Sender, parent: b, profile: profileID}
}

// ProfileBridge drives the browser one monoagent profile is bound to.
//
// The first tab it opens pins it to that browser's instance. Everything
// after goes there, even if the user rebinds the browser mid-run, because
// the tabs this bridge holds live in that browser and nowhere else. Create
// one per run (per GetPage), not one per process.
type ProfileBridge struct {
	via     targetSender
	parent  browser.ExtensionBridge
	profile string

	mu       sync.Mutex
	instance string // pinned browser, once a tab exists
	legacy   bool   // the bridge predates routing: send untargeted
}

// Profile is the monoagent profile this bridge runs as.
func (b *ProfileBridge) Profile() string { return b.profile }

// target is where the next command goes.
func (b *ProfileBridge) target() Target {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch {
	case b.legacy:
		return Target{}
	case b.instance != "":
		return Target{Instance: b.instance}
	default:
		return Target{Profile: b.profile}
	}
}

// Route reports the browser this bridge would use now, or why there is
// none (a *NoBrowserError names the fix).
func (b *ProfileBridge) Route() (ConnInfo, error) {
	info, err := b.via.ResolveTarget(b.target())
	if errors.Is(err, ErrBridgeTooOld) {
		b.mu.Lock()
		b.legacy = true
		b.mu.Unlock()
		if b.via.IsConnected() {
			return ConnInfo{Instance: legacyInstanceID, Legacy: true}, nil
		}
		return ConnInfo{}, ErrNoExtension
	}
	return info, err
}

// IsConnected reports whether a browser may run this profile right now.
func (b *ProfileBridge) IsConnected() bool {
	_, err := b.Route()
	return err == nil
}

// pin resolves the browser once and keeps it.
func (b *ProfileBridge) pin() (Target, error) {
	info, err := b.Route()
	if err != nil {
		return Target{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.legacy {
		return Target{}, nil
	}
	if b.instance == "" {
		b.instance = info.Instance
	}
	return Target{Instance: b.instance}, nil
}

func (b *ProfileBridge) CreateTab(url string) (int, error) {
	t, err := b.pin()
	if err != nil {
		return 0, err
	}
	resp, err := b.via.SendCommandTo(t, &Command{
		Type:   CmdCreateTab,
		Params: map[string]interface{}{"url": url},
	}, 30*time.Second)
	if err != nil {
		return 0, err
	}
	return parseTabID(resp)
}

func (b *ProfileBridge) CloseTab(tabID int) error {
	_, err := b.via.SendCommandTo(b.target(), &Command{Type: CmdCloseTab, TabID: tabID}, 30*time.Second)
	return err
}

func (b *ProfileBridge) NewPage(tabID int) browser.PageInterface {
	return NewExtensionPage(pinnedSender{via: b.via, t: b.target()}, tabID)
}

// CapturePage captures in this profile's browser.
func (b *ProfileBridge) CapturePage(req CaptureRequest) (*capture.Result, error) {
	c, ok := b.parent.(Capturer)
	if !ok {
		return nil, fmt.Errorf("this extension bridge cannot capture pages (%T)", b.parent)
	}
	t, err := b.pin()
	if err != nil {
		return nil, err
	}
	req.Target = t
	return c.CapturePage(req)
}

// Addr and PairingURL pass through, so the CLI's connect helpers (port
// hints, auto-pairing) work the same through a narrowed bridge.
func (b *ProfileBridge) Addr() (string, bool) {
	if p, ok := b.parent.(interface{ Addr() (string, bool) }); ok {
		return p.Addr()
	}
	return "", false
}

func (b *ProfileBridge) PairingURL() (string, bool) {
	if p, ok := b.parent.(interface{ PairingURL() (string, bool) }); ok {
		return p.PairingURL()
	}
	return "", false
}

// pinnedSender sends an ExtensionPage's commands to one browser.
type pinnedSender struct {
	via targetSender
	t   Target
}

func (p pinnedSender) SendCommand(cmd *Command, timeout time.Duration) (*Response, error) {
	return p.via.SendCommandTo(p.t, cmd, timeout)
}
```

- [ ] **Step 5: Run the tests**

Run: `export TMPDIR=/home/monoes/scratch/agent-tmp GOTMPDIR=/home/monoes/scratch/agent-tmp && go build ./... && go test ./internal/extension/ ./internal/browser/ 2>&1 | tail -5`
Expected: both `ok`.

- [ ] **Step 6: Commit**

```bash
git add internal/browser/provider.go internal/extension/profile_bridge.go internal/extension/profile_bridge_test.go
git commit -m "feat(bridge): ProfileBridge narrows a bridge to one profile's browser"
```

---

### Task 5: The CLI and the engine route by profile

**Files:**
- Create: `cmd/monoagentcli/browser_profile.go`
- Modify: `cmd/monoagentcli/workflow.go` (`buildEngine`, `lazyBrowserSessionProvider`)
- Modify: `cmd/monoagentcli/chrome_helper.go` (`ensureExtensionConnected` error hints)
- Modify call sites: `node.go:597`, `capture.go:90`, `login.go:225`, `login.go:299`, `login_pkg.go:98`, `login_pkg.go:139`, `application_apply.go:27,125`, `automation_rerecord.go:43,123`, `record_analyze.go:44,179`
- Modify: `cmd/monoagentcli/application_apply_test.go:50`
- Test: `cmd/monoagentcli/browser_profile_test.go`

**Interfaces:**
- Consumes: `browser.ProfileRouter`, `extension.NoBrowserError`, `(*extension.ProfileBridge).Route`, `vault.ProfileIDFromContext`, `vault.ContextWithProfileID`.
- Produces: `bridgeForProfile(bridge browserpkg.ExtensionBridge, profileID string) browserpkg.ExtensionBridge`; `setupProfileBridge(profileID string, logger zerolog.Logger, wait time.Duration) browserpkg.ExtensionBridge`; `browserProfile(cfg *globalConfig) string`; `routeHint(bridge connChecker) string`; `applicationApplyBridgeFunc func(profileID string) (browserpkg.ExtensionBridge, error)`.

- [ ] **Step 1: Write the failing tests**

Create `cmd/monoagentcli/browser_profile_test.go`:

```go
package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	browserpkg "github.com/monoes/mono-agent/internal/browser"
	"github.com/monoes/mono-agent/internal/extension"
	"github.com/monoes/mono-agent/internal/vault"
)

// fakeRouter records which profile each narrowed bridge was made for.
type fakeRouter struct {
	mu    sync.Mutex
	asked []string
}

func (f *fakeRouter) IsConnected() bool                          { return true }
func (f *fakeRouter) CreateTab(string) (int, error)              { return 1, nil }
func (f *fakeRouter) CloseTab(int) error                         { return nil }
func (f *fakeRouter) NewPage(int) browserpkg.PageInterface       { return nil }
func (f *fakeRouter) ForProfile(p string) browserpkg.ExtensionBridge {
	f.mu.Lock()
	f.asked = append(f.asked, p)
	f.mu.Unlock()
	return &fakeNarrowed{profile: p}
}

type fakeNarrowed struct{ profile string }

func (f *fakeNarrowed) IsConnected() bool                    { return true }
func (f *fakeNarrowed) CreateTab(string) (int, error)        { return 1, nil }
func (f *fakeNarrowed) CloseTab(int) error                   { return nil }
func (f *fakeNarrowed) NewPage(int) browserpkg.PageInterface { return nil }

// plainBridge is a bridge that cannot route (e.g. a test double).
type plainBridge struct{ fakeNarrowed }

func TestBridgeForProfile(t *testing.T) {
	r := &fakeRouter{}
	if got := bridgeForProfile(r, "p-work"); got.(*fakeNarrowed).profile != "p-work" {
		t.Fatalf("router not narrowed: %+v", got)
	}
	if got := bridgeForProfile(r, ""); got != browserpkg.ExtensionBridge(r) {
		t.Fatal("an empty profile must leave the bridge alone")
	}
	p := &plainBridge{}
	if got := bridgeForProfile(p, "p-work"); got != browserpkg.ExtensionBridge(p) {
		t.Fatal("a bridge that cannot route is returned as is")
	}
}

func TestLazyProviderUsesTheRunsProfile(t *testing.T) {
	r := &fakeRouter{}
	p := &lazyBrowserSessionProvider{
		logger:         zerolog.Nop(),
		defaultProfile: "p-default",
		setup:          func() browserpkg.ExtensionBridge { return r },
		ensure:         func(connChecker, time.Duration) error { return nil },
	}
	if _, err := p.GetPage(vault.ContextWithProfileID(context.Background(), "p-run"), "instagram", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := p.GetPage(context.Background(), "instagram", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Join(r.asked, ",") != "p-run,p-default" {
		t.Fatalf("asked = %v", r.asked)
	}
}

func TestLazyProviderDoesNotMakeOneProfileWaitForAnother(t *testing.T) {
	r := &fakeRouter{}
	release := make(chan struct{})
	p := &lazyBrowserSessionProvider{
		logger: zerolog.Nop(),
		setup:  func() browserpkg.ExtensionBridge { return r },
		ensure: func(b connChecker, _ time.Duration) error {
			if b.(*fakeNarrowed).profile == "p-slow" {
				<-release // this profile's browser is still starting
			}
			return nil
		},
	}
	go func() { _, _ = p.GetPage(vault.ContextWithProfileID(context.Background(), "p-slow"), "x", "") }()
	time.Sleep(20 * time.Millisecond)

	start := time.Now()
	if _, err := p.GetPage(vault.ContextWithProfileID(context.Background(), "p-fast"), "x", ""); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("p-fast waited %s behind p-slow", d)
	}
	close(release)
}

type routedStub struct {
	fakeNarrowed
	err error
}

func (r *routedStub) Route() (extension.ConnInfo, error) { return extension.ConnInfo{}, r.err }

func TestRouteHintNamesTheFix(t *testing.T) {
	stub := &routedStub{err: &extension.NoBrowserError{Profile: "p-x", BoundTo: []string{"p-a"}}}
	if h := routeHint(stub); !strings.Contains(h, `"p-x"`) || !strings.Contains(h, "extension bind") {
		t.Fatalf("hint = %q", h)
	}
	if h := routeHint(&plainBridge{}); h != "" {
		t.Fatalf("no hint for a plain bridge, got %q", h)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `export TMPDIR=/home/monoes/scratch/agent-tmp GOTMPDIR=/home/monoes/scratch/agent-tmp && go test ./cmd/monoagentcli/ -run 'TestBridgeForProfile|TestLazyProvider|TestRouteHint' 2>&1 | head -5`
Expected: build failure: `undefined: bridgeForProfile`, `unknown field defaultProfile`.

- [ ] **Step 3: Create `cmd/monoagentcli/browser_profile.go`**

```go
package main

import (
	"errors"
	"time"

	"github.com/rs/zerolog"

	browserpkg "github.com/monoes/mono-agent/internal/browser"
	"github.com/monoes/mono-agent/internal/extension"
)

// Per-profile browsers: each monoagent profile's browser actions run in the
// browser profile bound to it (see internal/extension/conns.go for the
// routing rule). Everything here is the CLI's half: choosing the profile a
// command runs as and narrowing the bridge to its browser.

// bridgeForProfile narrows bridge to the browser bound to profileID. A
// bridge that cannot route (a test double), or no profile at all, is
// returned unchanged, which reaches the default browser.
func bridgeForProfile(bridge browserpkg.ExtensionBridge, profileID string) browserpkg.ExtensionBridge {
	if r, ok := bridge.(browserpkg.ProfileRouter); ok && profileID != "" {
		return r.ForProfile(profileID)
	}
	return bridge
}

// setupProfileBridge is setupExtensionBridge narrowed to profileID's browser.
func setupProfileBridge(profileID string, logger zerolog.Logger, wait time.Duration) browserpkg.ExtensionBridge {
	return bridgeForProfile(setupExtensionBridge(logger, wait), profileID)
}

// browserProfile resolves the profile this invocation runs as, i.e.
// --profile (an id or a name) or the active one, to its id. initDB does the
// resolving; a command that never opened the database would otherwise route
// by a raw name, or by nothing.
func browserProfile(cfg *globalConfig) string {
	db, err := initDB(cfg)
	if err != nil {
		return cfg.ProfileID
	}
	db.Close()
	return cfg.ProfileID
}

// routeHint explains, for a bridge narrowed to a profile, why no browser
// may run it. It is appended to ensureExtensionConnected's timeout errors,
// which otherwise only say "did not connect".
func routeHint(bridge connChecker) string {
	r, ok := bridge.(interface {
		Route() (extension.ConnInfo, error)
	})
	if !ok {
		return ""
	}
	_, err := r.Route()
	var nb *extension.NoBrowserError
	if errors.As(err, &nb) {
		return "\n" + nb.Error()
	}
	return ""
}
```

- [ ] **Step 4: Engine: route each run by its own profile**

In `workflow.go`, set the provider in `buildEngine`:

```go
	sessionProvider := &lazyBrowserSessionProvider{logger: extLogger, defaultProfile: cfg.ProfileID}
```

Replace the `lazyBrowserSessionProvider` type and its `GetPage`:

```go
// lazyBrowserSessionProvider implements nodes.SessionProvider by connecting
// the Chrome extension bridge on first use instead of at engine build time.
// Browser nodes call GetPage when they execute, so the connect sequence
// runs only for workflows that actually contain a browser node.
//
// Each GetPage is narrowed to the browser bound to the run's profile: the
// execution's profile id rides ctx (WorkflowEngine.runExecution), so a
// daemon running many profiles' workflows sends each one to its own
// browser. Only building the shared bridge is serialised; waiting for a
// profile's browser is not, so one profile's slow browser never holds up
// another's run.
type lazyBrowserSessionProvider struct {
	logger         zerolog.Logger
	defaultProfile string // the engine's profile, for a ctx that carries none

	// setup and ensure default to setupExtensionBridge (3s wait) and
	// ensureExtensionConnected; tests replace them.
	setup  func() browserpkg.ExtensionBridge
	ensure func(connChecker, time.Duration) error

	mu     sync.Mutex
	bridge browserpkg.ExtensionBridge
}

func (p *lazyBrowserSessionProvider) base() browserpkg.ExtensionBridge {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.bridge == nil {
		if p.setup != nil {
			p.bridge = p.setup()
		} else {
			p.bridge = setupExtensionBridge(p.logger, 3*time.Second)
		}
	}
	return p.bridge
}

func (p *lazyBrowserSessionProvider) GetPage(ctx context.Context, platform, username string) (browserpkg.PageInterface, error) {
	profileID := vault.ProfileIDFromContext(ctx)
	if profileID == "" {
		profileID = p.defaultProfile
	}
	bridge := bridgeForProfile(p.base(), profileID)
	ensure := p.ensure
	if ensure == nil {
		ensure = ensureExtensionConnected
	}
	// No throwaway automation browser. When this profile's browser is not
	// attached, launch the user's real Chrome (same mechanism as `login`)
	// and wait for it.
	if err := ensure(bridge, 30*time.Second); err != nil {
		return nil, fmt.Errorf("browser session: %w", err)
	}
	return (&browserpkg.HybridSessionProvider{ExtBridge: bridge, Logger: p.logger}).GetPage(ctx, platform, username)
}
```

Add `"github.com/monoes/mono-agent/internal/vault"` to `workflow.go`'s imports.

- [ ] **Step 5: Explain routing failures in `ensureExtensionConnected`**

In `chrome_helper.go`, both "did not connect within %s" errors end with `%s%s", timeout, fallbackPortHint(bridge), bridgeLifetimeHint(bridge))`. Change both to `%s%s%s", timeout, fallbackPortHint(bridge), bridgeLifetimeHint(bridge), routeHint(bridge))`.

- [ ] **Step 6: Narrow every direct CLI browser entry point**

Each of these commands runs as one profile:

| File:line | Replace | With |
|---|---|---|
| `node.go:597` | `setupExtensionBridge(extLogger, 3*time.Second)` | `setupProfileBridge(cfg.ProfileID, extLogger, 3*time.Second)` |
| `capture.go:90` | `setupExtensionBridge(newExtensionBridgeLogger(), 3*time.Second)` | `setupProfileBridge(browserProfile(cfg), newExtensionBridgeLogger(), 3*time.Second)` |
| `login.go:225` | same | `setupProfileBridge(cfg.ProfileID, newExtensionBridgeLogger(), 3*time.Second)` (initDB ran just above) |
| `login.go:299` | same | `setupProfileBridge(cfg.ProfileID, newExtensionBridgeLogger(), 3*time.Second)` |
| `login_pkg.go:98` | same | `setupProfileBridge(browserProfile(cfg), newExtensionBridgeLogger(), 3*time.Second)` |
| `login_pkg.go:139` | same | `setupProfileBridge(cfg.ProfileID, newExtensionBridgeLogger(), 3*time.Second)` (db is open) |
| `automation_rerecord.go:43` | `setupExtensionBridge(logger, 3*time.Second)` | `setupProfileBridge(vault.ProfileIDFromContext(ctx), logger, 3*time.Second)` |
| `record_analyze.go:44` | same | `setupProfileBridge(vault.ProfileIDFromContext(ctx), logger, 3*time.Second)` |

Pass the profile in at the two ctx-based seams' callers:
- `automation_rerecord.go:123`: `rerecordSelector(cmd.Context(), …)` becomes `rerecordSelector(vault.ContextWithProfileID(cmd.Context(), browserProfile(cfg)), …)`.
- `record_analyze.go:179`: `recordVerifyExec(cmd.Context(), …)` becomes `recordVerifyExec(vault.ContextWithProfileID(cmd.Context(), browserProfile(cfg)), …)`.

The application seam takes the profile explicitly:

```go
var applicationApplyBridgeFunc = func(profileID string) (browserpkg.ExtensionBridge, error) {
	bridge := setupProfileBridge(profileID, newExtensionBridgeLogger(), 3*time.Second)
	if err := ensureExtensionConnected(bridge, 30*time.Second); err != nil {
		return nil, err
	}
	return bridge, nil
}
```

Its caller (`application_apply.go:125`) becomes `bridge, err := applicationApplyBridgeFunc(cfg.ProfileID)` (db is open there). In `application_apply_test.go:50`, change the stub to `func(string) (browserpkg.ExtensionBridge, error) { return nil, nil }`.

Add the `internal/vault` import to `automation_rerecord.go` and `record_analyze.go` where the compiler asks for it.

Then confirm nothing was missed. `setupExtensionBridge(` should now appear only in `node.go` (its definition), `browser_profile.go` and `workflow.go`:

Run: `grep -n "setupExtensionBridge(" cmd/monoagentcli/*.go | grep -v _test`
Expected: exactly those three files.

- [ ] **Step 7: Build, vet, test**

Run: `export TMPDIR=/home/monoes/scratch/agent-tmp GOTMPDIR=/home/monoes/scratch/agent-tmp && go build ./... && go vet ./cmd/monoagentcli/ && go test ./cmd/monoagentcli/ 2>&1 | tail -5`
Expected: `ok  github.com/monoes/mono-agent/cmd/monoagentcli`.

- [ ] **Step 8: Commit**

```bash
git add cmd/monoagentcli/
git commit -m "feat(cli): run each profile's browser actions in its bound browser"
```

---

### Task 6: `extension browsers`, `extension bind`, `extension unbind`

**Files:**
- Create: `cmd/monoagentcli/extension_browsers.go`
- Modify: `cmd/monoagentcli/extension.go` (register commands)
- Modify: `cmd/monoagentcli/extension_serve.go` (`runExtensionStatus` browser line)
- Test: `cmd/monoagentcli/extension_browsers_test.go`

**Interfaces:**
- Consumes: `findRunningBridge()`, `extension.NewRemoteSender(base).Browsers()/SetBinding()`, `profiledir.List`, `resolveProfileID`, `initDB`, `errInvalidInput`, `errNotFound`.
- Produces: CLI JSON `{"running":bool,"hint":string,"browsers":[browserRow]}` with `browserRow{instance,label,profile_id,profile_name,legacy,conflict,version,connected_at}`. `bind`/`unbind` print one `browserRow`. The GUI (Task 8) depends on these exact keys.

- [ ] **Step 1: Write the failing tests**

Create `cmd/monoagentcli/extension_browsers_test.go`:

```go
package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/extension"
)

var testInfos = []extension.ConnInfo{
	{Instance: "3f2a91c0-0000-4000-8000-000000000001", Label: "Edge Work", Profile: "p-work", Version: "1.5.0", ConnectedAt: time.Date(2026, 9, 27, 14, 2, 0, 0, time.UTC)},
	{Instance: "3f2a0000-0000-4000-8000-000000000002", Label: "Chrome Home", Version: "1.5.0"},
	{Instance: "legacy", Legacy: true},
}

func TestMatchBrowser(t *testing.T) {
	cases := map[string]string{
		"3f2a91c0-0000-4000-8000-000000000001": "3f2a91c0-0000-4000-8000-000000000001",
		"3f2a91":                               "3f2a91c0-0000-4000-8000-000000000001",
		"chrome home":                          "3f2a0000-0000-4000-8000-000000000002",
	}
	for q, want := range cases {
		got, err := matchBrowser(testInfos, q)
		if err != nil || got.Instance != want {
			t.Errorf("matchBrowser(%q) = %q, %v; want %q", q, got.Instance, err, want)
		}
	}
	if _, err := matchBrowser(testInfos, "3f2a"); err == nil || !strings.Contains(err.Error(), "matches 2 browsers") {
		t.Errorf("ambiguous prefix: err = %v", err)
	}
	if _, err := matchBrowser(testInfos, "nope"); err == nil {
		t.Error("unknown browser must fail")
	}
	if _, err := matchBrowser(testInfos, "3f"); err == nil {
		t.Error("a prefix under 4 characters must not match")
	}
}

func TestBrowserRowsAndTable(t *testing.T) {
	rows := browserRows(testInfos, map[string]string{"p-work": "Work"})
	if rows[0].ProfileName != "Work" || rows[1].ProfileID != "" || !rows[2].Legacy {
		t.Fatalf("rows = %+v", rows)
	}
	var out bytes.Buffer
	printBrowserTable(&out, rows)
	s := out.String()
	for _, want := range []string{"Edge Work", "Work", "any profile", "3f2a91c0", "too old to bind"} {
		if !strings.Contains(s, want) {
			t.Errorf("table missing %q:\n%s", want, s)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `export TMPDIR=/home/monoes/scratch/agent-tmp GOTMPDIR=/home/monoes/scratch/agent-tmp && go test ./cmd/monoagentcli/ -run 'TestMatchBrowser|TestBrowserRows' 2>&1 | head -5`
Expected: `undefined: matchBrowser`.

- [ ] **Step 3: Create `cmd/monoagentcli/extension_browsers.go`**

```go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/extension"
	"github.com/monoes/mono-agent/internal/profiledir"
)

// Which browser each profile runs in. A browser is one browser profile with
// the MonoAgent Bridge extension; the binding is stored in that extension,
// and these commands read it through the bridge or ask the extension to
// change it.

// browserRow is one connected browser as `extension browsers --json`
// prints it. The desktop app reads these keys.
type browserRow struct {
	Instance    string `json:"instance"`
	Label       string `json:"label"`
	ProfileID   string `json:"profile_id"`
	ProfileName string `json:"profile_name"`
	Legacy      bool   `json:"legacy"`
	Conflict    bool   `json:"conflict"`
	Version     string `json:"version"`
	ConnectedAt string `json:"connected_at"`
}

type browsersReport struct {
	Running  bool         `json:"running"`
	Hint     string       `json:"hint,omitempty"`
	Browsers []browserRow `json:"browsers"`
}

const noBridgeHint = "No bridge is running. Start one with `monoagentcli extension serve`."

func newExtensionBrowsersCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "browsers",
		Short: "List the browsers attached to the bridge and the profile each one runs",
		Long: "Every browser profile with the MonoAgent Bridge extension is its own browser here.\n" +
			"A browser bound to a profile runs that profile's browser actions, logged in as that\n" +
			"browser's accounts. An unbound browser is the default for profiles with no browser\n" +
			"of their own. Bind one with `extension bind` or in the extension's side panel.",
		Example: "  monoagentcli extension browsers\n  monoagentcli extension browsers --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runExtensionBrowsers(cmd.Context(), cfg, cmd.OutOrStdout())
		},
	}
}

func newExtensionBindCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "bind <browser> <profile>",
		Short: "Run a profile's browser actions in one browser",
		Long: "<browser> is an instance id (or its first 4+ characters) or a label from\n" +
			"`extension browsers`; <profile> is a profile id or name. The browser keeps the\n" +
			"binding across restarts; it is stored in that browser's extension.",
		Example: "  monoagentcli extension bind \"Edge Work\" Work\n  monoagentcli extension bind 3f2a Personal",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			profileID, err := resolveProfileID(db.DB, args[1])
			db.Close()
			if err != nil {
				return err
			}
			return runExtensionBind(cmd.Context(), cfg, cmd.OutOrStdout(), args[0], profileID)
		},
	}
}

func newExtensionUnbindCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:     "unbind <browser>",
		Short:   "Make a browser a default browser again (for profiles with none of their own)",
		Example: "  monoagentcli extension unbind \"Edge Work\"",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExtensionBind(cmd.Context(), cfg, cmd.OutOrStdout(), args[0], "")
		},
	}
}

func runExtensionBrowsers(ctx context.Context, cfg *globalConfig, out io.Writer) error {
	report := browsersReport{Browsers: []browserRow{}}
	_, base, ok := findRunningBridge()
	if !ok {
		report.Hint = noBridgeHint
		return printBrowsers(out, cfg.JSONOutput, report)
	}
	report.Running = true
	infos, err := extension.NewRemoteSender(base).Browsers()
	switch {
	case errors.Is(err, extension.ErrBridgeTooOld):
		report.Hint = err.Error()
	case err != nil:
		return errAuthConnection("listing browsers: %v", err)
	default:
		report.Browsers = browserRows(infos, profileNames(ctx, cfg))
	}
	return printBrowsers(out, cfg.JSONOutput, report)
}

func runExtensionBind(ctx context.Context, cfg *globalConfig, out io.Writer, query, profileID string) error {
	_, base, ok := findRunningBridge()
	if !ok {
		return errNotFound("%s", noBridgeHint)
	}
	sender := extension.NewRemoteSender(base)
	infos, err := sender.Browsers()
	if err != nil {
		return errAuthConnection("listing browsers: %v", err)
	}
	target, err := matchBrowser(infos, query)
	if err != nil {
		return err
	}
	if target.Legacy {
		return errInvalidInput("that browser's extension is too old to bind — reload it from the browser's extensions page (it needs version 1.5.0 or later)")
	}
	if err := sender.SetBinding(target.Instance, profileID, ""); err != nil {
		return fmt.Errorf("binding %s: %w (if the extension is older than 1.5.0, reload it)", target.Instance, err)
	}
	infos, err = sender.Browsers()
	if err != nil {
		return errAuthConnection("listing browsers: %v", err)
	}
	for _, row := range browserRows(infos, profileNames(ctx, cfg)) {
		if row.Instance == target.Instance {
			if cfg.JSONOutput {
				return json.NewEncoder(out).Encode(row)
			}
			printBrowserTable(out, []browserRow{row})
			return nil
		}
	}
	return errNotFound("the browser disconnected right after binding — check `monoagentcli extension browsers`")
}

// matchBrowser finds one browser by instance id, id prefix (4+ chars) or
// label (case-insensitive).
func matchBrowser(infos []extension.ConnInfo, query string) (extension.ConnInfo, error) {
	q := strings.TrimSpace(query)
	var hits []extension.ConnInfo
	for _, b := range infos {
		if b.Instance == q {
			return b, nil
		}
	}
	for _, b := range infos {
		if (len(q) >= 4 && strings.HasPrefix(b.Instance, q)) || strings.EqualFold(b.Label, q) {
			hits = append(hits, b)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return extension.ConnInfo{}, errNotFound("no connected browser matches %q — see `monoagentcli extension browsers`", q)
	default:
		return extension.ConnInfo{}, errInvalidInput("%q matches %d browsers — use more of the id", q, len(hits))
	}
}

func browserRows(infos []extension.ConnInfo, names map[string]string) []browserRow {
	rows := make([]browserRow, 0, len(infos))
	for _, b := range infos {
		row := browserRow{
			Instance: b.Instance, Label: b.Label, ProfileID: b.Profile, ProfileName: names[b.Profile],
			Legacy: b.Legacy, Conflict: b.Conflict, Version: b.Version,
		}
		if !b.ConnectedAt.IsZero() {
			row.ConnectedAt = b.ConnectedAt.Format(time.RFC3339)
		}
		rows = append(rows, row)
	}
	return rows
}

// profileNames maps profile ids to names for display; it is best effort.
func profileNames(ctx context.Context, cfg *globalConfig) map[string]string {
	names := map[string]string{}
	db, err := initDB(cfg)
	if err != nil {
		return names
	}
	defer db.Close()
	list, _ := profiledir.List(ctx, db.DB)
	for _, p := range list {
		names[p.ID] = p.Name
	}
	return names
}

func printBrowsers(out io.Writer, asJSON bool, report browsersReport) error {
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	if report.Hint != "" {
		fmt.Fprintln(out, report.Hint)
	}
	if report.Running && len(report.Browsers) == 0 && report.Hint == "" {
		fmt.Fprintln(out, "No browser is attached. Open a browser that has the MonoAgent Bridge extension.")
		return nil
	}
	if len(report.Browsers) > 0 {
		printBrowserTable(out, report.Browsers)
	}
	return nil
}

func printBrowserTable(out io.Writer, rows []browserRow) {
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "BROWSER\tID\tPROFILE\tNOTE")
	for _, r := range rows {
		label := r.Label
		if label == "" {
			label = "(unnamed)"
		}
		id := r.Instance
		if len(id) > 8 {
			id = id[:8]
		}
		profile := "any profile"
		if r.ProfileID != "" {
			profile = r.ProfileName
			if profile == "" {
				profile = r.ProfileID + " (unknown profile)"
			}
		}
		note := ""
		switch {
		case r.Legacy:
			note = "extension too old to bind — reload it"
		case r.Conflict:
			note = "another browser is bound to this profile; the newer one is used"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", label, id, profile, note)
	}
	_ = tw.Flush()
}
```

In `extension.go`, register the commands:

```go
	cmd.AddCommand(
		newExtensionServeCmd(),
		newExtensionStatusCmd(cfg),
		newExtensionPairCmd(),
		newExtensionResetCmd(),
		newExtensionBrowsersCmd(cfg),
		newExtensionBindCmd(cfg),
		newExtensionUnbindCmd(cfg),
	)
```

In `extension_serve.go`'s `runExtensionStatus`, after the `fmt.Fprintf(out, "  Extension: %s\n", …)` line:

```go
	if pairing != extension.PairingMismatch && st.Browsers > 1 {
		fmt.Fprintf(out, "  Browsers: %d attached — see `monoagentcli extension browsers`\n", st.Browsers)
	}
```

- [ ] **Step 4: Run tests and check the help output**

Run: `export TMPDIR=/home/monoes/scratch/agent-tmp GOTMPDIR=/home/monoes/scratch/agent-tmp && go test ./cmd/monoagentcli/ -run 'TestMatchBrowser|TestBrowserRows' && go build -o ~/scratch/per-profile/monoagentcli ./cmd/monoagentcli && HOME=~/scratch/per-profile/home ~/scratch/per-profile/monoagentcli extension bind --help | head -3`
Expected: `ok`, then the `bind` usage line.

- [ ] **Step 5: Commit**

```bash
git add cmd/monoagentcli/extension_browsers.go cmd/monoagentcli/extension_browsers_test.go cmd/monoagentcli/extension.go cmd/monoagentcli/extension_serve.go
git commit -m "feat(cli): extension browsers/bind/unbind"
```

---

### Task 7: The extension reports and changes its binding

**Files:**
- Create: `chrome-extension/browser_binding.js`
- Create: `chrome-extension/browser_binding.test.mjs`
- Create: `chrome-extension/sidepanel_binding.js`
- Modify: `chrome-extension/background.js` (importScripts, state, auth frame, `set_binding`, storage listener, status)
- Modify: `chrome-extension/sidepanel.html` (field + script tags)
- Modify: `chrome-extension/sidepanel.js` (install + redraw)
- Modify: `chrome-extension/manifest.json` (`"version": "1.5.0"`)

**Interfaces:**
- Consumes: auth frame fields (Task 1), `set_binding` params `{profile, label?}` (Task 2), `kind:"binding"` frame (Task 2).
- Produces: `globalThis.MonoBrowserBinding` = `{ INSTANCE_KEY, PROFILE_KEY, LABEL_KEY, MAX_LABEL, isValidProfileId, browserName, cleanLabel, defaultLabel, load, authFields, setBinding, bindingFrame, isBindingChange, options }`; `globalThis.MonoPanelBinding.install(opts) → { refresh, redraw }`.

- [ ] **Step 1: Write the failing tests**

Create `chrome-extension/browser_binding.test.mjs`:

```js
// Tests for which monoagent profile this browser runs automations for.
// `node --test 'chrome-extension/*.test.mjs'`.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

const { MonoBrowserBinding: B } = loadExtensionScripts(["browser_binding.js"]);

function fakeStorage(seed = {}) {
  const data = Object.assign({}, seed);
  return {
    data,
    get: async (keys) => {
      const out = {};
      for (const k of Array.isArray(keys) ? keys : [keys]) if (k in data) out[k] = data[k];
      return out;
    },
    set: async (values) => Object.assign(data, values),
  };
}

let n = 0;
const fakeCrypto = { randomUUID: () => `0000000${++n}-0000-4000-8000-000000000000` };
const EDGE_UA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/140.0 Safari/537.36 Edg/140.0";

test("the worker creates an instance id once and keeps it", async () => {
  const storage = fakeStorage();
  const first = await B.load(storage, { crypto: fakeCrypto, userAgent: EDGE_UA, create: true });
  const again = await B.load(storage, { crypto: fakeCrypto, userAgent: EDGE_UA, create: true });
  assert.match(first.instance, /^[A-Za-z0-9-]{8,64}$/);
  assert.equal(again.instance, first.instance);
  assert.equal(storage.data[B.INSTANCE_KEY], first.instance);
});

test("the side panel never mints an id (only the worker does)", async () => {
  const storage = fakeStorage();
  const state = await B.load(storage, { crypto: fakeCrypto, userAgent: EDGE_UA });
  assert.equal(state.instance, "");
  assert.equal(B.INSTANCE_KEY in storage.data, false);
});

test("a corrupt stored id is replaced", async () => {
  const storage = fakeStorage({ [B.INSTANCE_KEY]: "../x" });
  const state = await B.load(storage, { crypto: fakeCrypto, userAgent: EDGE_UA, create: true });
  assert.notEqual(state.instance, "../x");
});

test("default label names the browser", async () => {
  const state = await B.load(fakeStorage(), { crypto: fakeCrypto, userAgent: EDGE_UA, create: true });
  assert.match(state.label, /^Edge /);
});

test("auth fields leave out an empty profile", () => {
  const f = B.authFields({ instance: "abcdefgh-1", profile: "", label: "Edge" }, "1.5.0");
  assert.deepEqual(f, { instance: "abcdefgh-1", label: "Edge", version: "1.5.0" });
  const g = B.authFields({ instance: "abcdefgh-1", profile: "p-work", label: "Edge" }, "1.5.0");
  assert.equal(g.profile, "p-work");
});

test("setBinding stores a valid profile and a trimmed label", async () => {
  const storage = fakeStorage();
  const out = await B.setBinding(storage, { profile: " p-work ", label: "  Edge   Work " });
  assert.deepEqual(out, { profile: "p-work", label: "Edge Work" });
  assert.equal(storage.data[B.PROFILE_KEY], "p-work");
});

test("setBinding refuses a path-like profile and keeps the old one", async () => {
  const storage = fakeStorage({ [B.PROFILE_KEY]: "p-home" });
  await assert.rejects(() => B.setBinding(storage, { profile: "../etc" }), /invalid profile/);
  assert.equal(storage.data[B.PROFILE_KEY], "p-home");
});

test("unbinding stores an empty profile and leaves the label alone", async () => {
  const storage = fakeStorage({ [B.PROFILE_KEY]: "p-home", [B.LABEL_KEY]: "Mine" });
  await B.setBinding(storage, { profile: "" });
  assert.equal(storage.data[B.PROFILE_KEY], "");
  assert.equal(storage.data[B.LABEL_KEY], "Mine");
});

test("options: 'Any profile' first, the bound one selected, an unknown bound id kept", () => {
  const profiles = [{ id: "p-home", name: "Personal" }, { id: "p-work", name: "Work" }];
  const opts = B.options(profiles, "p-work");
  assert.equal(opts[0].id, "");
  assert.equal(opts.find((o) => o.selected).id, "p-work");
  const stale = B.options(profiles, "p-gone");
  assert.equal(stale.find((o) => o.selected).id, "p-gone");
  assert.match(stale.find((o) => o.id === "p-gone").name, /no longer exists/);
});

test("binding frame and change detection", () => {
  assert.deepEqual(B.bindingFrame({ profile: "p-work", label: "Edge" }), { kind: "binding", profile: "p-work", label: "Edge" });
  assert.equal(B.isBindingChange({ [B.PROFILE_KEY]: {} }, "local"), true);
  assert.equal(B.isBindingChange({ [B.LABEL_KEY]: {} }, "local"), true);
  assert.equal(B.isBindingChange({ captureProfile: {} }, "local"), false);
  assert.equal(B.isBindingChange({ [B.PROFILE_KEY]: {} }, "session"), false);
});
```

- [ ] **Step 2: Run to verify failure**

Run: `export TMPDIR=/home/monoes/scratch/agent-tmp && node --test chrome-extension/browser_binding.test.mjs 2>&1 | tail -5`
Expected: failure loading `browser_binding.js` (ENOENT).

- [ ] **Step 3: Create `chrome-extension/browser_binding.js`**

```js
/**
 * MonoAgent Bridge — which monoagent profile this browser runs automations for
 *
 * Every browser profile with this extension runs its own copy of it, and the
 * bridge keeps one socket per copy. This file is what lets the bridge tell
 * them apart and route by profile:
 *
 *   INSTANCE ID. Minted once, by the service worker only (two contexts
 *   minting at once would race to two ids), kept in chrome.storage.local,
 *   which is per browser profile. It is how the bridge recognises the same
 *   browser across worker restarts.
 *
 *   BINDING. The monoagent profile whose browser actions run here, or "" for
 *   a default browser (runs profiles that have no browser of their own). Set
 *   in the side panel, or remotely with `monoagentcli extension bind`
 *   (a set_binding command). Either way it lands in storage, and the worker
 *   tells the bridge with a kind:"binding" frame. The socket is never
 *   reconnected for it, because that would drop a run's in-flight commands.
 *
 * isValidProfileId is kept identical to profiledir.ValidProfileID (Go) and
 * MonoCaptureProfile.isValidProfileId: the id becomes a directory name on
 * the other side.
 */

(function (root) {
  "use strict";

  const INSTANCE_KEY = "bridgeInstanceId";
  const PROFILE_KEY = "boundProfile";
  const LABEL_KEY = "browserLabel";
  const KEYS = [INSTANCE_KEY, PROFILE_KEY, LABEL_KEY];
  const MAX_LABEL = 60;
  const INSTANCE_RE = /^[A-Za-z0-9-]{8,64}$/;

  function isValidProfileId(id) {
    const value = typeof id === "string" ? id.trim() : "";
    if (!value) return false;
    if (value.includes("/") || value.includes("\\")) return false;
    if (value.includes("..")) return false;
    return true;
  }

  function browserName(ua) {
    const s = String(ua || "");
    if (/Edg\//.test(s)) return "Edge";
    if (/OPR\//.test(s)) return "Opera";
    if (/Chrome\//.test(s)) return "Chrome";
    return "Browser";
  }

  function cleanLabel(s) {
    const v = String(s || "").replace(/\s+/g, " ").trim();
    return v.length > MAX_LABEL ? v.slice(0, MAX_LABEL) : v;
  }

  function defaultLabel(ua, instance) {
    return `${browserName(ua)} ${String(instance || "").slice(0, 4)}`.trim();
  }

  /**
   * load reads this browser's binding. opts: { crypto, userAgent, create }.
   * Only the worker passes create:true; the side panel reads what the worker
   * made (and shows no id until it has).
   */
  async function load(storage, opts) {
    const o = opts || {};
    let stored = {};
    try {
      stored = (await storage.get(KEYS)) || {};
    } catch {
      // No storage: an unbound browser with a fresh id is still routable.
    }
    let instance = typeof stored[INSTANCE_KEY] === "string" ? stored[INSTANCE_KEY] : "";
    if (!INSTANCE_RE.test(instance)) {
      instance = "";
      if (o.create && o.crypto && typeof o.crypto.randomUUID === "function") {
        instance = o.crypto.randomUUID();
        try {
          await storage.set({ [INSTANCE_KEY]: instance });
        } catch {
          // Kept for this worker's life; the next start mints another.
        }
      }
    }
    const profile = isValidProfileId(stored[PROFILE_KEY]) ? stored[PROFILE_KEY].trim() : "";
    const label = cleanLabel(stored[LABEL_KEY]) || defaultLabel(o.userAgent, instance);
    return { instance, profile, label };
  }

  /** authFields is what the worker adds to its auth frame. */
  function authFields(state, version) {
    const out = { instance: state.instance, label: state.label, version: String(version || "") };
    if (state.profile) out.profile = state.profile;
    return out;
  }

  /** setBinding stores {profile, label?}. "" profile unbinds. */
  async function setBinding(storage, params) {
    const p = params || {};
    const raw = typeof p.profile === "string" ? p.profile : "";
    if (raw.trim() && !isValidProfileId(raw)) throw new Error("invalid profile id");
    const update = { [PROFILE_KEY]: raw.trim() };
    if (typeof p.label === "string") update[LABEL_KEY] = cleanLabel(p.label);
    await storage.set(update);
    return { profile: update[PROFILE_KEY], label: update[LABEL_KEY] };
  }

  function bindingFrame(state) {
    return { kind: "binding", profile: state.profile || "", label: state.label || "" };
  }

  function isBindingChange(changes, area) {
    return area === "local" && !!changes && (PROFILE_KEY in changes || LABEL_KEY in changes);
  }

  /** options is what the side panel's select offers. */
  function options(profiles, bound) {
    const list = [{ id: "", name: "Any profile (default browser)", selected: !bound }];
    let found = false;
    for (const p of Array.isArray(profiles) ? profiles : []) {
      if (!p || !isValidProfileId(p.id)) continue;
      const selected = p.id === bound;
      found = found || selected;
      list.push({ id: p.id, name: p.name || p.id, selected });
    }
    if (bound && !found) list.push({ id: bound, name: `${bound} (no longer exists)`, selected: true });
    return list;
  }

  root.MonoBrowserBinding = {
    INSTANCE_KEY, PROFILE_KEY, LABEL_KEY, MAX_LABEL,
    isValidProfileId, browserName, cleanLabel, defaultLabel,
    load, authFields, setBinding, bindingFrame, isBindingChange, options,
  };
})(globalThis);
```

- [ ] **Step 4: Run the JS tests**

Run: `export TMPDIR=/home/monoes/scratch/agent-tmp && node --test chrome-extension/browser_binding.test.mjs 2>&1 | tail -5`
Expected: `# pass 10`, `# fail 0`.

- [ ] **Step 5: Wire the worker (`background.js`)**

1. Line 24: add `"browser_binding.js"` after `"capture_profile.js"` in the `importScripts(...)` list.
2. After `let connectionStatus = …` (State section), add:

```js
// This browser's identity and binding (browser_binding.js), loaded before
// every dial and kept current by the storage listener below.
let binding = { instance: "", profile: "", label: "" };

function loadBinding() {
  return MonoBrowserBinding.load(chrome.storage.local, {
    crypto: self.crypto,
    userAgent: navigator.userAgent,
    create: true,
  });
}
```

3. In `doConnect`, right after `await stickyLoaded;`:

```js
  binding = await loadBinding();
```

4. In `ws.onopen`, before `ws.send(JSON.stringify(authFrame));`:

```js
    // Which browser this is and which profile it runs: the bridge keeps one
    // socket per browser and routes each profile's commands by this.
    Object.assign(authFrame, MonoBrowserBinding.authFields(binding, chrome.runtime.getManifest().version));
```

5. In `handleCommand`'s `switch (cmd.type)`, before `default:`:

```js
      case "set_binding":
        // `monoagentcli extension bind` / the app's Settings → Browsers. The
        // storage listener below reports it back with a binding frame.
        result = await MonoBrowserBinding.setBinding(chrome.storage.local, params);
        break;
```

6. After the existing `chrome.storage.onChanged.addListener(…)` for the pairing token, add:

```js
// The binding changed (side panel or set_binding). Tell the bridge on the
// open socket instead of reconnecting, which would drop a running
// workflow's in-flight commands. A bound browser also saves captures into
// its profile by default.
chrome.storage.onChanged.addListener((changes, area) => {
  if (!MonoBrowserBinding.isBindingChange(changes, area)) return;
  loadBinding()
    .then((state) => {
      binding = state;
      if (state.profile) MonoCaptureProfile.remember(chrome.storage.local, state.profile);
      sendFrame(MonoBrowserBinding.bindingFrame(state));
      broadcastStatus();
    })
    .catch((err) => console.warn("[monoagent] binding update:", err.message));
});
```

7. In `statusPayload()`, add `binding: { instance: binding.instance, profile: binding.profile, label: binding.label },`.

8. `manifest.json`: `"version": "1.4.0"` → `"version": "1.5.0"`.

- [ ] **Step 6: Side panel control**

Create `chrome-extension/sidepanel_binding.js`:

```js
/**
 * MonoAgent Bridge — the side panel's "Automations in this browser" field.
 *
 * Draws the binding select from the same profile list the capture picker
 * uses, and saves straight to chrome.storage.local. The worker notices the
 * write and tells the bridge (browser_binding.js), so there is one path for
 * a binding change whoever makes it.
 */

(function (root) {
  "use strict";

  function install(opts) {
    const B = root.MonoBrowserBinding;
    const doc = opts.doc;
    const select = doc.getElementById("binding-profile");
    const label = doc.getElementById("binding-label");
    const save = doc.getElementById("binding-save");
    const msg = doc.getElementById("binding-msg");
    let state = { instance: "", profile: "", label: "" };

    function say(tone, text) {
      msg.dataset.tone = tone;
      msg.textContent = text;
    }

    function redraw() {
      select.textContent = "";
      for (const o of B.options(opts.profiles(), state.profile)) {
        const opt = doc.createElement("option");
        opt.value = o.id;
        opt.textContent = o.name;
        opt.selected = o.selected;
        select.appendChild(opt);
      }
    }

    async function refresh() {
      state = await B.load(opts.storage, { userAgent: opts.userAgent });
      label.value = state.label;
      redraw();
    }

    save.addEventListener("click", async () => {
      try {
        await B.setBinding(opts.storage, { profile: select.value, label: label.value });
        await refresh();
        say("ok", select.value
          ? "Saved. This profile's automations now open their tabs in this browser."
          : "Saved. This is a default browser again.");
      } catch (err) {
        say("error", err.message);
      }
    });

    return { refresh, redraw };
  }

  root.MonoPanelBinding = { install };
})(globalThis);
```

In `sidepanel.html`, add as the first `.field` inside `#settings-panel .panel-body` (before the pairing token field):

```html
          <div class="field" id="binding-field">
            <label for="binding-profile">Automations in this browser</label>
            <select id="binding-profile"></select>
            <p class="note-line">
              Workflows for the chosen profile open their tabs here, signed in as this browser's accounts.
              "Any profile" makes this the default browser for profiles that have none of their own.
            </p>
            <label for="binding-label">Name for this browser</label>
            <input type="text" id="binding-label" maxlength="60" placeholder="e.g. Edge — Work" />
            <button type="button" id="binding-save" class="btn-secondary btn-tiny">Save</button>
            <div id="binding-msg" class="msg" role="status" aria-live="polite"></div>
          </div>
```

Add before `<script src="sidepanel.js"></script>`:

```html
  <script src="browser_binding.js"></script>
  <script src="sidepanel_binding.js"></script>
```

In `sidepanel.js`, at the start of the `// --- connection settings ---` section:

```js
// Which profile this browser runs automations for (sidepanel_binding.js).
// Its select reuses the capture picker's profile list.
const panelBinding = MonoPanelBinding.install({
  doc: document,
  storage: chrome.storage.local,
  userAgent: navigator.userAgent,
  profiles: () => (lastFormState && Array.isArray(lastFormState.profiles) ? lastFormState.profiles : []),
});
panelBinding.refresh().catch(() => {});
```

At the end of `drawProfiles(state)`, add:

```js
  // The binding select offers the same list; redraw it when the list changes.
  if (typeof panelBinding !== "undefined") panelBinding.redraw();
```

- [ ] **Step 7: Run all extension tests, including the file-count guard**

Run: `export TMPDIR=/home/monoes/scratch/agent-tmp && node --test 'chrome-extension/**/*.test.mjs' 2>&1 | tail -6`
Expected: `# fail 0`. Browser tests (`*.browser.test.mjs`) skip without a browser, as they do in CI.

- [ ] **Step 8: Commit**

```bash
git add chrome-extension/
git commit -m "feat(extension): bind a browser to a monoagent profile (1.5.0)"
```

---

### Task 8: Desktop app: Settings → Browsers

**Files:**
- Create: `wails-app/app_browsers.go`
- Create: `wails-app/app_browsers_test.go`
- Create: `wails-app/frontend/src/components/settings/BrowserBindingsSection.jsx`
- Create: `wails-app/frontend/src/components/settings/BrowserBindingsSection.render.test.jsx`
- Modify: `wails-app/frontend/src/pages/Settings.jsx` (import + mount)
- Modify: `wails-app/frontend/src/wailsjs/go/main/App.js`, `App.d.ts`, `wailsjs/go/models.ts` (bindings)

**Interfaces:**
- Consumes: CLI JSON from Task 6 (`extension browsers|bind|unbind --json`).
- Produces: `App.GetBrowsers() (*BrowsersReport, error)`, `App.BindBrowser(instance, profileID string) (*BrowserRow, error)`.

- [ ] **Step 1: Write the failing Go test**

Create `wails-app/app_browsers_test.go`:

```go
package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeBrowsersCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	argsLog := filepath.Join(dir, "args.log")
	script := `#!/bin/sh
echo "$*" >> '` + argsLog + `'
case "$*" in
  *" extension browsers"*) echo '{"running":true,"browsers":[{"instance":"3f2a91c0-0000-4000-8000-000000000001","label":"Edge Work","profile_id":"p-work","profile_name":"Work","legacy":false,"conflict":false,"version":"1.5.0","connected_at":"2026-09-27T14:02:00Z"}]}';;
  *" extension bind "*) echo '{"instance":"3f2a91c0-0000-4000-8000-000000000001","label":"Edge Work","profile_id":"p-home","profile_name":"Personal"}';;
  *" extension unbind "*) echo '{"instance":"3f2a91c0-0000-4000-8000-000000000001","label":"Edge Work","profile_id":"","profile_name":""}';;
  *) echo 'unexpected' >&2; exit 2;;
esac
`
	bin := filepath.Join(dir, "monoagentcli")
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MONOAGENTCLI_BIN", bin)
	return argsLog
}

func TestBrowserBindingsShellOutToCLI(t *testing.T) {
	argsLog := fakeBrowsersCLI(t)
	a := newTestApp(t)
	a.ctx = context.Background()
	a.setActiveProfileID("p-work")

	rep, err := a.GetBrowsers()
	if err != nil || !rep.Running || len(rep.Browsers) != 1 || rep.Browsers[0].ProfileName != "Work" {
		t.Fatalf("GetBrowsers = %+v, %v", rep, err)
	}
	row, err := a.BindBrowser("3f2a91c0-0000-4000-8000-000000000001", "p-home")
	if err != nil || row.ProfileID != "p-home" {
		t.Fatalf("BindBrowser = %+v, %v", row, err)
	}
	row, err = a.BindBrowser("3f2a91c0-0000-4000-8000-000000000001", "")
	if err != nil || row.ProfileID != "" {
		t.Fatalf("unbind = %+v, %v", row, err)
	}
	if _, err := a.BindBrowser("", "p-home"); err == nil {
		t.Fatal("an empty browser id must be refused before shelling out")
	}

	log, _ := os.ReadFile(argsLog)
	for _, want := range []string{"extension browsers", "extension bind 3f2a91c0-0000-4000-8000-000000000001 p-home", "extension unbind 3f2a91c0-0000-4000-8000-000000000001"} {
		if !strings.Contains(string(log), want) {
			t.Errorf("argv log missing %q:\n%s", want, log)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd wails-app && export TMPDIR=/home/monoes/scratch/agent-tmp GOTMPDIR=/home/monoes/scratch/agent-tmp && go test -run TestBrowserBindings . 2>&1 | head -5`
Expected: `a.GetBrowsers undefined`.

- [ ] **Step 3: Create `wails-app/app_browsers.go`**

```go
package main

// Settings → Browsers: which browser each profile's automations run in.
// Everything shells out to `monoagentcli extension browsers|bind|unbind`;
// the binding itself is stored in each browser's extension.

import (
	"errors"
	"strings"
)

// BrowserRow mirrors one row of `extension browsers --json`.
type BrowserRow struct {
	Instance    string `json:"instance"`
	Label       string `json:"label"`
	ProfileID   string `json:"profile_id"`
	ProfileName string `json:"profile_name"`
	Legacy      bool   `json:"legacy"`
	Conflict    bool   `json:"conflict"`
	Version     string `json:"version"`
	ConnectedAt string `json:"connected_at"`
}

// BrowsersReport mirrors `extension browsers --json`.
type BrowsersReport struct {
	Running  bool         `json:"running"`
	Hint     string       `json:"hint"`
	Browsers []BrowserRow `json:"browsers"`
}

// GetBrowsers lists the browsers attached to the bridge.
func (a *App) GetBrowsers() (*BrowsersReport, error) {
	var r BrowsersReport
	if err := a.cliJSON(profileCLITimeout, &r, "extension", "browsers"); err != nil {
		return nil, err
	}
	if r.Browsers == nil {
		r.Browsers = []BrowserRow{}
	}
	return &r, nil
}

// BindBrowser binds a browser to a profile; an empty profileID unbinds it.
func (a *App) BindBrowser(instance, profileID string) (*BrowserRow, error) {
	instance = strings.TrimSpace(instance)
	if instance == "" {
		return nil, errors.New("no browser given")
	}
	args := []string{"extension", "unbind", instance}
	if p := strings.TrimSpace(profileID); p != "" {
		args = []string{"extension", "bind", instance, p}
	}
	var row BrowserRow
	if err := a.runMonoCLI("", &row, args...); err != nil {
		return nil, err
	}
	return &row, nil
}
```

- [ ] **Step 4: Run the Go test**

Run: `cd wails-app && export TMPDIR=/home/monoes/scratch/agent-tmp GOTMPDIR=/home/monoes/scratch/agent-tmp && go test -run TestBrowserBindings .`
Expected: `ok`.

- [ ] **Step 5: Regenerate the JS bindings**

Run: `cd wails-app && wails generate module`. If the wails CLI is not installed, add these by hand, following the neighbouring entries' exact format.

`App.js`:

```js
export function BindBrowser(arg1, arg2) {
  return window['go']['main']['App']['BindBrowser'](arg1, arg2);
}

export function GetBrowsers() {
  return window['go']['main']['App']['GetBrowsers']();
}
```

`App.d.ts`: `export function BindBrowser(arg1:string,arg2:string):Promise<main.BrowserRow>;` and `export function GetBrowsers():Promise<main.BrowsersReport>;`. Add the `BrowserRow`/`BrowsersReport` classes to `models.ts` as the generator would.

**Only commit the hunks for these two methods.** `models.ts` already carries unrelated uncommitted changes in the main checkout. In the worktree, review `git diff` before `git add -p`.

- [ ] **Step 6: Write the failing component test**

Create `wails-app/frontend/src/components/settings/BrowserBindingsSection.render.test.jsx`:

```jsx
// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'

const App = {}
beforeEach(() => {
  for (const k of ['GetBrowsers', 'BindBrowser', 'GetProfiles']) App[k] = vi.fn()
  window.go = { main: { App } }
  App.GetProfiles.mockResolvedValue([{ id: 'p-work', name: 'Work' }, { id: 'p-home', name: 'Personal' }])
})
afterEach(() => { cleanup(); delete window.go })

const edge = { instance: '3f2a91c0-0000-4000-8000-000000000001', label: 'Edge Work', profile_id: 'p-work', profile_name: 'Work', legacy: false, conflict: false, version: '1.5.0' }

async function mount(report) {
  App.GetBrowsers.mockResolvedValue(report)
  const { default: BrowserBindingsSection } = await import('./BrowserBindingsSection.jsx')
  render(<BrowserBindingsSection />)
  await screen.findByTestId('browser-bindings')
  await waitFor(() => expect(App.GetBrowsers).toHaveBeenCalled())
}

describe('BrowserBindingsSection', () => {
  it('shows each browser with its bound profile selected', async () => {
    await mount({ running: true, browsers: [edge] })
    const select = await screen.findByLabelText('Profile for Edge Work')
    expect(select).toHaveValue('p-work')
  })

  it('binds through the CLI when the select changes, then reloads', async () => {
    await mount({ running: true, browsers: [edge] })
    App.BindBrowser.mockResolvedValue({ ...edge, profile_id: 'p-home' })
    fireEvent.change(await screen.findByLabelText('Profile for Edge Work'), { target: { value: 'p-home' } })
    await waitFor(() => expect(App.BindBrowser).toHaveBeenCalledWith(edge.instance, 'p-home'))
    await waitFor(() => expect(App.GetBrowsers).toHaveBeenCalledTimes(2))
  })

  it('warns about a conflict and disables a legacy browser', async () => {
    await mount({ running: true, browsers: [
      { ...edge, conflict: true },
      { instance: 'legacy', label: '', profile_id: '', legacy: true },
    ] })
    expect(await screen.findByText(/only the most recently connected one is used/)).toBeInTheDocument()
    expect(screen.getByLabelText('Profile for legacy')).toBeDisabled()
  })

  it('says when no bridge is running', async () => {
    await mount({ running: false, hint: 'No bridge is running. Start one with `monoagentcli extension serve`.', browsers: [] })
    expect(await screen.findByText(/No bridge is running/)).toBeInTheDocument()
  })
})
```

- [ ] **Step 7: Create `BrowserBindingsSection.jsx`**

```jsx
import { useState, useEffect, useCallback } from 'react'
import { GetBrowsers, BindBrowser, GetProfiles } from '../../wailsjs/go/main/App'

// Which browser each profile's automations run in. Everything goes through
// `monoagentcli extension browsers|bind|unbind` (app_browsers.go). The
// binding lives in each browser's extension; this only asks it to change.

const mono = 'var(--font-mono)'
const card = {
  background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 'var(--radius-lg)',
  padding: '16px 20px', display: 'flex', flexDirection: 'column', gap: 14, marginBottom: 16,
}
const label = { fontFamily: mono, fontSize: 10, color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: 1 }
const hint = { fontFamily: 'var(--font-body)', fontSize: 10.5, color: 'var(--text-muted)', lineHeight: 1.5 }
const errText = { fontFamily: mono, fontSize: 10.5, color: 'var(--red)', lineHeight: 1.5, wordBreak: 'break-word' }
const warnText = { fontFamily: 'var(--font-body)', fontSize: 10.5, color: 'var(--yellow)', lineHeight: 1.5 }
const row = { display: 'flex', alignItems: 'center', gap: 12, borderTop: '1px solid var(--border)', paddingTop: 10 }
const name = { fontFamily: 'var(--font-body)', fontSize: 12.5, color: 'var(--text)' }
const input = {
  background: 'var(--elevated)', border: '1px solid var(--border)', color: 'var(--text)',
  borderRadius: 'var(--radius)', fontFamily: mono, fontSize: 12, padding: '6px 10px', minWidth: 180,
}

const errMsg = (e) => String(e?.message || e || 'unknown error')

export default function BrowserBindingsSection() {
  const [report, setReport] = useState(null)
  const [profiles, setProfiles] = useState([])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState('')

  const load = useCallback(async () => {
    try {
      const [r, p] = await Promise.all([GetBrowsers(), GetProfiles()])
      setReport(r)
      setProfiles(p || [])
      setError('')
    } catch (e) {
      setError(errMsg(e))
    }
  }, [])
  useEffect(() => { load() }, [load])

  const bind = async (instance, profileId) => {
    setBusy(instance)
    try {
      await BindBrowser(instance, profileId)
      await load()
    } catch (e) {
      setError(errMsg(e))
    } finally {
      setBusy('')
    }
  }

  const browsers = report?.browsers || []
  return (
    <div style={card} data-testid="browser-bindings">
      <div style={label}>Browsers</div>
      <div style={hint}>
        Each browser profile with the MonoAgent Bridge extension can run one profile's automations,
        signed in as that browser's accounts. Profiles bound to different browsers run side by side.
      </div>
      {error && <div style={errText} role="alert">{error}</div>}
      {report && !report.running && <div style={hint}>{report.hint || 'No bridge is running.'}</div>}
      {report?.running && report.hint && <div style={warnText}>{report.hint}</div>}
      {report?.running && !report.hint && browsers.length === 0 && (
        <div style={hint}>No browser is attached. Open a browser that has the MonoAgent Bridge extension.</div>
      )}
      {browsers.map((b) => {
        const title = b.label || b.instance.slice(0, 8)
        return (
          <div key={b.instance} data-browser={b.instance} style={row}>
            <div style={{ flex: 1, minWidth: 0 }}>
              <div style={name}>{title}</div>
              <div style={hint}>{b.instance.slice(0, 8)}{b.version ? ` · extension ${b.version}` : ''}</div>
              {b.conflict && <div style={warnText}>Another browser is bound to the same profile — only the most recently connected one is used.</div>}
              {b.legacy && <div style={warnText}>This browser's extension is too old to bind. Reload it from the browser's extensions page.</div>}
            </div>
            <select
              aria-label={`Profile for ${b.label || b.instance}`}
              value={b.profile_id || ''}
              disabled={b.legacy || busy === b.instance}
              onChange={(e) => bind(b.instance, e.target.value)}
              style={input}
            >
              <option value="">Any profile (default browser)</option>
              {profiles.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
              {b.profile_id && !profiles.some((p) => p.id === b.profile_id) && (
                <option value={b.profile_id}>{b.profile_id} (unknown)</option>
              )}
            </select>
          </div>
        )
      })}
    </div>
  )
}
```

In `Settings.jsx`, add `import BrowserBindingsSection from '../components/settings/BrowserBindingsSection.jsx'` next to the other section imports, and render `<BrowserBindingsSection />` directly after the `<HealthSection … />` line (363).

- [ ] **Step 8: Run the frontend tests**

Run: `cd wails-app/frontend && export TMPDIR=/home/monoes/scratch/agent-tmp && npx vitest run src/components/settings/BrowserBindingsSection.render.test.jsx 2>&1 | tail -6`
Expected: `4 passed`.

- [ ] **Step 9: Commit**

```bash
git add wails-app/app_browsers.go wails-app/app_browsers_test.go wails-app/frontend/src/components/settings/BrowserBindingsSection.jsx wails-app/frontend/src/components/settings/BrowserBindingsSection.render.test.jsx wails-app/frontend/src/pages/Settings.jsx
git add -p wails-app/frontend/src/wailsjs/go/main/App.js wails-app/frontend/src/wailsjs/go/main/App.d.ts wails-app/frontend/src/wailsjs/go/models.ts
git commit -m "feat(app): Settings → Browsers binds each browser to a profile"
```

---

### Task 9: `summary --all-profiles`: one roll-up across every profile

**Files:**
- Create: `internal/summary/global.go`
- Create: `internal/summary/global_test.go`
- Modify: `internal/summary/summary.go` (`Summary.Scope`, `Summary.Profiles`, `Build` sets scope)
- Modify: `internal/summary/workflows.go` (`ExecRow`, `ScheduleRow`, `ScheduleIssue` gain profile fields)
- Modify: `internal/summary/system.go` (`SessionRow` gains profile fields)
- Create: `cmd/monoagentcli/summary_all.go`
- Modify: `cmd/monoagentcli/summary.go` (use `summaryOptions`; `--all-profiles`)
- Test: `cmd/monoagentcli/summary_test.go` (append)

**Interfaces:**
- Produces: `summary.ScopeProfile = "profile"`, `summary.ScopeGlobal = "global"`; `Summary.Scope string` (`json:"scope"`); `Summary.Profiles []ProfileHeadline` (`json:"profiles,omitempty"`); `ProfileHeadline{ID, Name string; Current bool; WorkflowsActive, Running, Failed24h, WaitingForYou int; Error string}` with JSON `id,name,current,workflows_active,running,failed_24h,waiting_for_you,error`; `summary.ProfilePart{ID, Name string; S Summary}`; `summary.Merge(parts []ProfilePart, shared Summary, current string) Summary`; `summary.SortRecent(rows []ExecRow, limit int) []ExecRow`; `ProfileID`/`ProfileName` (`json:"profile_id,omitempty"`/`json:"profile_name,omitempty"`) on `ExecRow`, `ScheduleRow`, `ScheduleIssue`, `SessionRow`; CLI `summary --all-profiles`.

- [ ] **Step 1: Write the failing merge tests**

Create `internal/summary/global_test.go`:

```go
package summary

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func part(id, name string, s Summary) ProfilePart { return ProfilePart{ID: id, Name: name, S: s} }

func TestMergeSumsCountsAndTagsRows(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	at := func(m int) string { return now.Add(time.Duration(m) * time.Minute).Format(time.RFC3339) }
	work := Summary{
		Workflows: &WorkflowsSection{Total: 3, Active: 2},
		Executions: &ExecutionsSection{Running: 1, Last24h: ExecCounts{Total: 5, Failed: 1},
			Recent: []ExecRow{{ID: "w1", CreatedAt: at(-1)}, {ID: "w2", CreatedAt: at(-30)}}},
		Schedules:    &SchedulesSection{DaemonRunning: true, Upcoming: []ScheduleRow{{WorkflowID: "wa", NextRun: at(50)}}},
		HIL:          &HILSection{Total: 2, WorkflowPending: 2, OldestWaitingSince: at(-90)},
		Accounts:     &AccountsSection{Sessions: []SessionRow{{Platform: "linkedin", Username: "work"}}, Active: 1},
		Applications: &ApplicationsSection{ByStatus: map[string]int{"pending": 2}},
	}
	home := Summary{
		Workflows: &WorkflowsSection{Total: 1, Active: 1, Error: "boom"},
		Executions: &ExecutionsSection{Waiting: 2, Last24h: ExecCounts{Total: 1},
			Recent: []ExecRow{{ID: "h1", CreatedAt: at(-10)}}},
		Schedules:    &SchedulesSection{Upcoming: []ScheduleRow{{WorkflowID: "hb", NextRun: at(5)}}},
		HIL:          &HILSection{Total: 1, OldestWaitingSince: at(-200)},
		Accounts:     &AccountsSection{Sessions: []SessionRow{{Platform: "linkedin", Username: "me"}}, Expired: 1},
		Applications: &ApplicationsSection{ByStatus: map[string]int{"pending": 1, "sent": 4}},
	}
	shared := Summary{GeneratedAt: "g", Services: &ServicesSection{}}
	got := Merge([]ProfilePart{part("p-work", "Work", work), part("p-home", "Personal", home)}, shared, "p-home")

	if got.Scope != ScopeGlobal || got.Services == nil || got.GeneratedAt != "g" || got.ProfileID != "" {
		t.Fatalf("envelope: scope=%q services=%v generated=%q profile=%q", got.Scope, got.Services, got.GeneratedAt, got.ProfileID)
	}
	if w := got.Workflows; w.Total != 4 || w.Active != 3 || w.Error != "Personal: boom" {
		t.Errorf("workflows = %+v", w)
	}
	ex := got.Executions
	if ex.Running != 1 || ex.Waiting != 2 || ex.Last24h.Total != 6 || ex.Last24h.Failed != 1 {
		t.Errorf("executions = %+v", ex)
	}
	for i, id := range []string{"w1", "h1", "w2"} {
		if ex.Recent[i].ID != id {
			t.Fatalf("recent order = %+v, want w1 h1 w2", ex.Recent)
		}
	}
	if ex.Recent[1].ProfileID != "p-home" || ex.Recent[1].ProfileName != "Personal" {
		t.Errorf("row not tagged: %+v", ex.Recent[1])
	}
	if s := got.Schedules; !s.DaemonRunning || s.Upcoming[0].WorkflowID != "hb" || s.Upcoming[0].ProfileName != "Personal" {
		t.Errorf("schedules = %+v", s)
	}
	if h := got.HIL; h.Total != 3 || h.WorkflowPending != 2 || h.OldestWaitingSince != at(-200) {
		t.Errorf("hil = %+v", h)
	}
	if a := got.Accounts; len(a.Sessions) != 2 || a.Sessions[0].ProfileID != "p-work" || a.Active != 1 || a.Expired != 1 {
		t.Errorf("accounts = %+v", a)
	}
	if a := got.Applications; a.ByStatus["pending"] != 3 || a.ByStatus["sent"] != 4 {
		t.Errorf("applications = %+v", a)
	}
	if p := got.Profiles; len(p) != 2 || p[0].Current || !p[1].Current || p[0].Failed24h != 1 || p[0].Running != 1 ||
		p[1].WaitingForYou != 1 || p[1].Error == "" {
		t.Errorf("profiles = %+v", p)
	}
	if got.People != nil || got.Vault != nil {
		t.Error("a section no profile reported must stay nil")
	}
}

func TestSortRecentTrims(t *testing.T) {
	rows := make([]ExecRow, 20)
	for i := range rows {
		rows[i] = ExecRow{ID: fmt.Sprint(i), CreatedAt: time.Date(2026, 1, 1, 0, i, 0, 0, time.UTC).Format(time.RFC3339)}
	}
	got := SortRecent(rows, 15)
	if len(got) != 15 || got[0].ID != "19" || got[14].ID != "5" {
		t.Fatalf("got %d rows, first %s, last %s", len(got), got[0].ID, got[len(got)-1].ID)
	}
	if all := SortRecent(rows, -1); len(all) != 20 {
		t.Fatalf("limit -1 keeps all, got %d", len(all))
	}
}

func TestBuildSaysProfileScope(t *testing.T) {
	if s := Build(context.Background(), Options{Sections: map[string]bool{}}); s.Scope != ScopeProfile {
		t.Fatalf("scope = %q", s.Scope)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `export TMPDIR=/home/monoes/scratch/agent-tmp GOTMPDIR=/home/monoes/scratch/agent-tmp && go test ./internal/summary/ -run 'TestMerge|TestSortRecent|TestBuildSaysProfileScope' 2>&1 | head -5`
Expected: `undefined: ProfilePart`.

- [ ] **Step 3: Add the fields**

`summary.go`: add `Scope string \`json:"scope"\`` directly after `ProfileID` in `Summary`, and `Profiles []ProfileHeadline \`json:"profiles,omitempty"\`` as its last field. In `Build`, change the envelope line to:

```go
	s := Summary{V: 1, GeneratedAt: o.Now.UTC().Format(time.RFC3339), ProfileID: o.ProfileID, Scope: ScopeProfile}
```

`workflows.go`: add these two fields as the last fields of `ExecRow`, `ScheduleRow` and `ScheduleIssue`, and `system.go`: to `SessionRow`:

```go
	// ProfileID and ProfileName are set only in the All profiles view
	// (Merge), so every row says whose it is.
	ProfileID   string `json:"profile_id,omitempty"`
	ProfileName string `json:"profile_name,omitempty"`
```

- [ ] **Step 4: Create `internal/summary/global.go`**

```go
package summary

import (
	"sort"
	"time"
)

// The global dashboard: every profile's summary rolled into one. Each
// section is still computed per profile by Build, so the queries stay
// profile-scoped. Merge only adds, concatenates and tags. Sections that are
// the same whichever profile asks (services, automations) come from one
// build and are copied as they are.

const (
	ScopeProfile = "profile"
	ScopeGlobal  = "global"
)

// ProfilePart is one profile's summary, as Merge takes it.
type ProfilePart struct {
	ID   string
	Name string
	S    Summary
}

func (p ProfilePart) label() string {
	if p.Name != "" {
		return p.Name
	}
	return p.ID
}

// ProfileHeadline is one profile's row in the global dashboard.
type ProfileHeadline struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Current         bool   `json:"current"`
	WorkflowsActive int    `json:"workflows_active"`
	Running         int    `json:"running"`
	Failed24h       int    `json:"failed_24h"`
	WaitingForYou   int    `json:"waiting_for_you"`
	Error           string `json:"error,omitempty"`
}

// Merge rolls per-profile summaries into the global one. shared holds the
// profile-independent sections; current is the profile the caller runs as.
func Merge(parts []ProfilePart, shared Summary, current string) Summary {
	out := Summary{
		V: 1, GeneratedAt: shared.GeneratedAt, Scope: ScopeGlobal,
		Services: shared.Services, Automations: shared.Automations,
		Profiles: make([]ProfileHeadline, 0, len(parts)),
	}
	for _, p := range parts {
		out.Profiles = append(out.Profiles, headline(p, current))
		addRuns(&out, p)
		addInbox(&out, p)
		addSystem(&out, p)
	}
	if out.Executions != nil {
		out.Executions.Recent = SortRecent(out.Executions.Recent, recentLimit)
	}
	if out.Schedules != nil {
		sort.SliceStable(out.Schedules.Upcoming, func(i, j int) bool {
			return before(out.Schedules.Upcoming[i].NextRun, out.Schedules.Upcoming[j].NextRun)
		})
	}
	return out
}

// SortRecent orders runs newest first and keeps at most limit of them
// (limit <= 0 keeps all).
func SortRecent(rows []ExecRow, limit int) []ExecRow {
	sort.SliceStable(rows, func(i, j int) bool { return before(rows[j].CreatedAt, rows[i].CreatedAt) })
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
}

// before compares two of this package's timestamps (normaliseTime writes
// RFC3339 UTC); anything unparseable falls back to string order.
func before(a, b string) bool {
	ta, errA := time.Parse(time.RFC3339, a)
	tb, errB := time.Parse(time.RFC3339, b)
	if errA == nil && errB == nil {
		return ta.Before(tb)
	}
	return a < b
}

// tagErr appends one profile's section error, prefixed with whose it is.
func tagErr(have, err, who string) string {
	if err == "" {
		return have
	}
	e := who + ": " + err
	if have == "" {
		return e
	}
	return have + "; " + e
}

func headline(p ProfilePart, current string) ProfileHeadline {
	h := ProfileHeadline{ID: p.ID, Name: p.label(), Current: p.ID == current}
	if w := p.S.Workflows; w != nil {
		h.WorkflowsActive = w.Active
		h.Error = tagErr(h.Error, w.Error, "workflows")
	}
	if e := p.S.Executions; e != nil {
		h.Running = e.Running
		h.Failed24h = e.Last24h.Failed
		h.Error = tagErr(h.Error, e.Error, "executions")
	}
	if hl := p.S.HIL; hl != nil {
		h.WaitingForYou = hl.Total
		h.Error = tagErr(h.Error, hl.Error, "hil")
	}
	return h
}

func addRuns(out *Summary, p ProfilePart) {
	who := p.label()
	if s := p.S.Workflows; s != nil {
		if out.Workflows == nil {
			out.Workflows = &WorkflowsSection{}
		}
		d := out.Workflows
		d.Total += s.Total
		d.Active += s.Active
		d.Error = tagErr(d.Error, s.Error, who)
	}
	if s := p.S.Executions; s != nil {
		if out.Executions == nil {
			out.Executions = &ExecutionsSection{Recent: []ExecRow{}}
		}
		d := out.Executions
		d.Running += s.Running
		d.Queued += s.Queued
		d.Waiting += s.Waiting
		d.Last24h.Total += s.Last24h.Total
		d.Last24h.Success += s.Last24h.Success
		d.Last24h.Failed += s.Last24h.Failed
		d.Last24h.Cancelled += s.Last24h.Cancelled
		for _, r := range s.Recent {
			r.ProfileID, r.ProfileName = p.ID, who
			d.Recent = append(d.Recent, r)
		}
		d.Error = tagErr(d.Error, s.Error, who)
	}
	if s := p.S.Schedules; s != nil {
		if out.Schedules == nil {
			out.Schedules = &SchedulesSection{Upcoming: []ScheduleRow{}, Invalid: []ScheduleIssue{}}
		}
		d := out.Schedules
		d.DaemonRunning = d.DaemonRunning || s.DaemonRunning
		for _, r := range s.Upcoming {
			r.ProfileID, r.ProfileName = p.ID, who
			d.Upcoming = append(d.Upcoming, r)
		}
		for _, r := range s.Invalid {
			r.ProfileID, r.ProfileName = p.ID, who
			d.Invalid = append(d.Invalid, r)
		}
		d.Error = tagErr(d.Error, s.Error, who)
	}
}

func addInbox(out *Summary, p ProfilePart) {
	who := p.label()
	if s := p.S.HIL; s != nil {
		if out.HIL == nil {
			out.HIL = &HILSection{}
		}
		d := out.HIL
		d.WorkflowPending += s.WorkflowPending
		d.PeopleReview += s.PeopleReview
		d.Drafts += s.Drafts
		d.LinkSuggestions += s.LinkSuggestions
		d.Total += s.Total
		if s.OldestWaitingSince != "" && (d.OldestWaitingSince == "" || before(s.OldestWaitingSince, d.OldestWaitingSince)) {
			d.OldestWaitingSince = s.OldestWaitingSince
		}
		d.Error = tagErr(d.Error, s.Error, who)
	}
	if s := p.S.People; s != nil {
		if out.People == nil {
			out.People = &PeopleSection{}
		}
		d := out.People
		d.Total += s.Total
		d.Added7d += s.Added7d
		d.Lists += s.Lists
		d.Error = tagErr(d.Error, s.Error, who)
	}
	if s := p.S.Activity; s != nil {
		if out.Activity == nil {
			out.Activity = &ActivitySection{}
		}
		d := out.Activity
		d.Captures7d += s.Captures7d
		d.CapturesTotal += s.CapturesTotal
		d.Documents.Total += s.Documents.Total
		d.Documents.Indexed += s.Documents.Indexed
		d.Documents.IndexErrors += s.Documents.IndexErrors
		d.Documents.Summarising += s.Documents.Summarising
		d.Documents.SummaryErrors += s.Documents.SummaryErrors
		d.MessagesIn7d += s.MessagesIn7d
		d.MessagesOut7d += s.MessagesOut7d
		d.MessagesUnread += s.MessagesUnread
		d.Error = tagErr(d.Error, s.Error, who)
	}
	if s := p.S.Applications; s != nil {
		if out.Applications == nil {
			out.Applications = &ApplicationsSection{ByStatus: map[string]int{}}
		}
		d := out.Applications
		for k, v := range s.ByStatus {
			d.ByStatus[k] += v
		}
		d.Evaluated += s.Evaluated
		d.UnevaluatedPending += s.UnevaluatedPending
		d.Added7d += s.Added7d
		d.Error = tagErr(d.Error, s.Error, who)
	}
}

func addSystem(out *Summary, p ProfilePart) {
	who := p.label()
	if s := p.S.Recordings; s != nil {
		if out.Recordings == nil {
			out.Recordings = &RecordingsSection{}
		}
		d := out.Recordings
		d.Total += s.Total
		d.Unsaved += s.Unsaved
		d.Incomplete += s.Incomplete
		if before(d.LatestStartedAt, s.LatestStartedAt) {
			d.LatestStartedAt = s.LatestStartedAt
		}
		d.Error = tagErr(d.Error, s.Error, who)
	}
	if s := p.S.Jev; s != nil {
		if out.Jev == nil {
			out.Jev = &JevSection{}
		}
		d := out.Jev
		if s.KeyConfigured && !d.KeyConfigured || d.KeySource == "" {
			d.KeySource = s.KeySource
		}
		d.KeyConfigured = d.KeyConfigured || s.KeyConfigured
		d.SurfacesEnabled += s.SurfacesEnabled
		d.Calls24h += s.Calls24h
		d.Failures24h += s.Failures24h
		d.EstimatedUSD24h += s.EstimatedUSD24h
		d.Error = tagErr(d.Error, s.Error, who)
	}
	if s := p.S.Accounts; s != nil {
		if out.Accounts == nil {
			out.Accounts = &AccountsSection{Sessions: []SessionRow{}}
		}
		d := out.Accounts
		for _, r := range s.Sessions {
			r.ProfileID, r.ProfileName = p.ID, who
			d.Sessions = append(d.Sessions, r)
		}
		d.Active += s.Active
		d.Expired += s.Expired
		d.ExpiringSoon += s.ExpiringSoon
		d.Error = tagErr(d.Error, s.Error, who)
	}
	if s := p.S.Vault; s != nil {
		if out.Vault == nil {
			out.Vault = &VaultSection{}
		}
		d := out.Vault
		d.Secrets += s.Secrets
		d.Images += s.Images
		d.ImageBytes += s.ImageBytes
		d.Error = tagErr(d.Error, s.Error, who)
	}
}
```

- [ ] **Step 5: Run the merge tests**

Run: `export TMPDIR=/home/monoes/scratch/agent-tmp GOTMPDIR=/home/monoes/scratch/agent-tmp && go test ./internal/summary/`
Expected: `ok`.

- [ ] **Step 6: Write the failing CLI test**

Append to `cmd/monoagentcli/summary_test.go`:

```go
func TestSummaryAllProfilesRollsUp(t *testing.T) {
	cfg := newSummaryCLITestDB(t) // profile "default": workflow w1 (active), one pending HIL
	db, err := storage.NewDatabase(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec(`INSERT INTO profiles (id, name) VALUES ('p-work', 'Work');
		INSERT INTO workflows (id, name, is_active, profile_id) VALUES ('w2','B',1,'p-work'), ('w3','C',0,'p-work');
		INSERT INTO hil_pending (id, execution_id, workflow_id, node_id, node_name, status, profile_id) VALUES ('h2','e2','w2','n','N','pending','p-work')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	out, err := runSummary(t, cfg, "--all-profiles")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Scope     string `json:"scope"`
		Workflows struct {
			Total  int `json:"total"`
			Active int `json:"active"`
		} `json:"workflows"`
		HIL struct {
			WorkflowPending int `json:"workflow_pending"`
		} `json:"hil"`
		Services json.RawMessage `json:"services"`
		Profiles []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Current bool   `json:"current"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if got.Scope != "global" || got.Workflows.Total != 3 || got.Workflows.Active != 2 || got.HIL.WorkflowPending != 2 || len(got.Services) == 0 {
		t.Fatalf("global summary = %s", out)
	}
	if len(got.Profiles) != 2 || got.Profiles[0].ID != "default" || !got.Profiles[0].Current || got.Profiles[1].Name != "Work" {
		t.Fatalf("profiles = %+v", got.Profiles)
	}

	// The profile view is unchanged apart from saying which it is.
	out, err = runSummary(t, cfg)
	if err != nil || !strings.Contains(out, `"scope": "profile"`) || strings.Contains(out, `"profiles"`) {
		t.Fatalf("profile summary = %s (%v)", out, err)
	}
}
```

Run: `go test ./cmd/monoagentcli/ -run TestSummaryAllProfiles 2>&1 | head -5`
Expected: FAIL: `unknown flag: --all-profiles`.

- [ ] **Step 7: Extract `summaryOptions` and add `--all-profiles`**

Create `cmd/monoagentcli/summary_all.go`, moving the whole `opts := summary.Options{…}` block and the automations registry lookup out of `newSummaryCmd` into it:

```go
package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/capture"
	"github.com/monoes/mono-agent/internal/capturesummary"
	"github.com/monoes/mono-agent/internal/daemonhb"
	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/profiledir"
	"github.com/monoes/mono-agent/internal/recording"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/summary"
)

// summaryOptions is what `summary` reads for one profile. It is shared by
// the profile view and by --all-profiles, which builds it once per profile.
func summaryOptions(cfg *globalConfig, db *storage.Database, profileID string, want map[string]bool) summary.Options {
	root := profiledir.Root(db.DB, profileID)
	opts := summary.Options{
		DB: db.DB, ProfileID: profileID, Now: time.Now(), Sections: want,
		Workflows:     readOnlyHybridStore(db),
		DaemonRunning: func() bool { _, live := daemonhb.Read(); return live },
		DaemonSchedules: func() map[string]time.Time {
			hb, live := daemonhb.Read()
			if !live {
				return nil
			}
			out := make(map[string]time.Time, len(hb.Schedules))
			for _, s := range hb.Schedules {
				if t, err := time.Parse(time.RFC3339, s.NextRun); err == nil {
					out[s.WorkflowID+"/"+s.NodeID] = t
				}
			}
			return out
		},
		Daemon: summaryDaemon,
		Bridge: summaryBridge,
		OrgServe: func() (bool, []string) {
			hb, live := monomind.ReadServeHeartbeat(root)
			if hb == nil || !live {
				return false, nil
			}
			return true, hb.Running
		},
		Recordings: func() ([]recording.Summary, error) {
			// profileID is already an id (initDB resolved cfg's; the
			// --all-profiles loop reads ids from the table).
			if err := recording.SetProfile(strings.TrimSpace(profileID)); err != nil {
				return nil, err
			}
			return recording.List()
		},
		Captures: func() ([]capture.Entry, error) {
			inbox, err := capture.ProfileInbox(profileID)
			if err != nil {
				return nil, err
			}
			return capture.List(inbox)
		},
		SummaryState: func(dir string) string { return capturesummary.StateOf(dir, time.Now()) },
	}
	if want == nil || want["automations"] {
		if reg, err := openAutomationRegistry(); err == nil {
			opts.Automations = summaryAutomations{reg: reg, db: db.DB}
		}
	}
	return opts
}

// sharedSections read the same whichever profile asks: the background
// services and the installed automation packages are per machine.
var sharedSections = map[string]bool{"services": true, "automations": true}

// splitSections divides the wanted sections (nil = all) into the ones built
// per profile and the ones built once.
func splitSections(want map[string]bool) (perProfile, shared map[string]bool) {
	perProfile, shared = map[string]bool{}, map[string]bool{}
	for _, n := range summary.SectionNames {
		if want != nil && !want[n] {
			continue
		}
		if sharedSections[n] {
			shared[n] = true
		} else {
			perProfile[n] = true
		}
	}
	return perProfile, shared
}

// buildGlobalSummary is `summary --all-profiles`: every profile's sections,
// merged, plus the shared ones once. The profile running the command is
// marked current.
func buildGlobalSummary(ctx context.Context, cfg *globalConfig, db *storage.Database, want map[string]bool) (summary.Summary, error) {
	profiles, err := profiledir.List(ctx, db.DB)
	if err != nil {
		return summary.Summary{}, fmt.Errorf("list profiles: %w", err)
	}
	perProfile, shared := splitSections(want)
	parts := make([]summary.ProfilePart, 0, len(profiles))
	if len(perProfile) > 0 {
		for _, p := range profiles {
			parts = append(parts, summary.ProfilePart{
				ID: p.ID, Name: p.Name,
				S: summary.Build(ctx, summaryOptions(cfg, db, p.ID, perProfile)),
			})
		}
	}
	sharedSum := summary.Summary{GeneratedAt: time.Now().UTC().Format(time.RFC3339)}
	if len(shared) > 0 {
		sharedSum = summary.Build(ctx, summaryOptions(cfg, db, cfg.ProfileID, shared))
	}
	return summary.Merge(parts, sharedSum, cfg.ProfileID), nil
}
```

In `newSummaryCmd`, add `var allProfiles bool`, and replace everything from `root := profiledir.Root(db.DB, cfg.ProfileID)` down to `s := summary.Build(cmd.Context(), opts)` with:

```go
			var s summary.Summary
			if allProfiles {
				if s, err = buildGlobalSummary(cmd.Context(), cfg, db, want); err != nil {
					return err
				}
			} else {
				s = summary.Build(cmd.Context(), summaryOptions(cfg, db, cfg.ProfileID, want))
			}
```

Add the flag next to `--section`:

```go
	cmd.Flags().BoolVar(&allProfiles, "all-profiles", false, "Roll up every profile (the dashboard's All profiles view); list rows carry profile_id and profile_name")
```

Add `  monoagentcli --json summary --all-profiles` to the command's `Example`. Remove the imports `summary.go` no longer uses, as the compiler reports them.

- [ ] **Step 8: Run tests**

Run: `export TMPDIR=/home/monoes/scratch/agent-tmp GOTMPDIR=/home/monoes/scratch/agent-tmp && go build ./... && go test ./internal/summary/ ./cmd/monoagentcli/ -run 'Summary' 2>&1 | tail -4`
Expected: both `ok`. The existing `TestSummary*` tests still pass: the profile view only gained `"scope": "profile"`.

- [ ] **Step 9: Commit**

```bash
git add internal/summary/ cmd/monoagentcli/summary.go cmd/monoagentcli/summary_all.go cmd/monoagentcli/summary_test.go
git commit -m "feat(summary): --all-profiles rolls every profile into one summary"
```

---

### Task 10: `--all-profiles` for workflow list, recent executions and org summary

**Files:**
- Create: `cmd/monoagentcli/workflow_all_profiles.go`
- Create: `cmd/monoagentcli/workflow_all_profiles_test.go`
- Modify: `cmd/monoagentcli/workflow.go` (`newWorkflowListCmd`, `newWorkflowExecutionsCmd`)
- Modify: `cmd/monoagentcli/org_summary.go` (extract `orgSummaryFor`, `printOrgSummary`; `--all-profiles`; row profile fields)
- Test: `cmd/monoagentcli/org_summary_test.go` (append)

**Interfaces:**
- Consumes: `summary.SortRecent`, `ExecRow.ProfileID/ProfileName` (Task 9).
- Produces: `workflow list --all-profiles --json` → `[{…workflow…, "profile_id", "node_count", "profile_name"}]`; `workflow executions --all --all-profiles --limit N --json` → ExecRows tagged, newest first, at most N; `org summary --all-profiles [--fast]` → rows with `profile_id`/`profile_name`, `"scope":"global"`.

- [ ] **Step 1: Write the failing tests**

Create `cmd/monoagentcli/workflow_all_profiles_test.go`:

```go
package main

import (
	"encoding/json"
	"testing"

	"github.com/monoes/mono-agent/internal/storage"
)

// twoProfileDB is newSummaryCLITestDB plus a Work profile with its own
// workflow and runs in both profiles.
func twoProfileDB(t *testing.T) *globalConfig {
	t.Helper()
	cfg := newSummaryCLITestDB(t)
	db, err := storage.NewDatabase(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec(`INSERT INTO profiles (id, name) VALUES ('p-work', 'Work');
		INSERT INTO workflows (id, name, is_active, profile_id) VALUES ('w2','B',1,'p-work');
		INSERT INTO workflow_executions (id, workflow_id, status, profile_id, created_at) VALUES
		  ('e-old','w1','SUCCESS','default','2026-09-27 10:00:00'),
		  ('e-new','w2','FAILED','p-work','2026-09-27 11:00:00'),
		  ('e-mid','w1','SUCCESS','default','2026-09-27 10:30:00')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	return cfg
}

func TestWorkflowListAllProfiles(t *testing.T) {
	cfg := twoProfileDB(t)
	var runErr error
	out := captureStdout(t, func() {
		cmd := newWorkflowListCmd(cfg)
		cmd.SetArgs([]string{"--all-profiles"})
		runErr = cmd.Execute()
	})
	if runErr != nil {
		t.Fatal(runErr)
	}
	var rows []struct {
		ID          string `json:"id"`
		ProfileID   string `json:"profile_id"`
		ProfileName string `json:"profile_name"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(rows) != 2 || rows[0].ID != "w1" || rows[0].ProfileName != "Default" || rows[1].ID != "w2" || rows[1].ProfileID != "p-work" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestRecentExecutionsAllProfiles(t *testing.T) {
	cfg := twoProfileDB(t)
	var runErr error
	out := captureStdout(t, func() {
		cmd := newWorkflowExecutionsCmd(cfg)
		cmd.SetArgs([]string{"--all", "--all-profiles", "--limit", "2"})
		runErr = cmd.Execute()
	})
	if runErr != nil {
		t.Fatal(runErr)
	}
	var rows []struct {
		ID          string `json:"id"`
		ProfileName string `json:"profile_name"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(rows) != 2 || rows[0].ID != "e-new" || rows[0].ProfileName != "Work" || rows[1].ID != "e-mid" {
		t.Fatalf("rows = %+v, want e-new (Work) then e-mid", rows)
	}
}

func TestAllProfilesNeedsAll(t *testing.T) {
	cfg := twoProfileDB(t)
	cmd := newWorkflowExecutionsCmd(cfg)
	cmd.SetArgs([]string{"w1", "--all-profiles"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("--all-profiles with a workflow id must be refused")
	}
}
```

Append to `cmd/monoagentcli/org_summary_test.go` (add `"github.com/monoes/mono-agent/internal/storage"` to its imports):

```go
func TestOrgSummaryAllProfiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dbPath := filepath.Join(t.TempDir(), "o.db")
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatal(err)
	}
	rootD, rootW := t.TempDir(), t.TempDir()
	if _, err := db.DB.Exec(`UPDATE profiles SET root_dir = ? WHERE id = 'default'`, rootD); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec(`INSERT INTO profiles (id, name, root_dir) VALUES ('p-work', 'Work', ?)`, rootW); err != nil {
		t.Fatal(err)
	}
	db.Close()
	writeOrgFile(t, rootD, "home.json", `{"name":"home"}`)
	writeOrgFile(t, rootW, "acme.json", `{"name":"acme"}`)

	cfg := &globalConfig{DBPath: dbPath, ProfileID: "default"}
	cmd := newOrgCmd(cfg)
	cmd.SetArgs([]string{"summary", "--all-profiles", "--fast"})
	var runErr error
	out := captureStdout(t, func() { runErr = cmd.Execute() })
	if runErr != nil {
		t.Fatal(runErr)
	}
	var m struct {
		Scope string `json:"scope"`
		Orgs  []struct {
			Name        string `json:"name"`
			ProfileID   string `json:"profile_id"`
			ProfileName string `json:"profile_name"`
		} `json:"orgs"`
		Totals map[string]int `json:"totals"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if m.Scope != "global" || len(m.Orgs) != 2 || m.Totals["orgs"] != 2 {
		t.Fatalf("payload = %s", out)
	}
	if m.Orgs[0].Name != "home" || m.Orgs[0].ProfileID != "default" || m.Orgs[1].Name != "acme" || m.Orgs[1].ProfileName != "Work" {
		t.Fatalf("orgs = %+v", m.Orgs)
	}
}
```

Run: `export TMPDIR=/home/monoes/scratch/agent-tmp GOTMPDIR=/home/monoes/scratch/agent-tmp && go test ./cmd/monoagentcli/ -run 'AllProfiles' 2>&1 | head -8`
Expected: FAIL: `unknown flag: --all-profiles`.

- [ ] **Step 2: Create `cmd/monoagentcli/workflow_all_profiles.go`**

```go
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/monoes/mono-agent/internal/profiledir"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/summary"
	"github.com/monoes/mono-agent/internal/workflow"
)

// The All profiles view's lists. Each runs the ordinary per-profile read
// once per profile and tags the rows, so no query learns a new scope.

// profileWorkflows is one profile's own workflows. The store's file half
// has no profile filter, so the COALESCE(profile_id,'default') rule its
// SQLite half uses is applied here.
func profileWorkflows(ctx context.Context, store *workflow.HybridWorkflowStore, profileID string) ([]workflow.Workflow, error) {
	all, err := store.ListWorkflows(ctx, profileID)
	if err != nil {
		return nil, err
	}
	out := make([]workflow.Workflow, 0, len(all))
	for _, wf := range all {
		owner := wf.ProfileID
		if owner == "" {
			owner = "default"
		}
		if owner == profileID {
			out = append(out, wf)
		}
	}
	return out, nil
}

// allProfilesWorkflow is one row of `workflow list --all-profiles --json`.
type allProfilesWorkflow struct {
	workflow.Workflow
	NodeCount   int    `json:"node_count"`
	ProfileName string `json:"profile_name"`
}

func printAllProfilesWorkflows(ctx context.Context, db *storage.Database, store *workflow.HybridWorkflowStore, asJSON bool) error {
	profiles, err := profiledir.List(ctx, db.DB)
	if err != nil {
		return fmt.Errorf("list profiles: %w", err)
	}
	rows := []allProfilesWorkflow{}
	for _, p := range profiles {
		wfs, err := profileWorkflows(ctx, store, p.ID)
		if err != nil {
			return fmt.Errorf("list workflows of %s: %w", p.Name, err)
		}
		counts, err := store.NodeCounts(ctx, p.ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not count workflow nodes of %s: %v\n", p.Name, err)
		}
		for _, wf := range wfs {
			if wf.ProfileID == "" {
				wf.ProfileID = p.ID
			}
			rows = append(rows, allProfilesWorkflow{Workflow: wf, NodeCount: counts[wf.ID], ProfileName: p.Name})
		}
	}
	if asJSON {
		return json.NewEncoder(os.Stdout).Encode(rows)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROFILE\tID\tNAME\tACTIVE\tUPDATED AT")
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%t\t%s\n", r.ProfileName, r.ID, r.Name, r.IsActive, r.UpdatedAt.Format(time.RFC3339))
	}
	return w.Flush()
}

// allProfilesRecentExecutions is `workflow executions --all --all-profiles`:
// each profile's newest runs, tagged, merged newest first, at most limit
// (limit < 0: no limit).
func allProfilesRecentExecutions(ctx context.Context, db *sql.DB, limit int) ([]summary.ExecRow, error) {
	profiles, err := profiledir.List(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("list profiles: %w", err)
	}
	rows := []summary.ExecRow{}
	for _, p := range profiles {
		got, err := summary.RecentExecutions(ctx, db, p.ID, limit)
		if err != nil {
			return nil, fmt.Errorf("executions of %s: %w", p.Name, err)
		}
		for _, r := range got {
			r.ProfileID, r.ProfileName = p.ID, p.Name
			rows = append(rows, r)
		}
	}
	return summary.SortRecent(rows, limit), nil
}
```

- [ ] **Step 3: Wire the two workflow commands**

In `newWorkflowListCmd`: add `var allProfiles bool`. Replace the block from `all, err := store.ListWorkflows(ctx, cfg.ProfileID)` through the end of the filtering loop with:

```go
			if allProfiles {
				return printAllProfilesWorkflows(ctx, db, store, jsonOut || cfg.JSONOutput)
			}
			workflows, err := profileWorkflows(ctx, store, cfg.ProfileID)
			if err != nil {
				return fmt.Errorf("list workflows: %w", err)
			}
```

and register `cmd.Flags().BoolVar(&allProfiles, "all-profiles", false, "List every profile's workflows, each tagged with its profile")`.

In `newWorkflowExecutionsCmd`: add `var allProfiles bool`. After the `if all == (len(args) == 1)` check, add:

```go
			if allProfiles && !all {
				return errInvalidInput("--all-profiles goes with --all")
			}
```

In the `if all {` branch, replace `rows, err := summary.RecentExecutions(cmd.Context(), db.DB, cfg.ProfileID, n)` with:

```go
				var rows []summary.ExecRow
				if allProfiles {
					rows, err = allProfilesRecentExecutions(cmd.Context(), db.DB, n)
				} else {
					rows, err = summary.RecentExecutions(cmd.Context(), db.DB, cfg.ProfileID, n)
				}
```

Register `cmd.Flags().BoolVar(&allProfiles, "all-profiles", false, "With --all: every profile's runs, tagged with their profile")`.

- [ ] **Step 4: Org summary across profiles**

In `org_summary.go`, add to `orgSummaryRow`:

```go
	// Set only with --all-profiles.
	ProfileID   string `json:"profile_id,omitempty"`
	ProfileName string `json:"profile_name,omitempty"`
```

Move the body of `RunE` from `names, err := orgdesign.ListOrgNames(root)` through `wg.Wait()` into a function, unchanged apart from the lines noted:

```go
// orgSummaryFor is one profile's org rows. db may be nil (dbErr says why):
// the local facts (running, queued) are still reported.
func orgSummaryFor(ctx context.Context, root string, db *storage.Database, profileID string, dbErr error, fast bool) ([]orgSummaryRow, bool, error) {
	names, err := orgdesign.ListOrgNames(root)
	if err != nil {
		return nil, false, err
	}
	hb, serveLive := monomind.ReadServeHeartbeat(root)
	// … the running map, svc, rows loop and wg.Wait() exactly as before,
	// with cmd.Context() replaced by ctx and the `db, profileID, _, dbErr :=
	// env.Profile()` line removed (they are parameters now) …
	return rows, serveLive, nil
}

// printOrgSummary writes the rows with their totals.
func printOrgSummary(rows []orgSummaryRow, fast, serveLive bool, scope string) error {
	totals := map[string]int{"orgs": len(rows), "running": 0, "queued": 0, "needs_you": 0}
	for _, r := range rows {
		if r.Running {
			totals["running"]++
		}
		totals["queued"] += r.Queued
		if r.NeedsYou != nil {
			totals["needs_you"] += *r.NeedsYou
		}
	}
	return printJSONValue(map[string]interface{}{
		"v": 1, "fast": fast, "serve_running": serveLive, "scope": scope, "orgs": rows, "totals": totals,
	})
}
```

The new `RunE`:

```go
		RunE: func(cmd *cobra.Command, _ []string) error {
			if allProfiles {
				if env.projectFlag != "" {
					return errInvalidInput("--project names one profile's org root; drop it to use --all-profiles")
				}
				db, _, _, dbErr := env.Profile()
				if dbErr != nil {
					return dbErr
				}
				profiles, err := profiledir.List(cmd.Context(), db.DB)
				if err != nil {
					return fmt.Errorf("list profiles: %w", err)
				}
				rows := []orgSummaryRow{}
				serve := false
				for _, p := range profiles {
					r, live, err := orgSummaryFor(cmd.Context(), profiledir.Root(db.DB, p.ID), db, p.ID, nil, fast)
					if err != nil {
						return fmt.Errorf("orgs of %s: %w", p.Name, err)
					}
					for i := range r {
						r[i].ProfileID, r[i].ProfileName = p.ID, p.Name
					}
					rows = append(rows, r...)
					serve = serve || live
				}
				return printOrgSummary(rows, fast, serve, "global")
			}
			root := env.Root()
			db, profileID, _, dbErr := env.Profile()
			rows, live, err := orgSummaryFor(cmd.Context(), root, db, profileID, dbErr, fast)
			if err != nil {
				return err
			}
			return printOrgSummary(rows, fast, live, "profile")
		},
```

Add `var allProfiles bool` and `c.Flags().BoolVar(&allProfiles, "all-profiles", false, "Every profile's orgs, each row tagged with its profile")`. Add imports `fmt`, `internal/profiledir`, `internal/storage`.

- [ ] **Step 5: Run tests**

Run: `export TMPDIR=/home/monoes/scratch/agent-tmp GOTMPDIR=/home/monoes/scratch/agent-tmp && go build ./... && go test ./cmd/monoagentcli/ -run 'AllProfiles|OrgSummary|WorkflowList|Executions' 2>&1 | tail -4`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
git add cmd/monoagentcli/workflow.go cmd/monoagentcli/workflow_all_profiles.go cmd/monoagentcli/workflow_all_profiles_test.go cmd/monoagentcli/org_summary.go cmd/monoagentcli/org_summary_test.go
git commit -m "feat(cli): --all-profiles for workflow list, recent runs and org summary"
```

---

### Task 11: App bindings and a scope-aware dashboard data hook

**Files:**
- Create: `wails-app/app_dashboard_scope.go`
- Create: `wails-app/app_dashboard_scope_test.go`
- Modify: `wails-app/app_workflows.go` (`WorkflowSummary`, `WorkflowExecutionSummary` gain profile fields)
- Modify: `wails-app/frontend/src/wailsjs/go/main/App.{js,d.ts}`, `wailsjs/go/models.ts` (regenerate)
- Modify: `wails-app/frontend/src/services/api.js`
- Modify: `wails-app/frontend/src/pages/dashboard/useDashboardData.js`
- Create: `wails-app/frontend/src/pages/dashboard/useDashboardData.scope.test.jsx`

**Interfaces:**
- Consumes: the four CLI `--all-profiles` forms (Tasks 9–10).
- Produces: `App.GetGlobalSummary() string`, `App.GetGlobalOrgSummary(fast bool) string`, `App.ListAllProfilesWorkflows() ([]WorkflowSummary, error)`, `App.GetAllProfilesRecentExecutions(limit int) ([]WorkflowExecutionSummary, error)`; `api.getGlobalSummary()`, `api.getGlobalOrgSummary(fast)`, `api.listAllWorkflows()`, `api.getAllRecentExecutions(limit)`; `useDashboardData({ active, scope })` with `scope` `'profile' | 'global'`.

- [ ] **Step 1: Write the failing Go test**

Create `wails-app/app_dashboard_scope_test.go`:

```go
package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDashboardGlobalBindingsShellOut(t *testing.T) {
	dir := t.TempDir()
	argsLog := filepath.Join(dir, "args.log")
	// org summary first: its argv also contains " summary --all-profiles".
	script := `#!/bin/sh
echo "$*" >> '` + argsLog + `'
case "$*" in
  *" org summary --all-profiles"*) echo '{"scope":"global","orgs":[],"totals":{"orgs":0}}';;
  *" summary --all-profiles"*) echo '{"scope":"global","profiles":[{"id":"default","name":"Default","current":true}]}';;
  *" workflow list --all-profiles"*) echo '[{"id":"w2","name":"B","is_active":true,"profile_id":"p-work","node_count":3,"profile_name":"Work"}]';;
  *" workflow executions --all --all-profiles --limit 30"*) echo '[{"id":"e1","workflow_id":"w2","status":"SUCCESS","profile_id":"p-work","profile_name":"Work"}]';;
  *) echo 'unexpected' >&2; exit 2;;
esac
`
	bin := filepath.Join(dir, "monoagentcli")
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MONOAGENTCLI_BIN", bin)

	a := newTestApp(t)
	a.ctx = context.Background()
	a.setActiveProfileID("default")

	if s := a.GetGlobalSummary(); !strings.Contains(s, `"scope":"global"`) || !strings.Contains(s, `"profiles"`) {
		t.Fatalf("GetGlobalSummary = %s", s)
	}
	if s := a.GetGlobalOrgSummary(true); !strings.Contains(s, `"orgs":[]`) {
		t.Fatalf("GetGlobalOrgSummary = %s", s)
	}
	wfs, err := a.ListAllProfilesWorkflows()
	if err != nil || len(wfs) != 1 || wfs[0].ProfileID != "p-work" || wfs[0].ProfileName != "Work" || wfs[0].NodeCount != 3 {
		t.Fatalf("ListAllProfilesWorkflows = %+v, %v", wfs, err)
	}
	ex, err := a.GetAllProfilesRecentExecutions(30)
	if err != nil || len(ex) != 1 || ex[0].ProfileName != "Work" {
		t.Fatalf("GetAllProfilesRecentExecutions = %+v, %v", ex, err)
	}
	log, _ := os.ReadFile(argsLog)
	if !strings.Contains(string(log), "org summary --all-profiles --fast") {
		t.Errorf("argv log:\n%s", log)
	}
}
```

Run: `cd wails-app && export TMPDIR=/home/monoes/scratch/agent-tmp GOTMPDIR=/home/monoes/scratch/agent-tmp && go test -run TestDashboardGlobal . 2>&1 | head -4`
Expected: `a.GetGlobalSummary undefined`.

- [ ] **Step 2: Add the profile fields and the bindings**

In `app_workflows.go`, add to both `WorkflowSummary` and `WorkflowExecutionSummary`:

```go
	// Set only in the dashboard's All profiles view.
	ProfileID   string `json:"profile_id,omitempty"`
	ProfileName string `json:"profile_name,omitempty"`
```

Create `wails-app/app_dashboard_scope.go`:

```go
package main

// The dashboard's All profiles view: the same four reads as the profile
// view (GetSummary, GetOrgSummary, ListWorkflows, GetRecentExecutions),
// each with --all-profiles. The CLI runs the per-profile code for every
// profile and tags each row with profile_id/profile_name.

import (
	"strconv"

	"github.com/monoes/mono-agent/internal/workflow"
)

// GetGlobalSummary returns `summary --all-profiles --json` verbatim.
func (a *App) GetGlobalSummary() string {
	return a.rawCLI(summaryCLITimeout, "summary", "--all-profiles")
}

// GetGlobalOrgSummary returns `org summary --all-profiles [--fast]` verbatim.
func (a *App) GetGlobalOrgSummary(fast bool) string {
	args := []string{"org", "summary", "--all-profiles"}
	if fast {
		args = append(args, "--fast")
	}
	return a.rawCLI(orgSummaryCLITimeout, args...)
}

// ListAllProfilesWorkflows is ListWorkflows across every profile.
func (a *App) ListAllProfilesWorkflows() ([]WorkflowSummary, error) {
	var rows []struct {
		workflow.Workflow
		NodeCount   int    `json:"node_count"`
		ProfileName string `json:"profile_name"`
	}
	if err := a.runMonoCLI("", &rows, "workflow", "list", "--all-profiles"); err != nil {
		return nil, err
	}
	out := make([]WorkflowSummary, 0, len(rows))
	for _, r := range rows {
		s := workflowSummaryOf(&r.Workflow)
		s.NodeCount = r.NodeCount
		s.ProfileID = r.ProfileID
		s.ProfileName = r.ProfileName
		out = append(out, s)
	}
	return out, nil
}

// GetAllProfilesRecentExecutions is GetRecentExecutions across every profile.
func (a *App) GetAllProfilesRecentExecutions(limit int) ([]WorkflowExecutionSummary, error) {
	if limit <= 0 {
		limit = 20
	}
	rows := []WorkflowExecutionSummary{}
	if err := a.cliJSON(summaryCLITimeout, &rows, "workflow", "executions", "--all", "--all-profiles", "--limit", strconv.Itoa(limit)); err != nil {
		return nil, err
	}
	return rows, nil
}
```

Run: `cd wails-app && go test -run TestDashboardGlobal .`
Expected: `ok`.

- [ ] **Step 3: Bindings and `api.js`**

Regenerate with `cd wails-app && wails generate module`, or hand-add the four functions to `App.js`/`App.d.ts` in the neighbouring entries' exact format, plus the two new fields on the `WorkflowSummary`/`WorkflowExecutionSummary` classes in `models.ts`. **Only the hunks for these methods and fields go in the commit.**

In `services/api.js`, after `getOrgSummary`:

```js
  // The dashboard's All profiles view: the same reads across every profile,
  // each row tagged with profile_id/profile_name.
  getGlobalSummary:       () => GoApp.GetGlobalSummary().then(parseCLIJSON('summary')).catch(guard('summary', null)),
  getGlobalOrgSummary:    (fast = true) => GoApp.GetGlobalOrgSummary(fast).then(parseCLIJSON('org summary')).catch(guard('org summary', null)),
  listAllWorkflows:       () => GoApp.ListAllProfilesWorkflows().catch(guard('list workflows', [])),
  getAllRecentExecutions: (limit = 20) => GoApp.GetAllProfilesRecentExecutions(limit).catch(guard('recent executions', [])),
```

- [ ] **Step 4: Write the failing hook test**

Create `wails-app/frontend/src/pages/dashboard/useDashboardData.scope.test.jsx`:

```jsx
// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import { renderHook, waitFor, cleanup } from '@testing-library/react'

vi.mock('../../services/api.js', () => ({
  api: {
    getSummary: vi.fn(() => Promise.resolve({ v: 1, scope: 'profile' })),
    getGlobalSummary: vi.fn(() => Promise.resolve({ v: 1, scope: 'global', profiles: [] })),
    getOrgSummary: vi.fn(() => Promise.resolve({ v: 1, orgs: [], totals: {} })),
    getGlobalOrgSummary: vi.fn(() => Promise.resolve({ v: 1, scope: 'global', orgs: [], totals: {} })),
    listWorkflows: vi.fn(() => Promise.resolve([{ id: 'w1' }])),
    listAllWorkflows: vi.fn(() => Promise.resolve([{ id: 'w1' }, { id: 'w2', profile_id: 'p-work' }])),
    getRecentExecutions: vi.fn(() => Promise.resolve([])),
    getAllRecentExecutions: vi.fn(() => Promise.resolve([])),
  },
  subscribeEvent: vi.fn(() => () => {}),
}))
vi.mock('../../lib/usePageVisible.js', () => ({
  usePageVisibleRef: () => ({ current: true }), useVisibleCatchUp: () => {},
}))
import { api } from '../../services/api.js'
import { useDashboardData } from './useDashboardData.js'

afterEach(() => { cleanup(); vi.clearAllMocks() })

describe('useDashboardData scope', () => {
  it('reads the global forms in the All profiles view', async () => {
    const { result } = renderHook(() => useDashboardData({ scope: 'global' }))
    await waitFor(() => expect(result.current.summary?.scope).toBe('global'))
    expect(result.current.workflows).toHaveLength(2)
    expect(api.getSummary).not.toHaveBeenCalled()
    expect(api.getGlobalOrgSummary).toHaveBeenCalledWith(false)
  })

  it('switching scope reloads everything from the other scope', async () => {
    const { result, rerender } = renderHook(({ scope }) => useDashboardData({ scope }), { initialProps: { scope: 'profile' } })
    await waitFor(() => expect(result.current.summary?.scope).toBe('profile'))
    rerender({ scope: 'global' })
    await waitFor(() => expect(result.current.summary?.scope).toBe('global'))
    expect(api.listAllWorkflows).toHaveBeenCalled()
    expect(api.getAllRecentExecutions).toHaveBeenCalledWith(30)
    rerender({ scope: 'profile' })
    await waitFor(() => expect(result.current.summary?.scope).toBe('profile'))
    expect(result.current.workflows).toHaveLength(1)
  })
})
```

Run: `cd wails-app/frontend && export TMPDIR=/home/monoes/scratch/agent-tmp && npx vitest run src/pages/dashboard/useDashboardData.scope.test.jsx 2>&1 | tail -5`
Expected: FAIL (`getSummary` is called in the global view).

- [ ] **Step 5: Make the hook scope-aware**

In `useDashboardData.js`, change the signature and add a ref right after `activeRef`:

```js
export function useDashboardData({ active = true, scope = 'profile' } = {}) {
```

```js
  // Which view: this profile's reads, or their --all-profiles forms. A
  // reply that comes back after the view changed belongs to the other view
  // and is dropped, even a full org summary.
  const globalRef = useRef(scope === 'global')
  globalRef.current = scope === 'global'
```

Replace the three loaders:

```js
  const loadSummary = useCallback(async () => {
    const n = ++seq.current.summary
    const g = globalRef.current
    const s = await (g ? api.getGlobalSummary() : api.getSummary())
    if (n !== seq.current.summary || g !== globalRef.current) return
    if (s) setSummary(s)
    setSummaryFailed(!s)
  }, [])
  const loadOrgs = useCallback(async (fast = true) => {
    const n = ++seq.current.orgs
    const g = globalRef.current
    const o = await (g ? api.getGlobalOrgSummary(fast) : api.getOrgSummary(fast))
    if (g !== globalRef.current) return
    // A full reply is never discarded (it is the only source of needs_you);
    // a fast one is, when a newer request is in flight.
    if (!o || (fast && n !== seq.current.orgs)) return
    if (!fast) haveFull.current = true
    setOrgs(prev => (fast && haveFull.current ? mergeFast(prev, o) : o))
  }, [])
  const loadLists = useCallback(async () => {
    const n = ++seq.current.lists
    const g = globalRef.current
    const [w, e] = await Promise.all(g
      ? [api.listAllWorkflows(), api.getAllRecentExecutions(30)]
      : [api.listWorkflows(), api.getRecentExecutions(30)])
    if (n !== seq.current.lists || g !== globalRef.current) return
    setWorkflows(w || [])
    setExecutions(e || [])
  }, [])
```

After the `wasActive` effect, add:

```js
  // Switching view: clear what the other view showed and read it all again.
  const firstScope = useRef(true)
  useEffect(() => {
    if (firstScope.current) { firstScope.current = false; return }
    haveFull.current = false
    setSummary(null)
    setOrgs(null)
    setLoading(true)
    refresh().then(() => loadOrgs(false))
  }, [scope, refresh, loadOrgs])
```

Run: `cd wails-app/frontend && export TMPDIR=/home/monoes/scratch/agent-tmp && npx vitest run src/pages/dashboard/ 2>&1 | tail -5`
Expected: all pass, including the existing `useDashboardData.test.jsx`.

- [ ] **Step 6: Commit**

```bash
git add wails-app/app_dashboard_scope.go wails-app/app_dashboard_scope_test.go wails-app/app_workflows.go wails-app/frontend/src/services/api.js wails-app/frontend/src/pages/dashboard/useDashboardData.js wails-app/frontend/src/pages/dashboard/useDashboardData.scope.test.jsx
git add -p wails-app/frontend/src/wailsjs/go/main/App.js wails-app/frontend/src/wailsjs/go/main/App.d.ts wails-app/frontend/src/wailsjs/go/models.ts
git commit -m "feat(app): dashboard data can read every profile"
```

---

### Task 12: The dashboard toggle and the All profiles view

**Files:**
- Create: `wails-app/frontend/src/pages/dashboard/scope.js`
- Create: `wails-app/frontend/src/pages/dashboard/scope.test.js`
- Create: `wails-app/frontend/src/pages/dashboard/profileSwitch.js`
- Create: `wails-app/frontend/src/pages/dashboard/ProfileChip.jsx`
- Create: `wails-app/frontend/src/pages/dashboard/ProfilesCard.jsx`
- Create: `wails-app/frontend/src/pages/dashboard/ProfilesCard.render.test.jsx`
- Modify: `wails-app/frontend/src/pages/Dashboard.jsx`
- Modify: `wails-app/frontend/src/pages/dashboard/{WorkflowsCard,RecentRunsCard,AccountsCard}.jsx`
- Modify: `wails-app/frontend/src/pages/dashboard/WorkflowsCard.render.test.jsx` (append)
- Modify: `wails-app/frontend/src/locales/{en,es}.json`
- Modify: `wails-app/frontend/src/index.css`

**Interfaces:**
- Consumes: `useDashboardData({ scope })`, `summary.profiles[]`, row `profile_id`/`profile_name` (Tasks 9–11), `SwitchProfile` binding (existing).
- Produces: `loadScope(storage?) → 'profile'|'global'`, `saveScope(scope, storage?)`, `ownRow(row, currentId) → bool`, `currentProfileId(summary) → string`, `switchToProfile(id, reload?)`, `<ProfileChip row>`, `<ProfilesCard summary onSwitch>`; new props `currentId`, `onSwitch` on `WorkflowsCard` and `RecentRunsCard`.

**Rules the UI follows:**
- The toggle is two buttons in the header: **This profile** | **All profiles**. The choice is remembered in localStorage (wrapped in try/catch; a blocked storage starts on "This profile").
- "This profile" is exactly today's dashboard. No chips, no Profiles card.
- "All profiles": counts are summed across profiles, every list row has a profile chip, and a **Profiles** card (one row per profile: active workflows, running, failed in 24h, waiting for you, with "current" or a **Switch** button) heads the right column.
- A row from another profile has no run/stop/toggle/open. It shows **Switch**, which switches the app to that profile the way the sidebar does (persist, then reload). Run, stop and open act on the app's active profile, so offering them for another profile's workflow would act on the wrong profile.

- [ ] **Step 1: Write the failing tests**

Create `wails-app/frontend/src/pages/dashboard/scope.test.js`:

```js
import { describe, it, expect } from 'vitest'
import { loadScope, saveScope, ownRow, currentProfileId, SCOPE_KEY } from './scope.js'

const mem = () => { const d = {}; return { d, getItem: k => d[k] ?? null, setItem: (k, v) => { d[k] = v } } }
const broken = { getItem: () => { throw new Error('blocked') }, setItem: () => { throw new Error('blocked') } }

describe('dashboard scope', () => {
  it('defaults to this profile, remembers a choice, ignores junk', () => {
    const s = mem()
    expect(loadScope(s)).toBe('profile')
    saveScope('global', s)
    expect(s.d[SCOPE_KEY]).toBe('global')
    expect(loadScope(s)).toBe('global')
    s.d[SCOPE_KEY] = 'everything'
    expect(loadScope(s)).toBe('profile')
  })
  it('survives blocked storage', () => {
    expect(loadScope(broken)).toBe('profile')
    expect(() => saveScope('global', broken)).not.toThrow()
  })
  it('a row is ours when untagged or tagged with the current profile', () => {
    expect(ownRow({ id: 'w1' }, 'default')).toBe(true)
    expect(ownRow({ profile_id: 'default' }, 'default')).toBe(true)
    expect(ownRow({ profile_id: 'p-work' }, 'default')).toBe(false)
  })
  it('finds the current profile in either view', () => {
    expect(currentProfileId({ profiles: [{ id: 'a' }, { id: 'b', current: true }] })).toBe('b')
    expect(currentProfileId({ profile_id: 'default' })).toBe('default')
    expect(currentProfileId(null)).toBe('')
  })
})
```

Create `wails-app/frontend/src/pages/dashboard/ProfilesCard.render.test.jsx`:

```jsx
// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (k, o) => (o?.name ? `${k}:${o.name}` : k) }) }))
import ProfilesCard from './ProfilesCard.jsx'

afterEach(cleanup)
const summary = { profiles: [
  { id: 'default', name: 'Default', current: true, workflows_active: 2, running: 1, failed_24h: 0, waiting_for_you: 3 },
  { id: 'p-work', name: 'Work', current: false, workflows_active: 5, running: 0, failed_24h: 2, waiting_for_you: 0 },
] }

describe('ProfilesCard', () => {
  it('lists every profile, marks the current one, and switches to another', () => {
    const onSwitch = vi.fn()
    render(<ProfilesCard summary={summary} onSwitch={onSwitch} />)
    expect(screen.getByText('Default')).toBeInTheDocument()
    expect(screen.getByText('dashboard.profiles.current')).toBeInTheDocument()
    const switches = screen.getAllByRole('button', { name: /dashboard.profiles.switch/ })
    expect(switches).toHaveLength(1)
    fireEvent.click(switches[0])
    expect(onSwitch).toHaveBeenCalledWith('p-work')
  })
  it('shows unavailable for a profile whose sections failed', () => {
    render(<ProfilesCard summary={{ profiles: [{ id: 'x', name: 'X', error: 'executions: locked' }] }} onSwitch={vi.fn()} />)
    expect(screen.getByTitle('executions: locked')).toHaveTextContent('dashboard.unavailable')
  })
})
```

Append to `WorkflowsCard.render.test.jsx` (inside the file, after the existing `describe('WorkflowsCard', …)`):

```jsx
describe('WorkflowsCard in the All profiles view', () => {
  const mixed = [
    { id: 'w1', name: 'Scraper', is_active: true, profile_id: 'default', profile_name: 'Default' },
    { id: 'w9', name: 'Outreach', is_active: true, profile_id: 'p-work', profile_name: 'Work' },
  ]
  it('another profile\'s row shows its profile and Switch instead of Run', () => {
    const onSwitch = vi.fn()
    render(<WorkflowsCard workflows={mixed} executions={[]} schedules={null} currentId="default" onSwitch={onSwitch} {...noop} />)
    expect(screen.getByText('Work')).toBeInTheDocument()
    expect(screen.getAllByText('dashboard.workflows.run')).toHaveLength(1)
    fireEvent.click(screen.getByRole('button', { name: 'dashboard.profiles.switch' }))
    expect(onSwitch).toHaveBeenCalledWith('p-work')
  })
  it('recent runs from another profile switch instead of opening', () => {
    const onSwitch = vi.fn()
    const onNavigate = vi.fn()
    render(<RecentRunsCard currentId="default" onSwitch={onSwitch} onNavigate={onNavigate}
      executions={[{ id: 'e9', workflow_id: 'w9', workflow_name: 'Outreach', status: 'FAILED', profile_id: 'p-work', profile_name: 'Work', created_at: new Date().toISOString() }]} />)
    fireEvent.click(screen.getByText('Outreach'))
    expect(onSwitch).toHaveBeenCalledWith('p-work')
    expect(onNavigate).not.toHaveBeenCalled()
  })
})
```

Run: `cd wails-app/frontend && export TMPDIR=/home/monoes/scratch/agent-tmp && npx vitest run src/pages/dashboard/ 2>&1 | tail -6`
Expected: FAIL: `Failed to resolve import "./scope.js"`.

- [ ] **Step 2: Create the helpers and components**

`scope.js`:

```js
// Which dashboard the user last looked at: this profile's, or every
// profile's. Remembered per window; never required, since a blocked
// storage just starts on "profile".
export const SCOPE_KEY = 'monoagent.dashboard.scope'
export const SCOPES = ['profile', 'global']

export function loadScope(storage = globalThis.localStorage) {
  try {
    const v = storage?.getItem(SCOPE_KEY)
    return SCOPES.includes(v) ? v : 'profile'
  } catch {
    return 'profile'
  }
}

export function saveScope(scope, storage = globalThis.localStorage) {
  try { storage?.setItem(SCOPE_KEY, scope) } catch { /* the toggle still works until the window closes */ }
}

// ownRow: does this row belong to the profile the app is on? Rows in the
// profile view carry no profile_id and are always its own.
export function ownRow(row, currentId) {
  return !row?.profile_id || !currentId || row.profile_id === currentId
}

export function currentProfileId(summary) {
  return summary?.profiles?.find(p => p.current)?.id || summary?.profile_id || ''
}
```

`profileSwitch.js`:

```js
import { SwitchProfile } from '../../wailsjs/go/main/App'

// Another profile's rows are read-only on the dashboard: run, stop and open
// act on the app's active profile. Switching works like the sidebar's:
// persist the choice, then reload so every page re-reads its data.
export async function switchToProfile(id, reload = () => window.location.reload()) {
  await SwitchProfile(id)
  reload()
}
```

`ProfileChip.jsx`:

```jsx
import { useTranslation } from 'react-i18next'

// The profile a row belongs to. Only rows from the All profiles view carry
// one, so the profile view never shows a chip.
export default function ProfileChip({ row }) {
  const { t } = useTranslation()
  if (!row?.profile_id) return null
  const name = row.profile_name || row.profile_id
  return <span className="dash-chip dash-chip-profile" title={t('dashboard.profiles.chipTitle', { name })}>{name}</span>
}
```

`ProfilesCard.jsx`:

```jsx
import { useTranslation } from 'react-i18next'
import { Users } from 'lucide-react'
import { switchToProfile } from './profileSwitch.js'

// One row per profile in the All profiles view: what each is doing, and a
// way to go there.
export default function ProfilesCard({ summary, onSwitch = switchToProfile }) {
  const { t } = useTranslation()
  const profiles = summary?.profiles || []
  return (
    <div className="card" data-testid="dash-profiles">
      <div className="section-header">
        <div className="section-title"><Users size={12} /> {t('dashboard.profiles.title')}</div>
      </div>
      {profiles.length === 0 ? <div className="dash-empty">…</div> : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          {profiles.map(p => (
            <div key={p.id} className="dash-account" data-profile={p.id}>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div className="dash-ellipsis" style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--text)' }}>
                  {p.name}
                  {p.current && <span className="dash-chip" style={{ marginLeft: 6 }}>{t('dashboard.profiles.current')}</span>}
                </div>
                <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: p.error ? '#ef4444' : 'var(--text-muted)' }} title={p.error || undefined}>
                  {p.error ? t('dashboard.unavailable') : t('dashboard.profiles.line', {
                    active: p.workflows_active || 0, running: p.running || 0, failed: p.failed_24h || 0, waiting: p.waiting_for_you || 0,
                  })}
                </div>
              </div>
              {!p.current && (
                <button className="btn btn-ghost btn-sm" onClick={() => onSwitch(p.id)} title={t('dashboard.profiles.switchTo', { name: p.name })}>
                  {t('dashboard.profiles.switch')}
                </button>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
```

(The ProfilesCard test's `t` mock appends `:name` when a `name` option is passed, so the Switch button's accessible name is `dashboard.profiles.switch`. Its `title` is not part of the name. The test's `/dashboard.profiles.switch/` regex matches it.)

- [ ] **Step 3: Cards take `currentId` and `onSwitch`**

`WorkflowsCard.jsx`:
- imports: `import ProfileChip from './ProfileChip.jsx'`, `import { ownRow } from './scope.js'`, `import { switchToProfile } from './profileSwitch.js'`.
- `WorkflowRow` gains props `own` and `onSwitch`:
  - toggle button: `onClick={own ? handleToggle : undefined}` and `disabled={toggling || !own}`;
  - inside `.wf-name`, after the description block: `<ProfileChip row={wf} />`;
  - replace the `{st.live ? (…stop…) : (…run…)}` expression with:

```jsx
      {!own ? (
        <button className="btn btn-ghost btn-sm" onClick={() => onSwitch(wf.profile_id)}
          title={t('dashboard.profiles.switchTo', { name: wf.profile_name || wf.profile_id })}>
          {t('dashboard.profiles.switch')}
        </button>
      ) : st.live ? (
        <button className="btn btn-sm dash-stop-btn" onClick={handleStop} disabled={stopping} title={t('dashboard.workflows.stopTitle')}>
          {stopping ? <Loader size={11} style={{ animation: 'spin 1s linear infinite' }} /> : <StopCircle size={11} />}
          {t('dashboard.workflows.stop')}
        </button>
      ) : (
        <button className="btn btn-secondary btn-sm" onClick={handleRun} disabled={running} style={{ gap: 4, minWidth: 60, flexShrink: 0 }}>
          {running ? <Loader size={11} style={{ animation: 'spin 1s linear infinite' }} /> : <Play size={11} />}
          {running ? t('dashboard.workflows.starting') : t('dashboard.workflows.run')}
        </button>
      )}
```

  - wrap the open-editor chevron button in `{own && ( … )}`.
- `export default function WorkflowsCard({ workflows, executions, schedules, onRun, onStop, onToggle, onNavigate, currentId = '', onSwitch = switchToProfile })`. Pass `own={ownRow(wf, currentId)} onSwitch={onSwitch}` to each `WorkflowRow`.

`RecentRunsCard.jsx`:
- same three imports.
- `ExecRow` gains `currentId`, `onSwitch`: `const own = ownRow(exec, currentId)`, `const open = own ? () => onNavigate('noderunner', { executionId: exec.id, workflowId: exec.workflow_id }) : () => onSwitch(exec.profile_id)`. Its button `title` becomes `own ? t('dashboard.recentRuns.openTitle') : t('dashboard.profiles.switchTo', { name: exec.profile_name || exec.profile_id })`. After the workflow-name span, add `<ProfileChip row={exec} />`.
- `export default function RecentRunsCard({ executions, onNavigate, currentId = '', onSwitch = switchToProfile })` passes both to `ExecRow`.

`AccountsCard.jsx`: `import ProfileChip from './ProfileChip.jsx'`, and render `<ProfileChip row={s} />` just before the platform `badge`.

- [ ] **Step 4: The toggle on the page**

`Dashboard.jsx`:

```jsx
import { loadScope, saveScope, currentProfileId } from './dashboard/scope.js'
import ProfilesCard from './dashboard/ProfilesCard.jsx'
```

Inside the component, before `useDashboardData`:

```jsx
  const [scope, setScopeState] = useState(loadScope)
  const setScope = (s) => { saveScope(s); setScopeState(s) }
```

Change the hook call to `useDashboardData({ active: isActive, scope })`, and add `const currentId = currentProfileId(summary)` after it.

In `page-header-right`, before the refresh button:

```jsx
          <div className="dash-scope" role="group" aria-label={t('dashboard.scope.label')}>
            <button className={`btn btn-sm ${scope === 'profile' ? 'btn-secondary' : 'btn-ghost'}`}
              aria-pressed={scope === 'profile'} onClick={() => setScope('profile')}>
              {t('dashboard.scope.profile')}
            </button>
            <button className={`btn btn-sm ${scope === 'global' ? 'btn-secondary' : 'btn-ghost'}`}
              aria-pressed={scope === 'global'} onClick={() => setScope('global')}>
              {t('dashboard.scope.global')}
            </button>
          </div>
```

Pass `currentId={currentId}` to `WorkflowsCard` and `RecentRunsCard`. Make `{scope === 'global' && <ProfilesCard summary={summary} />}` the first child of the right `dash-col`.

`index.css` (next to the other `.dash-chip` rules):

```css
.dash-chip-profile { color: var(--purple-light); }
.dash-scope { display: inline-flex; gap: 2px; margin-right: 6px; }
```

- [ ] **Step 5: Strings in both locales**

Run from `wails-app/frontend`:

```bash
python3 - <<'EOF'
import json
add = {
  "en": {"scope": {"label": "Show", "profile": "This profile", "global": "All profiles"},
         "profiles": {"title": "Profiles", "current": "current", "switch": "Switch",
                      "switchTo": "Switch to {{name}}", "chipTitle": "From profile {{name}}",
                      "line": "{{active}} active · {{running}} running · {{failed}} failed (24h) · {{waiting}} waiting for you"}},
  "es": {"scope": {"label": "Mostrar", "profile": "Este perfil", "global": "Todos los perfiles"},
         "profiles": {"title": "Perfiles", "current": "actual", "switch": "Cambiar",
                      "switchTo": "Cambiar a {{name}}", "chipTitle": "Del perfil {{name}}",
                      "line": "{{active}} activos · {{running}} en curso · {{failed}} fallidos (24 h) · {{waiting}} esperándote"}},
}
for lang, keys in add.items():
    path = f"src/locales/{lang}.json"
    d = json.load(open(path))
    d["dashboard"].update(keys)
    with open(path, "w") as f:
        json.dump(d, f, ensure_ascii=False, indent=2)
        f.write("\n")
EOF
git diff --stat src/locales/
```

Expected: only the new keys added. If the diff shows whole-file reformatting (different indent in the original), revert and insert the two blocks by hand instead.

- [ ] **Step 6: Run the frontend suite**

Run: `cd wails-app/frontend && export TMPDIR=/home/monoes/scratch/agent-tmp && npx vitest run 2>&1 | tail -6`
Expected: all pass. That includes `locales/dashboardKeys.test.js`, which checks that every new `dashboard.*` key used in these files exists in both `en` and `es`.

- [ ] **Step 7: Commit**

```bash
git add wails-app/frontend/src/pages/Dashboard.jsx wails-app/frontend/src/pages/dashboard/ wails-app/frontend/src/locales/en.json wails-app/frontend/src/locales/es.json wails-app/frontend/src/index.css
git commit -m "feat(app): dashboard toggle between this profile and all profiles"
```

---

### Task 13: Documentation

**Files:**
- Create: `docs/BROWSER_PROFILES.md`
- Modify: `README.md` (Chrome extension section, around line 571–586)
- Modify: `docs/security/threat-model.md`
- Modify: `CHANGELOG.md` (`## [Unreleased]`)

- [ ] **Step 1: Create `docs/BROWSER_PROFILES.md`**

```markdown
# One browser per profile

Every browser profile with the MonoAgent Bridge extension installed (a Chrome
profile, an Edge profile, a separate browser) connects to the bridge on its
own. Bind each one to a monoagent profile and that profile's browser actions
(workflow browser nodes, `capture page`, `login`, `node run`, `application
apply`) run there, signed in as that browser's accounts.

Two profiles bound to two browsers run at the same time, including two
accounts on the same site.

## Set it up

1. Install the extension in each browser profile (`chrome://extensions` →
   Load unpacked → `chrome-extension/`) and pair it: paste the output of
   `monoagentcli extension pair` into the side panel's Connection settings.
2. In each browser's side panel, open **Connection settings → Automations in
   this browser** and choose the profile. Give the browser a name you'll
   recognise.
3. Check: `monoagentcli extension browsers`.

Or from a terminal: `monoagentcli extension bind "<browser name or id>" <profile>`.
Or in the desktop app: **Settings → Browsers**.

## Which browser runs what

For a profile P, the bridge picks:

1. the most recently connected browser bound to P;
2. otherwise a browser bound to no profile (a *default browser*);
3. never a browser bound to a different profile. If that's all there is, the run
   fails with "no browser is set up for profile P" and the fix.

A run stays in the browser it started in, even if you rebind that browser
mid-run.

Binding a browser also makes its profile the default for captures saved from
that browser.

## Upgrading

Old extensions (before 1.5.0) keep working as a single default browser. Old
bridges ignore bindings until restarted. After updating, restart the daemon
or `monoagentcli extension serve`, and reload the extension in every browser
profile.
```

- [ ] **Step 2: README**

In the Chrome extension section, change the bullet `- **Shared connection** — multiple CLI processes share one extension connection instead of fighting over the browser` to:

```markdown
- **Shared connection** — multiple CLI processes share the bridge instead of fighting over the browser
- **One browser per profile** — every browser profile with the extension connects on its own; bind each to a monoagent profile and that profile's automations run there, in parallel with the others. See [docs/BROWSER_PROFILES.md](docs/BROWSER_PROFILES.md)
```

- [ ] **Step 3: Threat model**

Add a paragraph to the bridge section of `docs/security/threat-model.md`:

```markdown
**Multiple browsers.** The bridge keeps one authenticated socket per browser
profile, keyed by an instance id the extension reports. All of them
authenticate with the same pairing token, so a token holder can already
drive every paired browser; binding adds routing, not a new capability.
`/monoagent/health` (unauthenticated) reports only a browser count; which
profile and label each browser has is behind the token at
`/monoagent/browsers` and `/monoagent/resolve`. A reported profile id that
`profiledir.ValidProfileID` rejects is dropped. A command for a profile is
never sent to a browser bound to a different profile.
```

- [ ] **Step 4: CHANGELOG**

Under `## [Unreleased]`:

```markdown
### Added
- One browser per profile: bind each browser profile's MonoAgent Bridge extension to a monoagent profile (side panel, `monoagentcli extension bind`, or Settings → Browsers). That profile's browser actions run there, so two profiles can run in parallel, even on the same site. `monoagentcli extension browsers` lists them. Extension 1.5.0.
- Dashboard: a **This profile / All profiles** toggle. All profiles sums every profile's counts, labels each workflow, run and account with its profile, and adds a Profiles card with a Switch button per profile. The CLI side is `--all-profiles` on `summary`, `workflow list`, `workflow executions --all` and `org summary`.
```

- [ ] **Step 4b: README dashboard line**

In the README's desktop-app section, where the dashboard is described, add: "Toggle **All profiles** in the dashboard header to see every profile at once. Rows are labelled with their profile, and **Switch** takes you to it."

- [ ] **Step 5: Commit**

```bash
git add docs/BROWSER_PROFILES.md README.md docs/security/threat-model.md CHANGELOG.md
git commit -m "docs: one browser per profile"
```

---

### Task 14: End-to-end verification with two real browsers

**Files:**
- Create: `scripts/e2e/set-extension-storage.mjs`
- Create: `scripts/e2e/per-profile-browsers.sh`

This task must pass before the PR is opened. It runs two real Chromium profiles against a private bridge (port 9232, scratch HOME). The user's Edge and `monoagent-bridge.service` are never touched.

- [ ] **Step 1: Create `scripts/e2e/set-extension-storage.mjs`**

```js
// Writes keys into the MonoAgent Bridge extension's chrome.storage.local in
// a browser started with --remote-debugging-port, through the extension's
// service worker. Usage: node set-extension-storage.mjs <debugPort> '<json>'
const [port, json] = process.argv.slice(2);
const values = JSON.parse(json);

async function workerTarget() {
  for (let i = 0; i < 50; i++) {
    const list = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
    const sw = list.find((t) => t.type === "service_worker" && /^chrome-extension:\/\/.+\/background\.js$/.test(t.url));
    if (sw) return sw;
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error(`no extension service worker on port ${port}`);
}

const target = await workerTarget();
const ws = new WebSocket(target.webSocketDebuggerUrl);
await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
const reply = new Promise((resolve) => {
  ws.onmessage = (ev) => {
    const msg = JSON.parse(ev.data);
    if (msg.id === 1) resolve(msg);
  };
});
ws.send(JSON.stringify({
  id: 1,
  method: "Runtime.evaluate",
  params: { expression: `chrome.storage.local.set(${JSON.stringify(values)}).then(() => "ok")`, awaitPromise: true },
}));
const msg = await reply;
ws.close();
if (msg.result?.result?.value !== "ok") {
  console.error(JSON.stringify(msg));
  process.exit(1);
}
```

- [ ] **Step 2: Create `scripts/e2e/per-profile-browsers.sh`**

```bash
#!/usr/bin/env bash
# Two real Chromium profiles, two monoagent profiles, one private bridge.
# Proves: each profile's command lands in its own browser, both at once, and
# a profile with no browser gets the explanatory error. Never touches the
# real HOME, port 9222, or the user's browsers.
set -euo pipefail

REPO="$(cd "$(dirname "$0")/../.." && pwd)"
W="${W:-$HOME/scratch/per-profile-e2e}"
H="$W/home"
PORT=9232
CHROMIUM="${CHROMIUM:-chromium}"
export TMPDIR="$HOME/scratch/agent-tmp" GOTMPDIR="$HOME/scratch/agent-tmp"
mkdir -p "$W" "$H" "$W/site" "$TMPDIR"

CLI="$W/monoagentcli"
(cd "$REPO" && go build -o "$CLI" ./cmd/monoagentcli)
mc() { HOME="$H" MONOAGENT_EXTENSION_PORT="$PORT" "$CLI" "$@"; }

cleanup() {
  for f in "$W"/*.pid; do [ -f "$f" ] && kill "$(cat "$f")" 2>/dev/null || true; done
}
trap cleanup EXIT

# Two profiles, plus one with no browser at all.
WORK=$(mc --json profile create Work | jq -r .id)
HOMEP=$(mc --json profile create Personal | jq -r .id)
SOLO=$(mc --json profile create Solo | jq -r .id)

# A page per browser, so a capture says which browser it came from.
echo '<title>work page</title><h1>work</h1>' > "$W/site/work.html"
echo '<title>personal page</title><h1>personal</h1>' > "$W/site/personal.html"
python3 -m http.server 9311 --bind 127.0.0.1 --directory "$W/site" >/dev/null 2>&1 & echo $! > "$W/site.pid"

mc extension serve >"$W/bridge.log" 2>&1 & echo $! > "$W/bridge.pid"
for _ in $(seq 50); do curl -sf "http://127.0.0.1:$PORT/monoagent/health" >/dev/null && break; sleep 0.2; done
TOKEN=$(cat "$H/.monoagent/extension.token")

launch() { # name debugPort url
  "$CHROMIUM" --user-data-dir="$W/ud-$1" --no-first-run --no-default-browser-check \
    --load-extension="$REPO/chrome-extension" --remote-debugging-port="$2" --remote-allow-origins='*' "$3" \
    >"$W/$1.log" 2>&1 & echo $! > "$W/$1.pid"
}
launch work 9301 "http://127.0.0.1:9311/work.html"
launch personal 9302 "http://127.0.0.1:9311/personal.html"

node "$REPO/scripts/e2e/set-extension-storage.mjs" 9301 \
  "{\"wsUrl\":\"ws://127.0.0.1:$PORT/monoagent\",\"pairingToken\":\"$TOKEN\",\"boundProfile\":\"$WORK\",\"browserLabel\":\"E2E Work\"}"
node "$REPO/scripts/e2e/set-extension-storage.mjs" 9302 \
  "{\"wsUrl\":\"ws://127.0.0.1:$PORT/monoagent\",\"pairingToken\":\"$TOKEN\",\"boundProfile\":\"$HOMEP\",\"browserLabel\":\"E2E Personal\"}"

# Both browsers attached and bound.
for _ in $(seq 100); do
  n=$(mc --json extension browsers | jq '[.browsers[] | select(.profile_id != "")] | length')
  [ "$n" = 2 ] && break; sleep 0.3
done
mc extension browsers
[ "$n" = 2 ] || { echo "FAIL: browsers did not both bind"; exit 1; }

# 1. Parallel: each profile captures its own browser's page, at the same time.
mc --profile Work --json capture page --out "$W/inbox-work" >"$W/cap-work.json" & A=$!
mc --profile Personal --json capture page --out "$W/inbox-personal" >"$W/cap-personal.json" & B=$!
wait $A; wait $B
grep -q work.html <(jq -r .meta.url "$W/cap-work.json") || { echo "FAIL: Work captured $(jq -r .meta.url "$W/cap-work.json")"; exit 1; }
grep -q personal.html <(jq -r .meta.url "$W/cap-personal.json") || { echo "FAIL: Personal captured $(jq -r .meta.url "$W/cap-personal.json")"; exit 1; }
echo "PASS: each profile captured in its own browser, in parallel"

# 2. A profile with no browser and no default browser gets the fix, not a random browser.
if out=$(mc --profile Solo capture page --timeout 5s 2>&1); then
  echo "FAIL: Solo captured with no browser of its own: $out"; exit 1
fi
grep -q "no browser is set up for profile" <<<"$out" || { echo "FAIL: unexpected error: $out"; exit 1; }
echo "PASS: Solo gets the no-browser explanation"

# 3. Rebind from the CLI: Personal's browser becomes a default browser, and Solo can use it.
mc extension unbind "E2E Personal"
mc --profile Solo --json capture page --out "$W/inbox-solo" >"$W/cap-solo.json"
grep -q personal.html <(jq -r .meta.url "$W/cap-solo.json") || { echo "FAIL: Solo did not use the default browser"; exit 1; }
echo "PASS: unbind makes a default browser that unbound profiles use"
echo "ALL PASS"
```

Check the `capture page` flags before the first run (`--out`, `--timeout`) against `mc capture page --help`. Adjust the script if a flag has a different name.

- [ ] **Step 3: Run it**

Run: `chmod +x scripts/e2e/per-profile-browsers.sh && scripts/e2e/per-profile-browsers.sh 2>&1 | tail -12`
Expected: the browsers table with `E2E Work → Work` and `E2E Personal → Personal`, then three `PASS` lines and `ALL PASS`.

- [ ] **Step 4: Verify the side panel and the app for real**

- Side panel: in the `work` Chromium, open the real side panel (CDP `Runtime.evaluate` with `userGesture: true` → `chrome.sidePanel.open({windowId})`, as in `~/scratch/side-panel/*.mjs`). Confirm **Automations in this browser** shows *Work*. Switch it to *Any profile* and Save. `mc extension browsers` must show that browser with `any profile` within a second, and the socket must **not** reconnect (the bridge log shows no second "extension connected" for that instance).
- App: run `cd wails-app && HOME=$HOME/scratch/per-profile-e2e/home MONOAGENT_EXTENSION_PORT=9232 wails dev` and open the served UI in a browser. Settings → Browsers must list both browsers. Changing a select must rebind (check with `mc extension browsers`). Take a screenshot as evidence.
- Dashboard, in the same `wails dev` session (the scratch HOME has Work, Personal and Solo; give Work and Personal one workflow each with `mc --profile Work workflow create …`, and run one of them once):
  - `mc --json summary --all-profiles | jq '.scope, (.profiles|length), .workflows.total'` prints `"global"`, `4` (Default + the three), and the sum of the workflows.
  - In the UI, **This profile** looks exactly as before: no chips, no Profiles card.
  - **All profiles** shows the Profiles card with the active one marked "current". Workflows and recent runs from other profiles carry their profile chip and a **Switch** button, and have no Run/Stop/toggle.
  - Clicking **Switch** on a Work row reloads the app on Work, with the toggle still on All profiles and Work now marked "current".
  - Reload the window: the toggle choice is kept.
  - Take screenshots of both views as evidence.

- [ ] **Step 5: Full suite, then commit**

Run: `export TMPDIR=/home/monoes/scratch/agent-tmp GOTMPDIR=/home/monoes/scratch/agent-tmp && go build ./... && go build -tags nosocial ./cmd/monoagentcli && go vet ./... && gofmt -l . && go test ./... 2>&1 | grep -v '^ok' | head -20; node --test 'chrome-extension/**/*.test.mjs' 2>&1 | tail -3; (cd wails-app && go test ./... 2>&1 | tail -2); (cd wails-app/frontend && npx vitest run 2>&1 | tail -4)`
Expected: no `FAIL` lines, `gofmt -l` prints nothing, `# fail 0`, vitest all passed.

```bash
git add scripts/e2e/
git commit -m "test(e2e): two real browsers, two profiles, in parallel"
```

- [ ] **Step 6: Open one PR and roll out after merge**

- `git push -u origin feat/per-profile-browsers` and `gh pr create --title "feat: one browser per profile" --body "<summary + the e2e output>"`. No Claude attribution lines.
- After merge and release: update all three installed binaries (`~/.local/bin/monoagentcli`, `~/.local/share/monoagent/MonoAgent`, `~/.local/share/monoagent/monoagentcli`), then `systemctl --user restart monoagent-bridge monoagent-daemon`. Update the main checkout (the user's Edge loads `chrome-extension/` from it; check for unpushed work first) and reload the extension in Edge. Confirm that `/monoagent/health` reports `"browsers": 1` and that the Edge side panel shows the new field.

---

## Self-review

**Spec coverage**
- "Match profile of browser with profile of extension and app": the binding is stored per browser profile (Task 7), reported to the bridge (Tasks 1–2), and shown and changed from the app (Task 8) and the CLI (Task 6).
- "Hard set a profile of monoagent in each browser profile": the side panel field (Task 7), `extension bind` (Task 6), Settings → Browsers (Task 8).
- "It uses that specific profile for the work": engine runs route by the execution's profile (Task 5, lazy provider). Direct CLI commands route by `--profile`/active (Task 5, eight call sites). Relay, capture and CDP carry the target (Task 3). Runs pin to one browser (Task 4).
- "Make sure everything works": 30+ unit tests across Go, JS and React. Four-way compatibility is tested (legacy extension: Task 1; old bridge: Tasks 3–4; old CLI: untargeted relay = default browser, Task 1 resolve rules; new extension + old bridge: extra auth fields ignored). The two-browser parallel e2e is Task 14, and real side-panel/app checks are Task 14 step 4.

- "Dashboard toggle: global shows everything from every profile, profile shows just like now": the four dashboard reads gain `--all-profiles` (Tasks 9–10), with a sum/tag merge tested per section. The app bindings and data hook switch between the two forms, and a reply for the other view is dropped (Task 11). The header toggle is remembered, rows are labelled, there's a Profiles card, and other profiles' rows are read-only with Switch (Task 12). The profile view is unchanged apart from a `"scope"` field. Existing dashboard tests keep passing, and Task 14 checks it in the real app.

**Gaps deliberately left out (follow-ups, not blockers)**
- In All profiles, the attention strip's links (approvals, drafts, …) open the pages of the **active** profile, and those pages are still profile-scoped. The counts include every profile. A global HIL inbox would be its own change.
- Run/stop/toggle on another profile's workflow needs a switch first. Acting on it in place would need profile-explicit app bindings (`--profile <other>` per call); that's possible later, but it's not needed for an overview.
- If the bound browser is closed, `ensureExtensionConnected` launches the *default* Chrome, not that browser profile. The error names the fix. Auto-launching a specific `--profile-directory` would need a stored launch hint.
- There is no check that the account logged into a browser matches a node's `username`. That behaviour is unchanged from today.
- `doc.lookup`/`doc.ask` from a bound browser still search whatever profile they're asked for. `profile.list` now defaults to the bound profile (Task 2), which covers captures.

**Type consistency check.** `Target{Profile, Instance}`, `ConnInfo`, `NoBrowserError{Profile, BoundTo}`, `ErrNoExtension`, `ErrBrowserGone`, `ErrBridgeTooOld`, `SendCommandTo(t, cmd, timeout)`, `CreateTabFor(t, url)`, `CloseTabFor(t, tabID)`, `ResolveTarget(t)`, `Browsers()`, `SetBinding(instance, profile, label)`, `ForProfile(profileID)`, `ProfileBridge.Route()`, `CmdSetBinding`, `KindBinding`, and JSON keys `instance/label/profile_id/profile_name/legacy/conflict/version/connected_at` are used with the same names and signatures in every task that references them.
