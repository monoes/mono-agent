package main

import (
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/monoes/mono-agent/internal/appupdate"
	"github.com/spf13/cobra"
)

// `update --app <exe>`: update the desktop app and the CLI bundled with it
// from the latest release (internal/appupdate). Every asset must match the
// release's SHA256SUMS.txt, and the files are swapped all-or-nothing, or
// nothing is installed. The desktop app calls this and only shows progress;
// it then quits, and on macOS/Windows a detached script relaunches it once
// it has exited.

// appUpdateResult is `update --app --json` on stdout. Progress goes to stderr.
type appUpdateResult struct {
	Success    bool   `json:"success"`
	UpToDate   bool   `json:"up_to_date,omitempty"`
	NewVersion string `json:"new_version,omitempty"`
	// Restart tells the app what happens next: "quit" (the files are
	// replaced; quit and start again) or "relaunch" (a script starts the
	// app again after it quits).
	Restart string `json:"restart,omitempty"`
	// Daemon is what the update did about a running daemon (daemonUpdate); absent when none ran
	// or it already ran this version.
	Daemon *daemonUpdate `json:"daemon,omitempty"`
	Error  string        `json:"error,omitempty"`
}

func runUpdateApp(cmd *cobra.Command, cfg *globalConfig, appPath, current string) error {
	progress := func(msg string) {
		if cfg.JSONOutput {
			fmt.Fprintf(cmd.ErrOrStderr(), "{\"kind\":\"line\",\"message\":%q}\n", msg)
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), msg)
		}
	}
	res, err := updateApp(cmd, appPath, current, progress)
	if err != nil {
		if !cfg.JSONOutput {
			return err
		}
		res.Error = err.Error()
	} else if !res.UpToDate {
		// The files are replaced; a daemon still runs the old ones. The app relays this line.
		if res.Daemon = daemonAfterUpdate(cmd.Context(), cfg, res.NewVersion); res.Daemon != nil {
			progress(res.Daemon.Message)
		}
	}
	if cfg.JSONOutput {
		return writeJSONTo(cmd.OutOrStdout(), res)
	}
	if res.UpToDate {
		fmt.Fprintln(cmd.OutOrStdout(), "The app is already on the latest release.")
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "App updated to %s — restart it (%s).\n", res.NewVersion, res.Restart)
	}
	return nil
}

func updateApp(cmd *cobra.Command, appPath, current string, progress func(string)) (appUpdateResult, error) {
	if appPath == "" {
		return appUpdateResult{}, errInvalidInput("--app needs the path of the desktop app's executable")
	}
	if abs, err := filepath.EvalSymlinks(appPath); err == nil {
		appPath = abs
	}
	u := appupdate.Updater{
		Context:       cmd.Context(),
		Client:        http.DefaultClient,
		APIURL:        latestReleaseURL,
		GOOS:          runtime.GOOS,
		GOARCH:        runtime.GOARCH,
		Exe:           appPath,
		Progress:      progress,
		StartDetached: startDetached,
	}
	if current != "" {
		u.UpToDate = func(tag string) bool { return !newerRelease(tag, current) }
	}
	r, err := u.Run()
	if err != nil {
		return appUpdateResult{}, err
	}
	return appUpdateResult{Success: true, UpToDate: r.UpToDate, NewVersion: r.NewVersion, Restart: r.Restart}, nil
}

// startDetached starts name in its own session (detachStart) so the
// restart outlives this CLI and the app that ran it.
func startDetached(name string, args ...string) error {
	return detachStart(exec.Command(name, args...)) //nolint:gosec
}
