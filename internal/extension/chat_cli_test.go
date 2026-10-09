package extension

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeCLI writes a shell script standing in for monoagentcli: it logs its
// argv (one line per call) and answers as the script body says.
func fakeCLI(t *testing.T, body string) (bin, log string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake")
	}
	dir := t.TempDir()
	bin, log = filepath.Join(dir, "monoagentcli"), filepath.Join(dir, "argv.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\n" + body
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

func readLog(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestCLIBackendRunTurn(t *testing.T) {
	bin, log := fakeCLI(t, `
cat > "$0.stdin"
echo '{"admitted":true,"existed":false,"turn":{"id":"t1"}}'
echo '{"seq":1,"type":"assistant.delta","payload":{"partId":"p","text":"yo"}}'
echo '{"seq":2,"type":"turn.finished","payload":{"status":"completed"}}'
`)
	b := &cliChatBackend{bin: bin}
	var got []ChatEvent
	spec := ChatTurnSpec{Profile: "p-work", Conversation: "c1", Turn: "t1", Instance: "ext-1", Message: "--sneaky"}
	if err := b.RunTurn(context.Background(), spec, func(e ChatEvent) { got = append(got, e) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Type != "assistant.delta" || got[1].Seq != 2 {
		t.Fatalf("events = %+v", got)
	}
	want := "--profile=p-work --json chat --conversation=c1 --turn=t1 --instance=ext-1 --tools=monoagent:read --prompt-stdin"
	if lines := readLog(t, log); len(lines) != 1 || lines[0] != want {
		t.Fatalf("argv = %q", lines)
	}
	// The message (page text included) goes over stdin, so it is in no
	// process listing.
	if in, err := os.ReadFile(bin + ".stdin"); err != nil || string(in) != "--sneaky" {
		t.Fatalf("stdin = %q, %v", in, err)
	}
	if strings.Contains(want, "runs") {
		t.Fatal("tools must never include runs")
	}
}

func TestCLIBackendRefusals(t *testing.T) {
	cases := []struct{ name, body, code string }{
		{"busy by message", `echo '{"error":"chat: conversation has an active turn","code":""}'; exit 3`, CodeBusy},
		{"coded", `echo '{"error":"not logged in","code":"agent_not_setup"}'; exit 4`, "agent_not_setup"},
		{"already ran", `echo '{"admitted":true,"existed":true,"turn":{"id":"t1"}}'`, ""},
	}
	for _, c := range cases {
		bin, _ := fakeCLI(t, c.body)
		err := (&cliChatBackend{bin: bin}).RunTurn(context.Background(), ChatTurnSpec{Conversation: "c1", Turn: "t1", Instance: "i"}, func(ChatEvent) {})
		if err == nil || errCode(err) != c.code {
			t.Errorf("%s: err = %v (code %q)", c.name, err, errCode(err))
		}
	}
}

func TestCLIBackendFinishesStrandedTurn(t *testing.T) {
	bin, log := fakeCLI(t, `
case "$*" in *history*) exit 0;; esac
cat > "$0.stdin"
echo '{"admitted":true,"existed":false,"turn":{"id":"t1"}}'
exit 1
`)
	err := (&cliChatBackend{bin: bin}).RunTurn(context.Background(), ChatTurnSpec{Conversation: "c1", Turn: "t1", Instance: "i"}, func(ChatEvent) {})
	if err == nil {
		t.Fatal("a turn that never finished must fail")
	}
	lines := readLog(t, log)
	if len(lines) != 2 || !strings.Contains(lines[1], "history finish --status=failed") || !strings.HasSuffix(lines[1], "-- c1 t1") {
		t.Fatalf("argv = %q", lines)
	}
}

func TestCLIBackendCancelRecordsCancelled(t *testing.T) {
	bin, log := fakeCLI(t, `
case "$*" in *history*) exit 0;; esac
cat > "$0.stdin"
echo '{"admitted":true,"existed":false,"turn":{"id":"t1"}}'
exec sleep 30
`)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- (&cliChatBackend{bin: bin}).RunTurn(ctx, ChatTurnSpec{Conversation: "c1", Turn: "t2", Instance: "i"}, func(ChatEvent) {})
	}()
	time.Sleep(400 * time.Millisecond) // let the child print its admission line
	cancel()
	if err := <-done; errCode(err) != "cancelled" {
		t.Fatalf("err = %v", err)
	}
	lines := readLog(t, log)
	if len(lines) != 2 || !strings.Contains(lines[1], "history finish --status=cancelled") {
		t.Fatalf("argv = %q", lines)
	}
}

func TestCLIBackendListEvents(t *testing.T) {
	bin, log := fakeCLI(t, `
case "$*" in
*history\ turns*) echo '{"items":[{"id":"t7"}],"next_cursor":""}';;
*history\ events*) echo '{"items":[{"seq":4,"type":"notice","payload":{"a":1}}],"turn":{"status":"active"},"has_more":false}';;
esac
`)
	b := &cliChatBackend{bin: bin}
	page, err := b.ListEvents(context.Background(), "p-work", "c1", "", 3)
	if err != nil {
		t.Fatal(err)
	}
	if page.Turn != "t7" || !page.TurnActive || len(page.Events) != 1 || page.Events[0].Seq != 4 {
		t.Fatalf("page = %+v", page)
	}
	lines := readLog(t, log)
	if len(lines) != 2 || !strings.HasSuffix(lines[1], "history events --after-seq=3 -- c1 t7") {
		t.Fatalf("argv = %q", lines)
	}
}

func TestCLIBackendCreateConversation(t *testing.T) {
	bin, log := fakeCLI(t, `echo '{"id":"conv-9"}'`)
	id, err := (&cliChatBackend{bin: bin}).CreateConversation(context.Background(), "", "claude", "haiku")
	if err != nil || id != "conv-9" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	if got := readLog(t, log)[0]; got != "--json chat history create --runtime=claude --workflow=general --model=haiku" {
		t.Fatalf("argv = %q", got)
	}
}
