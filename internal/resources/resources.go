// Package resources lists and creates the external resources a node's
// resource picker offers — Drive/Sheets files, Gmail labels, Slack channels
// and users — using a connection's stored credential data. It is the
// provider-API half of `monoagentcli connect resources`; credential lookup,
// profile scoping and token refresh stay with the caller.
package resources

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Item is a single listable resource (spreadsheet, channel, etc.).
type Item struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// ListResult is one page of listed resources. Items is never nil.
type ListResult struct {
	Items      []Item `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// Endpoints are the provider API roots, overridable so tests can point them
// at local servers.
type Endpoints struct {
	Drive  string // https://www.googleapis.com/drive/v3
	Sheets string // https://sheets.googleapis.com/v4
	Gmail  string // https://gmail.googleapis.com/gmail/v1
	Slack  string // https://slack.com/api
}

// DefaultEndpoints are the real provider APIs.
var DefaultEndpoints = Endpoints{
	Drive:  "https://www.googleapis.com/drive/v3",
	Sheets: "https://sheets.googleapis.com/v4",
	Gmail:  "https://gmail.googleapis.com/gmail/v1",
	Slack:  "https://slack.com/api",
}

// Client calls the provider APIs.
type Client struct {
	HTTP      *http.Client
	Endpoints Endpoints
}

// New returns a Client for the real provider APIs with a bounded timeout.
func New() *Client {
	return &Client{HTTP: &http.Client{Timeout: 30 * time.Second}, Endpoints: DefaultEndpoints}
}

// ErrUnsupported marks a platform or resource type that can't be listed or
// created; test with errors.Is.
var ErrUnsupported = errors.New("unsupported")

type unsupportedError struct{ msg string }

func (e *unsupportedError) Error() string        { return e.msg }
func (e *unsupportedError) Is(target error) bool { return target == ErrUnsupported }

func unsupported(format string, a ...interface{}) error {
	return &unsupportedError{msg: fmt.Sprintf(format, a...)}
}

// List lists resources of resourceType on platform. query optionally
// filters Drive/Sheets files by name.
func (c *Client) List(ctx context.Context, platform, resourceType, query string, creds map[string]interface{}) (ListResult, error) {
	var items []Item
	var err error
	switch platform {
	case "google_sheets", "google_drive":
		items, err = c.listDrive(ctx, creds, resourceType, query)
	case "gmail":
		items, err = c.listGmail(ctx, creds, resourceType)
	case "slack":
		items, err = c.listSlack(ctx, creds, resourceType)
	default:
		err = unsupported("platform %q not supported for resource listing", platform)
	}
	if err != nil {
		return ListResult{}, err
	}
	if items == nil {
		items = []Item{}
	}
	return ListResult{Items: items}, nil
}

// Create creates a resource named name: a spreadsheet on google_sheets, a
// folder on google_drive. resourceType is accepted for symmetry with List;
// each platform creates one kind of resource.
func (c *Client) Create(ctx context.Context, platform, resourceType, name string, creds map[string]interface{}) (Item, error) {
	switch platform {
	case "google_sheets":
		return c.createSheet(ctx, creds, name)
	case "google_drive":
		return c.createDriveFolder(ctx, creds, name)
	default:
		return Item{}, unsupported("create not supported for platform %q", platform)
	}
}

func (c *Client) listDrive(ctx context.Context, creds map[string]interface{}, resourceType, query string) ([]Item, error) {
	accessToken, _ := creds["access_token"].(string)
	if accessToken == "" {
		return nil, errors.New("google: access_token not found in credential")
	}
	var mimeType string
	switch resourceType {
	case "spreadsheets":
		mimeType = "application/vnd.google-apps.spreadsheet"
	case "folders":
		mimeType = "application/vnd.google-apps.folder"
	default:
		return nil, unsupported("google_drive: unsupported resource type %q", resourceType)
	}
	q := "mimeType='" + mimeType + "' and trashed=false"
	if query != "" {
		q += " and name contains '" + strings.ReplaceAll(query, "'", "\\'") + "'"
	}
	apiURL := c.Endpoints.Drive + "/files?q=" + url.QueryEscape(q) + "&fields=files(id,name,modifiedTime)&pageSize=50"
	body, err := c.googleGet(ctx, apiURL, accessToken)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Files []struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			ModifiedTime string `json:"modifiedTime"`
		} `json:"files"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("google: parse response: %v", err)
	}
	items := make([]Item, 0, len(resp.Files))
	for _, f := range resp.Files {
		items = append(items, Item{
			ID:       f.ID,
			Name:     f.Name,
			Metadata: map[string]interface{}{"modified_time": f.ModifiedTime},
		})
	}
	return items, nil
}

