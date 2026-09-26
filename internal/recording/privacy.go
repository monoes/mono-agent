package recording

import (
	"bytes"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// Privacy re-check (spec §8.2, security review H6/L1). The recorder already
// withholds these values; ingest checks again because the extension is not
// the only thing that can write to this socket, and a value that reached
// disk is a value that reaches the analyzer's prompt.

// sensitiveNameRe matches field names and labels that announce a secret.
// Deliberately broad: masking a harmless field costs a manual input at
// review time, missing a secret costs the secret.
var sensitiveNameRe = regexp.MustCompile(`(?i)pass|pwd|token|secret|(^|[^a-z])(pin|cvv|cvc|csc|otp|ssn|2fa|mfa)([^a-z]|$)`)

// SensitiveTarget reports whether a fingerprint names a field whose value
// must never be stored: the recorder flagged it, it is a password or hidden
// input, the page marks it with a card / password autocomplete token, or
// its name or label says it holds a secret.
func SensitiveTarget(fp *Fingerprint) bool {
	if fp == nil {
		return false
	}
	if fp.Sensitive {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(fp.InputType)) {
	case "password", "hidden":
		return true
	}
	if sensitiveAutocomplete(fp.Autocomplete) {
		return true
	}
	for _, s := range []string{fp.Name, fp.ID, fp.Label, fp.AriaName, fp.Placeholder, fp.TestID} {
		if s != "" && sensitiveNameRe.MatchString(s) {
			return true
		}
	}
	return false
}

func sensitiveAutocomplete(ac string) bool {
	for _, tok := range strings.Fields(strings.ToLower(ac)) {
		if strings.HasPrefix(tok, "cc-") || tok == "current-password" || tok == "new-password" || tok == "one-time-code" {
			return true
		}
	}
	return false
}

// Length caps for free text an event or start frame carries.
const (
	MaxGoalRunes  = 500
	MaxTitleRunes = 300
	MaxNoteRunes  = 500
)

// Sanitize makes an event safe to store: URLs sanitised, the value of a
// sensitive field dropped and marked Masked, free text capped.
func Sanitize(ev *Event) {
	if ev == nil {
		return
	}
	sanitizeEventURLs(ev)
	ev.Note = clipRunes(ev.Note, MaxNoteRunes)
	if !SensitiveTarget(ev.Target) {
		return
	}
	ev.Target.Sensitive = true
	if ev.Value != "" || ev.Type == EvType || ev.Type == EvSelect {
		ev.Value = ""
		ev.Masked = true
	}
}

// clipRunes caps s at n runes.
func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// ScrubHTML rewrites a DOM snippet so it carries no secrets:
//
//   - value attributes of sensitive inputs are removed (every input's, and
//     every textarea's text, when allValues is set — the snippet belongs to
//     an event on a sensitive field);
//   - data-* attributes are removed (apps park tokens and ids in them);
//   - URLs in href/src/action/formaction are sanitised (SanitizeURL);
//   - <script> elements are dropped.
//
// It uses an HTML tokenizer, not a pattern: attribute order, quoting and
// case cannot hide a value from it.
func ScrubHTML(src string, allValues bool) string {
	z := html.NewTokenizer(strings.NewReader(src))
	var out bytes.Buffer
	skip := "" // element whose content is being dropped
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			// io.EOF or a malformed tail: either way, what was
			// rewritten so far is all that is kept.
			return out.String()
		}
		tok := z.Token()
		if skip != "" {
			if tt == html.EndTagToken && tok.Data == skip {
				skip = ""
				if tok.Data != "script" {
					out.WriteString(tok.String())
				}
			}
			continue
		}
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			if tok.Data == "script" {
				if tt == html.StartTagToken {
					skip = "script"
				}
				continue
			}
			sensitive := allValues || sensitiveElement(tok)
			tok.Attr = scrubAttrs(tok, sensitive)
			out.WriteString(tok.String())
			if tok.Data == "textarea" && sensitive && tt == html.StartTagToken {
				skip = "textarea"
			}
		default:
			out.WriteString(tok.String())
		}
	}
}

// sensitiveElement reports whether a form control's value must go.
func sensitiveElement(tok html.Token) bool {
	if tok.Data != "input" && tok.Data != "textarea" {
		return false
	}
	for _, a := range tok.Attr {
		v := strings.ToLower(a.Val)
		switch strings.ToLower(a.Key) {
		case "type":
			if t := strings.TrimSpace(v); t == "password" || t == "hidden" {
				return true
			}
		case "autocomplete":
			if sensitiveAutocomplete(v) {
				return true
			}
		case "style":
			if strings.Contains(v, "-webkit-text-security") {
				return true
			}
		case "name", "id", "aria-label", "placeholder":
			if sensitiveNameRe.MatchString(a.Val) {
				return true
			}
		}
	}
	return false
}

func scrubAttrs(tok html.Token, sensitive bool) []html.Attribute {
	kept := tok.Attr[:0]
	for _, a := range tok.Attr {
		key := strings.ToLower(a.Key)
		switch {
		case strings.HasPrefix(key, "data-"):
			continue
		case key == "value" && sensitive:
			continue
		case key == "href" || key == "src" || key == "action" || key == "formaction":
			a.Val = SanitizeURL(a.Val)
		}
		kept = append(kept, a)
	}
	return kept
}
