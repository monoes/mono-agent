package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/monoes/mono-agent/internal/workflow"
)

// withServiceServer points the given base-URL var at an httptest server for
// the duration of the test (same pattern as withDevtoServer).
func withServiceServer(t *testing.T, ptr *string, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	if ptr != nil {
		orig := *ptr
		*ptr = srv.URL
		t.Cleanup(func() { *ptr = orig })
	}
	return srv
}

// executeMain runs node.Execute and returns the main-handle items.
func executeMain(t *testing.T, node workflow.NodeExecutor, config map[string]interface{}) []workflow.Item {
	t.Helper()
	out, err := node.Execute(context.Background(), workflow.NodeInput{}, config)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	for _, o := range out {
		if o.Handle == "main" {
			return o.Items
		}
	}
	return nil
}

func airtablePage(records int, offset string) map[string]interface{} {
	recs := make([]interface{}, 0, records)
	for i := 0; i < records; i++ {
		recs = append(recs, map[string]interface{}{
			"id": fmt.Sprintf("rec%d", i), "createdTime": "2026-01-01T00:00:00Z",
			"fields": map[string]interface{}{"Name": fmt.Sprintf("row%d", i)},
		})
	}
	page := map[string]interface{}{"records": recs}
	if offset != "" {
		page["offset"] = offset
	}
	return page
}

// TestAirtableListRecords_FollowsOffsetPagination verifies list_records
// follows Airtable's `offset` token instead of silently stopping after the
// first page.
func TestAirtableListRecords_FollowsOffsetPagination(t *testing.T) {
	var requests int32
	withServiceServer(t, &airtableBaseURL, func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&requests, 1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			_ = json.NewEncoder(w).Encode(airtablePage(2, "page2"))
			return
		}
		if r.URL.Query().Get("offset") != "page2" {
			t.Errorf("second request offset param = %q, want page2", r.URL.Query().Get("offset"))
		}
		_ = json.NewEncoder(w).Encode(airtablePage(3, ""))
	})

	items := executeMain(t, &AirtableNode{}, map[string]interface{}{
		"api_key": "tok", "base_id": "app1", "table": "T", "operation": "list_records",
	})
	if len(items) != 5 {
		t.Fatalf("expected 5 items across 2 pages, got %d", len(items))
	}
	if got := atomic.LoadInt32(&requests); got != 2 {
		t.Errorf("expected 2 requests, got %d", got)
	}
	if items[0].JSON["pagination_truncated"] == true {
		t.Error("pagination_truncated must not be set when all pages were fetched")
	}
}

// TestAirtableListRecords_TruncationFlagged verifies the maxListPages cap
// stops paging and marks every returned item with pagination_truncated=true
// so truncation is never silent.
func TestAirtableListRecords_TruncationFlagged(t *testing.T) {
	var requests int32
	withServiceServer(t, &airtableBaseURL, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(airtablePage(1, "always-more"))
	})

	items := executeMain(t, &AirtableNode{}, map[string]interface{}{
		"api_key": "tok", "base_id": "app1", "table": "T", "operation": "list_records",
	})
	if got := atomic.LoadInt32(&requests); got != maxListPages {
		t.Fatalf("expected the loop to cap at %d requests, got %d", maxListPages, got)
	}
	if len(items) != maxListPages {
		t.Fatalf("expected %d items (one per capped page), got %d", maxListPages, len(items))
	}
	for i, it := range items {
		if it.JSON["pagination_truncated"] != true {
			t.Fatalf("item %d missing pagination_truncated=true: %v", i, it.JSON)
		}
	}
}

