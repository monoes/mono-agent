# Job Discovery — Design Spec

Date: 2026-09-05
Status: Approved (Phase 2 of the "ultimate job applier" feature)
Branch: `worktree-feature+job-tender-applications`

## Context

Phase 2 of the multi-phase job/tender-applier feature (see
`docs/mastermind/specs/2026-09-05-applications-foundation-design.md` for the
full phase list and Phase 1's completed Foundation). Given a search
(keywords + location), find real job postings from an external source and
write them into the Phase 1 `applications` pipeline as new `pending`
applications, deduplicated against what's already stored.

**Tender discovery is out of scope for this phase.** The user's Phase 1
answer was "start generic / decide later" for tender portals — and manual
tender ingestion is already fully served by Phase 1's
`monoagentcli application add --kind tender ...`. Phase 2 adds nothing new
for tenders.

### Fixing a Phase 1 gap: missing `Title` field

Investigating the existing schema before designing discovery surfaced a real
gap: neither `job_details` nor `tender_details` has a title/position field
distinct from `company`/`issuing_org`. A scraped job like "Senior Backend
Engineer at Acme Corp" needs both pieces of information — this phase adds a
`title` column to both tables via a new migration (`032_application_titles.sql`)
before building the scraper, since discovery cannot populate meaningful data
without it. This is a schema fix, not new phase-2 functionality — see Task 0
in the implementation plan.

### Reused infrastructure (verified by reading the code)

- `crawl.FetchPage` (`internal/nodes/ai/crawl/engine.go:137`) — an existing
  generic page fetcher (static HTTP or headless-browser render mode, already
  sets a `User-Agent` header). This phase reuses it as the fetch layer
  instead of writing a new HTTP client.
- `github.com/PuerkitoBio/goquery` — already a direct dependency (used by
  `internal/nodes/data/html.go` and the crawl package). No new dependency
  needed for HTML parsing.
- `internal/applications.Store` (Phase 1) — `Create` and `List` are reused
  directly; discovery writes through the same validated path every other
  writer uses.

## Requirements

- Search one job source (LinkedIn, chosen below) by keywords + location,
  return real postings, and create a `pending` job application for each new
  (non-duplicate) result.
- Deduplicate against applications already in the store before inserting.
- Bounded, polite request pacing — no unbounded/parallel hammering of the
  source.
- Respect `robots.txt` for the endpoint being scraped.
- A CLI command to trigger a search and see what was imported.
- A workflow node so Phase 5 (chat-driven matching) can trigger discovery
  programmatically, not just via CLI.
- The source must be pluggable: adding a second job board later is a new
  file + one registry entry, not a rewrite.

## Architecture

### Scraping technique: unauthenticated HTTP scraping (decided, not a question)

Two approaches exist per the earlier research: (a) unauthenticated HTTP
scraping of LinkedIn's public "guest" job-search endpoint (JobSpy's proven
technique — no login, no browser), or (b) driving the existing authenticated
LinkedIn browser-bot session (`internal/bot/linkedin`) with a new job-search
action.

**Decision: (a), unauthenticated HTTP scraping.** Rationale:
- (b) requires writing an entirely new bot action type from scratch (new JS
  DOM automation, new action constant, new registration) — LinkedIn's bot
  today has zero job-search capability, so this isn't a small extension.
- (b) spends the user's real, authenticated LinkedIn session's rate budget
  and ban risk on scraping traffic, for a feature (search) that doesn't
  require being logged in at all — LinkedIn's guest endpoint returns public
  job postings with no auth.
- (a) matches JobSpy's own choice for exactly this reason and reuses
  `crawl.FetchPage`, which already exists.
- Tradeoff acknowledged: scraping LinkedIn without their API is against
  their Terms of Service, same as every tool studied in the research phase
  (JobSpy, AIHawk-the-jobbot, etc.). This is scoped as personal-use search
  automation (the user's own job search, rate-limited, no resale/mass
  harvesting) — the same posture as the reference tools.

### Package layout

New package `internal/discovery` (source-agnostic orchestration) +
`internal/discovery/sources/linkedin` (the LinkedIn-specific scraper), plus
a workflow-node wrapper `internal/nodes/discovery`.

