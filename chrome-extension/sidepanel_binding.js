/**
 * MonoAgent Bridge — the side panel's "Automations in this browser" field.
 *
 * Draws the binding select from the same profile list the capture picker
 * uses, and saves straight to chrome.storage.local. The worker notices the
 * write and tells the bridge (browser_binding.js), so there is one path for
 * a binding change whoever makes it.
 */

(function (root) {
  "use strict";

  function install(opts) {
    const B = root.MonoBrowserBinding;
    const doc = opts.doc;
    const select = doc.getElementById("binding-profile");
    const label = doc.getElementById("binding-label");
    const save = doc.getElementById("binding-save");
    const msg = doc.getElementById("binding-msg");
    let state = { instance: "", profile: "", label: "" };

    function say(tone, text) {
      msg.dataset.tone = tone;
      msg.textContent = text;
    }

    function redraw() {
      select.textContent = "";
      for (const o of B.options(opts.profiles(), state.profile)) {
        const opt = doc.createElement("option");
        opt.value = o.id;
        opt.textContent = o.name;
        opt.selected = o.selected;
        select.appendChild(opt);
      }
    }

    async function refresh() {
      state = await B.load(opts.storage, { userAgent: opts.userAgent });
      label.value = state.label;
      redraw();
    }

    save.addEventListener("click", async () => {
      try {
        await B.setBinding(opts.storage, { profile: select.value, label: label.value });
        await refresh();
        say("ok", select.value
          ? "Saved. This profile's automations now open their tabs in this browser."
          : "Saved. This is a default browser again.");
      } catch (err) {
        say("error", err.message);
      }
    });

    return { refresh, redraw };
  }

  root.MonoPanelBinding = { install };
})(globalThis);
