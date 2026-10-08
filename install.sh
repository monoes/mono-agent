#!/usr/bin/env bash
# install.sh — install the mono-agent CLI (monoagentcli).
#
# Default path (no GitHub API needed):
#   1. fetch manifest.json from the manifest URL
#   2. choose the "cli" asset for this host's os/arch
#   3. download it from the asset's url
#   4. verify its sha256 against the manifest
#   5. verify the manifest's Ed25519 signature (manifest.json.sig) when
#      `openssl` with Ed25519 support is present AND a public key is
#      configured (RELEASE_PUBKEY below). While no key is configured the
#      install is checksum-only and says so; it never claims a signature
#      was verified when it was not.
#
# Legacy path (kept simple, labelled in the output): the GitHub releases API
# plus SHA256SUMS.txt. Used only when MONOAGENT_ALLOW_LEGACY_GITHUB=1, or
# when the manifest URL cannot be reached. A manifest that is reachable but
# fails verification is a hard error, never a fallback.
#
# Supported platforms: darwin/{arm64,amd64}, linux/{amd64,arm64}.
# Windows: download the .exe from the releases page.
#
# Flags:
#   --no-verify   skip signature and checksum verification (not recommended)
#
# Environment:
#   INSTALL_DIR                      install location (default /usr/local/bin)
#   MONOAGENT_MANIFEST_URL           manifest URL (tests, mirrors)
#   MONOAGENT_RELEASE_PUBKEY         base64 raw Ed25519 public key; overrides RELEASE_PUBKEY
#   MONOAGENT_ALLOW_LEGACY_GITHUB=1  use the GitHub API path directly
set -euo pipefail

# Repository used by the legacy path.
REPO="monoes/mono-agent"
BIN="monoagentcli"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
MANIFEST_URL="${MONOAGENT_MANIFEST_URL:-https://monoes.me/mono-agent/releases/latest/manifest.json}"
# Pinned manifest-signing public key: base64 of the raw 32-byte Ed25519 key,
# as printed by `monoagentcli release keygen`. Empty until the owner generates
# the key; empty means checksum-only installs.
RELEASE_PUBKEY=""
RELEASE_PUBKEY="${MONOAGENT_RELEASE_PUBKEY:-$RELEASE_PUBKEY}"

err() { printf 'install.sh: error: %s\n' "$*" >&2; exit 1; }

NO_VERIFY=0
for arg in "$@"; do
  case "$arg" in
    --no-verify) NO_VERIFY=1 ;;
    *)            err "unknown option '$arg' (supported: --no-verify)" ;;
  esac
done

command -v curl >/dev/null 2>&1 || err "curl is required but not installed"

# sha256_of <file> — print the hex sha256 of a file.
sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    err "neither sha256sum nor shasum is available to verify the download"
  fi
}

# verify_checksum <dir> <asset> — legacy path: check <dir>/<asset> against the
# <asset> line in <dir>/SHA256SUMS.txt ("<sha256>  <filename>"). Prints the
# verified digest; returns non-zero (message on stderr) on failure.
verify_checksum() {
  v_dir="$1"
  v_asset="$2"
  v_line="$(grep "[[:space:]]${v_asset}\$" "${v_dir}/SHA256SUMS.txt" | head -n 1 || true)"
  if [ -z "$v_line" ]; then
    printf 'install.sh: error: no checksum entry for %s in SHA256SUMS.txt\n' "$v_asset" >&2
    return 1
  fi
  v_want="$(printf '%s' "$v_line" | awk '{print $1}')"
  v_got="$(sha256_of "${v_dir}/${v_asset}")"
  if [ "$v_got" != "$v_want" ]; then
    printf 'install.sh: error: checksum mismatch for %s (expected %s)\n' "$v_asset" "$v_want" >&2
    return 1
  fi
  printf '%s\n' "$v_want"
}

# json_field <object> <key> — value of a flat string/number field.
json_field() {
  printf '%s' "$1" | sed -n 's/.*"'"$2"'" *: *"\{0,1\}\([^",}]*\)"\{0,1\}.*/\1/p' | head -n 1
}

