// Package discovery finds job postings from external sources and imports
// them into the applications pipeline as new pending applications. See
// docs/mastermind/specs/2026-09-05-discovery-design.md.
package discovery

import (
	"context"
	"fmt"
	"os"

	"github.com/monoes/mono-agent/internal/applications"
)

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

// Search runs source.Search, then for each result not already a duplicate
// (per IsDuplicate), creates a pending job application via store.Create.
// A source error is returned alongside whatever was already collected and
// processed — a caller sees both the partial success and that it's
// incomplete, never a silent partial success or a total failure that
// discards real results. A per-result Create failure is counted in failed
// and logged, and does not abort the rest of the batch.
func Search(ctx context.Context, source Source, store *applications.Store, profileID string, query SearchQuery) (created []applications.Application, skipped, failed int, searchErr error) {
	results, err := source.Search(ctx, query)
	searchErr = err

	for _, r := range results {
		dup, dErr := IsDuplicate(ctx, store, profileID, r)
		if dErr != nil {
			return created, skipped, failed, fmt.Errorf("discovery.Search: checking duplicate: %w", dErr)
		}
		if dup {
			skipped++
			continue
		}

		isRemote := r.IsRemote
		app := &applications.Application{
			ProfileID: profileID,
			Kind:      applications.KindJob,
			Job: &applications.JobDetails{
				Title:       r.Title,
				Company:     r.Company,
				URL:         r.URL,
				Location:    r.Location,
				Description: r.Description,
				JobType:     r.JobType,
				IsRemote:    &isRemote,
				Source:      source.Name(),
				PostedAt:    r.PostedAt,
			},
		}
		if createErr := store.Create(ctx, app); createErr != nil {
			fmt.Fprintf(os.Stderr, "discovery.Search: creating application for %q: %v\n", r.URL, createErr)
			failed++
			continue
		}
		created = append(created, *app)
	}
	return created, skipped, failed, searchErr
}
