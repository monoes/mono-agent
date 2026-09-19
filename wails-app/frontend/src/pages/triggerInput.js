// Trigger-input helpers for the run dialog — UI concerns only.
//
// Which fields a workflow reads, and what examples to offer for them, is not
// decided here: it comes from `monoagentcli workflow inputs --json` (see
// internal/workflow/trigger_inputs.go), so a script, a test and this dialog
// all get the same answer. What is left below is presentation: validating
// what the user typed, and remembering it between runs.

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

// rememberedTriggerInput is what the dialog opens with: what the user ran
// last time for this workflow, else the skeleton the CLI supplied.
export function rememberedTriggerInput(workflowId, skeleton) {
  try {
    const saved = workflowId && localStorage.getItem(REMEMBER_PREFIX + workflowId);
    if (saved) return JSON.stringify(JSON.parse(saved), null, 2);
  } catch {
    // unreadable or malformed — fall through to the fresh skeleton
  }
  return JSON.stringify(skeleton ?? {}, null, 2);
}