```go
// internal/discovery/discovery.go

// SearchQuery is the source-agnostic search input.
type SearchQuery struct {
	Keywords string
	Location string
	Limit    int // max results to return; sources must not exceed this
}

// Result is one posting a Source found, in unified (JobSpy-schema-derived)
// shape — ready to map onto applications.JobDetails.
type Result struct {
	Title       string
	Company     string
	URL         string
	Location    string
	Description string
	JobType     string
	IsRemote    bool
	PostedAt    string // free-form, as scraped
}

// Source is implemented by each pluggable job board.
type Source interface {
	// Name is the value stored in JobDetails.Source for results this
	// Source produces, e.g. "linkedin".
	Name() string
	// Search returns up to query.Limit results. Implementations own their
	// own pagination and pacing internally.
	Search(ctx context.Context, query SearchQuery) ([]Result, error)
}
```

Registration mirrors `internal/noderegistry`'s explicit-list convention
(not a filesystem auto-discovery scheme — one source doesn't warrant that
machinery yet):

```go
// internal/discovery/registry.go
var sources = map[string]Source{
	"linkedin": linkedin.New(),
}

func Get(name string) (Source, bool) {
	s, ok := sources[name]
	return s, ok
}

func Names() []string {
	names := make([]string, 0, len(sources))
	for n := range sources {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
```

Adding a second board later is a new `internal/discovery/sources/<name>`
package implementing `Source`, plus one line in this map.

### LinkedIn source implementation

