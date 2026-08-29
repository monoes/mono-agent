# Labels

Small taxonomy on purpose: enough to route work, not enough to become a
second status system (GitHub's open/closed states and the CHANGELOG do that
job). Two axes — `area:` says *where* in the codebase, `priority:` says
*how urgent* — plus GitHub's defaults for triage verbs.

## Area

| Label | Description |
|---|---|
| `area:engine` | Workflow engine, node implementations, scheduler/daemon, execution semantics. |
| `area:docs` | README, AGENTS.md, `ref` manual content, docs/, templates' wording. |
| `area:security` | Secrets vault, keyring handling, crash reporting, sandboxing, SECURITY.md surface. |
| `area:gui` | Wails desktop app (`wails-app/`) and its bridge to the CLI. |

## Priority

| Label | Description |
|---|---|
| `priority:P1` | Data loss, security exposure, or broken core commands — fix before the next release. |
| `priority:P2` | Real pain, bounded scope — schedule normally. |

## GitHub defaults (retained)

| Label | Description |
|---|---|
| `bug` | Something isn't working (default). |
| `enhancement` | New feature or request (default). |
| `documentation` | Docs improvements (default). |
| `good first issue` | Good for newcomers (default). |
| `help wanted` | Extra attention is needed (default). |
| `duplicate` | This issue or pull request already exists (default). |
| `question` | Further information is requested (default). |
| `invalid` | This doesn't seem right (default). |
| `wontfix` | This will not be worked on (default). |

## Sync

Apply the non-default labels to a repo (idempotent via `--force`; the
GitHub defaults already exist on every repo):

```bash
gh label create 'area:engine'   --color 0E8A16 --description 'Workflow engine, node implementations, scheduler/daemon, execution semantics' --force
gh label create 'area:docs'     --color 0075CA --description 'README, AGENTS.md, ref manual content, docs/, templates wording' --force
gh label create 'area:security' --color B60205 --description 'Secrets vault, keyring handling, crash reporting, sandboxing, SECURITY.md surface' --force
gh label create 'area:gui'      --color 5319E7 --description 'Wails desktop app (wails-app/) and its bridge to the CLI' --force
gh label create 'priority:P1'   --color D93F0B --description 'Data loss, security exposure, or broken core commands — fix before the next release' --force
gh label create 'priority:P2'   --color FBCA04 --description 'Real pain, bounded scope — schedule normally' --force
```
