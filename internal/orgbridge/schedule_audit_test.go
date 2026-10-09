package orgbridge

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func writeAudit(t *testing.T, content string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".monomind", "orgs", "sched")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ScheduleAuditFile), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// The line monomind 2.24.1 really wrote (testdata/monomind-2.24.1).
func TestReadScheduleAuditRecorded2241Line(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "monomind", "testdata", "monomind-2.24.1", "schedule-audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := ReadScheduleAudit(writeAudit(t, string(b)), "sched")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Entries) != 1 || v.Entries[0].Event != "scheduled-tick-deferred" || v.Entries[0].Kind != ScheduleCoalesced {
		t.Fatalf("got %+v", v)
	}
	if !strings.Contains(v.Entries[0].Msg, "catch-up run") || v.Entries[0].At != "2026-10-05T18:47:19Z" {
		t.Fatalf("got %+v", v.Entries[0])
	}
}

// Synthetic lines (not monomind output): an unknown event, junk and a torn
// last line never fail the read.
// monomind 2.24.4 (#656) logs an unsigned definition and a failed precheck
// as scheduled-start-refused; the reason reaches the view verbatim.
func TestReadScheduleAuditUnsignedAndPrecheckRefusals(t *testing.T) {
	root := writeAudit(t, strings.Join([]string{
		`{"ts":1000,"event":"scheduled-start-refused","msg":"org \"sched\" is not signed — skipping scheduled run"}`,
		`{"ts":2000,"event":"scheduled-start-refused","msg":"precheck \"disk\" failed — skipping scheduled run: full"}`,
	}, "\n"))
	v, err := ReadScheduleAudit(root, "sched")
	if err != nil || len(v.Entries) != 2 {
		t.Fatalf("view = %+v, %v", v, err)
	}
	for _, e := range v.Entries {
		if e.Kind != ScheduleRefused || !strings.Contains(e.Msg, "skipping scheduled run") {
			t.Errorf("entry = %+v, want a refusal carrying its reason", e)
		}
	}
	if !strings.Contains(v.Entries[0].Msg, "precheck") || !strings.Contains(v.Entries[1].Msg, "not signed") {
		t.Errorf("newest first: %+v", v.Entries)
	}
}

func TestReadScheduleAuditToleratesUnknownAndDamagedLines(t *testing.T) {
	root := writeAudit(t, strings.Join([]string{
		`{"ts":1000,"event":"scheduled-start-refused","msg":"preflight: no runtime"}`,
		`not json`,
		`{"ts":2000,"event":"scheduled-tick-skipped","msg":"a run is already live"}`,
		`{"ts":3000,"event":"scheduled-from-the-future","msg":"?","extra":1}`,
		`{"ts":4000,"msg":"no event"}`,
		`{"ts":5000,"event":"scheduled-tick-deferred","msg":"held"`,
	}, "\n"))
	v, err := ReadScheduleAudit(root, "sched")
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range v.Entries {
		kinds = append(kinds, e.Kind)
	}
	if got := strings.Join(kinds, ","); got != "other,skipped,refused" {
		t.Fatalf("newest first, got %s", got)
	}
	if v.Entries[0].Event != "scheduled-from-the-future" {
		t.Fatalf("unknown event name kept verbatim: %+v", v.Entries[0])
	}
}

func TestReadScheduleAuditMissingFileAndBadName(t *testing.T) {
	v, err := ReadScheduleAudit(t.TempDir(), "sched")
	if err != nil || len(v.Entries) != 0 || v.Entries == nil {
		t.Fatalf("missing file is an empty view: %+v %v", v, err)
	}
	if _, err := ReadScheduleAudit(t.TempDir(), "../x"); err == nil {
		t.Fatal("a path-like org name must be refused")
	}
}

func TestReadScheduleAuditKeepsTheNewest(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= maxScheduleAudit+5; i++ {
		sb.WriteString(`{"ts":` + strconv.Itoa(i) + `,"event":"scheduled-tick-skipped","msg":"m"}` + "\n")
	}
	v, _ := ReadScheduleAudit(writeAudit(t, sb.String()), "sched")
	if len(v.Entries) != maxScheduleAudit || v.Total != maxScheduleAudit+5 || v.Entries[0].TS != int64(maxScheduleAudit+5) {
		t.Fatalf("got %d entries, total %d, first ts %d", len(v.Entries), v.Total, v.Entries[0].TS)
	}
}

// Recorded from monomind 2.24.1 with bwrap off PATH: the host preflight (R6)
// refused the scheduled start. The reason must reach the view verbatim.
func TestReadScheduleAuditRecordedPreflightRefusal(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "monomind", "testdata", "monomind-2.24.1", "schedule-audit-refused.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := ReadScheduleAudit(writeAudit(t, string(b)), "sched")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Entries) != 2 || v.Entries[0].Kind != ScheduleRefused || !strings.Contains(v.Entries[0].Msg, "cannot start on this host") {
		t.Fatalf("got %+v", v)
	}
}

// Recorded from monomind 2.24.1: a tick landing on a run started out of band
// (`org run` while serve is live) yields (skipped), and a tick landing on a
// run the scheduler itself started is held for one catch-up run (coalesced).
func TestReadScheduleAuditRecordedSkippedAndCoalesced(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "monomind", "testdata", "monomind-2.24.1", "schedule-audit-skipped.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := ReadScheduleAudit(writeAudit(t, string(b)), "sched")
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range v.Entries {
		kinds = append(kinds, e.Kind)
	}
	if got := strings.Join(kinds, ","); got != "skipped,skipped,skipped,skipped,coalesced" {
		t.Fatalf("got %s", got)
	}
	if v.Entries[0].Event != "scheduled-tick-skipped" || !strings.Contains(v.Entries[0].Msg, "is already live") {
		t.Fatalf("got %+v", v.Entries[0])
	}
}
