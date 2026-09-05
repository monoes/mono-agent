package arbeitnow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/monoes/mono-agent/internal/discovery"
)

// withTestServers points the package's robotsURL/apiBase vars at local
// httptest servers for the duration of t, restoring them afterward.
// robotsBody is served verbatim; page is served as the single JSON page
// (no further pagination — none of this file's tests need more than one).
func withTestServers(t *testing.T, robotsBody string, page apiResponse) {
	t.Helper()
	robots := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(robotsBody))
	}))
	t.Cleanup(robots.Close)

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(page)
	}))
	t.Cleanup(api.Close)

	origRobotsURL, origAPIBase := robotsURL, apiBase
	robotsURL, apiBase = robots.URL, api.URL
	t.Cleanup(func() { robotsURL, apiBase = origRobotsURL, origAPIBase })
}

func TestSearchFiltersByKeywordClientSide(t *testing.T) {
	withTestServers(t, "User-agent: *\nDisallow:\n", apiResponse{
		Data: []apiJob{
			{Title: "Backend Engineer", CompanyName: "Acme", URL: "https://a.example/1", Tags: []string{"go", "backend"}, CreatedAt: 1700000000},
			{Title: "Marketing Manager", CompanyName: "Beta", URL: "https://b.example/2", Tags: []string{"marketing"}},
		},
	})

	results, err := New().Search(context.Background(), discovery.SearchQuery{Keywords: "backend", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result matching \"backend\", got %d: %+v", len(results), results)
	}
	if results[0].Title != "Backend Engineer" || results[0].Company != "Acme" {
		t.Fatalf("unexpected result: %+v", results[0])
	}
	if results[0].PostedAt == "" {
		t.Fatal("expected PostedAt to be set from CreatedAt")
	}
}

func TestSearchMatchesKeywordAgainstTags(t *testing.T) {
	withTestServers(t, "User-agent: *\nDisallow:\n", apiResponse{
		Data: []apiJob{{Title: "Software Developer", CompanyName: "Acme", URL: "https://a.example/1", Tags: []string{"golang", "remote"}}},
	})

	results, err := New().Search(context.Background(), discovery.SearchQuery{Keywords: "golang", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected the tag match to be found, got %d results", len(results))
	}
}

func TestSearchWithNoKeywordsReturnsEverything(t *testing.T) {
	withTestServers(t, "User-agent: *\nDisallow:\n", apiResponse{
		Data: []apiJob{
			{Title: "Backend Engineer", CompanyName: "Acme", URL: "https://a.example/1"},
			{Title: "Marketing Manager", CompanyName: "Beta", URL: "https://b.example/2"},
		},
	})

	results, err := New().Search(context.Background(), discovery.SearchQuery{Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected both results with no keyword filter, got %d", len(results))
	}
}

func TestSearchRespectsRobotsDisallow(t *testing.T) {
	withTestServers(t, "User-agent: *\nDisallow: /api/job-board-api\n", apiResponse{})

	if _, err := New().Search(context.Background(), discovery.SearchQuery{Keywords: "x", Limit: 10}); err == nil {
		t.Fatal("expected an error when robots.txt disallows the API path, got nil")
	}
}

func TestSourceImplementsDiscoverySource(t *testing.T) {
	var _ discovery.Source = New()
	if New().Name() != "arbeitnow" {
		t.Fatalf("expected Name() to be \"arbeitnow\", got %q", New().Name())
	}
}
