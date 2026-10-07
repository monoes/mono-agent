package libraryfake

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

var devPrivate = sync.OnceValue(func() ed25519.PrivateKey {
	_, priv := accounttest.DevKeyPair()
	return priv
})

// TrustKey makes the calling test's account.Verify trust the fake's signing key,
// the development key under accounttest.DevKID, as a devaccount build always does.
func TrustKey(t testing.TB) {
	pub, _ := accounttest.DevKeyPair()
	account.SetTrustedKeysForTest(t, []account.Key{{KID: accounttest.DevKID, Public: pub}})
}

// signJWT mints an audience-bound EdDSA access token for g's user, valid for
// AccessTTL from now, shaped like monoes.me's (plan A, spike S6): typ at+jwt, aud
// an array that holds the MonoAgent audience, azp and client_id both the client.
// jti keeps two tokens minted in the same second apart.
func (s *Server) signJWT(g *grant, now time.Time, jti int) string {
	seg := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	head := seg(map[string]string{"alg": "EdDSA", "typ": "at+jwt", "kid": accounttest.DevKID})
	body := seg(map[string]any{"iss": account.Issuer, "aud": []string{account.Audience, account.Issuer + "/oauth2/userinfo"},
		"azp": account.ClientID, "client_id": account.ClientID, "sub": g.user.ID, "scope": strings.Join(g.scopes, " "),
		"iat": now.Unix(), "exp": now.Add(s.AccessTTL).Unix(), "plan": "free", "jti": fmt.Sprintf("jti-%d", jti)})
	sig := ed25519.Sign(devPrivate(), []byte(head+"."+body))
	return head + "." + body + "." + base64.RawURLEncoding.EncodeToString(sig)
}
