# Release runbook: signed update manifest

Every push to master still publishes a GitHub release (see `.github/workflows/release.yml`
and SECURITY.md, "Release governance"). Alongside it, CI builds, checks and signs what the
updater verifies from the public releases-only repo (default `monoes/mono-agent-releases`).
Releases are hands-free apart from the `release` environment approval.

## What is published

- the release assets and `SHA256SUMS`
- `manifest.json` (schema 1: version, release time, notes URL, and per-platform assets with
  `os`, `arch`, `name`, `url`, `sha256`, `size`, `kind` of `cli` or `app`)
- `manifest.json.sig`: one line, `<key id> <base64 Ed25519 signature>`, made over the exact
  bytes of `manifest.json`. The key id is the first 8 bytes of the SHA-256 of the public key,
  hex encoded.

Assets, `manifest.json` and `manifest.json.sig` are uploaded together, so an unsigned
manifest never sits alone in the public release. Downloads are anonymous. The client pins
only the public key(s) and trusts nothing else.

## How CI signs

The signing key is the **environment secret `RELEASE_SIGNING_KEY`** of the `release`
GitHub environment. It exists nowhere else in CI.

1. `release-manifest` builds `manifest.json` and `SHA256SUMS` from the build artifacts
   (unsigned; it never sees the key).
2. `sign-manifest` runs only for a push to master, with `environment: release` (so it waits
   for the reviewer approval and the branch rule below) and `permissions: contents: read`.
   The secret is set in the env of the one signing step only. Before signing,
   `go run ./cmd/release-manifest sign -key-env RELEASE_SIGNING_KEY -assets-dir ... ` checks
   the manifest against the actual built files and refuses to sign if:
   - any listed asset is missing, or its sha256 or size differs from the built file;
   - a built per-platform file is not listed, or an asset URL is not under this release;
   - the manifest version is not exactly the tag CI is releasing;
   - the version is lower than the latest published release of this repo.

   It then runs `monoagentcli release verify` as a self-check, against the pinned keys if the
   signing key is pinned in `internal/release/keys.go`, otherwise against the public key
   derived from the secret together with a workflow warning that the pin is missing.
   `manifest.json` and `manifest.json.sig` go up as the `signed-manifest` artifact.
3. `publish-releases-repo` (also `environment: release`) uploads assets, `SHA256SUMS`,
   `manifest.json` and `manifest.json.sig` in one command, only when the secrets
   `RELEASES_REPO` (`owner/name`) and `RELEASES_REPO_TOKEN` exist. It fails rather than
   upload without the signature. `sign-manifest` is a no-op when `RELEASES_REPO` is unset.

CI never overwrites `manifest.json` once `manifest.json.sig` exists on the release: the
upload step fails with a message instead. A new version always makes a new release, so this
only matters for a re-run of the same tag.

The `-key-env NAME` flag reads the base64 private key (64-byte key or 32-byte seed) from the
named environment variable. It is never accepted as a flag value, never printed, never written
to disk, and an empty variable is refused.

Optional expiry policy: the manifest job can pass `-expires-days N` to
`release-manifest manifest`, which adds a signed `expires_at` (RFC3339). Clients reject a
manifest whose `expires_at` is in the past. The default (0) omits the field.

Until `manifest.json.sig` exists, clients treat the manifest as unavailable and use the
GitHub API path (while `AllowLegacyGitHubUpdates` is true).

## One-time setup (owner)

1. Create the key straight into the secret. The private key never appears on screen or in
   shell history (keygen refuses to print it to a terminal); the public line is printed to
   stderr:

   ```bash
   monoagentcli release keygen --stdout-private | gh secret set RELEASE_SIGNING_KEY --env release --repo OWNER/REPO
   ```

   Create the environment first (step 3) if it does not exist, since `gh secret set --env`
   needs it. Nothing is stored in your keyring in this mode.
2. Copy the printed public line (`<keyid> <base64pub>`) into `pinnedReleaseKeys` in
   `internal/release/keys.go` and ship it in a release. Until a key is pinned no manifest is
   trusted by clients; CI warns while the pin is missing.
