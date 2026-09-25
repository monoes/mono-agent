//go:build !nosocial

package instagram

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/browser"
	"github.com/monoes/mono-agent/internal/fsconfine"
)

var shareTextRe = regexp.MustCompile(`(?i)^share$`)

// PublishContent creates a feed post: Create → Post → upload mediaPath →
// Next (crop) → Next (edit) → caption (and location) → Share, then waits for
// Instagram's "Your post has been shared". A share that is not confirmed is
// an error. mediaPath must be a local file; in a run an org role started it
// must lie in the role's workdir (fsconfine).
func (b *InstagramBot) PublishContent(ctx context.Context, p browser.PageInterface, mediaPath, caption, locationTag string) error {
	mediaPath = strings.TrimSpace(mediaPath)
	if mediaPath == "" {
		return errors.New("instagram: media path is required")
	}
	if strings.HasPrefix(mediaPath, "http://") || strings.HasPrefix(mediaPath, "https://") {
		return fmt.Errorf("instagram: media must be a local file path, got URL %q", mediaPath)
	}
	files, err := fsconfine.Paths(ctx, []string{mediaPath})
	if err != nil {
		return fmt.Errorf("instagram: %w", err)
	}
	if st, err := os.Stat(files[0]); err != nil || st.IsDir() {
		return fmt.Errorf("instagram: media file %q is not readable: %v", mediaPath, err)
	}

	if err := b.open(ctx, p, "https://www.instagram.com/"); err != nil {
		return err
	}

	// 1. Create (New post).
	cm := newMark()
	err = poll(ctx, findTimeout, func() (bool, error) {
		var found bool
		err := evalJS(p, `(m) => {
			const s = document.querySelector('svg[aria-label="New post"], svg[aria-label="Create"]');
			let b = s && btnOf(s);
			if (!b) b = document.querySelector('a[href*="/create/"]');
			if (!b) b = Array.from(document.querySelectorAll('a, [role="button"], [role="link"]')).find((x) => /^(create|new post)$/i.test(T(x)));
			if (b) mark(b, m);
			return !!b;
		}`, &found, cm)
		return found, err
	})
	if err != nil {
		d, jerr := b.jevPick(ctx, p, "click", "the Create (New post) button in Instagram's navigation", "", cm)
		if jerr != nil || !(strings.Contains(strings.ToLower(d.Text+" "+d.Label+" "+d.SVGLabel), "create") ||
			strings.Contains(strings.ToLower(d.Text+" "+d.Label+" "+d.SVGLabel), "new post")) {
			return fmt.Errorf("instagram: could not find the Create button: %v (jev: %v)", err, jerr)
		}
	}
	if err := clickMarked(p, cm); err != nil {
		return fmt.Errorf("instagram: click Create: %w", err)
	}

	// 2. The Create menu (Post / AI) — pick Post when it is shown — then the
	//    dialog's file input.
	var fileReady bool
	err = poll(ctx, findTimeout, func() (bool, error) {
		pm := newMark()
		var st string
		if err := evalJS(p, `(m) => {
			if (document.querySelector('[role="dialog"] input[type="file"]')) return 'file';
			const opts = Array.from(document.querySelectorAll('a, [role="button"], [role="link"], [role="menuitem"]'))
				.filter((x) => vis(x) && /^post$/i.test(T(x)) && !x.closest('form') && !x.closest('article'));
			if (opts.length) { mark(opts[0], m); return 'menu'; }
			return '';
		}`, &st, pm); err != nil {
			return false, err
		}
		if st == "menu" {
			_ = clickMarked(p, pm)
			return false, nil
		}
		fileReady = st == "file"
		return fileReady, nil
	})
	if err != nil {
		return fmt.Errorf("instagram: the create dialog did not offer a file upload: %w", err)
	}
	fileInput, err := p.Element("[role='dialog'] input[type='file']", 5*time.Second)
	if err != nil || fileInput == nil {
		return fmt.Errorf("instagram: file input vanished: %v", err)
	}
	if err := fileInput.SetFiles(files); err != nil {
		return fmt.Errorf("instagram: failed to set the media file: %w", err)
	}

	// 3. Next (crop) → Next (edit) until the caption box shows.
	capMark := newMark()
	captionReady := func() (bool, error) {
		var found bool
		err := evalJS(p, `(m) => {
			const d = dialogs().find((x) => x.querySelector('[aria-label^="Write a caption"], textarea')) || null;
			if (!d) return false;
			const c = d.querySelector('[contenteditable="true"][aria-label^="Write a caption"], textarea[aria-label^="Write a caption"], [role="textbox"][contenteditable="true"]');
			if (c) mark(c, m);
			return !!c;
		}`, &found, capMark)
		return found, err
	}
	for step := 0; step < 4; step++ {
		if done, _ := captionReady(); done {
			break
		}
		nm := newMark()
		err := poll(ctx, findTimeout, func() (bool, error) {
			var st struct {
				Found    bool   `json:"found"`
				Disabled bool   `json:"disabled"`
				Error    string `json:"error"`
			}
			err := evalJS(p, `(m) => {
				const d = dialogs()[0];
				if (!d) return { found: false };
				const e = T(d).match(/(file couldn.t be uploaded|this file is not supported|couldn.t upload)/i);
				if (e) return { found: false, error: e[1] };
				const b = byText(d, /^next$/i)[0];
				if (!b) return { found: false };
				mark(b, m);
				return { found: true, disabled: isDisabled(b) };
			}`, &st, nm)
			if st.Error != "" {
				return false, stopErr{fmt.Errorf("Instagram: %s", st.Error)}
			}
			if done, _ := captionReady(); done {
				return true, nil
			}
			return st.Found && !st.Disabled, err
		})
		if err != nil {
			return fmt.Errorf("instagram: create dialog stuck before the caption step: %w", err)
		}
		if done, _ := captionReady(); done {
			break
		}
		if err := clickMarked(p, nm); err != nil {
			return fmt.Errorf("instagram: click Next: %w", err)
		}
		if err := pause(ctx, uiPause); err != nil {
			return err
		}
	}
	if err := poll(ctx, findTimeout, captionReady); err != nil {
		d, jerr := b.jevPick(ctx, p, "fill", "the 'Write a caption…' box of the new post in the create dialog", "", capMark)
		if jerr != nil || !d.InDialog {
			return fmt.Errorf("instagram: caption box not found: %v (jev: %v)", err, jerr)
		}
	}

	// 4. Caption.
	if strings.TrimSpace(caption) != "" {
		if err := typeAtEnd(ctx, p, capMark, caption); err != nil {
			return fmt.Errorf("instagram: type caption: %w", err)
		}
	}

	// 5. Location: only a suggestion that matches the requested place.
	if loc := strings.TrimSpace(locationTag); loc != "" {
		lm := newMark()
		var found bool
		if err := evalJS(p, `(m) => { const el = Array.from(document.querySelectorAll('[role="dialog"] input[placeholder^="Add location"], [role="dialog"] input[aria-label^="Add location"]')).find(vis); if (el) mark(el, m); return !!el; }`, &found, lm); err != nil || !found {
			return errors.New("instagram: location field not found — not publishing without the requested location")
		}
		if err := typeAtEnd(ctx, p, lm, loc); err != nil {
			return fmt.Errorf("instagram: type location: %w", err)
		}
		sm := newMark()
		err := poll(ctx, findTimeout, func() (bool, error) {
			var hit bool
			err := evalJS(p, `(want, m) => {
				want = want.toLowerCase();
				const d = dialogs()[0] || document.body;
				const rows = Array.from(d.querySelectorAll('[role="button"], [role="option"], li, button')).filter((x) => vis(x) && !x.querySelector('input'));
				const r = rows.find((x) => { const t = T(x).toLowerCase(); return t.startsWith(want) || t.split(/\s*[,·]\s*/)[0] === want; });
				if (r) mark(r, m);
				return !!r;
			}`, &hit, loc, sm)
			return hit, err
		})
		if err != nil {
			return fmt.Errorf("instagram: no location suggestion matches %q — not publishing without it", loc)
		}
		if err := clickMarked(p, sm); err != nil {
			return fmt.Errorf("instagram: pick location: %w", err)
		}
		if err := pause(ctx, uiPause); err != nil {
			return err
		}
	}

	// 6. Share.
	shm := newMark()
	err = poll(ctx, findTimeout, func() (bool, error) {
		var st struct {
			Found    bool `json:"found"`
			Disabled bool `json:"disabled"`
		}
		err := evalJS(p, `(m) => {
			const d = dialogs()[0];
			const b = d && byText(d, /^share$/i)[0];
			if (!b) return { found: false };
			mark(b, m);
			return { found: true, disabled: isDisabled(b) };
		}`, &st, shm)
		return st.Found && !st.Disabled, err
	})
	if err != nil {
		d, jerr := b.jevPick(ctx, p, "click", "the Share button that publishes the new post in the create dialog", "", shm)
		if jerr != nil || !shareTextRe.MatchString(d.Text) {
			return fmt.Errorf("instagram: Share button not found: %v (jev: %v)", err, jerr)
		}
	}
	if err := clickMarked(p, shm); err != nil {
		return fmt.Errorf("instagram: click Share: %w", err)
	}

	// 7. Confirmation.
	err = poll(ctx, publishTimeout, func() (bool, error) {
		var st struct {
			Shared bool   `json:"shared"`
			Error  string `json:"error"`
		}
		err := evalJS(p, `() => {
			const t = VT();
			const e = t.match(/(post couldn.t be shared|couldn.t share|could not be shared|something went wrong)/i);
			return { shared: /your (post|reel) has been shared|post shared|reel shared/i.test(t) || !!document.querySelector('[role="dialog"] img[alt="Animated checkmark"], [role="dialog"] [aria-label="Animated checkmark"]'),
				error: e ? e[1] : '' };
		}`, &st)
		if st.Error != "" {
			return false, stopErr{fmt.Errorf("Instagram reported: %s", st.Error)}
		}
		return st.Shared, err
	})
	if err != nil {
		return fmt.Errorf("instagram: post not confirmed as shared: %w", err)
	}
	return nil
}
