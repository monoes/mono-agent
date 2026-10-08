package main

import (
	"context"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/library"
	"github.com/monoes/mono-agent/internal/storage"
)

// adoptionSetting is the settings row that says a database has had its one try (spec D23).
const adoptionSetting = "account_adoption"

// adoptOlderLogin is library.AdoptIntoAccount, a variable so that a test can see whether the wiring
// calls it without a monoes.me to call.
var adoptOlderLogin = library.AdoptIntoAccount

// adoptFirstRun is the first run of release R (spec section 8 step 3, D23): a library login that
// already exists, on any profile, is exchanged for the machine session. It runs before a gated or
// serving command (an open command calls monoes.me only when asked to) and does nothing while the
// gate is dormant (D22), with no guard installed, when somebody has signed in, or when the database
// is not there yet (a command must not create it for this).
//
// It tries once per database, ever: the try is claimed before it is made, with one row inserted only
// if absent, so two processes that start together make one try between them, and a failed try is
// never repeated (presenting a spent refresh token again could end every login of the account, A24).
// The exchange itself runs under the account store lock (library.AdoptIntoAccount). Any failure is
// swallowed: the command goes on, and nothing is printed or logged, tokens least of all.
func adoptFirstRun(cmd *cobra.Command, cfg *globalConfig) {
	if commandClass(cmd) == classOpen || account.EnforceDate().IsZero() {
		return
	}
	g := account.Current()
	if g == nil {
		return
	}
	if st := g.Status(); st.State != account.StateLocked || st.Reason != account.ReasonNotLoggedIn {
		return
	}
	path := expandPath(cfg.DBPath)
	if _, err := os.Stat(path); err != nil {
		return
	}
	db, err := storage.NewDatabase(path)
	if err != nil {
		return
	}
	defer db.Close()
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	claim, err := db.DB.ExecContext(ctx, `INSERT OR IGNORE INTO settings (key, value) VALUES (?, ?)`,
		adoptionSetting, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return
	}
	if n, _ := claim.RowsAffected(); n == 1 { // otherwise this database has had its try, or another process is making it
		_, _ = adoptOlderLogin(ctx, db.DB, g)
	}
}
