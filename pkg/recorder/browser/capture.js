// APEX Load embedded recorder capture script.
// Injected into every frame via AddInitScript. Reports structured interaction
// events to Go through the exposed window.__apexloadRecord binding. Uses
// composedPath() so interactions inside open shadow roots resolve to the real
// target element, and computes shadow-piercing CSS paths (Playwright's CSS
// engine pierces open shadow roots; XPath does not, so XPath is omitted for
// shadow elements).
(() => {
  'use strict';
  if (window.__apexloadRecorderInstalled) return;
  window.__apexloadRecorderInstalled = true;

  let paused = false;
  let assertMode = false;
  let toolbarHost = null;

  // The Go recorder owns the authoritative pause state (it also gates events
  // server-side); this setter keeps the local gate and toolbar in sync.
  window.__apexloadRecorderSetPaused = (value) => {
    paused = !!value;
    if (paused) assertMode = false;
    refreshToolbar();
  };

  // The Go recorder owns the authoritative element-picker (assert) state too,
  // so the control panel and the in-page toolbar never diverge.
  window.__apexloadRecorderSetAssertMode = (value) => {
    assertMode = !!value && !paused;
    refreshToolbar();
  };

  const TEST_ATTRS = ['data-testid', 'data-test-id', 'data-test', 'data-cy', 'data-qa'];
  const SAFE_IDENT = /^[A-Za-z_][A-Za-z0-9_-]*$/;

  function send(payload) {
    try {
      if (typeof window.__apexloadRecord === 'function') {
        window.__apexloadRecord(JSON.stringify(payload));
      }
    } catch (e) { /* recorder binding unavailable; drop event */ }
  }

  function escAttr(v) {
    return String(v).replace(/\\/g, '\\\\').replace(/"/g, '\\"');
  }

  function segmentFor(el) {
    if (el.id && SAFE_IDENT.test(el.id)) return '#' + el.id;
    let seg = el.tagName.toLowerCase();
    const parent = el.parentElement;
    if (parent) {
      const sameTag = Array.prototype.filter.call(parent.children, (c) => c.tagName === el.tagName);
      if (sameTag.length > 1) seg += ':nth-of-type(' + (sameTag.indexOf(el) + 1) + ')';
    }
    return seg;
  }

  function cssPathWithin(el, root) {
    const parts = [];
    let cur = el;
    while (cur && cur !== root && cur.nodeType === 1) {
      const seg = segmentFor(cur);
      parts.unshift(seg);
      if (seg.charAt(0) === '#') break;
      cur = cur.parentElement;
    }
    return parts.join(' > ');
  }

  // Builds a CSS path from the document down to the element, crossing open
  // shadow roots with a descendant combinator (Playwright CSS pierces them).
  function fullCssPath(el) {
    let path = '';
    let inShadow = false;
    let cur = el;
    for (let depth = 0; cur && depth < 10; depth++) {
      const root = cur.getRootNode();
      const within = cssPathWithin(cur, root);
      path = path ? within + ' ' + path : within;
      if (typeof ShadowRoot !== 'undefined' && root instanceof ShadowRoot) {
        inShadow = true;
        cur = root.host;
      } else {
        break;
      }
    }
    return { path: path, inShadow: inShadow };
  }

  function xpathFor(el) {
    const parts = [];
    let cur = el;
    while (cur && cur.nodeType === 1) {
      let idx = 1;
      let sib = cur.previousElementSibling;
      while (sib) {
        if (sib.tagName === cur.tagName) idx++;
        sib = sib.previousElementSibling;
      }
      parts.unshift(cur.tagName.toLowerCase() + '[' + idx + ']');
      cur = cur.parentElement;
    }
    return parts.length ? '//' + parts.join('/') : '';
  }

  function implicitRole(el) {
    const tag = el.tagName.toLowerCase();
    const type = (el.getAttribute('type') || '').toLowerCase();
    if (tag === 'button') return 'button';
    if (tag === 'a' && el.hasAttribute('href')) return 'link';
    if (tag === 'select') return 'combobox';
    if (tag === 'textarea') return 'textbox';
    if (tag === 'input') {
      if (type === 'button' || type === 'submit' || type === 'reset') return 'button';
      if (type === 'checkbox') return 'checkbox';
      if (type === 'radio') return 'radio';
      if (type === 'search') return 'searchbox';
      return 'textbox';
    }
    return '';
  }

  function shortText(el) {
    const raw = el.innerText || el.textContent || '';
    return raw.trim().replace(/\s+/g, ' ').slice(0, 120);
  }

  function metaFor(el) {
    const css = fullCssPath(el);
    let dataTestId = '';
    const attrs = {};
    for (let i = 0; i < TEST_ATTRS.length; i++) {
      const v = el.getAttribute(TEST_ATTRS[i]);
      if (v) {
        dataTestId = v;
        attrs[TEST_ATTRS[i]] = v;
        break;
      }
    }
    ['title', 'data-aura-class', 'data-aura-rendered-by'].forEach((name) => {
      const v = el.getAttribute(name);
      if (v) attrs[name] = v;
    });
    if (el.attributes) {
      Array.prototype.forEach.call(el.attributes, (attr) => {
        if (attr && attr.name && attr.name.indexOf('data-aura-') === 0 && attr.value) {
          attrs[attr.name] = attr.value;
        }
      });
    }
    return {
      tag: el.tagName.toLowerCase(),
      id: el.id || '',
      name: el.getAttribute('name') || '',
      type: el.getAttribute('type') || '',
      role: el.getAttribute('role') || implicitRole(el),
      aria_label: el.getAttribute('aria-label') || '',
      data_test_id: dataTestId,
      placeholder: el.getAttribute('placeholder') || '',
      text: shortText(el),
      attributes: attrs,
      css_path: css.path,
      xpath: css.inShadow ? '' : xpathFor(el),
      in_shadow_dom: css.inShadow
    };
  }

  // Same-origin frames identify themselves via frameElement; cross-origin
  // frames are resolved on the Go side from the binding source frame URL.
  function frameSelector() {
    try {
      const fe = window.frameElement;
      if (!fe) return '';
      if (fe.id && SAFE_IDENT.test(fe.id)) return 'iframe#' + fe.id;
      const name = fe.getAttribute('name');
      if (name) return 'iframe[name="' + escAttr(name) + '"]';
      const src = fe.getAttribute('src');
      if (src) return 'iframe[src="' + escAttr(src) + '"]';
      return 'iframe';
    } catch (e) {
      return '';
    }
  }

  function record(kind, el, value, masked) {
    send({
      kind: kind,
      value: value || '',
      masked: !!masked,
      ts: Date.now(),
      url: location.href,
      title: document.title,
      frame_selector: frameSelector(),
      element: el ? metaFor(el) : null
    });
  }

  // Password values never leave the page: the fill is recorded with an empty
  // value and masked=true so conversion emits a {{env.*}} placeholder instead.
  function isPasswordInput(el) {
    return el.tagName && el.tagName.toLowerCase() === 'input' &&
      (el.getAttribute('type') || 'text').toLowerCase() === 'password';
  }

  function recordFill(el) {
    const masked = isPasswordInput(el);
    record('fill', el, masked ? '' : el.value, masked);
  }

  function targetOf(ev) {
    const path = ev.composedPath ? ev.composedPath() : [ev.target];
    for (let i = 0; i < path.length; i++) {
      if (path[i] && path[i].nodeType === 1) return path[i];
    }
    return null;
  }

  function isToolbar(el) {
    return toolbarHost !== null && (el === toolbarHost || toolbarHost.contains(el));
  }

  // Prefer the interactive ancestor over a leaf node (e.g. the <span> inside a
  // <button>) so selectors target the element the user meant to act on.
  function interactiveAncestor(el) {
    if (!el.closest) return el;
    const root = el.getRootNode();
    let scoped = el;
    try {
      const found = el.closest('button, a[href], input, select, textarea, [role="button"], [role="link"], [role="tab"], [role="menuitem"], [role="checkbox"], [role="radio"], label, summary, [onclick]');
      if (found && found.getRootNode() === root) scoped = found;
    } catch (e) { /* keep leaf element */ }
    return scoped;
  }

  window.addEventListener('click', (ev) => {
    const raw = targetOf(ev);
    if (!raw || isToolbar(raw)) return;
    if (assertMode) {
      ev.preventDefault();
      ev.stopPropagation();
      assertMode = false;
      refreshToolbar();
      record('assert', raw, shortText(raw));
      return;
    }
    if (paused) return;
    const el = interactiveAncestor(raw);
    const tag = el.tagName ? el.tagName.toLowerCase() : '';
    if (tag === 'select') return; // the change listener records the selection
    if (tag === 'input' || tag === 'textarea') {
      const type = (el.getAttribute('type') || 'text').toLowerCase();
      const clickable = type === 'button' || type === 'submit' || type === 'reset' ||
        type === 'checkbox' || type === 'radio' || type === 'file';
      if (!clickable) return; // focus click before typing is noise
    }
    record('click', el, '');
  }, true);

  window.addEventListener('change', (ev) => {
    if (paused) return;
    const el = targetOf(ev);
    if (!el || isToolbar(el)) return;
    const tag = el.tagName.toLowerCase();
    if (tag === 'select') {
      record('select', el, el.value);
      return;
    }
    if (tag === 'input') {
      const type = (el.getAttribute('type') || 'text').toLowerCase();
      if (type === 'checkbox' || type === 'radio') return; // click already recorded
      if (type === 'file') {
        const names = Array.prototype.map.call(el.files || [], (f) => f.name).join(',');
        record('set_files', el, names);
        return;
      }
      recordFill(el);
      return;
    }
    if (tag === 'textarea') recordFill(el);
  }, true);

  window.addEventListener('keydown', (ev) => {
    if (paused) return;
    const el = targetOf(ev);
    if (!el || isToolbar(el)) return;
    if (ev.key !== 'Enter' && ev.key !== 'Escape' && ev.key !== 'Tab') return;
    const tag = el.tagName ? el.tagName.toLowerCase() : '';
    if (ev.key === 'Enter' && (tag === 'input' || tag === 'textarea')) {
      // The change event fires on blur, after navigation may have started —
      // capture the pending value now so Enter-submits keep their fill.
      recordFill(el);
    }
    record('press_key', el, ev.key);
  }, true);

  // --- Minimal toolbar (main frame only), isolated in a closed shadow root ---

  let refreshToolbar = () => {};

  function installToolbar() {
    if (window !== window.top || !document.body || toolbarHost) return;
    toolbarHost = document.createElement('div');
    toolbarHost.setAttribute('data-apexload-recorder', 'toolbar');
    const shadow = toolbarHost.attachShadow({ mode: 'closed' });
    shadow.innerHTML =
      '<style>' +
      ':host{all:initial}' +
      '.bar{position:fixed;bottom:16px;right:16px;z-index:2147483647;display:flex;gap:8px;' +
      'align-items:center;padding:8px 12px;border-radius:8px;background:#1c2333;color:#e6e9f0;' +
      'font:12px/1.4 system-ui,sans-serif;box-shadow:0 4px 16px rgba(0,0,0,.35)}' +
      'button{border:1px solid #3a4357;border-radius:6px;background:#2a3245;color:#e6e9f0;' +
      'padding:4px 10px;font:inherit;cursor:pointer}' +
      'button.active{background:#c2410c;border-color:#c2410c}' +
      '.dot{width:8px;height:8px;border-radius:50%;background:#ef4444}' +
      '.dot.paused{background:#9ca3af}' +
      '</style>' +
      '<div class="bar"><span class="dot"></span><span class="label">REC</span>' +
      '<button class="pause">Pause</button><button class="assert">Assert</button></div>';
    const dot = shadow.querySelector('.dot');
    const label = shadow.querySelector('.label');
    const pauseBtn = shadow.querySelector('.pause');
    const assertBtn = shadow.querySelector('.assert');
    refreshToolbar = () => {
      dot.className = 'dot' + (paused ? ' paused' : '');
      label.textContent = paused ? 'PAUSED' : (assertMode ? 'PICK ELEMENT' : 'REC');
      pauseBtn.textContent = paused ? 'Resume' : 'Pause';
      assertBtn.className = 'assert' + (assertMode ? ' active' : '');
    };
    pauseBtn.addEventListener('click', (ev) => {
      ev.stopPropagation();
      // Round-trip through Go: the recorder flips its authoritative state and
      // pushes it back into every page (so the control panel agrees).
      send({ kind: 'pause_toggle', ts: Date.now() });
    });
    assertBtn.addEventListener('click', (ev) => {
      ev.stopPropagation();
      if (paused) return;
      // Round-trip through Go so the control panel and every page agree on the
      // picker state (the recorder pushes it back via the setter below).
      send({ kind: 'pick_toggle', ts: Date.now() });
    });
    document.body.appendChild(toolbarHost);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', installToolbar);
  } else {
    installToolbar();
  }
})();
