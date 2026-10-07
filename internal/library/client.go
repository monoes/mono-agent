package library

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

// MaxArtifactBytes caps a download (the library accepts at most 20 MB).
const MaxArtifactBytes = 25 << 20

// TokenStore keeps one profile's login.
type TokenStore interface {
	Load(ctx context.Context) (*Token, error) // nil, nil when logged out
	Save(ctx context.Context, t *Token) error
	Delete(ctx context.Context) error
}

// Client calls the library API for one profile.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	Store   TokenStore // the profile's own login (a release before the machine session); nil: none
	// Session is the machine-wide monoes.me session (spec D21). It is used before
	// Store, and only for its own host; nil: Store alone.
	Session SessionSource
	Now     func() time.Time

	mu      sync.Mutex
	token   *Token
	loaded  bool
	session *Token // the session token last handed out
	oauth   *account.OAuthEndpoints
}

// NewClient returns a client for baseURL ("" = BaseURL()). Tokens only ever
// travel over https, or plain http to a loopback host (a local dev server).
func NewClient(baseURL string, store TokenStore) (*Client, error) {
	if baseURL == "" {
		baseURL = BaseURL()
	}
	baseURL = strings.TrimRight(baseURL, "/")
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("library: invalid base URL %q", baseURL)
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopbackHost(u.Hostname())) {
		return nil, fmt.Errorf("library: base URL %s must be https (plain http only for a loopback dev server)", baseURL)
	}
	return &Client{
		BaseURL: baseURL,
		HTTP:    &http.Client{Timeout: 2 * time.Minute},
		Store:   store,
		Now:     time.Now,
	}, nil
}

func isLoopbackHost(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// ErrNotLoggedIn is returned by calls that need a login when there is none.
// Every library call does: monoes.me answers 401 to anonymous reads too,
// official items included. A 401 *APIError matches it with errors.Is.
var ErrNotLoggedIn = errors.New("Log in to monoes.me first: monoagentcli library login")

// ErrSHA256Mismatch is returned when downloaded bytes don't hash to what
// the library reported.
var ErrSHA256Mismatch = errors.New("library: downloaded artifact does not match its sha256")

// APIError is a non-2xx answer: {error: {code, message}} with its status.
type APIError struct {
	Status     int
	Code       string
	Message    string
	RetryAfter string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	switch e.Status {
	case http.StatusUnauthorized:
		return ErrNotLoggedIn.Error()
	case http.StatusForbidden:
		return "monoes.me: not allowed: " + msg
	case http.StatusNotFound:
		return "monoes.me: not found: " + msg
	case http.StatusRequestEntityTooLarge:
		return "monoes.me: the artifact is too large (the library accepts up to 20 MB): " + msg
	case http.StatusTooManyRequests:
		if e.RetryAfter != "" {
			return "monoes.me: rate limited, retry in " + e.RetryAfter + "s: " + msg
		}
		return "monoes.me: rate limited, retry later: " + msg
	}
	return fmt.Sprintf("monoes.me: HTTP %d: %s", e.Status, msg)
}

// Is makes a 401 match ErrNotLoggedIn: the login is missing, expired or
// was refused, and logging in again is the fix either way.
func (e *APIError) Is(target error) bool {
	return target == ErrNotLoggedIn && e.Status == http.StatusUnauthorized
}

func apiError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	e := &APIError{Status: resp.StatusCode, RetryAfter: resp.Header.Get("Retry-After")}
	var body struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(b, &body) == nil && len(body.Error) > 0 {
		var obj struct{ Code, Message string }
		if json.Unmarshal(body.Error, &obj) == nil {
			e.Code, e.Message = obj.Code, obj.Message
		} else {
			_ = json.Unmarshal(body.Error, &e.Code) // {"error":"invalid_request"}
		}
	}
	if e.Message == "" && e.Code != "" {
		e.Message = e.Code
	}
	if e.Message == "" {
		e.Message = strings.TrimSpace(string(b))
		if len(e.Message) > 200 {
			e.Message = e.Message[:200] + "…"
		}
	}
	return e
}

// do sends a request with the bearer token, refreshing it once on a 401.
// Without a login it fails with ErrNotLoggedIn before any network call.
// body is buffered so the request can be replayed.
func (c *Client) do(ctx context.Context, method, path string, body []byte, contentType string) (*http.Response, error) {
	tok, err := c.currentToken(ctx)
	if err != nil {
		return nil, err
	}
	if tok == nil {
		return nil, ErrNotLoggedIn
	}
	send := func(t *Token) (*http.Response, error) {
		var rd io.Reader
		if body != nil {
			rd = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		if t != nil {
			req.Header.Set("Authorization", "Bearer "+t.AccessToken)
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return nil, fmt.Errorf("monoes.me unreachable (%s): %w", c.BaseURL, err)
		}
		return resp, nil
	}
	resp, err := send(tok)
	if err != nil || resp.StatusCode != http.StatusUnauthorized || tok.RefreshToken == "" {
		return resp, err
	}
	if nt, rerr := c.refresh(ctx, tok); rerr == nil {
		resp.Body.Close()
		return send(nt)
	}
	return resp, nil // the 401, for the caller's error
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	resp, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return apiError(resp)
	}
	return decodeJSON(resp.Body, out)
}

