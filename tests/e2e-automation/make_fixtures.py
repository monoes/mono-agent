#!/usr/bin/env python3
"""Write the packages and files the e2e scripts install and verify.

Usage: make_fixtures.py <out-dir> <site-origin>   (e.g. http://crm.e2e.test:18765)

Everything is generated, so the scripts carry no machine-specific files:
  e2e-offdom/         navigate + http_fetch_in_page, domains [crm.e2e.test]
  e2e-upload/         one upload step, to test upload confinement
  e2e-vis/, vis-bad/  a valid and an invalid `visibility` value
  zip-*.mpkg          archives with ../, absolute and symlink entries
  login-draft/        a draft (package layout) with a {{secret:password}} input
  pw-right.json, pw-wrong.json   0600 inputs files for record verify
  wf-import.json      a workflow with a fixed id (import checks)
  e2e-offdom-differs/ same id and version as e2e-offdom, other content
  legacy-actions/     legacy ~/.monoagent/actions/<p>/ files (crm over http, localhost)
"""

import json
import os
import stat
import sys
import zipfile

out, site = sys.argv[1], sys.argv[2].rstrip("/")
host = site.split("://", 1)[1].split(":", 1)[0]


def write(path, data):
    path = os.path.join(out, path)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as f:
        if isinstance(data, str):
            f.write(data)
        else:
            json.dump(data, f, indent=2)


def manifest(pid, actions, steps, start="/contacts"):
    return {
        "schema": "monoagent.automation/v1", "id": pid, "name": pid, "version": "0.1.0",
        "description": "e2e fixture package", "engine": ">=0.0.0",
        "site": {"startUrl": site + start, "domains": [host]},
        "permissions": {"steps": steps, "scripts": [], "downloads": False},
        "actions": actions, "policy": {"tier": "standard"},
    }


def action(pid, name, inputs, outputs, steps, effects="read", **extra):
    a = {"actionType": name, "automation": pid, "version": "1.0.0", "description": name,
         "sideEffects": effects, "inputs": {"required": inputs}, "outputs": {"success": outputs},
         "steps": steps}
    a.update(extra)
    return a


url_in = [{"name": "url", "type": "string", "description": "url"}]

# e2e-offdom: runtime domain enforcement
write("e2e-offdom/automation.json", manifest("e2e-offdom", ["go", "fetch"], ["extract_text", "http_fetch_in_page", "navigate"]))
write("e2e-offdom/actions/go.json", action("e2e-offdom", "go", url_in, ["title"], [
    {"id": "open", "type": "navigate", "url": "{{url}}"},
    {"id": "t", "type": "extract_text", "selector": "h1", "variable_name": "title"}]))
write("e2e-offdom/actions/fetch.json", action("e2e-offdom", "fetch", url_in, ["resp"], [
    {"id": "open", "type": "navigate", "url": site + "/contacts"},
    {"id": "f", "type": "http_fetch_in_page", "url": "{{url}}", "method": "GET", "variable_name": "resp"}]))

# e2e-upload: upload confinement (only ~/.monoagent/uploads/e2e-upload/)
write("e2e-upload/automation.json", manifest("e2e-upload", ["put"], ["extract_text", "navigate", "upload"], "/upload"))
write("e2e-upload/selectors.json", {
    "upload.input": {"intent": "file input", "candidates": [{"css": "#file-input", "score": 0.9}]},
    "upload.chosen": {"intent": "chosen file", "candidates": [{"css": "#chosen", "score": 0.9}]}})
write("e2e-upload/actions/put.json", action("e2e-upload", "put", [{"name": "file", "type": "string", "description": "file"}], ["chosen"], [
    {"id": "open", "type": "navigate", "url": site + "/upload"},
    {"id": "up", "type": "upload", "configKey": "upload.input", "intent": "file input", "value": "{{file}}"},
    {"id": "chosen", "type": "extract_text", "configKey": "upload.chosen", "intent": "chosen file", "variable_name": "chosen"}]))

# visibility: one valid, one invalid package
for pid, vis in (("e2e-vis", ["profile_view_visible_to_owner", "search_may_be_saved"]), ("vis-bad", ["totally_invisible"])):
    write(f"{pid}/automation.json", manifest(pid, ["go"], ["extract_text", "navigate"]))
    write(f"{pid}/actions/go.json", action(pid, "go", url_in, ["title"], [
        {"id": "open", "type": "navigate", "url": "{{url}}"},
        {"id": "t", "type": "extract_text", "selector": "h1", "variable_name": "title"}], visibility=vis))


# unsafe archives: a valid package plus one bad entry each
def pkg_files(root):
    for dp, _, fs in os.walk(os.path.join(out, root)):
        for f in fs:
            p = os.path.join(dp, f)
            yield p, os.path.relpath(p, os.path.join(out, root))


