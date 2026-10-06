// Package account is the machine-wide monoes.me session. One access token,
// an EdDSA-signed JWT that monoes.me issues and this binary verifies offline
// against keys pinned in it, plus a refresh token sealed under the OS keyring
// key, prove that a signed-in account exists on this machine. A Guard turns
// that session into a cached verdict (ok, grace or locked) that every gate of
// the program asks for: the CLI gate, the engine and runner checks, and the
// doors (HTTP API, webhook server, extension bridge, MCP).
//
// Until the enforcement date in rollout.go is set the package is dormant:
// nothing locks, nothing warns and nothing contacts monoes.me implicitly.
//
// Import rule: this package imports the standard library, golang.org/x/sys
// (the Windows lock and rename) and internal/secrets (the keyring key), and
// nothing else of this repository, so every other package may import it.
//
// Locking rule: every LoadRefresh and SaveRefresh is called with session.lock
// held (Store.Lock), as are the writes of session.json. The refresh algorithm
// does so, and so must any other caller (a logout, an adoption of an older
// login). Besides keeping the processes apart, it is what lets the store keep at
// most one key store call parked at a time (store.go, keyStoreLimit): a caller
// that does not hold the lock can park a second one.
//
// Files:
//
//	claims.go           the constants of the contract: host, audience, windows, timings
//	state.go            State, Reason, User, Status and Allowed
//	session.go          Session (with the marker of a grant in flight), NewSession and the verdict (Evaluate)
//	errors.go           LoginRequiredError and the errors a Refresher returns
//	rollout.go          the enforcement date (dormant until it is set) and Enforced
//	globals.go          the lock and the flag behind the process-wide variables
//	keys.go             the pinned verification keys and TrustedKeys
//	keys_default.go     extra keys of a default build: none
//	keys_devaccount.go  the slot for the development key of a -tags devaccount build
//	verify.go           strict EdDSA verification of an access token
//	sealer.go           the sealer of the refresh token, under the OS keyring key
//	store.go            the Store interface and the session on disk: atomic writes, the cross-process lock,
//	                    the bounded key store calls (callKeyStore) and the errSessionInvalid sentinel
//	readfile.go         reads a file of the store: a regular file of at most 64 KiB
//	readfile_unix.go    opens it without waiting for a FIFO's writer (O_NONBLOCK)
//	readfile_windows.go opens it on Windows
//	rename_unix.go      the rename of a write on Unix (os.Rename)
//	rename_windows.go   the rename of a write on Windows, written through (MoveFileEx)
//	lock_unix.go        the file lock on Unix (flock)
//	lock_windows.go     the file lock on Windows (LockFileEx)
//	syncdir_unix.go     flushes a directory after a rename or a remove
//	syncdir_windows.go  does nothing: Windows has no directory flush (its rename writes through)
//	guard.go            the Guard and its options, the Refresher and TokenSet it uses, the cached verdict,
//	                    Status, Require, OnRefused and Close, and the timings
//	guard_refresh.go    the refresh algorithm (EnsureFresh, Refresh, what each answer does to the session)
//	                    and the high-water mark and clock-guard record (touchHW, keepRecord)
//	guard_pending.go    a grant whose answer may be lost: its marker, its age and the drop of the token
//	guard_loop.go       the background refresher: StartRefresher and its loop
//	process.go          the process-wide guard: Install, InstallForTest, Current, Require, CurrentStatus
//	testhooks.go        the other test seams (SetTrustedKeysForTest, SetEnforceFromForTest, StrictForTest),
//	                    which panic outside a test binary like InstallForTest
package account
