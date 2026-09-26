#!/usr/bin/env node
// A stand-in for `monomind` so `record analyze` runs without spending AI.
// Point MONOMIND_BIN at this file. It speaks just enough of the agent exec
// protocol: `--version --json` answers the handshake, `agent exec` answers
// every prompt with a canned draft for the fixture CRM (create_contact on
// $E2E_SITE). Each prompt is saved to $E2E_WORK/prompts/ for inspection.

import { existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from "node:fs";

const argv = process.argv.slice(2);
const site = (process.env.E2E_SITE || "http://crm.e2e.test:18765").replace(/\/$/, "");
const work = process.env.E2E_WORK || `${process.env.HOME}/scratch/automation-e2e-run`;

if (argv[0] === "--version") {
  console.log(JSON.stringify({ v: 1, version: "2.16.5", min_caller: "1.0.0", capabilities: ["agent-exec", "agent-scan", "org-json-v1"] }));
  process.exit(0);
}
if (argv[0] !== "agent" || argv[1] !== "exec") {
  console.error("stub-monomind: unsupported:", argv.join(" "));
  process.exit(2);
}

const dir = `${work}/prompts`;
mkdirSync(dir, { recursive: true });
const i = argv.indexOf("--prompt-file");
if (i >= 0 && existsSync(argv[i + 1])) {
  writeFileSync(`${dir}/prompt-${readdirSync(dir).length + 1}.md`, readFileSync(argv[i + 1]));
}

const cand = (css, extra = []) => [{ css, score: 0.9 }, ...extra];
const answer = {
  automation: { id: "e2e-crm", name: "E2E CRM", description: "The e2e fixture CRM", login: { url: `${site}/login`, loggedIn: { selector: "#nav-contacts" } } },
  action: {
    actionType: "create_contact",
    description: "Create a contact from a name and an email, then read the contact list",
    sideEffects: "write",
    inputs: {
      required: [{ name: "email", type: "string", format: "email", description: "Contact email", ui: { label: "Email" } }],
      optional: [{ name: "name", type: "string", description: "Full name", default: "Katherine Johnson", ui: { label: "Full name" } }],
    },
    outputs: { success: ["contacts"] },
    outputSchema: { type: "object", properties: { contact_name: { type: "string" } } },
    steps: [
      { id: "open", type: "navigate", url: `${site}/contacts/new` },
      { id: "name", type: "type", configKey: "contact.name_input", intent: "the Full name field", value: "{{name}}" },
      { id: "email", type: "type", configKey: "contact.email_input", intent: "the Email field", value: "{{email}}" },
      { id: "save", type: "click", configKey: "contact.save_button", intent: "the Save button", sideEffect: true, until: { selector: "#toast" } },
      { id: "list", type: "click", configKey: "nav.contacts_link", intent: "the Contacts link", until: { urlMatches: "/contacts(?:[?#]|$)" } },
      { id: "rows", type: "extract_table", configKey: "contacts.list", value: "li.contact", intent: "the contact list", fields: { contact_name: "span.contact-name" }, variable_name: "contacts" },
    ],
  },
  selectors: {
    "contact.name_input": { intent: "Full name field", candidates: cand("#contact-name", [{ aria: { role: "textbox", name: "Full name" }, score: 0.85 }]) },
    "contact.email_input": { intent: "Email field", candidates: cand("#contact-email", [{ aria: { role: "textbox", name: "Email" }, score: 0.85 }]) },
    "contact.save_button": { intent: "Save button", candidates: [{ css: '[data-testid="save-contact"]', score: 1 }, { text: "Save", score: 0.6 }] },
    "nav.contacts_link": { intent: "Contacts link", candidates: cand("#nav-contacts", [{ text: "Contacts", score: 0.6 }]) },
    "contacts.list": { intent: "the contact list", candidates: cand("#contact-list") },
  },
  fragments: [],
  scripts: {},
  names: { automation: "e2e-crm", action: "create_contact", fragment: "create_contact" },
};

const text = JSON.stringify(answer);
const ev = (o) => process.stdout.write(JSON.stringify({ v: 1, ...o }) + "\n");
ev({ type: "start", runtime: "claude", cwd: process.cwd(), pid: process.pid });
ev({ type: "session", session_id: "th_stub" });
ev({ type: "assistant", text });
ev({ type: "result", subtype: "success", is_error: false, stop_reason: "end_turn", text, input_tokens: 1, output_tokens: 1, cost_usd: 0 });
ev({ type: "done", exit_code: 0 });