`internal/discovery/sources/linkedin/linkedin.go`:
- Endpoint: `https://www.linkedin.com/jobs-guest/jobs/api/seeMoreJobPostings/search?keywords=<kw>&location=<loc>&start=<offset>` (JobSpy's documented technique — public, no auth).
- Fetch via `crawl.FetchPage(ctx, url, crawl.FetchOptions{RenderMode: "static"})`.
- Parse the returned HTML with goquery: each result is a `<li>` containing a
  `base-card` — extract title, company, location, posting URL, and relative
  posted-at text via documented LinkedIn guest-endpoint CSS classes
  (`base-search-card__title`, `base-search-card__subtitle`,
  `job-search-card__location`, `base-card__full-link`,
  `job-search-card__listdate`).
- Pagination: offset-based (`start=0,25,50,...`), loop until `query.Limit`
  results collected or a page returns zero results (end of results).
- Pacing: a random delay of 1.5–3.0 seconds between page fetches (matches
  JobSpy's own politeness window) — never fetch pages concurrently for one
  search.
- Retry: on a transient error (timeout, 5xx), retry that one page once
  after a 2-second backoff; a second failure aborts the search with
  whatever results were already collected (partial success, not a hard
  failure) plus a returned error describing what was cut short.
- `robots.txt`: before the first fetch, fetch
  `https://www.linkedin.com/robots.txt` and check whether
  `/jobs-guest/jobs/api/seeMoreJobPostings/search` is disallowed for `*`.
  If disallowed, `Search` returns an error immediately — this is a hard
  stop, not a bypass-with-warning.
- Cap: `query.Limit` is clamped to a maximum of 100 per call, regardless of
  what's requested, to bound worst-case request volume for one CLI
  invocation.

### Deduplication

Before inserting each `Result`, check `internal/applications.Store` for an
existing job-kind application that is either:
1. An exact match on `JobDetails.URL` (cheap, zero false positives — most
   reposts of the *same* listing keep the same URL), or
2. An exact match on the normalized pair (`normalize(Title)`,
   `normalize(Company)`), where `normalize` lowercases, strips all
   characters except letters/digits/spaces, and collapses whitespace —
   catches a repost under a new URL with the same title+company.

```go
// internal/discovery/dedup.go
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

// IsDuplicate checks existing job applications for profileID against r,
// per the two-check rule above.
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
```

No time-window is applied (unlike job-ops's 30-day lookback) — Phase 1's
data volume is small enough that checking the full job-kind set is cheap;
a window can be added later if the table grows large enough to matter.

### CLI surface

```
monoagentcli application discover --keywords "backend engineer" --location "Berlin" [--source linkedin] [--limit 25]
```

- `--source` defaults to `linkedin` (currently the only registered source);
  an unknown name is a validation error listing valid names via
  `discovery.Names()`.
- `--limit` defaults to 25, clamped to 100 (matches the source-level cap).
- Output (text mode): `Imported 12 new job(s), skipped 3 duplicate(s).`
  Output (`--json`): `{"imported": 12, "skipped": 3, "applications": [...]}`
  where `applications` lists the newly created applications' `id`/`title`/`company`/`url`.

### Workflow node

`discovery.search_jobs` (`internal/nodes/discovery`), config: `keywords`
(required), `location`, `source` (default `linkedin`), `limit` (default 25).
Executes the same search-then-create-then-dedup flow as the CLI, emits one
output item per newly created application (kind-`applications.list`-node
compatible field shape: `id`, `kind`, `status`, `company`, `title`, `url`).
Skipped duplicates are not emitted as items but are counted in a final
summary log line, matching `applications.create`'s existing conventions.

## Data Flow

1. CLI/node calls `discovery.Get(sourceName)` to resolve the `Source`.
2. `Source.Search(ctx, query)` fetches and paginates, returning `[]Result`.
3. For each `Result`, `discovery.IsDuplicate` checks the store; duplicates
   are skipped and counted.
4. Non-duplicates are converted to `applications.Application{Kind: KindJob,
   Job: &applications.JobDetails{Title: r.Title, Company: r.Company, URL:
   r.URL, ..., Source: source.Name()}}` and written via
   `applications.Store.Create` — landing as `pending`, exactly like a
   manually-added job, with the same status-log/tag machinery Phase 1 built.
5. CLI/node reports counts (imported, skipped) and the created applications.

## Error Handling

- Unknown `--source` / `source` config value → CLI: `errInvalidInput`
  listing valid names; node: a plain error (workflow error handling applies
  as it does to every other node).
- `robots.txt` disallows the endpoint → the source returns an error; no
  fetch of the actual search endpoint occurs. Surfaced verbatim to the
  caller.
- A page fetch fails twice (see Retry above) → partial results are still
  processed (dedup + insert whatever was already collected) and the error
  describing the truncation is returned alongside the counts — a caller
  sees both what happened and that it's incomplete, not a silent partial
  success or a total failure that throws away real results.
- `applications.Store.Create` failing for one result (should be rare, since
  `Company`/`URL`/`Title` are always non-empty from a successful parse) is
  fatal for that one result only — it's logged and counted separately as
  "failed", not silently dropped, and does not abort the rest of the batch.

## Testing

- `internal/discovery/dedup_test.go` — `normalize` (case, punctuation,
  whitespace collapsing), `IsDuplicate` (URL match, normalized title+company
  match, no false positive on a genuinely different posting).
- `internal/discovery/sources/linkedin/linkedin_test.go` — HTML parsing
  against a fixed sample HTML fixture (captured guest-endpoint markup,
  stored as a testdata file) asserting exact field extraction; pagination
  loop logic tested against a fake `Source`-shaped HTTP round-tripper
  (`httptest.Server`) serving canned pages so no real network call happens
  in tests; robots.txt gate tested against both an allowing and a
  disallowing fixture response.
- `internal/nodes/discovery/search_jobs_test.go` — node `Execute` against an
  injected fake `Source` (via a package-level override hook, mirroring how
  `internal/applications` tests use a real temp SQLite DB) verifying
  dedup-skip counting and correct `applications.Store.Create` calls.
- `cmd/monoagentcli/application_discover_test.go` — CLI integration test
  against a fake source (same injection hook) verifying the text and
  `--json` output shapes and the imported/skipped counts.

## Known Limitation: Selector Accuracy Against Live LinkedIn

The CSS selectors above come from JobSpy's documented technique, not a live
fetch against LinkedIn performed during this design (this environment has
no outbound network access to verify current markup). The implementation's
test suite validates parsing logic against a hand-built HTML fixture
matching that documented structure — it proves the parser correctly
extracts fields from HTML shaped like LinkedIn's guest endpoint, not that
LinkedIn's live markup still matches exactly today. Selector drift (LinkedIn
changing class names) is an expected, ongoing maintenance reality for any
scraper, same as every reference tool studied — the pluggable `Source`
interface means fixing a broken selector later is a localized change to one
file, not a redesign.

## Out of Scope (this phase)

- Any second job board (Indeed, ZipRecruiter, Glassdoor, Google) — the
  `Source` interface and registry make adding one later a bounded,
  independent task, not a redesign.
- Tender discovery/scraping — already served by manual `application add
  --kind tender` (Phase 1); no tender-specific scraper this phase.
- Proxy rotation — not needed for a single-source, rate-limited personal-use
  tool; the interface doesn't preclude adding it to a `Source`
  implementation later if a source needs it.
- Time-windowed dedup — full-set dedup is sufficient at current data volumes
  (see Deduplication section).
