// internal/discovery/discovery_test.go
package discovery_test

import (
	"context"
	"testing"

	"github.com/monoes/mono-agent/internal/applications"
	"github.com/monoes/mono-agent/internal/discovery"
)

// fakeSource is a discovery.Source test double — no network calls.
type fakeSource struct {
	results []discovery.Result
	err     error
}

func (f *fakeSource) Name() string { return "fake" }
func (f *fakeSource) Search(ctx context.Context, query discovery.SearchQuery) ([]discovery.Result, error) {
	return f.results, f.err
}

func TestSearchCreatesNewApplications(t *testing.T) {
	db := newTestDB(t)
	store := applications.NewStore(db.DB)
	ctx := context.Background()

	source := &fakeSource{results: []discovery.Result{
		{Title: "Backend Engineer", Company: "Acme", URL: "https://acme.example/1"},
		{Title: "Frontend Engineer", Company: "Acme", URL: "https://acme.example/2"},
	}}

	created, skipped, failed, err := discovery.Search(ctx, source, store, "default", discovery.SearchQuery{Keywords: "engineer"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("expected 2 created, got %d", len(created))
	}
	if skipped != 0 || failed != 0 {
		t.Fatalf("expected 0 skipped/failed, got skipped=%d failed=%d", skipped, failed)
	}
	for _, app := range created {
		if app.Job.Source != "fake" {
			t.Fatalf("expected Source to be set to the source's Name(), got %q", app.Job.Source)
		}
	}
}

func TestSearchSkipsDuplicates(t *testing.T) {
	db := newTestDB(t)
	store := applications.NewStore(db.DB)
	ctx := context.Background()

	existing := &applications.Application{
		ProfileID: "default", Kind: applications.KindJob,
		Job: &applications.JobDetails{Title: "Backend Engineer", Company: "Acme", URL: "https://acme.example/1"},
	}
	if err := store.Create(ctx, existing); err != nil {
		t.Fatalf("seed Create: %v", err)
	}

	source := &fakeSource{results: []discovery.Result{
		{Title: "Backend Engineer", Company: "Acme", URL: "https://acme.example/1"}, // exact URL dup
		{Title: "Frontend Engineer", Company: "Acme", URL: "https://acme.example/2"}, // new
	}}

	created, skipped, failed, err := discovery.Search(ctx, source, store, "default", discovery.SearchQuery{Keywords: "engineer"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(created) != 1 {
		t.Fatalf("expected 1 created, got %d", len(created))
	}
	if skipped != 1 {
		t.Fatalf("expected 1 skipped, got %d", skipped)
	}
	if failed != 0 {
		t.Fatalf("expected 0 failed, got %d", failed)
	}
}

func TestSearchPropagatesSourceErrorWithPartialResults(t *testing.T) {
	db := newTestDB(t)
	store := applications.NewStore(db.DB)
	ctx := context.Background()

	sourceErr := context.DeadlineExceeded
	source := &fakeSource{
		results: []discovery.Result{{Title: "Backend Engineer", Company: "Acme", URL: "https://acme.example/1"}},
		err:     sourceErr,
	}

	created, _, _, err := discovery.Search(ctx, source, store, "default", discovery.SearchQuery{Keywords: "engineer"})
	if err == nil {
		t.Fatal("expected the source's error to propagate")
	}
	if len(created) != 1 {
		t.Fatalf("expected the partial result to still be created, got %d", len(created))
	}
}
