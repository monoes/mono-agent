package automation

import (
	"errors"
	"fmt"
	"strings"
)

// libraryTrust decides the source and trust of a `library install`: the
// archive downloaded from monoes.me, pinned to the sha256 the library
// reported. Official items (published by monoes) get the trust the
// embedded built-ins had, so the social flows that used them keep working
// without extra confirmations; everything else is imported.
func libraryTrust(src string, opts InstallOptions) (source, trust string, err error) {
	o := opts.Library
	if o.ItemID == "" {
		return "", "", errors.New("automation: library install without an item id")
	}
	if strings.TrimSpace(opts.ExpectSHA256) == "" {
		return "", "", errors.New("automation: library install needs the artifact's sha256")
	}
	if opts.Trust != "" || (opts.Source != "" && opts.Source != SourceMonoes) {
		return "", "", errors.New("automation: a library install takes its source and trust from the library")
	}
	if isURL(src) {
		return "", "", fmt.Errorf("automation: library installs read the verified download, not %s", src)
	}
	o.Source = SourceMonoes
	if o.Official {
		return SourceMonoes, TrustBuiltin, nil
	}
	return SourceMonoes, TrustImported, nil
}

// shipped reports whether a package is one the app vouches for: an
// embedded built-in from an older release, or an official monoes.me
// package. The policy gate reports those unavailable instead of
// installing them disabled, so a build without social support that shares
// this home leaves them as they are.
func (p *Package) shipped() bool {
	return p.Source == SourceBuiltin || (p.Source == SourceMonoes && p.trust() == TrustBuiltin)
}

// SetLibraryOrigin records where an installed package came from on
// monoes.me without reinstalling it: `library` adopts a built-in an older
// release seeded when an official item has its id, so `library update` can
// update it. Only a built-in or an existing monoes package is adopted.
func (r *Registry) SetLibraryOrigin(id string, o *LibraryOrigin) (bool, error) {
	adopted := false
	err := r.update(func(idx *indexFile) (bool, error) {
		e, ok := idx.Packages[id]
		if !ok || e.Removed {
			return false, fmt.Errorf("%w: %s", ErrNotInstalled, id)
		}
		if e.Source != SourceBuiltin && e.Source != SourceMonoes {
			return false, nil
		}
		if o != nil {
			c := *o
			c.Source = SourceMonoes
			o = &c
		}
		if (e.Library == nil && o == nil) || (e.Library != nil && o != nil && *e.Library == *o) {
			return false, nil
		}
		e.Library, adopted = o, true
		return true, nil
	})
	return adopted, err
}
