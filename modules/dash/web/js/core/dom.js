// DOM helpers. Everything goes in as text: there is no innerHTML anywhere in this app.
// h() builds elements; morph() applies a freshly built tree onto the live one, so a poll
// changes only what changed and scroll, focus, hover and selection survive.

const SVGNS = "http://www.w3.org/2000/svg";

function set(e, props) {
  for (const k in props) {
    const v = props[k];
    if (v == null || v === false) continue;
    if (k.startsWith("on") && typeof v === "function") (e._on ||= {})[k.slice(2).toLowerCase()] = v;
    else if (k === "class") e.setAttribute("class", Array.isArray(v) ? v.filter(Boolean).join(" ") : v);
    else if (k === "value") { e._v = String(v); e.value = String(v); }
    else if (k === "checked") { e._c = !!v; e.checked = !!v; if (v) e.setAttribute("checked", ""); }
    else e.setAttribute(k, v === true ? "" : lang && typeof v === "string" && LANG_ATTRS.has(k) ? lang(v) : v);
  }
}
// The interface language (core/lang.js): a lookup for the app's own words, applied as elements are built, so a re-render never flips them back.
let lang = null;
const LANG_ATTRS = new Set(["placeholder", "title", "aria-label"]);
export const setLang = f => { lang = f; };
/** The same lookup for words set outside h() (setAttribute, textContent, title =). */
export const tr = t => lang && typeof t === "string" ? lang(t) : t;
let textMap = null;
/** Optional display rewrite for every text node (the names easter egg). */
export const setTextMap = f => { textMap = f; };
function add(e, kids) {
  for (const c of kids) {
    if (c == null || c === false || c === true) continue;
    if (Array.isArray(c)) add(e, c);
    else e.append(c.nodeType ? c : document.createTextNode(textMap ? textMap(lang ? lang(String(c)) : String(c)) : lang ? lang(String(c)) : String(c)));
  }
}

/** h("div", {class, style, onclick, "data-key"}, ...children) */
export function h(tag, props, ...kids) {
  const e = document.createElement(tag);
  if (props) set(e, props);
  add(e, kids);
  return e;
}
/** Same for SVG. */
export function s(tag, props, ...kids) {
  const e = document.createElementNS(SVGNS, tag);
  if (props) set(e, props);
  add(e, kids);
  return e;
}
/** A fragment-like array, flattened by h(). */
export const join = (items, sep) => items.flatMap((x, i) => (i ? [sep, x] : [x]));

// ---- morph ----
// Keys: children with data-key are matched by key (reordered, not rebuilt).
// data-own: the element manages its own children (charts, canvases); they are replaced only when data-sig changes.
// data-own="morph": long lists. Nothing is built or touched until data-sig changes; then the children are morphed by key.
export function morph(a, b) {
  if (a.nodeType !== b.nodeType || a.nodeName !== b.nodeName ||
      (a.nodeType === 1 && a.getAttribute("data-key") !== b.getAttribute("data-key"))) {
    a.replaceWith(b); return b;
  }
  if (a.nodeType === 3) { if (a.nodeValue !== b.nodeValue) a.nodeValue = b.nodeValue; return a; }
  if (a.nodeType !== 1) return a;
  const own = b.hasAttribute("data-own"), sigChanged = own && a.getAttribute("data-sig") !== b.getAttribute("data-sig");
  for (const { name } of [...a.attributes]) if (!b.hasAttribute(name) && name !== "data-ok" && name !== "data-fail") a.removeAttribute(name);   // those two are a copy button's moment of feedback, set by hand: a poll's re-render mustn't wipe them
  for (const { name, value } of b.attributes) if (a.getAttribute(name) !== value) a.setAttribute(name, value);
  a._on = b._on;
  if (b._v !== undefined && document.activeElement !== a && a.value !== b._v) a.value = b._v;
  if (b._c !== undefined && document.activeElement !== a) a.checked = b._c;
  if (own) { if (sigChanged) { if (b.getAttribute("data-own") === "morph") morphKids(a, b); else a.replaceChildren(...b.childNodes); } return a; }
  morphKids(a, b);
  return a;
}
function morphKids(a, b) {
  const next = [...b.childNodes], keyed = new Map(), kept = new Set();
  for (const c of next) if (c.nodeType === 1 && c.hasAttribute("data-key")) kept.add(c.getAttribute("data-key"));
  // Drop what's going first: otherwise the nodes after it get moved in front of it, and moving a node takes focus out of it.
  for (const c of [...a.childNodes]) if (c.nodeType === 1 && c.hasAttribute("data-key")) { const k = c.getAttribute("data-key"); if (kept.has(k)) keyed.set(k, c); else c.remove(); }
  let cur = a.firstChild;
  for (const nb of next) {
    const k = nb.nodeType === 1 ? nb.getAttribute("data-key") : null;
    let m = null;
    if (k != null) { m = keyed.get(k) || null; if (m) keyed.delete(k); }
    else if (cur && !(cur.nodeType === 1 && cur.hasAttribute("data-key"))) m = cur;
    if (m) { if (m !== cur) a.insertBefore(m, cur); cur = morph(m, nb).nextSibling; }
    else a.insertBefore(nb, cur);
  }
  while (cur) { const n = cur.nextSibling; cur.remove(); cur = n; }
}
/** Put tree into container, morphing over what is there. */
export function mount(container, tree) {
  if (container.firstChild && !container.firstChild.nextSibling) morph(container.firstChild, tree);
  else container.replaceChildren(tree);
}

// ---- events: one delegated listener per type; handlers live on nodes as _on and survive morph ----
const TYPES = ["click", "input", "change", "keydown", "submit", "paste", "dragstart", "dragend", "dragover", "dragleave", "drop", "focusout", "toggle", "pointerdown", "dblclick"];
export function delegate(root = document) {
  for (const type of TYPES) root.addEventListener(type, ev => {
    for (let n = ev.target; n && n !== root; n = n.parentNode) {
      const f = n._on && n._on[type];
      if (f) {
        if (type === "click" && n.tagName === "BUTTON" && n._going) { ev.preventDefault(); break; }   // its last click is still working: a double click can't send it twice
        const r = f(ev, n);
        if (type === "click" && n.tagName === "BUTTON" && r && typeof r.then === "function") {
          n._going = true;
          const done = () => { n._going = false; clearTimeout(cap); }, cap = setTimeout(done, 20000);   // a handler that never settles can't lock its button for good
          r.then(done, done);
        }
        if (ev.cancelBubble) break;
      }
    }
  }, type === "toggle"); // toggle doesn't bubble, so catch it on the way down
}

/** True while the person has text selected on the page: views hold still so the selection isn't lost. */
export function selecting() { const s = getSelection(); return !!(s && !s.isCollapsed && s.toString().trim()); }

/** Copy text; returns a promise. Falls back to a hidden textarea when the clipboard is refused; rejects with a plain message if both fail. */
export function copyText(t) {
  const old = () => {
    const x = h("textarea", { style: "position:fixed;opacity:0" }); x.value = t; document.body.append(x); x.select();
    let ok = false;
    try { ok = document.execCommand("copy"); } catch { ok = false; } finally { x.remove(); }
    return ok ? Promise.resolve() : Promise.reject(new Error("Couldn't copy. Select the text and copy it by hand."));
  };
  if (!navigator.clipboard) return old();
  return navigator.clipboard.writeText(t).catch(old);
}
