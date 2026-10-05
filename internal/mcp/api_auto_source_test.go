package mcp

// What api_auto_set must never do cannot be seen from outside: reading a key leaves nothing to
// observe. jevconf.ResolveKey (and the client made with it) decrypts the vault entry, and may ask a
// keyring or a passphrase on a stdin that is the JSON-RPC stream; the tool only asks where a key is
// (openaiapi.DefaultAuto's Status, which lists names and never decrypts). So this looks at the code
// of the tool: no import of the vault or of a Jev client, and no call that resolves or uses a key.

import (
	"os"
	"strings"
	"testing"
)

func TestAPIAutoSetNeverResolvesOrUsesTheJevKey(t *testing.T) {
	src, err := os.ReadFile("apiauto_tool.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`internal/secrets"`, `internal/jev"`, "ResolveKey", "NewClient", "VaultKeyName", "secrets."} {
		if strings.Contains(string(src), forbidden) {
			t.Errorf("apiauto_tool.go mentions %s: the tool must only ask where the key is, never resolve or use it", forbidden)
		}
	}
}
