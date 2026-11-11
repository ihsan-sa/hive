// Usage: are we about to run out, then where the work went. Template: metrics.
import { h } from "../core/dom.js";
import { S, SYS, can, getPref, setPref } from "../core/store.js";
import { spendSince, totalUse, activeUse, weekNow, planWeight, fresh7, sessionNewer, inUse, runway, runOut, nextBack, weeklyStop, fileInfo, threadTitle, tierOf, agentStatus, sEnd, hasTiers, agentLabel } from "../core/model.js";
import { now, ago, dur, clock, dayName, untilText, money, money$, toks, pct, plural, HOUR, DAY, MIN } from "../core/format.js";
import { TIERS, tier, status } from "../core/status.js";
import { threadHref, agentHref, href } from "../core/router.js";
import { metrics, cols } from "../templates.js";
import { table } from "../ui/table.js";
import { pageHeader, section, tally, stats, share, rank, empty, waiting, button, seg, meter, statusWord, pageNav } from "../ui/kit.js";
import { gaugeRows } from "../ui/gauge.js";
import { openRef } from "../ui/refs.js";
import { chart, carry } from "../ui/chart.js";
import { askPause, askLimits, askProviderLine, askStopLine } from "../actions.js";

const LIST = "At API list prices: what this would cost if billed per token. A measure of work, not what the plan charges.";

function limitGauge(label, w, stop, t, extra = [], short = false) {
  const past = w.r != null && w.r * 1e3 < t, p = past ? 0 : Math.round(w.u * 100), left = w.r * 1e3 - t, at = left > DAY ? dayName(w.r * 1e3).split(",")[0] + " " + clock(w.r * 1e3) : clock(w.r * 1e3);
  const reset = w.r == null ? null : past ? (short ? "reset, not read since" : `reset at ${clock(w.r * 1e3)}, no new reading yet`) : short ? `resets ${at}` : `resets ${at}, in ${untilText(left)}`;
  return { label, value: p + "%", pct: p, stop, lines: [reset, ...extra] };
}

export function loginRow(U, l, t, span = 48 * HOUR) {
  // The app keeps a row to figures and one short line each (the person, 2026-11-04: "too much text"); the developer view keeps the sentences.
  const short = !!S.app.app, no5 = !!l.no_five_hour, L = l.limits || {}, st = l.stop || {}, wStop = st.weekly ?? U.stop_pct, hStop = st.five_hour;
  const rw = runway(l, U);
  let pace = null;
  if (rw.toStop != null && wStop) pace = short ? (rw.toStop > 0 && rw.before ? h("b", null, `stops in ~${dur(rw.toStop)}`) : null)
    : rw.toStop <= 0 ? `At or past ${wStop}%, where the starter stops new agents.`
    : rw.before ? h("b", null, `Reaches ${wStop}% in about ${dur(rw.toStop)} at the last 6 hours' pace, before it resets.`)
    : `At the last 6 hours' pace it won't reach ${wStop}% before it resets.`;
  const rs = l.resets || {}, src = short ? null : l.read_at ? "read " + ago(l.read_at * 1e3) : "from the last reading";
  const fresh = sessionNewer(l);
  const weekly = L.seven_day && fresh && (L.seven_day.r * 1e3 > t || l.weekly == null) ? limitGauge("Weekly", L.seven_day, wStop, t, [pace], short)
    : l.weekly != null ? limitGauge("Weekly", { u: l.weekly / 100, r: rs.weekly }, wStop, t, [pace, src], short)
    : { label: "Weekly", value: "?", lines: ["not read yet"] };
  const five = no5 ? null
    : L.five_hour && fresh && L.five_hour.r * 1e3 > t ? limitGauge("5-hour", L.five_hour, hStop, t, [!short && L.five_hour.u >= 0.9 ? "A session stopped by this limit resumes after the reset." : null], short)
    : l.five != null ? limitGauge("5-hour", { u: l.five / 100, r: rs.five_hour }, hStop, t, [src], short)
    : l.weekly != null ? limitGauge("5-hour", { u: 0, r: null }, hStop, t, [short ? "none open" : "no 5-hour window open: no use in the last 5 hours"], short)
    : L.five_hour ? limitGauge("5-hour", L.five_hour, hStop, t, [], short) : null;
  // history, in about 190 buckets carried forward, aligned so it holds still
  const N = 192, step = Math.max(MIN, Math.round(span / N / MIN) * MIN), start = Math.floor((t - N * step) / step) * step, a5 = new Array(N).fill(null), a7 = new Array(N).fill(null);
  for (const [w, u5, u7] of l.series || []) { const i = Math.floor((w * 1e3 - start) / step); if (i >= 0 && i < N) { if (u5 != null) a5[i] = u5 * 100; if (u7 != null) a7[i] = u7 * 100; } }
  carry(a5); carry(a7);
  const any = a => a.some(v => v != null), series = [a7, ...(!no5 && any(a5) ? [a5] : [])];
  // Signing in lives in Settings; a lapsed account says so here too, because this is where it stops working.
  const out = S.app.app && ((S.app.setup && S.app.setup.logins) || []).some(x => x.name === l.name && !x.signed_in);
  return {
    key: l.name, name: l.name, tag: l.plan ? (short ? l.plan : `plan: ${l.plan}`) : null, dim: l.paused,
    state: out ? { key: "out" } : l.paused ? { key: "paused", text: "paused: no new runs" } : inUse(l, U) ? { key: "inuse" } : { key: "atstop" },
    note: short ? (st.weekly != null ? `own line: ${st.weekly}%` : null)
      : `Stops at ${wStop}% weekly${st.weekly == null ? " (global)" : ""}` + (no5 ? ". No 5-hour limit on this plan." : hStop != null ? `, ${hStop}% 5-hour.` : ", no 5-hour stop."),
    gauges: [weekly, five].filter(Boolean),
    history: any(a7) || any(a5) ? chart({ series, kind: "step", start, step, max: 100, mark: wStop, compact: true, height: 44, area: false, names: ["weekly", "5-hour"], fmt: v => Math.round(v) + "%" }) : h("span", { class: "meta" }, "no history yet"),
    actions: [out && can("logins") ? button("Sign in", null, { size: "s", href: href("settings", "accounts") }) : null,
      ...(can("limits") ? [button(S.app.app ? "Stop line" : "Stop lines", () => askLimits(U, l), { size: "s" }), button(l.paused ? "Resume" : "Pause", () => askPause(U, l), { size: "s", kind: "quiet" })] : [])],
  };
}

