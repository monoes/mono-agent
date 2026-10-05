package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/monoes/mono-agent/internal/capture"
	"github.com/monoes/mono-agent/internal/captureindex"
	"github.com/monoes/mono-agent/internal/capturesummary"
	"github.com/monoes/mono-agent/internal/extension"
)

// Capture summaries ("Save page summary" / "Save video summary" in the
// extension's right-click menu). The bridge that owns the extension
// connection writes each capture as always, then asks an agent runtime for
// summary.md in the background — see internal/capturesummary.
//
// Which runtime: `extension serve --summary-runtime <id>`, else
// $MONOAGENT_SUMMARY_RUNTIME, else "claude". Any id `agent scan --installed`
// lists works (claude, codex, opencode, …); "off" turns summaries off, and
// each capture that asks for one then records that it was not written.
const summaryRuntimeEnv = "MONOAGENT_SUMMARY_RUNTIME"

// summaryRuntimeFlag is `extension serve --summary-runtime`.
var summaryRuntimeFlag string

func configuredSummaryRuntime() string {
	if v := strings.TrimSpace(summaryRuntimeFlag); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv(summaryRuntimeEnv)); v != "" {
		return v
	}
	return capturesummary.DefaultRuntime
}

// captureHooks is what installCaptureSummaries wires into a bridge: the
// summarizer and the capture indexing queue. Close stops both.
type captureHooks struct {
	summaries *capturesummary.Summarizer
	index     *captureindex.Queue
}

// Close cancels a summary or an indexing pass in flight. A pass cut short
// leaves its capture unindexed, and the next bridge's startup sweep takes
// it up again.
func (h *captureHooks) Close() {
	h.summaries.Close()
	h.index.Stop()
}

// sweepIndexAfter starts the bridge's catch-up pass over every profile's
// unindexed captures (sweepCaptureIndexAfter).
func (h *captureHooks) sweepIndexAfter(ctx context.Context, delay time.Duration) {
	sweepCaptureIndexAfter(ctx, h.index, delay)
}

// installCaptureSummaries wires a summarizer, the page-kind classifier and
// the capture indexing queue into srv's after-write hook and returns them.
// logf receives one line per summary and indexing outcome.
//
// The same catalog of installed runtimes backs the extension's "AI for
// summaries" picker (summary.runtimes / summary.models) and the check a
// capture's own runtime choice has to pass, so the picker never offers
// something the summarizer would then refuse.
func installCaptureSummaries(srv *extension.Server, logf func(string, ...any)) *captureHooks {
	runtime := configuredSummaryRuntime()
	catalog := capturesummary.MonomindCatalog()
	sum := capturesummary.New(runtime, capturesummary.ExecRunner(0))
	sum.Catalog = catalog
	sum.Logf = logf
	// Page-kind classification (Jev surface "capture") runs after the
	// summary is queued; it is a no-op unless the capture's profile enabled
	// it, and never makes the capture wait (capture_classify.go).
	classifier := newCaptureClassifier(logf)
	// Indexing makes the capture searchable from Ask and chat within
	// seconds (extension_capture_index.go). Queued like the summary, so
	// the capture is acknowledged as fast as before.
	index := newCaptureIndexQueue(logf)
	// A summary lands minutes after its capture; indexing it is one more
	// pass over the capture's profile.
	sum.OnDone = func(dir string, _ capturesummary.Status) { handleIndexSummary(index, dir) }
	srv.SetAfterWrite(func(res *capture.Result) {
		sum.Handle(res)
		classifier.Handle(res)
		handleIndexCapture(index, res)
	})
	srv.SetSummaryCatalog(catalog, runtime)
	srv.SetBrainStatusSource(captureBrainStatus)
	return &captureHooks{summaries: sum, index: index}
}

// loggerLogf adapts a zerolog logger for installCaptureSummaries.
func loggerLogf(logger zerolog.Logger) func(string, ...any) {
	return func(format string, args ...any) { logger.Info().Msgf(format, args...) }
}

// narrateLogf writes summary outcomes into `extension serve`'s own
// narration, timestamped like its connect/disconnect lines.
func narrateLogf(out io.Writer) func(string, ...any) {
	return func(format string, args ...any) {
		fmt.Fprintf(out, "[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	}
}

// checkSummaryChoice validates `capture page --summary-runtime/--summary-model`
// before anything is sent: the flags only mean something for a capture that
// asks for a summary, and their values must be shaped like a runtime id and a
// model id. Whether the runtime is installed is the bridge's check (it owns
// the scan), and a capture that fails it records why in summary.json.
func checkSummaryChoice(mode, runtime, model string) error {
	if runtime == "" && model == "" {
		return nil
	}
	if mode != "summary" && mode != "video" {
		return errInvalidInput("--summary-runtime and --summary-model need --mode summary or --mode video")
	}
	if runtime != "" && !capturesummary.ValidRuntimeID(runtime) {
		return errInvalidInput("--summary-runtime %q is not an agent runtime id (see `agent scan --installed`)", runtime)
	}
	if model != "" && !capturesummary.ValidModel(model) {
		return errInvalidInput("--summary-model %q is not a model id", model)
	}
	return nil
}
