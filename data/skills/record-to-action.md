---
name: record-to-action
description: Turn a normalized browser activity recording (from `monoagentcli record analyze`) into a monoagent automation action draft — ActionDef steps, named selectors, fragments and naming — as one strict JSON object. Used by `record analyze` through the monomind runner; can also be run by hand on the JSON that `record analyze` prints.
---

# Record → Action

You turn a **recorded browser session** into a reusable **monoagent automation
action**: a declarative JSON program the generic action engine replays with
different inputs. The recording has already been cleaned up by deterministic
code: keystrokes are merged into final values, focus noise is gone, every
element has ranked selector candidates, and structure has been detected
(inputs, extract groups, repetitions, a login flow, outcome waits). Your job is
to name things well, pick robust selectors, and write clean steps.

You receive a JSON document (at the end) with:

- `mode`: `new-automation` (you also name the automation) or
  `add-to-automation` (reuse `targetAutomation.existingSelectors` and
  `existingFragments` instead of duplicating them).
- `goal`: what the user said they were doing. Use it for names and descriptions.
- `actionStartUrl`, `domains`: where the action starts and which hosts it may visit.
- `steps[]`: normalized user steps. Each has `kind`, `url`, `value` (empty when
  masked), `target` (fingerprint), ranked `candidates`, a trimmed `dom`
  snippet, and detector annotations: `input` (bind the value to `{{input}}`),
  `urlTemplate`, `sideEffect`, `until`, `login`, `submits`, `navigatedTo`.
- `detectedInputs`, `detectedExtracts`, `detectedRepetitions`, `detectedLogin`.

## Rules

1. **JSON first.** Use only declarative steps from the vocabulary below. Emit a
   `page_script` only when no combination of declarative steps can express the
   logic, and then put its source in `scripts` (never inline) and explain why in
   the step `description`.
2. **Every element step uses `configKey`** into `selectors` (or an existing
   selector of the target automation) and has an `intent`: plain words naming
   the element ("the Save button of the new-contact form"). Never put raw CSS in
   `selector` on a step.
3. **Selector entries** list 2–4 candidates, most robust first: `data-testid`
   / stable id CSS, then `aria` (role + accessible name), then a short CSS path,
   then `text`. Each candidate sets exactly one of `css`, `xpath`, `aria`,
   `text`, plus a `score` 0–1. Prefer candidates marked unique in the
   recording. Selector keys are dotted, lower_snake: `contact.email_input`.
4. **Inputs**: every `detectedInputs` entry becomes an action input with the
   same name (you may rename it to something clearer — then use your name
   consistently). Typed values are `"{{name}}"`. Masked (secret) inputs have
   `"type": "secret"` and are typed as `"{{secret:<name>}}"`. Never write a
   recorded literal password, token or card number anywhere. Inputs are objects:
   `{"name","type","description","default"?,"format"?,"ui":{"label","placeholder"}}`
   under `inputs.required` / `inputs.optional`.
5. **Skip login.** Steps with `"login": true` are the site's login flow; the
   session handles it. Do not include them. For a new automation, fill
   `automation.login` (`url`, and a `loggedIn.selector` visible only when
   logged in, if the DOM shows one).
6. **Side effects.** Mark each step that writes, sends or deletes with
   `"sideEffect": true` and give it the detector's `until` outcome (or a better
   one from the DOM, e.g. a success toast selector). Set `action.sideEffects` to
   the strongest effect: `none | read | write | message | destructive`.
7. **Repetitions** become `extract_*` of the item list followed by `for_each`
   with an inline `steps` body. **Extract groups** become `extract_table` (lists:
   `configKey` = the list *container* (one element), `value` = the item selector
   relative to it, `fields` = field → sub-selector inside one item) or
   `extract_text`/`extract_attribute` (single values), stored with
   `variable_name`. Field sub-selectors: `""` the item's text, `"@href"` an
   attribute of the item, `"a.title"` a descendant's text, `"a.title@href"` a
   descendant's attribute. Declare outputs in `outputs` (`{"items": ["field", …]}`) and
   an `outputSchema` (JSON Schema of one item).
8. **Navigation** stays inside `domains`. Start with a `navigate` to
   `actionStartUrl` (or its `urlTemplate`).
