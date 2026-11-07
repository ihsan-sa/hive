// Side panel: an agent's page beside whatever view is open, drawn in its own container outside #main, so the view under it
// keeps polling and the panel is never morphed away. Same render as the full page, in a narrower container (it lays out
// as on a phone). Escape or a click on empty space closes it; "Full page" is a real link, and refs open in a new tab as usual.
// A thread opens in Comms, as it always has, so openPanel("thread", name) goes there. openSheet() puts any view's piece in
// the same panel (Flow's lane opens its Work card there): a draw function, drawn again on every store change, and its full page.
import { h, mount, selecting } from "../core/dom.js";
import { S, subscribe } from "../core/store.js";
import { agentHref, go, parse, threadHref } from "../core/router.js";
import { agentPage, loadFeed, pollAgent } from "../views/agent.js";
import { isAgent } from "../core/model.js";
import { button } from "./kit.js";

// Width per kind: an agent's page wants 640, a sheet (one Work card) 480; the grip's width is kept per kind too.
const PMIN = 380, DEFW = { agent: 640, sheet: 480 }, keyOf = () => P.kind === "sheet" ? "panel.w.sheet" : "panel.w";
const maxW = () => Math.max(PMIN, innerWidth - 240);
const clampW = (w) => Math.max(PMIN, Math.min(maxW(), Math.round(w)));
function setW(w, save) { w = clampW(w); document.documentElement.style.setProperty("--panel-w", w + "px"); if (save) try { localStorage.setItem(keyOf(), String(w)); } catch (e) {} return w; }
function applyW() { let w = 0; try { w = +localStorage.getItem(keyOf()); } catch (e) {} setW(w || DEFW[P.kind] || 640, false); }
function curW() { return P.root ? P.root.getBoundingClientRect().width : DEFW[P.kind] || 640; }
function grip() {
  const g = h("div", { class: "panel-grip", role: "separator", "aria-orientation": "vertical", "aria-label": "Resize panel", tabindex: 0, title: "Drag to resize, double-click to reset" });
  const move = (e) => setW(innerWidth - e.clientX, false);
  const up = () => { g.classList.remove("on"); removeEventListener("pointermove", move); removeEventListener("pointerup", up); removeEventListener("pointercancel", up); setW(curW(), true); };
  g.onpointerdown = (e) => { e.preventDefault(); g.classList.add("on"); addEventListener("pointermove", move); addEventListener("pointerup", up); addEventListener("pointercancel", up); };
  g.ondblclick = () => { try { localStorage.removeItem(keyOf()); } catch (e) {} applyW(); };
  g.onkeydown = (e) => { const d = e.key === "ArrowLeft" ? 24 : e.key === "ArrowRight" ? -24 : 0; if (d) { e.preventDefault(); setW(curW() + d, true); } };
  return g;
}
const P = { name: null, kind: null, draw: null, href: null, root: null, body: null, bar: null, grip: null, unsub: null, timer: 0, back: null };

function render(force) {
  if (!P.name || (!force && selecting())) return;
  mount(P.bar, h("div", { class: "panel-a" }, button("Open as a full page", null, { href: P.href, kind: "icon", icon: "expand" }), button("Close (Esc)", closePanel, { kind: "icon", icon: "close" })));
  mount(P.body, P.draw());
}
function ensure() {
  if (P.root) return;
  P.bar = h("div", { class: "panel-b" }); P.body = h("div");
  P.root = h("aside", { class: "panel", tabindex: -1, onclick: leaving }, P.bar, P.body);
  document.body.append(P.root);
  P.grip = grip(); document.body.append(P.grip);
  P.back = document.activeElement;
  P.unsub = subscribe(() => render(false));
  P.timer = setInterval(() => { if (!document.hidden && P.name && P.kind === "agent") pollAgent(P.name, refresh); }, 2000);
}
function show(kind, name, label, draw, href) {
  ensure();
  P.root.setAttribute("aria-label", label);
  const changed = P.kind !== kind || P.name !== name, newKind = P.kind !== kind;
  Object.assign(P, { kind, name, draw, href });
  if (newKind) applyW();
  document.body.setAttribute("data-panel", kind);   // css: a sheet moves the page over; an agent's page floats
  render(true);
  if (changed) P.root.scrollTop = 0;
  P.root.focus({ preventScroll: true });
  return changed;
}
const refresh = () => render(true);

export const panelOpen = () => !!P.name;

// A link to Comms or a Reply leaves the panel: otherwise, on a phone, it would cover the thread it just opened.
function leaving(e) {
  if (e.defaultPrevented || e.ctrlKey || e.metaKey || e.shiftKey) return;
  const a = e.target.closest("a"), to = a && a.getAttribute("href");
  if ((to && to.startsWith("#comms")) || e.target.closest(".msg-reply")) closePanel();
}

export function openPanel(kind, name) {
  if (kind !== "agent") { closePanel(); return go("comms", name); }
  if (!isAgent(name)) { closePanel(); location.hash = threadHref("@" + name); return; }  // someone outside: read their posts
  if (parse().id === "agent") return go("agent", name);
  if (show("agent", name, "Agent " + name, () => agentPage(P.name, "panel"), agentHref(name))) loadFeed(name, refresh);
}

/** A view's piece in the panel: draw() returns its node (drawn again on each store change); href is where it lives as a full page. */
export function openSheet(name, label, draw, href) { show("sheet", name, label, draw, href); }

export function closePanel() {
  if (!P.root) return;
  clearInterval(P.timer); P.unsub && P.unsub(); P.root.remove(); P.grip && P.grip.remove(); document.body.removeAttribute("data-panel");
  const back = P.back;
  Object.assign(P, { name: null, kind: null, draw: null, href: null, root: null, body: null, bar: null, unsub: null, timer: 0, back: null });
  if (back && back.isConnected && back.focus) back.focus({ preventScroll: true });
  dispatchEvent(new Event("panelclose"));
}

// Capture, so Escape closes the panel and doesn't also reach the view under it. A window open on top gets Escape first.
addEventListener("keydown", e => { if (e.key === "Escape" && P.root && !document.querySelector("dialog[open]")) { e.stopPropagation(); closePanel(); } }, true);
document.addEventListener("pointerdown", e => { if (P.root && e.target.closest && !e.target.closest(".panel,.panel-grip,button,a,input,textarea,select,label,dialog,canvas,svg,summary")) closePanel(); });
addEventListener("hashchange", () => { if (parse().id === "agent") closePanel(); });
