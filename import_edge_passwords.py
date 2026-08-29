#!/usr/bin/env python3
import csv
import json
import os
import shutil
import subprocess
import sys

def find_cli_binary():
    """Find monoagentcli executable."""
    candidates = [
        shutil.which("monoagentcli"),
        os.path.abspath("./bin/monoagentcli"),
        os.path.abspath("./build/monoagentcli"),
        os.path.abspath("./monoagentcli"),
        "/usr/local/bin/monoagentcli",
    ]
    for candidate in candidates:
        if candidate and os.path.isfile(candidate) and os.access(candidate, os.X_OK):
            return candidate
    return "monoagentcli"

def ensure_profile(cli_path, profile_name="edge"):
    """Ensure the target profile exists."""
    try:
        res = subprocess.run([cli_path, "profile", "list", "--json"], capture_output=True, text=True)
        if profile_name not in res.stdout:
            print(f"[*] Creating profile '{profile_name}'...")
            subprocess.run([cli_path, "profile", "create", profile_name], check=True)
        else:
            print(f"[*] Profile '{profile_name}' already exists.")
    except Exception as e:
        print(f"[!] Note on profile check: {e}")

def get_existing_vault_entries(cli_path, profile_name="edge"):
    """Fetch existing vault items to avoid duplicate name errors and redundant imports."""
    existing_names = set()
    existing_logins = set() # (username, url)
    try:
        res = subprocess.run(
            [cli_path, "--profile", profile_name, "secret", "list", "--json"],
            capture_output=True,
            text=True
        )
        if res.returncode == 0 and res.stdout.strip():
            entries = json.loads(res.stdout)
            for e in entries:
                name = e.get("name")
                if name:
                    existing_names.add(name)
                u = e.get("username") or ""
                url = e.get("url") or ""
                if u or url:
                    existing_logins.add((u, url))
    except Exception as e:
        print(f"[!] Warning fetching existing secrets: {e}")
    return existing_names, existing_logins

def make_unique_name(base_name, username, existing_names):
    """Generate a unique name for the secret entry."""
    if username:
        candidate = f"{base_name} ({username})"
    else:
        candidate = base_name

    if candidate not in existing_names:
        existing_names.add(candidate)
        return candidate

    counter = 2
    while f"{candidate} #{counter}" in existing_names:
        counter += 1
    unique = f"{candidate} #{counter}"
    existing_names.add(unique)
    return unique

def import_passwords(csv_path, profile_name="edge"):
    expanded_path = os.path.expanduser(csv_path)
    if not os.path.exists(expanded_path):
        print(f"[!] Error: File '{csv_path}' not found.")
        print(f"    1. Export your passwords from Microsoft Edge:")
        print(f"       Go to edge://passwords -> click '...' -> select 'Export passwords'")
        print(f"    2. Save it or pass the path to this script.")
        sys.exit(1)

    cli_path = find_cli_binary()
    print(f"[*] Using CLI: {cli_path}")
    ensure_profile(cli_path, profile_name)

    existing_names, existing_logins = get_existing_vault_entries(cli_path, profile_name)
    print(f"[*] Found {len(existing_names)} existing entries in profile '{profile_name}'.")

    success = 0
    skipped_empty = 0
    already_present = 0
    failed = 0

    with open(expanded_path, mode="r", encoding="utf-8-sig") as f:
        reader = csv.DictReader(f)
        for i, row in enumerate(reader, 1):
            base_name = (row.get("name") or row.get("title") or row.get("url") or f"login-{i}").strip()
            url = (row.get("url") or "").strip()
            username = (row.get("username") or row.get("user") or "").strip()
            password = (row.get("password") or "").strip()
            notes = (row.get("note") or row.get("notes") or "").strip()

            if not password:
                skipped_empty += 1
                continue

            # Check if this exact login already exists in vault
            if (username, url) in existing_logins or (base_name in existing_names and not username):
                already_present += 1
                continue

            unique_name = make_unique_name(base_name, username, existing_names)

            cmd = [
                cli_path,
                "--profile", profile_name,
                "secret", "add",
                "--kind", "login",
                "--name", unique_name,
                "--username", username,
                "--url", url,
                "--value", password,
            ]
            if notes:
                cmd.extend(["--notes", notes])

            try:
                result = subprocess.run(cmd, capture_output=True, text=True)
                if result.returncode == 0:
                    print(f"  [+] Added: {unique_name}")
                    existing_logins.add((username, url))
                    success += 1
                else:
                    print(f"  [-] Failed '{unique_name}': {result.stderr.strip() or result.stdout.strip()}")
                    failed += 1
            except Exception as ex:
                print(f"  [-] Error adding '{unique_name}': {ex}")
                failed += 1

    print("\n--- Import Summary ---")
    print(f"Successfully added: {success}")
    if already_present:
        print(f"Already in vault: {already_present}")
    if skipped_empty:
        print(f"Skipped (no password): {skipped_empty}")
    if failed:
        print(f"Failed: {failed}")
    print(f"\nTip: Don't forget to delete your plain CSV file once finished:")
    print(f"  rm \"{expanded_path}\"")

if __name__ == "__main__":
    csv_file = sys.argv[1] if len(sys.argv) > 1 else "edge_passwords.csv"
    import_passwords(csv_file)
