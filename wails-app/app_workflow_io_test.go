package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/workflow"
)

// parseStrict decodes JSON rejecting unknown fields, so a legacy-key drift
// (e.g. "node_type" instead of "type") fails the test instead of being
// silently ignored.
func parseStrict(data string, v interface{}) error {
	dec := json.NewDecoder(strings.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// ─────────────────────────────────────────────────────────────────────────────
// ExportWorkflow / ImportWorkflow
// ─────────────────────────────────────────────────────────────────────────────

// TestExportWorkflow_EmitsWorkflowFileShape: the binding's output must be the
// documented WorkflowFile format the CLI export emits and import parses —
// "type"/"position"/"source"/"target" keys, not the legacy internal marshal.
func TestExportWorkflow_EmitsWorkflowFileShape(t *testing.T) {
	a := newTestApp(t)

	saved, err := a.SaveWorkflow(SaveWorkflowRequest{
		Name: "shape-check", Description: "d", IsActive: true,
		Nodes: []WorkflowNodeData{
			{ID: "n1", NodeType: "trigger.manual", Name: "Start", PositionX: 1.5, PositionY: 2.5, Config: map[string]interface{}{}},
			{ID: "n2", NodeType: "core.set", Name: "Set", Config: map[string]interface{}{"field": "x"}},
		},
		Connections: []WorkflowConnectionData{
			{ID: "c1", SourceNodeID: "n1", TargetNodeID: "n2"},
		},
	})
	if err != nil {
		t.Fatalf("SaveWorkflow: %v", err)
	}

	exported, err := a.ExportWorkflow(saved.ID)
	if err != nil {
		t.Fatalf("ExportWorkflow: %v", err)
	}

	var wfFile workflow.WorkflowFile
	if err := parseStrict(exported, &wfFile); err != nil {
		t.Fatalf("export is not valid WorkflowFile JSON: %v\n%s", err, exported)
	}
	if wfFile.ID != saved.ID || wfFile.Name != "shape-check" || !wfFile.IsActive {
		t.Fatalf("unexpected header fields: %+v", wfFile)
	}
	if len(wfFile.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(wfFile.Nodes))
	}
	if wfFile.Nodes[0].Type != "trigger.manual" || wfFile.Nodes[0].ID != "n1" {
		t.Fatalf("unexpected first node: %+v", wfFile.Nodes[0])
	}
	if wfFile.Nodes[0].Position.X != 1.5 || wfFile.Nodes[0].Position.Y != 2.5 {
		t.Fatalf("position not preserved: %+v", wfFile.Nodes[0].Position)
	}
	if len(wfFile.Connections) != 1 || wfFile.Connections[0].Source != "n1" || wfFile.Connections[0].Target != "n2" {
		t.Fatalf("unexpected connections: %+v", wfFile.Connections)
	}
}

// TestExportWorkflow_OtherProfilesWorkflowHidden: one profile must not be
// able to export another profile's workflow.
func TestExportWorkflow_OtherProfilesWorkflowHidden(t *testing.T) {
	a := newTestApp(t)

	saved, err := a.SaveWorkflow(SaveWorkflowRequest{Name: "secret", IsActive: true})
	if err != nil {
		t.Fatalf("SaveWorkflow: %v", err)
	}

	a.setActiveProfileID("other-profile")
	if _, err := a.ExportWorkflow(saved.ID); err == nil {
		t.Fatal("expected error exporting another profile's workflow, got nil")
	}
}

// TestImportWorkflow_RejectsBadInput: garbage input fails validation before
// any subprocess is spawned.
func TestImportWorkflow_RejectsBadInput(t *testing.T) {
	a := newTestApp(t)

	for _, bad := range []string{"", "   ", "not json {", "/nonexistent/path/definitely-not-a-file.json"} {
		if _, err := a.ImportWorkflow(bad); err == nil {
			t.Fatalf("expected error for input %q, got nil", bad)
		}
	}
}

// buildTestCLI builds the repo's monoagentcli into a temp dir so subprocess
// tests run the same binary the GUI would spawn. Skips when building is not
// possible in this environment.
func buildTestCLI(t *testing.T) string {
	t.Helper()
	repoRoot, err := filepath.Abs("..") // wails-app/ is nested directly in the repo root
	if _, statErr := os.Stat(filepath.Join(repoRoot, "cmd", "monoagentcli")); err != nil || statErr != nil {
		t.Skipf("repo root with cmd/monoagentcli not found: %v / %v", err, statErr)
	}
	bin := filepath.Join(t.TempDir(), "monoagentcli")
	build := exec.Command("go", "build", "-o", bin, "./cmd/monoagentcli")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("building monoagentcli failed (%v): %s", err, out)
	}
	return bin
}

