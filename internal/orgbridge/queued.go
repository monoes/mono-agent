package orgbridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/orgdesign"
)

// QueuedMessage is one message waiting in a stopped org's offline queue
// (monomind's `<root>/.monomind/orgs/<org>/inbox.jsonl`). monomind drains
// that queue only when the org starts, so a reply or `org send` to a stopped
// org waits there until then (C-35).
type QueuedMessage struct {
	MessageID string `json:"messageId,omitempty"`
	From      string `json:"from"`
	To        string `json:"to"`
	Subject   string `json:"subject"`
	Body      string `json:"body"`
	Trace     *Trace `json:"trace,omitempty"`
	TS        int64  `json:"ts"`
	QueuedAt  string `json:"queued_at,omitempty"`
	// Endpoint marks an entry for an automation role; monomind retries it
	// by POST while the org runs instead of draining it into a mailbox.
	Endpoint bool `json:"endpoint"`
	// Draining marks an entry from an interrupted drain (`.draining`),
	// which monomind recovers at the next drain.
	Draining bool `json:"draining,omitempty"`
}

// rawQueued is monomind's QueuedMessage (orgrt/inbox.ts) as written to disk.
type rawQueued struct {
	FromQualified string `json:"fromQualified"`
	ToRole        string `json:"toRole"`
	Subject       string `json:"subject"`
	Body          string `json:"body"`
	TS            int64  `json:"ts"`
	MessageID     string `json:"messageId"`
	Endpoint      bool   `json:"endpoint"`
}

// InboxPath is monomind's offline queue file for org under root.
func InboxPath(root, org string) (string, error) {
	if !orgdesign.ValidOrgName(org) {
		return "", fmt.Errorf("invalid org name %q", org)
	}
	return filepath.Join(orgdesign.OrgsDir(root), org, "inbox.jsonl"), nil
}

// ReadQueued lists org's queued messages the way monomind's peekInbox does:
// an interrupted drain first, then the pending file. The files are monomind's
// and are only ever opened for reading here — mono-agent never rewrites them.
// skipped counts lines that are not valid JSON (monomind skips them too).
func ReadQueued(root, org string) (msgs []QueuedMessage, skipped int, err error) {
	path, err := InboxPath(root, org)
	if err != nil {
		return nil, 0, err
	}
	msgs = []QueuedMessage{}
	for _, f := range []struct {
		path     string
		draining bool
	}{{path + ".draining", true}, {path, false}} {
		raw, err := os.ReadFile(f.path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, 0, fmt.Errorf("reading %s: %w", f.path, err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var q rawQueued
			if json.Unmarshal([]byte(line), &q) != nil {
				skipped++
				continue
			}
			msgs = append(msgs, toQueued(q, f.draining))
		}
	}
	return msgs, skipped, nil
}

func toQueued(q rawQueued, draining bool) QueuedMessage {
	m := QueuedMessage{
		MessageID: q.MessageID, From: q.FromQualified, To: q.ToRole, Subject: q.Subject,
		Body: q.Body, TS: q.TS, Endpoint: q.Endpoint, Draining: draining,
	}
	if tr, ok := ParseTrace(q.Body); ok {
		m.Trace = &tr
		m.Body = StripTrace(q.Body)
	}
	if q.TS > 0 {
		m.QueuedAt = time.UnixMilli(q.TS).UTC().Format(time.RFC3339)
	}
	return m
}
