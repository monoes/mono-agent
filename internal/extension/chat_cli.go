package extension

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/ai/chatevents"
)

// cliChatBackend is the real ChatBackend: it runs this binary's own CLI, the
// way the desktop app's chat supervisor does (wails-app/app_chat.go). Tests
// replace exec with a fake; nothing here opens the database.
type cliChatBackend struct {
	// bin is the executable to run; "" means this one.
	bin string
}

// NewCLIChatBackend returns the backend that execs this binary's `chat`
// commands. It refuses to run unless the process is monoagentcli.
func NewCLIChatBackend() ChatBackend { return &cliChatBackend{} }

func (b *cliChatBackend) binary() (string, error) {
	if b.bin != "" {
		return b.bin, nil
	}
	bin, err := os.Executable()
	if err != nil {
		return "", Unavailable("cannot locate monoagentcli: %v", err)
	}
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(bin)), ".exe")
	if !strings.HasPrefix(base, "monoagentcli") {
		return "", Unavailable("chat needs the monoagentcli bridge (running as %s)", filepath.Base(bin))
	}
	return bin, nil
}

// chatBase is the global flags every call carries.
func chatBase(profile string) []string {
	args := []string{"--json"}
	if profile != "" {
		args = append([]string{"--profile=" + profile}, args...)
	}
	return args
}

func chatTurnArgv(s ChatTurnSpec) []string {
	args := append(chatBase(s.Profile), "chat", "--conversation="+s.Conversation, "--turn="+s.Turn,
		"--instance="+s.Instance, "--tools=monoagent", "--", s.Message)
	return args
}

// run execs the CLI to completion and returns stdout.
func (b *cliChatBackend) run(ctx context.Context, args ...string) ([]byte, error) {
	bin, err := b.binary()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = monomindWaitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, &RequestError{Code: CodeTimeout, Err: ctx.Err()}
		}
		return nil, cliFailure(stdout.Bytes(), stderr.String(), err)
	}
	return stdout.Bytes(), nil
}

// cliFailure explains a failed call, keeping the CLI's own message.
func cliFailure(stdout []byte, stderr string, err error) error {
	var refusal struct{ Error, Code string }
	_ = json.Unmarshal(lastJSONLine(stdout), &refusal)
	msg := refusal.Error
	if msg == "" {
		msg = strings.TrimSpace(stderr)
	}
	if len(msg) > stderrTail {
		msg = "…" + msg[len(msg)-stderrTail:]
	}
	if msg == "" {
		msg = err.Error()
	}
	code := refusal.Code
	if code == "" && strings.Contains(msg, "active turn") {
		code = CodeBusy
	}
	if code == "" {
		return errors.New(msg)
	}
	return &RequestError{Code: code, Err: errors.New(msg)}
}

func lastJSONLine(b []byte) []byte {
	lines := bytes.Split(bytes.TrimSpace(b), []byte("\n"))
	return lines[len(lines)-1]
}

func (b *cliChatBackend) CreateConversation(ctx context.Context, profile, runtime, model string) (string, error) {
	args := append(chatBase(profile), "chat", "history", "create", "--runtime="+runtime, "--workflow=general")
	if model != "" {
		args = append(args, "--model="+model)
	}
	out, err := b.run(ctx, args...)
	if err != nil {
		return "", err
	}
	var conv struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(out, &conv) != nil || conv.ID == "" {
		return "", errors.New("chat history create returned no conversation id")
	}
	return conv.ID, nil
}

