/**
 * MonoAgent Bridge -- what the recorder must never send (spec section 8.2)
 *
 * One place for the privacy rules, loaded first into the page so every
 * other recorder script applies the same ones: typed values (recorder.js),
 * picked data (recorder_list.js) and DOM snippets (recorder.js snippetOf).
 *
 * A value is withheld when the FIELD says it is secret:
 *   - input type password or hidden;
 *   - autocomplete current-password / new-password / one-time-code / cc-*;
 *   - its name, id, label, placeholder or aria-label reads like a secret:
 *     cvv, cvc, csc, cvn, ssn, pin, otp, passcode, security code, social
 *     security, one-time / verification code;
 * or when the VALUE is a card number (Luhn-valid 13-19 digits).
 *
 * The page scripts must stay plain ASCII (recorder_encoding.test.mjs).
 */

(function (root) {
  "use strict";

  const tagOf = (el) => String((el && el.tagName) || "").toLowerCase();
  const attr = (el, name) => (el && el.getAttribute ? el.getAttribute(name) : null);
  const typeOf = (el) => String(attr(el, "type") || "text").toLowerCase();

  function luhn(value) {
    const digits = String(value || "").replace(/[\s-]/g, "");
    if (!/^\d{13,19}$/.test(digits)) return false;
    let sum = 0;
    for (let i = 0; i < digits.length; i++) {
      let d = Number(digits[digits.length - 1 - i]);
      if (i % 2 === 1) {
        d *= 2;
        if (d > 9) d -= 9;
      }
      sum += d;
    }
    return sum % 10 === 0;
  }

  /** tokens splits "cardCvv", "card-cvv", "Card CVV" into ["card", "cvv"]. */
  function tokens(s) {
    return String(s || "")
      .replace(/([a-z])([A-Z])/g, "$1 $2")
      .toLowerCase()
      .split(/[^a-z0-9]+/)
      .filter(Boolean);
  }

  const SECRET_WORDS = new Set(["cvv", "cvv2", "cvc", "cvc2", "csc", "cvn", "ssn", "pin", "otp", "passcode", "totp", "2fa", "mfa"]);
  const SECRET_PAIRS = [
    ["security", "code"],
    ["social", "security"],
    ["one", "time"],
    ["verification", "code"],
    ["auth", "code"],
  ];

  /** looksSecret is true when a field's own description names a secret. */
  function looksSecret(text) {
    const t = tokens(text);
    if (t.some((w) => SECRET_WORDS.has(w))) return true;
    for (let i = 0; i + 1 < t.length; i++) {
      if (SECRET_PAIRS.some(([a, b]) => t[i] === a && t[i + 1] === b)) return true;
    }
    return false;
  }

  /**
   * maskReason says why a field's value must not be recorded, or "".
   * `label` is the field's label text when the caller knows it.
   */
  function maskReason(el, value, label) {
    const auto = String(attr(el, "autocomplete") || "").toLowerCase();
    const isInput = tagOf(el) === "input";
    if (isInput && typeOf(el) === "password") return "password";
    if (/(^|\s)(current-password|new-password)$/.test(auto)) return "password";
    if (/(^|\s)cc-/.test(auto)) return "card";
    if (/(^|\s)one-time-code$/.test(auto)) return "otp";
    if (isInput && typeOf(el) === "hidden") return "hidden";
    const described = [attr(el, "name"), attr(el, "id"), attr(el, "placeholder"), attr(el, "aria-label"), label].join(" ");
    if (looksSecret(described)) return "secret";
    if (luhn(value)) return "card";
    return "";
  }

  const slug = (s) =>
    String(s || "")
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "_")
      .replace(/^_+|_+$/g, "")
      .slice(0, 40);

  /** secretName suggests the secret input a masked value becomes. */
  function secretName(el, reason, label, looksGenerated) {
    const auto = String(attr(el, "autocomplete") || "").split(" ").pop();
    const id = attr(el, "id");
    const stableId = id && !(looksGenerated && looksGenerated(id)) ? id : "";
    const named = slug(attr(el, "name") || stableId || label || (auto !== "on" && auto !== "off" ? auto : ""));
    if (named) return named;
    return { password: "password", card: "card_number", otp: "one_time_code", hidden: "hidden_value" }[reason] || "secret";
  }

  /** scrubCards replaces every Luhn-valid card number in free text. */
  function scrubCards(text) {
    return String(text || "").replace(/\d(?:[ -]?\d){12,18}/g, (m) => (luhn(m) ? "[card]" : m));
  }

  root.MonoRecorderPrivacy = { luhn, maskReason, secretName, scrubCards, looksSecret, tokens, slug };
})(globalThis);
