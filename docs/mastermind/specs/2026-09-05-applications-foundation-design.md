# Applications Foundation — Design Spec

Date: 2026-09-05
Status: Approved (Phase 1 of the "ultimate job applier" feature)
Branch: `worktree-feature+job-tender-applications`

## Context

This is Phase 1 of a larger, multi-phase feature: a unified system for tracking and
automating **job applications and tender/procurement bids** in mono-agent. The full
feature was decomposed into independent sub-projects, each with its own
design → plan → build cycle:

1. **Foundation** (this spec) — application/tender data model, vault extension point, CLI CRUD.
2. Job & tender discovery/scraping.
3. Profile ingestion → knowledge graph (personal + optional company sub-profile).
4. CV / cover-letter / tender-document generation (HTML templates + PDF rendering, vault storage).
5. Chat-powered matching (gated fit-scoring against the profile knowledge graph).
6. Apply automation (browser form-filling and/or document-package staging, stopping
   short of the final submit action; auto vs. confirm modes; Wails GUI Applications board).

Phases 2–6 are out of scope for this spec and will each get their own design document
before being planned/built.

### Research inputs

This design incorporates concrete mechanisms studied from five reference repositories
(`~/Desktop/projects/{AIHawk,ai-job-search,career-ops,job-ops,JobSpy}`), most directly:

- **career-ops**: closed status-enum + append-only transition ledger; strict typed
  storage over ad-hoc blobs.
- **job-ops**: structural "automation never auto-submits" boundary — a hard product
  invariant enforced at the phase-6 design level, not just a prompt instruction.
- **JobSpy**: the unified job-posting field set this design's `job_details` table is
  derived from.
- **ai-job-search** / **JobSpy**: append-only provenance discipline for stored records.

AIHawk (the local checkout) turned out to be an unrelated project and contributed no
input to this data-model phase; its browser-tool-serialization and anti-detection
findings are noted for Phase 6.

## Requirements

- Track two kinds of application: `job` and `tender`, under one shared pipeline
  (status lifecycle, tagging, audit trail) rather than duplicated per-kind systems.
- User-specified 4-stage lifecycle: `pending`, `applied`, `rejected`, `cancelled`.
- User can attach free-form tags to any application.
- Every status change is auditable (who/what changed it, when, from what to what).
- Storage follows mono-agent's existing convention: SQLite is the source of truth
  (matches `secrets`, `connections`, and the vault stores), not a file-is-canonical
  model.
- Kind-specific fields (job vs. tender) are strongly typed and queryable in SQL, not
  a JSON blob — a third kind can be added later as a bounded, independent unit of work.
- Reserve a link from the future document vault (Phase 4) to an application, so that
  phase doesn't require a schema migration.

## Architecture

New Go package `internal/applications` (domain logic + SQLite store), following the
same structural convention as `internal/secrets` and `internal/connections`. A
workflow-node package `internal/nodes/applications` wraps the store for CLI/workflow
access, following the convention of `internal/nodes/vault`.

### Schema

```sql
CREATE TABLE applications (
    id          TEXT PRIMARY KEY,      -- uuid
    profile_id  TEXT NOT NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('job', 'tender')),
    status      TEXT NOT NULL CHECK (status IN ('pending', 'applied', 'rejected', 'cancelled')),
    created_at  TIMESTAMP NOT NULL,
    updated_at  TIMESTAMP NOT NULL
);

CREATE TABLE application_tags (
    application_id TEXT NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    tag            TEXT NOT NULL,
    PRIMARY KEY (application_id, tag)
);

CREATE TABLE application_status_log (
    id             TEXT PRIMARY KEY,   -- uuid
    application_id TEXT NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    from_status    TEXT,               -- NULL for the initial "created" row
    to_status      TEXT NOT NULL,
    actor          TEXT NOT NULL CHECK (actor IN ('user', 'system')),
    note           TEXT,
    created_at     TIMESTAMP NOT NULL
);
-- INSERT-only from the store API: no UPDATE/DELETE method exists for this table.

CREATE TABLE job_details (
    application_id    TEXT PRIMARY KEY REFERENCES applications(id) ON DELETE CASCADE,
    company           TEXT NOT NULL,
    url               TEXT NOT NULL,
    location          TEXT,
    description       TEXT,
    compensation_min  REAL,
    compensation_max  REAL,
    currency          TEXT,
    job_type          TEXT,            -- e.g. full_time, contract, internship
    is_remote         BOOLEAN,
    source            TEXT,            -- e.g. "manual", "linkedin", future scraper name
    posted_at         TIMESTAMP
);

CREATE TABLE tender_details (
    application_id           TEXT PRIMARY KEY REFERENCES applications(id) ON DELETE CASCADE,
    issuing_org              TEXT NOT NULL,
    url                      TEXT NOT NULL,
    description              TEXT,
    submission_deadline      TIMESTAMP NOT NULL,
    estimated_value          REAL,
    currency                 TEXT,
    required_certifications  TEXT,     -- comma-separated for Phase 1; may normalize later
    bid_documents_required   TEXT,     -- comma-separated for Phase 1
    source                   TEXT,
    published_at             TIMESTAMP
);
```

