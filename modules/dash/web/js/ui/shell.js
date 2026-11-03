// The top bar: brand, tabs from the view registry, and the two numbers you check most (running, weekly room).
import { HELP, startTour } from "./tour.js";
import { canFeedback, openFeedback, FEEDBACK_KEY } from "./feedback.js";
import { h, s } from "../core/dom.js";
import { S, can } from "../core/store.js";
import { fresh7, inUse, runway } from "../core/model.js";
import { ago, clock, now, plural, stamp } from "../core/format.js";
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
const fsDoc = (() => { try { return window.top.document; } catch (e) { return document; } })();   // inside the deck's single screen, the dashboard around it is what goes full screen
const fsOn = () => !!fsDoc.fullscreenElement;
const fsToggle = () => (fsOn() ? fsDoc.exitFullscreen() : fsDoc.documentElement.requestFullscreen()).catch(() => {});
const fsSay = () => (fsOn() ? "Leave full screen (F)" : "Full screen (F)");
const fsIcon = () => s("svg", { viewBox: "0 0 16 16", width: 14, height: 14, fill: "none", stroke: "currentColor", "stroke-width": 1.6, "aria-hidden": "true" },
  s("path", { d: fsOn() ? "M6 2v4H2M10 2v4h4M6 14v-4H2M10 14v-4h4" : "M2 6V2h4M14 6V2h-4M2 10v4h4M14 10v4h-4" }));
export function fullscreenButton() {
  if (!fsDoc.fullscreenEnabled) return null;
  return h("button", { type: "button", class: "btn fs", "data-size": "s", "data-kind": "quiet", title: fsSay(), "aria-label": fsSay(), onclick: fsToggle }, fsIcon());
}
fsDoc.addEventListener("fullscreenchange", () => { for (const b of document.querySelectorAll(".fs")) { b.replaceChildren(fsIcon()); b.title = fsSay(); b.setAttribute("aria-label", fsSay()); } });
addEventListener("keydown", e => {
  if ((e.key !== "f" && e.key !== "F") || e.ctrlKey || e.metaKey || e.altKey || !fsDoc.fullscreenEnabled) return;
  if (e.target.closest && e.target.closest("input,textarea,select,[contenteditable]")) return;
  e.preventDefault(); fsToggle();
});

// ---- the app's short version: how many are working, how much of the week is used, and a word when it has stopped ----
const logins = () => (S.usage && S.usage.logins) || (S.live && S.live.logins) || [];
/** Has the hive stopped starting agents, and until when? {why, until} or null. */
export function hiveStopped() {
  const all = logins(), hold = S.live && S.live.limit_until;
  if (hold) return { why: "limit", until: hold * 1e3 };
  if (all.length && !all.some(l => inUse(l, S.usage)) && !(S.app.setup && S.app.setup.other)) {  // with a Codex login or a key, agents still start
    const r = all.filter(l => !l.paused).map(l => runway(l, S.usage).toReset).filter(x => x != null).sort((a, b) => a - b)[0];
    return { why: all.every(l => l.paused) ? "paused" : "limit", until: r != null ? now() + r : null };
  }
  if (S.app.app && S.app.setup && S.app.setup.starter === false && S.posts.length) return { why: "off" };
  return null;
}
function appBits() {
  const n = S.running.size, us = logins().filter(l => inUse(l, S.usage)), stop = hiveStopped();
  const wk = us.map(fresh7).filter(v => v != null);
  return [
    S.failed ? h("span", { "data-tone": "bad", style: "color:var(--ci)" }, statusDot("stuck"), "Lost contact, last data " + ago(S.fresh)) : null,
    h("span", { title: "agents running now" }, statusDot("working", { live: n > 0 }), plural(n, "agent") + " working"),
    stop ? h("span", { "data-tone": "bad", style: "color:var(--ci)" }, statusDot("limited"), stop.why === "off" ? "Not running" : "Stopped: usage limit")
      : wk.length ? h("span", { title: "the most-used Claude account's share of its weekly limit" }, Math.round(Math.max(...wk)) + "% of the week used") : null,
  ];
}
/** The band under the bar when the hive has stopped: says why in words, and where to look. */
function band() {
  const st = hiveStopped(); if (!st) return null;
  const text = st.why === "off" ? "The hive isn't running, so nothing you send starts an agent."
    : st.why === "paused" ? "Every Claude account is paused, so no new agents start. Running ones finish their turn."
    : "The hive has stopped starting agents: every Claude account is past its usage line. Running ones finish their turn." + (st.until ? ` It starts again ${stamp(st.until)}.` : "");
  return h("div", { class: "band", role: "status", "data-tone": "bad", "data-key": "band" }, statusDot(st.why === "off" ? "failed" : "limited"), h("span", null, text),
    button(st.why === "off" ? "Settings" : "Usage", null, { href: href(st.why === "off" ? "settings" : "usage"), size: "s", kind: "quiet" }));
}

