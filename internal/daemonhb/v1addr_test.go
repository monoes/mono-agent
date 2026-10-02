package daemonhb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHeartbeatCarriesTheV1AddrAndTheConfinementPolicies(t *testing.T) {
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "hb.json"))

	if err := Write(Heartbeat{PID: os.Getpid(), APIAddr: "127.0.0.1:9322", V1Addr: "0.0.0.0:9443", APIConfinement: "any", V1Confinement: "chat-only", ContextConfinement: "sandboxed", AutoConfinement: "any"}); err != nil {
		t.Fatal(err)
	}
	hb, live := Read()
	if !live || hb.APIAddr != "127.0.0.1:9322" || hb.V1Addr != "0.0.0.0:9443" || hb.APIConfinement != "any" || hb.V1Confinement != "chat-only" || hb.ContextConfinement != "sandboxed" || hb.AutoConfinement != "any" {
		t.Fatalf("Read = %+v, live=%v", hb, live)
	}

	// Without a dedicated listener the key is absent, as for the other addresses.
	if err := Write(Heartbeat{PID: os.Getpid(), APIAddr: "127.0.0.1:9322"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"v1_addr", "api_confinement", "v1_confinement", "context_confinement", "auto_confinement"} {
		if strings.Contains(string(raw), key) {
			t.Errorf("%s must be omitted when empty: %s", key, raw)
		}
	}
}
