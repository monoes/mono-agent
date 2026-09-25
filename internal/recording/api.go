package recording

import "errors"

// ErrNotImplemented marks skeleton functions not yet filled in.
var ErrNotImplemented = errors.New("recording: not implemented")

// List returns recordings in the active profile's inbox, newest first.
func List() ([]Summary, error) { return nil, ErrNotImplemented }

// Find resolves a recording id (envelope dir name, or unique prefix) to
// its directory.
func Find(id string) (string, error) { return "", ErrNotImplemented }

// Load reads a recording envelope: summary, events (in seq order).
func Load(dir string) (*Summary, []Event, error) { return nil, nil, ErrNotImplemented }

// DOMSnippet returns dom-<eventId>.html for an event ("" when absent).
func DOMSnippet(dir, eventID string) (string, error) { return "", ErrNotImplemented }

// Delete removes a recording envelope.
func Delete(id string) error { return ErrNotImplemented }
