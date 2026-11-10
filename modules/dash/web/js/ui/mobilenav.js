// The phone: a bottom bar (thumb reach) with a More sheet, and the phone-only behaviours (keyboard, status fold, press-and-hold).
// Shown only when the app is under 700px wide (css/phone.css, a container query on .app; wider, the bar is display:none).
// Reads the same registry and nav layout as the top bar (core/nav.js), so a new view needs no change here.
import { h } from "../core/dom.js";
import { S } from "../core/store.js";
import { href } from "../core/router.js";
import { layout, moreGroups } from "../core/nav.js";
import { simpleOn, setSimple } from "../core/simple.js";
import { getTheme, cycleTheme, THEMES } from "../core/theme.js";
import { icon } from "./kit.js";
import { HELP, startTour } from "./tour.js";
import { canFeedback, openFeedback } from "./feedback.js";
import { fullscreen } from "./shell.js";

const ST = { open: false, update: null };
const lab = v => (typeof v.label === "function" ? v.label() : v.label);
const GLYPH = { start: "plus", comms: "chat", now: "agents", usage: "usage", settings: "settings", knowledge: "board", yours: "star", inbox: "box", made: "file", mail: "link",
  agents: "agents", timeline: "clock", mesh: "orbit", ledger: "archive", progress: "lineage", flow: "bands", jobs: "dial", rootwarden: "shield", design: "edit" };
const SHORT = { comms: "Chat", now: "Agents" };
const BAR = ["comms", "now", "usage", "knowledge"];   // the design's four (Chat, Agents, Usage, Knowledge); More is the fifth
const DEV_MAIN = ["comms", "now", "agents", "usage"];
export function mobileNav({ views, current, nav, update }) {
  ST.update = update;
  const on = v => (typeof v.tab === "function" ? v.tab() : v.tab);
  const app = S.app.app && nav;
  let main, groups, active;
  if (app) {
    const L = layout(views, nav, on, current);
    active = L.active;
    const all = [...L.main, ...L.icons];
    main = BAR.map(id => all.find(v => v.id === id)).filter(Boolean);
    if (main.length < 2) main = all.filter(v => v.id !== "start").slice(0, 4);   // simple view: Chat and Settings
    groups = moreGroups(views, nav, on, L, main.map(v => v.id), lab);
  } else {
    const by = new Map(views.map(v => [v.id, v]));
    main = DEV_MAIN.map(id => by.get(id)).filter(Boolean);
    groups = [["Pages", views.filter(v => on(v) && !DEV_MAIN.includes(v.id))]];
    active = current === "agent" ? "now" : main.some(v => v.id === current) ? current : null;
  }
  const inMore = !active || !main.some(v => v.id === active);
  const shut = () => { ST.open = false; };
  const close = () => { shut(); update(); };
  const th = getTheme();
  const item = v => h("a", { class: "menu-i", role: "menuitem", href: href(v.id), "data-key": v.id, "aria-current": v.id === current ? "page" : null, onclick: shut },
    icon(GLYPH[v.id] || "more"), h("span", { class: "menu-i-t" }, v.id === "agents" && nav && nav.names && nav.names.agents || lab(v)));   // "All agents", the desktop More menu's word (R10 #10)
  const act = (key, label, run, ic, right) => h("button", { type: "button", class: "menu-i", role: "menuitem", "data-key": key, onclick: run }, icon(ic), h("span", { class: "menu-i-t" }, label), right ? h("span", { class: "menu-i-r" }, right) : null);
  const tab = v => { const b = v.badge ? v.badge() : null;
    return h("a", { class: "bnav-i", href: href(v.id), "data-key": v.id, "aria-current": v.id === active ? "page" : null, onclick: shut },
      icon(GLYPH[v.id] || "agents"), h("span", { class: "bnav-l" }, (app && SHORT[v.id]) || lab(v)), b ? h("span", { class: "bnav-n", "aria-label": `${b} for you` }, b) : null); };
  return h("div", { class: "bnav-w", "data-open": ST.open ? "" : null },
    ST.open ? h("button", { type: "button", class: "scrim", "data-dim": "", "data-key": "scrim", "aria-label": "Close", tabindex: "-1", onclick: close }) : null,
    ST.open ? h("div", { class: "menu bnav-sheet", "data-key": "sheet", role: "menu", "aria-label": "More" },
      groups.map(([title, vs], i) => [i ? h("div", { class: "menu-sep", "data-key": "s" + i }) : null, title ? h("div", { class: "menu-h", "data-key": "h" + i }, title) : null, vs.map(item)]),
      h("div", { class: "menu-sep", "data-key": "s-help" }),
      h("div", { class: "menu-h", "data-key": "h-help" }, "Help and look"),
      act("theme", `Colour theme: ${THEMES.find(x => x[0] === th)[1]}`, () => { cycleTheme(); update(); }, th === "dark" ? "moon" : th === "light" ? "sun" : "auto", "tap to change"),
      fullscreen.can() ? act("fullscreen", fullscreen.on() ? "Leave full screen" : "Full screen", () => { close(); fullscreen.toggle(); }, "expand") : null,
      app ? act("simple", simpleOn() ? "Show everything" : "Simple view", () => { close(); setSimple(!simpleOn()); update(); }, "expand", simpleOn() ? "all the pages" : "just the chat") : null,
      app ? act("tour", "Take the tour", () => { close(); startTour(); }, "play") : null,
      app && canFeedback() ? act("feedback", "Send feedback", () => { close(); openFeedback(); }, "reply") : null,
      h("a", { class: "menu-i", role: "menuitem", href: HELP, target: "_blank", rel: "noopener", "data-key": "help" }, icon("info"), h("span", { class: "menu-i-t" }, "User guide"))) : null,
    h("nav", { class: "bnav", "data-key": "bar", "aria-label": "Main" }, main.map(tab),
      h("button", { type: "button", class: "bnav-i", "data-key": "more", "aria-haspopup": "menu", "aria-expanded": String(ST.open), "aria-current": inMore && !ST.open ? "page" : null, onclick: () => { ST.open = !ST.open; update(); } },
        icon("more"), h("span", { class: "bnav-l" }, "More"))));
}

