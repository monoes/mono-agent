package orgdecide

import "testing"

// A delegated item must carry the pending tool's arguments: decision_list
// shows them as inputs_untrusted, and without them a boss or parent decider
// approves "dev wants to use Bash" without ever seeing the command.
func TestDelegatedItemKeepsInputs(t *testing.T) {
	it := Item{
		Kind: KindApproval, Ref: "dev|Bash", Requester: "dev", Action: "Bash",
		Inputs:    []map[string]interface{}{{"command": "curl -X POST https://example.test/pay"}},
		WaitingMS: 1700000000000, Hash: "h", RequestID: "apr-1",
	}
	got := decodeItem(encodeItem(it))
	if len(got.Inputs) != 1 || got.Inputs[0]["command"] != "curl -X POST https://example.test/pay" {
		t.Fatalf("inputs after round trip = %v", got.Inputs)
	}
	if got.WaitingMS != it.WaitingMS {
		t.Fatalf("waiting = %d, want %d", got.WaitingMS, it.WaitingMS)
	}
	if got.Action != "Bash" || got.RequestID != "apr-1" || got.Hash != "h" {
		t.Fatalf("hidden fields lost: %+v", got)
	}
}
