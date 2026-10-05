package mcp

// The fixture of the tests of the settings tools (api_status, api_config_get/set/apply). They read
// and change what is outside the database too: the daemon's heartbeat, the OS service manager and
// the listeners' HTTP answers. Each of those is a fake here, handed to the server in Options.APIEnv,
// so that no test runs launchctl, systemctl or schtasks or depends on the machine it runs on.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/monoes/mono-agent/internal/apiconfig"
	"github.com/monoes/mono-agent/internal/autostart"
	"github.com/monoes/mono-agent/internal/daemonhb"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/testdb"
)

// fakeInstaller is the service manager of these tests: it records what it was asked and runs
// nothing. Install, Uninstall and Start are never what these tools do, so they are recorded too and
// a test can say that none happened.
type fakeInstaller struct {
	mu         sync.Mutex
	registered bool
	restartErr error
	calls      []string
}

func (f *fakeInstaller) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeInstaller) Install(context.Context) (autostart.Result, error) {
	f.record("install")
	return autostart.Result{}, nil
}
func (f *fakeInstaller) Uninstall(context.Context) error { f.record("uninstall"); return nil }
func (f *fakeInstaller) Start(context.Context) error     { f.record("start"); return nil }
func (f *fakeInstaller) Status(context.Context) (bool, string) {
	f.record("status")
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.registered, "/x/monoagent-daemon.service"
}
func (f *fakeInstaller) Restart(context.Context) error {
	f.record("restart")
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.restartErr
}

// count is how many times the service manager was asked for one thing.
func (f *fakeInstaller) count(call string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == call {
			n++
		}
	}
	return n
}

// everything is every call the service manager got, in order.
func (f *fakeInstaller) everything() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.calls, ",")
}

// configSetup says how a fixture differs from the usual one: a server of the default profile with
// mutations allowed, the exposure gate closed and no service registered.
type configSetup struct {
	work          bool // a server of the work profile (id p-7f3a9c), whose id is neither "default" nor its name
	readOnly      bool // without --allow-mutations
	allowExposure bool // with --allow-api-exposure
	registered    bool // the daemon is registered for auto-start
	apiOnly       bool // with --api-only
}

// configFixture is a server of the API tools over a database of its own.
type configFixture struct {
	t         *testing.T
	Server    *Server
	DBPath    string
	Side      *storage.Database // a second handle on the server's database
	Installer *fakeInstaller

	mu     sync.Mutex
	hb     *daemonhb.Heartbeat
	probes map[string][2]bool // base URL -> reachable, answers /v1
	probed []string
}

// clearAPIConfigEnv clears everything the settings tools read of the environment, so that a test
// sets exactly what it means to, and the real heartbeat of this machine is never read.
func clearAPIConfigEnv(t *testing.T) {
	t.Helper()
	for _, sp := range apiconfig.Specs() {
		t.Setenv(sp.Env, "")
	}
	for _, v := range []string{"MONOAGENT_HTTPAPI_ADDR", "MONOAGENT_MCP_ALLOW_MUTATIONS", "MONOAGENT_MCP_ALLOW_API_EXPOSURE", "TYPESAFE_API_KEY"} {
		t.Setenv(v, "")
	}
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "no-daemon.json"))
}

func newConfigFixture(t *testing.T, cs configSetup) *configFixture {
	t.Helper()
	clearAPIConfigEnv(t)
	dbPath := testdb.Path(t)
	side := sideDB(t, dbPath)
	profile := "default"
	if cs.work {
		if _, err := side.DB.Exec(`INSERT INTO profiles (id, name) VALUES (?, ?)`, workProfileID, workProfileName); err != nil {
			t.Fatal(err)
		}
		profile = workProfileName
	}
	f := &configFixture{t: t, DBPath: dbPath, Side: side, Installer: &fakeInstaller{registered: cs.registered}, probes: map[string][2]bool{}}
	f.Server = NewServer(Options{
		DBPath:           dbPath,
		Profile:          profile,
		WorkflowsDir:     filepath.Join(t.TempDir(), "workflows"),
		Version:          "test",
		AllowMutations:   !cs.readOnly,
		AllowAPIExposure: cs.allowExposure,
		APIOnly:          cs.apiOnly,
		APIEnv:           apiconfig.Env{Installer: f.Installer, Heartbeat: f.heartbeat, Probe: f.probe},
	})
	t.Cleanup(f.Server.closeRuntime)
	return f
}

