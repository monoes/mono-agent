package action

// Read-back check for type steps. A driver can report success while typing
// nothing (e.g. a CDP insert that went to the wrong element), which used to
// make a type step pass with an empty field. After typing, the field is read
// back; if the text is not there, the next typing method is tried (focus +
// InsertText, then element Input) and the step fails only when none lands.
//
// The landed rule is deliberately lenient, because fields legitimately
// rewrite what they receive:
//
//   - the value contains the typed text, compared after stripping
//     whitespace and common formatting characters ( ) - . / + and
//     lower-casing (phone masks, upper-casing fields, grouping spaces); or
//   - the value is non-empty and differs from the value before typing
//     (masks that reshape input, autocompletes that replace it); or
//   - the field cannot be read at all (driver without property access,
//     element detached because typing submitted/re-rendered) — unknown is
//     not treated as failure.
//
// A field that is still empty, or unchanged, after every method fails the
// step with "typed text did not reach the field". Values are never logged,
// only their lengths (the text may be a secret).

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/browser"
)

// errTypedTextMissing is the cause of a type step whose text never landed.
var errTypedTextMissing = errors.New("typed text did not reach the field")

// typeReadBackTries/Interval bound how long a read-back waits for a value
// to settle (frameworks may update the DOM a tick after the input event).
const (
	typeReadBackTries    = 3
	typeReadBackInterval = 100 * time.Millisecond
)

// readFieldValue returns the element's value (the "value" property of
// inputs/textareas/selects, else its text content). ok is false when the
// element cannot be read.
func readFieldValue(elem browser.ElementHandle) (val string, ok bool) {
	if elem == nil {
		return "", false
	}
	// A driver/handle without property support must not break typing.
	defer func() {
		if recover() != nil {
			val, ok = "", false
		}
	}()
	if v, err := elem.Property("value"); err == nil && v != nil {
		if s, isStr := v.(string); isStr {
			return s, true
		}
	}
	if s, err := elem.Text(); err == nil {
		return s, true
	}
	return "", false
}

// typedTextLanded applies the lenient rule above.
func typedTextLanded(before, after, text string) bool {
	if strings.TrimSpace(text) == "" {
		return true
	}
	if n := normalizeTyped(text); n != "" && strings.Contains(normalizeTyped(after), n) {
		return true
	}
	return strings.TrimSpace(after) != "" && after != before
}

func normalizeTyped(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch r {
		case ' ', '\t', '\n', '\r', ' ', '(', ')', '-', '.', '/', '+':
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// checkTyped reads the field back (briefly waiting for it to settle) and
// reports whether the text landed.
func checkTyped(elem browser.ElementHandle, before, text string) bool {
	for i := 0; i < typeReadBackTries; i++ {
		after, ok := readFieldValue(elem)
		if !ok || typedTextLanded(before, after, text) {
			return true
		}
		if i < typeReadBackTries-1 {
			time.Sleep(typeReadBackInterval)
		}
	}
	return false
}

// ensureTyped verifies a type step's text reached elem, trying the fallback
// methods when it did not. It returns a failed StepResult, or nil when the
// text landed (or the field cannot be read).
func (ae *ActionExecutor) ensureTyped(step StepDef, elem browser.ElementHandle, before, text string) *StepResult {
	if checkTyped(elem, before, text) {
		return nil
	}
	log := ae.logger.With().Str("stepID", step.ID).Int("textLen", len(text)).Logger()
	fallbacks := []struct {
		name string
		run  func() error
	}{
		{"focus+insertText", func() error {
			_ = elem.Focus()
			return ae.page.InsertText(text)
		}},
		{"element input", func() error { return elem.Input(text) }},
	}
	for _, fb := range fallbacks {
		log.Warn().Str("method", fb.name).Msg("typed text not in field; trying next method")
		if err := fb.run(); err != nil {
			log.Debug().Err(err).Str("method", fb.name).Msg("typing fallback failed")
			continue
		}
		if checkTyped(elem, before, text) {
			return nil
		}
	}
	return &StepResult{
		Success: false,
		StepID:  step.ID,
		Error:   fmt.Errorf("type step %s: %w (%d characters)", step.ID, errTypedTextMissing, len(text)),
	}
}
