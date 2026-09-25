package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/automation"
	"github.com/monoes/mono-agent/internal/extension"
	"github.com/monoes/mono-agent/internal/recording"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
)

// `automation rerecord <id> <key>` (contracts §9): open the site in the
// user's browser, let them click the element once, and replace the named
// selector with the candidates the extension picked.

// elementPicker is one open tab that can run the extension's picker.
type elementPicker interface {
	Pick(ctx context.Context, prompt string, timeout time.Duration) (*recording.Fingerprint, string, error)
	Close() error
}

// selectorReplacer is Registry.ReplaceSelector (a seam for tests).
type selectorReplacer interface {
	ReplaceSelector(id, key string, e action.SelectorEntry) (string, error)
}

// rerecordOpenPicker opens a tab at url through the same connect path
// `node run` uses. Tests replace it.
var rerecordOpenPicker = func(ctx context.Context, url string, verbose bool) (elementPicker, error) {
	logger := zerolog.New(os.Stderr).With().Timestamp().Str("component", "extension").Logger()
	if !verbose {
		logger = logger.Level(zerolog.WarnLevel)
	}
	bridge := setupExtensionBridge(logger, 3*time.Second)
	if !bridge.IsConnected() {
		if err := ensureExtensionConnected(bridge, 30*time.Second); err != nil {
			return nil, fmt.Errorf("browser bridge not connected: %w", err)
		}
	}
	tabID, err := bridge.CreateTab(url)
	if err != nil {
		return nil, fmt.Errorf("browser bridge not connected: open tab: %w", err)
	}
	pick, ok := pagePicker(bridge.NewPage(tabID))
	if !ok {
		_ = bridge.CloseTab(tabID)
		return nil, errors.New("this build's browser bridge cannot pick elements")
	}
	return &bridgePicker{pick: pick, close: func() error { return bridge.CloseTab(tabID) }}, nil
}

// The extension page must keep satisfying pagePicker's first case.
var _ pickFunc = (*extension.ExtensionPage)(nil).PickElement

type pickFunc func(ctx context.Context, prompt string, timeout time.Duration) (*recording.Fingerprint, string, error)

// pagePicker adapts the extension page's PickElement, which returns either
// the decoded fingerprint or its raw JSON (contracts §9).
func pagePicker(page any) (pickFunc, bool) {
	switch p := page.(type) {
	case interface {
		PickElement(context.Context, string, time.Duration) (*recording.Fingerprint, string, error)
	}:
		return p.PickElement, true
	case interface {
		PickElement(context.Context, string, time.Duration) (json.RawMessage, string, error)
	}:
		return func(ctx context.Context, prompt string, timeout time.Duration) (*recording.Fingerprint, string, error) {
			raw, url, err := p.PickElement(ctx, prompt, timeout)
			if err != nil {
				return nil, "", err
			}
			var fp recording.Fingerprint
			if err := json.Unmarshal(raw, &fp); err != nil {
				return nil, "", fmt.Errorf("decode picked element: %w", err)
			}
			return &fp, url, nil
		}, true
	}
	return nil, false
}

type bridgePicker struct {
	pick  pickFunc
	close func() error
}

func (b *bridgePicker) Pick(ctx context.Context, prompt string, timeout time.Duration) (*recording.Fingerprint, string, error) {
	return b.pick(ctx, prompt, timeout)
}
func (b *bridgePicker) Close() error { return b.close() }

// rerecordResult is the contract JSON.
type rerecordResult struct {
	Automation string                     `json:"automation"`
	Key        string                     `json:"key"`
	Where      string                     `json:"where"` // package | overlay
	Candidates []action.SelectorCandidate `json:"candidates"`
	URL        string                     `json:"url"`
}

