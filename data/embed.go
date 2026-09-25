package data

import "embed"

// MigrationsFS contains all SQL migration files embedded at compile time.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS

// AutomationsFS contains the built-in browser automation packages, embedded
// at compile time as the seed set. Layout (one package per directory):
//
//	automations/<id>/automation.json        manifest
//	automations/<id>/actions/<action>.json  action definitions
//	automations/<id>/{fragments,scripts,forms,tests}/…  optional
//
// See docs/mastermind/specs/2026-09-25-browser-automation-packages-design.md.
//
//go:embed automations
var AutomationsFS embed.FS

// SkillsFS contains Claude Code skill markdown files embedded at compile time.
// These are installed to ~/.claude/skills/ by `monoagent init --claude`.
//
//go:embed skills
var SkillsFS embed.FS
