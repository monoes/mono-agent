// Package osmenu renders and installs the macOS Quick Action "Add to MonoAgent
// Tasks: <profile>", which files the text selected in any app into one
// profile's task board (spec docs/mastermind/specs/2026-10-05-task-board-design.md,
// section 12). Nothing here needs macOS: rendering, reading and writing a
// bundle take a folder, so the tests run on every OS. The CLI chooses the
// folder (~/Library/Services) and runs the macOS tools.
package osmenu

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MenuPrefix starts every menu item and bundle name this package writes.
const MenuPrefix = "Add to MonoAgent Tasks"

// maxNameRunes caps the profile's name in a menu item and a bundle name, so
// that a bundle name stays well under the 255 bytes a file name may hold.
const maxNameRunes = 48

// hidden mirrors internal/tasks: characters that show nothing but carry text
// (the Unicode tag block, bidi controls, the byte order mark).
func hidden(r rune) bool {
	switch {
	case r >= 0xE0000 && r <= 0xE007F:
		return true
	case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069:
		return true
	}
	return r == 0xFEFF
}

// cleanName is s on one line with no control or hidden character and none of
// the non-characters U+FFFE and U+FFFF (XML cannot hold them), "/" (a submenu
// in the Services menu, a folder in a path) and ":" (shown as "/" by Finder)
// written as "-", and cut to maxNameRunes.
func cleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case r == '/' || r == ':':
			return '-'
		case unicode.IsControl(r), hidden(r), r == 0xFFFE, r == 0xFFFF:
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, "\U0000FFFD"))
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxNameRunes {
		s = strings.TrimRightFunc(string(r[:maxNameRunes-1]), unicode.IsSpace) + "\U00002026"
	}
	return s
}

// menuName is the profile's name as the menu item, the bundle name and the
// notification show it; a name that cleans to nothing is shown as the id.
func menuName(name, id string) string {
	for _, s := range []string{name, id} {
		if c := cleanName(s); c != "" {
			return c
		}
	}
	return "profile"
}

// MenuTitle is the Services menu item: "Add to MonoAgent Tasks: <name>".
func MenuTitle(name, id string) string { return MenuPrefix + ": " + menuName(name, id) }

// BundleName is the bundle's folder: "Add to MonoAgent Tasks (<name>).workflow".
func BundleName(name, id string) string {
	return MenuPrefix + " (" + menuName(name, id) + ").workflow"
}

// bundleID is a bundle's CFBundleIdentifier: one per profile id, made only of
// characters an identifier may hold.
func bundleID(profileID string) string {
	sum := sha256.Sum256([]byte(profileID))
	return "com.monoagent.tasks.menu." + hex.EncodeToString(sum[:6])
}

// shellQuote quotes s for a POSIX shell: nothing inside single quotes is
// special, and a single quote is written as quote, backslash, quote, quote.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// plistEscape escapes the characters XML reserves in text. Quotes, tabs and
// newlines stay as they are, as in Apple's own workflows.
func plistEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// plainValue refuses a value the bundle must carry exactly but cannot: an
// empty one, invalid UTF-8, or a control or hidden character (a property list
// cannot hold most control characters, and a path or an id never needs one).
func plainValue(what, v string) error {
	if v == "" {
		return fmt.Errorf("%s is empty", what)
	}
	if !utf8.ValidString(v) || strings.ContainsFunc(v, func(r rune) bool { return unicode.IsControl(r) || hidden(r) }) {
		return fmt.Errorf("%s %q holds a control or hidden character, or is not UTF-8", what, v)
	}
	return nil
}
