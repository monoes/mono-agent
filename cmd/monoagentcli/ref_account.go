package main

import "github.com/spf13/cobra"

const refAccountText = `monoagentcli — monoes.me account

CURRENT BUILD
  The account gate is dormant: no enforcement date is set, nothing locks or
  warns, and no implicit adoption, refresh or background refresher runs. Local
  work needs no account. The online library still requires a sign-in for reads.
  Production signing keys are not pinned yet; a production machine session
  cannot be verified until the server/key rollout. No enforcement date is
  announced here. The behavior below applies if a future release enables it.

COMMANDS (explicit account requests may contact monoes.me)
  monoagentcli account login
  monoagentcli account login --no-browser --timeout 2m
  monoagentcli account login --email you@example.com
  monoagentcli account login --email you@example.com --send
  monoagentcli account login --email you@example.com --code <code>
  monoagentcli account status [--offline] [--json]
  monoagentcli account logout
  library login, library logout and library status manage the same machine
  session. status --offline reads locally without refreshing. An explicit
  status can return locked / not_logged_in and exit 4 even while the gate is
  dormant; that does not prevent local work. Never paste a token into a command,
  workflow or chat; ask the user to run account login.

STORAGE AND NETWORK
  ~/.monoagent/account/ (0700): session.json, refresh.enc, session.lock (0600).
  One session per OS user, shared by profiles regardless of --db-path.
  refresh.enc is sealed under the OS key store (or the file keyring fallback).
  Sign in separately on every machine. Never copy or restore this folder, even
  from an old backup, or bake it into an image: reused refresh tokens can revoke
  a sign-in on the original and its copies. Independent sign-ins are separate.
  Default account commands use https://monoes.me; another sign-in host requires
  -tags devaccount. The library still honors MONOES_BASE_URL for its requests,
  but sends a machine session only to its issuing host.
  monoes.me receives the client id, sign-in proof or refresh token, requested
  resource/scopes, and with --email the address you type. It sees the account,
  request time and source IP address. No device identifier or usage counters
  are sent by the account gate. SECURITY.md lists the network requests.

WHEN ENABLED IN A FUTURE RELEASE
  warn: before enforce_from (account status --json), commands run and warn.
  enforced: from that date, locked gated commands refuse before doing work.
  ok: the signed token is valid. grace: renewal failed but the token is still
  valid, at most 24 hours from its signed issue time. locked: work is refused.
  Reasons include not_logged_in, expired, refused, clock_rollback, clock_skew,
  key_unknown (update), invalid, unreachable, server_error, keyring_unavailable
  and unconfirmed. Only invalid_grant in response to a refresh is refusal;
  other failures preserve grace while the token remains valid. An unknown
  refresh outcome is retried for 240 seconds; this machine then drops its
  refresh token and becomes unconfirmed. Sign in again on this machine.
  Clock rollback can lock work; a freshly verified token resets the clock guard.

WHAT STAYS OPEN WHEN ENABLED
  Open: version, help, completion (including __complete and __completeNoDesc),
  ref, update, doctor (including doctor fix), setup, account (all subcommands),
  library login, library logout, library status.
  Serving: daemon, httpapi, mcp (including --grant), extension serve (also the
  bridge serve alias). They start while locked and refuse work; they stay up
  so service managers do not repeatedly respawn them.
  Every other command is gated, including local-only commands, daemon install,
  daemon restart, daemon uninstall and org serve.

REFUSAL WHEN ENABLED
  Exit 4; first line: Log in to monoes.me first: monoagentcli account login
  With --json, login_required is true and code is auth_or_connection.
  Branch on exit 4 and login_required, not on message wording.
  HTTP API and /v1: 401 login_required. GET /health stays open and reports the
  account. Webhooks: 503 without execution. Extension bridge: account_locked.
  MCP: tool error. A refused workflow run records FAILED / login_required:.
  A locked daemon stays up, starts no new work, stops its org services and
  resumes when a valid sign-in appears. The Docker version health check only
  tests liveness, not permission to work.

HEADLESS AND DOCKER
  Sign in with account login --email as the OS user running the daemon.
  Docker HOME=/data keeps /data/.monoagent/account/ in the persistent volume.
  A container has no OS keychain: enable MONOAGENT_ALLOW_FILE_KEYRING=1 and
  create a private passphrase file BEFORE login. Set
  MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE to it (or use
  ~/.monoagent/keyring-passphrase). README's Docker section has the commands.
  A daemon has no terminal. Without a passphrase source it cannot unseal its
  refresh token: an enabled gate reports grace / keyring_unavailable, then
  locks at token expiry (at most 24 hours). secret keyring set-passphrase is
  itself gated. No account credential comes from an environment variable;
  there are no unattended machine tokens.

DEVELOPERS
  -tags devaccount trusts a development signing key and permits a development
  host via MONOES_BASE_URL. MONOAGENT_DEV_ENFORCE_FROM is read only by that
  build to exercise warning/enforcement. Releases never carry devaccount.
  The current MIT license is unchanged; the account gate is a product check,
  not a security boundary against the OS user who can rebuild the source.
`

func refAccountCmd() *cobra.Command {
	return &cobra.Command{
		Use: "account", Short: "Machine sign-in, the dormant account gate, and headless setup",
		Args: cobra.NoArgs,
		Run:  func(cmd *cobra.Command, args []string) { cmd.Print(refAccountText) },
	}
}
