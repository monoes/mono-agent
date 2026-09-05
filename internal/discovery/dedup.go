// internal/discovery/dedup.go
package discovery

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/monoes/mono-agent/internal/applications"
)

// normalize lowercases s, strips every character that isn't a letter,
// digit, or space, and collapses whitespace — used to compare a scraped
// title/company against what's already stored despite punctuation or
// casing differences.
func normalize(s string) string {
	var b strings.Builder
	lastWasSpace := true // trims leading space
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastWasSpace = false
		case unicode.IsSpace(r):
			if !lastWasSpace {
				b.WriteRune(' ')
			}
			lastWasSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

// IsDuplicate reports whether r matches an existing job-kind application
// for profileID, either by exact URL or by normalized (title, company).
func IsDuplicate(ctx context.Context, store *applications.Store, profileID string, r Result) (bool, error) {
	existing, err := store.List(ctx, profileID, applications.ListFilter{Kind: applications.KindJob})
	if err != nil {
		return false, fmt.Errorf("discovery.IsDuplicate: %w", err)
	}
	normTitle, normCompany := normalize(r.Title), normalize(r.Company)
	for _, app := range existing {
		if app.Job == nil {
			continue
		}
		if app.Job.URL == r.URL {
			return true, nil
		}
		if normalize(app.Job.Title) == normTitle && normalize(app.Job.Company) == normCompany {
			return true, nil
		}
	}
	return false, nil
}