for name, entry, link in (("zip-dotdot", "../../../zipslip-pwned.txt", False),
                          ("zip-abs", "/tmp/zipslip-abs-pwned.txt", False),
                          ("zip-nested", "actions/../../zipslip-nested.txt", False),
                          ("zip-symlink", "scripts/link.js", True)):
    with zipfile.ZipFile(os.path.join(out, name + ".mpkg"), "w") as z:
        for p, rel in pkg_files("e2e-offdom"):
            z.write(p, rel)
        if link:
            zi = zipfile.ZipInfo(entry)
            zi.create_system = 3
            zi.external_attr = (stat.S_IFLNK | 0o777) << 16
            z.writestr(zi, "/etc/passwd")
        else:
            z.writestr(entry, "pwned")

# login draft: a secret input typed into the password field
login = manifest("e2e-crm", ["sign_in"], ["click", "extract_text", "navigate", "type"], "/login")
write("login-draft/automation.json", login)
write("login-draft/selectors.json", {
    "login.username": {"intent": "Username field", "candidates": [{"css": "#username", "score": 0.9}]},
    "login.password": {"intent": "Password field", "candidates": [{"css": "#password", "score": 0.9}]},
    "login.submit": {"intent": "Sign in button", "candidates": [{"css": "#login-btn", "score": 0.9}]},
    "page.heading": {"intent": "page heading", "candidates": [{"css": "h1", "score": 0.5}]}})
write("login-draft/actions/sign_in.json", action("e2e-crm", "sign_in", [
    {"name": "username", "type": "string", "description": "user"},
    {"name": "password", "type": "secret", "description": "password"}], ["heading"], [
    {"id": "open", "type": "navigate", "url": site + "/login"},
    {"id": "user", "type": "type", "configKey": "login.username", "intent": "Username", "value": "{{username}}"},
    {"id": "pass", "type": "type", "configKey": "login.password", "intent": "Password", "value": "{{secret:password}}"},
    {"id": "go", "type": "click", "configKey": "login.submit", "intent": "Sign in", "until": {"urlMatches": "/contacts/new"}},
    {"id": "heading", "type": "extract_text", "configKey": "page.heading", "intent": "heading", "variable_name": "heading"}]))

password = os.environ.get("E2E_PASSWORD", "S3cr3t-Pa55word!")
for name, pw in (("pw-right.json", password), ("pw-wrong.json", "wrong-password")):
    path = os.path.join(out, name)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as f:
        json.dump({"username": "alice", "password": pw}, f)
    os.chmod(path, 0o600)

# a workflow with a fixed id, for the import checks (node type from e2e-offdom)
write("wf-import.json", {
    "id": "e2e-import-fixed-id", "name": "E2E import", "description": "v1", "version": 1, "is_active": False,
    "nodes": [
        {"id": "trigger", "type": "trigger.manual", "name": "Run", "position": {"x": 0, "y": 0}, "disabled": False, "config": {}},
        {"id": "go", "type": "e2e-offdom.go", "name": "Go", "position": {"x": 300, "y": 0}, "disabled": False,
         "config": {"url": site + "/contacts"}}],
    "connections": [{"id": "t-g", "source": "trigger", "source_handle": "main", "target": "go", "target_handle": "main"}]})

# same id and version as e2e-offdom, different content (a bundle "differs")
for rel in ("automation.json", "actions/go.json", "actions/fetch.json"):
    with open(os.path.join(out, "e2e-offdom", rel)) as f:
        data = json.load(f)
    if rel == "automation.json":
        data["description"] = "e2e fixture package, edited locally"
    write(os.path.join("e2e-offdom-differs", rel), data)

# legacy actions (~/.monoagent/actions/<platform>/): one on the fixture site
# over http, one on a local address (never bundled)
port = site.rsplit(":", 1)[1] if site.count(":") > 1 else "80"
legacy_steps = lambda base: [
    {"id": "open", "type": "navigate", "url": base + "/contacts/new"},
    {"id": "name", "type": "type", "selector": "#contact-name", "value": "{{name}}"},
    {"id": "email", "type": "type", "selector": "#contact-email", "value": "{{email}}"},
    {"id": "save", "type": "click", "selector": "[data-testid=save-contact]"},
    {"id": "t", "type": "extract_text", "selector": "h1", "variable_name": "title"}]
for plat, base in (("crmlegacy", site), ("locallegacy", f"http://localhost:{port}")):
    write(f"legacy-actions/{plat}/add_contact.json", {
        "actionType": "add_contact", "platform": plat, "description": "legacy: add a contact",
        "inputs": {"required": [{"name": "email", "type": "string"}], "optional": [{"name": "name", "type": "string"}]},
        "outputs": {"success": ["title"]}, "steps": legacy_steps(base)})