// Other providers: OpenRouter as dollars this month against the cap; Codex as its own windows.
export function providerRow(name, p, checked) {
  const read = checked ? `read ${ago(checked * 1e3)}, every minute` : null;
  const btn = can("limits") && (p.kind === "codex" || p.kind === "key") ? [button(p.kind === "codex" ? "Stop line" : "Monthly limit", () => askProviderLine(name, p), { size: "s" })] : null;
  const base = { key: name, name, actions: btn };
  const msg = text => ({ ...base, tag: p.kind === "codex" ? "Codex CLI" : "API", state: { key: "off", text: "not running here" }, note: read, message: text });
  if (p.kind === "key") {
    if (p.error) return msg(p.error);
    const m = p.monthly || 0, cap = p.monthly_cap, b = p.budget, f = p.free && p.free.limit ? p.free : null;
    const credit = b && b.remaining_usd != null ? `${money$(b.remaining_usd)} credit left` + (b.limit_usd != null ? ` of ${money$(b.limit_usd)}` : "") + (b.scope === "workspace" ? " on the account" : b.scope === "api_key" || b.scope === "key_limit" ? " on the key" : "")
      : p.remaining != null ? `${money$(p.remaining)} credit left on the key` + (p.limit != null ? ` (of ${money$(p.limit)})` : "") : null;
    return { ...base, tag: "API · pays per token", state: cap && m >= cap ? { key: "atstop" } : { key: "inuse" },
      note: [cap ? `Holds runs once this month's cost reaches $${cap}.` : "No monthly limit set in config.json.", read].filter(Boolean).join(" "),
      gauges: [
        { label: "This month (UTC)", value: money$(m), unit: cap ? `of $${cap}` : null, pct: cap ? Math.max((m / cap) * 100, m > 0 ? 1 : 0) : null, lines: [`${money$(p.daily)} today`, credit] },
        f ? { label: "Free models today (UTC)", value: String(f.used), unit: `of ${f.limit} requests`, pct: Math.max((f.used / f.limit) * 100, f.used > 0 ? 1 : 0), lines: [`${f.remaining} left, resets at 00:00 UTC`, "also capped at 20 a minute"] } : null,
      ].filter(Boolean) };
  }
  if (p.kind === "codex") {
    if (!p.logged_in) return msg((p.error || "not logged in") + ": the starter sends no runs to Codex until the ChatGPT login is set up (codex login).");
    const ws = (p.windows || []).slice().sort((a, b) => (a.minutes || 0) - (b.minutes || 0)), nm = m => (m === 300 ? "5-hour" : m === 10080 ? "Weekly" : m ? dur(m * MIN) : "Limit");
    const t = now(), over = ws.some(w => w.used >= p.stop_pct);
    return { ...base, tag: p.plan ? `plan: ${p.plan}` : "Codex CLI", state: over ? { key: "atstop" } : { key: "inuse" }, note: [`Stops at ${p.stop_pct}% of any window.`, read].filter(Boolean).join(" "),
      gauges: ws.length ? ws.map(w => limitGauge(nm(w.minutes), { u: w.used / 100, r: w.resets }, p.stop_pct, t)) : [{ label: "Limits", value: "–", lines: ["No limit windows reported."] }] };
  }
  return msg(p.error || "No limits to show.");
}

