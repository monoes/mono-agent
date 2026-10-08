# License audit 1 of 2: who holds the copyright

Date 2026-10-08. Tool: `scripts/licenseaudit/copyright.sh` (plan B5d, Task 1). Engineering evidence for the owner and for counsel; not legal advice.

**Method.** `git shortlog -sne HEAD` groups the history by name and address. For everyone who is not the owner, `git log --author` lists the files they touched and `git blame -w` counts the lines of those files that are still theirs at HEAD. The owner's addresses are the ones given to the script: nokhodian@gmail.com, 5457777+nokhodian@users.noreply.github.com and nokhodian@users.noreply.github.com (unconfirmed until the owner says so). A bot's version bumps hold no expression. Anyone else with a line still in the tree needs to consent, or the line must be replaced.

## Contributors at commit 2e76ad6e

| Contributor | Commits | Files that still hold their lines | Lines | Can the owner relicense |
|---|---|---|---|---|
| nokhodian <nokhodian@gmail.com> | 2207 | all | | yes (the owner) |
| morteza <5457777+nokhodian@users.noreply.github.com> | 319 | all | | yes (the owner) |
| Morteza Nokhodian <5457777+nokhodian@users.noreply.github.com> | 224 | all | | yes (the owner) |
| morteza <nokhodian@gmail.com> | 41 | all | | yes (the owner) |
| nokhodian <nokhodian@users.noreply.github.com> | 33 | all | | yes (the owner) |
| dependabot[bot] <49699333+dependabot[bot]@users.noreply.github.com> | 22 | manifests and lockfiles | | yes (a bot: version bumps hold no expression; confirm) |
| Elnaz sadat Sajad <elnazsajad@Elnazs-MacBook-Air.local> | 3 | internal/bot/x/bot.go, internal/workflow/hybrid_store.go | 32 | **needs consent** (or replace those lines) |
| Morteza Nokhodian <nokhodian@gmail.com> | 3 | all | | yes (the owner) |
| Minakgr <97474803+Minakgr@users.noreply.github.com> | 1 | wails-app/app_applications.go, wails-app/app_connections.go, wails-app/app_files.go, wails-app/app.go, wails-app/app_nodes.go, wails-app/app_orgs_design.go, wails-app/app_orgs.go, wails-app/app_update.go, wails-app/app_vault.go, wails-app/app_workflows.go, wails-app/proc_unix.go, wails-app/proc_windows.go | 34 | **needs consent** (or replace those lines) |

## What git cannot see

- **Code that names another origin.** `internal/jevpick/snapshot.js:1-2` says it is adapted from browser-use/jev-ultrafast (MIT License, Copyright (c) 2026 Browser Use): third-party code, not the owner's to relicense; it stays under its own notice, which `NOTICE` carries (Task 2). `internal/config/schemas.go:6` says it is ported from a Python `src/services/schemas.py`: the owner confirms that source was theirs. `CODE_OF_CONDUCT.md:120` is adapted from the Contributor Covenant (CC BY 4.0): keep its attribution; it is a document, not code.
- **No inbound terms.** `CONTRIBUTING.md` has no CLA, DCO or license statement for contributions, so every contribution came in under the license the project showed at the time (MIT). Task 3 adds a "Contribution terms" section before any new terms apply.
- **Outside git.** Code given to the owner by paste, e-mail or an assistant leaves no trace in `git shortlog`. Commits co-authored by an AI assistant appear under the owner's identity; what that means for ownership is a question for counsel.

## Decisions for the owner

1. Confirm that the three owner addresses above are all theirs.
2. For each **needs consent** row: ask the contributor, or replace their lines before the license changes (the row lists the files).
3. Confirm the origin of `internal/config/schemas.go`.
