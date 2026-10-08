> Historical session record, archived 2026-10-07. Read only when needed.
> Follow the handoff summary for current status; recorded model escalation, push restrictions and absolute scratch paths are historical, not instructions for the resumed session.

# Rules for every implementer and reviewer of the monoes.me account-gate program (read once, they bind every task)

## Authority and paths
- The plans are the requirements. The AUTHORITY for plans, the index and the spec is the docs worktree `$MONOAGENT_CHECKOUT/.claude/worktrees/feat+monoes-account-gate/docs/mastermind/` (branch feat/monoes-account-gate). Your task brief is an extract of it. The `docs/` folder inside your own code worktree is a STALE older copy (it comes from the base branch): never read it as authority.
- Work only in your lane's worktree (given in your dispatch), on its branch. Never edit another worktree. The finished B1a package `internal/account` is the base: consume its API as it is; do not change `internal/account` unless your task says so (B1b owns the package's later additions).
- The index section 3 is the frozen contract: names and signatures exactly as written. A needed contract change goes into your report under "Contract change requests", it is never made silently.

## Git
- Never push, never open a pull request, never merge, never touch master or another branch than your lane's. Never use bare `git stash`.
- Commit with TWO separate commands (the shell guard refuses chained git): `git add <files>` then `git commit -m "<type(scope): subject>" -m "<why, one short paragraph>" -m "Co-Authored-By: <your model name> <noreply@anthropic.com>"` where the trailer names the model you are (for example `Claude Opus 5.5`, `Claude Sonnet 5.5`). Conventional subjects. One `git rev-parse` per ref (it fails with several). Before every commit `git status --short` lists only the files the task names.
- Compound or long shell lines may be refused by the guard: put them in a script file under your lane's own scratch directory and run it with bash, or use the Edit/Write tools for file changes.
- `rm -r` and `rm -rf` (even inside a string) are refused: delete a file with plain `rm <file>`, a directory of your own with `find <dir> -mindepth 1 -delete` then `rmdir <dir>`.

## Tests and the machine
- Run the focused tests the task names while you work; run the task's own verification commands before you commit. Do NOT run the whole repository's `go test ./...`: it takes minutes and is red on a pristine macOS tree. Known failures on pristine master (not yours, do not investigate): cmd/monoagentcli TestCaptureTaskFilesOnTheBoard, TestCoderRootIsOneSharedFolder, TestWorkflowCancelSignalsAndMarks, TestCoderConversationFolders; internal/capturetask TestCreateAttachesEveryArtifact, TestCreateRecordsTheRealPathNotASymlink; internal/config TestGenerateConfigFailsFastWhenMonomindMissing; internal/monomind TestFindAll_ListsShadowedCopies. Load-only flakes (pass alone): internal/connections TestMigrateConnectionsToVault_SkipsRowLockedByAnotherProcess, internal/mcp TestGrantWaitTimeoutNote and TestManyUpdateCallsAtOnceAllLand, internal/dynorg TestIsolatedWritersRunInParallelInTheirOwnWorktrees, internal/monomind TestAgentTestGoDeadline (internal/monomind/agenttest_test.go:83; older notes say cmd/monoagentcli), internal/monomind TestOrgRunStartCancelStopsTheStart.
- The machine is SHARED with other Claude sessions (a web project driving 8 Chrome instances, a tasks-board lane running CLI suites): the load average has been 60 to 200 on 10 cores. Run only the focused tests the task names (no whole-package or whole-repo sweeps, no `-count=100` loops unless the brief asks), run Playwright with ONE worker and never start Chrome unless the task needs it, and expect slow runs: wait, do not retry blindly. NEVER select cmd/monoagentcli TestAutomationDoctorJSON with a loose -run pattern such as `Doc` (it hangs for minutes); it passes in a full package run.
- Use the shared default Go build cache; never create a private GOCACHE (the disk is tight). Tests use `internal/testhome` (a throwaway HOME); never run a shadow-HOME smoke that re-downloads Go caches. Several lanes run at once on 10 cores: a timing test that fails only under load is a defect of the test, report it with the evidence (the command, how it was loaded), do not retry it until it passes.
- Never print, log or put in a test's output a token, a refresh token or a key. Test fixtures use throwaway keys generated in the test.
- Files stay under 500 lines; split by responsibility as the plan says. A file that is already larger may grow by the plan's lines only.

## Process
- You never dispatch subagents (no helpers, never a reviewer). Review is the controller's job after your report.
- If the brief is unclear or contradicts what you find, ask first (NEEDS_CONTEXT) instead of guessing. If a task is too hard, say BLOCKED with specifics.
- Your report goes to the report file named in your dispatch (what you did, TDD evidence: RED command and failing output, GREEN command and passing output; files changed; concerns), and your reply is only the short status contract (under 15 lines).
- Reviewers: read-only on the checkout, report with file:line evidence, severity Critical / Important / Minor, never re-run the implementer's suites without a named doubt.