// ---- all the Claude accounts as one: weighted by plan, so an Ultra's week counts 10 Pros' (the person's ratio) ----
// One categorical slot per login, in config order, so a login keeps its color (palette checked with the dataviz validator, light and dark).
const SLOTS = [["#2a78d6", "#3987e5"], ["#eb6834", "#d95926"], ["#1baf7a", "#199e70"], ["#eda100", "#c98500"], ["#e87ba4", "#d55181"], ["#008300", "#008300"], ["#6250d6", "#9085e9"], ["#e34948", "#e66767"]];
const slot = i => i < SLOTS.length ? `light-dark(${SLOTS[i][0]},${SLOTS[i][1]})` : "var(--ink-3)";

/** Each login's weekly use over the last `span` ms, in `step` buckets carried forward, ending at its figure now; and the weighted total
 * wherever every login with a reading has one. {start, step, per:[{l, vals}], total} */
export function totalSeries(us, t = now(), span = 7 * DAY, step = 30 * MIN) {
  const start = Math.floor((t - span) / step) * step, N = Math.ceil((t - start) / step);
  const per = us.map(l => {
    const a = new Array(N).fill(null);
    for (const [w, , u7] of l.series || []) { const i = Math.floor((w * 1e3 - start) / step); if (u7 != null && i >= 0 && i < N) a[i] = u7 * 100; }
    const v = weekNow(l, t); if (v != null) a[N - 1] = v;
    return { l, vals: carry(a) };
  }).filter(x => x.vals.some(v => v != null));
  const cap = per.reduce((a, x) => a + planWeight(x.l), 0);
  const total = new Array(N).fill(null).map((_, i) => per.every(x => x.vals[i] != null) && cap ? per.reduce((a, x) => a + x.vals[i] * planWeight(x.l), 0) / cap : null);
  return { start, step, per, total };
}

function totalSection(U, t, R) {
  const us = U.logins || [], tot = totalUse(us, t);
  if (!tot) return null;
  const n = {}; for (const x of tot.parts) n[x.l.plan || "plan unknown"] = (n[x.l.plan || "plan unknown"] || 0) + 1;
  const mix = Object.entries(n).map(([k, v]) => `${v} ${k}`).join(", ");
  const T = totalSeries(us, t, R.span, R.step), color = new Map(us.map((l, i) => [l.name, slot(i)]));
  const big = [...tot.parts].sort((a, b) => b.v * b.w - a.v * a.w)[0];
  const key = h("span", { class: "chart-key" }, T.per.map(x => h("span", null, h("i", { style: `border-top-color:${color.get(x.l.name)}` }), `${x.l.name}${x.l.plan ? " (" + x.l.plan + ")" : ""}`)));
  return section({ title: "All accounts together", note: `this week's use over every Claude account, each counted by its plan: Ultra 10, Pro 1${tot.unread.length ? `; not read yet, left out: ${tot.unread.join(", ")}` : ""}` },
    stats([
      { label: "Used this week, all accounts", value: Math.round(tot.pct) + "%", meter: { value: 100 - tot.pct, tone: tUsed(tot.pct), label: "weekly left, all accounts" }, note: `paused and full accounts count at what they've used` },
      { label: "Capacity", value: `${tot.cap} Pro-weeks`, note: mix },
      big ? { label: "Most of it used by", value: big.l.name, note: `${Math.round(big.v)}% of its ${big.l.plan || ""} week: ${Math.round((big.v * big.w) / tot.cap)} points of the total` } : null,
    ].filter(Boolean)),
    T.per.every(x => x.vals.filter(v => v != null).length < 2) ? empty("No history yet: the charts fill in as the accounts are read.") : cols(
      chart({ title: "All accounts, % of the week used", note: R.name + ", weighted by plan", series: [T.total], kind: "step", start: T.start, step: T.step, max: 100, fmt: v => Math.round(v) + "%", top: "100%" }),
      h("div", null, chart({ title: "Each account, % of its week used", note: R.name, series: T.per.map(x => x.vals), colors: T.per.map(x => color.get(x.l.name)), names: T.per.map(x => x.l.name), kind: "step", start: T.start, step: T.step, max: 100, fmt: v => Math.round(v) + "%", top: "100%" }), key)));
}

