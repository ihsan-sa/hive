// Side panel: an agent's page beside whatever view is open, drawn in its own container outside #main, so the view under it
// keeps polling and the panel is never morphed away. Same render as the full page, in a narrower container (it lays out
// as on a phone). Escape or a click on empty space closes it; "Full page" is a real link, and refs open in a new tab as usual.
// A thread opens in Comms, as it always has, so openPanel("thread", name) goes there.
import { h, mount, selecting } from "../core/dom.js";
import { S, subscribe } from "../core/store.js";
import { agentHref, go, parse, threadHref } from "../core/router.js";
import { agentPage, loadFeed, pollAgent } from "../views/agent.js";
import { isAgent } from "../core/model.js";
import { button } from "./kit.js";

const P = { name: null, root: null, body: null, bar: null, unsub: null, timer: 0, back: null };

function render(force) {
  if (!P.name || (!force && selecting())) return;
  mount(P.bar, h("div", { class: "panel-a" }, button("Full page", null, { href: agentHref(P.name), size: "s", kind: "quiet", title: "Open as a full page" }), button("Close", closePanel, { size: "s", kind: "quiet", title: "Close (Esc)" })));
  mount(P.body, agentPage(P.name, "panel"));
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
  if (!P.root) {
    P.bar = h("div", { class: "panel-b" }); P.body = h("div");
    P.root = h("aside", { class: "panel", tabindex: -1, onclick: leaving }, P.bar, P.body);
    document.body.append(P.root);
    P.back = document.activeElement;
    P.unsub = subscribe(() => render(false));
    P.timer = setInterval(() => { if (!document.hidden && P.name) pollAgent(P.name, refresh); }, 2000);
  }
  P.root.setAttribute("aria-label", "Agent " + name);
  const changed = P.name !== name;
  P.name = name;
  render(true);
  if (changed) P.root.scrollTop = 0;
  P.root.focus({ preventScroll: true });
  if (changed) loadFeed(name, refresh);
}

export function closePanel() {
  if (!P.root) return;
  clearInterval(P.timer); P.unsub && P.unsub(); P.root.remove();
  const back = P.back;
  Object.assign(P, { name: null, root: null, body: null, bar: null, unsub: null, timer: 0, back: null });
  if (back && back.isConnected && back.focus) back.focus({ preventScroll: true });
  dispatchEvent(new Event("panelclose"));
}

// Capture, so Escape closes the panel and doesn't also reach the view under it. A window open on top gets Escape first.
addEventListener("keydown", e => { if (e.key === "Escape" && P.root && !document.querySelector("dialog[open]")) { e.stopPropagation(); closePanel(); } }, true);
document.addEventListener("pointerdown", e => { if (P.root && e.target.closest && !e.target.closest(".panel,button,a,input,textarea,select,label,dialog,canvas,svg,summary")) closePanel(); });
addEventListener("hashchange", () => { if (parse().id === "agent") closePanel(); });
