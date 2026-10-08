package account

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Logout revokes the refresh token at monoes.me (best effort) and forgets the login on
// this machine, whatever monoes.me answers: the refresh token is deleted and the session
// is replaced by one that holds no token, only the clock-guard record (clockRecord). It
// never erases that record (spec §4.5, A23): the enforcement date is judged against the
// highest time this machine has seen, and a machine that could drop the mark by logging
// out could run again after setting its clock back before the date. It revokes only a token
// that the guard would present (revocable, A24); any other is forgotten locally only.
func (c *Client) Logout(ctx context.Context) error {
	if !c.stored() {
		return nil // nothing to forget: leave no files behind
	}
	unlock, err := c.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	// Both reads are made under the lock. A refresh in another process before this point has
	// rotated the refresh token: revoking the one read earlier would revoke a dead token and
	// leave the live one valid at monoes.me.
	sess, lerr := c.Store.Load()
	rt, _ := c.Store.LoadRefresh() // a key store that does not answer means no revocation, not no logout
	if rt != "" && revocable(sess, lerr) {
		c.revoke(ctx, rt)
	}
	err = c.Store.DeleteRefresh()
	if keep := c.clockRecord(sess, lerr); keep != nil {
		if saveErr := c.Store.Save(keep); err == nil {
			err = saveErr
		}
	}
	return err
}

// revocable says whether logout may present the refresh token to the revoke endpoint: only when
// it knows that the guard would present that token itself (A24). That takes a session.json that
// was read and holds no pending_since marker (a refresh whose answer is in doubt: the token may
// already be spent, and revoking it would not touch the successor this machine never received),
// no unconfirmed drop (a token the guard gave up and never presents again) and no refusal. An
// unreadable file, one of a version this build does not read and a refresh token with no
// session.json behind it hide what a marker would say. Anything else is forgotten locally only: a
// refresh token left at monoes.me that nobody holds is the accepted cost, while revoking one that
// may have been rotated away might count as its reuse and sign out every install (what the revoke
// route does with a rotated token is unmeasured).
func revocable(sess *Session, lerr error) bool {
	if lerr != nil || sess == nil {
		return false
	}
	return sess.PendingSince.IsZero() && sess.LastResult != string(ReasonUnconfirmed) && sess.State != stateRefused
}

// lock takes the store lock for Logout and Adopt, and waits for it at most lockWaitTimeout, as the
// guard does: another process can hold it for as long as a passphrase prompt stays open, and an
// adoption runs implicitly before a command. Past the bound nothing has been read or sent.
func (c *Client) lock(ctx context.Context) (func(), error) {
	lctx, cancel := context.WithTimeout(ctx, lockWaitTimeout)
	defer cancel()
	unlock, err := c.Store.Lock(lctx)
	if err != nil {
		return nil, fmt.Errorf("account: taking the session lock: %w", err)
	}
	return unlock, nil
}

// stored says whether the account folder holds anything to forget. It looks at the two
// file names only, reads no secret and makes no file, so a machine with nothing stored
// is left untouched.
func (c *Client) stored() bool {
	for _, name := range []string{"session.json", "refresh.enc"} {
		if _, err := os.Stat(filepath.Join(c.Store.Dir(), name)); !errors.Is(err, os.ErrNotExist) {
			return true
		}
	}
	return false
}

// clockRecord is the session that logout leaves in place of sess: no token, the host and
// the high-water mark, the later of the one on file and now. It is nil when there is no
// session to replace (a refresh token alone has no mark to keep) and when sess already is
// such a record, so a second logout writes nothing. An unreadable session.json (lerr)
// loses its mark, so its record starts from now: the one write in this package that follows a
// failed Load (spec §4.6).
func (c *Client) clockRecord(sess *Session, lerr error) *Session {
	now := c.Now()
	switch {
	case lerr != nil:
		return &Session{V: 1, Host: c.Host, HW: now}
	case !signedIn(sess):
		return nil
	}
	hw := now
	if sess.HW.After(hw) {
		hw = sess.HW // a clock set back must not lower the mark
	}
	return &Session{V: 1, Host: sess.Host, HW: hw}
}

// revoke asks monoes.me to revoke token, within five seconds and never twice
// waiting for a dead network.
func (c *Client) revoke(ctx context.Context, token string) {
	if checkHost(c.Host) != nil {
		return // a refresh token never travels in the clear
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ep, err := DiscoverEndpoints(rctx, c.HTTP, c.Host)
	if err != nil {
		return
	}
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, ep.RevocationEndpoint,
		strings.NewReader(url.Values{"token": {token}, "client_id": {ClientID}}.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if resp, err := c.HTTP.Do(req); err == nil {
		resp.Body.Close()
	}
}