/** The app's answer: how much is left, and when more comes in. One account usually; with several, what's left is the one with most room, and when it comes back is the next one due that isn't paused. */
const tUsed = p => (p >= 90 ? "bad" : p >= 75 ? "warn" : "data");
export function leftAnswer(U, t = now()) {
  const us = U.logins || [], live = us.filter(l => inUse(l, U)), awake = us.filter(l => !l.paused), pick = (live.length ? live : awake.length ? awake : us).map(l => ({ l, v: fresh7(l), r: runway(l, U) })).sort((a, b) => (a.v ?? 0) - (b.v ?? 0))[0];
  if (!pick) return { lead: "No Claude account yet. Sign in under Settings.", tone: "quiet", figures: [] };
  const { l, v, r } = pick, L = l.limits || {}, w5 = !l.no_five_hour && L.five_hour && L.five_hour.r * 1e3 > t ? L.five_hour : null;
  const next = nextBack(U, t), back = next && next.at, several = us.length > 1, whose = next && several && next.l.name, left = v == null ? null : Math.max(0, 100 - Math.round(v)), room = v == null ? null : Math.max(0, Math.round(r.stop - v));
  let lead, tone = null, tail = null;
  const when = back ? `${dayName(back)} ${clock(back)}, in ${dur(next.in)}` : "";
  const su = (S.app && S.app.setup) || {};   // a Codex login, a key or a fallback chain: runs still start, so this never says the Hive stopped (core/model.js limitStop)
  if (!live.length && !su.other && !su.fallback) { lead = `The Hive has stopped starting agents: ${!awake.length ? (several ? "every account is paused" : "your account is paused") : several ? "every account is paused or past its line" : `your account is past its ${r.stop}% line`}.` + (back ? ` ${whose || "It"} comes back ${when}.` : ""); tone = "bad"; }
  else {
    const first = left == null ? "Claude hasn't said yet how much of this week is left" : `${left}% of this week is left`;
    const comes = back ? (whose ? `${whose} is the next to come back, ${when}.` : `It comes back in full ${when}.`) : "";
    let pace = "";
    if (r.before) { pace = ` At the last 6 hours' pace, new agents stop in about ${dur(r.toStop)}, at the ${r.stop}% line.`; tone = "warn"; }
    else if (back && left != null) pace = " At the current pace it lasts until then.";
    lead = first + (comes ? ", and " + comes[0].toLowerCase() + comes.slice(1) : ".") + pace;
    // The app look has the figure in its headline already: its lead starts at the coming-back part.
    tail = (comes + pace).trim();
  }
  // Short labels, one fact each (the person, 2026-11-04). "Left now" is the accounts the hive can run on now, weighted by plan.
  const act = activeUse(U, t), tot = totalUse(us, t), nowLeft = act ? Math.max(0, 100 - Math.round(act.pct)) : 0;
  const ro = runOut(U, t), out = ro && ro.at != null ? ro.at : null, anyPace = !!ro;
  const figures = [
    { label: "Left now", value: (act || left == null ? nowLeft : left) + "%", meter: act ? { value: nowLeft, tone: tUsed(act.pct), stop: live.length === 1 ? 100 - r.stop : null, label: "weekly left, accounts in use" } : null,
      note: act ? act.ls.map(x => x.name).join(", ") + (live.length === 1 && room != null ? ` · ${room} points to its line` : "") : "no account in use", tone: !live.length ? "bad" : null },
    { label: "Next to come back", value: back ? `${dayName(back).split(",")[0]} ${clock(back)}` : "?", note: back ? "in " + dur(next.in) + (whose ? `, when ${whose} resets` : ", all of it") : !awake.length ? "every account is paused" : null },
    act && act.five ? { label: "5-hour left", value: Math.max(0, 100 - Math.round(act.five.pct)) + "%", meter: { value: 100 - act.five.pct, tone: tUsed(act.five.pct), label: "5-hour left" }, note: `refills ${clock(act.five.r)}`, tone: act.five.pct >= 90 ? "bad" : null } : null,
    live.length ? { label: "Projected run out", value: out != null ? (out ? "in " + dur(out) : "now") : anyPace ? "Not this week" : "?",
      note: out != null ? (out ? `${dayName(t + out).split(",")[0]} ${clock(t + out)}, ` : "") + (ro.how === "week" ? "at this week's average pace" : "at the last 6 hours' pace") : anyPace ? "lasts until it resets" : "no recent pace", tone: out != null ? "warn" : null } : null,
    tot ? { label: "Left overall", value: Math.max(0, 100 - Math.round(tot.pct)) + "%", meter: { value: 100 - tot.pct, tone: tUsed(tot.pct), label: "weekly left, all accounts" }, note: `of ${tot.cap} Pro-weeks, all accounts` } : null,
  ].filter(Boolean);
  return { lead, tail: tail ?? lead, tone, figures, account: us.length > 1 ? l.name : null };
}

