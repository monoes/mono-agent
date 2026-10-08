package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/monoes/mono-agent/internal/release"
)

// releaseClient builds the signed-manifest client (a variable so tests can
// point it at an httptest server with generated keys).
var releaseClient = release.DefaultClient

// latestInfo is the latest release as the updaters need it: from the verified
// signed manifest, or, only while release.AllowLegacyGitHubUpdates, from the
// GitHub API.
type latestInfo struct {
	Tag      string
	URL      string
	Manifest *release.Manifest // set on the signed path
	Legacy   *latestRelease    // set on the legacy path
}

// fetchLatest prefers the signed manifest. Only "unavailable" (no pinned key,
// manifest not reachable) falls back; a manifest that fails a check is an
// error, never a reason to try the weaker path.
func fetchLatest(ctx context.Context) (*latestInfo, error) {
	m, err := releaseClient().Latest(ctx)
	if err == nil {
		return &latestInfo{Tag: m.Version, URL: m.NotesURL, Manifest: m}, nil
	}
	if !errors.Is(err, release.ErrUnavailable) {
		return nil, fmt.Errorf("the signed release manifest was rejected: %w; nothing was installed", err)
	}
	if !release.AllowLegacyGitHubUpdates {
		return nil, fmt.Errorf("cannot verify an update: %w", err)
	}
	rel, lerr := fetchLatestRelease(ctx)
	if lerr != nil {
		return nil, lerr
	}
	return &latestInfo{Tag: rel.TagName, URL: rel.HTMLURL, Legacy: rel}, nil
}
