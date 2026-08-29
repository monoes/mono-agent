package main

import (
	"strings"
	"testing"
)

// TestCrawlRejectsNonHTTPScheme guards the scheme allowlist: file://,
// javascript:, chrome://, and friends must be refused before any browser
// launch, not handed to Rod.
func TestCrawlRejectsNonHTTPScheme(t *testing.T) {
	for _, rawURL := range []string{
		"file:///etc/passwd",
		"javascript:alert(1)",
		"chrome://settings",
		"ftp://example.com/pub",
		"data:text/html,<h1>hi",
	} {
		cmd := newCrawlCmd(nil)
		cmd.SetArgs([]string{rawURL})
		err := cmd.Execute()
		if err == nil {
			t.Errorf("crawl %q: expected scheme rejection, got nil", rawURL)
			continue
		}
		if !strings.Contains(err.Error(), "http") {
			t.Errorf("crawl %q: error = %v, want it to mention only http/https are supported", rawURL, err)
		}
	}
}

// TestCrawlCapsWait guards the --wait bounds: negative values or anything
// over 120s must be refused up front (a typo like --wait 1200 would
// otherwise hang the command for 20 minutes).
func TestCrawlCapsWait(t *testing.T) {
	for _, wait := range []string{"-1", "121", "9999"} {
		cmd := newCrawlCmd(nil)
		cmd.SetArgs([]string{"https://example.com", "--wait", wait})
		err := cmd.Execute()
		if err == nil {
			t.Errorf("--wait %s: expected rejection, got nil", wait)
			continue
		}
		if !strings.Contains(err.Error(), "--wait") {
			t.Errorf("--wait %s: error = %v, want it to name --wait", wait, err)
		}
	}
}