/** The answer: can new runs start, and for how long at this pace? */
export function usageAnswer(U, t = now()) {
  const us = U.logins || [], live = us.filter(l => inUse(l, U));
  let lead, tone;
  if (!us.length) { lead = "No logins reported yet."; tone = "quiet"; }
  else if (!live.length) {
    const next = nextBack(U, t);
    lead = `No login has room, so nothing new starts.` + (next ? ` The first to reset is ${next.l.name}, in ${dur(next.in)}.` : ""); tone = "bad";
  } else {
    const soon = live.map(l => ({ l, r: runway(l, U) })).filter(x => x.r.before).sort((a, b) => a.r.toStop - b.r.toStop)[0];
    const which = live.length === 1 ? `Only ${live[0].name} can take new runs` : `${live.length} of ${us.length} logins can take new runs`;
    if (soon) { lead = `${which}. At the last 6 hours' pace, ${soon.l.name} reaches its ${soon.r.stop}% stop in about ${dur(soon.r.toStop)}, ${dur(soon.r.toReset - soon.r.toStop)} before it resets.`; tone = "warn"; }
    else { lead = `${which}, and none reaches its stop line before it resets at the current pace.`; tone = live.length === 1 ? "warn" : "good"; }
    const hot = live.map(l => ({ l, w: l.limits && l.limits.five_hour })).filter(x => x.w && x.w.r * 1e3 > t && x.w.u >= 0.9);
    if (hot.length) lead += ` ${hot.map(x => x.l.name).join(", ")} ${hot.length > 1 ? "are" : "is"} at ${Math.round(hot[0].w.u * 100)}% of the 5-hour limit.`;
  }
  return { lead, tone };
}

// The page's time range (the person, 2026-11-04): every chart and figure that has a time span follows it. Kept across visits.
const RANGES = [["1h", "1h", HOUR, "last hour"], ["6h", "6h", 6 * HOUR, "last 6 hours"], ["24h", "24h", DAY, "last 24 hours"], ["7d", "7d", 7 * DAY, "last 7 days"], ["all", "All", null, "all of it"]];
function range(U, t) {
  const k = getPref("usage.range", "24h"), r = RANGES.find(x => x[0] === k) || RANGES[2];
  let first = t; // "All": back to the oldest thing the page has
  if (!r[2]) { for (const l of U.logins || []) for (const p of l.series || []) first = Math.min(first, p[0] * 1e3); for (const f of Object.values(U.files || {})) for (const x in f.h) first = Math.min(first, +x * 1e3); }
  const span = r[2] || Math.max(DAY, t - first);
  return { key: r[0], span, since: r[2] ? t - span : 0, name: r[3], step: Math.max(MIN, Math.round(span / 288 / MIN) * MIN) };
}


// The priority list in the person's words: first, second, held (not tier numbers).
const PLAIN_TIER = { tier1: "First", tier2: "Second", held: "Held", other: "Everything else" };

/** The app look (R7, design-d-usage): a state word, one answer, two meters, the accounts as rows, the detail behind closed rows. */
function appHead(U, t, R, pick) {
  const us = U.logins || [], ans = leftAnswer(U, t), act = activeUse(U, t), wk = act || totalUse(us, t);
  const used = wk && (wk.parts || []).length ? Math.round(wk.pct) : null, stopAt = U.stop_pct ?? 100, atStop = us.some(l => !l.paused) ? !us.some(l => inUse(l, U)) : used != null && used >= stopAt;
  const state = used == null ? { tone: "quiet", text: "Not read yet" } : atStop ? { tone: "bad", text: "At the stop line" } : used >= 80 ? { tone: "warn", text: "Getting close" } : ans.tone === "warn" ? { tone: "warn", text: "Going fast" } : { tone: "good", text: "Plenty left" };
  const who = wk && wk.ls && us.length > 1 && wk.ls.length ? wk.ls.map(x => x.name) : null;
  const title = used == null ? "Usage isn't read yet." : atStop ? "You're at the stop line." : who ? `${who.join(", ")} ${who.length > 1 ? "have" : "has"} ${Math.max(0, 100 - used)}% left.` : `${Math.max(0, 100 - used)}% of this week is left.`;
  const tt = ans.figures.find(f => f.label === "Left overall");
  const raise = atStop && us.some(l => !l.paused) && can("config") ? button("Raise the stop line…", () => askStopLine(U), { kind: "primary" }) : null;
  const lastAt = Math.max(0, ...us.map(l => (l.limits && l.limits.at) || 0));
  return pageHeader({ subnav: pageNav(), state, title, lead: (tt && who ? `${tt.value} left across all accounts. ` : "") + (atStop ? ans.lead : ans.tail), tone: ans.tone, actions: [raise, pick], after: hero(U, t, used, stopAt, act),
    meta: [U.scanning ? "still reading the older logs" : null, lastAt ? `limits as last reported ${ago(lastAt * 1e3)}` : null] });
}

