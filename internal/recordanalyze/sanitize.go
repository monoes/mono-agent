package recordanalyze

import (
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/monoes/mono-agent/internal/recording"
)

// sensitiveParams are query parameters whose values are credentials or
// one-time tokens. Recorded URLs keep the parameter with a redacted value;
// the value never reaches the prompt, the draft or a navigate step.
var sensitiveParams = map[string]bool{
	"code": true, "token": true, "access_token": true, "id_token": true, "refresh_token": true,
	"state": true, "sig": true, "signature": true, "key": true, "api_key": true, "apikey": true,
	"password": true, "pass": true, "auth": true, "session": true, "sessionid": true,
	"magic": true, "reset": true, "otp": true,
}

// Redacted replaces a sensitive value.
const Redacted = "REDACTED"

var sensitiveInTextRe = func() *regexp.Regexp {
	names := make([]string, 0, len(sensitiveParams))
	for n := range sensitiveParams {
		names = append(names, regexp.QuoteMeta(n))
	}
	sort.Strings(names)
	return regexp.MustCompile(`(?i)([?&#;](?:amp;)?(?:` + strings.Join(names, "|") + `)=)[^&#"'\s<>\\]*`)
}()

// SanitizeURL drops the fragment and redacts sensitive query values.
// Template placeholders ({{x}}) are kept as they are.
func SanitizeURL(raw string) string {
	if raw == "" || strings.Contains(raw, "{{") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return sanitizeText(raw)
	}
	u.Fragment, u.RawFragment = "", ""
	if u.RawQuery != "" {
		q := u.Query()
		changed := false
		for k := range q {
			if sensitiveParams[strings.ToLower(k)] {
				q.Set(k, Redacted)
				changed = true
			}
		}
		if changed {
			u.RawQuery = q.Encode()
		}
	}
	return u.String()
}

// sanitizeText redacts sensitive "name=value" pairs inside free text (DOM
// snippets, hrefs, selector strings).
func sanitizeText(s string) string {
	if s == "" {
		return s
	}
	return sensitiveInTextRe.ReplaceAllString(s, "${1}"+Redacted)
}

// urlCarriesToken reports a literal URL with a sensitive query parameter
// (any literal value, redacted included) or a fragment holding one.
func urlCarriesToken(raw string) bool {
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return sensitiveInTextRe.MatchString(raw)
	}
	for k, vs := range u.Query() {
		if !sensitiveParams[strings.ToLower(k)] {
			continue
		}
		for _, v := range vs {
			if !strings.HasPrefix(strings.TrimSpace(v), "{{") {
				return true
			}
		}
	}
	return sensitiveInTextRe.MatchString("#" + u.Fragment)
}

// sanitizeFingerprint returns a copy with hrefs and selector strings redacted.
func sanitizeFingerprint(fp *recording.Fingerprint) *recording.Fingerprint {
	if fp == nil {
		return nil
	}
	c := *fp
	c.Href = SanitizeURL(c.Href)
	c.CSS, c.XPath = sanitizeText(c.CSS), sanitizeText(c.XPath)
	c.Candidates = append([]recording.Candidate(nil), fp.Candidates...)
	for i := range c.Candidates {
		c.Candidates[i].Value = sanitizeText(c.Candidates[i].Value)
	}
	return &c
}
