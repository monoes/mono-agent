# Mandatory monoes.me Account — B5d: the license workstream Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The owner can change the project's license and close the source on informed terms: two audits (who holds the copyright, which dependencies bind the license) are ready to run now and at any time, because they only read; the license swap and the public releases-only repository are specified in full and wait for the owner's go.

**Architecture:** Stdlib-only tooling in `scripts/licenseaudit` (a bash audit of the git history for copyright, a Python audit of the Go and npm dependencies that also writes `NOTICE`), plus two owner-gated tasks with complete code and edits for everything that does not depend on the owner's terms. A drift test per gated task keeps every license mention and every release URL in agreement, and each test carries its own small helpers, so none depends on another plan's files.

**Independent of release R.** This plan is the license half of the lead's split of B5b; the documentation half, `2026-10-05-monoes-account-gate-b5b-docs.md`, ships with release R. Nothing here is part of R and nothing in R waits for it. **The audits (Tasks 1 and 2) only read the repository and can run at any time**, before or after R. Tasks 3 and 4 each begin with a gate step and wait for the owner; they edit documents that R's plan rewrites, so they run after R's documents have merged (their line numbers are those of that tree, and every edit also quotes the text it changes, so it can be found on any tree).

**Not legal advice.** This is engineering guidance. The owner chooses the terms; counsel should read the audit reports, the license text, the contributor-consent approach and the closed-distribution steps before anything irreversible happens. The license text itself is an input the owner supplies: this plan never invents it.

**Tech Stack:** Go 1.26 (stdlib only), bash and Python 3 (stdlib only), Markdown, JSON, GitHub Actions YAML.

**Spec:** `docs/mastermind/specs/2026-10-05-monoes-account-gate-design.md` (§10 steps 1 to 5, §4.7, §12) and `docs/mastermind/plans/2026-10-05-monoes-account-gate-index.md`. Where this plan differs from the index (§2, §3.6) or from spec §13, the index and spec §13 win.

## Global Constraints

From the index §2, the lines that bind this plan, verbatim.

- `devaccount` is a build tag, never set by `release.yml`. Test seams panic unless `testing.Testing()`. No environment variable relaxes the gate in a default build, and a default build honors `MONOES_BASE_URL` nowhere: the library talks only to monoes.me, `library login` against another host refuses and names `-tags devaccount`, and the session token is sent only to the host that issued it. A local monoes.me dev server needs a `-tags devaccount` build.
- Never print, log or put in a test's output a token, a refresh token or a key. Test fixtures use throwaway keys generated in the test.
- Files stay under 500 lines; split by responsibility. Conventional commit subjects, `type(scope): subject`. Never commit secrets or `.env` files.
- Only B5b edits `README.md`, `AGENTS.md`, `SECURITY.md`, `SUPPORT.md`, `docs/COMPARISON.md`, `CONTRIBUTING.md`, `CHANGELOG.md` and the claim strings in `internal/i18n/locales`, so parallel phases do not conflict. The new desktop strings under `account.*` in `wails-app/frontend/src/locales/{en,es}.json` belong to B4. Other phases add `ref` text, and a minimal `AGENTS.md` line, only where a test requires it.