func newAutomationRerecordCmd(cfg *globalConfig) *cobra.Command {
	var url string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "rerecord <id> <selectorKey>",
		Short: "Re-record one selector by clicking the element in your browser",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := openAutomationRegistry()
			if err != nil {
				return err
			}
			res, err := rerecordSelector(cmd.Context(), reg, reg, args[0], args[1], url, timeout, cfg.Verbose)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if cfg.JSONOutput {
				return writeJSONTo(out, res)
			}
			fmt.Fprintf(out, "Re-recorded %s selector %s (%d candidate(s), saved in the %s) from %s\n",
				res.Automation, res.Key, len(res.Candidates), res.Where, res.URL)
			return nil
		},
	}
	cmd.Flags().StringVar(&url, "url", "", "Page to open (default: the package's site.startUrl)")
	cmd.Flags().DurationVar(&timeout, "timeout", 180*time.Second, "How long to wait for the click")
	return cmd
}

// packageGetter is the part of the registry rerecord reads.
type packageGetter interface {
	Get(id string) (*automation.Package, error)
}

func rerecordSelector(ctx context.Context, reg packageGetter, rep selectorReplacer, id, key, url string,
	timeout time.Duration, verbose bool) (*rerecordResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	pkg, err := reg.Get(id)
	if err != nil {
		return nil, err
	}
	prev, ok := pkg.Context().Selector(key)
	if !ok {
		return nil, fmt.Errorf("automation %s has no selector %q", id, key)
	}
	if url == "" {
		url = pkg.Manifest.Site.StartURL
	}
	if url == "" {
		return nil, fmt.Errorf("automation %s has no site.startUrl; pass --url", id)
	}
	label := prev.Intent
	if label == "" {
		label = key
	}
	ctx, cancel := context.WithTimeout(ctx, timeout+30*time.Second)
	defer cancel()
	picker, err := rerecordOpenPicker(ctx, url, verbose)
	if err != nil {
		return nil, err
	}
	defer picker.Close()
	fp, pickedURL, err := picker.Pick(ctx, "Click: "+label, timeout)
	if err != nil {
		switch msg := err.Error(); {
		case strings.Contains(msg, "cancelled"):
			return nil, errors.New("cancelled")
		case strings.Contains(msg, "timeout") || errors.Is(err, context.DeadlineExceeded):
			return nil, errors.New("timeout")
		}
		return nil, err
	}
	entry := selectorEntryFromFingerprint(fp, prev.Intent, time.Now())
	if len(entry.Candidates) == 0 {
		return nil, errors.New("the clicked element has no usable selector; try a nearby element")
	}
	where, err := rep.ReplaceSelector(id, key, entry)
	if err != nil {
		return nil, err
	}
	if pickedURL == "" {
		pickedURL = url
	}
	return &rerecordResult{Automation: id, Key: key, Where: where, Candidates: entry.Candidates, URL: pickedURL}, nil
}

// selectorEntryFromFingerprint converts the picked element's candidates:
// unique ones first, each group by score (stable), intent kept, verifiedAt
// now. A sensitive field's text candidates are dropped.
func selectorEntryFromFingerprint(fp *recording.Fingerprint, intent string, now time.Time) action.SelectorEntry {
	e := action.SelectorEntry{Intent: intent, VerifiedAt: now.UTC().Format(time.RFC3339), Candidates: []action.SelectorCandidate{}}
	if fp == nil {
		return e
	}
	cands := append([]recording.Candidate(nil), fp.Candidates...)
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].Unique != cands[j].Unique {
			return cands[i].Unique
		}
		return cands[i].Score > cands[j].Score
	})
	for _, c := range cands {
		sc := action.SelectorCandidate{Score: c.Score}
		switch c.Kind {
		case "css":
			sc.CSS = c.Value
		case "xpath":
			sc.XPath = c.Value
		case "aria":
			if c.Role == "" {
				continue
			}
			sc.Aria = &action.AriaSelector{Role: c.Role, Name: c.Name}
		case "text":
			if fp.Sensitive {
				continue
			}
			sc.Text = c.Value
		default:
			continue
		}
		if sc.CSS == "" && sc.XPath == "" && sc.Aria == nil && sc.Text == "" {
			continue
		}
		e.Candidates = append(e.Candidates, sc)
	}
	return e
}