# asset_url_allowed <url> — the asset must come over https from monoes.me,
# github.com or GitHub's release-asset hosts (same allow-list as the Go
# client). MONOAGENT_TEST_ASSET_URL_PREFIX (tests only) additionally allows
# URLs that start with that exact prefix.
asset_url_allowed() {
  if [ -n "${MONOAGENT_TEST_ASSET_URL_PREFIX:-}" ]; then
    case "$1" in "${MONOAGENT_TEST_ASSET_URL_PREFIX}"*) return 0 ;; esac
  fi
  case "$1" in https://*) ;; *) return 1 ;; esac
  u_host="${1#https://}"
  u_host="${u_host%%[/?#]*}"
  case "$u_host" in
    monoes.me|github.com|objects.githubusercontent.com|release-assets.githubusercontent.com) return 0 ;;
    *) return 1 ;;
  esac
}

# verify_manifest_signature <manifest> <sig-url> <tmp> — returns 0 verified,
# 2 skipped (no key / no openssl Ed25519; message printed), 1 failed.
verify_manifest_signature() {
  m_file="$1"; m_sigurl="$2"; m_tmp="$3"
  if [ -z "$RELEASE_PUBKEY" ]; then
    echo "NOTE: no release signing key is configured in this installer; manifest signature verification is SKIPPED (checksum-only)." >&2
    return 2
  fi
  if ! command -v openssl >/dev/null 2>&1; then
    echo "NOTE: openssl not found; manifest signature verification is SKIPPED (checksum-only)." >&2
    return 2
  fi
  {
    printf -- '-----BEGIN PUBLIC KEY-----\n'
    printf 'MCowBQYDK2VwAyEA%s\n' "$RELEASE_PUBKEY"
    printf -- '-----END PUBLIC KEY-----\n'
  } > "${m_tmp}/pub.pem"
  if ! openssl pkey -pubin -in "${m_tmp}/pub.pem" -noout >/dev/null 2>&1; then
    echo "NOTE: this openssl cannot read Ed25519 keys; manifest signature verification is SKIPPED (checksum-only)." >&2
    return 2
  fi
  curl --retry 3 --fail --location -fsSL -o "${m_tmp}/manifest.json.sig" "$m_sigurl" || return 1
  # Contract: ONE line "<keyid> <base64sig>". keyid is the hex of the first 8
  # bytes of SHA-256 of the raw public key. Anything else is malformed.
  m_nfields="$(awk 'NR==1{print NF}' "${m_tmp}/manifest.json.sig")"
  if [ "$m_nfields" != "2" ] || [ "$(wc -l < "${m_tmp}/manifest.json.sig" | tr -d ' ')" -gt 1 ]; then
    echo "install.sh: malformed manifest.json.sig (expected '<keyid> <base64sig>')" >&2
    return 1
  fi
  m_keyid="$(printf '%s' "$RELEASE_PUBKEY" | openssl base64 -d -A 2>/dev/null \
    | openssl dgst -sha256 -binary | od -An -tx1 | tr -d ' \n' | cut -c1-16)"
  m_sigkeyid="$(awk 'NR==1{print $1}' "${m_tmp}/manifest.json.sig")"
  if [ -z "$m_keyid" ] || [ "$m_sigkeyid" != "$m_keyid" ]; then
    echo "install.sh: manifest signed with key id '${m_sigkeyid}', not the pinned key '${m_keyid}'" >&2
    return 1
  fi
  awk 'NR==1{print $2}' "${m_tmp}/manifest.json.sig" | openssl base64 -d -A > "${m_tmp}/sig.bin" 2>/dev/null || return 1
  openssl pkeyutl -verify -pubin -inkey "${m_tmp}/pub.pem" -rawin \
    -in "$m_file" -sigfile "${m_tmp}/sig.bin" >/dev/null 2>&1 || return 1
  return 0
}

# ── Detect platform ──────────────────────────────────────────────────────────
OS="$(uname -s)"
ARCH="$(uname -m)"

case "$OS" in
  Darwin) os="darwin" ;;
  Linux)  os="linux" ;;
  *)      err "unsupported OS '$OS' (supported: darwin, linux). Windows: download monoagentcli-windows-amd64.exe from the releases page." ;;
esac