// newTestAppWithHomeDir is newTestApp, but with the workflow file store under
// homeDir/.monoagent/workflows — the same directory a monoagentcli subprocess
// with HOME=homeDir reads and writes.
func newTestAppWithHomeDir(t *testing.T, homeDir string) *App {
	t.Helper()
	sdb, err := storage.NewDatabase(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	if err := sdb.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	t.Cleanup(func() { sdb.DB.Close() })

	fileStore, err := workflow.NewWorkflowFileStore(filepath.Join(homeDir, ".monoagent", "workflows"))
	if err != nil {
		t.Fatalf("NewWorkflowFileStore: %v", err)
	}
	return &App{
		db:          sdb.DB,
		wfStore:     workflow.NewHybridWorkflowStore(fileStore, workflow.NewSQLiteWorkflowStore(sdb.DB)),
		runningCmds: make(map[string]*exec.Cmd),
	}
}

// TestExportImportRoundtripViaCLI: GUI save → GUI export → CLI import
// (subprocess, isolated HOME) → GUI read must roundtrip a workflow with its
// nodes, positions, and connections intact. This is the parity gap FD7
// closes: the GUI can now produce and consume the CLI's interchange format.
func TestExportImportRoundtripViaCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("HOME isolation is unix-only")
	}
	cliBin := buildTestCLI(t)

	// Build the CLI before overriding HOME so the go build cache stays on
	// the real home; the import subprocess below must only see the isolated
	// home (~/.monoagent of the test sandbox).
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", filepath.Dir(cliBin)+string(os.PathListSeparator)+os.Getenv("PATH"))

	a := newTestAppWithHomeDir(t, home)

	saved, err := a.SaveWorkflow(SaveWorkflowRequest{
		Name: "roundtrip", Description: "rt", IsActive: false,
		Nodes: []WorkflowNodeData{
			{ID: "n1", NodeType: "trigger.manual", Name: "Start", PositionX: 3, PositionY: 4, Config: map[string]interface{}{}},
			{ID: "n2", NodeType: "core.set", Name: "Set", Config: map[string]interface{}{"field": "x"}},
		},
		Connections: []WorkflowConnectionData{
			{ID: "c1", SourceNodeID: "n1", TargetNodeID: "n2"},
		},
	})
	if err != nil {
		t.Fatalf("SaveWorkflow: %v", err)
	}

	exported, err := a.ExportWorkflow(saved.ID)
	if err != nil {
		t.Fatalf("ExportWorkflow: %v", err)
	}

	// Raw-JSON input mode.
	imp, err := a.ImportWorkflow(exported)
	if err != nil {
		t.Fatalf("ImportWorkflow(json): %v", err)
	}
	if imp.Name != "roundtrip" {
		t.Fatalf("imported name = %q, want %q", imp.Name, "roundtrip")
	}
	if imp.ID == saved.ID {
		t.Fatalf("import should assign a fresh id, got the original %q", imp.ID)
	}

	got, err := a.GetWorkflow(imp.ID)
	if err != nil {
		t.Fatalf("GetWorkflow(imported): %v", err)
	}
	if got.Name != "roundtrip" || len(got.Nodes) != 2 || len(got.Connections) != 1 {
		t.Fatalf("roundtripped workflow lost data: %+v", got)
	}
	if got.Nodes[0].ID != "n1" || got.Nodes[0].NodeType != "trigger.manual" ||
		got.Nodes[0].PositionX != 3 || got.Nodes[0].PositionY != 4 {
		t.Fatalf("roundtripped node 0 degraded: %+v", got.Nodes[0])
	}
	if got.Connections[0].SourceNodeID != "n1" || got.Connections[0].TargetNodeID != "n2" {
		t.Fatalf("roundtripped connection degraded: %+v", got.Connections[0])
	}

	// File-path input mode: same import via a path to the JSON on disk.
	wfPath := filepath.Join(t.TempDir(), "workflow.json")
	if err := os.WriteFile(wfPath, []byte(exported), 0o644); err != nil {
		t.Fatalf("writing workflow file: %v", err)
	}
	imp2, err := a.ImportWorkflow(wfPath)
	if err != nil {
		t.Fatalf("ImportWorkflow(path): %v", err)
	}
	if imp2.Name != "roundtrip" || imp2.ID == imp.ID || imp2.ID == saved.ID {
		t.Fatalf("unexpected second import result: %+v", imp2)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// CancelWorkflow PID verification (RA1-8)
// ─────────────────────────────────────────────────────────────────────────────

// waitForExec blocks until pid has finished execve and its command line is
// readable. cmd.Start() returns after fork but before exec completes, and in
// that window /proc/<pid>/cmdline reads back empty — which
// readProcessCommandLine reports as "not alive" (its zombie/kernel-thread
// case), making signalWorkflowPID a no-op. Asserting straight after Start()
// therefore races exec and fails on machines fast enough to win it.
func waitForExec(t *testing.T, pid int) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if _, alive, err := readProcessCommandLine(pid); err == nil && alive {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("pid %d never became inspectable", pid)
}

// TestSignalWorkflowPID_RefusesForeignProcess: a live pid whose command line
// is not a monoagent binary must never be signaled.
func TestSignalWorkflowPID_RefusesForeignProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signal semantics are unix-only")
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot spawn sleep: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	waitForExec(t, cmd.Process.Pid)

	err := signalWorkflowPID(cmd.Process.Pid)
	if err == nil {
		t.Fatalf("expected refusal for non-monoagent pid %d, got nil", cmd.Process.Pid)
	}
	if !strings.Contains(err.Error(), "refusing to signal non-monoagent process") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestSignalWorkflowPID_DeadPidIsNoop: a stale pid (process already reaped)
// must be treated as "nothing to signal", not as a refusal — CancelWorkflow
// should still proceed to its bookkeeping.
func TestSignalWorkflowPID_DeadPidIsNoop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signal semantics are unix-only")
	}
	cmd := exec.Command("sleep", "0")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot spawn sleep: %v", err)
	}
	_ = cmd.Wait() // reap it — the pid is now gone

	if err := signalWorkflowPID(cmd.Process.Pid); err != nil {
		t.Fatalf("dead pid must be a no-op, got: %v", err)
	}
	if err := signalWorkflowPID(0); err != nil {
		t.Fatalf("pid 0 must be a no-op, got: %v", err)
	}
}

