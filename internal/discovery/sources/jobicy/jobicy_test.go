package jobicy

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
func withTestServers(t *testing.T, robotsBody string, resp apiResponse) {
	t.Helper()
	robots := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(robotsBody))
	}))
	t.Cleanup(robots.Close)

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(api.Close)

	origRobotsURL, origAPIBase := robotsURL, apiBase
	robotsURL, apiBase = robots.URL, api.URL
	t.Cleanup(func() { robotsURL, apiBase = origRobotsURL, origAPIBase })
}

func TestSearchPassesKeywordAndLocationAsServerSideFilters(t *testing.T) {
	robots := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("User-agent: *\nDisallow:\n"))
	}))
	defer robots.Close()

	var gotQuery string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		json.NewEncoder(w).Encode(apiResponse{Jobs: []apiJob{
			{JobTitle: "Backend Engineer", CompanyName: "Acme", URL: "https://a.example/1", JobGeo: "USA"},
		}})
	}))
	defer api.Close()

	origRobotsURL, origAPIBase := robotsURL, apiBase
	robotsURL, apiBase = robots.URL, api.URL
	defer func() { robotsURL, apiBase = origRobotsURL, origAPIBase }()

	results, err := New().Search(context.Background(), discovery.SearchQuery{Keywords: "engineer", Location: "usa", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Title != "Backend Engineer" {
		t.Fatalf("unexpected results: %+v", results)
	}
	if !results[0].IsRemote {
		t.Fatal("expected IsRemote=true (Jobicy is remote-jobs-only)")
	}
	if gotQuery == "" {
		t.Fatal("expected query params to be sent")
	}
	if !contains(gotQuery, "tag=engineer") {
		t.Fatalf("expected tag=engineer in query, got %q", gotQuery)
	}
	if !contains(gotQuery, "geo=usa") {
		t.Fatalf("expected geo=usa in query, got %q", gotQuery)
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestSearchFallsBackToExcerptWhenDescriptionEmpty(t *testing.T) {
	withTestServers(t, "User-agent: *\nDisallow:\n", apiResponse{Jobs: []apiJob{
		{JobTitle: "Backend Engineer", CompanyName: "Acme", URL: "https://a.example/1", JobExcerpt: "short excerpt"},
	}})

	results, err := New().Search(context.Background(), discovery.SearchQuery{Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Description != "short excerpt" {
		t.Fatalf("expected excerpt fallback, got: %+v", results)
	}
}

func TestSearchRespectsRobotsDisallow(t *testing.T) {
	withTestServers(t, "User-agent: *\nDisallow: /api/v2/remote-jobs\n", apiResponse{})

	if _, err := New().Search(context.Background(), discovery.SearchQuery{Keywords: "x", Limit: 10}); err == nil {
		t.Fatal("expected an error when robots.txt disallows the API path, got nil")
	}
}

func TestSourceImplementsDiscoverySource(t *testing.T) {
	var _ discovery.Source = New()
	if New().Name() != "jobicy" {
		t.Fatalf("expected Name() to be \"jobicy\", got %q", New().Name())
	}
}
