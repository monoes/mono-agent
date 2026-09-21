package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// doc.related and doc.ask (RCL-05) — asking the captures a question and
// getting back passages that say where they came from.
//
// Split from knowledge.go, which holds the runner and doc.lookup, purely
// for length. The rules are the same ones stated there: every answer comes
// from the monomind CLI, nothing is ever written, and a missing monomind is
// an absence rather than a failure.

// ---------------------------------------------------------------------------
// doc.related
// ---------------------------------------------------------------------------

func (s *Server) handleDocRelated(ctx context.Context, req *Request, _ ProgressFunc) (any, error) {
	target, ok := docTarget(req.String("url"))
	if target == "" {
		return nil, &RequestError{Code: CodeUnavailable, Err: fmt.Errorf("doc.related needs a url")}
	}
	// The same gate doc.lookup applies, for the same reason: nothing but an
	// http(s) page can have a capture behind it, and anything else reaching
	// the argv below is a flag smuggled in over the extension socket rather
	// than a question about a page. Nothing related is the honest answer.
	if !ok {
		return []map[string]any{}, nil
	}
	limit := clamp(req.Int("limit", 3), 1, askResultLimit)
	out, err := runDoc(ctx, s.knowledgeRunner(), "related",
		[]string{"--limit", strconv.Itoa(limit), "--json"}, target)
	if err != nil {
		return nil, err
	}
	var related []map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &related); err != nil {
		// `doc related` is documented never to throw, so anything
		// unparseable here means an empty brain or a CLI that printed a
		// notice. Neither is worth failing a panel over.
		return []map[string]any{}, nil
	}
	return related, nil
}

// ---------------------------------------------------------------------------
// doc.ask (RCL-05)
// ---------------------------------------------------------------------------

// AskAnswer is one cited passage from the user's own captures.
type AskAnswer struct {
	Title      string  `json:"title,omitempty"`
	Site       string  `json:"site,omitempty"`
	URL        string  `json:"url,omitempty"`
	CiteURL    string  `json:"citeUrl,omitempty"`
	CapturedAt string  `json:"capturedAt,omitempty"`
	Quote      string  `json:"quote"`
	Passage    string  `json:"passage,omitempty"`
	Anchor     string  `json:"anchor,omitempty"`
	Score      float64 `json:"score,omitempty"`
	Path       string  `json:"path,omitempty"`
	Envelope   string  `json:"envelope,omitempty"`
	// Stale marks a citation whose anchor was cut against a version the
	// page no longer is. Shown, never hidden — see citation.ts.
	Stale bool `json:"stale,omitempty"`
}

// AskResult is the whole answer: the hits, plus what could not be resolved.
type AskResult struct {
	Query    string      `json:"query"`
	Answers  []AskAnswer `json:"answers"`
	Warnings []string    `json:"warnings,omitempty"`
}

// searchHit is one row of `monomind doc search --json`. The fused result
// carries several kinds; only `excerpt` rows have a document behind them.
type searchHit struct {
	Kind       string  `json:"kind"`
	FilePath   string  `json:"filePath"`
	Scope      string  `json:"scope"`
	Text       string  `json:"text"`
	Score      float64 `json:"score"`
	ChunkIndex int     `json:"chunkIndex"`
	Anchor     string  `json:"anchor"`
	Provenance struct {
		Title        string `json:"title"`
		URL          string `json:"url"`
		CanonicalURL string `json:"canonicalUrl"`
		CapturedAt   string `json:"capturedAt"`
	} `json:"provenance"`
}

