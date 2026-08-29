package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/monoes/mono-agent/internal/workflow"
)

// githubBaseURL is a var (not const) so tests can point it at an httptest server.
var githubBaseURL = "https://api.github.com"

// ghRepoPath builds the /repos/{owner}/{repo} path with both segments
// PathEscape'd — an owner like "/../x" must never alter the request path.
func ghRepoPath(owner, repo string) string {
	return githubBaseURL + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)
}

// GitHubNode interacts with the GitHub REST API v3.
// Type: "service.github"
type GitHubNode struct{}

func (n *GitHubNode) Type() string { return "service.github" }

func (n *GitHubNode) Execute(ctx context.Context, input workflow.NodeInput, config map[string]interface{}) ([]workflow.NodeOutput, error) {
	token := strVal(config, "token")
	if token == "" {
		token = strVal(config, "access_token")
	}
	if token == "" {
		return nil, fmt.Errorf("service.github: 'token' is required")
	}

	operation := strVal(config, "operation")
	owner := strVal(config, "owner")
	repo := strVal(config, "repo")

	var items []workflow.Item
	var err error

	switch operation {
	case "list_repos":
		items, err = n.listRepos(ctx, token)
	case "list_issues":
		items, err = n.listIssues(ctx, token, owner, repo, config)
	case "get_issue":
		items, err = n.getIssue(ctx, token, owner, repo, config)
	case "create_issue":
		items, err = n.createIssue(ctx, token, owner, repo, config)
	case "update_issue":
		items, err = n.updateIssue(ctx, token, owner, repo, config)
	case "list_prs":
		items, err = n.listPRs(ctx, token, owner, repo, config)
	case "create_pr":
		items, err = n.createPR(ctx, token, owner, repo, config)
	case "list_releases":
		items, err = n.listReleases(ctx, token, owner, repo)
	case "create_release":
		items, err = n.createRelease(ctx, token, owner, repo, config)
	default:
		return nil, fmt.Errorf("service.github: unknown operation %q", operation)
	}

	if err != nil {
		return nil, err
	}

	return []workflow.NodeOutput{{Handle: "main", Items: items}}, nil
}

// ghRequest performs a GitHub API call returning a single JSON object.
func (n *GitHubNode) ghRequest(ctx context.Context, method, url, token string, body interface{}) (map[string]interface{}, error) {
	req, err := buildRequest(ctx, method, url, token, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	respBytes, err := readBodyCapped(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, errorBodyEcho(respBytes))
	}
	var result map[string]interface{}
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}
	return result, nil
}

// ghRequestListPage performs a GitHub API call returning a JSON array, plus
// the rel="next" URL from the Link header ("" when there is no next page).
func (n *GitHubNode) ghRequestListPage(ctx context.Context, method, url, token string, body interface{}) ([]interface{}, string, error) {
	req, err := buildRequest(ctx, method, url, token, body)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("HTTP %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	respBytes, err := readBodyCapped(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, errorBodyEcho(respBytes))
	}
	var result []interface{}
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return nil, "", fmt.Errorf("parsing JSON array: %w", err)
	}
	return result, ghNextLink(resp.Header.Get("Link")), nil
}

// ghNextLink extracts the rel="next" target from a GitHub Link header like
//
//	<https://api.github.com/repositories/1/issues?per_page=100&page=2>; rel="next",
//	<https://api.github.com/repositories/1/issues?per_page=100&page=5>; rel="last"
//
// returning "" when absent.
func ghNextLink(link string) string {
	for _, part := range strings.Split(link, ",") {
		part = strings.TrimSpace(part)
		semi := strings.Index(part, ";")
		if semi < 0 {
			continue
		}
		target := strings.TrimSpace(part[:semi])
		if !strings.HasPrefix(target, "<") || !strings.HasSuffix(target, ">") {
			continue
		}
		target = target[1 : len(target)-1]
		for _, param := range strings.Split(part[semi+1:], ";") {
			if strings.TrimSpace(param) == `rel="next"` {
				return target
			}
		}
	}
	return ""
}

// ghFetchAllList pages a GitHub list endpoint by following the Link header's
// rel="next" URL, up to maxListPages requests. Returns the accumulated
// entries and whether the page cap truncated the result.
func (n *GitHubNode) ghFetchAllList(ctx context.Context, url, token string) ([]interface{}, bool, error) {
	var all []interface{}
	for page := 0; page < maxListPages; page++ {
		list, next, err := n.ghRequestListPage(ctx, "GET", url, token, nil)
		if err != nil {
			return nil, false, err
		}
		all = append(all, list...)
		if next == "" {
			return all, false, nil
		}
		url = next
	}
	return all, true, nil
}

func (n *GitHubNode) listRepos(ctx context.Context, token string) ([]workflow.Item, error) {
	list, truncated, err := n.ghFetchAllList(ctx, githubBaseURL+"/user/repos?per_page=100", token)
	if err != nil {
		return nil, err
	}
	items := listToItems(list)
	if truncated {
		flagTruncated(items)
	}
	return items, nil
}

