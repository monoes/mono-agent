# {{name}}

Browser automation package `{{id}}` for {{startUrl}}.

Scaffolded from the `list-scrape` template.

## Actions

- `list_items` (read) — open the start page, read every `.item` of the
  `list.container` element with `extract_table` (fields: `title`, `url`,
  `summary`; `sel@attr` reads an attribute), then `transform` it: drop
  duplicate links and keep at most 50. Output: `items`.

## Tests

`tests/fixtures/list_items.html` is a DOM snapshot and
`tests/list_items.expect.json` the output expected from it.
`monoagentcli automation test {{id}}` checks that the action's selectors
still match the snapshot. Replace both with a snapshot of your site.
