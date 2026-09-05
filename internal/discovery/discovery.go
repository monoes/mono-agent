// Package discovery finds job postings from external sources and imports
// them into the applications pipeline as new pending applications. See
// docs/mastermind/specs/2026-09-05-discovery-design.md.
package discovery

import "context"

// SearchQuery is the source-agnostic search input.
type SearchQuery struct {
	Keywords string
	Location string
	Limit    int // max results to return; sources must not exceed this
}

// Result is one posting a Source found, in unified shape — ready to map
// onto applications.JobDetails.
type Result struct {
	Title       string
	Company     string
	URL         string
	Location    string
	Description string
	JobType     string
	IsRemote    bool
	PostedAt    string // free-form, as scraped
}

// Source is implemented by each pluggable job board.
type Source interface {
	// Name is the value stored in JobDetails.Source for results this
	// Source produces, e.g. "linkedin".
	Name() string
	// Search returns up to query.Limit results. Implementations own their
	// own pagination and pacing internally.
	Search(ctx context.Context, query SearchQuery) ([]Result, error)
}