func (n *GitHubNode) listIssues(ctx context.Context, token, owner, repo string, config map[string]interface{}) ([]workflow.Item, error) {
	params := url.Values{"per_page": {"100"}}
	if state := strVal(config, "state"); state != "" {
		params.Set("state", state)
	}
	list, truncated, err := n.ghFetchAllList(ctx, ghRepoPath(owner, repo)+"/issues?"+params.Encode(), token)
	if err != nil {
		return nil, err
	}
	items := listToItems(list)
	if truncated {
		flagTruncated(items)
	}
	return items, nil
}

func (n *GitHubNode) getIssue(ctx context.Context, token, owner, repo string, config map[string]interface{}) ([]workflow.Item, error) {
	number := intVal(config, "number")
	if number == 0 {
		return nil, fmt.Errorf("service.github: 'number' is required for get_issue")
	}
	u := fmt.Sprintf("%s/issues/%d", ghRepoPath(owner, repo), number)
	result, err := n.ghRequest(ctx, "GET", u, token, nil)
	if err != nil {
		return nil, err
	}
	return []workflow.Item{workflow.NewItem(result)}, nil
}

func (n *GitHubNode) createIssue(ctx context.Context, token, owner, repo string, config map[string]interface{}) ([]workflow.Item, error) {
	body := map[string]interface{}{
		"title": strVal(config, "title"),
		"body":  strVal(config, "body"),
	}
	if labels := strSliceVal(config, "labels"); len(labels) > 0 {
		body["labels"] = labels
	}
	url := ghRepoPath(owner, repo) + "/issues"
	result, err := n.ghRequest(ctx, "POST", url, token, body)
	if err != nil {
		return nil, err
	}
	return []workflow.Item{workflow.NewItem(result)}, nil
}

func (n *GitHubNode) updateIssue(ctx context.Context, token, owner, repo string, config map[string]interface{}) ([]workflow.Item, error) {
	number := intVal(config, "number")
	if number == 0 {
		return nil, fmt.Errorf("service.github: 'number' is required for update_issue")
	}
	body := map[string]interface{}{}
	if title := strVal(config, "title"); title != "" {
		body["title"] = title
	}
	if b := strVal(config, "body"); b != "" {
		body["body"] = b
	}
	if state := strVal(config, "state"); state != "" {
		body["state"] = state
	}
	if labels := strSliceVal(config, "labels"); len(labels) > 0 {
		body["labels"] = labels
	}
	url := fmt.Sprintf("%s/issues/%d", ghRepoPath(owner, repo), number)
	result, err := n.ghRequest(ctx, "PATCH", url, token, body)
	if err != nil {
		return nil, err
	}
	return []workflow.Item{workflow.NewItem(result)}, nil
}

func (n *GitHubNode) listPRs(ctx context.Context, token, owner, repo string, config map[string]interface{}) ([]workflow.Item, error) {
	params := url.Values{"per_page": {"100"}}
	if state := strVal(config, "state"); state != "" {
		params.Set("state", state)
	}
	list, truncated, err := n.ghFetchAllList(ctx, ghRepoPath(owner, repo)+"/pulls?"+params.Encode(), token)
	if err != nil {
		return nil, err
	}
	items := listToItems(list)
	if truncated {
		flagTruncated(items)
	}
	return items, nil
}

func (n *GitHubNode) createPR(ctx context.Context, token, owner, repo string, config map[string]interface{}) ([]workflow.Item, error) {
	head := strVal(config, "head")
	base := strVal(config, "base")
	if head == "" {
		return nil, fmt.Errorf("service.github: 'head' is required for create_pr")
	}
	if base == "" {
		return nil, fmt.Errorf("service.github: 'base' is required for create_pr")
	}
	body := map[string]interface{}{
		"title": strVal(config, "title"),
		"body":  strVal(config, "body"),
		"head":  head,
		"base":  base,
	}
	url := ghRepoPath(owner, repo) + "/pulls"
	result, err := n.ghRequest(ctx, "POST", url, token, body)
	if err != nil {
		return nil, err
	}
	return []workflow.Item{workflow.NewItem(result)}, nil
}

func (n *GitHubNode) listReleases(ctx context.Context, token, owner, repo string) ([]workflow.Item, error) {
	list, truncated, err := n.ghFetchAllList(ctx, ghRepoPath(owner, repo)+"/releases?per_page=100", token)
	if err != nil {
		return nil, err
	}
	items := listToItems(list)
	if truncated {
		flagTruncated(items)
	}
	return items, nil
}

func (n *GitHubNode) createRelease(ctx context.Context, token, owner, repo string, config map[string]interface{}) ([]workflow.Item, error) {
	body := map[string]interface{}{
		"tag_name": strVal(config, "tag_name"),
		"name":     strVal(config, "release_name"),
		"body":     strVal(config, "body"),
	}
	url := ghRepoPath(owner, repo) + "/releases"
	result, err := n.ghRequest(ctx, "POST", url, token, body)
	if err != nil {
		return nil, err
	}
	return []workflow.Item{workflow.NewItem(result)}, nil
}

// listToItems converts a []interface{} (each element a map) to []workflow.Item.
func listToItems(list []interface{}) []workflow.Item {
	items := make([]workflow.Item, 0, len(list))
	for _, elem := range list {
		if m, ok := elem.(map[string]interface{}); ok {
			items = append(items, workflow.NewItem(m))
		} else {
			items = append(items, workflow.NewItem(map[string]interface{}{"value": elem}))
		}
	}
	return items
}
