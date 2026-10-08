//go:build devaccount && !windows

package accountsmoke

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/monoes/mono-agent/internal/library"
	"github.com/monoes/mono-agent/internal/secrets"
	"github.com/monoes/mono-agent/internal/storage"
)

const seedEnv = "ACCOUNTSMOKE_SEED_OLDER_LOGIN"

// TestSeedOlderLogin is not a test: the smoke starts it as a process of its own to store an older
// library login in the rig's vault with the library's own writer. The vault key is cached per
// process, so a process per rig keeps one rig's key from leaking into another's.
func TestSeedOlderLogin(t *testing.T) {
	home := os.Getenv(seedEnv)
	if home == "" {
		t.Skip("started by the smoke only")
	}
	keyring.MockInitWithError(os.ErrNotExist) // the binary under test has no OS keyring either: the file keyring
	if err := os.MkdirAll(filepath.Join(home, ".monoagent"), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := storage.NewDatabase(filepath.Join(home, ".monoagent", "monoagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.ApplyMigrations(); err != nil { // the previous release had migrated it
		t.Fatal(err)
	}
	host := os.Getenv("MONOES_BASE_URL")
	vs := &library.VaultStore{DB: db.DB, ProfileID: "default", BaseURL: host}
	tok := &library.Token{AccessToken: os.Getenv("SEED_ACCESS"), RefreshToken: os.Getenv("SEED_REFRESH"), TokenType: "Bearer",
		Method: "pkce", BaseURL: host, User: &library.User{ID: "u-ada", Username: "ada", Email: testEmail}}
	if err := vs.Save(context.Background(), tok); err != nil {
		t.Fatal(err)
	}
	// A machine on the file keyring has the account key only once somebody has signed in (an implicit
	// call may not prompt for the passphrase that creates it), so make it as that sign-in would.
	if _, _, err := secrets.AccountKEK(true, true); err != nil {
		t.Fatal(err)
	}
}

// seedOlderLogin gives the rig the login the previous release stored.
func (r *rig) seedOlderLogin() {
	r.t.Helper()
	access, refresh := r.fake.NewGrant("ada")
	cmd := exec.Command(os.Args[0], "-test.run=^TestSeedOlderLogin$")
	cmd.Env = r.env(seedEnv+"="+r.home, "SEED_ACCESS="+access, "SEED_REFRESH="+refresh)
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("seeding the older login: %v\n%s", err, out)
	}
}

// Two processes that start together on a machine with an older login make one try between them:
// one refresh grant reaches monoes.me, the older refresh token is never presented again (monoes.me
// would end every login of the account), and the older login is gone.
func TestTwoFirstRunsMakeOneAdoption(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("the vault key lives in the OS keychain there, not in the rig's file keyring")
	}
	r := newRig(t, rigOptions{enforce: past})
	r.seedOlderLogin()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r.run("workflow", "list") }()
	}
	wg.Wait()
	r.run("workflow", "list")
	if n := r.edge.refreshes(); n != 1 {
		t.Fatalf("%d refresh grants reached monoes.me, want 1", n)
	}
	if r.fake.Replays != 0 {
		t.Fatal("a spent refresh token was presented again")
	}
	if sess := r.session(); sess == nil || sess.AccessToken == "" {
		t.Fatal("nobody adopted the older login")
	}
	if res := r.run("workflow", "list"); res.code != 0 {
		t.Fatalf("after the adoption the gated command is refused (exit %d): %s", res.code, res.stderr)
	}
	if n := r.edge.refreshes(); n != 1 {
		t.Fatalf("a later run presented a token: %d grants", n)
	}
}
