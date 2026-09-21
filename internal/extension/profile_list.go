package extension

import (
	"context"
	"fmt"

	"github.com/monoes/mono-agent/internal/profiledir"
)

// profile.list — "which profiles can I save into?"
//
// The extension's popup offers a profile picker so a page can be filed into
// one brain rather than another, and it has to fill that picker with the
// user's REAL profiles, not a list typed into the extension. This is the
// question it asks, over the same request channel as doc.lookup.
//
// The answer comes from whatever the host process hands over via
// SetProfileSource — this package never opens a database. Two reasons: the
// transport has no business owning one, and the method should only be
// advertised (ping's `methods`) when something can actually answer it. An
// extension talking to a process with no source simply sees no profile.list
// and keeps saving the way it always has.
//
//	extension → Go  {"kind":"request","id":"req-…","method":"profile.list"}
//	Go → extension  {"kind":"reply","id":"req-…","ok":true,
//	                 "data":{"profiles":[{"id":"…","name":"Work","default":true}]}}

// MethodProfileList lists the profiles a capture can be filed into.
const MethodProfileList = "profile.list"

// ProfileSource answers "which profiles exist". The host process supplies
// it (see cmd/monoagentcli/capture_profiles.go); an error means "cannot
// answer right now", which the extension renders as absence rather than
// failure.
type ProfileSource func(ctx context.Context) ([]profiledir.Profile, error)

// ProfileList is the reply's shape.
type ProfileList struct {
	Profiles []profiledir.Profile `json:"profiles"`
	// Default is the id the picker should pre-select when it has no stored
	// choice — empty when there are no profiles at all.
	Default string `json:"default,omitempty"`
}

// SetProfileSource installs (or replaces) the source behind profile.list,
// registering the handler with it. Passing nil takes the method away again.
//
// Registration hangs off the source on purpose: HandleRequest both adds and
// replaces, and RequestMethods is what the extension probes, so "there is a
// source" and "the method is advertised" are the same fact rather than two
// that can drift.
func (s *Server) SetProfileSource(src ProfileSource) {
	if src == nil {
		s.handlerMu.Lock()
		delete(s.handlers, MethodProfileList)
		s.handlerMu.Unlock()
		return
	}
	s.HandleRequest(MethodProfileList, func(ctx context.Context, _ *Request, _ ProgressFunc) (any, error) {
		return listProfiles(ctx, src)
	})
}

// listProfiles is the handler's body, kept separate so a test can call it
// without a server.
func listProfiles(ctx context.Context, src ProfileSource) (*ProfileList, error) {
	profiles, err := src(ctx)
	if err != nil {
		// Not an error the user did anything about: no database yet, a
		// locked one, a CLI built without profiles. The picker falls back
		// to saving the way it did before.
		return nil, Unavailable("%s", fmt.Sprintf("cannot list profiles: %v", err))
	}
	out := &ProfileList{Profiles: profiles}
	if out.Profiles == nil {
		// A picker renders an empty list; it cannot render a null.
		out.Profiles = []profiledir.Profile{}
	}
	for _, p := range out.Profiles {
		if p.Default {
			out.Default = p.ID
			break
		}
	}
	return out, nil
}
