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
func OpenForApplication(ctx context.Context, jobURL string) error {
	launchURL, err := launcher.New().Headless(false).Launch()
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
