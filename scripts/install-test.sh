#!/usr/bin/env bash
# install-test.sh — exercises install.sh against a local python3 http.server
# serving a fake manifest and fake binary. Installs only into a throwaway
# INSTALL_DIR; never touches the real install dir, ~/.monoagent or PATH, and
# makes no network calls (the legacy GitHub path is not exercised).
#
# Run:  bash scripts/install-test.sh
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
installer="$root/install.sh"

for t in python3 openssl curl tar; do
  command -v "$t" >/dev/null 2>&1 || { echo "install-test: SKIP ($t not available)"; exit 0; }
done

case "$(uname -s)" in Darwin) os=darwin ;; Linux) os=linux ;; *) echo "install-test: SKIP (unsupported OS)"; exit 0 ;; esac
case "$(uname -m)" in arm64|aarch64) arch=arm64 ;; *) arch=amd64 ;; esac

work="$(mktemp -d "${TMPDIR:-$HOME/scratch}/install-test.XXXXXX" 2>/dev/null || mktemp -d)"
srv_pid=""
cleanup() { [ -n "$srv_pid" ] && kill "$srv_pid" 2>/dev/null || true; rm -rf "$work"; }
trap cleanup EXIT

web="$work/web"; mkdir -p "$web"
sha() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'; else shasum -a 256 "$1" | awk '{print $1}'; fi; }

port="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])')"
(cd "$web" && exec python3 -m http.server "$port" --bind 127.0.0.1 >/dev/null 2>&1) &
srv_pid=$!
base="http://127.0.0.1:$port"
for _ in $(seq 1 50); do curl -fs -o /dev/null "$base/" && break; sleep 0.1; done

# Fake release: a raw binary and a tarball holding one.
asset="monoagentcli-$os-$arch"
printf '#!/bin/sh\necho fake-monoagentcli\n' > "$web/$asset"
mkdir -p "$work/pkg" && cp "$web/$asset" "$work/pkg/monoagentcli"
tar -czf "$web/$asset.tar.gz" -C "$work/pkg" monoagentcli

write_manifest() { # <asset-name> <sha>
  cat > "$web/manifest.json" <<EOF
{"schema":1,"version":"v9.9.9","released_at":"2026-10-08T00:00:00Z","notes_url":"$base/notes",
 "assets":[
  {"os":"plan9","arch":"amd64","name":"other","url":"$base/other","sha256":"00","size":1,"kind":"cli"},
  {"os":"$os","arch":"$arch","name":"app-$asset","url":"$base/nope","sha256":"00","size":1,"kind":"app"},
  {"os":"$os","arch":"$arch","name":"$1","url":"$base/$1","sha256":"$2","size":10,"kind":"cli"}
 ]}
EOF
}

# Test key pair, generated here; never committed.
openssl genpkey -algorithm ed25519 -out "$work/key.pem" 2>/dev/null
pub="$(openssl pkey -in "$work/key.pem" -pubout -outform DER 2>/dev/null | tail -c 32 | openssl base64 -A)"
keyid="$(openssl pkey -in "$work/key.pem" -pubout -outform DER 2>/dev/null | tail -c 32 | openssl dgst -sha256 -binary | od -An -tx1 | tr -d ' \n' | cut -c1-16)"
sign() {
  openssl pkeyutl -sign -inkey "$work/key.pem" -rawin -in "$web/manifest.json" -out "$work/sig.bin" 2>/dev/null
  printf '%s %s\n' "$keyid" "$(openssl base64 -A < "$work/sig.bin")" > "$web/manifest.json.sig"
}

fails=0
run() { # <name> <expect: ok|fail> <grep-pattern> [env...]; extra args are env assignments
  name="$1"; want="$2"; pat="$3"; shift 3
  dest="$work/prefix-$name"
  rc=0
  out="$(env -u MONOAGENT_RELEASE_PUBKEY INSTALL_DIR="$dest" MONOAGENT_TEST_ASSET_URL_PREFIX="$base/" MONOAGENT_MANIFEST_URL="$base/manifest.json" "$@" bash "$installer" 2>&1)" || rc=$?
  ok=1
  if [ "$want" = ok ]; then [ "$rc" -eq 0 ] && [ -x "$dest/monoagentcli" ] || ok=0
  else [ "$rc" -ne 0 ] && [ ! -e "$dest/monoagentcli" ] || ok=0; fi
  printf '%s\n' "$out" | grep -q -e "$pat" || ok=0
  if [ "$ok" -eq 1 ]; then echo "PASS $name"; else echo "FAIL $name (rc=$rc)"; printf '%s\n' "$out" | sed 's/^/    /'; fails=$((fails+1)); fi
}

