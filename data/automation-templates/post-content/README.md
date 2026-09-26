# {{name}}

Browser automation package `{{id}}` for {{startUrl}}.

Scaffolded from the `post-content` template.

## Actions

- `create_post` (write) — open `{{startUrl}}new`, close a consent banner
  (fragment `dismiss_banner`), type `title` and `body`, and click Publish.
  The Publish click is marked `"sideEffect": true`: `record verify` and
  safe-mode runs stop right before it. Its `until` waits for the post URL
  or a success toast, so the action only succeeds once the post exists.

Put a `core.human_in_loop` node in front of this action in workflows that
publish on your behalf.