/** The view, on the root: css/phone.css hides both bars in a conversation (data-view=thread) and a file. */
export function setView(id, arg) {
  const b = document.body, thread = id === "comms" && !!arg && !String(arg).startsWith("focus:") && arg !== "~list" && arg !== "~new";   // a file (.kb[data-in]) is caught in the CSS
  const v = thread ? "thread" : id;
  if (b.dataset.view !== v) b.dataset.view = v;
}

if (typeof document !== "undefined") {
  addEventListener("hashchange", () => { ST.open = false; });
  addEventListener("keydown", e => { if (e.key === "Escape" && ST.open && ST.update) { ST.open = false; ST.update(); } });

  // The keyboard. Android (interactive-widget=resizes-content, index.html) shrinks the page itself; iOS leaves it and shrinks
  // only the visual viewport. Either way: a text field has focus and the room left is clearly less than the tallest seen.
  // Then data-kb=open on the root (the top and bottom bars hide, the strip folds) and --vvh is the height left, so the
  // conversation and the composer sit on the keyboard.
  const vv = window.visualViewport, root = document.body;
  let tall = innerHeight;
  const typing = () => { const a = document.activeElement; return !!a && (a.isContentEditable || a.tagName === "TEXTAREA" || (a.tagName === "INPUT" && !/^(button|checkbox|radio|range|submit|reset|file|color)$/.test(a.type))); };
  const kb = () => {
    const vh = vv ? vv.height : innerHeight;
    if (!typing()) tall = Math.max(innerHeight, vh);
    const open = typing() && matchMedia("(pointer:coarse), (max-width:699px)").matches && vh < tall - 120;
    if (open) { root.style.setProperty("--vvh", Math.round(vh) + "px"); if (root.dataset.kb !== "open") root.dataset.kb = "open"; if (vv && vv.offsetTop) scrollTo(0, 0); }
    else if (root.dataset.kb) { delete root.dataset.kb; root.style.removeProperty("--vvh"); }
  };
  if (vv) { vv.addEventListener("resize", kb); vv.addEventListener("scroll", kb); }
  addEventListener("resize", kb);
  addEventListener("focusin", () => setTimeout(kb, 60));
  addEventListener("focusout", () => setTimeout(kb, 60));

  const phone = () => root.clientWidth < 700;
  // The status strip (.cm-strip[data-fold]): on a phone both lines show (one row and two at most); a tap shows every word, another folds it again (R15).
  // Kept on the root (data-strip=open), not the strip: a redraw rebuilds the strip with data-fold and would fold it back.
  addEventListener("click", e => {
    const s = phone() && e.target.closest?.(".cm-strip[data-fold]");
    if (!s || e.target.closest("a,button,input,select,textarea,summary,details>p,[role=menu]")) return;
    if (root.dataset.strip) delete root.dataset.strip; else root.dataset.strip = "open";
  });
  addEventListener("hashchange", () => { delete root.dataset.strip; });
  // Press and hold a message (500ms, without moving) to show its tools (.msg-tools); a tap elsewhere hides them.
  let hold = null, held = null;
  const drop = () => { if (held) { delete held.dataset.held; held = null; } };
  addEventListener("pointerdown", e => {
    clearTimeout(hold);
    if (held && !e.target.closest?.(".msg-tools")) drop();
    if (e.pointerType !== "touch" || !phone()) return;
    const m = e.target.closest?.(".msg"); if (!m || e.target.closest("a,button,input,textarea,summary")) return;
    const x = e.clientX, y = e.clientY;
    const cancel = ev => { if (!ev || ev.type !== "pointermove" || Math.hypot(ev.clientX - x, ev.clientY - y) > 10) { clearTimeout(hold); off(); } };
    const off = () => { removeEventListener("pointermove", cancel); removeEventListener("pointerup", cancel); removeEventListener("pointercancel", cancel); };
    addEventListener("pointermove", cancel); addEventListener("pointerup", cancel); addEventListener("pointercancel", cancel);
    hold = setTimeout(() => { off(); drop(); held = m; m.dataset.held = ""; if (navigator.vibrate) try { navigator.vibrate(10); } catch {} }, 500);
  }, true);
  addEventListener("contextmenu", e => { if (phone() && e.target.closest?.(".msg[data-held]")) e.preventDefault(); });
}
