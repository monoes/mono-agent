# Workflow marketplace — curation policy (closes issue #25's policy phase)

## Why policy before plumbing

Importing a workflow is equivalent to executing code — `core.code` runs
inline JavaScript, `system.execute_command` runs shell commands, `db.*`
nodes execute arbitrary queries, and template expressions can reach local
files, all with the OS user's own privileges (see AGENTS.md's "Importing a
workflow is equivalent to executing code" warning and SECURITY.md's
Resource limits section). A marketplace that makes untrusted imports
one-click turns that existing risk into a supply-chain surface: the whole
point of a marketplace is to make people trust and click "install" on code
they didn't write, faster than they'd otherwise review it.

This repo has direct precedent for taking curation seriously before
shipping distribution: dead action templates for 13 platforms were deleted
outright rather than left as stale, unreviewed inventory (see the
2026-08-28 trust-hygiene record referenced from earlier planning docs).
The same discipline applies here — this document is the policy that must
exist and be followed before any submission/install plumbing ships, not a
formality written after the fact.

## Scope of this document

This is Phase 1 (policy) of issue #25. It intentionally does **not** ship:
hosting infrastructure, a submission web UI, or install-time code changes
to `workflow import`. Those are Phase 2 (plumbing), deliberately deferred —
building distribution mechanics before the policy that governs what gets
distributed is exactly the mistake this document exists to avoid. Phase 2
should be scoped as its own follow-up once this policy has actually been
exercised against a handful of real submissions and proven workable.

## Submission review checklist

Every template submitted for the marketplace must pass this checklist
before publication. No auto-publish — a human reviewer works through this
list for every submission, every version bump.

1. **Manifest present and valid** (see format below) — id, version,
   license, author, node-type list, and declared external calls all
   present and accurate against the actual workflow JSON.
2. **Node-type list matches reality**: every node type actually used in
   the workflow JSON appears in the manifest's `node_types` list. A
   mismatch is an automatic reject (either the manifest is stale or
   someone's hiding what the workflow does).
3. **Security review is mandatory, not optional, when the workflow
   contains any of**:
   - `core.code` (arbitrary JavaScript)
   - `system.execute_command` (shell execution)
   - `http.ssh`
   - any `db.*` node
   - any node field that looks like it accepts a credential/secret value
     directly in config (as opposed to via `credential_id`/vault
     reference — a template that asks the user to paste a raw API key
     into a template field, rather than resolving it from their own vault,
     is a reject on its own)

   The security review reads every `core.code`/`system.execute_command`
   body in full and confirms it does what the description claims — no
   obfuscated or minified inline code is accepted.
4. **External calls declared**: every outbound HTTP/API call target
   (domains, not just node types) the workflow makes must be listed in the
   manifest's `external_calls`. A template that talks to an undeclared
   domain is a reject.
5. **No embedded credentials**: grep the workflow JSON for anything
   credential-shaped (API-key-looking strings, tokens, connection strings)
   before publication — automated pre-check, human confirms no false
   negative.
6. **License is OSI-approved or explicitly "all rights reserved, personal
   use only"** — no ambiguous/missing license (see Licensing below).

Submissions that fail any item are rejected with the specific failing item
named, not silently dropped — the submitter gets an actionable reason.

## Template manifest format

```json
{
  "manifest_version": 1,
  "id": "morning-briefing",
  "version": "1.0.0",
  "name": "Morning Briefing",
  "description": "RSS -> AI summary -> human review -> email digest",
  "author": "someone@example.com",
  "license": "MIT",
  "node_types": [
    "trigger.schedule",
    "system.rss_read",
    "core.filter",
    "service.openrouter",
    "core.human_in_loop",
    "comm.email_send"
  ],
  "external_calls": [
    "openrouter.ai",
    "<the RSS feed's own host, workflow-specific>"
  ],
  "requires_credentials": ["openrouter", "smtp"],
  "checksum_sha256": "<sha-256 of the canonical workflow JSON, hex-encoded>",
  "published_at": "2026-09-01T00:00:00Z"
}
```

Field notes:

- `manifest_version` is the manifest schema's own version (this document
  defines v1), independent of the template's own `version`.
- `checksum_sha256` is computed over the exact workflow JSON bytes being
  published. Phase 2's install path verifies this before import — a
  mismatch means the file was tampered with or corrupted in transit and
  must hard-fail the install, not warn-and-continue.
- `requires_credentials` names the credential *kinds* the workflow expects
  (matching `connect <platform>`/vault kind names) — never actual secret
  values. This is what powers the "this needs API keys for X, Y" install
  warning in Phase 2.
- `node_types` and `external_calls` are exactly the "this is code" install
  warning's content: Phase 2's install UX (still unbuilt) must render both
  lists to the user, unconditionally, before import proceeds — matching
  the tone of the existing `workflow import` warning in AGENTS.md/README.

## Versioning and takedown

- **Versioning**: semver (`MAJOR.MINOR.PATCH`) in the manifest's `version`
  field. A republished template with the same `id` and a lower-or-equal
  version than what's already published is rejected — versions are
  monotonic per id, no silent overwrites.
- **Takedown**: any published template can be pulled by the maintainer at
  any time, for any of: a reported security issue, license violation,
  platform-ToS conflict (mirroring this repo's own Usage Policy stance on
  social automation), or maintainer discretion for a low-single-maintainer
  project. Pulling a template does not retroactively affect users who
  already imported it locally — `workflow import` copies the JSON into the
  user's own database; the marketplace has no ongoing connection to
  already-imported workflows. A takedown removes future discoverability
  only, consistent with the local-first, no-phone-home design documented
  in SECURITY.md.
- No auto-publish, ever, for a resubmission triggered by a version bump —
  every version goes back through the full submission review checklist
  above. A template that passed review at 1.0.0 is not grandfathered at
  2.0.0.

## Licensing

Recorded here per issue #25's requirement (also to be mirrored into
CONTRIBUTING.md when Phase 2 plumbing lands): submitted templates must
declare an explicit license in their manifest. Recommended default for
templates intended for broad reuse: **MIT**, matching this repo's own
license and minimizing friction for reuse. Templates the author wants to
keep more restricted may use "all rights reserved, personal use only" —
but that must be stated explicitly, not left blank; "no license = no
public template" is the fallback DECISION, not an oversight (an unlicensed
public template creates the same ambiguous-rights problem #4/#12's parent
security review flagged for the repo's own root LICENSE, just at the
per-template level).

## What Phase 2 (plumbing, not built here) will need

Left explicitly as follow-up, listed so it isn't lost:

- Checksum verification wired into an install path (likely a new
  `workflow templates install <manifest+file>` command, or a `--manifest`
  flag on the existing `workflow import`) — hard-fail on mismatch.
- The install-time "this is code" warning UX (CLI confirmation prompt +
  GUI modal) rendering `node_types`/`external_calls`/`requires_credentials`
  from the manifest before import proceeds.
- An actual hosting/discovery mechanism — out of scope to design here; the
  policy above is written to be hosting-agnostic (works whether templates
  end up in a GitHub repo, a static JSON index, or something else).
