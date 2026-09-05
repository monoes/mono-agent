// internal/apply/browser.go
package apply

import (
	"context"
	"fmt"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// OpenForApplication launches a real, VISIBLE browser window at jobURL,
// for a human to complete the application by hand. This function
// contains no interaction beyond navigation — a companion test in this
// package mechanically enforces that this file never grows a
// form-interaction call. The browser is deliberately NOT closed before
// returning (unlike documents.RenderPDF's throwaway headless instance)
// — it stays open for the user to use.
//
// Leakless(false) is required for that to actually hold: go-rod's
// launcher enables "leakless" by default, which force-kills the spawned
// browser the moment THIS process exits — since the CLI command that
// calls this function returns and exits almost immediately after opening
// the page, the default behavior would silently close the window right
// after it appears, defeating the entire point of this function. Verified
// directly against go-rod's source (lib/launcher/launcher.go: "Leakless
// will be enabled by default... If enabled, the browser will be force
// killed after the Go process exits").
func OpenForApplication(ctx context.Context, jobURL string) error {
	launchURL, err := launcher.New().Headless(false).Leakless(false).Launch()
	if err != nil {
		return fmt.Errorf("apply.OpenForApplication: launch browser: %w", err)
	}

	browser := rod.New().ControlURL(launchURL).Context(ctx)
	if err := browser.Connect(); err != nil {
		return fmt.Errorf("apply.OpenForApplication: connect to browser: %w", err)
	}

	if _, err := browser.Page(proto.TargetCreateTarget{URL: jobURL}); err != nil {
		return fmt.Errorf("apply.OpenForApplication: open page: %w", err)
	}
	return nil
}
