package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/orgdesign"
)

// newOrgLegacyCmd surfaces orgs left in the CLI's old default root
// (~/.monoagent/.monomind/orgs) now that org commands resolve the active
// profile's folder (C-31).
func newOrgLegacyCmd(env *orgEnv) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "legacy",
		Short: "Find and move orgs left in the old CLI default folder (~/.monoagent)",
	}
	cmd.AddCommand(newOrgLegacyListCmd(env), newOrgLegacyMoveCmd(env))
	return cmd
}

func legacyRoot() string { return expandPath(legacyOrgProjectRoot) }

func newOrgLegacyListCmd(env *orgEnv) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List orgs in the legacy folder",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			legacy := legacyRoot()
			names := []string{}
			if filepath.Clean(legacy) != filepath.Clean(env.Root()) {
				found, err := orgdesign.ListOrgNames(legacy)
				if err != nil {
					return err
				}
				names = append(names, found...)
			}
			return printJSONValue(map[string]interface{}{"v": 1, "legacy_root": legacy, "orgs": names})
		},
	}
}

func newOrgLegacyMoveCmd(env *orgEnv) *cobra.Command {
	return &cobra.Command{
		Use:   "move <org>",
		Short: "Move an org (config, run data, and state files) from the legacy folder into the active profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !orgdesign.ValidOrgName(name) {
				return fmt.Errorf("invalid org name %q", name)
			}
			_, _, root, err := env.Profile()
			if err != nil {
				return err
			}
			legacy := legacyRoot()
			if filepath.Clean(legacy) == filepath.Clean(root) {
				return fmt.Errorf("the active profile already uses %s", legacy)
			}
			srcDir, dstDir := orgdesign.OrgsDir(legacy), orgdesign.OrgsDir(root)
			if _, err := os.Stat(filepath.Join(srcDir, name+".json")); err != nil {
				return fmt.Errorf("org %q is not in %s", name, srcDir)
			}
			if _, err := os.Stat(filepath.Join(dstDir, name+".json")); err == nil {
				return fmt.Errorf("org %q already exists in %s; rename one of them first", name, dstDir)
			}
			if running, _ := legacyOrgRunning(cmd, legacy, name); running {
				return fmt.Errorf("org %q is running from %s; stop it before moving", name, legacy)
			}
			moves, err := legacyOrgPaths(srcDir, name)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(dstDir, 0o755); err != nil {
				return err
			}
			for _, base := range moves {
				dst := filepath.Join(dstDir, base)
				if _, err := os.Lstat(dst); err == nil {
					return fmt.Errorf("%s already exists; nothing was moved", dst)
				}
			}
			for _, base := range moves {
				if err := os.Rename(filepath.Join(srcDir, base), filepath.Join(dstDir, base)); err != nil {
					return fmt.Errorf("moving %s: %w (earlier files in this move are already in %s)", base, err, dstDir)
				}
			}
			return printJSONValue(map[string]interface{}{"v": 1, "org": name, "moved_to": root, "files": moves})
		},
	}
}

// legacyOrgPaths lists the config, its run-data directory, and monomind's
// per-org state files (<name>-state.json, ...) — everything that makes up
// the org on disk.
func legacyOrgPaths(dir, name string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		switch {
		case n == name+".json", n == name && e.IsDir():
			out = append(out, n)
		case orgdesign.IsOrgArtifactFile(name, n):
			out = append(out, n)
		}
	}
	return out, nil
}

// legacyOrgRunning asks monomind whether the org is running; unknown counts
// as not running only when monomind is unavailable.
func legacyOrgRunning(cmd *cobra.Command, root, name string) (bool, error) {
	out, err := monomind.OrgStatus(cmd.Context(), root, name)
	if err != nil {
		return false, err
	}
	var st struct {
		Status string `json:"status"`
		Items  []struct {
			Status string `json:"status"`
		} `json:"items"`
	}
	if json.Unmarshal(out, &st) != nil {
		return false, nil
	}
	if st.Status == "running" {
		return true, nil
	}
	for _, it := range st.Items {
		if it.Status == "running" {
			return true, nil
		}
	}
	return false, nil
}
