package apiconfig

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/autostart"
	"github.com/monoes/mono-agent/internal/daemonhb"
)

// fakeInstaller is the service manager of a test: it answers Status from its fields and
// counts what it is asked, and never runs anything.
type fakeInstaller struct {
	registered     bool
	where          string
	statusCalls    int
	restartCalls   int
	restartErr     error
	otherCallsMade int
}

func (f *fakeInstaller) Status(context.Context) (bool, string) {
	f.statusCalls++
	return f.registered, f.where
}
func (f *fakeInstaller) Restart(context.Context) error { f.restartCalls++; return f.restartErr }

// Install, Uninstall and Start are never called by the documents.
func (f *fakeInstaller) Install(context.Context) (autostart.Result, error) {
	f.otherCallsMade++
	return autostart.Result{}, nil
}
func (f *fakeInstaller) Uninstall(context.Context) error { f.otherCallsMade++; return nil }
func (f *fakeInstaller) Start(context.Context) error     { f.otherCallsMade++; return nil }

func shellEnv(env map[string]string, hb func() (daemonhb.Heartbeat, bool), inst *fakeInstaller) Env {
	if hb == nil {
		hb = noDaemon
	}
	return Env{Getenv: envMap(env), Heartbeat: hb, Installer: inst}
}

func rowOf(t *testing.T, r ConfigReport, key string) SettingReport {
	t.Helper()
	for _, s := range r.Settings {
		if s.Key == key {
			return s
		}
	}
	t.Fatalf("no row for %s", key)
	return SettingReport{}
}

func TestShowWithNothingSavedAndNoDaemon(t *testing.T) {
	inst := &fakeInstaller{registered: true, where: "/x/unit"}
	r, err := Show(context.Background(), openDB(t), shellEnv(nil, nil, inst))
	if err != nil {
		t.Fatal(err)
	}
	if r.V != 1 || r.Environment != "shell" || r.RestartNeeded || r.Problems == nil || len(r.Problems) != 0 {
		t.Errorf("report: %+v", r)
	}
	if r.Daemon != (DaemonReport{Running: false, ReportsSettings: false, Autostart: true}) {
		t.Errorf("daemon: %+v", r.Daemon)
	}
	if len(r.Settings) != 10 {
		t.Fatalf("%d settings", len(r.Settings))
	}
	for i, sp := range Specs() {
		row := r.Settings[i]
		if row.Key != sp.Key || row.ServerFlag != sp.ServerFlag || row.Env != sp.Env || row.Default != sp.Default {
			t.Errorf("row %d: %+v does not match its spec %+v", i, row, sp)
		}
		if row.Saved != "" || row.Effective != sp.Default || row.Source != SourceDefault || row.Running != nil || row.RunningSource != "" || row.State != StateNotRunning {
			t.Errorf("%s: %+v", sp.Key, row)
		}
	}
	if inst.statusCalls != 1 || inst.restartCalls != 0 || inst.otherCallsMade != 0 {
		t.Errorf("the service manager was asked %d times for its status, to restart %d times and for anything else %d times", inst.statusCalls, inst.restartCalls, inst.otherCallsMade)
	}
}

func TestShowSaysWhoseEnvironmentItReads(t *testing.T) {
	inst := &fakeInstaller{}
	env := shellEnv(nil, nil, inst)
	env.Environment = "mcp"
	r, err := Show(context.Background(), openDB(t), env)
	if err != nil || r.Environment != "mcp" || r.Daemon.Autostart {
		t.Fatalf("%+v, %v", r, err)
	}
}

