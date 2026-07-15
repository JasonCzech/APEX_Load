// APEX Load embedded recorder capture script.
// Injected into every frame via AddInitScript. Reports structured interaction
// events to Go through the exposed window.__apexloadRecord binding. Uses
// composedPath() so interactions inside open shadow roots resolve to the real
// target element, and computes shadow-piercing CSS paths (Playwright's CSS
// engine pierces open shadow roots; XPath does not, so XPath is omitted for
// shadow elements).
(() => {
  'use strict';
  function isRecorderControlPanelPage() {
    try {
      const fragment = String(window.location.hash || '').replace(/^#\/?/, '');
      return fragment === 'recorder-panel' || fragment.indexOf('recorder-panel?') === 0 || fragment.indexOf('recorder-panel/') === 0;
    } catch (e) {
      return false;
    }
  }
  if (isRecorderControlPanelPage()) return;
  if (window.__apexloadRecorderInstalled) return;
  window.__apexloadRecorderInstalled = true;

  let paused = false;
  let assertMode = false;
  let toolbarHost = null;
  let pendingClick = null;
  let pendingHover = null;
  let pendingDrag = null;
  let windowScrollState = null;
  const elementScrollStates = new WeakMap();
  const editableFillStates = new WeakMap();
  let lastRecordedFill = null;
  let lastRecordedHover = null;
  let lastEnterPressAt = 0;

  // Live recording state the Go recorder pushes into the page for the advanced
  // sidebar (counts, active section, open transactions, recent steps). Updated
  // via __apexloadRecorderSetState; rendered by the panel when expanded.
  let sidebarState = {
    section: 'actions',
    stepCount: 0,
    navCount: 0,
    openTransactions: [],
    recentSteps: []
  };

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

  // The Go recorder pushes the full live snapshot here on every relevant change
  // (debounced server-side). It is authoritative for pause/pick too, so a freshly
  // navigated document or a new tab hydrates correctly from a single call.
  window.__apexloadRecorderSetState = (json) => {
    try {
      const next = JSON.parse(json);
      if (next && typeof next === 'object') {
        sidebarState = next;
        if (typeof next.paused === 'boolean') paused = next.paused;
        if (typeof next.picking === 'boolean') assertMode = next.picking && !paused;
      }
    } catch (e) { return; }
    refreshToolbar();
  };

  const TEST_ATTRS = ['data-testid', 'data-test-id', 'data-test', 'data-cy', 'data-qa'];
  const SAFE_IDENT = /^[A-Za-z_][A-Za-z0-9_-]*$/;

  // Per-session secret substituted by the Go recorder at injection time. It
  // lives only in this closure, so arbitrary page script (which can still reach
  // window.__apexloadRecord) cannot read it and therefore cannot forge a
  // destructive control event (stop/delete/exclude/mark) that the recorder will
  // accept.
  const CONTROL_NONCE = '__APEXLOAD_CONTROL_NONCE__';

  // Capture these before application JavaScript runs. Looking the binding or
  // JSON.stringify up dynamically would let the recorded page replace them,
  // observe the closure nonce on a legitimate event, and then forge steps.
  const RECORD_BINDING = typeof window.__apexloadRecord === 'function'
    ? window.__apexloadRecord.bind(window) : null;
  const SAFE_JSON_STRINGIFY = JSON.stringify.bind(JSON);
  const CONTROL_NONCE_SUFFIX = ',"nonce":' + SAFE_JSON_STRINGIFY(CONTROL_NONCE) + '}';

  function send(payload) {
    if (!RECORD_BINDING || !payload || typeof payload !== 'object') return;
    // Serialize the page-derived payload before appending the secret. This
    // prevents a hostile Object.prototype setter/toJSON hook from observing
    // CONTROL_NONCE while retaining ordinary JSON payload compatibility.
    const body = SAFE_JSON_STRINGIFY(payload);
    if (typeof body !== 'string' || body.length < 2 || body.charAt(0) !== '{' || body.charAt(body.length - 1) !== '}') return;
    const serialized = body.substring(0, body.length - 1) + CONTROL_NONCE_SUFFIX;
    // Dispatch immediately (fire-and-forget) rather than chaining through a
    // single promise. Chaining deferred each binding call until the previous
    // event's full round-trip to the Go recorder resolved, so any events still
    // queued when a navigation tore down this JS context were silently dropped —
    // exactly the pending fill/click that the beforeunload/pagehide flush exists
    // to preserve. Dispatching now puts every event in flight before teardown;
    // the Go handler serializes them under its own lock so ordering is kept.
    try {
      const result = RECORD_BINDING(serialized);
      if (result && typeof result.catch === 'function') result.catch(() => {});
    } catch (e) {
      /* binding torn down mid-navigation; nothing more we can do */
    }
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

  // safeMetaFor wraps metaFor so a DOM exception while collecting element
  // metadata (detached node, cross-origin frame, exotic element state) degrades
  // to a null element instead of throwing out of the event handler and silently
  // dropping the entire recorded step.
  function safeMetaFor(el) {
    try {
      return metaFor(el);
    } catch (e) {
      return null;
    }
  }

  function frameSelectorForElement(fe) {
    if (!fe) return '';
    const tag = (fe.tagName || 'iframe').toLowerCase();
    if (fe.id && SAFE_IDENT.test(fe.id)) return tag + '#' + fe.id;
    const name = fe.getAttribute('name');
    if (name) return tag + '[name="' + escAttr(name) + '"]';
    const title = fe.getAttribute('title');
    if (title) return tag + '[title="' + escAttr(title) + '"]';
    const src = fe.getAttribute('src');
    if (src) return tag + '[src="' + escAttr(src) + '"]';
    try {
      // Build a per-parent CSS path (segmentFor counts :nth-of-type among the
      // element's own siblings). A document-wide index — `querySelectorAll(tag)`
      // then :nth-of-type(N) — is wrong: :nth-of-type counts within a parent, so
      // that selector matches a different frame (or nothing) whenever same-tag
      // frames live under different parents.
      const root = fe.ownerDocument.documentElement || fe.ownerDocument.body;
      const path = cssPathWithin(fe, root);
      if (path) return path;
    } catch (e) {}
    return tag;
  }

  // Same-origin frames identify themselves via frameElement; cross-origin
  // frames are resolved on the Go side from the binding source frame URL.
  function frameSelector() {
    try {
      return frameSelectorForElement(window.frameElement);
    } catch (e) {
      return '';
    }
  }

  function framePath() {
    const path = [];
    let win = window;
    for (let guard = 0; guard < 20; guard++) {
      try {
        if (!win || win === win.parent) break;
        const selector = frameSelectorForElement(win.frameElement);
        if (!selector) break;
        path.unshift(selector);
        win = win.parent;
      } catch (e) {
        break;
      }
    }
    return path;
  }

  function capturePayload(kind, el, value, masked, extra) {
    const payload = {
      kind: kind,
      value: value || '',
      masked: !!masked,
      ts: Date.now(),
      url: location.href,
      title: document.title,
      frame_selector: frameSelector(),
      frame_path: framePath(),
      element: el ? safeMetaFor(el) : null
    };
    if (extra && typeof extra === 'object') Object.assign(payload, extra);
    return payload;
  }

  function record(kind, el, value, masked, extra) {
    const payload = capturePayload(kind, el, value, masked, extra);
    send(payload);
  }

  function currentWindowScroll() {
    return {
      x: Math.round(window.scrollX || window.pageXOffset || 0),
      y: Math.round(window.scrollY || window.pageYOffset || 0)
    };
  }

  windowScrollState = Object.assign(currentWindowScroll(), { dx: 0, dy: 0, timer: null });

  function scrollEventElement(ev) {
    const target = ev && ev.target;
    if (!target || target === window || target === document ||
        target === document.documentElement || target === document.body ||
        target === document.scrollingElement) {
      return null;
    }
    return target.nodeType === 1 ? target : null;
  }

  function flushScroll(el, state) {
    if (state.timer) {
      clearTimeout(state.timer);
      state.timer = null;
    }
    const dx = Math.round(state.dx || 0);
    const dy = Math.round(state.dy || 0);
    state.dx = 0;
    state.dy = 0;
    if (paused || (!dx && !dy)) return;
    record('scroll', el, '', false, {
      delta_x: dx,
      delta_y: dy,
      scroll_x: Math.round(state.x || 0),
      scroll_y: Math.round(state.y || 0)
    });
  }

  function scheduleScroll(el, state) {
    if (state.timer) clearTimeout(state.timer);
    state.timer = setTimeout(() => flushScroll(el, state), 250);
  }

  // Password values never leave the page: the fill is recorded with an empty
  // value and masked=true so conversion emits a {{env.*}} placeholder instead.
  function isPasswordInput(el) {
    return el.tagName && el.tagName.toLowerCase() === 'input' &&
      (el.getAttribute('type') || 'text').toLowerCase() === 'password';
  }

  function isTextEntryControl(el) {
    if (!el || !el.tagName) return false;
    if (isEditableContent(el)) return true;
    const tag = el.tagName.toLowerCase();
    if (tag === 'textarea') return true;
    if (tag !== 'input') return false;
    const type = (el.getAttribute('type') || 'text').toLowerCase();
    return type !== 'button' && type !== 'submit' && type !== 'reset' &&
      type !== 'checkbox' && type !== 'radio' && type !== 'file' &&
      type !== 'image';
  }

  function isEditableContent(el) {
    return !!(el && (el.isContentEditable ||
      (el.getAttribute && String(el.getAttribute('contenteditable') || '').toLowerCase() === 'true')));
  }

  function isExplicitEditableHost(el) {
    if (!el || !el.getAttribute) return false;
    const value = el.getAttribute('contenteditable');
    return value !== null && String(value).toLowerCase() !== 'false';
  }

  function contentEditableHost(el) {
    if (!isEditableContent(el)) return null;
    let cur = el;
    while (cur && cur.nodeType === 1) {
      if (isExplicitEditableHost(cur)) return cur;
      cur = cur.parentElement;
    }
    return el;
  }

  function fillSnapshot(el) {
    const masked = isPasswordInput(el);
    if (isEditableContent(el)) {
      return { value: String(el.innerText || el.textContent || ''), masked: false };
    }
    return { value: masked ? '' : String(el.value || ''), masked: masked };
  }

  function elementFingerprint(el) {
    const meta = el ? safeMetaFor(el) : null;
    if (!meta) return '';
    return meta.css_path || meta.id || meta.name ||
      [meta.tag, meta.type, meta.aria_label, meta.placeholder].join('|');
  }

  function fillPayload(el, source) {
    el = contentEditableHost(el) || el;
    const snapshot = fillSnapshot(el);
    const payload = capturePayload('fill', el, snapshot.value, snapshot.masked);
    lastRecordedFill = {
      key: elementFingerprint(el),
      value: snapshot.value,
      masked: snapshot.masked,
      source: source || 'change',
      ts: Date.now()
    };
    return payload;
  }

  function recordFill(el, source) {
    send(fillPayload(el, source));
  }

  function shouldSkipDuplicateFill(el) {
    if (!lastRecordedFill || lastRecordedFill.source !== 'keydown') return false;
    const key = elementFingerprint(el);
    if (!key || key !== lastRecordedFill.key) return false;
    const snapshot = fillSnapshot(el);
    const age = Date.now() - lastRecordedFill.ts;
    return age >= 0 && age <= 1000 &&
      snapshot.value === lastRecordedFill.value &&
      snapshot.masked === lastRecordedFill.masked;
  }

  function scheduleEditableFill(el, source) {
    const host = contentEditableHost(el);
    if (!host) return;
    const existing = editableFillStates.get(host);
    if (existing && existing.timer) clearTimeout(existing.timer);
    editableFillStates.set(host, {
      source: source || 'input',
      timer: setTimeout(() => {
        editableFillStates.delete(host);
        if (!shouldSkipDuplicateFill(host)) recordFill(host, source || 'input');
      }, 350)
    });
  }

  function takeEditableFill(el, source) {
    const host = contentEditableHost(el);
    if (!host) return null;
    const existing = editableFillStates.get(host);
    if (existing && existing.timer) clearTimeout(existing.timer);
    editableFillStates.delete(host);
    if (shouldSkipDuplicateFill(host)) return null;
    return fillPayload(host, source || (existing && existing.source) || 'focusout');
  }

  function flushEditableFill(el, source) {
    const payload = takeEditableFill(el, source);
    if (payload) send(payload);
  }

  function shouldSuppressKeyboardClick(ev) {
    if (!lastEnterPressAt || ev.detail !== 0) return false;
    const age = Date.now() - lastEnterPressAt;
    return age >= 0 && age <= 700;
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
      const found = el.closest('button, a[href], input, select, textarea, [contenteditable], [role="button"], [role="link"], [role="tab"], [role="menuitem"], [role="checkbox"], [role="radio"], label, summary, [onclick]');
      if (found && found.getRootNode() === root) scoped = found;
    } catch (e) { /* keep leaf element */ }
    return scoped;
  }

  function mouseButtonName(ev, fallback) {
    switch (ev && ev.button) {
      case 1: return 'middle';
      case 2: return 'right';
      case 0: return fallback || '';
      default: return fallback || '';
    }
  }

  function eventModifiers(ev) {
    const out = [];
    if (ev.altKey) out.push('Alt');
    if (ev.ctrlKey) out.push('Control');
    if (ev.metaKey) out.push('Meta');
    if (ev.shiftKey) out.push('Shift');
    return out;
  }

  function pointerExtra(ev, fallbackButton) {
    const extra = {};
    const button = mouseButtonName(ev, fallbackButton);
    const modifiers = eventModifiers(ev);
    if (button && button !== 'left') extra.button = button;
    if (modifiers.length) extra.modifiers = modifiers;
    return extra;
  }

  function selectedValues(el) {
    if (!el || !el.selectedOptions) return [];
    return Array.prototype.map.call(el.selectedOptions, (opt) => String(opt.value || opt.text || ''))
      .filter((value) => value !== '');
  }

  function cancelPendingClick() {
    if (!pendingClick) return;
    clearTimeout(pendingClick.timer);
    pendingClick = null;
  }

  function cancelPendingHover() {
    if (!pendingHover) return;
    clearTimeout(pendingHover.timer);
    pendingHover = null;
  }

  function shouldSkipDuplicateHover(el) {
    if (!lastRecordedHover) return false;
    const key = elementFingerprint(el);
    if (!key || key !== lastRecordedHover.key) return false;
    const age = Date.now() - lastRecordedHover.ts;
    return age >= 0 && age <= 2000;
  }

  function scheduleHover(el) {
    if (!el || paused || assertMode || shouldSkipDuplicateHover(el)) return;
    const key = elementFingerprint(el);
    if (!key) return;
    if (pendingHover && pendingHover.key === key) return;
    cancelPendingHover();
    pendingHover = {
      el: el,
      key: key,
      timer: setTimeout(() => {
        const pending = pendingHover;
        pendingHover = null;
        if (!pending || paused || assertMode) return;
        record('hover', pending.el, '');
        lastRecordedHover = { key: pending.key, ts: Date.now() };
      }, 500)
    };
  }

  function flushPendingClick() {
    if (!pendingClick) return;
    const pending = pendingClick;
    pendingClick = null;
    clearTimeout(pending.timer);
    // Stamp the original click time, not the flush time: the click is deferred up
    // to 250ms (to coalesce double-clicks), so a fill/keypress landing inside that
    // window would otherwise carry an earlier ts and be ordered BEFORE the click
    // in the generated DSL.
    if (!paused) record('click', pending.el, '', false, Object.assign({ ts: pending.ts }, pending.extra || {}));
  }

  function scheduleClick(el, extra) {
    if (pendingClick) {
      if (pendingClick.el !== el || JSON.stringify(pendingClick.extra || {}) !== JSON.stringify(extra || {})) {
        flushPendingClick();
      } else {
        cancelPendingClick();
      }
    }
    pendingClick = {
      el: el,
      extra: extra || {},
      ts: Date.now(),
      timer: setTimeout(() => {
        const pending = pendingClick;
        pendingClick = null;
        if (pending && !paused) record('click', pending.el, '', false, Object.assign({ ts: pending.ts }, pending.extra || {}));
      }, 250)
    };
  }

  window.addEventListener('click', (ev) => {
    if (!ev.isTrusted) return;
    cancelPendingHover();
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
    if (shouldSuppressKeyboardClick(ev)) return;
    const el = interactiveAncestor(raw);
    const tag = el.tagName ? el.tagName.toLowerCase() : '';
    if (tag === 'select') return; // the change listener records the selection
    if (tag === 'input' || tag === 'textarea') {
      const type = (el.getAttribute('type') || 'text').toLowerCase();
      const clickable = type === 'button' || type === 'submit' || type === 'reset' ||
        type === 'checkbox' || type === 'radio' || type === 'file';
      if (!clickable) return; // focus click before typing is noise
    }
    scheduleClick(el, pointerExtra(ev, 'left'));
  }, true);

  window.addEventListener('dblclick', (ev) => {
    if (!ev.isTrusted) return;
    cancelPendingHover();
    const raw = targetOf(ev);
    if (!raw || isToolbar(raw) || paused || assertMode) return;
    const el = interactiveAncestor(raw);
    cancelPendingClick();
    record('dblclick', el, '', false, pointerExtra(ev, 'left'));
  }, true);

  window.addEventListener('contextmenu', (ev) => {
    if (!ev.isTrusted) return;
    cancelPendingHover();
    const raw = targetOf(ev);
    if (!raw || isToolbar(raw) || paused || assertMode) return;
    cancelPendingClick();
    record('click', interactiveAncestor(raw), '', false, pointerExtra(ev, 'right'));
  }, true);

  window.addEventListener('auxclick', (ev) => {
    if (!ev.isTrusted) return;
    cancelPendingHover();
    if (ev.button !== 1) return;
    const raw = targetOf(ev);
    if (!raw || isToolbar(raw) || paused || assertMode) return;
    cancelPendingClick();
    record('click', interactiveAncestor(raw), '', false, pointerExtra(ev, 'middle'));
  }, true);

  window.addEventListener('dragstart', (ev) => {
    if (!ev.isTrusted) return;
    cancelPendingHover();
    if (paused || assertMode) return;
    const raw = targetOf(ev);
    if (!raw || isToolbar(raw)) return;
    pendingDrag = { el: interactiveAncestor(raw), ts: Date.now() };
  }, true);

  window.addEventListener('drop', (ev) => {
    if (!ev.isTrusted) return;
    if (paused || assertMode) return;
    const raw = targetOf(ev);
    if (!raw || isToolbar(raw) || !pendingDrag) return;
    const age = Date.now() - pendingDrag.ts;
    if (age >= 0 && age <= 10000) {
      record('drag_and_drop', pendingDrag.el, '', false, {
        target_element: safeMetaFor(interactiveAncestor(raw))
      });
    }
    pendingDrag = null;
  }, true);

  window.addEventListener('dragend', (ev) => {
    if (!ev.isTrusted) return;
    pendingDrag = null;
  }, true);

  window.addEventListener('mouseover', (ev) => {
    if (!ev.isTrusted) return;
    if (paused || assertMode) return;
    const raw = targetOf(ev);
    if (!raw || isToolbar(raw)) return;
    scheduleHover(interactiveAncestor(raw));
  }, true);

  window.addEventListener('mouseout', (ev) => {
    if (!ev.isTrusted) return;
    if (!pendingHover) return;
    if (!ev.relatedTarget) {
      cancelPendingHover();
    }
  }, true);

  window.addEventListener('scroll', (ev) => {
    if (!ev.isTrusted) return;
    if (paused || assertMode) return;
    const el = scrollEventElement(ev);
    if (el && isToolbar(el)) return;
    if (!el) {
      const next = currentWindowScroll();
      windowScrollState.dx += next.x - windowScrollState.x;
      windowScrollState.dy += next.y - windowScrollState.y;
      windowScrollState.x = next.x;
      windowScrollState.y = next.y;
      scheduleScroll(null, windowScrollState);
      return;
    }
    let state = elementScrollStates.get(el);
    const next = {
      x: Math.round(el.scrollLeft || 0),
      y: Math.round(el.scrollTop || 0)
    };
    if (!state) {
      // Seed from the element's current scroll position, not {0,0}: an element
      // may already be scrolled (browser scroll restoration, anchor jump, an
      // app-initialized virtual list) before the user first touches it, and
      // seeding at 0 would record that entire pre-existing offset as the first
      // scroll delta and make replay over-scroll the container.
      state = { x: next.x, y: next.y, dx: 0, dy: 0, timer: null };
      elementScrollStates.set(el, state);
    }
    state.dx += next.x - state.x;
    state.dy += next.y - state.y;
    state.x = next.x;
    state.y = next.y;
    scheduleScroll(el, state);
  }, true);

  window.addEventListener('beforeunload', (ev) => { if (ev.isTrusted) flushPendingClick(); }, true);
  window.addEventListener('pagehide', (ev) => { if (ev.isTrusted) flushPendingClick(); }, true);

  window.addEventListener('change', (ev) => {
    if (!ev.isTrusted) return;
    if (paused) return;
    const el = targetOf(ev);
    if (!el || isToolbar(el)) return;
    const tag = el.tagName.toLowerCase();
    if (tag === 'select') {
      const values = selectedValues(el);
      record('select', el, el.value, false, values.length > 1 ? { values: values } : {});
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
      if (!shouldSkipDuplicateFill(el)) recordFill(el, 'change');
      return;
    }
    const editHost = contentEditableHost(el);
    if (tag === 'textarea' && !shouldSkipDuplicateFill(el)) recordFill(el, 'change');
    if (editHost) flushEditableFill(editHost, 'change');
  }, true);

  window.addEventListener('input', (ev) => {
    if (!ev.isTrusted) return;
    if (paused) return;
    const el = targetOf(ev);
    if (!el || isToolbar(el)) return;
    if (!contentEditableHost(el)) return;
    scheduleEditableFill(el, 'input');
  }, true);

  window.addEventListener('focusout', (ev) => {
    if (!ev.isTrusted) return;
    if (paused) return;
    const el = targetOf(ev);
    if (!el || isToolbar(el)) return;
    if (!contentEditableHost(el)) return;
    flushEditableFill(el, 'focusout');
  }, true);

  window.addEventListener('keydown', (ev) => {
    if (!ev.isTrusted) return;
    if (paused) return;
    const el = targetOf(ev);
    if (!el || isToolbar(el)) return;
    if (ev.key !== 'Enter' && ev.key !== 'Escape' && ev.key !== 'Tab') return;
    const tag = el.tagName ? el.tagName.toLowerCase() : '';
    const entryEl = contentEditableHost(el) || el;
    const events = [];
    if ((ev.key === 'Enter' || ev.key === 'Tab') && isTextEntryControl(entryEl)) {
      // The change event fires on blur, after navigation may have started —
      // capture the pending value now so Enter-submits and Tab moves keep it.
      // Send the fill and keypress in one binding call: Playwright may dispatch
      // separate fire-and-forget binding callbacks concurrently, which can
      // otherwise append the keypress before the fill despite this JS order.
      if (contentEditableHost(entryEl)) {
        const fill = takeEditableFill(entryEl, 'keydown');
        if (fill) events.push(fill);
      } else {
        events.push(fillPayload(entryEl, 'keydown'));
      }
    }
    if (ev.key === 'Enter') lastEnterPressAt = Date.now();
    events.push(capturePayload('press_key', entryEl, ev.key));
    if (events.length === 1) {
      send(events[0]);
    } else {
      send({ kind: 'capture_batch', events: events });
    }
  }, true);

  // --- Control panel (main frame only), isolated in a closed shadow root ---
  // Collapsed it is the minimal REC/Pause/Assert bar; expanded it is a
  // draggable, corner-snapping advanced sidebar (section / transactions / notes
  // / navigate / recent-steps). Every action round-trips through the
  // __apexloadRecord binding so the Go recorder stays authoritative, and the
  // recorder pushes live state back via __apexloadRecorderSetState. Because the
  // whole UI lives inside the closed shadow host, the capture listeners' existing
  // isToolbar() guard keeps its own clicks/keystrokes out of the recording.

  let refreshToolbar = () => {};

  const TOOLBAR_STYLE =
    ':host{all:initial}' +
    '.wrap{position:fixed;z-index:2147483647}' +
    '.wrap.corner-tl{top:16px;left:16px}.wrap.corner-tr{top:16px;right:16px}' +
    '.wrap.corner-bl{bottom:16px;left:16px}.wrap.corner-br{bottom:16px;right:16px}' +
    '.bar{display:flex;gap:8px;align-items:center;padding:8px 12px;border-radius:8px;' +
    'background:#1c2333;color:#e6e9f0;font:12px/1.4 system-ui,sans-serif;' +
    'box-shadow:0 4px 16px rgba(0,0,0,.35);cursor:grab}' +
    '.panel{display:none;flex-direction:column;gap:8px;width:300px;max-height:calc(100vh - 32px);' +
    'overflow:auto;padding:10px;border-radius:10px;background:#1c2333;color:#e6e9f0;' +
    'font:12px/1.4 system-ui,sans-serif;box-shadow:0 8px 28px rgba(0,0,0,.45)}' +
    '.wrap.dragging .bar,.wrap.dragging .phead{cursor:grabbing}' +
    'button{border:1px solid #3a4357;border-radius:6px;background:#2a3245;color:#e6e9f0;' +
    'padding:4px 8px;font:inherit;cursor:pointer}' +
    'button:hover{background:#333d54}' +
    'button.active,button.on{background:#c2410c;border-color:#c2410c}' +
    '.stopRec{background:#7f1d1d;border-color:#b91c1c;color:#fee2e2}' +
    '.stopRec:hover{background:#991b1b}' +
    '.dot{width:8px;height:8px;border-radius:50%;background:#ef4444;flex:0 0 auto}' +
    '.dot.paused{background:#9ca3af}' +
    '.phead{display:flex;gap:8px;align-items:center;cursor:grab;user-select:none}' +
    '.phead .title{font-weight:600}.spacer{flex:1}' +
    '.prow{display:flex;gap:6px}' +
    '.counts{color:#9aa4bd;font-size:11px}' +
    '.slabel{color:#9aa4bd;font-size:11px}' +
    '.section{display:flex;gap:4px;align-items:center;flex-wrap:wrap}' +
    '.row{display:flex;gap:6px}' +
    '.row input{flex:1;min-width:0;border:1px solid #3a4357;border-radius:6px;' +
    'background:#11161f;color:#e6e9f0;padding:4px 8px;font:inherit}' +
    '.txnActive{display:none;flex:1;min-width:0;border:1px solid #1f6f5c;border-radius:6px;' +
    'background:#11302a;color:#a7f3d0;padding:4px 8px;font:inherit;white-space:nowrap;' +
    'overflow:hidden;text-overflow:ellipsis}' +
    '.txnToggle.stop{background:#7f1d1d;border-color:#b91c1c;color:#fee2e2}' +
    '.txnToggle.stop:hover{background:#991b1b}' +
    '.steps{display:flex;flex-direction:column;gap:4px}' +
    '.stepsHead{display:flex;align-items:center;justify-content:space-between}' +
    '.stepsList{display:flex;flex-direction:column;gap:3px;max-height:200px;overflow:auto}' +
    '.step{display:flex;gap:6px;align-items:center;padding:3px 4px;border-radius:6px;background:#161c28}' +
    '.step.excluded{opacity:.5}.step .seq{color:#6b7790;flex:0 0 auto}' +
    '.step .kind{color:#cbd5e1;flex:0 0 auto;text-transform:uppercase;font-size:10px;letter-spacing:.04em}' +
    '.step .detail{flex:1;min-width:0;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}' +
    '.step .bdg{font-size:9px;color:#fbbf24;border:1px solid #5b4a1e;border-radius:4px;padding:0 3px;flex:0 0 auto}' +
    '.step .acts{display:flex;gap:3px;flex:0 0 auto}.step .acts button{padding:0 6px}' +
    '.empty{color:#6b7790;text-align:center;padding:6px 0}' +
    '.hint{color:#6b7790;font-size:10px;text-align:center}';

  const TOOLBAR_HTML =
    '<div class="wrap">' +
    '<div class="bar"><span class="dot"></span><span class="label">REC</span>' +
    '<button class="pause">Pause</button><button class="assert">Assert</button><button class="stopRec" title="Stop recording">Stop</button>' +
    '<button class="expand" title="Advanced controls">⋯</button></div>' +
    '<div class="panel">' +
    '<div class="phead"><span class="dot"></span><span class="title">APEX Recorder</span>' +
    '<span class="label"></span><span class="spacer"></span>' +
    '<button class="collapse" title="Collapse">▾</button></div>' +
    '<div class="prow"><button class="pause">Pause</button><button class="assert">Assert</button><button class="stopRec" title="Stop recording">Stop</button></div>' +
    '<div class="counts"></div>' +
    '<div class="section"><span class="slabel">Section</span>' +
    '<button data-section="init">Init</button><button data-section="actions">Main</button>' +
    '<button data-section="teardown">Teardown</button></div>' +
    '<div class="nav row"><input class="navUrl" type="text" placeholder="Navigate to URL…" />' +
    '<button class="navGo">Go</button></div>' +
    '<div class="txn"><div class="row"><input class="txnName" type="text" placeholder="Transaction name…" />' +
    '<span class="txnActive"></span><button class="txnToggle">Start</button></div></div>' +
    '<div class="note row"><input class="noteText" type="text" placeholder="Note for next action…" />' +
    '<button class="noteAdd">Add</button></div>' +
    '<div class="steps"><div class="stepsHead"><span class="slabel">Recent steps</span>' +
    '<button class="undo">Undo last</button></div><div class="stepsList"></div></div>' +
    '<div class="hint">Convert &amp; save from the APEX Load dashboard.</div>' +
    '</div></div>';

  function lsGet(key) { try { return localStorage.getItem('apexload_rec_' + key); } catch (e) { return null; } }
  function lsSet(key, val) { try { localStorage.setItem('apexload_rec_' + key, val); } catch (e) { /* ignore */ } }

  function installToolbar() {
    if (window !== window.top || !document.body || toolbarHost) return;

    const cfg = (window.__apexloadRecorderConfig && typeof window.__apexloadRecorderConfig === 'object')
      ? window.__apexloadRecorderConfig : {};
    const sidebarEnabled = cfg.sidebar !== false;
    let corner = lsGet('sidebar_corner') || cfg.corner || 'br';
    if (!/^[tb][lr]$/.test(corner)) corner = 'br';
    const storedOpen = lsGet('sidebar_open');
    let expanded = sidebarEnabled && (storedOpen === null ? cfg.open !== false : storedOpen === '1');

    toolbarHost = document.createElement('div');
    toolbarHost.setAttribute('data-apexload-recorder', 'toolbar');
    const shadow = toolbarHost.attachShadow({ mode: 'closed' });
    shadow.innerHTML = '<style>' + TOOLBAR_STYLE + '</style>' + TOOLBAR_HTML;

    const wrap = shadow.querySelector('.wrap');
    const bar = shadow.querySelector('.bar');
    const panel = shadow.querySelector('.panel');
    const phead = shadow.querySelector('.phead');
    const dots = shadow.querySelectorAll('.dot');
    const labels = shadow.querySelectorAll('.label');
    const pauseBtns = shadow.querySelectorAll('.pause');
    const assertBtns = shadow.querySelectorAll('.assert');
    const stopBtns = shadow.querySelectorAll('.stopRec');
    const expandBtn = shadow.querySelector('.expand');
    const collapseBtn = shadow.querySelector('.collapse');
    const counts = shadow.querySelector('.counts');
    const sectionBtns = shadow.querySelectorAll('.section [data-section]');
    const navUrl = shadow.querySelector('.navUrl');
    const navGo = shadow.querySelector('.navGo');
    const txnName = shadow.querySelector('.txnName');
    const txnActive = shadow.querySelector('.txnActive');
    const txnToggle = shadow.querySelector('.txnToggle');
    const noteText = shadow.querySelector('.noteText');
    const noteAdd = shadow.querySelector('.noteAdd');
    const undoBtn = shadow.querySelector('.undo');
    const stepsList = shadow.querySelector('.stepsList');

    if (!sidebarEnabled) { expandBtn.style.display = 'none'; expanded = false; }

    function applyCorner() {
      wrap.classList.remove('corner-tl', 'corner-tr', 'corner-bl', 'corner-br');
      wrap.classList.add('corner-' + corner);
    }

    function badge(text) { const b = document.createElement('span'); b.className = 'bdg'; b.textContent = text; return b; }

    function stepButton(text, title, on, handler) {
      const b = document.createElement('button');
      b.textContent = text;
      b.title = title;
      if (on) b.className = 'on';
      b.addEventListener('click', (ev) => { ev.stopPropagation(); handler(); });
      return b;
    }

    function activeTxnName() {
      const open = sidebarState.openTransactions || [];
      return open.length ? open[open.length - 1] : '';
    }

    function renderTxnToggle() {
      const current = activeTxnName();
      if (current) {
        txnName.style.display = 'none';
        txnName.disabled = true;
        txnActive.style.display = 'block';
        txnActive.textContent = current;
        txnActive.title = 'Open transaction: ' + current;
        txnToggle.textContent = 'Stop';
        txnToggle.title = 'Stop transaction "' + current + '"';
        txnToggle.className = 'txnToggle stop';
        return;
      }
      txnName.style.display = '';
      txnName.disabled = false;
      txnActive.style.display = 'none';
      txnActive.textContent = '';
      txnActive.title = '';
      txnToggle.textContent = 'Start';
      txnToggle.title = 'Start transaction';
      txnToggle.className = 'txnToggle';
    }

    function renderSteps() {
      stepsList.textContent = '';
      const steps = sidebarState.recentSteps || [];
      if (!steps.length) {
        const empty = document.createElement('div');
        empty.className = 'empty';
        empty.textContent = 'No steps recorded yet';
        stepsList.appendChild(empty);
        return;
      }
      steps.forEach((s) => {
        const row = document.createElement('div');
        row.className = 'step' + (s.excluded ? ' excluded' : '');
        const seq = document.createElement('span'); seq.className = 'seq'; seq.textContent = '#' + s.seq;
        const kind = document.createElement('span'); kind.className = 'kind'; kind.textContent = s.kind;
        const detail = document.createElement('span'); detail.className = 'detail'; detail.textContent = s.detail || '';
        row.appendChild(seq); row.appendChild(kind); row.appendChild(detail);
        if (s.masked) row.appendChild(badge('masked'));
        if (s.secret) row.appendChild(badge('secret'));
        if (s.param) row.appendChild(badge('param'));
        const acts = document.createElement('span'); acts.className = 'acts';
        acts.appendChild(stepButton(s.excluded ? '+' : '−', s.excluded ? 'Include step' : 'Exclude step', false, () => {
          send({ kind: 'step_exclude', seq: s.seq, excluded: !s.excluded, ts: Date.now() });
        }));
        if (s.fill) {
          acts.appendChild(stepButton('🔒', 'Mark as secret', s.secret, () => {
            send({ kind: 'step_mark', seq: s.seq, secret: !s.secret, param: s.param, ts: Date.now() });
          }));
          acts.appendChild(stepButton('≈', 'Mark as parameter', s.param, () => {
            send({ kind: 'step_mark', seq: s.seq, secret: s.secret, param: !s.param, ts: Date.now() });
          }));
        }
        row.appendChild(acts);
        stepsList.appendChild(row);
      });
    }

    function render() {
      bar.style.display = expanded ? 'none' : 'flex';
      panel.style.display = expanded ? 'flex' : 'none';
      if (!dragging) {
        wrap.style.left = ''; wrap.style.top = ''; wrap.style.right = ''; wrap.style.bottom = '';
        applyCorner();
      }
      const status = paused ? 'PAUSED' : (assertMode ? 'PICK ELEMENT' : 'REC');
      dots.forEach((d) => { d.className = 'dot' + (paused ? ' paused' : ''); });
      labels.forEach((l) => { l.textContent = status; });
      pauseBtns.forEach((b) => { b.textContent = paused ? 'Resume' : 'Pause'; });
      assertBtns.forEach((b) => { b.className = 'assert' + (assertMode ? ' active' : ''); });
      counts.textContent = (sidebarState.stepCount || 0) + ' steps · ' + (sidebarState.navCount || 0) + ' navigations';
      const active = sidebarState.section || 'actions';
      sectionBtns.forEach((b) => { b.className = b.getAttribute('data-section') === active ? 'on' : ''; });
      renderTxnToggle();
      renderSteps();
    }

    // --- Drag + corner snap (Gemini-style movable dock) ---
    let dragging = false, dragOffX = 0, dragOffY = 0;
    function clamp(v, min, max) { return v < min ? min : (v > max ? max : v); }
    function onPointerMove(ev) {
      const w = wrap.offsetWidth, h = wrap.offsetHeight;
      wrap.style.left = clamp(ev.clientX - dragOffX, 4, window.innerWidth - w - 4) + 'px';
      wrap.style.top = clamp(ev.clientY - dragOffY, 4, window.innerHeight - h - 4) + 'px';
    }
    function onPointerUp() {
      window.removeEventListener('pointermove', onPointerMove, true);
      window.removeEventListener('pointerup', onPointerUp, true);
      if (!dragging) return;
      dragging = false;
      wrap.classList.remove('dragging');
      const r = wrap.getBoundingClientRect();
      const cx = r.left + r.width / 2, cy = r.top + r.height / 2;
      corner = (cy < window.innerHeight / 2 ? 't' : 'b') + (cx < window.innerWidth / 2 ? 'l' : 'r');
      lsSet('sidebar_corner', corner);
      wrap.style.left = ''; wrap.style.top = ''; wrap.style.right = ''; wrap.style.bottom = '';
      applyCorner();
    }
    function startDrag(ev) {
      if (ev.button !== undefined && ev.button !== 0) return;
      const t = ev.target;
      if (t && (t.tagName === 'BUTTON' || t.tagName === 'INPUT')) return;
      ev.preventDefault();
      const r = wrap.getBoundingClientRect();
      dragOffX = ev.clientX - r.left;
      dragOffY = ev.clientY - r.top;
      wrap.style.left = r.left + 'px';
      wrap.style.top = r.top + 'px';
      wrap.style.right = 'auto';
      wrap.style.bottom = 'auto';
      wrap.classList.remove('corner-tl', 'corner-tr', 'corner-bl', 'corner-br');
      dragging = true;
      wrap.classList.add('dragging');
      window.addEventListener('pointermove', onPointerMove, true);
      window.addEventListener('pointerup', onPointerUp, true);
    }
    bar.addEventListener('pointerdown', startDrag);
    phead.addEventListener('pointerdown', startDrag);

    // --- Wire actions (all round-trip through the recorder binding) ---
    function setExpanded(v) { expanded = v; lsSet('sidebar_open', v ? '1' : '0'); render(); }
    expandBtn.addEventListener('click', (ev) => { ev.stopPropagation(); setExpanded(true); });
    collapseBtn.addEventListener('click', (ev) => { ev.stopPropagation(); setExpanded(false); });
    pauseBtns.forEach((b) => b.addEventListener('click', (ev) => { ev.stopPropagation(); send({ kind: 'pause_toggle', ts: Date.now() }); }));
    assertBtns.forEach((b) => b.addEventListener('click', (ev) => { ev.stopPropagation(); if (paused) return; send({ kind: 'pick_toggle', ts: Date.now() }); }));
    stopBtns.forEach((b) => b.addEventListener('click', (ev) => { ev.stopPropagation(); send({ kind: 'recording_stop', ts: Date.now() }); }));
    sectionBtns.forEach((b) => b.addEventListener('click', (ev) => { ev.stopPropagation(); send({ kind: 'section_set', section: b.getAttribute('data-section'), ts: Date.now() }); }));

    function doNavigate() { const u = navUrl.value.trim(); if (u) send({ kind: 'navigate', url: u, ts: Date.now() }); }
    navGo.addEventListener('click', (ev) => { ev.stopPropagation(); doNavigate(); });
    navUrl.addEventListener('keydown', (ev) => { if (ev.key === 'Enter') { ev.preventDefault(); doNavigate(); } });

    function doTxnToggle() {
      const current = activeTxnName();
      if (current) {
        send({ kind: 'transaction_end', name: current, ts: Date.now() });
        return;
      }
      const n = txnName.value.trim();
      if (n) {
        send({ kind: 'transaction_start', name: n, ts: Date.now() });
        txnName.value = '';
      }
    }
    txnToggle.addEventListener('click', (ev) => { ev.stopPropagation(); doTxnToggle(); });
    txnName.addEventListener('keydown', (ev) => { if (ev.key === 'Enter') { ev.preventDefault(); doTxnToggle(); } });

    function doNote() { const t = noteText.value.trim(); if (t) { send({ kind: 'note_add', text: t, ts: Date.now() }); noteText.value = ''; } }
    noteAdd.addEventListener('click', (ev) => { ev.stopPropagation(); doNote(); });
    noteText.addEventListener('keydown', (ev) => { if (ev.key === 'Enter') { ev.preventDefault(); doNote(); } });

    undoBtn.addEventListener('click', (ev) => { ev.stopPropagation(); send({ kind: 'delete_last_step', ts: Date.now() }); });

    refreshToolbar = render;
    render();
    document.body.appendChild(toolbarHost);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', installToolbar);
  } else {
    installToolbar();
  }
})();