func decodeJSON(r io.Reader, out any) error {
	if err := json.NewDecoder(io.LimitReader(r, 16<<20)).Decode(out); err != nil {
		return fmt.Errorf("monoes.me: unexpected response: %w", err)
	}
	return nil
}

// List lists items.
func (c *Client) List(ctx context.Context, q ListQuery) (*ListResult, error) {
	v := url.Values{}
	for k, s := range map[string]string{"kind": q.Kind, "scope": q.Scope, "q": q.Search, "tag": q.Tag} {
		if s != "" {
			v.Set(k, s)
		}
	}
	if q.Page > 0 {
		v.Set("page", strconv.Itoa(q.Page))
	}
	if q.PerPage > 0 {
		v.Set("per_page", strconv.Itoa(q.PerPage))
	}
	var out ListResult
	if err := c.getJSON(ctx, "/api/library/items?"+v.Encode(), &out); err != nil {
		return nil, err
	}
	if out.Items == nil {
		out.Items = []Item{}
	}
	return &out, nil
}

// Get returns one item by id or "<kind>/<slug>".
func (c *Client) Get(ctx context.Context, ref string) (*Item, error) {
	var it Item
	if err := c.getJSON(ctx, "/api/library/items/"+escapeRef(ref), &it); err != nil {
		return nil, err
	}
	return &it, nil
}

func escapeRef(ref string) string {
	parts := strings.Split(ref, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// Me returns the logged-in account.
func (c *Client) Me(ctx context.Context) (*Me, error) {
	var me Me
	if err := c.getJSON(ctx, "/api/library/me", &me); err != nil {
		return nil, err
	}
	return &me, nil
}

// Download fetches an item's artifact and checks its sha256 against both
// the X-Content-SHA256 header and the item's own sha256. It returns the
// bytes and their hash.
func (c *Client) Download(ctx context.Context, it *Item) ([]byte, string, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/library/items/"+url.PathEscape(it.ID)+"/artifact", nil, "")
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, "", apiError(resp)
	}
	if resp.ContentLength > MaxArtifactBytes {
		return nil, "", fmt.Errorf("monoes.me: artifact is %d bytes, over the %d byte limit", resp.ContentLength, MaxArtifactBytes)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxArtifactBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("monoes.me: download: %w", err)
	}
	if len(b) > MaxArtifactBytes {
		return nil, "", fmt.Errorf("monoes.me: artifact over the %d byte limit", MaxArtifactBytes)
	}
	sum := sha256.Sum256(b)
	got := hex.EncodeToString(sum[:])
	header := strings.ToLower(strings.TrimSpace(resp.Header.Get("X-Content-SHA256")))
	want := strings.ToLower(strings.TrimSpace(it.SHA256))
	if header == "" && want == "" {
		return nil, "", fmt.Errorf("%w: the library sent no sha256 to check against", ErrSHA256Mismatch)
	}
	for _, w := range []string{header, want} {
		if w != "" && w != got {
			return nil, "", fmt.Errorf("%w: expected %s, got %s", ErrSHA256Mismatch, w, got)
		}
	}
	return b, got, nil
}

// Create publishes a new item (POST /api/library/items).
func (c *Client) Create(ctx context.Context, up Upload) (*Item, error) {
	fields := map[string]string{"kind": up.Kind, "visibility": up.Visibility, "name": up.Name,
		"description": up.Description, "version": up.Version, "tags": strings.Join(up.Tags, ",")}
	return c.upload(ctx, http.MethodPost, "/api/library/items", fields, up)
}

// PutArtifact uploads a new version of an existing item.
func (c *Client) PutArtifact(ctx context.Context, id string, up Upload) (*Item, error) {
	return c.upload(ctx, http.MethodPut, "/api/library/items/"+url.PathEscape(id)+"/artifact",
		map[string]string{"version": up.Version}, up)
}

// Patch changes an item's listing fields (name, description, tags,
// visibility).
func (c *Client) Patch(ctx context.Context, id string, fields map[string]any) (*Item, error) {
	body, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, http.MethodPatch, "/api/library/items/"+url.PathEscape(id), body, "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, apiError(resp)
	}
	var it Item
	if err := decodeJSON(resp.Body, &it); err != nil {
		return nil, err
	}
	return &it, nil
}

func (c *Client) upload(ctx context.Context, method, path string, fields map[string]string, up Upload) (*Item, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, k := range []string{"kind", "visibility", "name", "description", "tags", "version"} {
		if v := fields[k]; v != "" {
			if err := mw.WriteField(k, v); err != nil {
				return nil, err
			}
		}
	}
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, up.Filename))
	h.Set("Content-Type", up.ContentType)
	fw, err := mw.CreatePart(h)
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(up.Data); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, method, path, buf.Bytes(), mw.FormDataContentType())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, apiError(resp)
	}
	var it Item
	if err := decodeJSON(resp.Body, &it); err != nil {
		return nil, err
	}
	return &it, nil
}