func (f *configFixture) heartbeat() (daemonhb.Heartbeat, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hb == nil {
		return daemonhb.Heartbeat{}, false
	}
	return *f.hb, true
}

// daemon makes a daemon live, with the heartbeat given (the pid and the time are filled in).
func (f *configFixture) daemon(hb daemonhb.Heartbeat) {
	f.mu.Lock()
	defer f.mu.Unlock()
	hb.PID = os.Getpid()
	f.hb = &hb
}

func (f *configFixture) probe(base string, wantV1 bool) (reachable, v1Answers bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probed = append(f.probed, base)
	p := f.probes[base]
	return p[0], p[1] && wantV1
}

// answers makes the listener at a base URL (http://127.0.0.1:9322) answer /health, and /v1 when
// v1 is true.
func (f *configFixture) answers(base string, v1 bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probes[base] = [2]bool{true, v1}
}

// call runs a tool of the server and returns its text.
func (f *configFixture) call(name string, args map[string]any) (string, error) {
	f.t.Helper()
	return callAPITool(f.t, f.Server, name, args)
}

func (f *configFixture) mustCall(name string, args map[string]any) string {
	f.t.Helper()
	return mustCall(f.t, f.Server, name, args)
}

// save saves settings the way `api config set` does, without checking them.
func (f *configFixture) save(words ...string) { f.t.Helper(); saveSettings(f.t, f.Side, words...) }

// clear removes everything that is saved, as a person would with the command (the tool would
// have to be allowed to widen to do it).
func (f *configFixture) clear() {
	f.t.Helper()
	if err := apiconfig.Update(context.Background(), f.Side.DB, func(s *apiconfig.Settings) error {
		for _, k := range apiconfig.Keys() {
			s.Unset(k)
		}
		return nil
	}); err != nil {
		f.t.Fatal(err)
	}
}

// saved is what is saved now.
func (f *configFixture) saved() apiconfig.Settings {
	f.t.Helper()
	s, err := apiconfig.Load(context.Background(), f.Side.DB)
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

func decodeConfigReport(t *testing.T, text string) apiconfig.ConfigReport {
	t.Helper()
	var r apiconfig.ConfigReport
	if err := json.Unmarshal([]byte(text), &r); err != nil {
		t.Fatalf("not an api config document: %v\n%s", err, scrubbed(text))
	}
	return r
}

func decodeChangeResult(t *testing.T, text string) apiconfig.ChangeResult {
	t.Helper()
	var r apiconfig.ChangeResult
	if err := json.Unmarshal([]byte(text), &r); err != nil {
		t.Fatalf("not an api config change document: %v\n%s", err, scrubbed(text))
	}
	return r
}

func decodeStatusReport(t *testing.T, text string) apiconfig.StatusReport {
	t.Helper()
	var r apiconfig.StatusReport
	if err := json.Unmarshal([]byte(text), &r); err != nil {
		t.Fatalf("not an api status document: %v\n%s", err, scrubbed(text))
	}
	return r
}

// rowOf is the row of one setting in a config document.
func rowOf(t *testing.T, r apiconfig.ConfigReport, key string) apiconfig.SettingReport {
	t.Helper()
	for _, row := range r.Settings {
		if row.Key == key {
			return row
		}
	}
	t.Fatalf("the document has no row for %s", key)
	return apiconfig.SettingReport{}
}

// settingsOf is the saved value of every setting of a document, for a failure message.
func settingsOf(r apiconfig.ConfigReport) string {
	var parts []string
	for _, row := range r.Settings {
		if row.Saved != "" {
			parts = append(parts, fmt.Sprintf("%s=%s", row.Key, row.Saved))
		}
	}
	return strings.Join(parts, " ")
}
