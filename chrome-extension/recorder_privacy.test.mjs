// Tests for recorder_privacy.js: which fields and values the recorder must
// never send, the secret names it suggests, and card scrubbing for snippets.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";
import { h } from "./recorder_fake_dom.mjs";

const { MonoRecorderPrivacy: P } = loadExtensionScripts(["recorder_privacy.js"]);

test("fields are masked by type and autocomplete", () => {
  assert.equal(P.maskReason(h("input", { type: "password" }), "x"), "password");
  assert.equal(P.maskReason(h("input", { type: "hidden" }), "x"), "hidden");
  assert.equal(P.maskReason(h("input", { autocomplete: "cc-csc" }), "123"), "card");
  assert.equal(P.maskReason(h("input", { autocomplete: "section-a new-password" }), "x"), "password");
  assert.equal(P.maskReason(h("input", { autocomplete: "one-time-code" }), "123456"), "otp");
  assert.equal(P.maskReason(h("input", { name: "q" }), "shoes"), "");
});

test("cvv, csc, ssn, pin and otp fields are masked by name, id, label or placeholder", () => {
  const masked = [
    h("input", { name: "cardCvv" }),
    h("input", { id: "cvc2" }),
    h("input", { name: "csc" }),
    h("input", { name: "ssn" }),
    h("input", { name: "user-pin" }),
    h("input", { placeholder: "Enter your OTP" }),
    h("input", { "aria-label": "Security code" }),
    h("input", { name: "one_time_password" }),
  ];
  for (const el of masked) assert.equal(P.maskReason(el, "1234"), "secret", JSON.stringify(el.attrs));
  assert.equal(P.maskReason(h("input", { name: "x" }), "123-45", "Social security number"), "secret", "label counts");
  for (const name of ["spinner", "pinterest_url", "shipping", "opinion", "keywords"]) {
    assert.equal(P.maskReason(h("input", { name }), "a"), "", name);
  }
});

test("a Luhn-valid card number is masked wherever it is typed", () => {
  assert.equal(P.maskReason(h("textarea", {}), "4111 1111 1111 1111"), "card");
  assert.equal(P.maskReason(h("input", {}), "4111 1111 1111 1112"), "");
});

test("scrubCards removes card numbers from free text and leaves other digits", () => {
  assert.equal(P.scrubCards("<textarea>pay 4242-4242-4242-4242 now</textarea>"), "<textarea>pay [card] now</textarea>");
  assert.equal(P.scrubCards("order 1234567890123 ref"), "order 1234567890123 ref", "not Luhn-valid");
  assert.equal(P.scrubCards("id 42"), "id 42");
});

test("secret names prefer name, then a stable id, then the label", () => {
  const gen = (id) => /^:r/.test(id);
  assert.equal(P.secretName(h("input", { name: "cardCvv" }), "secret", "", gen), "cardcvv");
  assert.equal(P.secretName(h("input", { id: ":r3:" }), "password", "", gen), "password");
  assert.equal(P.secretName(h("input", { id: ":r3:" }), "secret", "Security code", gen), "security_code");
  assert.equal(P.secretName(h("input", {}), "card", "", gen), "card_number");
});

test("sensitive names: pass, token, secret, pin, cvv, otp, ssn (H6)", () => {
  for (const name of ["pass", "passwd", "user_password", "apiToken", "client_secret", "pin", "card-cvv", "otp", "ssn"]) {
    assert.ok(P.sensitiveField(h("input", { name })), name);
  }
  assert.equal(P.sensitiveField(h("input", { name: "passport_no" })), "");
  assert.equal(P.sensitiveField(h("input", { autocomplete: "cc-exp" })), "card");
});

test("-webkit-text-security marks a CSS-masked field", () => {
  const g = loadExtensionScripts(["recorder_privacy.js"], {
    getComputedStyle: () => ({ webkitTextSecurity: "disc", getPropertyValue: () => "disc" }),
  });
  // The script reads root.getComputedStyle; the sandbox root is the fake global.
  g.getComputedStyle = () => ({ webkitTextSecurity: "disc", getPropertyValue: () => "disc" });
  const el = h("input", { name: "code_display" });
  el.nodeType = 1;
  assert.equal(g.MonoRecorderPrivacy.sensitiveField(el), "password");
});

test("sanitizeUrl drops fragments, keeps hash routes, redacts secret params (M5)", () => {
  assert.equal(P.sanitizeUrl("https://a.test/x#section"), "https://a.test/x");
  assert.equal(P.sanitizeUrl("https://a.test/#/inbox/3"), "https://a.test/#/inbox/3");
  assert.equal(P.sanitizeUrl("https://a.test/#/x?token=1"), "https://a.test/");
  assert.equal(
    P.sanitizeUrl("https://a.test/cb?code=1&id_token=2&sig=3&q=shoes&X-Amz-Signature=4&reset=5"),
    "https://a.test/cb?code=REDACTED&id_token=REDACTED&sig=REDACTED&q=shoes&X-Amz-Signature=REDACTED&reset=REDACTED"
  );
  assert.equal(P.sanitizeUrl("not a url"), "not a url");
});