// TestGitHubListIssues_PaginationAndEscaping verifies list_issues follows
// the Link header rel="next" across pages AND that a hostile owner value
// like "/../x" is PathEscape'd rather than spliced raw into the path.
func TestGitHubListIssues_PaginationAndEscaping(t *testing.T) {
	var requests int32
	withServiceServer(t, &githubBaseURL, func(w http.ResponseWriter, r *http.Request) {
		// The crafted owner must never alter the path: /repos/%2F..%2Fx/r/...
		if got := r.URL.EscapedPath(); !strings.Contains(got, "%2F..%2Fx/r/issues") {
			t.Errorf("owner not escaped: raw path %q", got)
		}
		n := atomic.AddInt32(&requests, 1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			w.Header().Set("Link", fmt.Sprintf(`<http://%s/repos/%%2F..%%2Fx/r/issues?per_page=100&page=2>; rel="next", <http://%s/repos/%%2F..%%2Fx/r/issues?per_page=100&page=9>; rel="last"`, r.Host, r.Host))
		}
		_ = json.NewEncoder(w).Encode([]interface{}{
			map[string]interface{}{"number": float64(n*10 - 1), "title": "a"},
			map[string]interface{}{"number": float64(n * 10), "title": "b"},
		})
	})

	items := executeMain(t, &GitHubNode{}, map[string]interface{}{
		"token": "tok", "owner": "/../x", "repo": "r", "operation": "list_issues",
	})
	if got := atomic.LoadInt32(&requests); got != 2 {
		t.Fatalf("expected 2 pages, got %d", got)
	}
	if len(items) != 4 {
		t.Fatalf("expected 4 items across 2 pages, got %d", len(items))
	}
	if items[0].JSON["pagination_truncated"] == true {
		t.Error("pagination_truncated must not be set when all pages were fetched")
	}
}

// TestGitHubListRepos_TruncationFlagged verifies the maxListPages cap on the
// Link-header loop marks items as truncated.
func TestGitHubListRepos_TruncationFlagged(t *testing.T) {
	var requests int32
	withServiceServer(t, &githubBaseURL, func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&requests, 1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Link", fmt.Sprintf(`<http://%s/user/repos?per_page=100&page=%d>; rel="next"`, r.Host, n+1))
		_ = json.NewEncoder(w).Encode([]interface{}{map[string]interface{}{"id": float64(n)}})
	})

	items := executeMain(t, &GitHubNode{}, map[string]interface{}{"token": "tok", "operation": "list_repos"})
	if got := atomic.LoadInt32(&requests); got != maxListPages {
		t.Fatalf("expected the loop to cap at %d requests, got %d", maxListPages, got)
	}
	for i, it := range items {
		if it.JSON["pagination_truncated"] != true {
			t.Fatalf("item %d missing pagination_truncated=true", i)
		}
	}
}

func TestGhNextLink(t *testing.T) {
	cases := []struct {
		link string
		want string
	}{
		{`<https://api.github.com/repositories/1/issues?page=2>; rel="next", <https://api.github.com/repositories/1/issues?page=5>; rel="last"`, "https://api.github.com/repositories/1/issues?page=2"},
		{`<https://api.github.com/repositories/1/issues?page=5>; rel="last"`, ""},
		{``, ""},
		{`<https://x.test/a>; rel="prev"`, ""},
	}
	for _, tc := range cases {
		if got := ghNextLink(tc.link); got != tc.want {
			t.Errorf("ghNextLink(%q) = %q, want %q", tc.link, got, tc.want)
		}
	}
}

// TestNotionQueryDatabase_FollowsCursor verifies query_database follows
// has_more/next_cursor and sends start_cursor on subsequent pages.
func TestNotionQueryDatabase_FollowsCursor(t *testing.T) {
	var requests int32
	withServiceServer(t, &notionBaseURL, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		n := atomic.AddInt32(&requests, 1)
		w.Header().Set("Content-Type", "application/json")
		if n == 2 && body["start_cursor"] != "cur-2" {
			t.Errorf("second page start_cursor = %v, want cur-2", body["start_cursor"])
		}
		if n == 1 {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"results":     []interface{}{map[string]interface{}{"id": "p1"}, map[string]interface{}{"id": "p2"}},
				"has_more":    true,
				"next_cursor": "cur-2",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"results":  []interface{}{map[string]interface{}{"id": "p3"}},
			"has_more": false,
		})
	})

	items := executeMain(t, &NotionNode{}, map[string]interface{}{
		"token": "tok", "database_id": "db1", "operation": "query_database",
	})
	if len(items) != 3 {
		t.Fatalf("expected 3 items across 2 pages, got %d", len(items))
	}
}

