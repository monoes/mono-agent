# Support

Where to take what, so the issue tracker stays a quality bug/task list
instead of mixing in questions, discussion, and support requests.

## Questions, ideas, "is this the right tool for X"

Use [GitHub Discussions](https://github.com/monoes/mono-agent/discussions):

- **Q&A** — usage questions ("how do I…", "why doesn't X work the way I
  expect")
- **Ideas** — feature ideas and discussion before they're concrete enough
  to become a tracked issue
- **Show and tell** — workflows/templates/setups you built
- **General** — anything else community-shaped

Discussions was chosen over a chat server (Discord/Slack) deliberately:
zero moderation infrastructure to maintain, indexed by search engines and
GitHub's own search, and it lives next to the code instead of rotting in an
unsearchable chat history. This is a pre-1.0, single-maintainer project —
a realtime chat venue would need moderation bandwidth that doesn't exist
yet. This can be revisited if the project outgrows Discussions.

Not sure Mono Agent is even the right tool? Read
[docs/COMPARISON.md](docs/COMPARISON.md) first — an honest comparison
against n8n, Activepieces, Windmill, and Node-RED, including when to use
n8n instead.

## Bugs and well-scoped feature requests

Use the [issue tracker](https://github.com/monoes/mono-agent/issues) with
the bug report or feature request template. A good bug report includes
reproduction steps, `monoagentcli version` output, and (if relevant) the
workflow/action JSON that triggers it — see `.github/ISSUE_TEMPLATE/`.

If you're not sure whether something is a bug or a question, start in
Discussions — it's easy to convert a Discussion into an issue once it's
concrete, harder to go the other way.

## Security vulnerabilities

**Never** file a public issue or Discussion for a security vulnerability.
See [SECURITY.md](SECURITY.md) for private reporting instructions
(GitHub private vulnerability reporting, preferred, or email).

## The `monoes_apis` backend

Out of scope for this repo's support channels. Mono Agent's core (the
`monoagentcli` binary, the workflow engine, the CLI/MCP surface, the Wails
GUI, the Chrome extension bridge) is entirely local-first and does not
depend on any hosted backend — see SECURITY.md's "Telemetry and crash
reporting" section for the complete, honest list of the only situations in
which it makes network requests.
