package library

import (
	"context"
	"database/sql"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/profiledir"
)

// vaultWriteTimeout bounds the update of an older login's vault entry once its exchange is done:
// a local database write, made on a context the caller's cancellation does not reach (A20).
const vaultWriteTimeout = 10 * time.Second

// AdoptIntoAccount turns an older monoes.me library login into the machine session
// (spec D23): the first profile, the active one first, whose vault holds a login
// for the account host and whose refresh token monoes.me exchanges for an
// audience-bound token becomes the session, and its vault entry is removed.
//
// It lives here and not in internal/account because it reads the library's vault
// entry. It does nothing while the gate is dormant (nothing is called implicitly)
// or when somebody has already signed in (a session with a token, or one that
// monoes.me refused; a session with neither is only a clock-guard record, A23 and
// A25), and it writes nothing on a machine with no older login. Whatever monoes.me
// answers, the outcome is
// "adopted" or "sign in once more", never a refusal; the error is for local
// failures.
//
// Its caller (B5a) tries once per database, claiming the try before the call, so
// one call tries every profile that has an older login and decides the fate of each
// refresh token itself, because monoes.me ends every refresh token of the account
// when a spent one is presented again, and an older binary would present whatever
// is left in the vault. Adopted: the vault entry is removed. Dead (invalid_grant:
// spent, revoked or expired): removed too, and the next profile is tried, its login
// being a chain of its own. No verdict: the call stops there, and the login stays as
// it was when nothing was sent or monoes.me answered with a complete 4xx (it processed
// nothing), with the tokens monoes.me issued when it answered with tokens that no
// session can be made of (keepAlive), and goes when the request went out and its outcome is
// unknown (A24: any other answer, a 5xx included, or none; monoes.me may have rotated
// the token and the answer is lost, and presenting it again after the 300-second reuse
// window would end every refresh token of the account), or when the answer arrived and the new refresh token could not be stored here (A24(d); the error is returned). The older login is read and
// updated under the account store lock, so no other process presents its token
// meanwhile. Once an exchange is sent it is completed, and what became of the older
// refresh token is written to the vault, even if ctx is cancelled meanwhile (A20): a
// spent refresh token left in the vault would be presented again. A caller that must
// judge at once afterwards builds a new guard.
func AdoptIntoAccount(ctx context.Context, db *sql.DB, g *account.Guard) (adopted bool, err error) {
	if db == nil || g == nil || account.EnforceDate().IsZero() {
		return false, nil
	}
	if st := g.Status(); st.State != account.StateLocked || st.Reason != account.ReasonNotLoggedIn {
		return false, nil
	}
	store, err := account.DefaultStore()
	if err != nil {
		return false, err
	}
	host := account.Host()
	client := account.NewClient(host, store)
	profiles, err := profiledir.List(ctx, db)
	if err != nil {
		return false, err
	}
	for _, p := range activeFirst(profiles) {
		vs := &VaultStore{DB: db, ProfileID: p.ID, BaseURL: host}
		if tok, lerr := vs.Load(ctx); lerr != nil || tok == nil || tok.RefreshToken == "" {
			continue // a look without the lock: a machine with no older login writes nothing
		}
		var cur *Token
		res, aerr := client.Adopt(ctx, func() (string, *account.User, bool) {
			tok, lerr := vs.Load(ctx) // again, under the lock: another process may have spent or replaced it
			if lerr != nil || tok == nil || tok.RefreshToken == "" {
				return "", nil, false
			}
			cur = tok
			var u *account.User
			if tok.User != nil {
				u = &account.User{ID: tok.User.ID, Email: tok.User.Email, Username: tok.User.Username}
			}
			return tok.RefreshToken, u, true
		}, func(res account.AdoptResult) {
			// The exchange has spent the older refresh token, so what becomes of it is settled even if
			// the caller gave up meanwhile (A20): a context of its own, bounded.
			vctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), vaultWriteTimeout)
			defer cancel()
			switch {
			case res.Adopted, res.Dead, res.Unconfirmed:
				_ = vs.Delete(vctx) // adopted, dead, possibly spent (A24) or spent and not stored here (A24(d)): it must never be presented again
			case res.Tokens != nil && cur != nil:
				keepAlive(vctx, vs, cur, res.Tokens)
			}
		})
		switch {
		case aerr != nil:
			return false, aerr
		case res.Adopted:
			_, _ = g.Refresh(ctx) // best effort: g re-reads the session
			return true, nil
		case cur != nil && !res.Dead:
			return false, nil // no verdict on this login (it stays, or went if it may be spent): the others wait
		}
	}
	return false, nil
}

// activeFirst puts the active profile in front, the rest in their order.
func activeFirst(ps []profiledir.Profile) []profiledir.Profile {
	out := make([]profiledir.Profile, 0, len(ps))
	for _, p := range ps {
		if p.Default {
			out = append(out, p)
		}
	}
	for _, p := range ps {
		if !p.Default {
			out = append(out, p)
		}
	}
	return out
}

// keepAlive writes the tokens an unsuccessful adoption was answered with back into
// the older login, whose refresh token the exchange spent. The expiry is left open:
// the next call finds out. A vault that does not take them still holds the spent
// token, which must never be presented again, so the entry is removed instead.
func keepAlive(ctx context.Context, vs *VaultStore, tok *Token, ts *account.TokenSet) {
	nt := *tok
	nt.AccessToken, nt.RefreshToken, nt.ExpiresAt = ts.AccessToken, ts.RefreshToken, time.Time{}
	if vs.Save(ctx, &nt) != nil {
		_ = vs.Delete(ctx)
	}
}