case "$ARCH" in
  arm64|aarch64) arch="arm64" ;;
  amd64|x86_64)  arch="amd64" ;;
  *)             err "unsupported architecture '$ARCH' on $os" ;;
esac

ASSET="monoagentcli-${os}-${arch}"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT
SRC=""   # the downloaded file to install, set by either path
TAG=""

# ── Manifest path ────────────────────────────────────────────────────────────
# Returns 10 when the manifest cannot be fetched (caller may fall back);
# any other failure exits.
install_from_manifest() {
  echo "Fetching release manifest: ${MANIFEST_URL}"
  curl --retry 3 --fail --location -fsSL -o "${TMP_DIR}/manifest.json" "$MANIFEST_URL" || return 10

  if [ "$NO_VERIFY" -eq 1 ]; then
    echo "WARNING: --no-verify given; skipping signature and checksum verification." >&2
  else
    sig_status=0
    verify_manifest_signature "${TMP_DIR}/manifest.json" "${MANIFEST_URL}.sig" "$TMP_DIR" || sig_status=$?
    case "$sig_status" in
      0) echo "Manifest signature verified (Ed25519)." ;;
      2) ;;
      *) err "manifest signature verification FAILED; the manifest is not signed by the pinned release key. Nothing was installed." ;;
    esac
  fi

  flat="$(tr '\n\r\t' '   ' < "${TMP_DIR}/manifest.json")"
  TAG="$(json_field "$flat" version)"
  # expires_at (optional, covered by the signature): RFC 3339 UTC as written by
  # cmd/release-manifest. Same UTC layout, so a string compare orders it. Any
  # other layout is refused rather than guessed at.
  if [ "$NO_VERIFY" -ne 1 ]; then
    m_exp="$(json_field "$flat" expires_at)"
    if [ -n "$m_exp" ]; then
      printf '%s' "$m_exp" | grep -Eq '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$' \
        || err "the manifest expires_at '${m_exp}' is not a UTC RFC 3339 time. Nothing was installed."
      m_now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
      [ "$m_exp" \> "$m_now" ] \
        || err "the manifest expired at ${m_exp}; ask the maintainers for a fresh release. Nothing was installed."
    fi
  fi
  # Asset objects hold no nested braces, so each is a {...} with no inner { or }.
  entry=""
  while IFS= read -r obj; do
    if [ "$(json_field "$obj" os)" = "$os" ] && [ "$(json_field "$obj" arch)" = "$arch" ] \
       && [ "$(json_field "$obj" kind)" = "cli" ]; then
      entry="$obj"; break
    fi
  done < <(printf '%s' "$flat" | grep -o '{[^{}]*}' || true)
  [ -n "$entry" ] || err "the manifest has no CLI asset for ${os}/${arch}"
  a_name="$(json_field "$entry" name)"
  a_url="$(json_field "$entry" url)"
  a_sha="$(json_field "$entry" sha256)"
  [ -n "$a_name" ] && [ -n "$a_url" ] || err "the manifest entry for ${os}/${arch} is incomplete"
  asset_url_allowed "$a_url" \
    || err "the manifest asset url '${a_url}' is not https on an allowed host (monoes.me, github.com, GitHub release assets). Nothing was installed."
  case "$a_name" in */*|..*) err "the manifest asset name '${a_name}' is not a plain file name" ;; esac

  echo "Downloading ${a_name} (${TAG:-unknown version})..."
  curl --retry 3 --fail --location --progress-bar -o "${TMP_DIR}/${a_name}" "$a_url" \
    || err "download failed: $a_url"

  if [ "$NO_VERIFY" -ne 1 ]; then
    [ -n "$a_sha" ] || err "the manifest gives no sha256 for ${a_name}"
    got="$(sha256_of "${TMP_DIR}/${a_name}")"
    [ "$got" = "$a_sha" ] \
      || err "checksum mismatch for ${a_name} (manifest ${a_sha}, downloaded ${got}). Nothing was installed."
    echo "Checksum verified: ${a_name} SHA256 ${got}"
  fi

  case "$a_name" in
    *.tar.gz|*.tgz)
      command -v tar >/dev/null 2>&1 || err "tar is required to unpack ${a_name}"
      mkdir -p "${TMP_DIR}/x"
      tar -xzf "${TMP_DIR}/${a_name}" -C "${TMP_DIR}/x" || err "could not unpack ${a_name}"
      SRC="$(find "${TMP_DIR}/x" -type f -name "$BIN" | head -n 1)"
      [ -n "$SRC" ] || err "${a_name} does not contain ${BIN}"
      ;;
    *) SRC="${TMP_DIR}/${a_name}" ;;
  esac
}

# ── Legacy path: GitHub API + SHA256SUMS.txt ─────────────────────────────────
install_from_github_legacy() {
  echo "Using the legacy GitHub release path for ${REPO} (no signed manifest)."
  RELEASE_JSON="$(curl --retry 3 --fail --location -fsSL "https://api.github.com/repos/${REPO}/releases/latest")" \
    || err "could not reach the GitHub API (network down, rate-limited, or the repository is not public)"
  TAG="$(printf '%s' "$RELEASE_JSON" | grep -o '"tag_name": *"[^"]*"' | head -1 | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')"
  [ -n "$TAG" ] || err "could not parse the latest tag from the GitHub API response"
  URL="https://github.com/${REPO}/releases/download/${TAG}/${ASSET}"

  echo "Downloading ${ASSET} (${TAG})..."
  curl --retry 3 --fail --location --progress-bar -o "${TMP_DIR}/${ASSET}" "$URL" \
    || err "download failed: $URL (does this release include the ${ASSET} asset?)"

  if [ "$NO_VERIFY" -eq 1 ]; then
    echo "WARNING: --no-verify given; skipping checksum verification." >&2
  else
    SUMS_URL="https://github.com/${REPO}/releases/download/${TAG}/SHA256SUMS.txt"
    curl --retry 3 --fail --location -fsSL -o "${TMP_DIR}/SHA256SUMS.txt" "$SUMS_URL" \
      || err "SHA256SUMS.txt not found at ${SUMS_URL}. Refusing to install an unverified binary; re-run with --no-verify to override at your own risk."
    VERIFIED="$(verify_checksum "${TMP_DIR}" "${ASSET}")" \
      || err "checksum verification FAILED for ${ASSET}; the download does not match ${TAG}'s published digest. Nothing was installed."
    echo "Checksum verified: ${ASSET} SHA256 ${VERIFIED}"
  fi
  SRC="${TMP_DIR}/${ASSET}"
}

if [ "${MONOAGENT_ALLOW_LEGACY_GITHUB:-0}" = "1" ]; then
  install_from_github_legacy
else
  rc=0
  install_from_manifest || rc=$?
  if [ "$rc" -eq 10 ]; then
    echo "Manifest unreachable (${MANIFEST_URL}); falling back to the legacy GitHub path." >&2
    install_from_github_legacy
  fi
fi

chmod +x "$SRC"

# ── Install ──────────────────────────────────────────────────────────────────
# sudo only when the install dir (or, if it does not exist yet, its closest
# existing parent) is not writable by this user — e.g. INSTALL_DIR=~/.local/bin
# never needs it.
writable_target() {
  d="$1"
  while [ ! -d "$d" ]; do
    parent="$(dirname "$d")"
    [ "$parent" = "$d" ] && return 1
    d="$parent"
  done
  [ -w "$d" ]
}
SUDO=""
if [ "$(id -u)" != "0" ] && ! writable_target "$INSTALL_DIR"; then
  command -v sudo >/dev/null 2>&1 \
    || err "${INSTALL_DIR} is not writable by $(id -un) and sudo is not available; re-run as root, or choose a writable directory with INSTALL_DIR=\$HOME/.local/bin"
  SUDO="sudo"
fi

$SUDO mkdir -p "$INSTALL_DIR" || err "cannot create ${INSTALL_DIR}"
$SUDO mv "$SRC" "${INSTALL_DIR}/${BIN}" || err "cannot install into ${INSTALL_DIR}"

echo "Installed: ${INSTALL_DIR}/${BIN} (${TAG:-unknown version}, ${os}/${arch})"
echo "Verify with:  ${INSTALL_DIR}/${BIN} version"
