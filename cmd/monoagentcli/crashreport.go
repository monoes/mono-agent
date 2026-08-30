package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// crashReportOptIn enables GitHub crash filing via the sibling `monomind`
// CLI. Default (unset or any value other than "1"): the crash record is
// written to a local file only — nothing ever leaves the machine, and no
// subprocess is spawned.
const crashReportOptIn = "MONOAGENT_CRASH_REPORT"

// reportCrash is called from a deferred recover() in main(). It never panics
// itself and never blocks longer than a few seconds — it's an observability
// side effect, not something that should change crash behavior for the user.
//
// Default: append the crash record (timestamp, version, panic title, stack)
// to $HOME/.monoagent/crashes/crash-<unixts>.log (best-effort; failures are
// reported on stderr and swallowed).
//
// Opt-in: when MONOAGENT_CRASH_REPORT=1 AND `monomind` is already installed
// (resolved via exec.LookPath), the crash is filed to GitHub through
// `monomind report-crash` so redaction, dedup against existing issues, and
// auth (gh CLI / GITHUB_TOKEN) live in one place. There is no auto-download
// fallback: npx is never invoked.
func reportCrash(panicValue interface{}, stack []byte) {
	title := fmt.Sprintf("panic: %v", panicValue)
	body := fmt.Sprintf("Uncaught panic in `monoagentcli`.\n\nVersion: %s (built %s)\n\n```\n%s\n```\n", getVersion(), getBuildDate(), stack)

	if os.Getenv(crashReportOptIn) == "1" {
		if monomindPath, err := exec.LookPath("monomind"); err == nil {
			if reportViaMonomind(monomindPath, title, body) {
				return
			}
		}
	}
	saveCrashLocally(title, body)
}

// reportViaMonomind returns false if the invocation failed (e.g. an older
// monomind on PATH predates the `report-crash` command) so the caller can
// fall back rather than silently swallowing the crash report.
func reportViaMonomind(monomindPath, title, body string) bool {
	cmd := exec.Command(monomindPath, "report-crash", "--repo", "monoes/mono-agent", "--title", title, "--body", body)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return runWithTimeout(cmd, 15*time.Second) == nil
}

func runWithTimeout(cmd *exec.Cmd, timeout time.Duration) error {
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		return fmt.Errorf("crash report timed out after %s", timeout)
	}
}

// saveCrashLocally appends the crash record to a per-crash log file under
// $HOME/.monoagent/crashes/. Best-effort: any failure is reported on stderr
// and returned from silently — this must never re-panic or mask the
// original crash.
func saveCrashLocally(title, body string) {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[monoagentcli] crash occurred, and couldn't save a report (no home dir): %s\n", title)
		return
	}
	crashDir := filepath.Join(home, ".monoagent", "crashes")
	if err := os.MkdirAll(crashDir, 0o755); err != nil {
		return
	}
	path := filepath.Join(crashDir, fmt.Sprintf("crash-%d.log", time.Now().Unix()))
	record := fmt.Sprintf("timestamp: %s\nversion: %s (built %s)\ntitle: %s\n\n%s\n",
		time.Now().UTC().Format(time.RFC3339), version, buildDate, title, body)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.WriteString(record); err != nil {
		return
	}
	fmt.Fprintf(os.Stderr, "[monoagentcli] crash report saved to %s (set MONOAGENT_CRASH_REPORT=1 to file via monomind)\n", path)
}
