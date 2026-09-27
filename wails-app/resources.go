package main

import (
	"strings"
	"time"
)

// ResourceItem is a single listable resource (spreadsheet, channel, etc.)
type ResourceItem struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// ResourceListResult is returned by ListResources.
type ResourceListResult struct {
	Items       []ResourceItem `json:"items"`
	NextCursor  string         `json:"next_cursor,omitempty"`
	Error       string         `json:"error,omitempty"`
	NeedsReauth bool           `json:"needs_reauth,omitempty"`
}

// ResourceItemResult is returned by CreateResource.
type ResourceItemResult struct {
	Item  *ResourceItem `json:"item,omitempty"`
	Error string        `json:"error,omitempty"`
}

// resourcesCLITimeout bounds one picker call: a token refresh and a
// provider request, each capped at 30 s inside the CLI.
const resourcesCLITimeout = 90 * time.Second

// ListResources lists external resources for a given platform and resource
// type via `monoagentcli connect resources`, which resolves the credential in
// the active profile and refreshes an expiring token. credentialID is the
// connection ID. query is an optional search string.
func (a *App) ListResources(platform, resourceType, credentialID, query string) ResourceListResult {
	args := []string{"connect", "resources", "--platform=" + platform, "--type=" + resourceType}
	if query != "" {
		args = append(args, "--query="+query)
	}
	var result ResourceListResult
	if err := a.cliJSON(resourcesCLITimeout, &result, append(args, "--", credentialID)...); err != nil {
		return ResourceListResult{Items: []ResourceItem{}, Error: err.Error(), NeedsReauth: resourceNeedsReauth(err.Error())}
	}
	if result.Items == nil {
		result.Items = []ResourceItem{}
	}
	return result
}

// CreateResource creates a new external resource via `monoagentcli connect
// resources create` and returns the created item.
func (a *App) CreateResource(platform, resourceType, credentialID, name string) ResourceItemResult {
	var result ResourceItemResult
	if err := a.cliJSON(resourcesCLITimeout, &result, "connect", "resources", "create",
		"--platform="+platform, "--type="+resourceType, "--name="+name, "--", credentialID); err != nil {
		return ResourceItemResult{Error: err.Error()}
	}
	return result
}

// resourceNeedsReauth reports whether a picker error should offer to
// reconnect: the credential is gone (or another profile's), or the provider
// rejected the token.
func resourceNeedsReauth(msg string) bool {
	return strings.HasPrefix(msg, "credential lookup:") ||
		strings.Contains(msg, "401") || strings.Contains(msg, "UNAUTHENTICATED") || strings.Contains(msg, "Invalid Credentials")
}
