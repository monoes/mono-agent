// Package tasks is the personal task board: tasks that sit in one profile,
// move through five columns and are worked by the operator and by agents.
//
// Every rule (who may do what, claims and leases, ordering, limits) lives
// here and nowhere else; the CLI, the MCP tools, the extension bridge and the
// app are thin callers. Every method takes the profile id: there is no task
// outside a profile and no query without one.
//
// A profile's board belongs to the profile row (ON DELETE CASCADE): deleting a
// profile, or rebuilding the profiles table with foreign keys on, deletes its
// tasks, events and revision.
//
// Spec: docs/mastermind/specs/2026-10-05-task-board-design.md.
package tasks