// TestJiraListIssues_FollowsStartAtAndBearerAuth verifies list_issues pages
// via startAt until total/isLast and that an access_token authenticates via
// Bearer without email/api_token.
func TestJiraListIssues_FollowsStartAtAndBearerAuth(t *testing.T) {
	var requests int32
	srv := withServiceServer(t, nil, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/search" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok-oauth" {
			t.Errorf("Authorization = %q, want Bearer tok-oauth", got)
		}
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		n := atomic.AddInt32(&requests, 1)
		switch n {
		case 1:
			if body["startAt"] != float64(0) {
				t.Errorf("page 1 startAt = %v, want 0", body["startAt"])
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"startAt": 0, "maxResults": 2, "total": 3,
				"issues": []interface{}{map[string]interface{}{"key": "A-1"}, map[string]interface{}{"key": "A-2"}},
			})
		case 2:
			if body["startAt"] != float64(2) {
				t.Errorf("page 2 startAt = %v, want 2", body["startAt"])
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"startAt": 2, "maxResults": 2, "total": 3, "isLast": true,
				"issues": []interface{}{map[string]interface{}{"key": "A-3"}},
			})
		default:
			t.Errorf("unexpected extra request #%d", n)
		}
	})

	items := executeMain(t, &JiraNode{}, map[string]interface{}{
		"domain": srv.URL, "access_token": "tok-oauth", "jql": "project = X", "operation": "list_issues",
	})
	if len(items) != 3 {
		t.Fatalf("expected 3 issues across 2 pages, got %d", len(items))
	}
}

// TestJiraBasicAuthPath verifies the legacy email+api_token path still
// authenticates with Basic auth.
func TestJiraBasicAuthPath(t *testing.T) {
	srv := withServiceServer(t, nil, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Basic YUBiLmNvbTp0b2s=" { // base64("a@b.com:tok")
			t.Errorf("Authorization = %q, want Basic YUBiLmNvbTp0b2s=", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"issues": []interface{}{}})
	})
	executeMain(t, &JiraNode{}, map[string]interface{}{
		"domain": srv.URL, "email": "a@b.com", "api_token": "tok", "operation": "list_issues",
	})
}

// TestHubspotList_FollowsAfterCursor verifies hubspot lists follow the
// paging.next.after cursor.
func TestHubspotList_FollowsAfterCursor(t *testing.T) {
	var requests int32
	withServiceServer(t, &hubspotBaseURL, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/crm/v3/objects/contacts" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		n := atomic.AddInt32(&requests, 1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			if r.URL.Query().Get("after") != "" {
				t.Errorf("page 1 after = %q, want empty", r.URL.Query().Get("after"))
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"results": []interface{}{map[string]interface{}{"id": "c1"}, map[string]interface{}{"id": "c2"}},
				"paging":  map[string]interface{}{"next": map[string]interface{}{"after": "42"}},
			})
			return
		}
		if got := r.URL.Query().Get("after"); got != "42" {
			t.Errorf("page 2 after = %q, want 42", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []interface{}{map[string]interface{}{"id": "c3"}},
		})
	})

	items := executeMain(t, &HubSpotNode{}, map[string]interface{}{
		"access_token": "tok", "operation": "list_contacts",
	})
	if len(items) != 3 {
		t.Fatalf("expected 3 contacts across 2 pages, got %d", len(items))
	}
}

// TestLinearListIssues_FollowsPageInfo verifies list_issues follows
// pageInfo.hasNextPage/endCursor, passing `after` on subsequent pages.
func TestLinearListIssues_FollowsPageInfo(t *testing.T) {
	var requests int32
	withServiceServer(t, &linearGraphQLURL, func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Variables map[string]interface{} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		n := atomic.AddInt32(&requests, 1)
		w.Header().Set("Content-Type", "application/json")
		hasNext := n == 1
		cursor := ""
		if n == 1 {
			cursor = "cur-1"
		}
		if n == 2 {
			after, _ := payload.Variables["after"].(string)
			if after != "cur-1" {
				t.Errorf("page 2 after = %v, want cur-1", payload.Variables["after"])
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"issues": map[string]interface{}{
					"nodes": []interface{}{map[string]interface{}{"id": fmt.Sprintf("i%d", n)}},
					"pageInfo": map[string]interface{}{
						"hasNextPage": hasNext,
						"endCursor":   cursor,
					},
				},
			},
		})
	})

	items := executeMain(t, &LinearNode{}, map[string]interface{}{
		"api_key": "tok", "operation": "list_issues",
	})
	if len(items) != 2 {
		t.Fatalf("expected 2 issues across 2 pages, got %d", len(items))
	}
}
