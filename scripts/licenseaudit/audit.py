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
