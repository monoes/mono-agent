# Release runbook: signed update manifest

Every push to master still publishes a GitHub release (see `.github/workflows/release.yml`
and SECURITY.md, "Release governance"). Alongside it, CI prepares what the updater will
verify once releases move to the public releases-only repo
(default `monoes/mono-agent-releases`).

## What is published

- the release assets and `SHA256SUMS`
- `manifest.json` (schema 1: version, release time, notes URL, and per-platform assets with
  `os`, `arch`, `name`, `url`, `sha256`, `size`, `kind` of `cli` or `app`)
- `manifest.json.sig`: one line, `<key id> <base64 Ed25519 signature>`, made over the exact
  bytes of `manifest.json`. The key id is the first 8 bytes of the SHA-256 of the public key,
  hex encoded.

Downloads are anonymous. The client pins only the public key(s) and trusts nothing else.

## Two-step flow

The signing key is the owner's. **CI never holds it.**

1. CI (`release-manifest` job) builds `manifest.json` and `SHA256SUMS` with
   `go run ./cmd/release-manifest manifest` and attaches them to the run as the
   `unsigned-manifest` artifact (the checksums are also in the run summary). After the
   `release` environment is approved, the `publish-releases-repo` job uploads the assets and
   the unsigned manifest to the releases repo, but only when the secrets `RELEASES_REPO`
   (`owner/name`) and `RELEASES_REPO_TOKEN` (a token that can write releases there) both
   exist. Without them it does nothing.
2. The owner downloads the unsigned manifest and signs it locally:

   ```bash
   # once: create the key (stored in the OS keyring), pin the printed public key
   monoagentcli release keygen

   # per release
   gh release download vX.Y.Z --repo monoes/mono-agent-releases --pattern manifest.json
   go run ./cmd/release-manifest sign -manifest manifest.json -keyring
   gh release upload vX.Y.Z manifest.json.sig --repo monoes/mono-agent-releases
   monoagentcli release verify --repo monoes/mono-agent-releases --tag vX.Y.Z
   ```

   Check `manifest.json` against the run's checksums (or the `unsigned-manifest` artifact)
   before signing. `-keyring` reads the entry written by keygen (service
   `monoagent-release-signing`, account `ed25519-v1`, value the base64 of the 64-byte
   private key; a bare 32-byte seed is accepted too). Use `-key-file path/to/key` for a key
   in a file.

   CI never overwrites `manifest.json` once `manifest.json.sig` exists on the release: the
   upload step fails with a message instead. A new version always makes a new release, so
   this only matters for a re-run of the same tag.

3. Optional expiry policy: the manifest job can pass `-expires-days N` to
   `release-manifest manifest`, which adds a signed `expires_at` (RFC3339). Clients reject a
   manifest whose `expires_at` is in the past, so a stale or replayed manifest stops working
   after N days. The default (0) omits the field.

Until `manifest.json.sig` is uploaded, clients treat the manifest as unavailable and use the
GitHub API path (while `AllowLegacyGitHubUpdates` is true).

## One-time setup

Run `monoagentcli release keygen`, paste the printed public key line into
`internal/release/keys.go` (`pinnedReleaseKeys`), and ship it in a release. Until a key is
pinned no manifest is trusted.

## Making this repo private

`AllowLegacyGitHubUpdates` must be set to false in the same release that makes this repo
private. Otherwise clients keep calling the GitHub API path against a repo they can no
longer read, and the signed manifest is bypassed as a fallback.
