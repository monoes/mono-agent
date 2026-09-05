// Package linkedin implements discovery.Source against LinkedIn's public
// unauthenticated "guest" job-search endpoint — no login required. See
// docs/mastermind/specs/2026-09-05-discovery-design.md.
package linkedin

import (
	"context"
	"fmt"
	"math/rand"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/monoes/mono-agent/internal/discovery"
	"github.com/monoes/mono-agent/internal/nodes/ai/crawl"
)

const (
	guestSearchBase = "https://www.linkedin.com/jobs-guest/jobs/api/seeMoreJobPostings/search"
	guestSearchPath = "/jobs-guest/jobs/api/seeMoreJobPostings/search"
	robotsURL       = "https://www.linkedin.com/robots.txt"
	pageSize        = 25
	maxLimit        = 100
)

// Source scrapes LinkedIn's public guest job-search endpoint.
type Source struct{}

// New creates a LinkedIn Source.
func New() *Source { return &Source{} }

// Name identifies results from this Source as coming from "linkedin".
func (s *Source) Name() string { return "linkedin" }

// Search fetches up to query.Limit (capped at 100) job postings matching
// query.Keywords/query.Location, paginating the guest endpoint, pacing
// requests 1.5-3.0s apart, retrying a transient page failure once.
func (s *Source) Search(ctx context.Context, query discovery.SearchQuery) ([]discovery.Result, error) {
	limit := query.Limit
	if limit <= 0 || limit > maxLimit {
		limit = maxLimit
	}

	robotsTxt, err := fetchRobotsTxt(ctx, robotsURL)
	if err != nil {
		return nil, fmt.Errorf("linkedin: checking robots.txt: %w", err)
	}
	if isDisallowedByRobots(robotsTxt, guestSearchPath) {
		return nil, fmt.Errorf("linkedin: robots.txt disallows %s — refusing to scrape", guestSearchPath)
	}

	var results []discovery.Result
	for start := 0; len(results) < limit; start += pageSize {
		pageURL := fmt.Sprintf("%s?keywords=%s&location=%s&start=%d",
			guestSearchBase, url.QueryEscape(query.Keywords), url.QueryEscape(query.Location), start)

		html, err := fetchPageWithRetry(ctx, pageURL)
		if err != nil {
			if len(results) > 0 {
				return results, fmt.Errorf("linkedin: search truncated after %d result(s): %w", len(results), err)
			}
			return nil, fmt.Errorf("linkedin: search: %w", err)
		}

		pageResults, err := ParseSearchPage(html)
		if err != nil {
			return results, fmt.Errorf("linkedin: parsing page at start=%d: %w", start, err)
		}
		if len(pageResults) == 0 {
			break
		}
		results = append(results, pageResults...)
		if len(results) >= limit {
			break
		}

		select {
		case <-ctx.Done():
			return results, ctx.Err()
		case <-time.After(paceDelay()):
		}
	}

	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func paceDelay() time.Duration {
	return time.Duration(1500+rand.Intn(1500)) * time.Millisecond
}

func fetchPageWithRetry(ctx context.Context, pageURL string) (string, error) {
	result, err := crawl.FetchPage(ctx, pageURL, crawl.FetchOptions{RenderMode: "static"})
	if err == nil {
		return result.HTML, nil
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(2 * time.Second):
	}
	result, err = crawl.FetchPage(ctx, pageURL, crawl.FetchOptions{RenderMode: "static"})
	if err != nil {
		return "", err
	}
	return result.HTML, nil
}

// ParseSearchPage extracts job postings from one page of LinkedIn guest
// search-result HTML. Exported so it can be unit-tested against a fixture
// without a network call.
func ParseSearchPage(html string) ([]discovery.Result, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("parse html: %w", err)
	}
	var results []discovery.Result
	doc.Find("li").Each(func(_ int, li *goquery.Selection) {
		card := li.Find(".base-card").First()
		if card.Length() == 0 {
			return
		}
		title := strings.TrimSpace(card.Find(".base-search-card__title").First().Text())
		company := strings.TrimSpace(card.Find(".base-search-card__subtitle").First().Text())
		location := strings.TrimSpace(card.Find(".job-search-card__location").First().Text())
		jobURL, _ := card.Find(".base-card__full-link").First().Attr("href")
		postedAt := strings.TrimSpace(card.Find(".job-search-card__listdate").First().Text())
		if title == "" || jobURL == "" {
			return
		}
		results = append(results, discovery.Result{
			Title:    title,
			Company:  company,
			URL:      strings.SplitN(jobURL, "?", 2)[0],
			Location: location,
			PostedAt: postedAt,
		})
	})
	return results, nil
}