/** Two meters: this week against the stop line, and this 5 hours. The bar and its words come from the same figure. */
function hero(U, t, used, stopAt, act) {
  if (used == null) return null;
  const next = nextBack(U, t), back = next && next.at;
  const one = (label, pct, stop, under, k) => h("div", { class: "u-m", "data-key": k },
    h("div", { class: "u-m-h" }, h("span", { class: "u-m-l" }, label), h("span", { class: "meta num" }, `${pct}% used` + (stop != null ? ` · stop line at ${stop}%` : ""))),
    meter({ value: pct, stop, tone: stop != null && pct >= stop ? "bad" : null, label: `${pct}% of ${label.toLowerCase()} used` + (stop != null ? `; stop line at ${stop}%` : "") }),
    under ? h("span", { class: "meta" }, under) : null);
  const five = act && act.five ? Math.round(act.five.pct) : null;
  return h("div", { class: "usage-hero" },
    one("This week", used, stopAt < 100 ? stopAt : null, back ? (next.l && (U.logins || []).length > 1 ? `${next.l.name} comes back ${dayName(back).split(",")[0]} ${clock(back)}` : `Comes back in full ${dayName(back).split(",")[0]} ${clock(back)}`) : null, "week"),
    five != null ? one("This 5 hours", five, null, `Refills ${clock(act.five.r)}`, "five") : null);
}

/** One account's state word, read from the same weekly figure its bar draws, so a full bar can't say "in use". */
function acctWord(r) {
  const w = r.gauges.find(g => g.label === "Weekly"), f = r.gauges.find(g => g.label === "5-hour");
  const full = g => g && g.pct != null && g.pct >= (g.stop ?? 100);
  if (r.state.key === "out") return ["bad", "signed out"];
  if (r.state.key === "paused") return ["quiet", "paused"];
  if (full(w)) return ["bad", "at the stop line"];
  if (full(f)) return ["warn", "5-hour full"];
  if (r.state.key === "atstop") return ["warn", "at its stop line"];
  return ["good", "in use"];
}

function appAccounts(U, t, R) {
  const us = U.logins || [];
  const rows = us.map(l => loginRow(U, l, t, Math.min(R.span, 7 * DAY)));
  const row = r => {
    const w = r.gauges.find(g => g.label === "Weekly"), f = r.gauges.find(g => g.label === "5-hour"), [tone, word] = acctWord(r);
    const reset = g => g && (g.lines || []).find(x => typeof x === "string");
    return h("div", { class: "lr u-acct", "data-key": r.key, "data-dim": r.dim ? "" : null },
      h("span", { class: "av", "aria-hidden": "true" }, (r.name || "?")[0].toUpperCase()),
      h("span", { class: "u-acct-m" },
        h("span", { class: "lr-t" }, h("b", null, r.name), r.tag ? h("span", { class: "meta" }, "Claude · " + r.tag) : null),
        h("span", { class: "lr-s" }, [`Week ${w ? w.value : "?"}`, f ? `5 hours ${f.value}` : null].filter(Boolean).join(" · ")),
        h("span", { class: "gauge-rs meta" }, reset(w) || "not read yet"), f && reset(f) ? h("span", { class: "gauge-rs meta" }, "5 hours " + reset(f)) : null),
      w && w.pct != null ? meter({ value: w.pct, stop: w.stop, tone: tone === "bad" ? "bad" : null, label: `${r.name}: ${w.value} of the week used` }) : h("span", { class: "meta" }, "–"),
      statusWord(tone, word, null, "pg-t"),
      r.actions && r.actions.filter(Boolean).length ? h("span", { class: "u-acct-a" }, ...r.actions) : null);
  };
  return section({ title: "Accounts", actions: can("config") ? button("Change stop line", () => askStopLine(U), { size: "s" }) : null },
    h("div", { class: "list u-accts" }, rows.map(row)),
    h("p", { class: "meta u-note" }, `A bar and its word come from the same reading; the stop line is ${U.stop_pct}%` + stopsApart(rows, U.stop_pct) + "."));
}
/** Accounts whose own weekly stop line differs from the global one, so the note doesn't say 100% beside a lead that says 75% (round 1111b, R15-1). */
function stopsApart(rows, g) {
  const by = new Map();
  for (const r of rows) { const s = (r.gauges.find(x => x.label === "Weekly") || {}).stop; if (s != null && s !== g) by.set(s, [...(by.get(s) || []), r.name]); }
  return [...by].map(([s, n]) => `, ${s}% for ${n.join(" and ")}`).join("");
}

function appChart(U, t, R) {
  const us = U.logins || [], T = totalSeries(us, t, R.span, R.step), color = new Map(us.map((l, i) => [l.name, slot(i)]));
  if (!T.per.length) return null;
  if (T.per.every(x => x.vals.filter(v => v != null).length < 2)) return empty("No history yet: the chart fills in as the accounts are read.");   // one reading draws stray dots, not a line
  const many = T.per.length > 1;
  const series = many ? [T.total, ...T.per.map(x => x.vals)] : [T.total.some(v => v != null) ? T.total : T.per[0].vals];
  const key = many ? h("span", { class: "chart-key" }, T.per.map(x => h("span", null, h("i", { style: `border-top-color:${color.get(x.l.name)}` }), x.l.name))) : null;
  return h("div", null, chart({ title: "% of the week used", note: `dashed: stop line ${U.stop_pct}%`, series, colors: many ? ["var(--data)", ...T.per.map(x => color.get(x.l.name))] : null, kind: "step", start: T.start, step: T.step, max: 100, mark: U.stop_pct, markLabel: "stop line", fmt: v => Math.round(v) + "%", top: "100%" }), key);
}

