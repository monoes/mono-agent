package health

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// GroupBrowser covers the browser, the MonoAgent extension and its bridge.
const GroupBrowser = "browser"

const (
	CheckBrowser   = "browser.installed"
	CheckExtension = "browser.extension"
	CheckBridge    = "browser.bridge"
	CheckPaired    = "browser.paired"

	FixBrowserInstall   = "browser.install"
	FixExtensionInstall = "browser.extension.install"
	FixExtensionPair    = "browser.extension.pair"
)

var browserFeatures = []string{"crawling", "page capture", "platform logins"}

func browserChecks() []Check {
	return []Check{
		{ID: CheckBrowser, Group: GroupBrowser, Title: "Browser", Features: browserFeatures, Run: checkBrowser},
		{ID: CheckExtension, Group: GroupBrowser, Title: "MonoAgent extension", Features: browserFeatures,
			DependsOn: []string{CheckBrowser}, Run: checkExtension},
		{ID: CheckBridge, Group: GroupBrowser, Title: "Extension bridge", Features: browserFeatures, Run: checkBridge},
		{ID: CheckPaired, Group: GroupBrowser, Title: "Extension connection", Features: browserFeatures,
			DependsOn: []string{CheckBridge}, Run: checkPaired},
	}
}

func browserFixes() []Fix {
	manual := func(context.Context, *Env, func(string)) error { return fmt.Errorf("this needs to be done by hand") }
	return []Fix{
		{FixInfo: FixInfo{ID: FixBrowserInstall, Label: "Install a Chromium browser", Safety: SafetyManual,
			Command: "install Google Chrome, Microsoft Edge, Chromium or Brave"}, Apply: manual},
		{FixInfo: FixInfo{ID: FixExtensionInstall, Label: "Load the MonoAgent extension", Safety: SafetyManual,
			Command: "open chrome://extensions → enable Developer mode → Load unpacked → the chrome-extension folder"}, Apply: manual},
		{FixInfo: FixInfo{ID: FixExtensionPair, Label: "Pair the extension", Safety: SafetyManual,
			Command: "monoagentcli extension pair"}, Apply: manual},
	}
}

func checkBrowser(_ context.Context, env *Env) Result {
	if env.FindBrowser == nil {
		return Result{Status: StatusSkip, Summary: "not available"}
	}
	if p := env.FindBrowser(); p != "" {
		return Result{Status: StatusOK, Summary: p}
	}
	return Result{Status: StatusWarn, Summary: "no Chrome, Edge, Chromium or Brave found", FixID: FixBrowserInstall}
}

func checkExtension(_ context.Context, env *Env) Result {
	if env.ExtensionInstalled == nil {
		return Result{Status: StatusSkip, Summary: "not available"}
	}
	if env.ExtensionInstalled() {
		return Result{Status: StatusOK, Summary: "installed in a browser profile"}
	}
	res := Result{Status: StatusWarn, Summary: "not found in any browser profile", FixID: FixExtensionInstall}
	if env.ExtensionDir != nil {
		res.Detail = "load unpacked from: " + env.ExtensionDir()
	}
	return res
}

func checkBridge(ctx context.Context, env *Env) Result {
	if env.Bridge == nil {
		return Result{Status: StatusSkip, Summary: "not available"}
	}
	b, ok := env.Bridge(ctx)
	if !ok {
		return Result{Status: StatusWarn, Summary: "not running",
			Detail: "the daemon hosts it (monoagentcli daemon), or run: monoagentcli extension serve", FixID: FixDaemonStart}
	}
	summary := fmt.Sprintf("%s (pid %d, up %s)", b.Addr, b.PID, time.Duration(b.UptimeSec)*time.Second)
	if skewed(b.Version, env.Version) {
		return Result{Status: StatusWarn, Summary: summary,
			Detail: fmt.Sprintf("the bridge runs %s but this CLI is %s — restart whatever started it to pick up the new build", b.Version, env.Version)}
	}
	return Result{Status: StatusOK, Summary: summary}
}

// skewed reports a real version difference between two release builds.
func skewed(a, b string) bool {
	norm := func(v string) string { return strings.TrimPrefix(strings.TrimSpace(v), "v") }
	a, b = norm(a), norm(b)
	if a == "" || b == "" || a == "dev" || b == "dev" || strings.Contains(a, "-g") || strings.Contains(b, "-g") {
		return false
	}
	return a != b
}

func checkPaired(ctx context.Context, env *Env) Result {
	b, ok := env.Bridge(ctx)
	if !ok {
		return Result{Status: StatusSkip, Summary: "bridge not running"}
	}
	switch b.Status {
	case "connected":
		return Result{Status: StatusOK, Summary: "extension connected"}
	case "unpaired":
		return Result{Status: StatusWarn, Summary: "extension is not paired with this bridge", FixID: FixExtensionPair}
	default:
		return Result{Status: StatusInfo, Summary: "extension not connected right now — the browser may be closed or idle"}
	}
}
