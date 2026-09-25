package bottest

// runtimeJS is a port of the DOM half of chrome-extension/content.js. It is
// installed lazily as window.__bottest in the page's main world (the
// extension uses an isolated world; nothing here depends on the difference)
// and is re-created by every new document, so element ids go stale on
// navigation exactly as the extension's do. Ids carry a per-document tag so
// a stale id can never alias an element of the next page.
const runtimeJS = `(() => {
  const reg = new Map();
  const tag = Math.random().toString(36).slice(2, 8);
  let n = 0;
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  const register = (el) => { const id = 'el_' + tag + '_' + (++n); reg.set(id, new WeakRef(el)); return id; };
  const get = (id) => {
    const ref = reg.get(id);
    const el = ref && ref.deref();
    if (!el) { reg.delete(id); throw new Error('Element ' + id + ' no longer exists in DOM'); }
    return el;
  };
  const query = ({ selector, xpath }) => {
    if (xpath) return document.evaluate(xpath, document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null).singleNodeValue;
    return document.querySelector(selector);
  };
  const queryAll = ({ selector, xpath }) => {
    if (xpath) {
      const r = document.evaluate(xpath, document, null, XPathResult.ORDERED_NODE_SNAPSHOT_TYPE, null);
      const out = [];
      for (let i = 0; i < r.snapshotLength; i++) out.push(r.snapshotItem(i));
      return out;
    }
    return Array.from(document.querySelectorAll(selector));
  };
  const codeFor = (key) => ({ Enter: 'Enter', Tab: 'Tab', Escape: 'Escape', Backspace: 'Backspace', Delete: 'Delete',
    ArrowUp: 'ArrowUp', ArrowDown: 'ArrowDown', ArrowLeft: 'ArrowLeft', ArrowRight: 'ArrowRight', Home: 'Home', End: 'End',
    PageUp: 'PageUp', PageDown: 'PageDown', Space: 'Space', ' ': 'Space' }[key] || ('Key' + key.toUpperCase()));
  const keyCodeFor = (key) => ({ Enter: 13, Tab: 9, Escape: 27, Backspace: 8, Delete: 46, ArrowUp: 38, ArrowDown: 40,
    ArrowLeft: 37, ArrowRight: 39, Home: 36, End: 35, PageUp: 33, PageDown: 34, Space: 32, ' ': 32 }[key] || key.toUpperCase().charCodeAt(0));

  return {
    find(p) { const el = query(p); return el ? register(el) : null; },
    findAll(p) { return queryAll(p).map(register); },
    has(p) { return !!query(p); },
    race({ selectors }) {
      for (let i = 0; i < selectors.length; i++) {
        const el = document.querySelector(selectors[i]);
        if (el) return { index: i, id: register(el) };
      }
      return null;
    },
    click({ id }) {
      const el = get(id);
      el.scrollIntoView({ block: 'center', behavior: 'instant' });
      const rect = el.getBoundingClientRect();
      const init = { bubbles: true, cancelable: true, clientX: rect.left + rect.width / 2, clientY: rect.top + rect.height / 2, view: window };
      el.dispatchEvent(new MouseEvent('mousedown', init));
      el.dispatchEvent(new MouseEvent('mouseup', init));
      try {
        if (typeof el.click === 'function') el.click(); else el.dispatchEvent(new MouseEvent('click', init));
      } catch (e) { el.dispatchEvent(new MouseEvent('click', init)); }
      return true;
    },
    async input({ id, text, clearFirst = true, delay = 10 }) {
      const el = get(id);
      el.focus();
      el.dispatchEvent(new FocusEvent('focus', { bubbles: true }));
      const ce = el.isContentEditable;
      if (clearFirst) {
        if (ce) { const sel = window.getSelection(); sel.selectAllChildren(el); document.execCommand('delete', false); }
        else { el.value = ''; el.dispatchEvent(new InputEvent('input', { bubbles: true, inputType: 'deleteContentBackward' })); }
      }
      if (ce) {
        let pasted = false;
        try {
          el.focus();
          const dt = new DataTransfer();
          dt.setData('text/plain', text);
          el.dispatchEvent(new ClipboardEvent('paste', { bubbles: true, cancelable: true, clipboardData: dt }));
          await sleep(200);
          if (el.textContent && el.textContent.trim().length > 0) pasted = true;
        } catch (e) {}
        if (!pasted) {
          document.execCommand('insertText', false, text);
          await sleep(200);
          if (el.textContent && el.textContent.trim().length > 0) pasted = true;
        }
        if (!pasted) {
          el.textContent = text;
          el.dispatchEvent(new InputEvent('input', { bubbles: true, inputType: 'insertText', data: text }));
        }
      } else {
        for (const ch of text) {
          el.value += ch;
          el.dispatchEvent(new InputEvent('input', { bubbles: true, data: ch, inputType: 'insertText' }));
          if (delay > 0) await sleep(delay);
        }
        el.dispatchEvent(new Event('change', { bubbles: true }));
      }
      return true;
    },
    text({ id }) { return (get(id).textContent || '').trim(); },
    attr({ id, name }) { return get(id).getAttribute(name); },
    prop({ id, name }) {
      const v = get(id)[name];
      try { return JSON.parse(JSON.stringify(v === undefined ? null : v)); } catch (e) { return null; }
    },
    html({ id }) { return get(id).innerHTML; },
    focus({ id }) { const el = get(id); el.focus(); el.dispatchEvent(new FocusEvent('focus', { bubbles: true })); return true; },
    scrollIntoView({ id }) { get(id).scrollIntoView({ block: 'center', behavior: 'smooth' }); return true; },
    rect({ id }) { const r = get(id).getBoundingClientRect(); return { x: r.x, y: r.y, width: r.width, height: r.height }; },
    async stable({ id, ms }) {
      const el = get(id);
      const box = () => { const r = el.getBoundingClientRect(); return [r.x, r.y, r.width, r.height].join(','); };
      let last = box();
      for (;;) {
        await sleep(ms);
        const now = box();
        if (now === last) return true;
        last = now;
      }
    },
    setFiles({ id, fileData }) {
      const el = get(id);
      if (!el || el.tagName !== 'INPUT' || el.type !== 'file') throw new Error('set_files: element is not a file input');
      if (!fileData || fileData.length === 0) throw new Error('set_files: no file data provided');
      const dt = new DataTransfer();
      for (const fd of fileData) {
        const bs = atob(fd.data);
        const ia = new Uint8Array(bs.length);
        for (let i = 0; i < bs.length; i++) ia[i] = bs.charCodeAt(i);
        const blob = new Blob([ia], { type: fd.mimeType || 'image/png' });
        dt.items.add(new File([blob], fd.name || 'image.png', { type: blob.type }));
      }
      el.files = dt.files;
      el.dispatchEvent(new Event('change', { bubbles: true }));
      el.dispatchEvent(new Event('input', { bubbles: true }));
      return true;
    },
    async keyboardType({ text, delay = 30 }) {
      const target = document.activeElement || document.body;
      for (const ch of text) {
        const init = { key: ch, code: 'Key' + ch.toUpperCase(), keyCode: ch.charCodeAt(0), charCode: ch.charCodeAt(0), which: ch.charCodeAt(0), bubbles: true, cancelable: true };
        target.dispatchEvent(new KeyboardEvent('keydown', init));
        target.dispatchEvent(new KeyboardEvent('keypress', init));
        if ('value' in target) target.value += ch;
        target.dispatchEvent(new InputEvent('input', { bubbles: true, data: ch, inputType: 'insertText' }));
        target.dispatchEvent(new KeyboardEvent('keyup', init));
        if (delay > 0) await sleep(delay);
      }
      return true;
    },
    keyboardPress({ key }) {
      const target = document.activeElement || document.body;
      const kc = keyCodeFor(key);
      const init = { key, code: codeFor(key), keyCode: kc, which: kc, bubbles: true, cancelable: true };
      target.dispatchEvent(new KeyboardEvent('keydown', init));
      target.dispatchEvent(new KeyboardEvent('keypress', init));
      target.dispatchEvent(new KeyboardEvent('keyup', init));
      return true;
    },
    insertText({ text, id }) {
      let target = null;
      if (id) { const ref = reg.get(id); target = ref && ref.deref(); if (target) { target.focus(); target.click(); } }
      if (!target) target = document.activeElement;
      if (target) {
        try {
          target.focus();
          const dt = new DataTransfer();
          dt.setData('text/plain', text);
          target.dispatchEvent(new ClipboardEvent('paste', { bubbles: true, cancelable: true, clipboardData: dt }));
          return { inserted: true, method: 'paste' };
        } catch (e) {}
      }
      if (document.execCommand('insertText', false, text)) return { inserted: true, method: 'execCommand' };
      if (target && (target.tagName === 'TEXTAREA' || target.tagName === 'INPUT')) {
        target.value = text;
        target.dispatchEvent(new InputEvent('input', { bubbles: true, data: text, inputType: 'insertText' }));
        target.dispatchEvent(new Event('change', { bubbles: true }));
        return { inserted: true, method: 'value' };
      }
      if (target && target.isContentEditable) {
        target.textContent = text;
        target.dispatchEvent(new InputEvent('input', { bubbles: true, data: text, inputType: 'insertText' }));
        return { inserted: true, method: 'textContent' };
      }
      return { inserted: false };
    },
    scrollBy({ x, y }) { window.scrollBy({ left: x, top: y, behavior: 'smooth' }); return true; },
  };
})()`

