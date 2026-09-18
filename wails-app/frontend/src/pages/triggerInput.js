// Trigger-input discovery for manually-run workflows.
//
// A manual-trigger workflow whose nodes read {{ $json.<field> }} needs data
// supplied at run time (the CLI's `workflow run --input`). The GUI's run
// button used to send nothing, so every such expression resolved to nothing
// and the run quietly did nothing — a Gemini image workflow finished green
// having generated no images, because its "{{ json $json.prompts }}" had
// rendered the string "null".
//
// These helpers let the editor notice that a workflow wants input and offer a
// filled-in skeleton for it.

// $json.field, $json["field"] and $json['field'].
const DOT_REF = /\$json\.([A-Za-z_][A-Za-z0-9_]*)/g;
const BRACKET_REF = /\$json\[\s*["']([^"']+)["']\s*\]/g;
// One {{ … }} expression, so a field's surrounding call is knowable.
const EXPRESSION = /\{\{([\s\S]*?)\}\}/g;

// collectTriggerFields returns the trigger fields a workflow's node configs
// reference, in first-seen order, each marked structured when it is passed
// through the `json` template function — `{{ json $json.prompts }}` wants an
// array or object, `{{ $json.topic }}` a string.
export function collectTriggerFields(nodes) {
  const found = new Map(); // name -> { name, structured }
  const note = (name, structured) => {
    const existing = found.get(name);
    if (existing) {
      existing.structured = existing.structured || structured;
      return;
    }
    found.set(name, { name, structured });
  };

  for (const text of configStrings(nodes)) {
    for (const [, body] of text.matchAll(EXPRESSION)) {
      // `json $json.x` / `toJson $json.x` — the value is a collection.
      const structured = /\bjson\s+\$json[.[]/i.test(body);
      for (const [, name] of body.matchAll(DOT_REF)) note(name, structured);
      for (const [, name] of body.matchAll(BRACKET_REF)) note(name, structured);
    }
  }
  return [...found.values()];
}

// configStrings walks every node's config and yields each string value,
// however deeply nested.
function* configStrings(nodes) {
  const walk = function* (value) {
    if (typeof value === 'string') {
      yield value;
    } else if (Array.isArray(value)) {
      for (const v of value) yield* walk(v);
    } else if (value && typeof value === 'object') {
      for (const v of Object.values(value)) yield* walk(v);
    }
  };
  for (const node of nodes || []) yield* walk(node?.config ?? node?.data?.config);
}

// triggerInputSkeleton renders fields as a JSON object to edit: [] for the
// ones a `json` call marks as collections, "" for the rest.
export function triggerInputSkeleton(fields) {
  if (!fields || fields.length === 0) return '{}';
  const obj = {};
  for (const f of fields) obj[f.name] = f.structured ? [] : '';
  return JSON.stringify(obj, null, 2);
}

// parseTriggerInput validates what the user typed. Returns { ok, value } or
// { ok: false, error } with a message worth showing.
export function parseTriggerInput(text) {
  const trimmed = (text || '').trim();
  if (trimmed === '' || trimmed === '{}') return { ok: true, value: '' };
  let parsed;
  try {
    parsed = JSON.parse(trimmed);
  } catch (err) {
    return { ok: false, error: `Not valid JSON: ${err.message}` };
  }
  if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
    return { ok: false, error: 'Trigger input must be a JSON object, e.g. {"prompts": ["a red bicycle"]}' };
  }
  return { ok: true, value: JSON.stringify(parsed) };
}

// Trigger data is remembered per workflow so a re-run starts from what worked
// last time rather than an empty skeleton. Per-viewer convenience only —
// wrapped because storage can be unavailable or throw.
const REMEMBER_PREFIX = 'monoagent:wf-trigger-input:';

export function rememberTriggerInput(workflowId, inputJSON) {
  if (!workflowId) return;
  try {
    if (inputJSON) localStorage.setItem(REMEMBER_PREFIX + workflowId, inputJSON);
    else localStorage.removeItem(REMEMBER_PREFIX + workflowId);
  } catch {
    // storage unavailable — remembering is optional
  }
}

export function rememberedTriggerInput(workflowId, fields) {
  try {
    const saved = workflowId && localStorage.getItem(REMEMBER_PREFIX + workflowId);
    if (saved) return JSON.stringify(JSON.parse(saved), null, 2);
  } catch {
    // unreadable or malformed — fall through to a fresh skeleton
  }
  return triggerInputSkeleton(fields);
}
