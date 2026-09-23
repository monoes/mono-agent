package orgbridge

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/monoes/mono-agent/internal/orggrant"
)

// Trace is the chain position a message or tool call carries (U10).
type Trace struct {
	ChainID string `json:"chain_id"`
	Hop     int    `json:"hop"`
}

var traceLineRe = regexp.MustCompile(`(?m)^\[trace (chn_[A-Za-z0-9_-]+) hop=(\d+)\]\s*$`)

// ParseTrace finds the first `[trace chn_… hop=N]` line in body.
func ParseTrace(body string) (Trace, bool) {
	m := traceLineRe.FindStringSubmatch(body)
	if m == nil {
		return Trace{}, false
	}
	hop, err := strconv.Atoi(m[2])
	if err != nil || hop < 0 {
		return Trace{}, false
	}
	return Trace{ChainID: m[1], Hop: hop}, true
}

// Line renders t as the header line.
func (t Trace) Line() string { return fmt.Sprintf("[trace %s hop=%d]", t.ChainID, t.Hop) }

// NewTrace starts a chain.
func NewTrace() Trace { return Trace{ChainID: orggrant.NewChainID()} }

// WithTrace returns body with t as its first line, replacing any trace line
// already in it (a message is only ever at one position in one chain).
func WithTrace(body string, t Trace) string {
	body = traceLineRe.ReplaceAllString(body, "")
	body = strings.TrimLeft(body, "\n")
	return t.Line() + "\n" + body
}

// StripTrace removes trace lines from body.
func StripTrace(body string) string {
	return strings.TrimLeft(traceLineRe.ReplaceAllString(body, ""), "\n")
}

var chainIDRe = regexp.MustCompile(`^chn_[A-Za-z0-9_-]+$`)

// validChainID reports whether s has the shape of a chain id, the same one
// a trace line accepts.
func validChainID(s string) bool { return chainIDRe.MatchString(s) }