// Effective is what a server started from this process would use: its environment over the
// saved value over the default, in the canonical spelling.
func TestShowEffectiveValuesFollowThePrecedence(t *testing.T) {
	db := openDB(t)
	putRow(t, db, `{"v":1,"confinement":"sandboxed","max_concurrent":8,"turn_timeout":"900s","image_runtimes":"none"}`)
	r, err := Show(context.Background(), db, shellEnv(map[string]string{
		"MONOAGENT_API_CONFINEMENT": "chat-only", "MONOAGENT_API_TOOL_RUNTIMES": "agy, Codex",
	}, nil, &fakeInstaller{}))
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]SettingReport{
		"confinement":    {Saved: "sandboxed", Effective: "chat-only", Source: SourceEnv},
		"max_concurrent": {Saved: "8", Effective: "8", Source: SourceSaved},
		"turn_timeout":   {Saved: "15m", Effective: "15m", Source: SourceSaved}, // shown in the spelling that is stored
		"image_runtimes": {Saved: "none", Effective: "none", Source: SourceSaved},
		"tool_runtimes":  {Saved: "", Effective: "antigravity,codex", Source: SourceEnv},
		"v1_addr":        {Saved: "", Effective: "", Source: SourceDefault},
	} {
		got := rowOf(t, r, key)
		if got.Saved != want.Saved || got.Effective != want.Effective || got.Source != want.Source {
			t.Errorf("%s: saved %q effective %q from %s, want %q %q %s", key, got.Saved, got.Effective, got.Source, want.Saved, want.Effective, want.Source)
		}
	}
}

func TestShowAgainstALiveDaemon(t *testing.T) {
	db := openDB(t)
	putRow(t, db, `{"v":1,"v1_addr":"0.0.0.0:9443","max_concurrent":8,"turn_timeout":"20m","confinement":"sandboxed"}`)
	hb := daemonhb.Heartbeat{PID: 1, APISettings: map[string]daemonhb.APISetting{
		"v1_addr":        {Value: "", Source: "default"},       // saved since: pending
		"max_concurrent": {Value: "8", Source: "saved"},        // applied
		"turn_timeout":   {Value: "5m", Source: "env"},         // overridden
		"confinement":    {Value: "sandboxed", Source: "flag"}, // overridden, though equal
		"tool_runtimes":  {Value: "claude,codex", Source: "default"},
	}}
	inst := &fakeInstaller{registered: true}
	r, err := Show(context.Background(), db, shellEnv(nil, func() (daemonhb.Heartbeat, bool) { return hb, true }, inst))
	if err != nil {
		t.Fatal(err)
	}
	if r.Daemon != (DaemonReport{Running: true, ReportsSettings: true, Autostart: true}) || !r.RestartNeeded {
		t.Errorf("daemon %+v, restart needed %v", r.Daemon, r.RestartNeeded)
	}
	for key, want := range map[string]struct {
		running, source, state string
		reported               bool
	}{
		"v1_addr":        {"", "default", StatePendingRestart, true},
		"max_concurrent": {"8", "saved", StateApplied, true},
		"turn_timeout":   {"5m", "env", StateOverridden, true},
		"confinement":    {"sandboxed", "flag", StateOverridden, true},
		"tool_runtimes":  {"claude,codex", "default", StateApplied, true},
		"image_runtimes": {"", "", StateUnknown, false}, // this daemon says nothing of it
	} {
		row := rowOf(t, r, key)
		if row.State != want.state || row.RunningSource != want.source || (row.Running != nil) != want.reported || (row.Running != nil && *row.Running != want.running) {
			t.Errorf("%s: %+v, want running %q from %q, %s", key, row, want.running, want.source, want.state)
		}
	}
}

// A live daemon that predates the report: its settings are unknown, and nothing is pending.
func TestShowAgainstADaemonThatPredatesTheReport(t *testing.T) {
	db := openDB(t)
	putRow(t, db, `{"v":1,"confinement":"sandboxed"}`)
	r, err := Show(context.Background(), db, shellEnv(nil, func() (daemonhb.Heartbeat, bool) { return daemonhb.Heartbeat{PID: 1}, true }, &fakeInstaller{}))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Daemon.Running || r.Daemon.ReportsSettings || r.RestartNeeded {
		t.Errorf("daemon %+v, restart needed %v", r.Daemon, r.RestartNeeded)
	}
	for _, row := range r.Settings {
		if row.State != StateUnknown || row.Running != nil || row.RunningSource != "" {
			t.Errorf("%s: %+v", row.Key, row)
		}
	}
}

