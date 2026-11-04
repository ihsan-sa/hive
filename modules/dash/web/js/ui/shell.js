// The top bar: brand, tabs from the view registry, and the two numbers you check most (running, weekly room).
import { brandMark } from "../core/brand.js";
import { logoClick } from "../core/alias.js";
import { HELP, startTour } from "./tour.js";
import { canFeedback, openFeedback, FEEDBACK_KEY } from "./feedback.js";
import { h, s } from "../core/dom.js";
import { S, can, needsAccount } from "../core/store.js";
import { simpleOn, setSimple } from "../core/simple.js";
import { accountUse, activeUse, fresh7, inUse, runOut, runway, totalUse } from "../core/model.js";
import { ago, clock, dur, now, plural, stamp } from "../core/format.js";
import { href } from "../core/router.js";
import { layout } from "../core/nav.js";
import { getTheme, cycleTheme, THEMES } from "../core/theme.js";
import { statusDot, button, icon } from "./kit.js";
import { D as deck, supported as deckSupported } from "../deck/control.js";

export function statusBits() {
  const n = S.running.size, all = (S.usage && S.usage.logins) || (S.live && S.live.logins) || [], us = all.filter(l => inUse(l, S.usage)), hold = S.live && S.live.limit_until;
  return [
    S.failed ? h("span", { "data-tone": "bad", style: "color:var(--ci)" }, statusDot("stuck"), "Lost contact, last data " + ago(S.fresh)) : null,
    h("span", { title: "agents running now", "data-tone": n ? "good" : null }, statusDot("working", { live: n > 0 }), h("b", null, String(n)), " running"),
    us.length ? loginMarks()
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
const fsIcon = () => icon("expand");
export function fullscreenButton() {
  if (!fsDoc.fullscreenEnabled) return null;
  return h("button", { type: "button", class: "btn fs", "data-kind": "icon", title: fsSay(), "aria-label": fsSay(), onclick: fsToggle }, fsIcon());
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
function simpleBits() {
  const stop = hiveStopped(), use = needsAccount() ? null : accountUse();
  return [
    S.failed ? h("span", { "data-tone": "bad", style: "color:var(--ci)" }, statusDot("stuck"), "Lost contact, last news " + ago(S.fresh)) : null,
    stop ? h("span", { "data-tone": "bad", style: "color:var(--ci)" }, statusDot("limited"), "Taking a break") : S.running.size ? h("span", null, statusDot("working", { live: true }), "Working on it") : null,
    use && use.week ? h("span", { title: "How much of your Claude account's weekly limit is used" }, use.week.used + "% of the week used") : null,
  ];
}
/** The week used, as a small ring: two circles, the second dashed to the share used. */
const ring = pct => s("svg", { class: "top-ring", viewBox: "0 0 16 16", "aria-hidden": "true" },
  s("circle", { cx: 8, cy: 8, r: 6, fill: "none", stroke: "var(--line)", "stroke-width": 2.5 }),
  s("circle", { cx: 8, cy: 8, r: 6, fill: "none", stroke: pct <= 10 ? "var(--bad)" : pct <= 25 ? "var(--warn)" : "var(--data)", "stroke-width": 2.5, "stroke-linecap": "round",
    "stroke-dasharray": `${(Math.min(100, Math.max(0, pct)) * 0.377).toFixed(1)} 37.7`, transform: "rotate(-90 8 8)" }));
/** A pill that opens a page: the whole pill is the link (a stretched anchor), so the pill keeps the bar's own look. */
const goPill = (to, say, attrs, ...kids) => h("span", { ...attrs, class: "top-pill" }, ...kids, h("a", { class: "top-pill-go", href: href(to), "aria-label": say, title: attrs.title || say }));
function appBits() {
  const n = S.running.size, all = logins(), stop = hiveStopped(), tot = totalUse(all), act = activeUse(S.usage || { logins: all });
  const left = act && Math.max(0, 100 - Math.round(act.pct)), left5 = act && act.five && Math.max(0, 100 - Math.round(act.five.pct));
  const names = act && act.ls.map(l => l.name + (l.plan ? " " + l.plan : "")).join(", ");
  const ro = act && runOut(S.usage || { logins: all }), out = ro && ro.at != null ? ro.at : null;
  return [
    S.failed ? h("span", { "data-tone": "bad" }, statusDot("stuck"), "Lost contact, last data " + ago(S.fresh)) : null,
    stop ? goPill(stop.why === "off" ? "settings" : "usage", stop.why === "off" ? "Settings" : "Usage", { "data-tone": "bad", title: stop.why === "off" ? "The Hive isn't running" : "Every account is past its usage line" }, statusDot("limited"), stop.why === "off" ? "Not running" : "Stopped")
      : goPill("now", "Agents: what each is doing", { title: "agents running now: open Agents", "data-tone": n ? "good" : null }, statusDot("working", { live: n > 0 }), n ? [h("b", null, String(n)), n === 1 ? " agent working" : " agents working"] : "No agents working"),
    // What's left on the accounts the hive can run on now (the person, 2026-11-04), weighted by plan; and the 5-hour window when one is open.
    act ? goPill("usage", "Usage", { title: `Left on the accounts in use now (${names}): ${left}% of this week` + (act.five ? `, ${left5}% of the 5-hour window` : "") + (out != null ? `. At ${ro.how === "week" ? "this week's average" : "the last 6 hours'"} pace it runs out in about ${dur(out)}, ${clock(now() + out)}` : ro && ro.lasts ? ". At this pace it lasts until the week resets" : "") + (tot ? `. All accounts together: ${Math.max(0, 100 - Math.round(tot.pct))}% left` : "") + ". Open Usage",
        "data-tone": left <= 10 || (left5 != null && left5 <= 10) ? "bad" : null },
      ring(left), left + "% left", act.five ? h("span", { class: "top-pill-5" }, ` · 5h ${left5}%`) : null, out != null ? h("span", { class: "top-pill-5" }, ` · out in ${dur(out)}`) : null) : null,
  ];
}
/** Developer: one mark per login, height = weekly use, red at or past its stop line. */
function loginMarks() {
  const all = logins(); if (!all.length) return null;
  const full = all.filter(l => !inUse(l, S.usage) && !l.paused).length;
  const say = all.map(l => { const v = fresh7(l); return `${l.name} ${v == null ? "?" : Math.round(v) + "%"}${l.paused ? " (paused)" : inUse(l, S.usage) ? "" : " (full)"}`; }).join(", ");
  return h("span", { title: "weekly use per login: " + say },
    h("span", { class: "top-logins", "aria-hidden": "true" }, all.map(l => { const v = fresh7(l) || 0;
      return h("i", { style: `height:${Math.max(2, Math.round(Math.min(100, v) * 0.14))}px`, "data-tone": l.paused ? "quiet" : inUse(l, S.usage) ? null : "bad" }); })),
    `${full} of ${all.length} full`);
}
/** The band under the bar when the hive has stopped: says why in words, and where to look. */
function band(current) {
  if (simpleOn() && needsAccount() && current !== "start" && current !== "settings") return h("div", { class: "band", role: "status", "data-key": "band" }, statusDot("limited"),
    h("span", null, can("logins") ? "Your Hive can't start until you add your Claude account." : "Your Hive isn't ready yet. The person setting it up still has a step to finish."),
    can("logins") ? button("Add it", null, { href: href("settings"), size: "s", kind: "quiet" }) : null);
  const st = hiveStopped(); if (!st) return null;
  if (simpleOn()) return h("div", { class: "band", role: "status", "data-tone": "bad", "data-key": "band" }, statusDot(st.why === "off" ? "failed" : "limited"),
    h("span", null, st.why === "off" ? "Your Hive isn't running right now, so it won't answer yet." : "Your Hive has used up its allowance for now and is taking a break." + (st.until ? ` It comes back ${stamp(st.until)}. Nothing is lost.` : " Nothing is lost.")));
  const text = st.why === "off" ? "The Hive isn't running, so nothing you send starts an agent."
    : st.why === "paused" ? "Every Claude account is paused, so no new agents start. Running ones finish their turn."
    : "The Hive has stopped starting agents: every Claude account is past its usage line. Running ones finish their turn." + (st.until ? ` It starts again ${stamp(st.until)}.` : "");
  return h("div", { class: "band", role: "status", "data-tone": "bad", "data-key": "band" }, statusDot(st.why === "off" ? "failed" : "limited"), h("span", null, text),
    button(st.why === "off" ? "Settings" : "Usage", null, { href: href(st.why === "off" ? "settings" : "usage"), size: "s", kind: "quiet" }));
}

// ---- tabs: on the developer route every view in registry order; in the app core/nav.js says where each one sits ----
const NAV = { open: false };
const lab = v => (typeof v.label === "function" ? v.label() : v.label);
const tabLink = (v, active) => { const b = v.badge ? v.badge() : null;
  return h("a", { class: "tab", href: href(v.id), "data-key": v.id, "aria-current": v.id === active ? "page" : null }, lab(v), b ? h("span", { class: "tab-badge", "aria-label": `${b} for you` }, b) : null); };
const moreHead = text => h("div", { class: "more-h", role: "presentation" }, text);
addEventListener("hashchange", () => { NAV.open = false; });
document.addEventListener("click", e => { if (NAV.open && !e.target.closest(".more,dialog.fb")) { NAV.open = false; NAV.update && NAV.update(); } });
addEventListener("keydown", e => { if (e.key === "Escape" && NAV.open) { NAV.open = false; NAV.update && NAV.update(); } });

export function topBar({ views, current, update, nav }) {
  const on = v => (typeof v.tab === "function" ? v.tab() : v.tab);
  const simple = simpleOn();
  const mode = S.app.mode, route = simple || S.app.route !== "outside" ? null : mode === "full" ? `full, ${Math.ceil(S.app.full_left / 60)} min` : mode ? "limited" : can("post") ? "outside access" : "read-only";
  const th = getTheme(), app = S.app.app && nav;
  // During the walkthrough only Settings (for Accounts) and Help: no tabs to wander off to before setup is finished.
  const setup = app && S.app.settings && S.app.settings.setup_done === false;
  NAV.update = update;
  let tabs, row = null, icons = [];
  if (!app) tabs = views.filter(on).map(v => tabLink(v, current));
  else {
    const L = layout(views, nav, on, current);
    row = setup ? null : L.row;
    const inMore = L.more.find(v => v.id === current);
    const tools = L.more.map(v => h("a", { class: "more-i", role: "menuitem", href: href(v.id), "data-key": v.id, "aria-current": v.id === current ? "page" : null, title: v.about ? (typeof v.about === "function" ? v.about() : v.about) : null },
      h("b", null, lab(v)), v.about ? h("span", null, typeof v.about === "function" ? v.about() : v.about) : null));
    const help = [
      h("a", { class: "more-i", role: "menuitem", href: HELP, target: "_blank", rel: "noopener", "data-key": "help" }, h("b", null, "User guide")),
      h("button", { type: "button", class: "more-i", role: "menuitem", "data-key": "tour", onclick: () => { NAV.open = false; update(); startTour(); } }, h("b", null, "Take the tour")),
      canFeedback() ? h("button", { type: "button", class: "more-i", role: "menuitem", "data-key": "feedback", onclick: () => { NAV.open = false; update(); openFeedback(); } }, h("b", null, "Send feedback"), h("span", null, FEEDBACK_KEY)) : null,
      h("button", { type: "button", class: "more-i", role: "menuitem", "data-key": "simple", onclick: () => { NAV.open = false; setSimple(!simple); update(); } },
        h("b", null, simple ? "Show everything" : "Simple view"), h("span", null, simple ? "all the tabs and what's going on behind the scenes" : "just the chat")),
    ];
    tabs = setup ? [] : L.main.map(v => tabLink(v, L.active));
    icons = [...L.icons.filter(v => !setup || v.id === "settings").map(v => { const b = v.badge ? v.badge() : null;
        return h("a", { class: "btn", "data-kind": "icon", href: href(v.id), "data-key": v.id, "aria-label": lab(v), title: lab(v), "aria-current": v.id === L.active || v.id === current ? "page" : null },
          icon(v.icon || v.id), b ? h("span", { class: "more-dot", "aria-label": `${b} for you` }) : null); }),
      h("div", { class: "more", "data-key": "more" },
        h("button", { type: "button", class: "btn", "data-kind": "icon", "aria-label": "More views", title: inMore ? `More views (${lab(inMore)} is open)` : "More views", "aria-expanded": String(NAV.open), "aria-haspopup": "true", "aria-current": inMore ? "page" : null, onclick: () => { NAV.open = !NAV.open; update(); } },
          icon("more"), inMore ? h("span", { class: "more-dot", "aria-hidden": "true" }) : null),
        NAV.open ? h("div", { class: "more-m", role: "menu" }, tools.length && !setup ? moreHead("Views") : null, setup ? null : tools, tools.length ? moreHead("Help") : null, help) : null)];
  }
  const bits = setup ? () => null : app ? (simple ? simpleBits : appBits) : statusBits;
  return h("div", null,
    h("div", { class: "top-in" },
      h("a", { class: "brand", href: app ? "#comms" : "#now", onclick: logoClick }, brandMark()),
      h("nav", { class: "tabs", "aria-label": "Views" }, tabs),
      h("div", { class: "top-status", role: "status" }, route ? h("span", { class: "top-route", title: "Allowed from outside: " + (S.app.can || []).join(", ") }, route) : null,
        mode === "expired" ? h("a", { class: "top-route", href: "/cdn-cgi/access/logout", title: "Full mode lasts an hour after signing in. This signs you out; open this page again and sign in with Google." }, "sign in again for full") : null, bits()),
      h("div", { class: "top-icons" }, icons,
        deckSupported() && !app ? h("a", { class: "btn top-deck", "data-kind": "icon", href: "#deck", "data-on": deck.state === "on" ? "1" : null, "data-armed": deck.armed ? "1" : null, "aria-label": deck.armed ? "Deck: full control" : "Deck", title: deck.state === "on" ? "DDJ-FLX4 connected" : "Run this page from a DDJ-FLX4" }, icon("dial")) : null,
        button(`Colour theme: ${THEMES.find(x => x[0] === th)[1]}`, () => { cycleTheme(); update(); }, { kind: "icon", icon: th === "light" ? "sun" : th === "dark" ? "moon" : "auto", cls: "theme", title: `Colour theme: ${THEMES.find(x => x[0] === th)[1]}. Auto follows your system setting` }),
        fullscreenButton())),
    h("div", { class: "top-sub", "aria-hidden": "true" }, bits()),
    row ? h("nav", { class: "subtabs", "aria-label": "Pages here" }, row.map(v => h("a", { class: "subtab", href: href(v.id), "data-key": v.id, "aria-current": v.id === current ? "page" : null }, (nav.names && nav.names[v.id]) || lab(v)))) : null,
    app ? band(current) : null);
}
