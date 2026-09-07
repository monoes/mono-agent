// internal/apply/browser.go
package apply

import (
	"context"
	"fmt"

	"github.com/monoes/mono-agent/internal/browser"
)

// OpenForApplicationFunc is OpenForApplication's implementation, exposed as
// a swappable package-level variable so callers can inject a fake in tests
// that don't care about actually opening a browser tab (e.g. a CLI test
// verifying auto-mode prompt-suppression, not browser mechanics) — mirrors
// documents.RenderPDFFunc's convention exactly. Production code always
// goes through this var.
var OpenForApplicationFunc = openForApplicationImpl

// OpenForApplication opens jobURL as a new tab in the user's real,
// already-logged-in Chrome via the MonoAgent browser extension, for a
// human to complete the application by hand. This function contains no
// interaction beyond navigation — a companion test in this package
// mechanically enforces that this file never grows a form-interaction
// call. There is no local-browser fallback: if the extension isn't
// connected, this fails with a clear error instead of launching a fresh,
// unauthenticated Chromium instance — matching
// internal/browser.HybridSessionProvider's "no Rod fallback" behavior
// elsewhere in the codebase. bridge is typically obtained via
// setupExtensionBridge/ensureExtensionConnected (cmd/monoagentcli) so
// Chrome is reused (or launched once, never duplicated) across calls.
func OpenForApplication(ctx context.Context, jobURL string, bridge browser.ExtensionBridge) error {
	return OpenForApplicationFunc(ctx, jobURL, bridge)
}

func openForApplicationImpl(ctx context.Context, jobURL string, bridge browser.ExtensionBridge) error {
	if bridge == nil || !bridge.IsConnected() {
		return fmt.Errorf("apply.OpenForApplication: MonoAgent browser extension is not connected — connect it (run `monoagentcli extension` for setup/pairing instructions) so the job posting opens as a tab in your real, already-logged-in Chrome instead of a separate throwaway browser window")
	}
	if _, err := bridge.CreateTab(jobURL); err != nil {
		return fmt.Errorf("apply.OpenForApplication: opening tab via extension: %w", err)
	}
	return nil
}