// citation is `monomind doc cite --json` (Citation in citation.ts).
type citation struct {
	Quote      string `json:"quote"`
	Passage    string `json:"passage"`
	Anchor     string `json:"anchor"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	CiteURL    string `json:"citeUrl"`
	CapturedAt string `json:"capturedAt"`
	FilePath   string `json:"filePath"`
	Stale      bool   `json:"stale"`
}

func (s *Server) handleDocAsk(ctx context.Context, req *Request, progress ProgressFunc) (any, error) {
	query := req.String("q")
	if query == "" {
		query = req.String("query")
	}
	if query == "" {
		return nil, &RequestError{Code: CodeUnavailable, Err: fmt.Errorf("doc.ask needs a question")}
	}
	// The question is a flag VALUE (`-q <query>`), which `--` cannot protect:
	// monomind's parser reads a dash-leading value as the next flag and
	// leaves -q set to true (parser.ts, parseFlag), so `-q --config=…` would
	// arrive as a --config the search never asked for. A question has no
	// business starting with a dash, so the dashes go.
	query = strings.TrimSpace(strings.TrimLeft(query, "-"))
	if query == "" {
		return nil, &RequestError{Code: CodeUnavailable, Err: fmt.Errorf("doc.ask needs a question")}
	}
	limit := clamp(req.Int("limit", 4), 1, askResultLimit)
	runner := s.knowledgeRunner()

	progress("searching", query)
	out, err := runDoc(ctx, runner, "search",
		[]string{"-q", query, "--limit", strconv.Itoa(limit), "--json"})
	if err != nil {
		return nil, err
	}
	var hits []searchHit
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &hits); err != nil {
		return nil, fmt.Errorf("doc search did not return JSON: %s", firstLine(string(out)))
	}

	result := &AskResult{Query: query, Answers: []AskAnswer{}}
	// Only document hits. The fused result also carries knowledge-graph
	// triplets and memory rows, which have no capture to cite — and a
	// citation is the entire point of this panel.
	docHits := make([]searchHit, 0, len(hits))
	for _, h := range hits {
		if h.FilePath != "" && (h.Kind == "" || h.Kind == "excerpt") {
			docHits = append(docHits, h)
		}
	}
	if len(docHits) == 0 {
		return result, nil
	}

	for i, hit := range docHits {
		if ctx.Err() != nil {
			result.Warnings = append(result.Warnings, "ran out of time before citing every hit")
			break
		}
		progress("citing", fmt.Sprintf("%d/%d", i+1, len(docHits)))
		answer := s.cite(ctx, runner, hit)
		result.Answers = append(result.Answers, answer)
	}
	return result, nil
}

// cite resolves one search hit to a quotable passage. A citation that will
// not resolve is not dropped: the hit's own chunk text is used instead, so
// the panel shows something honest rather than a shorter list with no
// explanation.
func (s *Server) cite(ctx context.Context, runner Runner, hit searchHit) AskAnswer {
	url := hit.Provenance.CanonicalURL
	if url == "" {
		url = hit.Provenance.URL
	}
	answer := AskAnswer{
		Title:      hit.Provenance.Title,
		URL:        url,
		CapturedAt: hit.Provenance.CapturedAt,
		Quote:      collapse(hit.Text, 320),
		Anchor:     hit.Anchor,
		Score:      hit.Score,
		Path:       hit.FilePath,
		Envelope:   envelopeDir(hit.FilePath),
		Site:       hostOf(url),
	}

	// The path, anchor and scope come from monomind's own search output —
	// not from the caller, but not from this process either, and they land
	// in an argv the same way. Same gate: a flag-shaped anchor or scope is
	// dropped (the chunk index and the default scope still cite the hit),
	// and a flag-shaped path fails runDoc outright, which leaves the answer
	// below quoting the hit's own text.
	flags := []string{"--json"}
	if safeArgValue(hit.Anchor) {
		flags = append(flags, "--anchor", hit.Anchor)
	} else {
		flags = append(flags, "--chunk", strconv.Itoa(hit.ChunkIndex))
	}
	if safeArgValue(hit.Scope) {
		flags = append(flags, "--scope", hit.Scope)
	}
	out, err := runDoc(ctx, runner, "cite", flags, hit.FilePath)
	if err != nil {
		return answer
	}
	var cite citation
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &cite); err != nil {
		return answer
	}
	if cite.Quote != "" {
		answer.Quote = cite.Quote
	}
	answer.Passage = cite.Passage
	if cite.Title != "" {
		answer.Title = cite.Title
	}
	if cite.URL != "" {
		answer.URL = cite.URL
		answer.Site = hostOf(cite.URL)
	}
	if cite.CiteURL != "" {
		answer.CiteURL = cite.CiteURL
	}
	if cite.CapturedAt != "" {
		answer.CapturedAt = cite.CapturedAt
	}
	if cite.Anchor != "" {
		answer.Anchor = cite.Anchor
	}
	answer.Stale = cite.Stale
	return answer
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

// collapse squeezes whitespace and bounds a passage, so a chunk used as a
// fallback quote does not arrive as three screens of markdown.
func collapse(s string, max int) string {
	out := strings.Join(strings.Fields(s), " ")
	if len(out) <= max {
		return out
	}
	return strings.TrimSpace(out[:max]) + "…"
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
