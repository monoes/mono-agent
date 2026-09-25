//go:build social

package x

import (
	"context"
	"fmt"

	botpkg "github.com/monoes/mono-agent/internal/bot"
	"github.com/monoes/mono-agent/internal/browser"
	"github.com/monoes/mono-agent/internal/jevpick"
)

// pickInfo describes a Jev-picked element, read from the page.
type pickInfo struct {
	Inside  bool   `json:"inside"`
	TestID  string `json:"testid"`
	Label   string `json:"label"`
	Pressed string `json:"pressed"`
	Tag     string `json:"tag"`
	Role    string `json:"role"`
}

const pickInfoJS = `(sel, scope) => {
	const e = document.querySelector(sel);
	if (!e) return { inside: false, testid: '', label: '', pressed: '', tag: '', role: '' };
	const t = e.closest('[data-testid]');
	return {
		inside: scope ? !!e.closest(scope) : true,
		testid: t ? (t.getAttribute('data-testid') || '') : '',
		label: ((e.getAttribute('aria-label') || '') + ' ' + (e.innerText || e.textContent || '')).trim(),
		pressed: e.getAttribute('aria-pressed') || '',
		tag: e.tagName,
		role: e.getAttribute('role') || '',
	};
}`

// jevPick asks Jev (when the node layer enabled it) for the element
// matching intent. When scope is set the pick must lie inside it — a pick
// elsewhere on the page (another post's button, say) is rejected, never
// used. The caller must Release the pick.
func (b *XBot) jevPick(ctx context.Context, p browser.PageInterface, kind, intent, hint, scope string) (*botpkg.JevPick, pickInfo, error) {
	var info pickInfo
	pick, err := b.JevElement(ctx, p, jevpick.Target{Kind: kind, Intent: intent, Hint: hint})
	if err != nil {
		return nil, info, err
	}
	if err := botpkg.EvalJSON(p, pickInfoJS, &info, pick.Selector, scope); err != nil {
		pick.Release()
		return nil, info, fmt.Errorf("read picked element: %w", err)
	}
	if !info.Inside {
		pick.Release()
		return nil, info, fmt.Errorf("picked element %q is outside the target", truncateForError(info.Label, 60))
	}
	return pick, info, nil
}