// TestReadProcessCommandLine_Self: the helper must see the calling process.
func TestReadProcessCommandLine_Self(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix implementation")
	}
	cmdline, alive, err := readProcessCommandLine(os.Getpid())
	if err != nil {
		t.Fatalf("readProcessCommandLine(self): %v", err)
	}
	if !alive || cmdline == "" {
		t.Fatalf("self should be alive with a command line, got alive=%v cmd=%q", alive, cmdline)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// RunNode cancellation (RA1-9)
// ─────────────────────────────────────────────────────────────────────────────

// TestStopNodeRun_KillsRegisteredProcess: StopNodeRun must kill the
// subprocess registered under the run id and complain about runs that are
// unknown or never started.
func TestStopNodeRun_KillsRegisteredProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("spawned process test uses sleep")
	}
	a := newTestApp(t)

	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot spawn sleep: %v", err)
	}
	a.runningCmds["noderun:7"] = cmd

	if err := a.StopNodeRun("7"); err != nil {
		t.Fatalf("StopNodeRun: %v", err)
	}
	if waitErr := cmd.Wait(); waitErr == nil {
		t.Fatal("expected the stopped process to exit with an error, got nil")
	}

	if err := a.StopNodeRun("7"); err == nil {
		t.Fatal("expected error stopping an already-finished run, got nil")
	}

	a.runningCmds["noderun:8"] = &exec.Cmd{} // registered but never started
	if err := a.StopNodeRun("8"); err == nil {
		t.Fatal("expected error for a never-started run, got nil")
	}
}
