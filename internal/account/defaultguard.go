package account

import (
	"context"
	"sync"
	"testing"
)

var (
	hostMu       sync.RWMutex
	hostOverride string

	sealerMu       sync.RWMutex
	sealerOverride Sealer
)

// Host is the monoes.me host the machine session belongs to: HostURL, or in a
// devaccount build MONOES_BASE_URL (host_default.go, host_devaccount.go).
func Host() string {
	hostMu.RLock()
	defer hostMu.RUnlock()
	if hostOverride != "" {
		return hostOverride
	}
	return hostFromEnv()
}

// SetHostForTest points Host at a fake monoes.me for the test and restores it on
// cleanup. It panics outside a test binary, like the other test seams, and the
// test must not call t.Parallel(): the marker it sets makes the testing package
// panic if it does.
func SetHostForTest(t testing.TB, host string) {
	t.Helper()
	requireTestBinary("SetHostForTest")
	t.Setenv(testStateEnv, "1")
	hostMu.Lock()
	prev := hostOverride
	hostOverride = host
	hostMu.Unlock()
	t.Cleanup(func() {
		hostMu.Lock()
		hostOverride = prev
		hostMu.Unlock()
	})
}

// SetSealerForTest makes the default store, and so the default guard and the
// package-level calls below, seal the refresh token with s instead of the key
// store, and puts the key store back on cleanup. Without it a test's sign-in
// seals under the account key that internal/secrets reads from the key store at
// every call: the mock keyring that most tests re-make, or, in a test that never
// installs the mock, the developer's own keychain. It panics outside a test
// binary, and the test must not call t.Parallel(): the marker it sets makes the
// testing package panic if it does.
func SetSealerForTest(t testing.TB, s Sealer) {
	t.Helper()
	requireTestBinary("SetSealerForTest")
	t.Setenv(testStateEnv, "1")
	sealerMu.Lock()
	prev := sealerOverride
	sealerOverride = s
	sealerMu.Unlock()
	t.Cleanup(func() {
		sealerMu.Lock()
		sealerOverride = prev
		sealerMu.Unlock()
	})
}

// sealerFor is the sealer of the default store: a test's, or the key store. Only
// an explicit sign-in (interactive) may let the key store ask for a passphrase.
func sealerFor(interactive bool) Sealer {
	sealerMu.RLock()
	s := sealerOverride
	sealerMu.RUnlock()
	switch {
	case s != nil:
		return s
	case interactive:
		return NewInteractiveKeyringSealer()
	}
	return NewKeyringSealer()
}

// DefaultStore is the session store of this OS user: ~/.monoagent/account, its
// refresh token sealed under the key store, which never prompts. Opening it
// creates nothing.
func DefaultStore() (Store, error) { return defaultStore(sealerFor(false)) }

func defaultStore(s Sealer) (Store, error) {
	dir, err := DefaultDir()
	if err != nil {
		return nil, err
	}
	return OpenStore(dir, s), nil
}

// defaultClient is the client of the package-level calls below. interactive is for
// a sign-in, which owns the terminal: sealing the refresh token for the first time
// may ask for the file keyring's passphrase, as the vault does. Nothing else ever
// prompts.
func defaultClient(interactive bool) (*Client, error) {
	st, err := defaultStore(sealerFor(interactive))
	if err != nil {
		return nil, err
	}
	return NewClient(Host(), st), nil
}

// NewDefaultGuard is the guard of a production process: the default store and the
// network Refresher for Host. It creates no file and calls nothing.
func NewDefaultGuard() (*Guard, error) {
	st, err := DefaultStore()
	if err != nil {
		return nil, err
	}
	return NewGuard(GuardOptions{Store: st, Refresher: NewRefresher(Host())}), nil
}

// Login signs in through the browser against Host and stores the session.
func Login(ctx context.Context, o LoginOptions) (Status, error) {
	c, err := defaultClient(true)
	if err != nil {
		return Status{}, err
	}
	return c.Login(ctx, o)
}

// SendEmailCode emails a sign-in code through Host.
func SendEmailCode(ctx context.Context, email string) error {
	c, err := defaultClient(false)
	if err != nil {
		return err
	}
	return c.SendEmailCode(ctx, email)
}

// VerifyEmailCode trades an emailed code for the machine session.
func VerifyEmailCode(ctx context.Context, email, code string) (Status, error) {
	c, err := defaultClient(true)
	if err != nil {
		return Status{}, err
	}
	return c.VerifyEmailCode(ctx, email, code)
}

// Logout revokes the refresh token (best effort) and forgets the login, keeping the machine's
// clock-guard record: the session becomes one with no token (Client.Logout, A23).
func Logout(ctx context.Context) error {
	c, err := defaultClient(false)
	if err != nil {
		return err
	}
	return c.Logout(ctx)
}
