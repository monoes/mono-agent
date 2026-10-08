package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/monoes/mono-agent/data"
	"github.com/spf13/cobra"
)

// claudeInitMarker is written after a successful first-run Claude check so we
// don't repeat the check on every subsequent invocation.
const claudeInitMarker = ".claude_init"

// claudeSkillNames are the skills distributed with monoagent. Each is embedded
// as data/skills/<name>.md and installed as ~/.claude/skills/<name>/SKILL.md
// (the folder layout Claude Code loads) so that a Claude Code session in ANY
// project — not just this repo — knows monoagent exists and how to drive it.
// record-to-action is deliberately absent: it is the prompt `record analyze`
// feeds the monomind runner, not a skill for interactive sessions.
var claudeSkillNames = []string{
	"action-template-generator",
	"monoagent-workflows",
	"monoagent-tasks",
}

// legacyClaudeSkillSHA256 are the contents earlier releases wrote as flat
// ~/.claude/skills/<name>.md files (every version of the embedded source in
// git history). A flat file is removed only when it matches one of these or
// the current embedded source; anything else is the user's and stays.
var legacyClaudeSkillSHA256 = map[string][]string{
	"action-template-generator": {"c449b34462a7c64dc790f8fa438b51ea9fd1af26c78f945c8073c34ccd956a6a"},
	"monoagent-workflows": {
		"de0cd8e93a65370fb2deea72158ae54fd6a96782a8898927f74e79b60147f37c",
		"ea17e3874149f4af46a7a467057c827b0eeb061918221d9a45915aa536b4a545",
	},
}

// claudeSkillFile is where a skill lives under skillsDir.
func claudeSkillFile(skillsDir, name string) string {
	return filepath.Join(skillsDir, name, "SKILL.md")
}

// removeLegacyClaudeSkill deletes the old flat <name>.md when it is
// byte-identical to something monoagent shipped. Best-effort.
func removeLegacyClaudeSkill(skillsDir, name string, current []byte) {
	flat := filepath.Join(skillsDir, name+".md")
	got, err := os.ReadFile(flat)
	if err != nil {
		return
	}
	sum := sha256.Sum256(got)
	hexSum := hex.EncodeToString(sum[:])
	match := bytes.Equal(got, current)
	for _, h := range legacyClaudeSkillSHA256[name] {
		if h == hexSum {
			match = true
		}
	}
	if match {
		_ = os.Remove(flat)
	}
}

