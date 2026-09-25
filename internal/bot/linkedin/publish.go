//go:build !nosocial

package linkedin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/browser"
	"github.com/monoes/mono-agent/internal/fsconfine"
	"github.com/monoes/mono-agent/internal/jevpick"
)

// startPostJS tags the feed's "Start a post" control.
const startPostJS = `(tok) => {
	L.unmarkAll(tok);
	const c = document.querySelector('button.share-box-feed-entry__trigger, button.artdeco-button.share-box-feed-entry__trigger')
		|| (() => { const d = document.querySelector('[aria-label="Start a post"]'); return d ? (d.closest('[role="button"], button') || d) : null; })()
		|| Array.from(document.querySelectorAll('button, [role="button"]')).find((b) => /^Start a post/i.test(L.text(b)));
	if (!c) return { state: 'none' };
	L.mark(c, tok);
	return { state: 'ready' };
}`

// shareDialogJS tags the share dialog's editor (tok) and reports the dialog.
const shareDialogJS = `(tok) => {
	L.unmarkAll(tok);
	const dialogs = Array.from(document.querySelectorAll('[role="dialog"], .share-box, .share-creation-state')).filter((d) => L.vis(d));
	for (const d of dialogs) {
		const ed = Array.from(d.querySelectorAll('div.ql-editor[contenteditable="true"], [role="textbox"][contenteditable="true"], [contenteditable="true"][aria-label]')).find((e) => L.vis(e) && !e.closest('.ql-clipboard'));
		if (ed) { L.mark(ed, tok); return { ok: true, state: 'ready' }; }
	}
	return { ok: false, state: dialogs.length ? 'no_editor' : 'no_dialog' };
}`

// postButtonJS tags the dialog's Post button (tok) — only inside the dialog
// holding the editor edTok.
const postButtonJS = `(edTok, tok) => {
	L.unmarkAll(tok);
	const ed = L.marked(edTok);
	if (!ed) return { state: 'no_editor' };
	const d = ed.closest('[role="dialog"], .share-box, .share-creation-state');
	if (!d) return { state: 'no_dialog' };
	const btns = Array.from(d.querySelectorAll('button')).filter((b) => L.vis(b));
	const cands = btns.filter((b) => b.classList.contains('share-actions__primary-action') || /^Post$/i.test(L.text(b)) || /^Post$/i.test((b.getAttribute('aria-label') || '').trim()));
	if (cands.length !== 1) return { state: cands.length ? 'ambiguous' : 'no_button', count: cands.length };
	L.mark(cands[0], tok);
	if (cands[0].disabled || cands[0].getAttribute('aria-disabled') === 'true') return { state: 'disabled' };
	return { state: 'ready' };
}`

// publishedJS reports whether the share dialog closed and LinkedIn
// confirmed the post (a toast, or the post text showing in the feed).
const publishedJS = `(edTok, snip) => {
	const ed = L.marked(edTok);
	const open = !!ed && L.vis(ed);
	const norm = (s) => s.replace(/\s+/g, ' ').trim();
	const toast = Array.from(document.querySelectorAll('.artdeco-toast-item, [role="alert"], #artdeco-toasts, [data-testid*="toast"]')).some((t) => L.vis(t) && /post/i.test(L.text(t)));
	const inFeed = Array.from(document.querySelectorAll('.update-components-text, [data-testid="expandable-text-box"], .feed-shared-update-v2__description')).some((e) => norm(L.text(e)).includes(snip));
	const toastLink = Array.from(document.querySelectorAll('.artdeco-toast-item a[href*="/feed/update/"], [role="alert"] a[href*="/feed/update/"]')).map((a) => a.href)[0] || '';
	return { ok: !open && (toast || inFeed), open, toast, inFeed, url: toastLink };
}`

