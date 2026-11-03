// The top bar: brand, tabs from the view registry, and the two numbers you check most (running, weekly room).
import { h, s } from "../core/dom.js";
import { S, can } from "../core/store.js";
import { fresh7, inUse } from "../core/model.js";
import { ago, clock, now } from "../core/format.js";
import { href } from "../core/router.js";
import { getTheme, cycleTheme, THEMES } from "../core/theme.js";
import { statusDot, button } from "./kit.js";
import { D as deck, supported as deckSupported } from "../deck/control.js";

export function statusBits() {
  const n = S.running.size, all = (S.usage && S.usage.logins) || (S.live && S.live.logins) || [], us = all.filter(l => inUse(l, S.usage)), hold = S.live && S.live.limit_until;
  return [
    S.failed ? h("span", { "data-tone": "bad", style: "color:var(--ci)" }, statusDot("stuck"), "Lost contact, last data " + ago(S.fresh)) : null,
    h("span", { title: "agents running now" }, statusDot("working", { live: true }), n + " running"),
    us.length ? h("span", { title: "weekly use of the logins that can still take new runs" }, "weekly " + us.map(l => { const v = fresh7(l); return l.name + " " + (v == null ? "?" : Math.round(v) + "%"); }).join(", "))
      : all.length ? h("span", { "data-tone": "bad", style: "color:var(--ci)" }, "no account has room") : null,
    hold ? h("span", { "data-tone": "bad", style: "color:var(--ci)" }, "starts held until " + clock(hold * 1e3)) : null,
    ...us.map(l => { const w = l.limits && l.limits.five_hour; return w && w.r * 1e3 > now() ? h("span", { "data-tone": w.u >= 0.9 ? "bad" : null, style: w.u >= 0.9 ? "color:var(--ci)" : null }, "5-hour " + l.name + " " + Math.round(w.u * 100) + "%") : null; }),
  ];
}

// ---- full screen: a button on every page, and the F key. Absent where the browser can't do it (an iPhone, for one). ----
const fsOn = () => !!document.fullscreenElement;
const fsToggle = () => (fsOn() ? document.exitFullscreen() : document.documentElement.requestFullscreen()).catch(() => {});
const fsSay = () => (fsOn() ? "Leave full screen (F)" : "Full screen (F)");
const fsIcon = () => s("svg", { viewBox: "0 0 16 16", width: 14, height: 14, fill: "none", stroke: "currentColor", "stroke-width": 1.6, "aria-hidden": "true" },
  s("path", { d: fsOn() ? "M6 2v4H2M10 2v4h4M6 14v-4H2M10 14v-4h4" : "M2 6V2h4M14 6V2h-4M2 10v4h4M14 10v4h-4" }));
export function fullscreenButton() {
  if (!document.fullscreenEnabled) return null;
  return h("button", { type: "button", class: "btn fs", "data-size": "s", "data-kind": "quiet", title: fsSay(), "aria-label": fsSay(), onclick: fsToggle }, fsIcon());
}
document.addEventListener("fullscreenchange", () => { for (const b of document.querySelectorAll(".fs")) { b.replaceChildren(fsIcon()); b.title = fsSay(); b.setAttribute("aria-label", fsSay()); } });
addEventListener("keydown", e => {
  if ((e.key !== "f" && e.key !== "F") || e.ctrlKey || e.metaKey || e.altKey || !document.fullscreenEnabled) return;
  if (e.target.closest && e.target.closest("input,textarea,select,[contenteditable]")) return;
  e.preventDefault(); fsToggle();
});

export function topBar({ views, current, update }) {
  const shown = views.filter(v => (typeof v.tab === "function" ? v.tab() : v.tab));
  const mode = S.app.mode, route = S.app.route !== "outside" ? null : mode === "full" ? `full, ${Math.ceil(S.app.full_left / 60)} min` : mode ? "limited" : can("post") ? "outside access" : "read-only";
  const th = getTheme();
  return h("div", null,
    h("div", { class: "top-in" },
      h("a", { class: "brand", href: "#now" }, "Hive"),
      h("nav", { class: "tabs", "aria-label": "Views" }, shown.map(v => {
        const b = v.badge ? v.badge() : null;
        return h("a", { class: "tab", href: href(v.id), "data-key": v.id, "aria-current": v.id === current ? "page" : null }, v.label, b ? h("span", { class: "tab-badge", "aria-label": `${b} unread` }, b) : null);
      })),
      h("div", { class: "top-status", role: "status" }, route ? h("span", { class: "top-route", title: "Allowed from outside: " + (S.app.can || []).join(", ") }, route) : null,
        mode === "expired" ? h("a", { class: "top-route", href: "/cdn-cgi/access/logout", title: "Full mode lasts an hour after signing in. This signs you out; open this page again and sign in with Google." }, "sign in again for full") : null, statusBits()),
      deckSupported() ? h("a", { class: "top-deck", href: "#deck", "data-on": deck.state === "on" ? "1" : null, "data-armed": deck.armed ? "1" : null, title: deck.state === "on" ? "DDJ-FLX4 connected" : "Run this page from a DDJ-FLX4" }, deck.armed ? "FULL CONTROL" : "Deck") : null,
      button(THEMES.find(x => x[0] === th)[1], () => { cycleTheme(); update(); }, { size: "s", kind: "quiet", cls: "theme", title: "Colour theme: Auto follows your system setting" }),
      fullscreenButton()),
    h("div", { class: "top-sub", "aria-hidden": "true" }, statusBits()));
}
