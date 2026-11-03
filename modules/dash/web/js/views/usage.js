// Usage: are we about to run out, then where the work went. Template: metrics.
import { h } from "../core/dom.js";
import { S, SYS, can } from "../core/store.js";
import { spendSince, fresh7, inUse, runway, weeklyStop, fileInfo, threadTitle, tierOf, agentStatus, sEnd } from "../core/model.js";
import { now, ago, dur, clock, dayName, untilText, money, money$, toks, pct, plural, HOUR, DAY, MIN } from "../core/format.js";
import { TIERS, tier, status } from "../core/status.js";
import { threadHref, agentHref, href } from "../core/router.js";
import { metrics, cols } from "../templates.js";
import { pageHeader, section, tally, stats, share, rank, empty, button } from "../ui/kit.js";
import { gaugeRows } from "../ui/gauge.js";
import { openRef } from "../ui/refs.js";
import { chart, carry } from "../ui/chart.js";
import { askPause, askLimits, askProviderLine, askStopLine } from "../actions.js";

const LIST = "At API list prices: what this would cost if billed per token. A measure of work, not what the plan charges.";

function limitGauge(label, w, stop, t, extra = []) {
  const past = w.r != null && w.r * 1e3 < t, p = past ? 0 : Math.round(w.u * 100), left = w.r * 1e3 - t;
  const reset = w.r == null ? null : past ? `reset at ${clock(w.r * 1e3)}, no new reading yet` : `resets ${left > DAY ? dayName(w.r * 1e3) + " " + clock(w.r * 1e3) : clock(w.r * 1e3)}, in ${untilText(left)}`;
  return { label, value: p + "%", pct: p, stop, lines: [reset, ...extra] };
}

