package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/ai/chatevents"
)

// A turn agent exec gave up on after 429s (agent-exec rev 20): each retry
// notice and the final "Rate limited by …" message stay in the timeline,
// the turn fails, and the conversation takes the next turn as usual.
func TestJournaledRateLimitedTurnThenNextTurn(t *testing.T) {
	dbPath := newChatCLITestDB(t)
	store := openTestChatStore(t, dbPath)
	conv, _ := store.CreateConversation("default", "agent", "general", "fake", "", "")
	mark := filepath.Join(t.TempDir(), "limited-once")
	const final = "Rate limited by openrouter/x:free (429) after 3 attempts. Free models are rate-limited; try again later or pick another model."
	bin := writeFakeMonomindForChat(t, `  echo '{"v":1,"type":"start","runtime":"fake","cwd":"/app","pid":1}'
  echo '{"v":1,"type":"session","session_id":"th_rl"}'
  if [ ! -f '`+mark+`' ]; then
    touch '`+mark+`'
    echo '{"v":1,"type":"status","phase":"notice","message":"Rate limited (429) by openrouter/x:free; retrying in 2s (attempt 2/3)"}'
    echo '{"v":1,"type":"status","phase":"notice","message":"Rate limited (429) by openrouter/x:free; retrying in 5s (attempt 3/3)"}'
    echo '{"v":1,"type":"error","code":"rate-limited","fatal":true,"message":"`+final+`"}'
    echo '{"v":1,"type":"done","exit_code":1}'
    exit 1
  fi
  echo '{"v":1,"type":"assistant","text":"hello"}'
  echo '{"v":1,"type":"result","subtype":"success","is_error":false,"stop_reason":"end_turn","text":"hello"}'
  echo '{"v":1,"type":"done","exit_code":0}'`)

	out, err := runChatCmd(t, dbPath, bin, "--conversation", conv.ID, "--turn", "t-rl", "--", "hi")
	if err == nil {
		t.Fatal("rate-limited turn returned no error")
	}
	turn := parseJournaledTurn(t, out)
	if p := turn.finished(t); p.Status != chatevents.StatusFailed || p.Code != "" || !strings.Contains(p.Reason, final) {
		t.Errorf("turn.finished = %+v", p)
	}
	var got []chatevents.NoticePayload
	for _, rec := range turn.byType(chatevents.EventNotice) {
		var n chatevents.NoticePayload
		json.Unmarshal(rec.Payload, &n)
		if n.Code == "agent.sandbox" { // every turn's sandbox notice (#236), not a rate-limit one
			continue
		}
		got = append(got, n)
	}
	if len(got) != 3 ||
		got[0].Code != noticeRateLimitRetry || got[0].Severity != chatevents.SeverityWarning || !strings.Contains(got[0].Message, "attempt 2/3") ||
		got[1].Code != noticeRateLimitRetry || !strings.Contains(got[1].Message, "attempt 3/3") ||
		got[2].Code != noticeRateLimited || got[2].Severity != chatevents.SeverityError || got[2].Message != final {
		t.Errorf("notices = %+v", got)
	}

	out, err = runChatCmd(t, dbPath, bin, "--conversation", conv.ID, "--turn", "t-next", "--", "again")
	if err != nil {
		t.Fatalf("next turn after a rate limit: %v\n%s", err, out)
	}
	if p := parseJournaledTurn(t, out).finished(t); p.Status != chatevents.StatusCompleted {
		t.Errorf("next turn.finished = %+v", p)
	}
}
