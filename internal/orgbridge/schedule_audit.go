package orgbridge

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/orgdesign"
)

// ScheduleAuditFile is monomind's per-org log of scheduled ticks that did
// not start a run: <root>/.monomind/orgs/<org>/schedule-audit.jsonl, one
// {ts, event, msg} object per line (orgrt/scheduled-run.ts, 2.24+).
const ScheduleAuditFile = "schedule-audit.jsonl"

// maxScheduleAudit bounds what is returned: the newest entries.
const maxScheduleAudit = 200

// Tick outcomes the view groups the monomind events under. An event this
// build does not know is kept as ScheduleOther with its own name.
const (
	ScheduleRefused   = "refused"   // scheduled-start-refused: startOrg threw (host preflight, eval gate, daemon lock, unsigned def)
	ScheduleSkipped   = "skipped"   // scheduled-tick-skipped: a run was already live, the tick yielded
	ScheduleCoalesced = "coalesced" // scheduled-tick-deferred: tick landed mid-run, held for one catch-up run
	ScheduleOther     = "other"
)

// ScheduleAuditEntry is one audit line.
type ScheduleAuditEntry struct {
	TS    int64  `json:"ts"`
	At    string `json:"at,omitempty"` // RFC 3339 UTC of TS
	Event string `json:"event"`        // monomind's event name, verbatim
	Kind  string `json:"kind"`         // refused | skipped | coalesced | other
	Msg   string `json:"msg"`
}

// ScheduleAuditView is what `org schedule-audit` prints.
type ScheduleAuditView struct {
	V       int                  `json:"v"`
	Org     string               `json:"org"`
	Entries []ScheduleAuditEntry `json:"entries"` // newest first
	Total   int                  `json:"total"`   // lines read, before the cap
}

// ScheduleAuditKind maps a monomind event name to its outcome group.
func ScheduleAuditKind(event string) string {
	switch event {
	case "scheduled-start-refused":
		return ScheduleRefused
	case "scheduled-tick-skipped":
		return ScheduleSkipped
	case "scheduled-tick-deferred":
		return ScheduleCoalesced
	}
	return ScheduleOther
}

// ReadScheduleAudit reads the org's schedule-audit.jsonl read-only. A
// missing file is an empty view (a refused start of an unsigned scheduled
// org leaves no line at all — monomind only logs that to `org serve`'s
// stdout). Lines that do not parse, and events this build does not know,
// are never fatal: the former are skipped, the latter shown as "other".
func ReadScheduleAudit(root, org string) (ScheduleAuditView, error) {
	if !orgdesign.ValidOrgName(org) {
		return ScheduleAuditView{}, fmt.Errorf("invalid org name %q", org)
	}
	v := ScheduleAuditView{V: 1, Org: org, Entries: []ScheduleAuditEntry{}}
	f, err := os.Open(filepath.Join(orgdesign.OrgsDir(root), org, ScheduleAuditFile))
	if err != nil {
		return v, nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	var all []ScheduleAuditEntry
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e ScheduleAuditEntry
		if json.Unmarshal([]byte(line), &e) != nil || e.Event == "" {
			continue
		}
		e.Kind = ScheduleAuditKind(e.Event)
		if e.TS > 0 {
			e.At = time.UnixMilli(e.TS).UTC().Format(time.RFC3339)
		}
		all = append(all, e)
	}
	v.Total = len(all)
	if len(all) > maxScheduleAudit {
		all = all[len(all)-maxScheduleAudit:]
	}
	for i := len(all) - 1; i >= 0; i-- {
		v.Entries = append(v.Entries, all[i])
	}
	return v, nil
}