func (b *cliChatBackend) RunTurn(ctx context.Context, spec ChatTurnSpec, onEvent func(ChatEvent)) error {
	bin, err := b.binary()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, bin, chatTurnArgv(spec)...)
	cmd.WaitDelay = monomindWaitDelay
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start monoagentcli: %w", err)
	}
	finished, admitted, refusal := readTurn(stdout, onEvent)
	waitErr := cmd.Wait()

	if ctx.Err() != nil {
		if admitted && !finished {
			b.finish(spec, chatevents.StatusCancelled, "stopped from the side panel")
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &RequestError{Code: CodeTimeout, Err: ctx.Err()}
		}
		return &RequestError{Code: "cancelled", Err: errors.New("the turn was stopped")}
	}
	if !admitted {
		return cliFailure(refusal, stderr.String(), orExit(waitErr))
	}
	if !finished {
		reason := "monoagentcli ended without finishing the turn"
		if waitErr != nil {
			reason = "monoagentcli exited: " + waitErr.Error()
		}
		b.finish(spec, chatevents.StatusFailed, reason)
		return errors.New(reason)
	}
	return nil
}

func orExit(err error) error {
	if err != nil {
		return err
	}
	return errors.New("monoagentcli exited before starting the turn")
}

// readTurn reads the admission line then the event lines. It returns whether
// turn.finished was seen, whether the turn was admitted, and, when it was
// not, the raw first line (the {"error","code"} refusal).
func readTurn(r interface{ Read([]byte) (int, error) }, onEvent func(ChatEvent)) (finished, admitted bool, first []byte) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		if !admitted {
			var adm struct {
				Admitted bool `json:"admitted"`
				Existed  bool `json:"existed"`
				Turn     struct {
					ID string `json:"id"`
				} `json:"turn"`
			}
			if json.Unmarshal(line, &adm) != nil || adm.Turn.ID == "" {
				return false, false, append([]byte(nil), line...)
			}
			if adm.Existed {
				return false, false, []byte(`{"error":"that turn already ran"}`)
			}
			admitted = true
			continue
		}
		var rec chatevents.Record
		if json.Unmarshal(line, &rec) != nil || rec.Type == "" {
			continue
		}
		if rec.Type == chatevents.EventTurnFinished {
			finished = true
		}
		onEvent(ChatEvent{Seq: rec.Seq, Type: string(rec.Type), Payload: rec.Payload})
	}
	return finished, admitted, first
}

// finish records a turn whose process ended without its own turn.finished.
func (b *cliChatBackend) finish(spec ChatTurnSpec, st chatevents.TurnStatus, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, _ = b.run(ctx, append(chatBase(spec.Profile), "chat", "history", "finish",
		"--status="+string(st), "--reason="+reason, "--", spec.Conversation, spec.Turn)...)
}

func (b *cliChatBackend) ListEvents(ctx context.Context, profile, conversation, turn string, afterSeq int64) (ChatEventsPage, error) {
	if turn == "" {
		out, err := b.run(ctx, append(chatBase(profile), "chat", "history", "turns", "--limit=1", "--", conversation)...)
		if err != nil {
			return ChatEventsPage{}, err
		}
		var turns struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		}
		if json.Unmarshal(out, &turns) != nil {
			return ChatEventsPage{}, errors.New("chat history turns returned unreadable output")
		}
		if len(turns.Items) == 0 {
			return ChatEventsPage{Events: []ChatEvent{}}, nil
		}
		turn = turns.Items[0].ID
	}
	out, err := b.run(ctx, append(chatBase(profile), "chat", "history", "events",
		"--after-seq="+strconv.FormatInt(afterSeq, 10), "--", conversation, turn)...)
	if err != nil {
		return ChatEventsPage{}, err
	}
	var page struct {
		Items []chatevents.Record `json:"items"`
		Turn  struct {
			Status string `json:"status"`
		} `json:"turn"`
	}
	if json.Unmarshal(out, &page) != nil {
		return ChatEventsPage{}, errors.New("chat history events returned unreadable output")
	}
	res := ChatEventsPage{Turn: turn, Events: make([]ChatEvent, 0, len(page.Items)), TurnActive: page.Turn.Status == "active"}
	for _, r := range page.Items {
		res.Events = append(res.Events, ChatEvent{Seq: r.Seq, Type: string(r.Type), Payload: r.Payload})
	}
	return res, nil
}
