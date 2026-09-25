//go:build social

package bot

// Jev element picker for the social bots (plan
// docs/plans/2026-09-25-jev-integration.md, WS11). A bot embeds JevPicker;
// the node layer hands it a client with SetJevPicker when the profile opted
// in. A bot consults Jev only where its own heuristic finder found no element
// or more than one; with no picker, or on any Jev failure, the bot does
// exactly what it did before.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/monoes/mono-agent/internal/browser"
	"github.com/monoes/mono-agent/internal/jev"
	"github.com/monoes/mono-agent/internal/jevpick"
)

// DefaultJevMinP is the social-bot threshold from plan §5 (top option's
// probability), used when SetJevPicker is given a non-positive minP.
const DefaultJevMinP = 0.6

const (
	jevPickTimeout    = 10 * time.Second
	jevElementTimeout = 5 * time.Second
)

// JevPicker is embedded in bots that can ask Jev for an element. The zero
// value is disabled.
type JevPicker struct {
	jevClient *jev.Client
	jevMinP   float64
}

// SetJevPicker enables Jev element picks for this bot instance. A nil client
// disables them; minP <= 0 selects DefaultJevMinP.
func (j *JevPicker) SetJevPicker(c *jev.Client, minP float64) {
	if minP <= 0 || minP > 1 {
		minP = DefaultJevMinP
	}
	j.jevClient, j.jevMinP = c, minP
}

// JevAvailable reports whether a pick can be attempted on page: a client is
// set and the page speaks raw CDP (the extension page does).
func (j *JevPicker) JevAvailable(page browser.PageInterface) bool {
	if j == nil || j.jevClient == nil || page == nil {
		return false
	}
	_, ok := page.(jevpick.Page)
	return ok
}

// ErrJevUnavailable is returned by JevElement when no pick can be attempted.
var ErrJevUnavailable = errors.New("jev picker not available")

// JevPick is one element Jev chose, marked in the page.
type JevPick struct {
	*jevpick.Picked
	// Selector selects the marked element through the page's own driver.
	Selector string
	// Element is the handle for Selector.
	Element browser.ElementHandle
	page    jevpick.Page
}

// Release removes the pick's DOM marker. Errors are ignored: the page may
// have navigated away, taking the marker along. Safe on nil.
func (p *JevPick) Release() {
	if p == nil || p.page == nil || p.Picked == nil {
		return
	}
	_ = jevpick.Unmark(p.page, p.Marker)
	p.page = nil
}

// JevElement asks Jev for the element matching t, marks it, and resolves it
// through page.Element. The caller must Release the returned pick. Any
// failure (unavailable, NONE, below threshold, stale, network, element not
// found) is returned as an error and leaves no marker behind.
func (j *JevPicker) JevElement(ctx context.Context, page browser.PageInterface, t jevpick.Target) (*JevPick, error) {
	if !j.JevAvailable(page) {
		return nil, ErrJevUnavailable
	}
	cdp := page.(jevpick.Page)
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, jevPickTimeout)
	defer cancel()
	picked, err := jevpick.Pick(ctx, j.jevClient, cdp, t, j.jevMinP)
	if err != nil {
		return nil, err
	}
	p := &JevPick{Picked: picked, page: cdp,
		Selector: fmt.Sprintf("[%s='%s']", jevpick.MarkerAttr, picked.Marker)}
	el, err := page.Element(p.Selector, jevElementTimeout)
	if err == nil && el == nil {
		err = errors.New("marked element not found")
	}
	if err != nil {
		p.Release()
		return nil, err
	}
	p.Element = el
	return p, nil
}