func (c *Client) listGmail(ctx context.Context, creds map[string]interface{}, resourceType string) ([]Item, error) {
	accessToken, _ := creds["access_token"].(string)
	if accessToken == "" {
		return nil, errors.New("gmail: access_token not found in credential")
	}
	if resourceType != "labels" {
		return nil, unsupported("gmail: unsupported resource type %q", resourceType)
	}
	body, err := c.googleGet(ctx, c.Endpoints.Gmail+"/users/me/labels", accessToken)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Labels []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("gmail: parse response: %v", err)
	}
	items := make([]Item, 0, len(resp.Labels))
	for _, l := range resp.Labels {
		items = append(items, Item{ID: l.ID, Name: l.Name})
	}
	return items, nil
}

func (c *Client) listSlack(ctx context.Context, creds map[string]interface{}, resourceType string) ([]Item, error) {
	token, _ := creds["access_token"].(string)
	if token == "" {
		token, _ = creds["bot_token"].(string)
	}
	if token == "" {
		return nil, errors.New("slack: access_token or bot_token not found in credential")
	}
	var apiURL string
	switch resourceType {
	case "channels":
		apiURL = c.Endpoints.Slack + "/conversations.list?limit=200&exclude_archived=true"
	case "users":
		apiURL = c.Endpoints.Slack + "/users.list?limit=200"
	default:
		return nil, unsupported("slack: unsupported resource type %q", resourceType)
	}
	body, _, err := c.do(ctx, http.MethodGet, apiURL, token, nil)
	if err != nil {
		return nil, fmt.Errorf("slack: http: %v", err)
	}
	var resp struct {
		OK       bool   `json:"ok"`
		Error    string `json:"error"`
		Channels []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"channels"`
		Members []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Profile struct {
				RealName string `json:"real_name"`
			} `json:"profile"`
		} `json:"members"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("slack: parse: %v", err)
	}
	if !resp.OK {
		return nil, fmt.Errorf("slack: API error: %s", resp.Error)
	}
	var items []Item
	for _, ch := range resp.Channels {
		items = append(items, Item{ID: ch.ID, Name: "#" + ch.Name})
	}
	for _, m := range resp.Members {
		name := m.Profile.RealName
		if name == "" {
			name = m.Name
		}
		items = append(items, Item{ID: m.ID, Name: name})
	}
	return items, nil
}

func (c *Client) createSheet(ctx context.Context, creds map[string]interface{}, name string) (Item, error) {
	accessToken, _ := creds["access_token"].(string)
	if accessToken == "" {
		return Item{}, errors.New("google: access_token not found")
	}
	payload := map[string]interface{}{"properties": map[string]string{"title": name}}
	body, _, err := c.do(ctx, http.MethodPost, c.Endpoints.Sheets+"/spreadsheets", accessToken, payload)
	if err != nil {
		return Item{}, fmt.Errorf("google: create sheet: %v", err)
	}
	var created struct {
		SpreadsheetID string `json:"spreadsheetId"`
		Properties    struct {
			Title string `json:"title"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.SpreadsheetID == "" {
		return Item{}, fmt.Errorf("google: parse create response: %s", string(body))
	}
	return Item{ID: created.SpreadsheetID, Name: created.Properties.Title}, nil
}

func (c *Client) createDriveFolder(ctx context.Context, creds map[string]interface{}, name string) (Item, error) {
	accessToken, _ := creds["access_token"].(string)
	if accessToken == "" {
		return Item{}, errors.New("google: access_token not found")
	}
	payload := map[string]string{"name": name, "mimeType": "application/vnd.google-apps.folder"}
	body, _, err := c.do(ctx, http.MethodPost, c.Endpoints.Drive+"/files", accessToken, payload)
	if err != nil {
		return Item{}, fmt.Errorf("google drive: create folder: %v", err)
	}
	var created struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.ID == "" {
		return Item{}, fmt.Errorf("google drive: parse create response: %s", string(body))
	}
	return Item{ID: created.ID, Name: created.Name}, nil
}

// googleGet GETs a Google API URL, failing on any non-200 status with the
// status code in the message (callers detect 401 there to offer reconnect).
func (c *Client) googleGet(ctx context.Context, apiURL, accessToken string) ([]byte, error) {
	body, status, err := c.do(ctx, http.MethodGet, apiURL, accessToken, nil)
	if err != nil {
		return nil, fmt.Errorf("google API GET: %w", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("google API returned %d: %s", status, string(body))
	}
	return body, nil
}

// do sends one Bearer-authenticated request, JSON-encoding payload when it
// is non-nil, and returns the response body and status.
func (c *Client) do(ctx context.Context, method, apiURL, token string, payload interface{}) ([]byte, int, error) {
	var reqBody io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
		reqBody = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, apiURL, reqBody)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("read body: %w", err)
	}
	return body, resp.StatusCode, nil
}
