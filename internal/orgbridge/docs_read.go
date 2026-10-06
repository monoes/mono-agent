package orgbridge

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/monoes/mono-agent/internal/orgdesign"
)

// runIDRe keeps a run id a single path segment.
var runIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// maxDocEventsBytes caps how much of events.jsonl is read into memory.
const maxDocEventsBytes = 32 << 20

const docGenesis = "0000000000000000000000000000000000000000000000000000000000000000"

// DocsDir is monomind's per-run document store: <root>/.monomind/orgs/<org>/docs/<run>.
func DocsDir(root, org, run string) (string, error) {
	if !orgdesign.ValidOrgName(org) {
		return "", fmt.Errorf("invalid org name %q", org)
	}
	if run != "" && (!runIDRe.MatchString(run) || strings.Contains(run, "..")) {
		return "", fmt.Errorf("invalid run id %q", run)
	}
	return filepath.Join(orgdesign.OrgsDir(root), org, "docs", run), nil
}

// DocRuns lists the runs that have a document store, newest first (run ids
// start with a timestamp, so a reverse sort is chronological).
func DocRuns(root, org string) []string {
	base, err := DocsDir(root, org, "")
	if err != nil {
		return nil
	}
	ents, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var runs []string
	for _, e := range ents {
		if e.IsDir() && runIDRe.MatchString(e.Name()) {
			runs = append(runs, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(runs)))
	return runs
}

// ReadDocView reads one run's document store, read-only, and builds its view.
// run "" picks the newest run. A missing store is an empty view, not an error:
// an org with no documents (or no run yet) simply has nothing to show. The org
// definition is read for the rework caps and deliverable files; if it cannot
// be read the view is still built from the store alone.
func ReadDocView(root, org, run string) (DocView, error) {
	runs := DocRuns(root, org)
	if run == "" && len(runs) > 0 {
		run = runs[0]
	}
	dir, err := DocsDir(root, org, run)
	if err != nil {
		return DocView{}, err
	}
	in := DocInput{Org: org, Run: run, Runs: runs}
	if run != "" {
		in.Events, in.Integrity = readDocEvents(filepath.Join(dir, "events.jsonl"))
		in.Delivered = readDelivered(filepath.Join(dir, "notices.jsonl"))
	}
	if doc, err := orgdesign.Load(root, org); err == nil {
		applyOrgDef(&in, doc)
	}
	return BuildDocView(in), nil
}

func applyOrgDef(in *DocInput, doc *orgdesign.Doc) {
	in.Caps = map[string]int{}
	for _, s := range doc.Sections {
		in.SectionOrder = append(in.SectionOrder, s.Name)
		if s.MaxReworkRounds != nil && *s.MaxReworkRounds > 0 {
			in.Caps[s.Name] = *s.MaxReworkRounds
		}
	}
	in.Deliverables = map[string][]string{}
	for _, d := range doc.Documents {
		var files []struct {
			File string `json:"file"`
		}
		if json.Unmarshal(d.DeliverableFiles, &files) != nil {
			continue
		}
		for _, f := range files {
			if f.File != "" {
				in.Deliverables[d.Name] = append(in.Deliverables[d.Name], f.File)
			}
		}
	}
}

// readDocEvents parses events.jsonl and verifies monomind's hash chain
// (each line carries the sha-256 of the previous line's text). Like monomind
// it stops at the first line that breaks the chain and reports why; a torn
// final line (a write in progress) is skipped silently.
func readDocEvents(path string) ([]DocEvent, string) {
	fi, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ""
		}
		return nil, "events.jsonl unreadable: " + err.Error()
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return nil, "events.jsonl is a symlink; not read"
	}
	if fi.Size() > maxDocEventsBytes {
		return nil, "events.jsonl is too large to read"
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, "events.jsonl unreadable: " + err.Error()
	}
	var out []DocEvent
	prev, seq := docGenesis, 0
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	complete := bytes.HasSuffix(b, []byte("\n"))
	var lines [][]byte
	for sc.Scan() {
		lines = append(lines, append([]byte(nil), sc.Bytes()...))
	}
	for i, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		last := i == len(lines)-1
		var head struct {
			Seq  int    `json:"seq"`
			Prev string `json:"prev"`
		}
		var e DocEvent
		if json.Unmarshal(line, &head) != nil || json.Unmarshal(line, &e) != nil {
			if last && !complete {
				return out, ""
			}
			return out, fmt.Sprintf("line %d does not parse; showing events up to %d", i+1, seq)
		}
		if head.Seq != seq+1 || head.Prev != prev {
			return out, fmt.Sprintf("event %d does not chain from the line before it; showing events up to %d", head.Seq, seq)
		}
		sum := sha256.Sum256(line)
		prev, seq = hex.EncodeToString(sum[:]), head.Seq
		out = append(out, e)
	}
	return out, ""
}

// readDelivered maps delivered notice keys to their delivery time
// (notices.jsonl, monomind's delivery journal). Damaged lines are skipped.
func readDelivered(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		var r struct {
			T   string `json:"t"`
			Key string `json:"key"`
			At  string `json:"at"`
		}
		if json.Unmarshal(sc.Bytes(), &r) == nil && r.T == "delivered" && r.Key != "" {
			if _, ok := out[r.Key]; !ok {
				out[r.Key] = r.At
			}
		}
	}
	return out
}
