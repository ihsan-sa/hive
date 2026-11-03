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
    else e.setAttribute(k, v === true ? "" : v);
  }
}
function add(e, kids) {
  for (const c of kids) {
    if (c == null || c === false || c === true) continue;
    if (Array.isArray(c)) add(e, c);
    else e.append(c.nodeType ? c : document.createTextNode(String(c)));
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
  for (const { name } of [...a.attributes]) if (!b.hasAttribute(name)) a.removeAttribute(name);
  for (const { name, value } of b.attributes) if (a.getAttribute(name) !== value) a.setAttribute(name, value);
  a._on = b._on;
  if (b._v !== undefined && document.activeElement !== a && a.value !== b._v) a.value = b._v;
  if (b._c !== undefined && document.activeElement !== a) a.checked = b._c;
  if (own) { if (sigChanged) { if (b.getAttribute("data-own") === "morph") morphKids(a, b); else a.replaceChildren(...b.childNodes); } return a; }
  morphKids(a, b);
  return a;
}
function morphKids(a, b) {
  const next = [...b.childNodes], keyed = new Map();
  for (const c of a.childNodes) if (c.nodeType === 1 && c.hasAttribute("data-key")) keyed.set(c.getAttribute("data-key"), c);
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
const TYPES = ["click", "input", "change", "keydown", "submit", "paste", "dragover", "dragleave", "drop", "focusout"];
export function delegate(root = document) {
  for (const type of TYPES) root.addEventListener(type, ev => {
    for (let n = ev.target; n && n !== root; n = n.parentNode) {
      const f = n._on && n._on[type];
      if (f) { f(ev, n); if (ev.cancelBubble) break; }
    }
  });
}

/** True while the person has text selected on the page: views hold still so the selection isn't lost. */
export function selecting() { const s = getSelection(); return !!(s && !s.isCollapsed && s.toString().trim()); }

/** Copy text; returns a promise. */
export function copyText(t) {
  if (navigator.clipboard) return navigator.clipboard.writeText(t);
  const x = h("textarea", { style: "position:fixed;opacity:0" }); x.value = t; document.body.append(x); x.select();
  try { document.execCommand("copy"); } finally { x.remove(); }
  return Promise.resolve();
}
