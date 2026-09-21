package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/capture"
	"github.com/monoes/mono-agent/internal/extension"
)

// newCaptureCmd returns the `capture` command group: save a page from the
// user's real, logged-in Chrome into the monomind inbox
// (~/.monomind/inbox/<timestamp>-<slug>/), and list what is already there.
//
// The capture itself happens in the browser — the extension holds the
// `debugger` permission, so an MHTML snapshot and a print-to-PDF of a page
// the user is signed into need no new permission and no second, cookie-less
// browser. This side sends the command, reassembles what comes back (large
// artifacts arrive in chunks), and writes the envelope. See
// docs/BROWSER_TRACK_PLAN.md and internal/capture.
func newCaptureCmd(cfg *globalConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "capture",
		Short: "Capture web pages from your browser into the monomind inbox",
	}
	cmd.AddCommand(
		newCapturePageCmd(cfg),
		newCaptureListCmd(cfg),
	)
	cmd.AddCommand(captureGlueCmds(cfg)...) // export, import, task — see capture_archive.go
	return cmd
}

// knownCaptureFormats are the artifact kinds the extension can produce.
var knownCaptureFormats = []string{"mhtml", "pdf", "readable", "screenshot"}

func newCapturePageCmd(cfg *globalConfig) *cobra.Command {
	var (
		tab        int
		formats    []string
		selection  bool
		note       string
		tags       []string
		collection string
		out        string
		timeout    time.Duration
	)
	cmd := &cobra.Command{
		Use:   "page",
		Short: "Capture a browser tab into the inbox",
		Long: "Captures a tab in your real Chrome — cookies, session and all — and writes\n" +
			"the capture envelope (page.mhtml, page.pdf, readable.md, screenshot.png and\n" +
			"meta.json) into ~/.monomind/inbox/<timestamp>-<slug>/.\n" +
			"\n" +
			"With no --tab, the active tab is captured. Formats the page cannot produce\n" +
			"are reported as warnings and simply left out of the envelope; the capture\n" +
			"still lands.",
		Example: "  monoagentcli capture page\n" +
			"  monoagentcli capture page --formats mhtml,readable --note \"for the Q4 memo\" --tag research\n" +
			"  monoagentcli capture page --tab 42 --selection --collection reading --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			if tab < 0 {
				return errInvalidInput("--tab must be a positive Chrome tab id, got %d", tab)
			}
			formats, err := normalizeCaptureFormats(formats)
			if err != nil {
				return err
			}

			bridge := setupExtensionBridge(newExtensionBridgeLogger(), 3*time.Second)
			capturer, ok := bridge.(extension.Capturer)
			if !ok {
				return fmt.Errorf("this extension bridge cannot capture pages (%T)", bridge)
			}
			if err := ensureExtensionConnected(bridge, 30*time.Second); err != nil {
				return errAuthConnection("%v", err)
			}

			res, err := capturer.CapturePage(extension.CaptureRequest{
				TabID:      tab,
				Formats:    formats,
				Selection:  selection,
				Note:       note,
				Tags:       tags,
				Collection: collection,
				Timeout:    timeout,
				Inbox:      expandPath(out),
			})
			if err != nil {
				return fmt.Errorf("capturing page: %w", err)
			}

			if cfg.JSONOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}
			for _, w := range res.Warnings {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Captured %s (%s) — %s\n",
				res.Meta.DedupeURL(), strings.Join(res.Artifacts, ", "), captureHumanBytes(res.Bytes))
			fmt.Fprintln(cmd.OutOrStdout(), res.Path)
			return nil
		},
	}
	cmd.Flags().IntVar(&tab, "tab", 0, "Chrome tab id to capture (default: the active tab)")
	cmd.Flags().StringSliceVar(&formats, "formats", nil,
		"Artifacts to capture: "+strings.Join(knownCaptureFormats, ", ")+" (default: all of them)")
	cmd.Flags().BoolVar(&selection, "selection", false, "Capture only the current selection, not the whole page")
	cmd.Flags().StringVar(&note, "note", "", "Note to store alongside the capture")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "Tag to store alongside the capture (repeatable)")
	cmd.Flags().StringVar(&collection, "collection", "", "Collection to file the capture under")
	cmd.Flags().StringVar(&out, "out", "", "Inbox directory to write into (default: ~/.monomind/inbox)")
	cmd.Flags().DurationVar(&timeout, "timeout", extension.DefaultCaptureTimeout,
		"How long to wait for the browser to finish the capture")
	return cmd
}

func newCaptureListCmd(cfg *globalConfig) *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the captures waiting in the inbox",
		Long: "Lists the capture envelopes in ~/.monomind/inbox, newest first.\n" +
			"\n" +
			"Captures still being written (staging directories, and any directory\n" +
			"without a meta.json) are skipped: a capture is only real once it has been\n" +
			"renamed into place, which is what keeps a watcher from ingesting half of\n" +
			"one.",
		Example: "  monoagentcli capture list\n  monoagentcli capture list --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			inbox := expandPath(out)
			if inbox == "" {
				inbox = capture.DefaultInbox()
			}
			entries, err := capture.List(inbox)
			if err != nil {
				return fmt.Errorf("reading inbox: %w", err)
			}

			if cfg.JSONOutput {
				if entries == nil {
					entries = []capture.Entry{}
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(entries)
			}
			if len(entries) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "No captures in %s\n", inbox)
				return nil
			}
			table := newPlainTable(cmd.OutOrStdout(), []string{"CAPTURED", "TITLE", "URL", "SIZE", "PATH"}, nil)
			for _, e := range entries {
				_ = table.Append([]string{
					e.CapturedAt,
					truncateCaptureCell(e.Title, 40),
					truncateCaptureCell(e.URL, 60),
					captureHumanBytes(e.Bytes),
					e.Path,
				})
			}
			return table.Render()
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "Inbox directory to read (default: ~/.monomind/inbox)")
	return cmd
}

// normalizeCaptureFormats validates --formats and drops duplicates, so a
// typo fails here with exit code 3 rather than reaching the browser and
// coming back as a capture that is quietly missing an artifact.
func normalizeCaptureFormats(formats []string) ([]string, error) {
	if len(formats) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(formats))
	out := make([]string, 0, len(formats))
	for _, raw := range formats {
		f := strings.ToLower(strings.TrimSpace(raw))
		if f == "" {
			continue
		}
		if !slicesContains(knownCaptureFormats, f) {
			return nil, errInvalidInput("unknown --formats value %q; want one of: %s",
				raw, strings.Join(knownCaptureFormats, ", "))
		}
		if seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil, errInvalidInput("--formats was given but named no formats")
	}
	sort.Strings(out)
	return out, nil
}

func slicesContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// captureHumanBytes renders a byte count the way a person reads it.
func captureHumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

// truncateCaptureCell keeps one table cell from wrapping the whole row.
func truncateCaptureCell(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