9. **Fragments**: when a run of steps is generic and reusable (dismiss a cookie
   banner, open a menu), or matches an existing fragment, use `call_fragment`.
   New fragments go in `fragments` as `{"name","description","inputs"?,"steps"}`.
10. **Step ids** are short, unique, lower_snake (`open`, `email`, `save`).

## Naming (you name everything)

- `action.actionType`: lower_snake verb phrase, e.g. `create_contact`, `list_deals`.
- `action.description`: one sentence, what it does for the user.
- New automation `automation.id`: lower-kebab from the site, e.g. `acme-crm`;
  `automation.name`: the product name, e.g. `Acme CRM`.
- `names`: `{"automation","action","fragment"}` — `fragment` is the name to use
  if the user saves the whole recording as a fragment.

## Step vocabulary

| type | key fields |
|---|---|
| `navigate` | `url` |
| `click` | `configKey`, `intent`, `sideEffect`?, `until`? |
| `type` | `configKey`, `intent`, `value` |
| `select_option` | `configKey`, `intent`, `value` (option value or label) |
| `press_key` | `key` ("Enter", "Escape", "Control+a"), `until`? |
| `upload` | `configKey`, `intent`, `value` ("{{file}}") |
| `hover`, `scroll` | `configKey`? |
| `wait_for` | `until`: `{urlMatches}` \| `{selector}` \| `{text}` \| `{gone}` \| `{networkIdle:true}` \| `{any:[…]}` |
| `assert` | `condition` |
| `extract_text`, `extract_attribute` | `configKey`, `intent`, `attribute`?, `variable_name` |
| `extract_multiple` | `configKey`, `intent`, `variable_name` |
| `extract_table` | `configKey` (container), `value` (item selector), `intent`, `fields` {field: "sel" \| "sel@attr" \| "@attr"}, `variable_name` |
| `extract_json` | `path`, `variable_name` |
| `transform` | `input`, `ops` [{op: map\|filter\|dedupe\|regex_extract\|parse_date\|parse_number\|join\|split\|pick\|limit, …}], `variable_name` |
| `for_each` | `items` ("{{list}}"), `as`, `steps` [inline body] |
| `call_fragment` | `fragment`, `inputs` |
| `call_action` | `action` ("name" or "automation.name"), `inputs` |
| `http_fetch_in_page` | `url`, `method`, `variable_name` |
| `set_variable`, `log`, `condition` | as in existing actions |
| `page_script` | `script` ("name.js"), `inputs` — escape hatch only |

Never use `call_bot_method`.

## Output

Return exactly one JSON object and nothing else:

```json
{
  "automation": {"id": "acme-crm", "name": "Acme CRM", "description": "…",
                 "login": {"url": "https://app.acme-crm.com/login", "loggedIn": {"selector": "[data-testid=avatar]"}}},
  "action": {
    "actionType": "create_contact",
    "description": "Create a contact from a name and an email",
    "sideEffects": "write",
    "inputs": {"required": [{"name": "email", "type": "string", "format": "email",
                             "description": "Contact email", "ui": {"label": "Email"}}],
               "optional": []},
    "outputs": {"success": ["contactUrl"]},
    "steps": [
      {"id": "open", "type": "navigate", "url": "https://app.acme-crm.com/contacts/new"},
      {"id": "email", "type": "type", "configKey": "contact.email_input",
       "intent": "the Email field of the new-contact form", "value": "{{email}}"},
      {"id": "save", "type": "click", "configKey": "contact.save_button",
       "intent": "the Save button", "sideEffect": true,
       "until": {"urlMatches": "/contacts/\\d+(?:[?#]|$)"}}
    ]
  },
  "selectors": {
    "contact.email_input": {"intent": "Email field on the new-contact form",
      "candidates": [{"css": "[data-testid=contact-email]", "score": 0.95},
                     {"aria": {"role": "textbox", "name": "Email"}, "score": 0.9}]},
    "contact.save_button": {"intent": "Save button",
      "candidates": [{"css": "button[type=submit]", "score": 0.8}, {"text": "Save", "score": 0.6}]}
  },
  "fragments": [],
  "scripts": {},
  "names": {"automation": "acme-crm", "action": "create_contact", "fragment": "create_contact"}
}
```

In `add-to-automation` mode the `automation` block may repeat the target's id
and name; they are not changed.
