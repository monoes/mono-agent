package recording

import (
	"regexp"
	"strings"
)

// Privacy re-check (spec §8.2). The recorder already withholds these
// values; ingest checks again because the extension is not the only thing
// that can write to this socket, and a value that reached disk is a value
// that reaches the analyzer's prompt.

// SensitiveTarget reports whether a fingerprint names a field whose value
// must never be stored: a password or hidden input, or one the page marks
// with a card / password autocomplete token.
func SensitiveTarget(fp *Fingerprint) bool {
	if fp == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(fp.InputType)) {
	case "password", "hidden":
		return true
	}
	for _, tok := range strings.Fields(strings.ToLower(fp.Autocomplete)) {
		if strings.HasPrefix(tok, "cc-") || tok == "current-password" || tok == "new-password" || tok == "one-time-code" {
			return true
		}
	}
	return false
}

// Sanitize drops the value of an event on a sensitive field and marks it
// Masked.
func Sanitize(ev *Event) {
	if ev == nil || !SensitiveTarget(ev.Target) {
		return
	}
	if ev.Value != "" || ev.Type == EvType || ev.Type == EvSelect {
		ev.Value = ""
		ev.Masked = true
	}
}

var (
	inputTag      = regexp.MustCompile(`(?is)<input\b[^>]*>`)
	sensitiveAttr = regexp.MustCompile(`(?is)\btype\s*=\s*["']?\s*(password|hidden)\b|\bautocomplete\s*=\s*["']?[^"'>]*\b(cc-[a-z-]+|current-password|new-password|one-time-code)\b`)
	valueAttr     = regexp.MustCompile(`(?is)\s+value\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
)

// ScrubHTML removes value attributes from sensitive <input> tags in a DOM
// snippet.
func ScrubHTML(html string) string {
	return inputTag.ReplaceAllStringFunc(html, func(tag string) string {
		if !sensitiveAttr.MatchString(tag) {
			return tag
		}
		return valueAttr.ReplaceAllString(tag, "")
	})
}