export default {
  id: "usage", label: "Usage", about: "how much is left, and when it comes back", tab: true,
  render(ctx) {
    const U = S.usage;
    if (!U) return h("div", { class: "page" }, pageNav(), waiting(S.usageFailed, "Reading usage…", "usage", "usage"));
    const t = now(), us = U.logins || [], P = U.providers || {}, R = range(U, t);
    const pick = seg(RANGES.map(x => [x[0], x[1], x[3]]), R.key, k => { setPref("usage.range", k); ctx && ctx.update ? ctx.update() : null; }, { label: "Time range" });
    const live = us.filter(l => inUse(l, U)), paused = us.filter(l => l.paused), full = us.filter(l => !l.paused && !inUse(l, U));

    const app = S.app.app, ans = app ? leftAnswer(U, t) : null, { lead, tone } = app ? ans : usageAnswer(U, t);
    const lastAt = Math.max(0, ...us.map(l => (l.limits && l.limits.at) || 0));
    const d1 = spendSince(R.since);
    // The app shows its answer as short figures; a sentence only when nothing can start.
    const header = pageHeader({ subnav: pageNav(),
      title: app ? "How much is left" : "Usage", lead: app && tone !== "bad" ? null : lead, tone, actions: pick,
      after: app ? (ans.figures.length ? stats(ans.figures) : null) : us.length ? tally([{ n: live.length, label: "in use", status: "inuse" }, { n: full.length, label: "at the stop line", status: "atstop" }, { n: paused.length, label: "paused", status: "paused" }]) : null,
      meta: [`${money(d1)} of work, ${R.name}`, U.scanning ? "still reading the older logs" : null, lastAt ? `limits as last reported ${ago(lastAt * 1e3)}` : null],
    });

    const key = h("span", { class: "chart-key" }, h("span", null, h("i"), "weekly"), h("span", null, h("i", { "data-k": "1" }), "5-hour"), h("span", null, h("i", { "data-k": "mark" }), "stop"), h("span", null, "· " + (R.span > 7 * DAY ? "last 7 days" : R.name)));
    const limits = section({ title: app ? "Claude accounts" : "Logins", note: app ? `how much of each is used; stop line ${U.stop_pct}%` : "pause one to keep the starter off it, or set where it stops",
        actions: app && can("config") ? button("Change stop line", () => askStopLine(U), { size: "s", kind: "quiet" }) : null },
      gaugeRows({ head: ["Account", "Limits", key, ""], histKey: key, rows: us.map(l => loginRow(U, l, t, Math.min(R.span, 7 * DAY))) }));
    const provs = Object.keys(P).filter(k => !k.startsWith("_"));
    const others = provs.length ? section({ title: "Other providers", note: "accounts other than Claude, and how much of each is used" }, gaugeRows({ rows: provs.map(k => providerRow(k, P[k], P._checked)) })) : null;

    // Spend
    const files = U.files, tk = [0, 0, 0, 0]; let all = 0, hours = 0;
    for (const f of Object.values(files)) { all += f.c; for (let i = 0; i < 4; i++) tk[i] += f.t[i]; }
    for (const s of S.sessions) { const a = Math.max(s.start, R.since), b = Math.min(sEnd(s), t); if (b > a) hours += (b - a) / HOUR; }
    const posts = S.posts.filter(p => p.t > R.since && !SYS.has(p.from)).length, h1 = spendSince(t - 2 * HOUR) / 2;
    // Spend is kept by the hour, so a range under 6 hours still draws the last 6; longer ones group into 6-hour or day bars.
    const bStep = R.span > 14 * DAY ? DAY : R.span > 2 * DAY ? 6 * HOUR : HOUR, bSpan = Math.max(6 * HOUR, R.span), hN = Math.ceil(bSpan / bStep);
    const hStart = bStep === DAY ? new Date(t - hN * DAY).setHours(0, 0, 0, 0) : Math.floor((t - bSpan) / bStep) * bStep, bars = new Array(hN + 1).fill(0);
    for (const f of Object.values(files)) for (const k in f.h) { const i = Math.floor((+k * 1e3 - hStart) / bStep); if (i >= 0 && i <= hN) bars[i] += f.h[k]; }
    let run = 0; const cum = bars.map(v => (run += v));
    const byT = {}, byA = {}, byTier = { tier1: 0, tier2: 0, held: 0, other: 0 }, byM = {};
    for (const [k, f] of Object.entries(files)) {
      if (!R.since || !f.mh) { if (!R.since) for (const m in f.m) byM[m] = (byM[m] || 0) + f.m[m]; }
      else for (const x in f.mh) if (+x * 1e3 >= R.since) for (const m in f.mh[x]) byM[m] = (byM[m] || 0) + f.mh[x][m];
      let c = 0; for (const x in f.h) if (+x * 1e3 >= R.since) c += f.h[x]; if (!c) continue;
      const i = fileInfo(k), th = i.thread || "(unknown)";
      byT[th] = (byT[th] || 0) + c; byA[i.name] = (byA[i.name] || 0) + c; byTier[tierOf(th)] += c;
    }
    const top = (o, n = 12) => Object.entries(o).sort((a, b) => b[1] - a[1]).slice(0, n);
    const spend = section({ title: app ? "The figures" : "Cost", note: LIST },
      stats([
        { label: R.key === "all" ? "In range" : "Last " + R.key, value: money(d1), note: `${money(h1)} an hour lately` },
        { label: "All of it", value: money(all), note: `since ${S.posts[0] ? dayName(S.posts[0].t) : "?"}` },
        { label: "Per agent-hour", value: hours ? money(d1 / hours) : "–", note: `${hours.toFixed(0)} agent-hours, ${R.name}` },
        { label: app ? "Per message" : "Per board post", value: posts ? money(d1 / posts) : "–", note: `${posts} posts, ${R.name}` },
        { label: "Tokens written", value: toks(tk[1]), note: `output, all of it; ${toks(tk[0])} plain input` },
        app ? { label: "Read from memory instead of again", value: pct(tk[2], tk[0] + tk[2] + tk[3]), note: "of what agents read (cheaper)" }
          : { label: "Input served from cache", value: pct(tk[2], tk[0] + tk[2] + tk[3]), note: `${toks(tk[2])} read from cache, ${toks(tk[3])} written to it` },
      ]));
    const charts = cols(
      chart({ title: bStep === DAY ? "Cost per day" : bStep === HOUR ? "Cost per hour" : "Cost per 6 hours", note: R.span < bSpan ? "last 6 hours" : R.name, series: [bars], kind: "bar", start: hStart, step: bStep, fmt: money, top: money(Math.max(...bars)) }),
      chart({ title: "Cost so far", note: R.span < bSpan ? "last 6 hours" : R.name, series: [cum], kind: "step", start: hStart, step: bStep, fmt: money, top: money(run) }));
    const tot = d1 || 1;
    const tiers = section({ title: "Cost by your priority list", note: R.name + ": spend per priority" },
      share(TIERS.map(x => ({ key: x.key, label: app ? PLAIN_TIER[x.key] : x.label, tone: x.tone, value: byTier[x.key], text: `${money(byTier[x.key])} (${Math.round((100 * byTier[x.key]) / tot)}%)` }))));
    const who = cols(
      section({ title: app ? "Costliest conversations" : "Costliest threads", note: R.name + ": who spent the most" }, rank(top(byT).map(([k, v]) => ({ key: k, label: threadTitle(k) || k || (app ? "No conversation" : "No thread"), value: v, text: money(v), tone: tier(tierOf(k)).tone, href: threadHref(k) })))),
      section({ title: "Costliest agents", note: R.name + ": who spent the most" }, rank(top(byA).map(([k, v]) => ({ key: k, label: k ? agentLabel(k) : "No agent", value: v, text: money(v), tone: status(agentStatus(k).key).tone, href: agentHref(k), onClick: e => openRef(e, "agent", k) })))));
    const models = section({ title: "By model", note: R.name + ": spend split by model" }, !Object.keys(byM).length ? empty("No model cost in this range.") : rank(top(byM).map(([k, v]) => ({ key: k, label: k, value: v, text: money(v), tone: "data" }))));

    if (app) {
      const fold = (title, summary, ...b) => section({ fold: true, title, summary }, ...b);
      const hist = appChart(U, t, R), noHist = hist && hist.classList && hist.classList.contains("empty");   // one reading only: the fold's line says so, not a chart hidden in it
      return metrics({ header: appHead(U, t, R, pick), sections: [appAccounts(U, t, R), others,
        h("div", { class: "u-folds" },
          fold("Used, by hour", noHist ? "No history yet: it fills in as the accounts are read" : R.name, hist),
          fold("How much work was done", `${money(d1)} at list prices, ${R.name}`, spend, charts, models),
          hasTiers() ? fold("Cost by your priority list", "first, second, held", tiers) : null,
          fold("Costliest conversations and agents", R.name, who))] });
    }
    return metrics({ header, sections: [totalSection(U, t, R), limits, others, spend, charts, hasTiers() ? tiers : null, who, models] });
  },
};
