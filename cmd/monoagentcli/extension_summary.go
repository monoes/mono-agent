package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"

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

// installCaptureSummaries wires a summarizer into srv's after-write hook
// and returns it. logf receives one line per summary outcome.
func installCaptureSummaries(srv *extension.Server, logf func(string, ...any)) *capturesummary.Summarizer {
	sum := capturesummary.New(configuredSummaryRuntime(), capturesummary.ExecRunner(0))
	sum.Logf = logf
	srv.SetAfterWrite(sum.Handle)
	return sum
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