export function loginRow(U, l, t) {
  const no5 = !!l.no_five_hour, L = l.limits || {}, st = l.stop || {}, wStop = st.weekly ?? U.stop_pct, hStop = st.five_hour;
  const rw = runway(l, U);
  let pace = null;
  if (rw.toStop != null && wStop) pace = rw.toStop <= 0 ? `At or past ${wStop}%, where the starter stops new agents.`
    : rw.before ? h("b", null, `Reaches ${wStop}% in about ${dur(rw.toStop)} at the last 6 hours' pace, before it resets.`)
    : `At the last 6 hours' pace it won't reach ${wStop}% before it resets.`;
  const rs = l.resets || {}, src = l.read_at ? "read " + ago(l.read_at * 1e3) : "from the last reading";
  const fresh = !l.read_at || (L.at || 0) >= l.read_at || l.weekly == null;  // a session's reading only if newer than the API's
  const weekly = L.seven_day && fresh && (L.seven_day.r * 1e3 > t || l.weekly == null) ? limitGauge("Weekly", L.seven_day, wStop, t, [pace])
    : l.weekly != null ? limitGauge("Weekly", { u: l.weekly / 100, r: rs.weekly }, wStop, t, [pace, src])
    : { label: "Weekly", value: "?", lines: ["not read yet"] };
  const five = no5 ? null
    : L.five_hour && fresh && L.five_hour.r * 1e3 > t ? limitGauge("5-hour", L.five_hour, hStop, t, [L.five_hour.u >= 0.9 ? "A session stopped by this limit resumes after the reset." : null])
    : l.five != null ? limitGauge("5-hour", { u: l.five / 100, r: rs.five_hour }, hStop, t, [src])
    : l.weekly != null ? limitGauge("5-hour", { u: 0, r: null }, hStop, t, ["no 5-hour window open: no use in the last 5 hours"])
    : L.five_hour ? limitGauge("5-hour", L.five_hour, hStop, t) : null;
  // history, 15-minute buckets carried forward, aligned so it holds still
  const step = 15 * MIN, start = Math.floor((t - 48 * HOUR) / step) * step, N = 48 * 4, a5 = new Array(N).fill(null), a7 = new Array(N).fill(null);
  for (const [w, u5, u7] of l.series || []) { const i = Math.floor((w * 1e3 - start) / step); if (i >= 0 && i < N) { if (u5 != null) a5[i] = u5 * 100; if (u7 != null) a7[i] = u7 * 100; } }
  carry(a5); carry(a7);
  const any = a => a.some(v => v != null), series = [a7, ...(!no5 && any(a5) ? [a5] : [])];
  // Signing in lives in Settings; a lapsed account says so here too, because this is where it stops working.
  const out = S.app.app && ((S.app.setup && S.app.setup.logins) || []).some(x => x.name === l.name && !x.signed_in);
  return {
    key: l.name, name: l.name, tag: l.plan ? `plan: ${l.plan}` : null, dim: l.paused,
    state: out ? { key: "out" } : l.paused ? { key: "paused", text: "paused: no new runs" } : inUse(l, U) ? { key: "inuse" } : { key: "atstop" },
    note: `Stops at ${wStop}% weekly${st.weekly == null ? " (global)" : ""}` + (no5 ? ". No 5-hour limit on this plan." : hStop != null ? `, ${hStop}% 5-hour.` : ", no 5-hour stop."),
    gauges: [weekly, five].filter(Boolean),
    history: any(a7) || any(a5) ? chart({ series, kind: "step", start, step, max: 100, mark: wStop, compact: true, height: 44, area: false, names: ["weekly", "5-hour"], fmt: v => Math.round(v) + "%" }) : h("span", { class: "meta" }, "no history yet"),
    actions: [out && can("logins") ? button("Sign in", null, { size: "s", href: href("settings") }) : null,
      ...(can("limits") ? [button(S.app.app ? "Usage line" : "Stop lines", () => askLimits(U, l), { size: "s" }), button(l.paused ? "Resume" : "Pause", () => askPause(U, l), { size: "s", kind: "quiet" })] : [])],
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
      note: [cap ? `Holds runs once this month's spend reaches $${cap}.` : "No monthly limit set in config.json.", read].filter(Boolean).join(" "),
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

/** The app's answer: how much is left, and when it comes back. One account usually; with several, the one with most room. */
export function leftAnswer(U, t = now()) {
  const us = U.logins || [], live = us.filter(l => inUse(l, U)), pick = (live.length ? live : us).map(l => ({ l, v: fresh7(l), r: runway(l, U) })).sort((a, b) => (a.v ?? 0) - (b.v ?? 0))[0];
  if (!pick) return { lead: "No Claude account yet. Sign in under Settings.", tone: "quiet", figures: [] };
  const { l, v, r } = pick, L = l.limits || {}, w5 = !l.no_five_hour && L.five_hour && L.five_hour.r * 1e3 > t ? L.five_hour : null;
  const back = r.toReset != null ? t + r.toReset : null, left = v == null ? null : Math.max(0, 100 - Math.round(v)), room = v == null ? null : Math.max(0, Math.round(r.stop - v));
  let lead, tone = null;
  if (!live.length) { lead = `The hive has stopped starting agents: ${us.length > 1 ? "every account is" : "your account is"} past its ${r.stop}% line.` + (back ? ` It comes back ${dayName(back)} ${clock(back)}, in ${dur(r.toReset)}.` : ""); tone = "bad"; }
  else {
    lead = (left == null ? "Claude hasn't said yet how much of this week is left" : `${left}% of this week is left`) + (back ? `, and it comes back in full ${dayName(back)} ${clock(back)}, in ${dur(r.toReset)}.` : ".");
    if (r.before) { lead += ` At the last 6 hours' pace, new agents stop in about ${dur(r.toStop)}, at the ${r.stop}% line.`; tone = "warn"; }
    else if (back && left != null) lead += " At the current pace it lasts until then.";
  }
  const figures = [
    { label: "Left this week", value: left == null ? "?" : left + "%", meter: { value: v, stop: r.stop, label: "weekly use" }, note: room != null ? `${room} points before new agents stop, at ${r.stop}%` : null, tone: !live.length ? "bad" : null },
    { label: "Comes back", value: back ? `${dayName(back).split(",")[0]} ${clock(back)}` : "?", note: back ? "in " + dur(r.toReset) + ", all of it" : null },
    w5 ? { label: "Left in this 5 hours", value: Math.max(0, 100 - Math.round(w5.u * 100)) + "%", meter: { value: w5.u * 100, label: "5-hour use" }, note: `refills ${clock(w5.r * 1e3)}, in ${dur(w5.r * 1e3 - t)}`, tone: w5.u >= 0.9 ? "bad" : null } : null,
  ];
  return { lead, tone, figures, account: us.length > 1 ? l.name : null };
}

/** The answer: can new runs start, and for how long at this pace? */
export function usageAnswer(U, t = now()) {
  const us = U.logins || [], live = us.filter(l => inUse(l, U));
  let lead, tone;
  if (!us.length) { lead = "No logins reported yet."; tone = "quiet"; }
  else if (!live.length) {
    const next = us.filter(l => !l.paused).map(l => ({ l, r: runway(l, U).toReset })).filter(x => x.r != null).sort((a, b) => a.r - b.r)[0];
    lead = `No login has room, so nothing new starts.` + (next ? ` The first to reset is ${next.l.name}, in ${dur(next.r)}.` : ""); tone = "bad";
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

export default {
  id: "usage", label: "Usage", about: "how much is left, and when it comes back", tab: true,
  render() {
    const U = S.usage;
    if (!U) return h("div", { class: "page" }, empty("Reading usage…", { kind: "loading" }));
    const t = now(), us = U.logins || [], P = U.providers || {};
    const live = us.filter(l => inUse(l, U)), paused = us.filter(l => l.paused), full = us.filter(l => !l.paused && !inUse(l, U));

    const app = S.app.app, ans = app ? leftAnswer(U, t) : null, { lead, tone } = app ? ans : usageAnswer(U, t);
    const lastAt = Math.max(0, ...us.map(l => (l.limits && l.limits.at) || 0));
    const d1 = spendSince(t - DAY);
    const header = pageHeader({
      title: app ? "How much is left" : "Usage", lead, tone,
      after: app ? (ans.figures.length ? h("div", { style: "margin-top:var(--s3)" }, stats(ans.figures)) : null) : us.length ? tally([{ n: live.length, label: "in use", status: "inuse" }, { n: full.length, label: "at the stop line", status: "atstop" }, { n: paused.length, label: "paused", status: "paused" }]) : null,
      meta: [`${money(d1)} of work in the last 24 hours`, U.scanning ? "still reading the older logs" : null, lastAt ? `limits as last reported ${ago(lastAt * 1e3)}` : null],
    });

    const key = h("span", { class: "chart-key" }, h("span", null, h("i"), "weekly"), h("span", null, h("i", { "data-k": "1" }), "5-hour"), h("span", null, h("i", { "data-k": "mark" }), "stop"), h("span", null, "· last 48h"));
    const limits = section({ title: app ? "Claude accounts" : "Logins", note: app ? `new agents stop at ${U.stop_pct}% of the week, unless an account has its own line` : "pause one to keep the starter off it, or set where it stops",
        actions: app && can("config") ? button("Change", () => askStopLine(U), { size: "s", kind: "quiet" }) : null },
      gaugeRows({ head: ["Login", "Limits", key, ""], histKey: key, rows: us.map(l => loginRow(U, l, t)) }));
    const provs = Object.keys(P).filter(k => !k.startsWith("_"));
    const others = provs.length ? section({ title: "Other providers" }, gaugeRows({ rows: provs.map(k => providerRow(k, P[k], P._checked)) })) : null;

    // Spend
    const files = U.files, tk = [0, 0, 0, 0]; let all = 0, hours = 0;
    for (const f of Object.values(files)) { all += f.c; for (let i = 0; i < 4; i++) tk[i] += f.t[i]; }
    for (const s of S.sessions) { const a = Math.max(s.start, t - DAY), b = Math.min(sEnd(s), t); if (b > a) hours += (b - a) / HOUR; }
    const posts = S.posts.filter(p => p.t > t - DAY && !SYS.has(p.from)).length, h1 = spendSince(t - 2 * HOUR) / 2;
    const hN = Math.max(6, Math.min(48, Math.ceil((t - (S.posts[0] ? S.posts[0].t : t)) / HOUR))), hStart = Math.floor((t - hN * HOUR) / HOUR) * HOUR, bars = new Array(hN + 1).fill(0);
    for (const f of Object.values(files)) for (const k in f.h) { const i = Math.floor((+k * 1e3 - hStart) / HOUR); if (i >= 0 && i <= hN) bars[i] += f.h[k]; }
    let run = 0; const cum = bars.map(v => (run += v));
    const byT = {}, byA = {}, byTier = { tier1: 0, tier2: 0, held: 0, other: 0 }, byM = {};
    for (const [k, f] of Object.entries(files)) {
      for (const m in f.m) byM[m] = (byM[m] || 0) + f.m[m];
      let c = 0; for (const x in f.h) if (+x * 1e3 >= t - DAY) c += f.h[x]; if (!c) continue;
      const i = fileInfo(k), th = i.thread || "(unknown)";
      byT[th] = (byT[th] || 0) + c; byA[i.name] = (byA[i.name] || 0) + c; byTier[tierOf(th)] += c;
    }
    const top = (o, n = 12) => Object.entries(o).sort((a, b) => b[1] - a[1]).slice(0, n);
    const spend = section({ title: app ? "How much work was done" : "Spend", note: LIST },
      stats([
        { label: "Last 24 hours", value: money(d1), note: `${money(h1)} an hour lately` },
        { label: "All of it", value: money(all), note: `since ${S.posts[0] ? dayName(S.posts[0].t) : "?"}` },
        { label: "Per agent-hour", value: hours ? money(d1 / hours) : "–", note: `${hours.toFixed(0)} agent-hours in 24h` },
        { label: "Per board post", value: posts ? money(d1 / posts) : "–", note: `${posts} posts in 24h` },
        { label: "Tokens written", value: toks(tk[1]), note: `output, all of it; ${toks(tk[0])} plain input` },
        { label: "Input served from cache", value: pct(tk[2], tk[0] + tk[2] + tk[3]), note: `${toks(tk[2])} read from cache, ${toks(tk[3])} written to it` },
      ]));
    const charts = cols(
      chart({ title: "Spend per hour", note: `last ${hN} hours`, series: [bars], kind: "bar", start: hStart, step: HOUR, fmt: money, top: money(Math.max(...bars)) }),
      chart({ title: "Spend so far", note: `last ${hN} hours`, series: [cum], kind: "step", start: hStart, step: HOUR, fmt: money, top: money(run) }));
    const tot = d1 || 1;
    const tiers = section({ title: "Spend against your priority list", note: "last 24 hours" },
      share(TIERS.map(x => ({ key: x.key, label: x.label, tone: x.tone, value: byTier[x.key], text: `${money(byTier[x.key])} (${Math.round((100 * byTier[x.key]) / tot)}%)` }))));
    const who = cols(
      section({ title: "Costliest threads", note: "last 24 hours" }, rank(top(byT).map(([k, v]) => ({ key: k, label: threadTitle(k), value: v, text: money(v), tone: tier(tierOf(k)).tone, href: threadHref(k) })))),
      section({ title: "Costliest agents", note: "last 24 hours" }, rank(top(byA).map(([k, v]) => ({ key: k, label: k, value: v, text: money(v), tone: status(agentStatus(k).key).tone, href: agentHref(k), onClick: e => openRef(e, "agent", k) })))));
    const models = section({ title: "By model", note: "all of it" }, rank(top(byM).map(([k, v]) => ({ key: k, label: k, value: v, text: money(v), tone: "info" }))));

    return metrics({ header, sections: [limits, others, spend, charts, tiers, who, models] });
  },
};