// ---- tabs: on the developer route every view in registry order; in the app a few up front and the rest under More ----
const NAV = { open: false };
const lab = v => (typeof v.label === "function" ? v.label() : v.label);
const tabLink = (v, current) => { const b = v.badge ? v.badge() : null;
  return h("a", { class: "tab", href: href(v.id), "data-key": v.id, "aria-current": v.id === current ? "page" : null }, lab(v), b ? h("span", { class: "tab-badge", "aria-label": `${b} for you` }, b) : null); };
addEventListener("hashchange", () => { NAV.open = false; });
document.addEventListener("click", e => { if (NAV.open && !e.target.closest(".more")) { NAV.open = false; NAV.update && NAV.update(); } });
addEventListener("keydown", e => { if (e.key === "Escape" && NAV.open) { NAV.open = false; NAV.update && NAV.update(); } });

export function topBar({ views, current, update, nav }) {
  const on = v => (typeof v.tab === "function" ? v.tab() : v.tab);
  const mode = S.app.mode, route = S.app.route !== "outside" ? null : mode === "full" ? `full, ${Math.ceil(S.app.full_left / 60)} min` : mode ? "limited" : can("post") ? "outside access" : "read-only";
  const th = getTheme(), app = S.app.app && nav;
  NAV.update = update;
  let tabs;
  if (!app) tabs = views.filter(on).map(v => tabLink(v, current));
  else {
    const by = new Map(views.map(v => [v.id, v]));
    const main = nav.main.map(id => by.get(id)).filter(v => v && on(v));
    const more = [...nav.more.map(id => by.get(id)), ...views.filter(v => !nav.main.includes(v.id) && !nav.more.includes(v.id))].filter(v => v && on(v));
    const inMore = more.find(v => v.id === current);
    tabs = [...main.map(v => tabLink(v, current)),
      more.length ? h("div", { class: "more", "data-key": "more" },
        h("button", { type: "button", class: "tab", "aria-expanded": String(NAV.open), "aria-haspopup": "true", "aria-current": inMore ? "page" : null, onclick: () => { NAV.open = !NAV.open; update(); } }, inMore ? lab(inMore) : "More", h("span", { "aria-hidden": "true", class: "more-c" }, "▾")),
        NAV.open ? h("div", { class: "more-m", role: "menu" }, more.map(v => h("a", { class: "more-i", role: "menuitem", href: href(v.id), "data-key": v.id, "aria-current": v.id === current ? "page" : null },
          h("b", null, lab(v)), v.about ? h("span", null, typeof v.about === "function" ? v.about() : v.about) : null))
          .concat(h("a", { class: "more-i", role: "menuitem", href: HELP, target: "_blank", rel: "noopener", "data-key": "help" }, h("b", null, "Help"), h("span", null, "the user guide")),
            h("button", { type: "button", class: "more-i", role: "menuitem", "data-key": "tour", onclick: () => { NAV.open = false; update(); startTour(); } }, h("b", null, "Take the tour"), h("span", null, "a few minutes, step by step")),
            canFeedback() ? h("button", { type: "button", class: "more-i", role: "menuitem", "data-key": "feedback", onclick: () => { NAV.open = false; update(); openFeedback(); } }, h("b", null, "Send feedback"), h("span", null, `a screenshot and a comment (${FEEDBACK_KEY})`)) : null)) : null) : null];
  }
  const bits = app ? appBits : statusBits;
  return h("div", null,
    h("div", { class: "top-in" },
      h("a", { class: "brand", href: app ? "#comms" : "#now" }, "Hive"),
      h("nav", { class: "tabs", "aria-label": "Views" }, tabs),
      h("div", { class: "top-status", role: "status" }, route ? h("span", { class: "top-route", title: "Allowed from outside: " + (S.app.can || []).join(", ") }, route) : null,
        mode === "expired" ? h("a", { class: "top-route", href: "/cdn-cgi/access/logout", title: "Full mode lasts an hour after signing in. This signs you out; open this page again and sign in with Google." }, "sign in again for full") : null, bits()),
      deckSupported() && !app ? h("a", { class: "top-deck", href: "#deck", "data-on": deck.state === "on" ? "1" : null, "data-armed": deck.armed ? "1" : null, title: deck.state === "on" ? "DDJ-FLX4 connected" : "Run this page from a DDJ-FLX4" }, deck.armed ? "FULL CONTROL" : "Deck") : null,
      button(THEMES.find(x => x[0] === th)[1], () => { cycleTheme(); update(); }, { size: "s", kind: "quiet", cls: "theme", title: "Colour theme: Auto follows your system setting" }),
      fullscreenButton()),
    h("div", { class: "top-sub", "aria-hidden": "true" }, bits()),
    app ? band() : null);
}
