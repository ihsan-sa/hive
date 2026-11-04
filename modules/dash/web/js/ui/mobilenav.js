// Phone navigation: a bottom tab bar (thumb reach) with a "More" sheet. Shown only under 640px (css/mobile.css hides it otherwise).
// Reads the same registry and nav layout as the top bar, so a new view needs no change here.
import { h } from "../core/dom.js";
import { S } from "../core/store.js";
import { href } from "../core/router.js";
import { layout } from "../core/nav.js";
import { simpleOn, setSimple } from "../core/simple.js";
import { getTheme, cycleTheme, THEMES } from "../core/theme.js";
import { icon } from "./kit.js";
import { HELP } from "./tour.js";

const ST = { open: false, update: null };
const lab = v => (typeof v.label === "function" ? v.label() : v.label);
const GLYPH = { start: "plus", comms: "chat", now: "agents", usage: "usage", settings: "settings", knowledge: "board" };
const SHORT = { now: "Agents", comms: "Chat" };
const DEV_MAIN = ["comms", "now", "agents", "usage"];

export function mobileNav({ views, current, nav, update }) {
  ST.update = update;
  const on = v => (typeof v.tab === "function" ? v.tab() : v.tab);
  const app = S.app.app && nav;
  let main, more, active;
  if (app) {
    const L = layout(views, nav, on, current);
    active = L.active;
    main = L.main.filter(v => v.id !== "start").slice(0, 4);
    const shown = new Set(main.map(v => v.id));
    more = [...L.icons, ...L.main, ...L.more].filter((v, i, a) => !shown.has(v.id) && a.indexOf(v) === i);
  } else {
    const by = new Map(views.map(v => [v.id, v]));
    main = DEV_MAIN.map(id => by.get(id)).filter(Boolean);
    more = views.filter(v => on(v) && !DEV_MAIN.includes(v.id));
    active = current === "agent" ? "now" : main.some(v => v.id === current) ? current : null;
  }
  const inMore = !active || !main.some(v => v.id === active);
  const close = () => { ST.open = false; update(); };
  const th = getTheme();
  const link = v => h("a", { class: "mn-i", href: href(v.id), "data-key": v.id, "aria-current": v.id === current ? "page" : null, onclick: () => { ST.open = false; } },
    h("b", null, lab(v)), v.about ? h("span", null, typeof v.about === "function" ? v.about() : v.about) : null);
  const tab = v => { const b = v.badge ? v.badge() : null;
    return h("a", { class: "mn-t", href: href(v.id), "data-key": v.id, "aria-current": v.id === active ? "page" : null, onclick: () => { ST.open = false; } },
      icon(GLYPH[v.id] || "agents"), h("span", null, SHORT[v.id] || lab(v)), b ? h("i", { class: "mn-b", "aria-label": `${b} for you` }, b) : null); };
  return h("div", { class: "mn", "data-open": ST.open ? "" : null },
    ST.open ? h("div", { class: "mn-scrim", onclick: close }) : null,
    ST.open ? h("div", { class: "mn-sheet", role: "menu" },
      h("div", { class: "mn-grab", "aria-hidden": "true" }),
      more.length ? h("div", { class: "mn-h" }, "Pages") : null,
      more.map(link),
      h("div", { class: "mn-h" }, "Help and display"),
      h("a", { class: "mn-i", href: HELP, target: "_blank", rel: "noopener" }, h("b", null, "User guide")),
      h("button", { type: "button", class: "mn-i", onclick: () => { cycleTheme(); update(); } }, h("b", null, `Colour theme: ${THEMES.find(x => x[0] === th)[1]}`), h("span", null, "tap to change")),
      app ? h("button", { type: "button", class: "mn-i", onclick: () => { ST.open = false; setSimple(!simpleOn()); update(); } }, h("b", null, simpleOn() ? "Show everything" : "Simple view"), h("span", null, simpleOn() ? "all the tabs" : "just the chat")) : null) : null,
    h("nav", { class: "mn-bar", "aria-label": "Main" }, main.map(tab),
      h("button", { type: "button", class: "mn-t", "aria-expanded": String(ST.open), "aria-current": inMore && !ST.open ? "page" : null, onclick: () => { ST.open = !ST.open; update(); } },
        icon("more"), h("span", null, "More"))));
}
addEventListener("hashchange", () => { ST.open = false; });
addEventListener("keydown", e => { if (e.key === "Escape" && ST.open && ST.update) { ST.open = false; ST.update(); } });