B5d is the license half of B5b (the lead's split), so its Tasks 3 and 4 keep B5b's right to edit those files, after R's documents have merged. Task 4 edits `release.yml` and must leave B5a's `release-guard` job, the `needs` that names it, and the `--options runtime` signatures and their checks (B5a's Task 5b), as they are.

## Review Focus

1. **Relicensing code the owner cannot relicense.** A contributor's lines still in the tree (at plan time `Elnaz sadat Sajad` and `Minakgr`) and third-party code that names its own origin (`internal/jevpick/snapshot.js`, MIT) are not the owner's to relicense, and the project has no inbound terms (no CLA, no DCO). Pinned by `Copyright.test_verdicts` (a guest under a mixed-case address, a bot, a deleted file), Task 1's report, Task 3's gate prerequisites (no **needs consent** row open) and its "Contribution terms".
2. **A copyleft or unclassified dependency that passes silently, or a `go list -e` error that drops modules from the audit.** Pinned by `Classify.test_licenses` (an MPL text that names the AGPL must stay MPL: it did not, the first time), `Classify.test_flagged`, `Npm.test_build_tooling_is_separate_and_a_missing_license_is_unknown`, `Notice.test_keeps_license_text_and_names_what_is_missing` (Task 2) and the report's "Load errors" section.
3. **Closing the repository strands every installed client.** An install older than the repoint release keeps asking the old `releases/latest` URL; once the repository is private that answers 404, so it can never update, and `key_unknown` tells such an install to run `update`. Pinned by Task 4's decision step (a waiting window, or a name swap, chosen before any edit) and `TestReleaseURLsNameOnlyThePublicRepo`.
4. **A license mention that survives the swap:** the README badge and license section, the OpenAPI document, the desktop's copyright line, the comparison page, the usage policy and the library packages' `"license": "MIT"`. Pinned by `TestLicenseMentionsAgree` (Task 3), which reads the owner's two constants and fails on the old name.
5. **A release path that still needs the private repository:** `install.sh`, the crash-report `--repo`, the advisory link, the `release.yml` download URLs, and `actions/attest-build-provenance`, which GitHub offers for private repositories only on Enterprise Cloud and which would stop every release once the source is closed. Pinned by `TestReleaseURLsNameOnlyThePublicRepo` and by Task 4's proof of the workflow on a throwaway pair of repositories.

---

## Ordering (read first)

- **Tasks 1 and 2 stand alone.** They change nothing a user can see and can land at any time as `chore(license)` commits (every merge to master releases; a `chore` commit is a patch release). Task 2's `NOTICE` is a prerequisite of Task 4, whose release workflow copies it.
- **Tasks 3 and 4 are independent of each other** (the owner may change the license without closing the source, or the reverse) and each waits for its gate step.
- **Owner inputs.** Task 3: `LICENSE.new` (the license text), `LICENSE_NAME`, `LICENSE_BADGE`, `COPYRIGHT_LINE`, `LICENSE_SUMMARY`, `CONTRIBUTION_TERMS`. Task 4: `RELEASES_REPO`, `SOURCE_REPO`, `RELEASES_BRANCH`, the choice between repointing and swapping names, and a token stored as the secret `RELEASES_REPO_TOKEN`. Task 1 also needs the owner's e-mail addresses confirmed.
- **Line numbers.** An edit names the lines its file has before that task's first edit to it, in the tree after R's documents (the documentation plan) have merged; For a file that another plan also edits (`release.yml` gets B5a's `release-guard` job and its hardened-runtime signing, `update.go` B5a's daemon restart, `internal/httpapi/openapi.yaml` B3b's account answers) the numbers are those at `3cc58601`, which those plans move, and these edits do not touch what they add. An earlier edit in the same file moves the later ones too, so find each edit by its quoted text, or apply a file's edits from the last to the first.
- `<scratchpad>` below is the session's scratchpad directory.

---

### Task 1: L1 — the copyright audit

**Files:**
- Create: `scripts/licenseaudit/copyright.sh`, `scripts/licenseaudit/test_copyright.py`, `docs/mastermind/reports/2026-10-05-license-copyright-audit.md`
- Test: `scripts/licenseaudit/test_copyright.py`

**Interfaces:** Consumes `git` and the repository history. Produces `bash scripts/licenseaudit/copyright.sh <owner e-mails, comma-separated> [repo]`, a Markdown table on stdout: contributor, commits, files that still hold their lines, lines, can the owner relicense (`yes` or `**needs consent**`).

- [ ] **Step 1: Write the failing test.** `scripts/licenseaudit/test_copyright.py` builds a throwaway repository with an owner, a guest who left lines behind under a mixed-case address, and a bot, and runs the script on it:

```python
import os
import subprocess
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))


def write(path, text):
    with open(path, "w") as f:
        f.write(text)


class Copyright(unittest.TestCase):
    """The copyright audit's verdicts, on a throwaway repository with an owner, a
    guest who left lines behind under a mixed-case address, and a bot."""

    def test_verdicts(self):
        with tempfile.TemporaryDirectory() as d:
            def run(author, *args):
                name, email = author.split("|")
                env = {**os.environ, "GIT_AUTHOR_NAME": name, "GIT_AUTHOR_EMAIL": email, "GIT_COMMITTER_NAME": name, "GIT_COMMITTER_EMAIL": email}
                subprocess.run(["git", "-C", d, "-c", "commit.gpgsign=false", *args], check=True, capture_output=True, env=env)

            def put(name, text):
                write(os.path.join(d, name), text)

            owner, guest, bot = "Owner|o@x.test", "Guest Person|G@x.test", "dependabot[bot]|1+dependabot[bot]@users.noreply.github.com"
            run(owner, "init", "-q")
            put("a.txt", "one\ntwo\nthree\n")
            put("go.mod", "v1\n")
            put("gone.txt", "temporary\n")
            run(owner, "add", ".")
            run(owner, "commit", "-q", "-m", "owner")
            put("b.txt", "guest line\nguest line 2\n")
            put("a.txt", "one\ntwo\nthree\nguest in a\n")
            run(guest, "add", ".")
            run(guest, "commit", "-q", "-m", "guest")
            run(guest, "rm", "-q", "gone.txt")
            run(guest, "commit", "-q", "-m", "guest removes a file")
            put("go.mod", "v2\n")
            run(bot, "add", ".")
            run(bot, "commit", "-q", "-m", "bump")
            out = subprocess.run(["bash", os.path.join(HERE, "copyright.sh"), "o@x.test", d], check=True, capture_output=True, text=True).stdout
        rows = {line.split(" <")[0][2:]: line for line in out.splitlines() if line.startswith("| ") and "<" in line}
        self.assertIn("yes (the owner)", rows["Owner"])
        self.assertIn("a.txt, b.txt | 3 | **needs consent**", rows["Guest Person"])
        self.assertIn("yes (a bot", rows["dependabot[bot]"])


if __name__ == "__main__":
    unittest.main()
```

- [ ] **Step 2: Run it and watch it fail.** `PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts/licenseaudit -p 'test_copyright.py'`. Expected: an error, `CalledProcessError ... copyright.sh ... returned non-zero exit status 127` (no such file).
- [ ] **Step 3: Write the script.** `scripts/licenseaudit/copyright.sh` (portable to the bash 3.2 of macOS):

```bash
#!/usr/bin/env bash
# Who still has code in the tree: the copyright audit of the license workstream
# (spec §10 step 1). Prints a Markdown table: for each person in the history,
# whether the owner can relicense their work. The owner's own commits can be
# relicensed by the owner; a bot's version bumps hold no expression; anyone else
# with a line still in the tree must consent, or the line must be replaced.
#
#   bash scripts/licenseaudit/copyright.sh <owner e-mails, comma-separated> [repo]
set -euo pipefail
owners=",$(printf '%s' "${1:?usage: copyright.sh <owner e-mails, comma-separated> [repo]}" | tr 'A-Z' 'a-z'),"
cd "${2:-.}"
echo "## Contributors at commit $(git rev-parse --short HEAD)"
echo
echo "| Contributor | Commits | Files that still hold their lines | Lines | Can the owner relicense |"
echo "|---|---|---|---|---|"
git shortlog -sne HEAD | while IFS=$'\t' read -r commits ident; do
  commits=$(echo $commits)
  email=$(printf '%s' "${ident##*<}" | tr -d '>' | tr 'A-Z' 'a-z')
  case "$owners" in *",$email,"*) echo "| $ident | $commits | all | | yes (the owner) |"; continue ;; esac
  case "$ident" in *"[bot]"*) echo "| $ident | $commits | manifests and lockfiles | | yes (a bot: version bumps hold no expression; confirm) |"; continue ;; esac
  files="" lines=0
  while IFS= read -r f; do
    [ -f "$f" ] || continue
    n=$(git blame -w --line-porcelain HEAD -- "$f" | tr 'A-Z' 'a-z' | grep -c "^author-mail <$email>\$" || true)
    if [ "$n" -gt 0 ]; then files="${files:+$files, }$f"; lines=$((lines + n)); fi
  done < <(git log -i -F --author="<$email>" --name-only --format= | sort -u)
  if [ "$lines" -eq 0 ]; then echo "| $ident | $commits | none | 0 | yes (none of their lines is left) |"
  else echo "| $ident | $commits | $files | $lines | **needs consent** (or replace those lines) |"; fi
done
```

- [ ] **Step 4: Run the test.** The same command. Expected: `Ran 1 test ... OK`.
- [ ] **Step 5: Run it on the history.** The owner's addresses at plan time are `nokhodian@gmail.com`, `5457777+nokhodian@users.noreply.github.com` and `nokhodian@users.noreply.github.com` (the names `nokhodian`, `morteza`, `Morteza Nokhodian`); ask the owner to confirm them. `mkdir -p docs/mastermind/reports`, then `bash scripts/licenseaudit/copyright.sh nokhodian@gmail.com,5457777+nokhodian@users.noreply.github.com,nokhodian@users.noreply.github.com > <scratchpad>/l1-table.md`. Expected at plan time: the owner rows, `dependabot[bot]` (a bot), `Elnaz sadat Sajad` with 31 lines in `internal/bot/x/bot.go` and `internal/workflow/hybrid_store.go`, and `Minakgr` with 33 lines in eleven `wails-app` files, both **needs consent**. Trust the table, not these numbers.
- [ ] **Step 6: Read what git cannot see.** `git grep -nIiE "copyright \(c\)|licensed under|adapted from|ported from|derived from|copied from" -- . ':!docs' ':!CHANGELOG.md' ':!package-lock.json' ':!wails-app/frontend/package-lock.json' ':!go.sum' ':!chrome-extension/adapters/fixtures' ':!internal/bot/producthunt/testdata'` and read each hit that is not a prose use of those words. At plan time the real ones are `internal/jevpick/snapshot.js:1`, `internal/config/schemas.go:6` and `CODE_OF_CONDUCT.md:120`; add any other to the tail below.
- [ ] **Step 7: Assemble the report.** Write `<scratchpad>/l1-head.md` and `<scratchpad>/l1-tail.md` with the Write tool, then `cat <scratchpad>/l1-head.md <scratchpad>/l1-table.md <scratchpad>/l1-tail.md > docs/mastermind/reports/2026-10-05-license-copyright-audit.md`. The head:

````markdown
# License audit 1 of 2: who holds the copyright

Date 2026-10-05. Tool: `scripts/licenseaudit/copyright.sh` (plan B5d, Task 1). Engineering evidence for the owner and for counsel; not legal advice.

**Method.** `git shortlog -sne HEAD` groups the history by name and address. For everyone who is not the owner, `git log --author` lists the files they touched and `git blame -w` counts the lines of those files that are still theirs at HEAD. The owner's addresses are the ones given to the script: nokhodian@gmail.com, 5457777+nokhodian@users.noreply.github.com and nokhodian@users.noreply.github.com (unconfirmed until the owner says so). A bot's version bumps hold no expression. Anyone else with a line still in the tree needs to consent, or the line must be replaced.

````

  The tail:

````markdown

## What git cannot see

- **Code that names another origin.** `internal/jevpick/snapshot.js:1-2` says it is adapted from browser-use/jev-ultrafast (MIT License, Copyright (c) 2026 Browser Use): third-party code, not the owner's to relicense; it stays under its own notice, which `NOTICE` carries (Task 2). `internal/config/schemas.go:6` says it is ported from a Python `src/services/schemas.py`: the owner confirms that source was theirs. `CODE_OF_CONDUCT.md:120` is adapted from the Contributor Covenant (CC BY 4.0): keep its attribution; it is a document, not code.
- **No inbound terms.** `CONTRIBUTING.md` has no CLA, DCO or license statement for contributions, so every contribution came in under the license the project showed at the time (MIT). Task 3 adds a "Contribution terms" section before any new terms apply.
- **Outside git.** Code given to the owner by paste, e-mail or an assistant leaves no trace in `git shortlog`. Commits co-authored by an AI assistant appear under the owner's identity; what that means for ownership is a question for counsel.

## Decisions for the owner

1. Confirm that the three owner addresses above are all theirs.
2. For each **needs consent** row: ask the contributor, or replace their lines before the license changes (the row lists the files).
3. Confirm the origin of `internal/config/schemas.go`.
````

- [ ] **Step 8: Commit.**
  - `git add scripts/licenseaudit/copyright.sh scripts/licenseaudit/test_copyright.py docs/mastermind/reports/2026-10-05-license-copyright-audit.md`
  - `git commit -m "chore(license): copyright audit of the git history" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"`

### Task 2: L2 — the dependency audit and the NOTICE file

**Files:**
- Create: `scripts/licenseaudit/audit.py`, `scripts/licenseaudit/test_audit.py`, `docs/mastermind/reports/2026-10-05-license-dependency-audit.md`, `NOTICE`
- Test: `scripts/licenseaudit/test_audit.py`

**Interfaces:**
- Consumes: `go list` for both Go modules (`.` and `wails-app`), `wails-app/frontend/package-lock.json` and, for texts, `wails-app/frontend/node_modules`.
- Produces: `python3 scripts/licenseaudit/audit.py deps` (the report) and `... notice` (the NOTICE file; it exits non-zero naming any package it has no license text for); `classify(text) -> str`, `flagged(lic) -> bool`, `npm_deps() -> list[dict]` (reads `audit.FRONTEND`), `notice(deps, out) -> list[str]` (the packages without a license text).

- [ ] **Step 1: Write the failing test.** `scripts/licenseaudit/test_audit.py`:

```python
import io
import json
import os
import tempfile
import unittest

import audit

def write(path, text):
    with open(path, "w") as f:
        f.write(text)


class Classify(unittest.TestCase):
    def test_licenses(self):
        cases = {
            "MIT": "Permission is hereby granted, free of charge, to any person obtaining a copy",
            "BSD-3-Clause": "Redistribution and use in source and binary forms, with or without modification... Neither the name of the copyright holder",
            "BSD-2-Clause": "Redistribution and use in source and binary forms, with or without modification, are permitted",
            "Apache-2.0": "Apache License\n Version 2.0, January 2004",
            "ISC": "Permission to use, copy, modify, and/or distribute this software for any purpose with or without fee",
            # The MPL names the GNU licenses as secondary licenses: it must stay the MPL.
            "MPL-2.0": 'Mozilla Public License Version 2.0 ... "Secondary License" means the GNU General Public License, the GNU Lesser General Public License or the GNU Affero General Public License',
            "LGPL": "GNU LESSER GENERAL PUBLIC LICENSE Version 3 ... the GNU General Public License",
            "UNKNOWN": "All rights reserved. Do not copy.",
        }
        for want, text in cases.items():
            self.assertEqual(audit.classify(text), want)

    def test_flagged(self):
        for lic, want in {"MIT": False, "BSD-3-Clause+MIT+Public-Domain": False, "(MIT OR WTFPL)": False,
                          "MPL-2.0": True, "LGPL": True, "CC-BY-SA-4.0": True, "UNKNOWN": True}.items():
            self.assertEqual(audit.flagged(lic), want, lic)


class Npm(unittest.TestCase):
    def test_build_tooling_is_separate_and_a_missing_license_is_unknown(self):
        with tempfile.TemporaryDirectory() as d:
            lock = {"packages": {"": {}, "node_modules/react": {"version": "19.0.0", "license": "MIT"},
                                 "node_modules/a/node_modules/lightningcss": {"version": "1.0.0", "license": "MPL-2.0", "dev": True},
                                 "node_modules/bare": {"version": "1.0.0"}}}
            write(os.path.join(d, "package-lock.json"), json.dumps(lock))
            audit.FRONTEND = d
            got = {x["name"]: x for x in audit.npm_deps()}
        self.assertEqual(sorted(got), ["bare", "lightningcss", "react"])
        self.assertTrue(got["lightningcss"]["dev"] and not got["react"]["dev"])
        self.assertEqual(got["bare"]["license"], "UNKNOWN")


class Notice(unittest.TestCase):
    def test_keeps_license_text_and_names_what_is_missing(self):
        with tempfile.TemporaryDirectory() as d:
            write(os.path.join(d, "LICENSE"), "Permission is hereby granted, free of charge\nCopyright (c) Someone")
            out = io.StringIO()
            missing = audit.notice([
                {"name": "example.test/has", "version": "v1", "license": "MIT", "dir": d, "dev": False},
                {"name": "example.test/none", "version": "v1", "license": "MIT", "dir": "", "dev": False},
                {"name": "example.test/tooling", "version": "v1", "license": "MPL-2.0", "dir": "", "dev": True},
            ], out=out)
        self.assertEqual(missing, ["example.test/none"])  # build tooling is not in NOTICE
        for want in ("example.test/has v1 (MIT)", "Copyright (c) Someone", "internal/jevpick/snapshot.js", "Copyright (c) 2026 Browser Use"):
            self.assertIn(want, out.getvalue())
        self.assertNotIn("example.test/tooling", out.getvalue())


if __name__ == "__main__":
    unittest.main()
```

- [ ] **Step 2: Run it and watch it fail.** `PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts/licenseaudit -p 'test_audit.py'`. Expected: `ModuleNotFoundError: No module named 'audit'`.
- [ ] **Step 3: Write the tool.** `scripts/licenseaudit/audit.py`:

```python
#!/usr/bin/env python3
"""Dependency tooling of the license workstream (spec §10 step 2). Run it from the
repository root; it only reads:

  python3 scripts/licenseaudit/audit.py deps   > docs/mastermind/reports/2026-10-05-license-dependency-audit.md
  python3 scripts/licenseaudit/audit.py notice > NOTICE

It needs the Go module cache and, for the desktop frontend's license texts,
node_modules matching the lockfile (`npm ci --ignore-scripts` in wails-app/frontend)."""
import io
import json
import os
import subprocess
import sys

FRONTEND = "wails-app/frontend"
# release.yml builds the CLI for five platforms with CGO off, and the desktop app
# (its own Go module) with CGO on.
TARGETS = [
    ("cli", ".", "./cmd/monoagentcli", "", "0", ["darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64", "windows/amd64"]),
    ("app", "wails-app", ".", "webkit2_41", "1", ["darwin/arm64", "linux/amd64", "windows/amd64"]),
]
LIST_FORMAT = '{{with .Module}}M {{.Path}} {{.Version}} {{.Dir}}{{"\\n"}}{{end}}{{if .Error}}E {{.ImportPath}}: {{.Error.Err}}{{"\\n"}}{{end}}'
# The MPL text names the AGPL, GPL and LGPL as secondary licenses, and the LGPL and
# AGPL texts quote the GPL: the order matters.
PHRASES = [
    ("MPL-2.0", "mozilla public license"), ("AGPL", "gnu affero general public license"),
    ("LGPL", "gnu lesser general public license"), ("GPL", "gnu general public license"),
    ("Apache-2.0", "apache license version 2.0"), ("MIT", "permission is hereby granted, free of charge"),
    ("ISC", "permission to use, copy, modify, and/or distribute this software for any purpose with or without fee"),
    ("BSD-2-Clause", "redistribution and use in source and binary forms"), ("Public-Domain", "dedicated to the public domain"),
]
# Third-party code that lives in this repository's own files.
VENDORED = [("internal/jevpick/snapshot.js", "browser-use/jev-ultrafast, jev_ultrafast/snapshot.js", "Copyright (c) 2026 Browser Use")]
MIT_TEXT = """Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
"""


def read(path):
    with open(path, errors="replace") as f:
        return f.read()


def classify(text):
    """The license a text grants, or UNKNOWN, so that a person reads it."""
    t = " ".join(text.lower().split())
    for lic, phrase in PHRASES:
        if phrase in t:
            return "BSD-3-Clause" if lic == "BSD-2-Clause" and "neither the name" in t else lic
    return "UNKNOWN"


def flagged(lic):
    """A license that puts conditions on a closed distribution, or that nobody classified."""
    return any(c in lic.upper() for c in ("GPL", "MPL", "EPL", "CDDL", "SSPL", "CC-BY-SA", "UNKNOWN"))


def license_files(d):
    """(license files, upstream-notice files) of a package directory, top level only.
    The second kind goes into NOTICE but is not classified."""
    lic, extra = [], []
    for name in sorted(os.listdir(d)) if d and os.path.isdir(d) else []:
        n, path = name.lower(), os.path.join(d, name)
        if os.path.isdir(path):
            continue
        if "3rd-party" in n or "third-party" in n or n.startswith("notice"):
            extra.append(path)
        elif n.startswith(("license", "licence", "copying", "unlicense")):
            lic.append(path)
    return lic, extra


def license_of(d):
    files, _ = license_files(d)
    known = {classify(read(f)) for f in files} - {"UNKNOWN"}
    return "+".join(sorted(known)) or "UNKNOWN"


def go_deps():
    """Third-party Go modules linked into every target on every platform (build
    constraints pull different modules per OS), and the load errors that
    `go list -e` tolerated, so that none is hidden."""
    mods, errors = {}, set()
    for name, cwd, pkg, tags, cgo, platforms in TARGETS:
        for p in platforms:
            goos, goarch = p.split("/")
            cmd = ["go", "list", "-e", "-deps", "-f", LIST_FORMAT] + (["-tags", tags] if tags else []) + [pkg]
            out = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, check=True,
                                 env={**os.environ, "GOOS": goos, "GOARCH": goarch, "CGO_ENABLED": cgo}).stdout
            for line in out.splitlines():
                kind, _, rest = line.partition(" ")
                f = rest.split()
                if kind == "E":
                    errors.add(rest)
                elif kind == "M" and len(f) == 3 and not f[0].startswith("github.com/monoes/mono-agent"):
                    m = mods.setdefault((f[0], f[1]), {"name": f[0], "version": f[1], "dir": f[2], "built": [], "dev": False})
                    if f"{name} {p}" not in m["built"]:
                        m["built"].append(f"{name} {p}")
    for m in mods.values():
        m["license"] = license_of(m["dir"])
    return [mods[k] for k in sorted(mods)], sorted(errors)


def npm_deps():
    """The lockfile's SPDX id for each package, and whether it is development-only."""
    out = []
    for key, p in sorted(json.loads(read(f"{FRONTEND}/package-lock.json"))["packages"].items()):
        if key:
            d = os.path.join(FRONTEND, key)
            out.append({"name": key.rsplit("node_modules/", 1)[1], "version": p.get("version", ""), "license": p.get("license") or "UNKNOWN",
                        "dir": d if os.path.isdir(d) else "", "built": ["npm"], "dev": bool(p.get("dev"))})
    return out


def kinds(deps):
    return ", ".join(sorted({d["license"] for d in deps}))


def report(go_mods, load_errors, npm):
    ship = [d for d in npm if not d["dev"]]
    tooling = [d for d in npm if d["dev"]]
    sha = subprocess.run(["git", "rev-parse", "--short", "HEAD"], capture_output=True, text=True).stdout.strip()
    print(f"# Dependency license audit\n\nCommit {sha}; `python3 scripts/licenseaudit/audit.py deps`. Not legal advice.\n")
    print(f"- Go modules linked into the CLI or the app: {len(go_mods)} (licenses: {kinds(go_mods)})")
    print(f"- npm packages that ship in the app: {len(ship)} (licenses: {kinds(ship)})")
    print(f"- npm packages that are build tooling only, not distributed: {len(tooling)} (licenses: {kinds(tooling)})\n")
    print("## Flagged: copyleft or unclassified, in what ships\n")
    hits = [d for d in go_mods + ship if flagged(d["license"])]
    for d in hits:
        print(f"- `{d['name']}` {d['version']}: {d['license']} ({', '.join(d['built'])})")
    print("" if hits else "None.")
    if load_errors:
        print("## Load errors that `go list -e` tolerated (a module reachable only through them is missing below)\n")
        print("\n".join(f"- {e}" for e in load_errors) + "\n")
    print("## Go modules\n\n| Module | Version | License | Built into |\n|---|---|---|---|")
    for d in go_mods:
        print(f"| {d['name']} | {d['version']} | {d['license']} | {', '.join(d['built'])} |")


def notice(deps, out=sys.stdout):
    """The third-party notices of what ships (not build tooling): every license text
    and NOTICE file, verbatim, which MIT, BSD, ISC and Apache-2.0 ask to accompany a
    binary. Returns the packages it found no license text for, so that the file is
    never silently incomplete."""
    rule, missing = "=" * 78, []
    print("Mono Agent includes third-party software. The licenses of that software follow.", file=out)
    for d in deps:
        lic, extra = license_files(d["dir"])
        if d["dev"]:
            continue
        if not lic:
            missing.append(d["name"])
            continue
        print(f"\n{rule}\n{d['name']} {d['version']} ({d['license']})\n{rule}\n", file=out)
        if "MPL" in d["license"] and d["name"].startswith("github.com/"):
            print(f"Source code: https://{d['name']}\n", file=out)  # MPL-2.0 §3.2: say where the source is
        for f in lic + extra:
            print(read(f).strip() + "\n", file=out)
    for where, origin, copyright in VENDORED:
        print(f"\n{rule}\n{where} (adapted from {origin}) (MIT)\n{rule}\n{copyright}\n\n{MIT_TEXT}", file=out)
    return missing


if __name__ == "__main__":
    if sys.argv[1:] not in (["deps"], ["notice"]):
        sys.exit("usage: python3 scripts/licenseaudit/audit.py deps|notice")
    go_mods, load_errors = go_deps()
    npm = npm_deps()
    if sys.argv[1] == "deps":
        report(go_mods, load_errors, npm)
    else:
        buf = io.StringIO()
        gone = notice(go_mods + npm, out=buf)
        if gone:
            sys.exit(f"no license text on disk for {gone} (run `npm ci --ignore-scripts` in {FRONTEND} first)")
        sys.stdout.write(buf.getvalue())
```

- [ ] **Step 4: Run the tests.** `PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts/licenseaudit -v`. Expected: `Ran 5 tests ... OK` (the copyright test of Task 1 included).
- [ ] **Step 5: Prepare the inputs (one network step).** `mkdir -p wails-app/frontend/dist && touch wails-app/frontend/dist/.keep` (the app's `go:embed` needs the folder; it is gitignored, so `go list -e` reports no error), and in `wails-app/frontend` run `npm ci --ignore-scripts` to put the license texts of the lockfile's exact versions in `node_modules/` (about 140 MB; gitignored).
- [ ] **Step 6: Run the audit.** `python3 scripts/licenseaudit/audit.py deps > <scratchpad>/l2-report.md`. Expected at plan time: 74 Go modules, 111 npm packages that ship (licenses ISC and MIT only), 118 that are build tooling, and under "Flagged" exactly one line, `github.com/go-sql-driver/mysql` v1.10.1 MPL-2.0 (the CLI builds). A "Load errors" section means a module is missing from the report: fix its cause first. A person reads any `UNKNOWN`.
- [ ] **Step 7: Write the findings and assemble the report.** Write `<scratchpad>/l2-tail.md` with the Write tool, then `cat <scratchpad>/l2-report.md <scratchpad>/l2-tail.md > docs/mastermind/reports/2026-10-05-license-dependency-audit.md`:

````markdown

## Findings

1. **`github.com/go-sql-driver/mysql` is MPL-2.0 and is linked into the CLI** (the MySQL node, `internal/nodes/db/mysql.go`). MPL-2.0 is a per-file copyleft: a binary that includes it may be distributed under other terms for the rest of the program if recipients are told how to get the source of the MPL-covered files (MPL-2.0 §3.2) and the MPL text travels with the binary. No modified copy is vendored here, so naming the module and version as the source meets that; `NOTICE` does it (`Source code: https://github.com/go-sql-driver/mysql`). Counsel confirms; the alternative is to drop the node.
2. **No other copyleft code ships.** The twelve MPL-2.0 npm packages (`lightningcss` and its platform binaries) are build tooling and are not in the app's files.
3. **Releases ship binaries without third-party license texts today** (`release.yml`, "Flatten and checksum all release files": binaries, archives and checksums only). MIT, BSD, ISC and Apache-2.0 ask for their text to accompany a binary. `NOTICE` is that text; Task 4 attaches it to every release with one `cp` line, a change that can land alone whether or not the repository is ever closed.
4. **`modernc.org/sqlite`** keeps BSD-3-Clause, MIT (sqlite-vec) and the SQLite public-domain dedication in separate files, plus `LICENSE-3RD-PARTY.md` for the C it transpiles; `NOTICE` includes all of them, and the Apache-2.0 modules' `NOTICE` files likewise.
5. **Vendored code:** `internal/jevpick/snapshot.js` (Browser Use, MIT) is in `NOTICE` by hand (`VENDORED` in `scripts/licenseaudit/audit.py`); add any other vendored file the copyright audit finds.
6. **Not covered:** images and fonts under `assets/` and in the desktop frontend, and the monomind runtime (a separate program the app starts, not linked in).
````

- [ ] **Step 8: Generate `NOTICE`.** `python3 scripts/licenseaudit/audit.py notice > NOTICE`. Expected: exit 0 and about 10,000 lines (a non-zero exit lists the packages with no license text on disk: run the `npm ci` step first; delete the partial `NOTICE` it left). Check `head -3 NOTICE`, `grep -c '^Source code:' NOTICE` (expected: 1) and `grep -n 'Browser Use' NOTICE`.
- [ ] **Step 9: Check the tree.** `git status --short` lists only the files above (`node_modules/` and `dist/` are ignored), then `go build ./...` and `gofmt -l .` (no output).
- [ ] **Step 10: Commit.**
  - `git add scripts/licenseaudit/audit.py scripts/licenseaudit/test_audit.py docs/mastermind/reports/2026-10-05-license-dependency-audit.md NOTICE`
  - `git commit -m "chore(license): dependency audit and the NOTICE file" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"`

### Task 3: L3 — swap the license and every license mention (OWNER-GATED)

- [ ] **Step 1: Gate. Do not start until the owner has chosen the license terms and says go.** This plan never invents them; the inputs, by name:
  - `LICENSE.new`: the complete license text, saved by the owner at the repository root (uncommitted).
  - `LICENSE_NAME` (prose, for example `Apache License 2.0`) and `LICENSE_BADGE` (the same for the README badge, shields.io-escaped: `-` doubled, a space `_`).
  - `COPYRIGHT_LINE`, for `wails-app/wails.json` and the macOS About panel (`NSHumanReadableCopyright`). `LICENSE` says `Copyright (c) 2026 Morteza Nokhodian` and `wails.json` says `© 2026 MonoAgent`: the owner picks one.
  - `LICENSE_SUMMARY`, the plain-English bullets for the README's License section; `CONTRIBUTION_TERMS`, the paragraph for `CONTRIBUTING.md` (a CLA, a DCO, or "contributions are licensed under the project's license"), which it lacks today.
  - Prerequisites: no **needs consent** row of Task 1's report is open (replaced, or the contributor agreed in writing and the report says so); Task 2's flagged `go-sql-driver/mysql` is resolved; the owner has read the not-legal-advice note.

**Files:**
- Create: `cmd/monoagentcli/license_mentions_test.go`
- Modify: `LICENSE` (replaced), `README.md`, `docs/COMPARISON.md` (16), `docs/USAGE_POLICY.md` (182), `internal/httpapi/openapi.yaml` (21), `wails-app/wails.json` (17), `CONTRIBUTING.md`
- Test: `cmd/monoagentcli/license_mentions_test.go`; `internal/httpapi` (unchanged)

**Interfaces:** Consumes nothing from the documentation plan: the test carries its own two helpers. Produces nothing.

- [ ] **Step 2: Confirm what there is to change.** `git grep -nE 'SPDX-License-Identifier' -- . ':!docs' ':!data/schemas'` (expected: no output: there is no header to update) and `git grep -n 'About' -- wails-app/frontend/src ':!wails-app/frontend/src/wailsjs'` (expected: only `missingIsAboutJev` hits: no About screen; if one exists, update its license text here). `git grep -nwE 'MIT' -- . ':!docs/mastermind' ':!docs/plans' ':!CHANGELOG.md' ':!NOTICE' ':!architecture.md' ':!package-lock.json' ':!wails-app/frontend/package-lock.json' ':!wails-app/frontend/src/wailsjs'` lists the rest. The seven `automations/*/automation.json`, the five `data/automation-templates/*/automation.json` and `internal/nodes/testdata/hackernews_native/automation.json` carry `"license": "MIT"` for packages published through the library (each package states its own license); `CONTRIBUTING.md:113` and `internal/jevpick/snapshot.js:2` are about other people's MIT. Leave them unless the owner decides the library packages follow the new terms; then change that value in the thirteen files and raise each package's `version` (a package's version must grow; its hash changes: `make library-official` rebuilds them).
- [ ] **Step 3: Write the failing test,** writing the owner's `LICENSE_NAME` and `COPYRIGHT_LINE` into its two constants. `cmd/monoagentcli/license_mentions_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The owner's choice (plan B5d, Task 3, step 1). These three lines are the only
// ones this file's owner edits by hand.
const (
	licenseName   = "<LICENSE_NAME>"
	copyrightLine = "<COPYRIGHT_LINE>"
	oldLicense    = "MIT"
)

var oldLicenseWord = regexp.MustCompile(`\b` + oldLicense + `\b`)

// licenseDoc reads a file of the repository whose license statements this test keeps in agreement.
func licenseDoc(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// licenseSays checks that a file holds each statement, ignoring case and the line
// breaks of a wrapped paragraph.
func licenseSays(t *testing.T, rel string, wants ...string) {
	t.Helper()
	flat := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
	text := flat(licenseDoc(t, rel))
	for _, w := range wants {
		if !strings.Contains(text, flat(w)) {
			t.Errorf("%s does not say %q", rel, w)
		}
	}
}

func TestLicenseMentionsAgree(t *testing.T) {
	if strings.HasPrefix(strings.TrimSpace(licenseDoc(t, "LICENSE")), oldLicense+" License") {
		t.Errorf("LICENSE still holds the %s text", oldLicense)
	}
	// Not checked for the word: CONTRIBUTING.md and the marketplace policy
	// recommend MIT for other people's templates, and the comparison table
	// names the competitors' licenses (its own row is asserted below).
	for _, rel := range []string{"README.md", "docs/USAGE_POLICY.md", "internal/httpapi/openapi.yaml", "wails-app/wails.json"} {
		if oldLicenseWord.MatchString(licenseDoc(t, rel)) {
			t.Errorf("%s still names %s as the license", rel, oldLicense)
		}
	}
	licenseSays(t, "README.md", "licensed under "+licenseName, "[License: "+licenseName+"]")
	licenseSays(t, "docs/COMPARISON.md", "| **License** | "+licenseName+" |")
	licenseSays(t, "internal/httpapi/openapi.yaml", "name: "+licenseName)
	licenseSays(t, "wails-app/wails.json", copyrightLine)
	licenseSays(t, "CONTRIBUTING.md", "## Contribution terms")
}
```

- [ ] **Step 4: Run it and watch it fail.** `go test ./cmd/monoagentcli/ -run TestLicenseMentionsAgree -count=1`. Expected: `LICENSE still holds the MIT text`, `README.md still names MIT as the license`, `wails-app/wails.json does not say "<the owner's COPYRIGHT_LINE>"`.
- [ ] **Step 5: Swap the file and edit the mentions.** `mv LICENSE.new LICENSE`, then write the owner's values where `<LICENSE_NAME>`, `<LICENSE_BADGE>`, `<COPYRIGHT_LINE>`, `<LICENSE_SUMMARY>` and `<CONTRIBUTION_TERMS>` stand. The OpenAPI document is 3.0.3, whose `license` object has `name` and `url` only; its `url` is Task 4's.

- `README.md` line 9: replace `[![License: MIT](https://img.shields.io/badge/License-MIT-purple?style=for-the-badge)](LICENSE)` with `[![License: <LICENSE_NAME>](https://img.shields.io/badge/License-<LICENSE_BADGE>-purple?style=for-the-badge)](LICENSE)`.
- `README.md` line 43: replace `> Mono Agent is an independent, unofficial, MIT-licensed project. It is not affiliated with` with `> Mono Agent is an independent, unofficial project, licensed under <LICENSE_NAME>. It is not affiliated with`.
- Replace `README.md` lines 809-815, which read:

```
Mono Agent is released under the [MIT License](LICENSE). In plain English:

- ✅ You may **use** it, commercially or personally
- ✅ You may **modify** it and build your own tools on it
- ✅ You may **distribute** copies and modified versions
- ❌ It comes with **no warranty** — the authors are not liable for anything it does or fails to do
- 📋 Keep the license and copyright notice with any copy you distribute
```

with:

```
Mono Agent is released under the [<LICENSE_NAME>](LICENSE). In plain English:

<LICENSE_SUMMARY>
```

- `README.md` line 821: replace `Independent, unofficial, MIT-licensed. Use at your own risk` with `Independent, unofficial, licensed under <LICENSE_NAME>. Use at your own risk`.
- `docs/COMPARISON.md` line 16: replace `| **License** | MIT | Sustainable Use` with `| **License** | <LICENSE_NAME> | Sustainable Use`.
- `docs/USAGE_POLICY.md` line 182: replace `Mono Agent is an independent, unofficial, MIT-licensed open-source project.` with `Mono Agent is an independent, unofficial project, licensed under <LICENSE_NAME>.`.
- Replace `internal/httpapi/openapi.yaml` lines 20-21, which read:

```
  license:
    name: MIT
```

with:

```
  license:
    name: <LICENSE_NAME>
```
- `wails-app/wails.json` line 17: replace `"copyright": "© 2026 MonoAgent"` with `"copyright": "<COPYRIGHT_LINE>"`.
- In `CONTRIBUTING.md`, before line 78 (it starts `## Security`), insert:

```
## Contribution terms

<CONTRIBUTION_TERMS>
```

- [ ] **Step 6: Run the tests.** `go test ./cmd/monoagentcli/ -run TestLicenseMentionsAgree -count=1` and `go test ./internal/httpapi/ -count=1` (expected: `ok` for both), `jq . wails-app/wails.json > /dev/null` (no output), then `go build ./...`.
- [ ] **Step 7: Commit.**
  - `git add LICENSE README.md docs/COMPARISON.md docs/USAGE_POLICY.md internal/httpapi/openapi.yaml wails-app/wails.json CONTRIBUTING.md cmd/monoagentcli/license_mentions_test.go`
  - `git commit -m "docs(license): relicense under <LICENSE_NAME>" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"`

### Task 4: L4 — a public releases-only repository (OWNER-GATED)

- [ ] **Step 1: Gate. Do not start until the owner has decided to close the source and says go.** This task changes how every installed client updates. The owner supplies `RELEASES_REPO` (`owner/name` of the public repository), `SOURCE_REPO` (the one that goes private), `RELEASES_BRANCH` (its default branch, `main`) and chooses:
  - **A. Repoint, wait, then close.** Ship a release whose code asks `RELEASES_REPO`, a new repository, and wait a period the owner picks (spec §4.7's key-rotation wait is eight weeks) before making the source private. An install older than that release keeps asking `github.com/monoes/mono-agent`, gets 404 after the close and can never update: its user reinstalls by hand. `SOURCE_REPO` stays `monoes/mono-agent`.
  - **B. Swap the names.** Rename the source repository (for instance to `monoes/mono-agent-src`) and create the public one as `monoes/mono-agent`. A renamed repository's old URLs redirect to it, and a new repository under the old name replaces that redirect (GitHub's page on renaming a repository warns of exactly this: "do not reuse the original name of the renamed repository. If you do, redirects to the renamed repository will no longer work"), so `api.github.com/repos/monoes/mono-agent/releases/latest`, the `install.sh` raw URL and `releases/download/…` all reach the public repository and installed clients keep updating. `RELEASES_REPO` stays `monoes/mono-agent`, `SOURCE_REPO` is the new name, and most edits below change nothing.
  - Prerequisite for either: Task 2 has landed, so `NOTICE` exists (the release workflow below copies it and fails without it).
  - Either way a private repository uses the plan's included Actions minutes, and `release.yml` runs macOS jobs; beyond them GitHub's published rates are Linux $0.006, Windows $0.010 and macOS $0.062 a minute (docs.github.com, "Actions minute multipliers", read 2026-10-05). Check the plan before closing.

**Files:**
- Create: `cmd/monoagentcli/release_repo_test.go`; in the releases repository: `README.md`, `.github/ISSUE_TEMPLATE/*` (copied)
- Modify: `cmd/monoagentcli/update.go` (243–245), `cmd/monoagentcli/crashreport.go` (50), `install.sh` (24), `.github/workflows/release.yml`, `SECURITY.md` (the advisory URL, "Verifying a release"), `README.md`, `SUPPORT.md`
- Test: `cmd/monoagentcli/release_repo_test.go`; `update_check_test.go`, `update_app_test.go`, `update_test.go` (unchanged, must stay green)

**Interfaces:** Consumes `latestReleaseURL` (update.go:245), read by `fetchLatestRelease` (the `update` command, and `doctor`'s `core.update` through `health.Env.LatestVersion`, doctor_env.go:40) and by `updateApp` (update_app.go:69, which hands it to `appupdate.Updater.APIURL`); `wails-app/updater.go` holds no URL (it runs `monoagentcli update --check`). Produces `const releasesRepo` in `update.go`.

- [ ] **Step 2: Write the failing test,** writing `SOURCE_REPO` into its constant. `cmd/monoagentcli/release_repo_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sourceRepo is the repository the code lives in once it is private (owner/name).
// No URL that a user's machine or browser uses to get, report on or verify a
// release may name it. The line below is the owner-supplied input of this file.
const sourceRepo = "<SOURCE_REPO>"

// releaseDoc reads a file of the repository whose release URLs this test checks.
func releaseDoc(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func TestReleaseURLsNameOnlyThePublicRepo(t *testing.T) {
	if releasesRepo == sourceRepo {
		t.Fatalf("releasesRepo and sourceRepo are both %q: the releases repository must be a different, public one", sourceRepo)
	}
	if want := "https://api.github.com/repos/" + releasesRepo + "/releases/latest"; latestReleaseURL != want {
		t.Errorf("latestReleaseURL = %q, want %q", latestReleaseURL, want)
	}
	for _, rel := range []string{
		"install.sh", "README.md", "SECURITY.md", "SUPPORT.md", ".github/workflows/release.yml",
		"cmd/monoagentcli/update.go", "cmd/monoagentcli/crashreport.go",
	} {
		text := releaseDoc(t, rel)
		for _, bad := range []string{
			"repos/" + sourceRepo, sourceRepo + "/releases", "githubusercontent.com/" + sourceRepo, "-R " + sourceRepo,
			`REPO="` + sourceRepo, sourceRepo + "/discussions", sourceRepo + "/issues", sourceRepo + "/security",
		} {
			if strings.Contains(text, bad) {
				t.Errorf("%s names the private source repository (%q): users cannot reach it", rel, bad)
			}
		}
	}
	if !strings.Contains(releaseDoc(t, "install.sh"), `REPO="`+releasesRepo+`"`) {
		t.Errorf("install.sh does not install from %s", releasesRepo)
	}
	if !strings.Contains(releaseDoc(t, ".github/workflows/release.yml"), "RELEASES_REPO: "+releasesRepo) {
		t.Errorf("release.yml does not publish to %s", releasesRepo)
	}
}
```

- [ ] **Step 3: Run it and watch it fail.** `go test ./cmd/monoagentcli/ -run TestReleaseURLsNameOnlyThePublicRepo -count=1`. Expected: a build failure, `undefined: releasesRepo`.
- [ ] **Step 4: The owner prepares the repositories** (outside this repository; `gh` is the GitHub CLI):
  1. Option B only: `gh release download <latest tag> -R monoes/mono-agent -D <scratchpad>/latest`, then `gh repo rename mono-agent-src -R monoes/mono-agent`.
  2. `gh repo create <RELEASES_REPO> --public --add-readme` (a repository needs a first commit before it can hold releases). Enable Issues, Discussions and private vulnerability reporting. Commit the README below and a copy of `.github/ISSUE_TEMPLATE/` with links changed to files that exist there (its README and SECURITY.md).
  3. Option B only, at once: `gh release create <latest tag> -R monoes/mono-agent <scratchpad>/latest/* --title <latest tag> --notes "Republished"`, so `releases/latest` answers again within minutes.
  4. A fine-grained token with `Contents: Read and write` on `RELEASES_REPO` only, stored as the secret `RELEASES_REPO_TOKEN` of the source repository (`gh secret set RELEASES_REPO_TOKEN -R <SOURCE_REPO>`, typed or piped, never on a command line); rotate it on a schedule the owner picks.
  5. Make the source private only after the last step of this task has shown a release in `RELEASES_REPO`.

````markdown
# MonoAgent releases

Binaries and checksums of MonoAgent (`monoagentcli` and the desktop app).

- Install (macOS, Linux): `curl -fsSL https://raw.githubusercontent.com/<RELEASES_REPO>/<RELEASES_BRANCH>/install.sh | bash`, then `monoagentcli account login`. Every download is verified against `SHA256SUMS.txt`.
- Update: `monoagentcli update`. Third-party licenses: `NOTICE`, attached to every release.
- Questions and bugs: this repository's Discussions and Issues. Security: SECURITY.md or security@monoes.me.
````

- [ ] **Step 5: Repoint the code.**

- Replace `cmd/monoagentcli/update.go` lines 243-245, which read:

```
// latestReleaseURL is GitHub's latest-release endpoint (a variable so tests
// can point it at a fake server).
var latestReleaseURL = "https://api.github.com/repos/monoes/mono-agent/releases/latest"
```

with:

```
// releasesRepo is the public repository the release assets are published to
// (owner/name); every release URL of the CLI is built from it.
const releasesRepo = "<RELEASES_REPO>"

// latestReleaseURL is GitHub's latest-release endpoint (a variable so tests
// can point it at a fake server).
var latestReleaseURL = "https://api.github.com/repos/" + releasesRepo + "/releases/latest"
```
- `cmd/monoagentcli/crashreport.go` line 50: replace `"report-crash", "--repo", "monoes/mono-agent", "--title"` with `"report-crash", "--repo", releasesRepo, "--title"`.
- `install.sh` line 24: replace `REPO="monoes/mono-agent"` with `REPO="<RELEASES_REPO>"`.

- [ ] **Step 6: Run the update tests.** `go test ./cmd/monoagentcli/ -run 'TestUpdate|TestLatestRelease|TestFetchLatest|TestAppUpdate' -count=1`. Expected: `ok` (they point `latestReleaseURL` at a fake server). `TestReleaseURLsNameOnlyThePublicRepo` fails until the next steps are done.
- [ ] **Step 7: Edit `.github/workflows/release.yml`.** The attestation step goes: GitHub provides artifact attestations for private repositories only on GitHub Enterprise Cloud (docs.github.com, "Using artifact attestations to establish provenance for builds"), so on any other plan `actions/attest-build-provenance` fails once the repository is private and would stop every release. On Enterprise Cloud, skip the edit that trims `permissions` and the one that deletes the attestation step; the attestation is then stored in the private repository, and only people who can read it can run `gh attestation verify -R <SOURCE_REPO>`.

- Replace `.github/workflows/release.yml` lines 388-396, which read:

```
    # id-token: write lets actions/attest-build-provenance mint a Sigstore
    # signature over an OIDC token scoped to this repo/commit/workflow run;
    # attestations: write publishes the resulting attestation to the repo
    # (MA-13) so `gh attestation verify` can tie a downloaded artifact back
    # to exactly this build, without needing a maintainer-held signing key.
    permissions:
      contents: write
      id-token: write
      attestations: write
```

with:

```
    # No build provenance attestation: GitHub offers them for private
    # repositories only on Enterprise Cloud. Users verify against SHA256SUMS.txt.
    permissions:
      contents: write
```

- Replace `.github/workflows/release.yml` lines 399-400, which read:

```
      PREV: ${{ needs.version.outputs.prev }}
    steps:
```

with:

```
      PREV: ${{ needs.version.outputs.prev }}
      # The public repository the release is published to (the build runs here).
      RELEASES_REPO: <RELEASES_REPO>
      RELEASES_BRANCH: <RELEASES_BRANCH>
    steps:
```

- Replace `.github/workflows/release.yml` lines 412-413, which read:

```
          find artifacts/ -type f | while read f; do cp "$f" "release-files/$(basename "$f")"; done
          cd release-files
```

with:

```
          find artifacts/ -type f | while read f; do cp "$f" "release-files/$(basename "$f")"; done
          cp NOTICE release-files/NOTICE   # third-party license texts travel with the binaries
          cd release-files
```

- Delete `.github/workflows/release.yml` lines 418-425, which read:

```
      # SLSA provenance attestation (MA-13): cryptographically ties every
      # release-files/ hash — including SHA256SUMS.txt itself — to this
      # exact repo, commit, and workflow run. Verify a downloaded artifact
      # with: gh attestation verify <file> -R monoes/mono-agent
      - name: Attest build provenance
        uses: actions/attest-build-provenance@4d101475d8b20a2381f78447822ac1eab6504dd8 # v4.2.2
        with:
          subject-path: "release-files/*"
```

- `.github/workflows/release.yml`: replace every occurrence (8) of `https://github.com/monoes/mono-agent/releases/download/${TAG}/` with `https://github.com/${RELEASES_REPO}/releases/download/${TAG}/`.
- Replace `.github/workflows/release.yml` lines 516-521, which read:

```
      - name: Create GitHub Release
        uses: softprops/action-gh-release@efb35369e0ad2afab669f228072c1b0d510eae64 # v3.0.3
        with:
          tag_name: ${{ env.TAG }}
          body: ${{ steps.notes.outputs.NOTES }}
          files: release-files/*
```

with:

```
      - name: Create the release in the public releases repository
        uses: softprops/action-gh-release@efb35369e0ad2afab669f228072c1b0d510eae64 # v3.0.3
        with:
          repository: ${{ env.RELEASES_REPO }}
          token: ${{ secrets.RELEASES_REPO_TOKEN }}
          target_commitish: ${{ env.RELEASES_BRANCH }}
          tag_name: ${{ env.TAG }}
          body: ${{ steps.notes.outputs.NOTES }}
          files: release-files/*

      # The README's one-liner serves install.sh from the releases repository.
      - name: Publish install.sh to the releases repository
        env:
          GH_TOKEN: ${{ secrets.RELEASES_REPO_TOKEN }}
        run: |
          sha="$(gh api "repos/${RELEASES_REPO}/contents/install.sh?ref=${RELEASES_BRANCH}" --jq .sha 2>/dev/null || true)"
          args=(-f message="install.sh from ${TAG}" -f branch="${RELEASES_BRANCH}" -f content="$(base64 < install.sh | tr -d '\n')")
          [ -n "$sha" ] && args+=(-f sha="$sha")
          gh api -X PUT "repos/${RELEASES_REPO}/contents/install.sh" "${args[@]}" > /dev/null
```

- [ ] **Step 8: Edit the documents that name the repository.**

- `SECURITY.md` line 11: replace `  https://github.com/monoes/mono-agent/security/advisories/new` with `  https://github.com/<RELEASES_REPO>/security/advisories/new`.
- Replace `SECURITY.md` lines 323-338, which read:

````
Every release publishes `SHA256SUMS.txt` alongside the binaries, and the
release workflow attaches a [SLSA build provenance
attestation](https://github.com/actions/attest-build-provenance) to every
file it produces (including the checksum file itself), signed via GitHub's
OIDC-backed Sigstore integration — no maintainer-held key involved. Verify a
downloaded artifact was actually built by this repo's release workflow from
the commit it claims:

```bash
gh attestation verify monoagentcli-darwin-arm64 -R monoes/mono-agent
```

This proves the artifact's hash matches what GitHub Actions produced for a
specific commit in this repository — it does **not** yet carry a personal
code-signing identity (see below), so on macOS/Windows you will still see an
unidentified-developer warning until that lands.
````

with:

````
Every release publishes `SHA256SUMS.txt` alongside the binaries, at
https://github.com/<RELEASES_REPO>/releases. `update`, `update --app` and
`install.sh` verify every download against it and install nothing on a
mismatch. To check a file by hand:

```bash
shasum -a 256 --check --ignore-missing SHA256SUMS.txt   # macOS; sha256sum on Linux
```

There is no build provenance attestation: releases are built in a private
repository, and GitHub provides attestations for private repositories only on
GitHub Enterprise Cloud. The binaries do not yet carry a personal code-signing
identity (see below), so on macOS/Windows you will still see an
unidentified-developer warning until that lands.
````
- Delete `README.md` line 10, which reads `[![CI](https://github.com/monoes/mono-agent/actions/workflows/ci.yml/badge.svg)](https://github.com/monoes/mono-agent/actions/workflows/ci.yml)`.
- `README.md` line 78: replace `https://raw.githubusercontent.com/monoes/mono-agent/master/install.sh` with `https://raw.githubusercontent.com/<RELEASES_REPO>/<RELEASES_BRANCH>/install.sh`.
- `README.md`: replace every occurrence (3) of `https://github.com/monoes/mono-agent/releases/latest` with `https://github.com/<RELEASES_REPO>/releases/latest`.
- `README.md` line 793: replace `https://github.com/monoes/mono-agent/discussions` with `https://github.com/<RELEASES_REPO>/discussions`.
- `SUPPORT.md` line 8: replace `https://github.com/monoes/mono-agent/discussions` with `https://github.com/<RELEASES_REPO>/discussions`.
- `SUPPORT.md` line 31: replace `https://github.com/monoes/mono-agent/issues` with `https://github.com/<RELEASES_REPO>/issues`.

  Left for the plan that closes the source, and not scanned by the test: the two `git clone https://github.com/monoes/mono-agent.git` blocks in the README (build-from-source instructions), its "Contributing" paragraph and `CONTRIBUTING.md` (pull requests), and `docker-compose.yml`'s `build: .` (Docker then builds only for someone who has the source: publish an image from `release.yml` in that plan, or say so in the README). `.github/ISSUE_TEMPLATE/*` stay here, unused, once copied to the releases repository.
- [ ] **Step 9: Run the checks.** `go build ./...`, `go vet ./...`, `gofmt -l .` (no output), `go test ./cmd/monoagentcli/ -run TestReleaseURLsNameOnlyThePublicRepo -count=1` (expected: `ok`), `sh -n install.sh` and `python3 -c "import yaml; yaml.safe_load(open('.github/workflows/release.yml'))"` (no output; skip if PyYAML is absent).
- [ ] **Step 10: Prove the workflow on a throwaway pair first.** Point `RELEASES_REPO` at a scratch public repository and run the release once from a scratch copy of the source repository with its own `RELEASES_REPO_TOKEN`: the publishing and `install.sh` steps are what only a real run exercises. Then restore the real values.
- [ ] **Step 11: Commit.**
  - `git add cmd/monoagentcli/release_repo_test.go cmd/monoagentcli/update.go cmd/monoagentcli/crashreport.go install.sh .github/workflows/release.yml SECURITY.md README.md SUPPORT.md`
  - `git commit -m "build(release): publish releases to the public releases repository" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"`
- [ ] **Step 12: The first release.** After the push: `gh release view <tag> -R <RELEASES_REPO> --json assets --jq '.assets[].name'` lists the binaries, `SHA256SUMS.txt` and `NOTICE`; `curl -fsSL https://api.github.com/repos/<RELEASES_REPO>/releases/latest | jq -r .tag_name` prints the tag; `curl -fsSI https://raw.githubusercontent.com/<RELEASES_REPO>/<RELEASES_BRANCH>/install.sh` answers 200; `monoagentcli update --check` from an installed client names the new tag. Only then make the source repository private.

## Contract change requests

None.