3. Protect the `release` environment. Find your numeric user id, then set the required
   reviewer and limit deployments to master:

   ```bash
   OWNER=your-github-login; REPO=monoes/mono-agent
   ID=$(gh api users/$OWNER --jq .id)
   # required reviewer = the owner (self review stays allowed: you are the only maintainer)
   gh api -X PUT repos/$REPO/environments/release --input - <<JSON
   {"reviewers":[{"type":"User","id":$ID}],"prevent_self_review":false,
    "deployment_branch_policy":{"protected_branches":false,"custom_branch_policies":true}}
   JSON
   # deployment branches: master only
   gh api -X POST repos/$REPO/environments/release/deployment-branch-policies -f name=master -f type=branch
   ```

   `PUT` replaces the environment's settings, so run it once and then check
   Settings -> Environments -> release. Also keep branch protection on master (SECURITY.md).

## Rotation

Clients only trust manifests signed by a key pinned in their own build, so the release that
introduces the new key must still be signed by the old one. Stage the new key first; do not
overwrite the secret yet.

1. Generate the new key into a private file and note its public line (printed to stderr).
   This is the one place a private key touches disk; delete the file in step 4:

   ```bash
   (umask 077; monoagentcli release keygen --stdout-private > "$HOME/scratch/release-key-next")
   ```

2. Ship a release that pins BOTH keys (old and new) in `pinnedReleaseKeys`. CI signs it with
   the old key (still in the secret), which clients already trust.
3. Once that release is out, promote the new key and make it the signer:

   ```bash
   gh secret set RELEASE_SIGNING_KEY --env release --repo OWNER/REPO < "$HOME/scratch/release-key-next"
   ```

4. Delete `$HOME/scratch/release-key-next`.
5. Later, when clients have moved past builds that only know the old key, ship a release
   (signed by the new key) that removes the old line from `pinnedReleaseKeys`.

## Incident: the secret may have leaked

A leaked key can sign manifests that every client pinning it accepts, and only a client update
revokes it. Move fast, in the same order as a rotation but pinning only the new key:

1. Stage a new key (step 1 of Rotation) and note its public line.
2. Ship a release that pins the new key and **removes the leaked key** from
   `pinnedReleaseKeys`. CI signs it with the leaked key (still in the secret), because that is
   the only signature existing clients accept. Approve it yourself, after checking
   `.github/workflows/release.yml` on master and the environment's deployment history for
   anything you did not do.
3. Immediately set the secret to the new key (Rotation step 3) and delete the staged file.
4. Clients that do not update to that release keep trusting the leaked key and need a manual
   install (installer / releases page). If the leak is also a workflow or reviewer
   compromise, fix that first: revoke the offending access, re-check the environment's
   required reviewers and deployment branches.

## Break-glass: sign locally

The local path still works and is the fallback if CI signing is unavailable or untrusted:

```bash
# once: create a key in the OS keyring (pin the printed public key)
monoagentcli release keygen

gh release download vX.Y.Z --repo monoes/mono-agent-releases --pattern manifest.json
go run ./cmd/release-manifest sign -manifest manifest.json -keyring     # or -key-file path/to/key
gh release upload vX.Y.Z manifest.json.sig --repo monoes/mono-agent-releases
monoagentcli release verify manifest.json manifest.json.sig   # add --pubkey "<id> <b64>" to try another key
```

`-keyring` reads the entry written by keygen (service `monoagent-release-signing`, account
`ed25519-v1`, value the base64 of the 64-byte private key; a bare 32-byte seed is accepted
too). Add `-assets-dir DIR -expect-version vX.Y.Z -min-version vA.B.C` to get the same
asset checks as CI. Check `manifest.json` against the run's checksums before signing.

## Making this repo private

`AllowLegacyGitHubUpdates` must be set to false in the same release that makes this repo
private. Otherwise clients keep calling the GitHub API path against a repo they can no
longer read, and the signed manifest is bypassed as a fallback.
