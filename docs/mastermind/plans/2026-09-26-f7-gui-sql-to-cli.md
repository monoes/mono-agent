# F7: Move the Desktop App's Remaining Database Access Behind the CLI

**Goal:** every desktop-app binding that reads or writes SQLite (or calls an internal store directly) shells out to `monoagentcli … --json` instead, the same way the dashboard, HIL, messages and application bindings already do.

**Why:** in mono-agent, functionality lives in `monoagentcli`, and the GUI only renders and shells out (memory *ui-calls-cli-for-functionality*). Logic that lives only in the GUI can't be tested from a terminal, can't be scripted, and drifts away from the CLI's version of the same idea. The dashboard follow-ups already found real bugs of exactly this kind: profile scoping, and an unverified update download.

**Stacked on:** `feat/dashboard-followups-2` (PR #166).

## Rules for every area

1. **CLI first.** If no command returns what the binding needs, add or extend a `monoagentcli` command with `--json` output: snake_case keys, `[]` rather than `null`, and the exit codes from `cmd/monoagentcli/exitcodes.go` (2 = not found, 3 = invalid input). Test it with a seeded, migrated DB (see `cmd/monoagentcli/summary_test.go` and `people_messages_read_test.go`).
2. **Same binding surface.** Keep every binding's **name, parameters and returned JSON shape** exactly as they are, so the frontend and the generated `wailsjs` don't change. If the old shape is awkward, adapt it in the binding. If a change is truly unavoidable, report it; don't edit the frontend.
3. **Shell out with the existing helpers.**
   - `runMonoCLI(stdin, &result, args...)` in `app_applications.go` decodes typed results. It adds `--profile <active> --json`.
   - `rawCLI(timeout, args...)` in `app_summary.go` returns raw JSON.
   - `cliJSON(timeout, &result, args...)` gives typed results with a deadline, for polled bindings.
4. **Test each binding** with a fake CLI (`fakeCLI(t, body)` in `app_health_stream_test.go`, plus `loggedArgs`) and assert the exact argv. Shell-script fakes are unix-only, so put `//go:build !windows` on those test files.
5. **Delete the SQL** from the binding once the CLI path works. Don't leave dead helpers behind.
6. **Scope.** Touch only the files your area owns (below). You may add new files. If you need a change in a file another area owns, don't make it: report it.
7. **Verify before reporting done:**
   - `gofmt -l` prints nothing, and `go vet ./...` passes.
   - `go test ./...` passes; state the package count.
   - `cd wails-app && go vet -tags webkit2_41 . && go test -tags webkit2_41 ./...` passes.
   - `cd wails-app/frontend && npx vitest run` passes (the frontend must be unaffected). Run `npm ci && npm run build` first; the Go build embeds `frontend/dist`.
8. **Commits:** conventional style, one per coherent step, and always `git commit -F - -- <exact paths>`. Never amend, rebase or push. No Co-Authored-By lines or other attribution trailers.
9. **Scratch space** goes under `~/scratch/`, never `/tmp`. Never touch `~/.monoagent`, the real HOME, or ports 9222/9322. Tests use `t.TempDir()` and `t.Setenv("HOME", …)`.

## Areas

| Area | Branch / worktree | Bindings (current file) | Owns | Likely CLI work |
|---|---|---|---|---|
| **A. People & CRM** | `feat/f7-people` · `~/scratch/wt-f7-people` | `app_people.go`: GetPeople, GetPeopleCount, GetPersonDetail, GetPersonInteractions, GetPersonPosts, GetPostDetail, GetPostComments, AddPersonMessage, ComposePersonMessage, GetDraftPersonMessages, SendDraftPersonMessage, RejectDraftPersonMessage, AddPersonStatus, GetLatestPersonStatus, GetPersonStatusHistory. `app.go`: GetAllTags, GetPersonTags, AddPersonTag, UpdateTagColor, RemovePersonTag, GetPeopleTagsMap, GetSocialLists | `wails-app/app_people.go`, and the tag and social-list functions in `wails-app/app.go`; new `wails-app/app_people_*.go` / `app_tags.go`; `cmd/monoagentcli/people*.go`, `list.go`; new cmd files | Most commands already exist (`people list/get`, `people tag …`, `people status …`, `people messages …`, `list ls`). Missing ones are probably a people count, posts/comments and the tags map. |
| **B. Image vault** | `feat/f7-images` · `~/scratch/wt-f7-images` | `app_vault.go`: GetVaultImages, GetVaultImage, GetVaultImageData, AddVaultImage, SaveVaultImageToFile, UpdateVaultImageLabel, DeleteVaultImage, SearchVaultImages, GetVaultStats | the image functions in `wails-app/app_vault.go` (not the secret ones, which already use the CLI); new `cmd/monoagentcli/vault_image*.go` | There is no CLI yet. Add `monoagentcli image list/get/data/add/label/delete/search/stats/export`, keeping the `images:changed` event emits in the binding. |
| **C. Workflows** | `feat/f7-workflows` · `~/scratch/wt-f7-workflows` | `app_workflows.go`: GetWorkflow, SaveWorkflow, DeleteWorkflow, ExportWorkflow, SetWorkflowActive, GetWorkflowExecutions, GetExecutionDetail, CancelWorkflow, RunWorkflowWithInput, and ListWorkflows (reads the store) | `wails-app/app_workflows.go`; new `wails-app/app_workflows_*.go`; `cmd/monoagentcli/workflow*.go`, `execution_json.go` | These exist: `workflow get/delete/export/activate/deactivate/executions/run`. Probably missing: a save/update of a full workflow document from the editor, `workflow cancel <exec-id>` (which must keep the refusal to kill the daemon's own PID), and `workflow execution <id>` (detail). |
| **D. Sessions & connections** | `feat/f7-sessions` · `~/scratch/wt-f7-sessions` | `app.go`: GetSessions, TestSession, DeleteSession. `app_connections.go`: ListConnections, TestConnection, RemoveConnection, SaveConnectionDirect, GetOAuthCredentials, SetOAuthCredentials, ListCredentialsForNode | the session functions in `wails-app/app.go`, `wails-app/app_connections.go`; `cmd/monoagentcli/login*.go`, `connect.go`, new cmd files | `login status --json` (now snake_case) and `connect list/test/remove` exist. Probably missing: a session delete/test by id, direct save of a connection, get/set of OAuth app credentials (secrets must never be printed unless explicitly requested), and credentials for a node. |
| **E. Profiles & misc** | `feat/f7-profiles` · `~/scratch/wt-f7-profiles` | `app.go`: GetProfiles, CreateProfile, SwitchProfile, GetActiveProfile, MoveProfileFolder, RevealProfileFolder, GetTemplates, IsReady. `app_orgs_design.go`: ReloadOrg. `app_nodes.go`: GetWorkflowNodeTypes. `app_monomind_projects.go`: ListMonomindProjects. `app_documents.go`: GetProfileDocument. `app_capture_view.go`: GetCaptureView | the profile, template and IsReady functions in `wails-app/app.go`; the other files listed; `cmd/monoagentcli/profile*.go`, `template*.go`, new cmd files | `profile list/current/switch/create` exist. Note that SwitchProfile also restarts the app's own watchers (UI state); only the persisted switch moves to the CLI. IsReady may legitimately stay local (a DB-open check); decide and explain. |

**Shared file:** several areas remove functions from `wails-app/app.go`. Each area deletes **only its own functions** and adds replacements in new files, so the merge conflicts are limited to the regions each area removed.

## Out of scope (for now)
- `app_ai.go`, the in-app AI provider. It is due for removal (memory *ai-backend-monomind-runner*): porting it would be wasted work. It gets its own removal task.
- `app_chat.go`, the conversation history (`aiStore`). It needs a chat-history CLI design first.
- The document and image folder watchers' reconcile writes (`app_documents_watch.go`, `app_images_watch.go`). These are background sync that should move into the daemon, not per-call CLI invocations.
- Internal helpers that read a single setting for UI state: `orgProjectRoot`, `restrictFileWriteForOrgs`, `documentPath`, `IsMonomindInitialized`. Area E decides which of these must move.

## Integration
When an area is done, the lead verifies it independently (same checks as rule 7), merges the branches one at a time into an integration branch `feat/f7` (`git -c rerere.enabled=false merge`), re-runs every suite, and opens one PR stacked on #166.
