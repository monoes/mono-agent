package orgdecide

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/orgdesign"
)

// Verdict is a decider's reply.
type Verdict struct {
	Verdict   string `json:"verdict"` // approve | deny | answer | escalate
	Answer    string `json:"answer,omitempty"`
	Rationale string `json:"rationale"`
}

// Outcome is one decider call.
type Outcome struct {
	Verdict  Verdict
	CostUSD  float64
	Latency  time.Duration
	Resolver string // e.g. model:claude-fable-5-1
}

// DeciderImpl resolves one item.
type DeciderImpl interface {
	Decide(ctx context.Context, p Prompt) (Outcome, error)
}

// AllowedVerdicts lists what a decider may answer for an item kind at a
// level; escalate exists only at mid, where a human is still in the loop.
func AllowedVerdicts(kind, level string) []string {
	var v []string
	if kind == KindQuestion {
		v = []string{"answer"}
	} else {
		v = []string{"approve", "deny"}
	}
	if level == orgdesign.LevelMid {
		v = append(v, "escalate")
	}
	return v
}

// ParseVerdict extracts the first JSON object from text and checks it fits
// the allowed verdicts. Anything else is a failure (plan §7.7).
func ParseVerdict(text string, allowed []string) (Verdict, error) {
	obj, err := firstJSONObject(text)
	if err != nil {
		return Verdict{}, err
	}
	var v Verdict
	if err := json.Unmarshal([]byte(obj), &v); err != nil {
		return Verdict{}, fmt.Errorf("unusable decider reply: %w", err)
	}
	v.Verdict = strings.ToLower(strings.TrimSpace(v.Verdict))
	ok := false
	for _, a := range allowed {
		if a == v.Verdict {
			ok = true
		}
	}
	if !ok {
		return Verdict{}, fmt.Errorf("decider verdict %q is not one of %s", v.Verdict, strings.Join(allowed, ", "))
	}
	if v.Verdict == "answer" && strings.TrimSpace(v.Answer) == "" {
		return Verdict{}, errors.New("decider chose answer but gave no answer")
	}
	return v, nil
}

func firstJSONObject(s string) (string, error) {
	start := strings.IndexByte(s, '{')
	for start >= 0 {
		depth, inStr, esc := 0, false, false
		for i := start; i < len(s); i++ {
			c := s[i]
			switch {
			case esc:
				esc = false
			case c == '\\' && inStr:
				esc = true
			case c == '"':
				inStr = !inStr
			case inStr:
			case c == '{':
				depth++
			case c == '}':
				depth--
				if depth == 0 {
					cand := s[start : i+1]
					if json.Valid([]byte(cand)) {
						return cand, nil
					}
					i = len(s)
				}
			}
		}
		next := strings.IndexByte(s[start+1:], '{')
		if next < 0 {
			break
		}
		start += 1 + next
	}
	return "", errors.New("unusable decider reply: no JSON object")
}

// ExecFunc runs one agent turn (monomind.Exec in production).
type ExecFunc func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error)

// ModelDecider (a DeciderImpl) is a stateless one-shot `monomind agent exec` with no tools
// (U17 "model").
type ModelDecider struct {
	Runtime string
	Model   string
	Timeout time.Duration
	Exec    ExecFunc
}

// Decide implements Decider.
func (d *ModelDecider) Decide(ctx context.Context, p Prompt) (Outcome, error) {
	exec := d.Exec
	if exec == nil {
		exec = monomind.Exec
	}
	started := time.Now()
	resolver := "model:" + d.Model
	var assistant strings.Builder
	res, err := exec(ctx, monomind.ExecOptions{
		Runtime:      d.Runtime,
		Model:        d.Model,
		Prompt:       p.User,
		SystemPrompt: p.System,
		Timeout:      d.Timeout,
	}, func(ev monomind.Event) {
		if ev.Type == monomind.EventAssistant {
			assistant.WriteString(ev.Text)
		}
	})
	out := Outcome{Latency: time.Since(started), Resolver: resolver}
	if err != nil {
		return out, fmt.Errorf("decider %s: %w", resolver, err)
	}
	out.CostUSD = res.CostUSD
	if res.Err != nil {
		return out, fmt.Errorf("decider %s: %s", resolver, res.Err.Error())
	}
	text := res.ResultText
	if strings.TrimSpace(text) == "" {
		text = assistant.String()
	}
	v, err := ParseVerdict(text, p.Allowed)
	if err != nil {
		return out, err
	}
	out.Verdict = v
	return out, nil
}