# 1. checksum-only: no key configured, says so and does not claim a signature.
write_manifest "$asset" "$(sha "$web/$asset")"; sign
run unsigned-no-key ok "signature verification is SKIPPED"
out="$(INSTALL_DIR="$work/p-neg" MONOAGENT_TEST_ASSET_URL_PREFIX="$base/" MONOAGENT_MANIFEST_URL="$base/manifest.json" bash "$installer" 2>&1)"
if printf '%s' "$out" | grep -q "signature verified"; then echo "FAIL claimed verification without a key"; fails=$((fails+1)); else echo "PASS no false claim"; fi

# 2. tarball asset.
write_manifest "$asset.tar.gz" "$(sha "$web/$asset.tar.gz")"; sign
run tarball ok "Checksum verified"

# 3. signed with the pinned key.
write_manifest "$asset" "$(sha "$web/$asset")"; sign
run signed ok "Manifest signature verified" MONOAGENT_RELEASE_PUBKEY="$pub"

# 4. wrong sha256 in a validly signed manifest.
write_manifest "$asset" "$(printf 'x%.0s' $(seq 1 64))"; sign
run bad-sha fail "checksum mismatch" MONOAGENT_RELEASE_PUBKEY="$pub"

# 5. manifest altered after signing.
write_manifest "$asset" "$(sha "$web/$asset")"; sign
sed 's/v9.9.9/v6.6.6/' "$web/manifest.json" > "$work/m2" && cp "$work/m2" "$web/manifest.json"
run tampered fail "signature verification FAILED" MONOAGENT_RELEASE_PUBKEY="$pub"

# 6. key configured but signature missing.
write_manifest "$asset" "$(sha "$web/$asset")"; rm -f "$web/manifest.json.sig"
run missing-sig fail "signature verification FAILED" MONOAGENT_RELEASE_PUBKEY="$pub"

# 7. signed by a different key.
sign_other() { openssl genpkey -algorithm ed25519 -out "$work/o.pem" 2>/dev/null
  openssl pkeyutl -sign -inkey "$work/o.pem" -rawin -in "$web/manifest.json" -out "$work/o.bin" 2>/dev/null
  printf '%s %s\n' "$keyid" "$(openssl base64 -A < "$work/o.bin")" > "$web/manifest.json.sig"; }
sign_other
run wrong-key fail "signature verification FAILED" MONOAGENT_RELEASE_PUBKEY="$pub"

# 7b. signature carries a key id other than the pinned one.
write_manifest "$asset" "$(sha "$web/$asset")"; sign
sed 's/^[0-9a-f]*/0000000000000000/' "$web/manifest.json.sig" > "$work/s2" && cp "$work/s2" "$web/manifest.json.sig"
run wrong-keyid fail "not the pinned key" MONOAGENT_RELEASE_PUBKEY="$pub"

# 7c. malformed one-field signature file.
sign; awk '{print $2}' "$web/manifest.json.sig" > "$work/s3" && cp "$work/s3" "$web/manifest.json.sig"
run one-field-sig fail "malformed manifest.json.sig" MONOAGENT_RELEASE_PUBKEY="$pub"

# 7d. asset url on a disallowed host; the test prefix override is emptied so
# the real allow-list applies.
cat > "$web/manifest.json" <<EOF
{"schema":1,"version":"v9.9.9","assets":[{"os":"$os","arch":"$arch","name":"$asset","url":"https://evil.example.com/$asset","sha256":"00","size":1,"kind":"cli"}]}
EOF
run bad-host fail "not https on an allowed host" MONOAGENT_TEST_ASSET_URL_PREFIX=""

# 7e. validly signed but expired manifest.
write_manifest "$asset" "$(sha "$web/$asset")"
sed 's/"schema":1,/"schema":1,"expires_at":"2020-01-01T00:00:00Z",/' "$web/manifest.json" > "$work/m3" && cp "$work/m3" "$web/manifest.json"; sign
run expired fail "expired" MONOAGENT_RELEASE_PUBKEY="$pub"

# 8. no asset for this platform.
printf '{"schema":1,"version":"v9.9.9","assets":[]}' > "$web/manifest.json"
run no-asset fail "no CLI asset"

if [ "$fails" -ne 0 ]; then echo "install-test: $fails failure(s)"; exit 1; fi
echo "install-test: all passed"
