package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/monomind"
)

var (
	workspaceAdjectives = []string{"amber", "brisk", "calm", "clever", "crisp", "daring", "eager", "fuzzy", "gentle", "glad",
		"golden", "humble", "jolly", "keen", "lively", "lucky", "mellow", "nimble", "plucky", "quiet",
		"rapid", "rosy", "sunny", "swift", "tidy", "vivid", "witty", "zesty"}
	workspaceNouns = []string{"otter", "falcon", "maple", "comet", "badger", "heron", "cedar", "pebble", "lynx", "orchid",
		"harbor", "meadow", "walrus", "quartz", "sparrow", "willow", "panda", "ember", "fjord", "koala",
		"lagoon", "nebula", "raven", "tulip", "yak", "zephyr"}
)

func pick(words []string) string {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(words))))
	if err != nil {
		return words[0]
	}
	return words[n.Int64()]
}

// coderWorkspace is `coder workspace new --json`.
type coderWorkspace struct {
	Path    string                  `json:"path"`
	Created bool                    `json:"created"`
	Git     bool                    `json:"git"`
	Init    *monomind.WorkspaceInit `json:"init,omitempty"`
}

// initWorkspace sets a folder up for coder turns; a var so tests can skip
// the monomind subprocess.
var initWorkspace = monomind.InitWorkspace

// newCoderWorkspace creates a fresh, randomly named folder under root
// (<yyyymmdd>-<adjective>-<noun>), git-inits it so every change the agent
// makes can be reviewed and reverted, and initializes it as a monomind
// project.
func newCoderWorkspace(ctx context.Context, root string) (*coderWorkspace, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("creating workspace root: %w", err)
	}
	day := time.Now().Format("20060102")
	var dir string
	for i := 0; ; i++ {
		name := fmt.Sprintf("%s-%s-%s", day, pick(workspaceAdjectives), pick(workspaceNouns))
		if i >= 20 {
			name = fmt.Sprintf("%s-%d", name, time.Now().UnixNano()%100000)
		}
		dir = filepath.Join(root, name)
		err := os.Mkdir(dir, 0o755)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrExist) || i > 25 {
			return nil, fmt.Errorf("creating workspace: %w", err)
		}
	}
	ws := &coderWorkspace{Path: dir, Created: true}
	if git, err := exec.LookPath("git"); err == nil {
		ws.Git = exec.CommandContext(ctx, git, "init", "-q", dir).Run() == nil
	}
	res, err := initWorkspace(ctx, "", dir)
	if err != nil {
		return ws, fmt.Errorf("workspace %s created but not initialized: %w", dir, err)
	}
	ws.Init = res
	return ws, nil
}

// resolveCoderCwd validates a user-picked folder: any existing directory,
// made absolute with symlinks resolved, so resuming always finds the same
// Claude Code session folder.
func resolveCoderCwd(p string) (string, error) {
	abs, err := filepath.Abs(expandHome(p))
	if err != nil {
		return "", errInvalidInput("--cwd: %v", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", errInvalidInput("--cwd %s: %v", p, err)
	}
	fi, err := os.Stat(real)
	if err != nil || !fi.IsDir() {
		return "", errInvalidInput("--cwd %s is not a folder", p)
	}
	return real, nil
}

func newCoderWorkspaceCmd(cfg *globalConfig) *cobra.Command {
	cmd := &cobra.Command{Use: "workspace", Short: "Coder mode working folders"}

	var root string
	newCmd := &cobra.Command{
		Use:   "new",
		Short: "Create a fresh, randomly named test folder and initialize it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.Close()
			s, err := requireCoderReady(cmd, db.DB)
			if err != nil {
				return err
			}
			if root == "" {
				root = s.WorkspaceRoot
			}
			ws, err := newCoderWorkspace(cmd.Context(), expandHome(root))
			if err != nil {
				return err
			}
			if cfg.JSONOutput {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(ws)
			}
			fmt.Fprintln(cmd.OutOrStdout(), ws.Path)
			return nil
		},
	}
	newCmd.Flags().StringVar(&root, "root", "", "Create it here instead of the configured workspace root")
	withJSONErrors(cfg, newCmd)

	var limit int
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List folders coder conversations have used, most recent first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, profileID, closeStore, err := openChatHistory(cfg)
			if err != nil {
				return err
			}
			defer closeStore()
			list, err := store.ListCoderWorkspaces(profileID, limit)
			if err != nil {
				return err
			}
			type item struct {
				Path          string `json:"path"`
				LastUsed      string `json:"lastUsed"`
				Conversations int    `json:"conversations"`
				Exists        bool   `json:"exists"`
			}
			out := make([]item, 0, len(list))
			for _, w := range list {
				fi, err := os.Stat(w.Path)
				out = append(out, item{w.Path, w.LastUsed, w.Conversations, err == nil && fi.IsDir()})
			}
			if cfg.JSONOutput {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
			}
			for _, w := range out {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%d\n", w.Path, w.LastUsed, w.Conversations)
			}
			return nil
		},
	}
	listCmd.Flags().IntVar(&limit, "limit", 20, "Maximum folders to list")
	withJSONErrors(cfg, listCmd)

	cmd.AddCommand(newCmd, listCmd)
	return cmd
}