// An invalid saved value does not stop the report: it is how the value is found and removed.
func TestShowReportsInvalidSavedValuesAsProblems(t *testing.T) {
	db := openDB(t)
	putRow(t, db, `{"v":1,"confinement":"everything","tls_cert_file":"/c.pem"}`)
	r, err := Show(context.Background(), db, shellEnv(nil, nil, &fakeInstaller{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Problems) != 2 || r.Problems[0].Key != "confinement" || r.Problems[1].Key != "tls_cert_file" {
		t.Fatalf("problems: %+v", r.Problems)
	}
	if got := rowOf(t, r, "confinement"); got.Saved != "everything" || got.Effective != "everything" {
		t.Errorf("the invalid value is shown as it is stored: %+v", got)
	}
}

func TestShowRefusesADocumentItCannotRead(t *testing.T) {
	db := openDB(t)
	putRow(t, db, `{"v":2}`)
	if _, err := Show(context.Background(), db, shellEnv(nil, nil, &fakeInstaller{})); !errors.Is(err, ErrTooNew) {
		t.Errorf("a newer format: %v", err)
	}
	putRow(t, db, `nonsense`)
	if _, err := Show(context.Background(), db, shellEnv(nil, nil, &fakeInstaller{})); err == nil || !strings.Contains(err.Error(), Row) {
		t.Errorf("a damaged document: %v", err)
	}
}

// The document is a contract: every field and its place, with `running` present for an empty
// value the daemon reports and absent for one it does not.
func TestTheConfigDocumentKeepsItsFieldsAndTheirOrder(t *testing.T) {
	empty, eight := "", "8"
	r := ConfigReport{
		V: 1, Environment: "shell", RestartNeeded: true, Problems: []Problem{{Key: "confinement", Message: "confinement must be chat-only, sandboxed or any"}},
		Daemon: DaemonReport{Running: true, ReportsSettings: true, Autostart: true},
		Settings: []SettingReport{
			{Key: "v1_addr", ServerFlag: "--v1-addr", Env: "MONOAGENT_API_V1_ADDR", Saved: "0.0.0.0:9443", Default: "", Effective: "0.0.0.0:9443", Source: "saved", Running: &empty, RunningSource: "default", State: "pending_restart"},
			{Key: "max_concurrent", ServerFlag: "--max-concurrent", Env: "MONOAGENT_API_MAX_CONCURRENT", Default: "4", Effective: "8", Source: "env", Running: &eight, RunningSource: "flag", State: "overridden"},
			{Key: "turn_timeout", Env: "MONOAGENT_API_TURN_TIMEOUT", Default: "10m", Effective: "10m", Source: "default", State: "unknown"},
		},
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"v":1,"environment":"shell","settings":[` +
		`{"key":"v1_addr","server_flag":"--v1-addr","env":"MONOAGENT_API_V1_ADDR","saved":"0.0.0.0:9443","default":"","effective":"0.0.0.0:9443","source":"saved","running":"","running_source":"default","state":"pending_restart"},` +
		`{"key":"max_concurrent","server_flag":"--max-concurrent","env":"MONOAGENT_API_MAX_CONCURRENT","default":"4","effective":"8","source":"env","running":"8","running_source":"flag","state":"overridden"},` +
		`{"key":"turn_timeout","server_flag":"","env":"MONOAGENT_API_TURN_TIMEOUT","default":"10m","effective":"10m","source":"default","state":"unknown"}],` +
		`"daemon":{"running":true,"reports_settings":true,"autostart":true},"restart_needed":true,` +
		`"problems":[{"key":"confinement","message":"confinement must be chat-only, sandboxed or any"}]}`
	if string(b) != want {
		t.Errorf("the document changed:\n got %s\nwant %s", b, want)
	}
}