// typeCDPFocusJS is the focus step of the extension's type_cdp command
// (background.js typeCDP): the first visible contenteditable larger than
// 100×30, else the first [role=textbox]. It ignores any element id.
const typeCDPFocusJS = `(() => {
  const candidates = document.querySelectorAll('[contenteditable="true"]');
  for (const el of candidates) {
    const rect = el.getBoundingClientRect();
    if (rect.width > 100 && rect.height > 30 && rect.top > 0) {
      el.focus();
      el.click();
      return { found: true, tag: el.tagName };
    }
  }
  const tb = document.querySelector('[role="textbox"]');
  if (tb) { tb.focus(); tb.click(); return { found: true, tag: 'textbox' }; }
  return { found: false };
})()`

// evalWrapperJS mirrors the extension's MAIN-world eval (background.js
// evalInTab): the code is compiled with new Function — which a CSP without
// 'unsafe-eval' blocks — and a thrown error becomes {__monoagent_error}.
// %s are the JSON-encoded code string and args array.
const evalWrapperJS = `(async () => {
  try {
    const __args = %s;
    const __fn = new Function('return (' + %s + ')')();
    return typeof __fn === 'function' ? await __fn(...__args) : __fn;
  } catch (e) {
    return { __monoagent_error: String((e && e.message) || e) };
  }
})()`
