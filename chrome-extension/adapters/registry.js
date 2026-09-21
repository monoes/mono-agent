/**
 * MonoAgent Bridge — per-site adapter registry (CLIP-10)
 *
 * A YouTube page saved by the generic readable pipeline is a player shell: a
 * title, a row of recommendations, and none of what the page is actually
 * about. Same for an arXiv abstract page, a thread, a PR. An adapter's job is
 * to produce the *canonical form* of such a page — the transcript, the
 * abstract and its citation, the posts in order — instead of the wrapper.
 *
 * The invariant that governs every line here: an adapter must never be able
 * to make a page fail to capture. Site markup changes constantly, so a
 * decline, a throw, an empty result and a bad artifact name are all the same
 * event — fall back to the generic pipeline, record a warning in the
 * envelope, and carry on. `run` therefore returns `null` rather than
 * throwing, and the caller treats `null` as "nothing special about this
 * page".
 *
 * Adapter shape:
 *
 *   {
 *     name: "youtube",
 *     match(ctx) -> boolean,
 *     extract(ctx) -> null | {
 *       markdown,            // required; the canonical document
 *       title?,              // overrides the page <title> in meta
 *       meta?: {...},        // merged into meta.json (meta.adapter is set here)
 *       artifacts?: [{ name, text }],
 *       warnings?: [string],
 *     }
 *   }
 *
 * ctx shape (everything is optional except url and tree):
 *
 *   { url, tree, title, baseUrl, document }
 *
 * `tree` is the node tree from domlite.js / MonoReadable.snapshot, which is
 * what makes adapters unit-testable from a trimmed HTML fixture. `document`
 * is the live DOM when there is one; an adapter may use it for signals a
 * snapshot cannot carry, but must still work without it.
 */

(function (root) {
  "use strict";

  const adapters = [];
  let lastWarnings = [];

  function register(adapter) {
    if (!adapter || typeof adapter.name !== "string" || !adapter.name) {
      throw new Error("adapter needs a name");
    }
    if (typeof adapter.match !== "function") throw new Error(`adapter ${adapter.name} needs match()`);
    if (typeof adapter.extract !== "function") throw new Error(`adapter ${adapter.name} needs extract()`);
    // Registering by name rather than appending: the page scripts are
    // re-injected on every capture, and a tab that had been captured twice
    // must not end up running every adapter twice.
    const existing = adapters.findIndex((a) => a.name === adapter.name);
    if (existing >= 0) adapters[existing] = adapter;
    else adapters.push(adapter);
    return adapter;
  }

  const WINDOWS_RESERVED = new Set([
    "CON", "PRN", "AUX", "NUL",
    "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
    "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9",
  ]);

  /**
   * validArtifactName mirrors ValidArtifactName in internal/capture/envelope.go.
   * The Go receiver rejects the whole envelope on a bad name, so an adapter
   * that invents one must lose its artifact, not the page. Kept in sync by
   * hand and by registry.test.mjs; if the two ever drift, Go is the authority.
   */
  function validArtifactName(name) {
    if (typeof name !== "string" || !name || name.length > 64) return false;
    if (name.startsWith(".") || name.endsWith(".")) return false;
    if (name === "meta.json") return false; // reserved: the writer owns it
    if (!/^[A-Za-z0-9._-]+$/.test(name)) return false;
    if (name.includes("..")) return false;
    return !WINDOWS_RESERVED.has(name.split(".")[0].toUpperCase());
  }

  function normalizeArtifacts(list, adapterName, warnings) {
    const out = [];
    for (const artifact of Array.isArray(list) ? list : []) {
      if (!artifact || typeof artifact.text !== "string") continue;
      if (!validArtifactName(artifact.name)) {
        warnings.push(`adapter ${adapterName} rejected artifact name ${JSON.stringify(String(artifact.name))}`);
        continue;
      }
      out.push({ name: artifact.name, text: artifact.text });
    }
    return out;
  }

  /**
   * run picks the first adapter that claims the page and produces something,
   * and returns null when none does. Warnings accumulate across every adapter
   * tried, so the envelope records *why* a page fell back rather than just
   * that it did.
   */
  function run(ctx) {
    const warnings = [];
    lastWarnings = warnings;
    for (const adapter of adapters) {
      let claims = false;
      try {
        claims = !!adapter.match(ctx);
      } catch (err) {
        warnings.push(`adapter ${adapter.name} failed to match: ${err.message}`);
        continue;
      }
      if (!claims) continue;

      let result;
      try {
        result = adapter.extract(ctx);
      } catch (err) {
        warnings.push(`adapter ${adapter.name} failed: ${err.message}`);
        continue;
      }
      if (!result || typeof result.markdown !== "string" || !result.markdown.trim()) {
        warnings.push(`adapter ${adapter.name} declined this page`);
        continue;
      }

      const artifacts = normalizeArtifacts(result.artifacts, adapter.name, warnings);
      return {
        adapter: adapter.name,
        markdown: result.markdown.trim(),
        title: result.title || null,
        // meta.adapter is written last so an adapter cannot lie about which
        // adapter ran — that field is how a stale extraction is spotted later.
        meta: Object.assign({}, result.meta, { adapter: adapter.name }),
        artifacts,
        warnings: warnings.concat(Array.isArray(result.warnings) ? result.warnings : []),
      };
    }
    return null;
  }

  root.MonoAdapters = {
    register,
    run,
    all: () => adapters.slice(),
    reset: () => {
      adapters.length = 0;
      lastWarnings = [];
    },
    lastWarnings: () => lastWarnings.slice(),
    validArtifactName,
  };
})(globalThis);
