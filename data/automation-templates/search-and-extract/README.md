# {{name}}

Browser automation package `{{id}}` for {{startUrl}}.

Scaffolded from the `search-and-extract` template.

## Actions

- `search` (read) — type `query` into the search box, press Enter, wait
  for results (or a "No results" message), extract each `.result` (`title`,
  `url`, `snippet`) and keep the first 20 distinct links. Output: `results`.

## Tests

`tests/fixtures/search.html` is a results-page snapshot and
`tests/search.expect.json` the output expected from it. Run
`monoagentcli automation test {{id}}`.
