package extension

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/recording"
)

// pick_element — re-record a single selector (contracts §9).
//
//	Go → extension
//	  {"id":…, "type":"pick_element", "tabId":12,
//	   "params":{"prompt":"Click: the Send button","timeoutMs":180000}}
//	extension → Go
//	  {"id":…, "success":true, "data":{"fingerprint":{…}, "url":"https://…"}}
//	  {"id":…, "success":false, "error":"cancelled"}   (Esc)
//	  {"id":…, "success":false, "error":"timeout"}     (nobody clicked)
//
// The fingerprint is a recording.Fingerprint: ranked, uniqueness-checked
// candidates and no field value, ever.

// Errors PickElement returns. Their messages are the strings the CLI's JSON
// errors use (contracts §9), so a caller can print err.Error() as-is.
var (
	ErrPickCancelled      = errors.New("cancelled")
	ErrPickTimeout        = errors.New("timeout")
	ErrBridgeNotConnected = errors.New("browser bridge not connected")
)

// DefaultPickTimeout is how long the user gets to click when the caller
// names no timeout.
const DefaultPickTimeout = 180 * time.Second

// pickSlack is added to the user's time to form the command's own timeout,
// so the extension's "timeout" answer arrives before Go gives up waiting.
const pickSlack = 5 * time.Second

// pickResult is pick_element's response data.
type pickResult struct {
	Fingerprint *recording.Fingerprint `json:"fingerprint"`
	URL         string                 `json:"url"`
}

// PickElement shows the extension's picker overlay in this page's tab with
// prompt, waits up to timeout for the user to click one element, and
// returns its fingerprint and the page URL. Esc gives ErrPickCancelled, no
// click in time ErrPickTimeout, no extension ErrBridgeNotConnected; ctx
// ending returns ctx.Err() (the overlay then times out on its own).
func (ep *ExtensionPage) PickElement(ctx context.Context, prompt string, timeout time.Duration) (*recording.Fingerprint, string, error) {
	if timeout <= 0 {
		timeout = DefaultPickTimeout
	}
	cmd := &Command{
		Type:  CmdPickElement,
		TabID: ep.tabID,
		Params: map[string]interface{}{
			"prompt":    prompt,
			"timeoutMs": timeout.Milliseconds(),
		},
	}
	type outcome struct {
		resp *Response
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		resp, err := ep.server.SendCommand(cmd, timeout+pickSlack)
		done <- outcome{resp, err}
	}()
	var out outcome
	select {
	case out = <-done:
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
	if out.err != nil {
		return nil, "", pickError(out.resp, out.err)
	}
	return decodePick(out.resp)
}

// pickError maps a failed pick_element to the contract's errors.
func pickError(resp *Response, err error) error {
	reason := ""
	if resp != nil {
		reason = strings.TrimSpace(resp.Error)
	}
	msg := err.Error()
	switch {
	case reason == "cancelled" || strings.HasSuffix(msg, "extension error: cancelled"):
		return ErrPickCancelled
	case reason == "timeout" || strings.HasSuffix(msg, "extension error: timeout") ||
		strings.Contains(msg, "command "+CmdPickElement+" timed out"):
		return ErrPickTimeout
	case errors.Is(err, context.DeadlineExceeded):
		return ErrPickTimeout
	case strings.Contains(msg, "no extension connected") || strings.HasPrefix(msg, "relay request:"):
		// "relay request:" is the bridge process itself being unreachable
		// (connection refused, reset); a relay that is merely slow comes
		// back as a timeout above, never here.
		return ErrBridgeNotConnected
	}
	return fmt.Errorf("pick element: %w", err)
}

// decodePick reads the response data into a fingerprint, re-applying the
// privacy rules ingest uses for recordings.
func decodePick(resp *Response) (*recording.Fingerprint, string, error) {
	if resp == nil || resp.Data == nil {
		return nil, "", errors.New("pick element: empty response")
	}
	blob, err := json.Marshal(resp.Data)
	if err != nil {
		return nil, "", fmt.Errorf("pick element: %w", err)
	}
	var res pickResult
	if err := json.Unmarshal(blob, &res); err != nil {
		return nil, "", fmt.Errorf("pick element: bad response: %w", err)
	}
	fp := res.Fingerprint
	if fp == nil || fp.Tag == "" {
		return nil, "", errors.New("pick element: response has no fingerprint")
	}
	if fp.Href != "" {
		fp.Href = recording.SanitizeURL(fp.Href)
	}
	if recording.SensitiveTarget(fp) {
		fp.Sensitive = true
	}
	if fp.Candidates == nil {
		fp.Candidates = []recording.Candidate{}
	}
	return fp, recording.SanitizeURL(res.URL), nil
}
