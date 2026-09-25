// Reads internal/recording/types.go and returns, per struct, the set of JSON
// field names Go will accept — so the recorder's tests check the frames the
// extension builds against the authoritative schema, not against a copy of
// it that can drift.

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const HERE = dirname(fileURLToPath(import.meta.url));
const TYPES = join(HERE, "..", "internal", "recording", "types.go");

/** goJsonTags → { StructName: Set(jsonName) }. Untagged fields use the Go name. */
export function goJsonTags(path = TYPES) {
  const src = readFileSync(path, "utf8");
  const out = {};
  const structRe = /type\s+(\w+)\s+struct\s*\{([\s\S]*?)\n\}/g;
  let m;
  while ((m = structRe.exec(src))) {
    const [, name, body] = m;
    const fields = new Set();
    for (const raw of body.split("\n")) {
      const line = raw.replace(/\/\/.*$/, "").trim();
      if (!line) continue;
      const tag = /`json:"([^",]*)/.exec(line);
      if (tag) {
        if (tag[1] && tag[1] !== "-") fields.add(tag[1]);
        continue;
      }
      // "X, Y, W, H float64" — untagged: JSON uses the Go names.
      const names = /^([A-Z]\w*(?:\s*,\s*[A-Z]\w*)*)\s+\S+$/.exec(line);
      if (names) for (const n of names[1].split(",")) fields.add(n.trim());
    }
    out[name] = fields;
  }
  return out;
}
