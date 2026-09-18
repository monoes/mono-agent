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
//
// A field also inherits the examples of the node field it feeds. The
// reference lives in a node config key (`prompts`), and that key's schema
// carries ready-made values, so "what do I type here?" has a real answer
// instead of an empty array.
export function collectTriggerFields(nodes, liveSchemas) {
  const found = new Map(); // name -> { name, structured, examples }
  const note = (name, structured, examples) => {
    const existing = found.get(name);
    if (!existing) {
      found.set(name, { name, structured, examples: examples || [] });
      return;
    }
    existing.structured = existing.structured || structured;
    if (existing.examples.length === 0 && examples?.length) existing.examples = examples;
  };

  for (const node of nodes || []) {
    for (const { configKey, text } of configStrings(node)) {
      const examples = schemaExamples(node, configKey, liveSchemas);
      for (const [, body] of text.matchAll(EXPRESSION)) {
        // `json $json.x` / `toJson $json.x` — the value is a collection.
        const structured = /\bjson\s+\$json[.[]/i.test(body);
        for (const [, name] of body.matchAll(DOT_REF)) note(name, structured, examples);
        for (const [, name] of body.matchAll(BRACKET_REF)) note(name, structured, examples);
      }
    }
  }
  return [...found.values()];
}

// schemaExamples returns the example values a node's schema offers for one
// config key (NodeSchemaField.Examples, internal/workflow/schema_loader.go).
//
// The node catalog's schema wins over the copy saved inside the workflow: a
// workflow saved before a field grew examples still carries the old snapshot,
// and that is exactly the workflow whose user needs the examples most.
function schemaExamples(node, configKey, liveSchemas) {
  const fields = liveSchemas?.[node?.subtype ?? node?.type]?.fields ?? node?.schema?.fields;
  if (!Array.isArray(fields)) return [];
  const field = fields.find(f => f?.key === configKey);
  return Array.isArray(field?.examples) ? field.examples : [];
}

// configStrings walks a node's config and yields every string value with the
// top-level config key it sits under, however deeply nested.
function* configStrings(node) {
  const walk = function* (configKey, value) {
    if (typeof value === 'string') {
      yield { configKey, text: value };
    } else if (Array.isArray(value)) {
      for (const v of value) yield* walk(configKey, v);
    } else if (value && typeof value === 'object') {
      for (const v of Object.values(value)) yield* walk(configKey, v);
    }
  };
  const config = node?.config ?? node?.data?.config;
  if (!config || typeof config !== 'object') return;
  for (const [key, value] of Object.entries(config)) yield* walk(key, value);
}

// triggerInputSkeleton renders fields as a JSON object to edit. A field with
// examples is filled with them — two sample prompts beat an empty array,
// since the user can see the shape and the wording and edit from there.
export function triggerInputSkeleton(fields) {
  if (!fields || fields.length === 0) return '{}';
  const obj = {};
  for (const f of fields) {
    if (f.examples?.length) {
      obj[f.name] = f.structured ? [...f.examples] : f.examples[0];
    } else {
      obj[f.name] = f.structured ? [] : '';
    }
  }
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
