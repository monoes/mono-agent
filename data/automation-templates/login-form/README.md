# {{name}}

Browser automation package `{{id}}` for {{startUrl}}.

Scaffolded from the `login-form` template.

## Actions

- `log_in` (write) — open `{{startUrl}}login`, type `username` and
  `password` (a `secret` input: resolved from the vault and masked in logs),
  press the submit button (the step marked `"sideEffect": true`, where
  verification replay stops) and wait for the account menu.

## Login block

`automation.json` → `login` tells `monoagentcli login {{id}}` which page to
open and how to recognise a logged-in session (`loggedIn.selector`,
optionally a session `cookie`). Prefer logging in by hand through
`login {{id}}` over storing a password: the session is captured once and
reused.