// PublishPost publishes a text post (optionally with media files) from the
// feed's share box and verifies LinkedIn confirmed it.
func (b *LinkedInBot) PublishPost(ctx context.Context, page browser.PageInterface, text, media string) (map[string]interface{}, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("linkedin: post text is required")
	}
	var files []string
	for _, f := range strings.Split(media, ",") {
		if f = strings.TrimSpace(f); f != "" {
			files = append(files, f)
		}
	}
	if len(files) > 0 {
		confined, err := fsconfine.Paths(ctx, files)
		if err != nil {
			return nil, fmt.Errorf("linkedin: media: %w", err)
		}
		files = confined
	}
	if err := navigate(ctx, page, "https://www.linkedin.com/feed/"); err != nil {
		return nil, err
	}
	tok := newToken("share")
	defer unmark(page, tok)
	defer unmark(page, tok+"-start")
	var st reactState
	_ = poll(ctx, findTimeout, func() (bool, error) {
		if err := run(page, startPostJS, &st, tok+"-start"); err != nil {
			return false, nil
		}
		return st.State == "ready", nil
	})
	if st.State != "ready" {
		pick, jerr := b.JevElement(ctx, page, jevpick.Target{Kind: "click", Intent: "the \"Start a post\" box at the top of the LinkedIn feed that opens the post composer"})
		if jerr != nil {
			return nil, fmt.Errorf("linkedin: \"Start a post\" not found on the feed (jev fallback: %v)", jerr)
		}
		var ok bool
		err := run(page, `(m, tok) => { const el = document.querySelector('[data-monoagent-jev="' + m + '"]'); if (!el) return false; L.unmarkAll(tok); L.mark(el, tok); return true; }`, &ok, pick.Marker, tok+"-start")
		pick.Release()
		if err != nil || !ok {
			return nil, fmt.Errorf("linkedin: jev pick for \"Start a post\" vanished")
		}
	}
	if err := clickMarked(page, tok+"-start"); err != nil {
		return nil, fmt.Errorf("linkedin: failed to open the post composer: %w", err)
	}
	var dlg struct {
		OK    bool   `json:"ok"`
		State string `json:"state"`
	}
	if err := waitFor(ctx, page, verifyTimeout, shareDialogJS, &dlg, tok); err != nil {
		return nil, fmt.Errorf("linkedin: the post composer did not open (%s): %w", dlg.State, err)
	}
	if err := typeMarked(ctx, page, tok, text); err != nil {
		return nil, fmt.Errorf("linkedin: typing the post failed: %w", err)
	}
	if len(files) > 0 {
		if err := b.attachMedia(ctx, page, tok, files); err != nil {
			return nil, err
		}
	}

	postTok := tok + "-post"
	defer unmark(page, postTok)
	_ = poll(ctx, 5*time.Second, func() (bool, error) {
		if err := run(page, postButtonJS, &st, tok, postTok); err != nil {
			return false, nil
		}
		return st.State == "ready", nil
	})
	if st.State != "ready" {
		pick, jerr := b.JevElement(ctx, page, jevpick.Target{Kind: "click", Intent: "the Post button that publishes the post in the open LinkedIn post composer dialog"})
		if jerr != nil {
			return nil, fmt.Errorf("linkedin: the composer's Post button is not usable (%s, %d candidates) (jev fallback: %v)", st.State, st.Count, jerr)
		}
		var ok bool
		err := run(page, `(m, edTok, tok) => {
			const el = document.querySelector('[data-monoagent-jev="' + m + '"]');
			const ed = L.marked(edTok);
			if (!el || !ed) return false;
			const d = ed.closest('[role="dialog"], .share-box, .share-creation-state');
			const b = el.closest('button') || el;
			if (!d || !d.contains(b) || b.disabled) return false;
			L.unmarkAll(tok); L.mark(b, tok); return true;
		}`, &ok, pick.Marker, tok, postTok)
		pick.Release()
		if err != nil || !ok {
			return nil, fmt.Errorf("linkedin: jev picked a button outside the composer; not posting")
		}
	}
	if err := clickMarked(page, postTok); err != nil {
		return nil, fmt.Errorf("linkedin: failed to click Post: %w", err)
	}
	var done struct {
		OK     bool   `json:"ok"`
		Open   bool   `json:"open"`
		Toast  bool   `json:"toast"`
		InFeed bool   `json:"inFeed"`
		URL    string `json:"url"`
	}
	if err := waitFor(ctx, page, 2*verifyTimeout, publishedJS, &done, tok, snippet(text)); err != nil {
		_ = run(page, publishedJS, &done, tok, snippet(text))
		return nil, fmt.Errorf("linkedin: post not confirmed (composer open=%v, toast=%v, in feed=%v): %w", done.Open, done.Toast, done.InFeed, err)
	}
	return map[string]interface{}{"success": true, "post_url": done.URL, "media_count": len(files)}, nil
}

// attachMedia uploads files through the composer's media button and
// confirms the media editor (Next/Done) so the composer shows them.
func (b *LinkedInBot) attachMedia(ctx context.Context, page browser.PageInterface, edTok string, files []string) error {
	btnTok := edTok + "-media"
	defer unmark(page, btnTok)
	var found bool
	_ = run(page, `(edTok, tok) => {
		const ed = L.marked(edTok);
		const d = ed && ed.closest('[role="dialog"], .share-box, .share-creation-state');
		if (!d) return false;
		if (d.querySelector('input[type="file"]')) return true;
		const b = Array.from(d.querySelectorAll('button')).find((x) => /^Add (media|a photo|photo)/i.test((x.getAttribute('aria-label') || '').trim()));
		if (!b) return false;
		L.mark(b, tok); return true;
	}`, &found, edTok, btnTok)
	var hasBtn bool
	_ = run(page, `(tok) => !!L.marked(tok)`, &hasBtn, btnTok)
	if hasBtn {
		if err := clickMarked(page, btnTok); err != nil {
			return fmt.Errorf("linkedin: failed to open the media picker: %w", err)
		}
		_ = sleepCtx(ctx, uiSettle)
	}
	input, err := page.Element(`input[type="file"]`, 5*time.Second)
	if err != nil || input == nil {
		return fmt.Errorf("linkedin: no file input for media upload: %v", err)
	}
	if err := input.SetFiles(files); err != nil {
		return fmt.Errorf("linkedin: media upload: %w", err)
	}
	// The media editor asks to confirm with Next or Done before returning
	// to the text composer.
	nextTok := edTok + "-next"
	defer unmark(page, nextTok)
	for i := 0; i < 2; i++ {
		var ok bool
		wait := 5 * time.Second
		if i > 0 {
			wait = 3 * uiSettle
		}
		_ = poll(ctx, wait, func() (bool, error) {
			if err := run(page, `(tok) => {
				L.unmarkAll(tok);
				const b = Array.from(document.querySelectorAll('[role="dialog"] button')).find((x) => L.vis(x) && !x.disabled && /^(Next|Done)$/i.test(L.text(x)));
				if (!b) return false;
				L.mark(b, tok); return true;
			}`, &ok, nextTok); err != nil {
				return false, nil
			}
			return ok, nil
		})
		if !ok {
			break
		}
		if err := clickMarked(page, nextTok); err != nil {
			return fmt.Errorf("linkedin: failed to confirm the media: %w", err)
		}
		_ = sleepCtx(ctx, uiSettle)
	}
	var dlg struct {
		OK bool `json:"ok"`
	}
	if err := waitFor(ctx, page, verifyTimeout, `(tok) => ({ ok: !!L.marked(tok) && L.vis(L.marked(tok)) })`, &dlg, edTok); err != nil {
		return fmt.Errorf("linkedin: the composer did not come back after adding media: %w", err)
	}
	return nil
}