// newInitCmd returns the `monoagent init` command.
func newInitCmd() *cobra.Command {
	var claudeFlag bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize monoagent integrations",
		Long: `Set up monoagent integrations.

  --claude    Install the action-template-generator Claude Code skill so
              Claude automatically knows how to crawl new websites using
              monoagent's browser automation.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !claudeFlag {
				return cmd.Help()
			}
			return installClaudeSkill(true)
		},
	}

	cmd.Flags().BoolVar(&claudeFlag, "claude", false, "Install Claude Code skill for crawling automation")
	return cmd
}

// installClaudeSkill copies the embedded skill file to ~/.claude/skills/.
// If verbose is true, it prints status messages.
func installClaudeSkill(verbose bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home dir: %w", err)
	}

	skillsDir := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return fmt.Errorf("create skills dir: %w", err)
	}

	for _, name := range claudeSkillNames {
		content, err := data.SkillsFS.ReadFile("skills/" + name + ".md")
		if err != nil {
			return fmt.Errorf("read embedded skill %s: %w", name, err)
		}

		dest := claudeSkillFile(skillsDir, name)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("create skill dir: %w", err)
		}
		if err := os.WriteFile(dest, content, 0o644); err != nil {
			return fmt.Errorf("write skill to %s: %w", dest, err)
		}
		removeLegacyClaudeSkill(skillsDir, name, content)
		if verbose {
			fmt.Printf("Claude skill installed: %s\n", dest)
		}
	}

	// Write the marker so first-run check doesn't repeat.
	monoagentDir := filepath.Join(home, ".monoagent")
	_ = os.MkdirAll(monoagentDir, 0o755)
	_ = os.WriteFile(filepath.Join(monoagentDir, claudeInitMarker), []byte("1"), 0o644)

	if verbose {
		fmt.Printf("\nIn Claude Code, run /action-template-generator to generate crawling templates.\n")
		fmt.Printf("Claude now also knows how to run monoagent workflows and templates from any project:\n")
		fmt.Printf("  monoagentcli workflow templates list\n")
	}
	return nil
}

// runClaudeFirstRunCheck installs monoagent's Claude Code skills that are
// missing, if Claude Code is detected (~/.claude/ exists). It checks the
// skills themselves rather than trusting the first-run marker, so a
// monoagent version that ships a NEW skill delivers it to existing installs
// too. It runs before every command, so it never rewrites a skill that is
// there: one the user edited, or one another monoagent binary wrote, stays
// as it is. Refreshing a stale skill is the doctor fix
// (integrations.claude_skills), which asks first. Errors are silently
// ignored — this is best-effort.
func runClaudeFirstRunCheck() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	// Detect Claude Code: ~/.claude/ directory must exist.
	claudeDir := filepath.Join(home, ".claude")
	if _, err := os.Stat(claudeDir); os.IsNotExist(err) {
		// Claude Code not installed — write marker so we don't re-check.
		marker := filepath.Join(home, ".monoagent", claudeInitMarker)
		if _, err := os.Stat(marker); err != nil {
			monoagentDir := filepath.Join(home, ".monoagent")
			_ = os.MkdirAll(monoagentDir, 0o755)
			_ = os.WriteFile(marker, []byte("0"), 0o644)
		}
		return
	}

	_, missing, _ := claudeSkillsState()
	if len(missing) == 0 {
		return
	}
	if err := installMissingClaudeSkills(missing); err != nil {
		return
	}

	fmt.Fprintf(os.Stderr, "[monoagent] Claude Code detected — monoagent skills installed.\n")
	fmt.Fprintf(os.Stderr, "[monoagent] Claude can now run: monoagentcli workflow templates list\n")
}

// claudeSkillsState compares ~/.claude/skills with the skills embedded in
// this binary. claudeFound is false when Claude Code isn't installed.
func claudeSkillsState() (claudeFound bool, missing, stale []string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, nil, nil
	}
	claudeDir := filepath.Join(home, ".claude")
	if _, err := os.Stat(claudeDir); err != nil {
		return false, nil, nil
	}
	for _, name := range claudeSkillNames {
		want, err := data.SkillsFS.ReadFile("skills/" + name + ".md")
		if err != nil {
			continue
		}
		got, err := os.ReadFile(claudeSkillFile(filepath.Join(claudeDir, "skills"), name))
		switch {
		case err != nil:
			missing = append(missing, name)
		case !bytes.Equal(got, want):
			stale = append(stale, name)
		}
	}
	return true, missing, stale
}

// installMissingClaudeSkills writes only the named skills, leaving every
// skill already in ~/.claude/skills alone.
func installMissingClaudeSkills(names []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	skillsDir := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return err
	}
	for _, name := range names {
		content, err := data.SkillsFS.ReadFile("skills/" + name + ".md")
		if err != nil {
			return err
		}
		dest := claudeSkillFile(skillsDir, name)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			continue // appeared meanwhile: it is not ours to replace
		}
		if err != nil {
			return err
		}
		_, werr := f.Write(content)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return werr
		}
		removeLegacyClaudeSkill(skillsDir, name, content)
	}
	monoagentDir := filepath.Join(home, ".monoagent")
	_ = os.MkdirAll(monoagentDir, 0o755)
	_ = os.WriteFile(filepath.Join(monoagentDir, claudeInitMarker), []byte("1"), 0o644)
	return nil
}
