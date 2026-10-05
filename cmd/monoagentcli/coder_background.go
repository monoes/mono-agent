package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/ai/chatevents"
)

// execTreeEnv is the variable monomind puts on every process a full-access
// turn starts, set to a random per-turn token (monomind's
// process-tree-marker.ts).
const execTreeEnv = "MONOMIND_EXEC_TREE"

// processIdentity says what makes pid the process it is now, so a later
// stop can tell it from an unrelated process that reused the pid: its
// turn's MONOMIND_EXEC_TREE token on Linux, else its start time. "" when
// the process is gone or can't be identified.
func processIdentity(pid int) string {
	if pid <= 0 {
		return ""
	}
	if runtime.GOOS == "linux" {
		env, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
		if err != nil {
			return ""
		}
		for _, kv := range bytes.Split(env, []byte{0}) {
			if v, ok := strings.CutPrefix(string(kv), execTreeEnv+"="); ok && v != "" {
				return "tree:" + v
			}
		}
		return ""
	}
	if runtime.GOOS == "windows" {
		return ""
	}
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if s := strings.TrimSpace(string(out)); err == nil && s != "" {
		return "started:" + s
	}
	return ""
}

// shortCommand is pid's command line (processCommand), shortened for a
// notice.
func shortCommand(pid int) string {
	cmd := processCommand(pid)
	if len(cmd) > 120 {
		cmd = cmd[:117] + "..."
	}
	return cmd
}

// backgroundSettle is how long a turn's end waits for the processes its
// done event listed to exit before warning about them.
var backgroundSettle = 2 * time.Second

// stillRunning is the pids still alive once they had up to settle to exit.
func stillRunning(pids []int, settle time.Duration) []int {
	deadline := time.Now().Add(settle)
	for {
		var alive []int
		for _, pid := range pids {
			if processAlive(pid) {
				alive = append(alive, pid)
			}
		}
		if len(alive) == 0 || !time.Now().Before(deadline) {
			return alive
		}
		pids = alive
		time.Sleep(100 * time.Millisecond)
	}
}

// backgroundStop is `coder stop-background --json`.
type backgroundStop struct {
	Stopped []int `json:"stopped"`
	Gone    []int `json:"gone"`
	Refused []int `json:"refused"`
}

// stopBackground stops each recorded process that is still the same
// process: SIGTERM, then SIGKILL if it outlives grace.
func stopBackground(refs []chatevents.ProcessRef, grace time.Duration) backgroundStop {
	res := backgroundStop{Stopped: []int{}, Gone: []int{}, Refused: []int{}}
	var signalled []int
	for _, r := range refs {
		now := processIdentity(r.Pid)
		switch {
		case now == "" && !processAlive(r.Pid):
			res.Gone = append(res.Gone, r.Pid)
		case r.Identity == "" || now != r.Identity:
			res.Refused = append(res.Refused, r.Pid)
		default:
			if err := terminateProcess(r.Pid); err != nil {
				res.Refused = append(res.Refused, r.Pid)
				continue
			}
			signalled = append(signalled, r.Pid)
		}
	}
	deadline := time.Now().Add(grace)
	for _, pid := range signalled {
		for processAlive(pid) && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		if processAlive(pid) {
			_ = killProcess(pid)
		}
		res.Stopped = append(res.Stopped, pid)
	}
	return res
}

func newCoderStopBackgroundCmd(cfg *globalConfig) *cobra.Command {
	var conversationID, turnID string
	cmd := &cobra.Command{
		Use:   "stop-background",
		Short: "Stop the background processes a coder turn left running",
		Long: "Stops the processes a coder turn reported still running when it ended (its " +
			"coder.background notice). A process is only stopped while it is still the one the turn " +
			"started; one whose pid now belongs to something else is refused.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if conversationID == "" || turnID == "" {
				return errInvalidInput("--conversation and --turn are required")
			}
			store, profileID, closeStore, err := openChatHistory(cfg)
			if err != nil {
				return err
			}
			defer closeStore()
			var refs []chatevents.ProcessRef
			for after := int64(0); ; {
				page, err := store.GetEvents(conversationID, turnID, profileID, after, 1000)
				if err != nil {
					return chatStoreErr(err)
				}
				for _, ev := range page {
					var n chatevents.NoticePayload
					if ev.Type == chatevents.EventNotice && json.Unmarshal(ev.Payload, &n) == nil && n.Code == noticeCoderBackground {
						refs = append(refs, n.Processes...)
					}
				}
				if len(page) < 1000 {
					break
				}
				after = page[len(page)-1].Seq
			}
			res := stopBackground(refs, 3*time.Second)
			if cfg.JSONOutput {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(res)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "stopped %v, already gone %v, refused %v\n", res.Stopped, res.Gone, res.Refused)
			return nil
		},
	}
	cmd.Flags().StringVar(&conversationID, "conversation", "", "The coder conversation")
	cmd.Flags().StringVar(&turnID, "turn", "", "The turn that left the processes running")
	withJSONErrors(cfg, cmd)
	return cmd
}