Vault documents (introduced in Phase 4) will carry an optional
`application_id REFERENCES applications(id)` column, added now via an empty
placeholder migration comment so Phase 4 only needs to add the vault table itself,
not touch `applications`.

### Status transition graph (enforced, not just documented)

```
pending  → applied
pending  → cancelled
applied  → rejected
applied  → cancelled
```

`rejected` and `cancelled` are terminal — no outgoing edges. Every transition is one
SQLite transaction: validate against this graph, `UPDATE applications.status`,
`INSERT INTO application_status_log`. Both writes happen or neither does.

### Kind-consistency guard

The `applications.Store` (Go) rejects, before any row is written:
- Creating a `job_details` row for an application whose `kind != 'job'` (and the
  tender equivalent).
- Creating an application of a given kind without its matching detail row in the
  same transaction (no application ever exists without exactly one detail row).

## Data Flow

1. **Create** — CLI `monoagentcli application add --kind job --company ... --url ...`
   (or `--kind tender --issuing-org ... --submission-deadline ...`) →
   `applications.Store.Create` inserts the base row + matching detail row + the
   initial `application_status_log` row (`from_status: NULL, to_status: pending`)
   in one transaction.
2. **Transition** — CLI `monoagentcli application status <id> set <status>` → store
   validates against the transition graph, writes new status + ledger row
   transactionally.
3. **Tag** — CLI `monoagentcli application tag <id> add|remove <tag>` → junction
   table write.
4. **List / inspect** — CLI `monoagentcli application list [--kind] [--status] [--tag]`
   and `monoagentcli application get <id>` (includes detail row + full status-log
   history). Read-only, no side effects.
5. **Workflow nodes** (`applications.create`, `applications.set_status`,
   `applications.tag`, `applications.list`) wrap the identical store methods used by
   the CLI — later phases (discovery writing new `pending` rows, apply-automation
   moving rows to `applied`) go through the same validated path, never raw SQL.

## Error Handling

- **Illegal status transition** (including from a terminal state) → typed error,
  surfaced verbatim by the CLI and returned as a node execution error.
- **Kind/detail mismatch** → rejected at the store boundary before any row is written.
- **Missing required per-kind fields** (job: `company`, `url`; tender: `issuing_org`,
  `url`, `submission_deadline`) → validated in Go before insert, with a field-named
  error message — SQLite's own constraints are the second line of defense, not the
  first.
- **Concurrent status transitions on the same application** → the whole transition
  (read current status, validate, write both rows) is one SQLite transaction; a race
  loses via normal locking rather than double-transitioning or corrupting the ledger.
- **Ledger integrity** — `application_status_log` has no UPDATE/DELETE path in the
  store API at all; append-only is structural.

## Testing

TDD per project convention:
- `internal/applications/store_test.go` — create job/tender (valid + missing-field
  failure), every valid transition appends exactly one ledger row, every invalid
  transition (including from a terminal state) is rejected and appends nothing,
  kind/detail mismatch is rejected, tag add/remove round-trips, `List` filters
  (`kind`, `status`, `tag`) return correct sets.
- `internal/nodes/applications/*_test.go` — each node's `Execute` delegates correctly
  to the store and maps store errors to node errors (thin-wrapper tests, not
  reimplementing store logic).
- CLI integration test — add → set-status → show → list happy path, plus at least
  one illegal-transition failure case, asserting on exact output.

## Out of Scope (this phase)

- Discovery/scraping, profile knowledge-graph ingestion, document generation, chat
  matching, and apply automation — each is a separate future spec (Phases 2–6 above).
- Normalizing `required_certifications` / `bid_documents_required` into their own
  tables — deferred until Phase 6 needs to query them structurally; comma-separated
  text is sufficient for Phase 1's CRUD surface.
- A UI (Wails GUI) for applications — CLI only per the user's explicit requirement;
  the GUI is addressed in Phase 6.
