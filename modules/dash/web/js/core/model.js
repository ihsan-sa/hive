// What the data means: statuses, the agent card, hive stats, usage pace. Pure reads of the store.
import { S, OUTSIDE, SYS } from "./store.js";
import { simpleOn } from "./simple.js";
import { now, ago, dur, median, short, mdPlain, MIN, HOUR, DAY } from "./format.js";

export const isAgent = n => S.agents.has(n);
/** A warden judges whether work crosses the hard limits. The starter names them w + 4 hex (core/starter.py is_warden); `warden` and `rootwarden` are the fixed names. */
export const isWarden = n => /^(warden|rootwarden|w[0-9a-f]{4})$/.test(n);
export const WARDEN_HINT = "A warden: it judges whether work crosses the hard limits, and doesn't take ordinary work.";
export const isRunning = n => S.running.has(n);
export const sEnd = s => s.end ?? (isRunning(s.name) ? now() : s.start);
export function agentThread(n) { for (let i = S.sessions.length - 1; i >= 0; i--) if (S.sessions[i].name === n) return S.sessions[i].thread; return null; }

export function tierOf(th) {
  const P = (S.live && S.live.priorities) || {}; th = th || "";
  if (P.set && P.set[th]) return P.set[th];
  for (const k of ["tier1", "tier2", "held"]) for (const w of P[k] || []) {
    if (w.endsWith("*") ? th.startsWith(w.slice(0, -1)) : th === w || th.startsWith(w + "-") || th.endsWith("-" + w) || th.includes("-" + w + "-")) return k;
  }
  return "other";
}
/** A priority list exists (kb/priorities.md): a fresh consumer Hive has none, and then tiers mean nothing to show. */
export const hasTiers = () => !!(S.live && S.live.priorities && S.live.priorities.tier1);
export const tierRank = th => ["tier1", "tier2", "other", "held"].indexOf(tierOf(th));

/** A running agent's state: {key: working|queue|quiet|stuck, text}. Same test as before. */
export function agentState(a) {
  const q = now() - (a.last ? a.last * 1e3 : 0);
  if (!a.alive) return { key: "stuck", text: "process gone" };
  if (q < 3 * MIN) return { key: "working", text: "working" };
  const since = a.last ? a.last * 1e3 : 0;
  let unseen = 0;
  for (let i = S.posts.length - 1; i >= 0; i--) { const p = S.posts[i]; if (p.t <= since) break; if (p.from !== a.name && (p.men.includes(a.name) || (a.thread && p.thread === a.thread))) unseen++; }
  const j = a.jobs || {}, more = unseen ? `, ${unseen} new post${unseen > 1 ? "s" : ""} when it returns` : "";
  if (j.running) return { key: "working", text: (j.running > 1 ? j.running + " jobs" : "job") + " running" + (j.queued ? `, ${j.queued} more queued` : "") + more };
  if (j.queued) return { key: "queue", text: `${j.queued > 1 ? j.queued + " jobs" : "job"} in the queue ${dur(j.waited * 1e3)}` + more };
  if (a.shells) return { key: "quiet", text: `waiting on ${a.shells > 1 ? a.shells + " jobs" : "a job"} it started` + more };   // the starter hands it new posts itself
  if (unseen) return { key: "stuck", text: `quiet ${dur(q)}, hasn't seen ${unseen} new post${unseen > 1 ? "s" : ""} for it` };
  return { key: "quiet", text: "quiet " + dur(q) + (S.app.app ? "" : ", no job in the queue") };
}
/** Any name's status: {key, label}. Cached per poll. */
export function agentStatus(n) {
  let v = S.stCache.get(n); if (v) return v;
  const L = S.running.get(n), s = S.lastSess[n];
  if (OUTSIDE.has(n)) v = { key: "outside", label: "" };
  else if (L) { const st = agentState(L); v = { key: st.key, label: st.text }; }
  else if (S.pending[n]) v = { key: "queued", label: "queued" };
  else if (!s) v = { key: "none", label: "hasn't run" };
  else if (S.killed[n] >= s.start) v = { key: "killed", label: "killed" };
  else if (S.limitedNow.has(s.session)) v = { key: "limited", label: "stopped at a limit" };
  else if (s.exit && s.exit !== 143) v = { key: "failed", label: `failed (exit ${s.exit})` };
  else v = { key: "ended", label: "ended " + ago(sEnd(s)) };
  S.stCache.set(n, v); return v;
}

// ---- the agent card: what an agent runs on. One list of rows, so a new harness or option adds a row here. ----
export function modelName(id) { const m = /^claude-([a-z]+)-(\d+)-(\d+)(?:-\d{8})?$/.exec(id || ""); return m ? `${m[1][0].toUpperCase() + m[1].slice(1)} ${m[2]}.${m[3]}` : id; }
export function loginName(path) { const l = ((S.live && S.live.logins) || []).find(x => x.path === path); return l ? l.name : path ? String(path).replace(/^.*\/\.claude-?/, "") || "main" : null; }
/** [{k, v, short, hint}] — short is the compact form used on Now cards. */
export function agentCard(n) {
  const ss = S.sessions.filter(s => s.name === n), s = ss[ss.length - 1]; if (!s) return [];
  const files = (S.usage && S.usage.files) || {}, init = (s.out && files[s.out] && files[s.out].init) || {};
  const L = S.running.get(n), asked = s.model, prov = s.provider || (asked && asked.includes(":") ? asked.split(":")[0] : null);
  const rows = [];
  const model = init.model ? modelName(init.model) : asked ? (prov ? asked.slice(prov.length + 1) : modelName(asked)) : "login default";
  rows.push({ k: "Model", v: model, short: model === "login default" ? "default model" : model,
    hint: (init.model ? `model id ${init.model}` : "") + (asked ? ` · asked for with --model ${asked}` : " · no --model given, so the login's default") });
  const h = s.harness || "claude-code", hn = { "claude-code": "Claude Code", codex: "Codex" }[h] || h, via = h === "claude-code" && prov && prov !== "codex";
  const harness = hn + (init.cc && h === "claude-code" ? " " + init.cc : "") + (via ? ` via ${prov}` : "");
  rows.push({ k: "Harness", v: harness, short: harness, hint: via ? `Claude Code pointed at the ${prov} API` : "the program the agent runs in" });
  const eff = s.effort || (L && L.effort);
  if (eff && !s.effort && L) s.effort = eff;
  const effText = eff || (L ? "not seen yet" : "not recorded");
  rows.push({ k: "Effort", v: effText, short: eff ? eff + " effort" : "effort " + effText,
    hint: eff ? "reasoning effort, as the harness reports it" : L ? "shows once it runs its first tool" : "this session's start didn't record an effort" });
  const acct = prov || loginName(s.login);
  if (acct) rows.push({ k: "Account", v: acct, short: acct, hint: prov ? "provider account" : "the Claude login it runs on" });
  if (init.fast === "on") rows.push({ k: "Fast mode", v: "on", short: "fast mode", hint: "" });
  if (init.perm && init.perm !== "bypassPermissions") rows.push({ k: "Permissions", v: init.perm, short: "permissions " + init.perm, hint: "" });
  return rows;
}

/** An agent's spend from its transcripts (S.agentCosts), or from the session logs when those say more: a running agent's newest
 *  turns since the last read, or a harness with no transcripts to read. stream: {c, t:[in, out, cache read, cache write]}.
 *  -> {usd, t, calls (null if unknown), m (model id -> usd), sub (usd of subagents), src} */
export function agentSpend(n, stream) {
  const A = S.agentCosts && S.agentCosts.by && S.agentCosts.by[n];
  if (A && A.usd >= stream.c) return { usd: A.usd, t: A.t, calls: A.calls, m: A.m, sub: A.sub, src: "transcripts" };
  return { usd: stream.c, t: stream.t, calls: A ? A.calls : null, m: stream.m || (A ? A.m : {}), sub: A ? A.sub : 0, src: "session logs" };
}

// ---- hive stats, last window ----
export function hiveStats(from, to) {
  const P = S.posts.filter(p => p.t >= from && p.t <= to && !SYS.has(p.from));
  let a2a = 0; const lat = []; let asked = 0; const byAuthor = {};
  for (const p of S.posts) (byAuthor[p.from] ||= []).push(p.t);
  for (const p of P) {
    const ms = p.men.filter(m => isAgent(m) && m !== p.from);
    if (isAgent(p.from) && ms.length) a2a++;
    for (const m of ms) {
      asked++; const arr = byAuthor[m] || []; let lo = 0, hi = arr.length;
      while (lo < hi) { const x = (lo + hi) >> 1; if (arr[x] <= p.t) lo = x + 1; else hi = x; }
      if (lo < arr.length && arr[lo] - p.t < 6 * HOUR) lat.push(arr[lo] - p.t);
    }
  }
  const ss = S.sessions.filter(s => s.start >= from && s.start <= to), fresh = ss.filter(s => !s.resumed);
  const byAgents = fresh.filter(s => isAgent(s.from)).length, byYou = fresh.filter(s => OUTSIDE.has(s.from)).length;
  const th = {}; for (const p of P) if (isAgent(p.from)) (th[p.th] ||= new Set()).add(p.from);
  const tl = Object.values(th);
  const links = {}; for (const p of P) for (const m of p.men) if (isAgent(p.from) && isAgent(m) && m !== p.from) { (links[p.from] ||= new Set()).add(m); (links[m] ||= new Set()).add(p.from); }
  let hub = null, hubN = 0; for (const k in links) if (links[k].size > hubN) { hub = k; hubN = links[k].size; }
  const ended = S.sessions.filter(s => s.end && s.end >= from && s.end <= to);
  return { total: P.length, a2a, asked, answered: lat.length, lat: median(lat), fresh: fresh.length, byAgents, byYou, bySys: fresh.length - byAgents - byYou,
    threads: tl.length, multi: tl.filter(s => s.size > 1).length, hub, hubN,
    clean: ended.filter(s => s.exit === 0 || s.exit == null).length, stopped: ended.filter(s => s.exit === 143).length,
    failed: ended.filter(s => s.exit === 1 && !s.limited).length, limited: S.limited.filter(t => t >= from && t <= to).length };
}
/** Agent-hours per tier in a window. */
export function tierHours(from, to) {
  const hrs = { tier1: 0, tier2: 0, held: 0, other: 0 };
  for (const s of S.sessions) { const a = Math.max(s.start, from), b = Math.min(sEnd(s), to); if (b > a) hrs[tierOf(s.thread)] += b - a; }
  return hrs;
}

// ---- usage ----
export function spendSince(from) { let c = 0; const U = S.usage; if (!U) return 0; for (const f of Object.values(U.files)) for (const h in f.h) if (+h * 1e3 >= from) c += f.h[h]; return c; }
// A usage file is <name>-<MMDD>-<HHMMSS> in the machine's local time. Match by its out name, else by agent and start minute:second.
const outCache = new Map();
export function fileInfo(k) {
  let s = S.byOut[k] || outCache.get(k);
  if (!s) {
    const m = /^(.+)-\d{4}-\d{2}(\d{2})(\d{2})$/.exec(k);
    if (m) { for (let i = S.sessions.length - 1; i >= 0; i--) { const x = S.sessions[i], d = new Date(x.start); if (x.name === m[1] && d.getMinutes() === +m[2] && d.getSeconds() === +m[3]) { s = x; break; } } }
    if (s) outCache.set(k, s);
  }
  return { name: s ? s.name : k.split("-")[0], thread: s ? s.thread : null };
}
/** Is a session's own limit reading the newest we have? An account nobody runs on keeps its last session's figure for days, so the API's reading wins when it's newer. */
export function sessionNewer(l) { return !l.read_at || ((l.limits && l.limits.at) || 0) >= l.read_at || l.weekly == null; }
/** A login's weekly share: the newest of the session logs' figure and the API reading. */
export function fresh7(l) { const w = l.limits && l.limits.seven_day; return w && sessionNewer(l) && w.r * 1e3 > now() ? w.u * 100 : l.weekly; }
export const weeklyStop = (l, U) => { const st = l.stop || {}; return st.weekly ?? st.global ?? (U && U.stop_pct) ?? 98; };
/** In use: an account the starter can still give new runs to (not paused, under its weekly stop line). */
export function inUse(l, U) { if (l.paused) return false; const v = fresh7(l); return v == null || v < weeklyStop(l, U); }
/** A login's share of all the Claude capacity: what its plan gives a week, with Pro as 1. The person's ratio (2026-11-04): Ultra (Max 20x) is 10 Pros.
 * Max 5x is a guess at 5 until the person says. An unknown plan counts as 1. */
export const PLAN_WEIGHT = { Pro: 1, "Max 5x": 5, "Max 20x": 10, Ultra: 10 };
export const planWeight = l => PLAN_WEIGHT[l.plan] ?? 1;
/** Weekly use in percent as of t: 0 once the weekly reset has passed with no newer reading. null if never read. */
export function weekNow(l, t = now()) {
  const v = fresh7(l); if (v == null) return null;
  const w = l.limits && l.limits.seven_day; if (w && sessionNewer(l) && w.r * 1e3 > t) return v;
  const r = l.resets && l.resets.weekly; return r && r * 1e3 < t ? 0 : v;
}
/** All the Claude logins as one: capacity-weighted weekly use, sum(used × weight) / sum(weight). Paused logins and ones past their
 * stop line count at what they've used, since the person asked for used against available over every plan. A login never read is left out.
 * {pct, cap, parts:[{l, v, w}], unread:[names]} or null with no reading at all. */
export function totalUse(all, t = now()) {
  const parts = [], unread = [];
  for (const l of all || []) { const v = weekNow(l, t); if (v == null) unread.push(l.name); else parts.push({ l, v, w: planWeight(l) }); }
  const cap = parts.reduce((a, x) => a + x.w, 0); if (!cap) return null;
  return { pct: parts.reduce((a, x) => a + x.v * x.w, 0) / cap, cap, parts, unread };
}
/** The accounts in use now (not paused, under their weekly line) as one, weighted by plan like totalUse: what the hive can still run on.
 * five: the same over the in-use accounts that have a 5-hour window open, {pct, r (ms, soonest refill)}. null when none is in use. */
export function activeUse(U, t = now()) {
  const ls = ((U && U.logins) || []).filter(l => inUse(l, U)); if (!ls.length) return null;
  const tot = totalUse(ls, t), f5 = [];
  for (const l of ls) {
    if (l.no_five_hour) continue;
    const L = l.limits || {}, w = L.five_hour, r5 = l.resets && l.resets.five_hour;
    if (w && sessionNewer(l) && w.r * 1e3 > t) f5.push({ v: w.u * 100, r: w.r * 1e3, w: planWeight(l) });
    else if (l.five != null && r5 && r5 * 1e3 > t) f5.push({ v: l.five, r: r5 * 1e3, w: planWeight(l) });
  }
  const c5 = f5.reduce((a, x) => a + x.w, 0);
  return { ls, pct: tot ? tot.pct : 0, cap: tot ? tot.cap : ls.reduce((a, l) => a + planWeight(l), 0), parts: tot ? tot.parts : [],
    five: c5 ? { pct: f5.reduce((a, x) => a + x.v * x.w, 0) / c5, r: Math.min(...f5.map(x => x.r)) } : null };
}
/** Simple mode's figures: how much of the account with the most room is used, this week and in this 5 hours. {week, five}, each {used, back} or null; null with no reading at all. */
export function accountUse(t = now()) {
  const all = (S.usage && S.usage.logins) || (S.live && S.live.logins) || [], rank = l => fresh7(l) ?? 101;
  const l = [...all.filter(x => inUse(x, S.usage))].sort((a, b) => rank(a) - rank(b))[0] || all[0]; if (!l) return null;
  const L = l.limits || {}, rs = l.resets || {}, v = fresh7(l), w5 = L.five_hour && L.five_hour.r * 1e3 > t ? L.five_hour : null;
  const wr = (L.seven_day && L.seven_day.r) || rs.weekly, fr = w5 ? w5.r : rs.five_hour;
  const week = v == null ? null : { used: Math.round(v), back: wr && wr * 1e3 > t ? wr * 1e3 : null };
  const five = w5 ? { used: Math.round(w5.u * 100), back: fr * 1e3 } : l.five != null && fr && fr * 1e3 > t ? { used: Math.round(l.five), back: fr * 1e3 } : null;
  return week || five ? { week, five } : null;
}
/** Pace over the last 6 hours of a login's series: {rate per second, at}. */
export function usageProjection(series) {
  const pts = (series || []).filter(x => x[2] != null && x[0] * 1e3 > now() - 6 * HOUR); if (pts.length < 2) return null;
  const a = pts[0], b = pts[pts.length - 1]; if (b[0] - a[0] < 1800) return null;
  const rate = (b[2] - a[2]) / (b[0] - a[0]); return rate > 0 ? { rate, at: b[2] } : null;
}
/** Hours until a login reaches its weekly stop at the recent pace, and hours until it resets. */
export function runway(l, U) {
  const L = l.limits || {}, stop = weeklyStop(l, U), pr = usageProjection(l.series);
  const reset = L.seven_day ? L.seven_day.r * 1e3 : l.resets && l.resets.weekly ? l.resets.weekly * 1e3 : null;
  const toStop = pr ? ((stop / 100 - pr.at) / pr.rate) * 1e3 : null;
  return { stop, toStop, toReset: reset ? reset - now() : null, before: toStop != null && reset != null && toStop < reset - now() };
}
/** When the hive runs out: the accounts in use now each reach their weekly line, at the last 6 hours' pace, or failing that the week's
 * average since it began (the line the who command draws). {at (ms from t, the last in-use account to stop), how: "6h"|"week", per: [{l, toStop, how}]}
 * when every in-use account stops before it resets; {at: null, lasts: true} when one lasts to its reset; null with no account in use or no pace. */
export function runOut(U, t = now()) {
  const ls = ((U && U.logins) || []).filter(l => inUse(l, U)); if (!ls.length) return null;
  const per = ls.map(l => {
    const r = runway(l, U), v = fresh7(l);
    if (r.toStop != null) return { l, toStop: Math.max(0, r.toStop), toReset: r.toReset, how: "6h" };
    const start = r.toReset != null ? t + r.toReset - 7 * 864e5 : null;
    if (start == null || v == null || !(v > 0) || t - start < 18e5 || v >= r.stop) return { l, toStop: null, toReset: r.toReset, how: null };
    return { l, toStop: ((r.stop - v) * (t - start)) / v, toReset: r.toReset, how: "week" };
  });
  if (per.every(x => x.toStop == null)) return null;
  if (per.some(x => x.toStop == null || (x.toReset != null && x.toStop >= x.toReset))) return { at: null, lasts: true, per };
  const last = per.reduce((a, x) => (x.toStop > a.toStop ? x : a));
  return { at: last.toStop, how: per.some(x => x.how === "week") ? "week" : "6h", per };
}
/** The next account to come in: the soonest weekly reset among the accounts that can use it. Not a paused one, a signed-out one, one whose line never lets it run, or a reset that has already passed with no new reading. {l, at, in}, or null. */
export function nextBack(U, t = now()) {
  const out = new Set(((S.app && S.app.app && S.app.setup && S.app.setup.logins) || []).filter(x => !x.signed_in).map(x => x.name));
  const when = l => { const L = l.limits || {}, rs = l.resets || {}; return [L.seven_day && L.seven_day.r, rs.weekly].map(r => (r ? r * 1e3 : 0)).find(r => r > t) ?? null; };
  return (U.logins || []).filter(l => !l.paused && !out.has(l.name) && weeklyStop(l, U) > 0).map(l => ({ l, at: when(l) })).filter(x => x.at != null).sort((a, b) => a.at - b.at).map(x => ({ ...x, in: x.at - t }))[0] || null;
}
// ---- conversations: what a thread is called, who is in it, what state it is in ----
const MENTIONS = /^(?:[ \t]*@[\w.-]+[,:]?[ \t]+)+/;
const SOURCE = /^\((?:from ([^,)\n]{1,80}), )?(web app|in Slack|via the box)(?:, replying to ".*?"|, (?:[^()\n]|\([^()\n]*\))*)?\)[ \t]*/;
const SOURCE_LABEL = { "web app": "Web", "in Slack": "Slack", "via the box": "Box" };
/** What the person typed, without the routing the server writes into the text ("@a7c31 ", "reply to message <id>: ").
 *  The board keeps the text as it is; this is for display only. */
// The developer route reads the board as it is: mentions, reply lines and thread ids stay.
export function displayText(p) {
  if (!S.app.app) return String(p.text || "");
  if (p.disp != null) return p.disp;
  let t = String(p.text || "").replace(MENTIONS, "");
  t = t.replace(/^reply to message [0-9a-f]+:\s*/i, "");
  // Where the person wrote it: the server and the Slack door head the text with it for agents to read. In the app it's a badge (postSource).
  // Only on the person's own posts, so an agent quoting one keeps its words.
  const m = p.from === "person" && t.match(SOURCE);
  p.src = m ? { name: m[1] || null, how: SOURCE_LABEL[m[2]], said: m[0].trim().slice(1, -1) } : null;
  if (m) t = t.slice(m[0].length).replace(MENTIONS, "");
  return (p.disp = t);
}
/** What the person typed, for sending again: no reply line, no source head and no agent's name (that agent may have moved on, so the To chip decides). @new and the like stay, since the person often types them. On every route, unlike displayText. */
export function typedText(p) {
  let t = String(p.text || ""); const kept = [];
  const heads = () => { const m = t.match(MENTIONS); if (m) { t = t.slice(m[0].length); kept.push(...m[0].trim().split(/[\s,:]+/)); } };
  heads(); t = t.replace(/^reply to message [0-9a-f]+:\s*/i, "");
  const m = t.match(SOURCE); if (m) t = t.slice(m[0].length);
  heads();
  const say = simpleOn() ? [] : kept.filter(n => !/^@[a-z][0-9a-f]{4}$/.test(n) && n !== "@person");
  return (say.length ? say.join(" ") + " " : "") + t;
}
/** Where one of the person's posts was written, in the app: {name or null, how: "Web" | "Slack" | "Box", said: the words as on the board}, or null. */
export function postSource(p) {
  if (!S.app.app) return null;
  displayText(p);
  return p.src || null;
}
const HEX = /^[0-9a-f]{12}$/;
/** A thread's title: its name, the name the indexer gave a number thread, or in the app the start of its first message. */
/** A /btw side thread's main conversation (person_post names it btw-<main>-<4 hex>), or null. */
/** A side question's words without the @model the composer added at the end. */
export const btwText = t => String(t || "").replace(/(\s*@[a-z][\w-]*)+\s*$/i, "");
export const btwParent = th => ((th || "").match(/^btw-(.+)-[0-9a-f]{4}$/) || [])[1] || null;
export function threadTitle(th) {
  const own = S.threadMeta && S.threadMeta[th] && S.threadMeta[th].title; if (own) return own;   // the person renamed it
  if (btwParent(th)) { const d = S.threads.get(th), p = d && d.posts[0]; return "Aside: " + (p ? short(btwText(mdPlain(displayText(p))).split("\n")[0], 60) : btwParent(th)); }
  if (!HEX.test(th || "")) return simpleOn() && th ? (t => t[0].toUpperCase() + t.slice(1))(th.replace(/[-_]+/g, " ")) : th;
  if (S.threadNames[th]) return S.threadNames[th];
  if (!S.app.app) return th;
  const d = S.threads.get(th), p = d && d.posts[0];
  return p ? short(mdPlain(displayText(p)).split("\n")[0], 64) : th;
}
/** The agent behind "@question" in the side search works here (dash.py, side_ask): an agent like any other in Agents, but its thread is no conversation, so no chat list holds it. */
export const SIDE_THREAD = "side";
/** Only the starter, keeper or box ever posted here, or it's the sidebar agent's. */
export const isSystemThread = d => !!d && (d.name === SIDE_THREAD || d.posts.every(p => SYS.has(p.from)));

// ---- how much of the board someone wants to see: Chat's For you, Updates and Agent comms ----
export const SHOW = { you: 1, updates: 2, comms: 4, ALL: 7 };   // bits: any of them can be on
const OPS = new Set(["watchdog", "memguard"]), HOUSE = new Set(["keeper", "improve", "watchdog"]);   // the hive looking after itself
/** Which one a post is. For you: from or to the person. Updates: an agent or outsider saying how it's going. Agent comms: agents talking to each other, and the hive's own notices. */
export function postKind(p) {
  if (p.lv !== undefined) return p.lv;
  if (p.th === SIDE_THREAD) return p.lv = 0;
  if (p.from === "person" || p.men.includes("person")) return p.lv = SHOW.you;
  if (SYS.has(p.from) || OPS.has(p.from) || isWarden(p.from) || HOUSE.has(p.th)) return p.lv = SHOW.comms;
  return p.lv = p.men.some(m => m !== p.from && !OUTSIDE.has(m)) ? SHOW.comms : SHOW.updates;
}
/** Every kind of post a thread has: a thread with none of what's on isn't listed. */
export function threadKinds(d) {
  if (btwParent(d.name)) return SHOW.updates;   // a side question is a process the hive runs, not the conversation: listed under Updates only (11041319bbd0)
  if (d.lvN !== d.posts.length) { let v = d.lvN ? d.lv : 0; for (let i = d.lvN || 0; i < d.posts.length; i++) v |= postKind(d.posts[i]); d.lv = v; d.lvN = d.posts.length; }
  return d.lv;
}
/** Each finished run's last word in a thread the person is part of. Agents report a result without naming the person, so For you needs these or it never shows the answer. */
export function closingPosts() {
  const out = new Set();
  for (const s of S.sessions) {
    const d = s.end && s.thread ? S.threads.get(s.thread) : null;
    if (!d || !(threadKinds(d) & SHOW.you)) continue;
    for (let i = d.posts.length - 1; i >= 0 && d.posts[i].t >= s.start; i--) {
      const p = d.posts[i];
      if (p.from === s.name && p.t <= s.end + 5000) { if (postKind(p) !== SHOW.comms) out.add(p.id); break; }
    }
  }
  return out;
}
/** Everyone who took part in a thread: posted, ran, or is queued for it. Outside names first, then running, queued, the rest. */
export function threadPeople(th) {
  const d = S.threads.get(th), m = new Set(d ? d.who : []);
  for (const s of S.sessions) if (s.thread === th) m.add(s.name);
  for (const a of (S.live ? S.live.agents : [])) if (a.thread === th && a.name) m.add(a.name);
  for (const p of (S.live ? S.live.pending : [])) if ((p.threads || []).includes(th)) m.add(p.name);
  const rank = n => (OUTSIDE.has(n) ? 0 : isRunning(n) ? 1 : S.pending[n] ? 2 : 3);
  return [...m].sort((a, b) => rank(a) - rank(b));
}
const asksQuestion = p => { const lines = mdPlain(displayText(p)).trim().split("\n").filter(l => l.trim()); return /\?["')]*\s*$/.test(lines[lines.length - 1] || ""); };
/** Who the person has been talking to in a thread: the last agent that wrote to them, or that they named. */
export function partner(th) {
  const d = S.threads.get(th); if (!d) return null;
  for (let i = d.posts.length - 1; i >= 0; i--) {
    const p = d.posts[i];
    if (OUTSIDE.has(p.from)) { const a = p.men.find(isAgent); if (a) return a; }
    else if (isAgent(p.from) && p.men.includes("person")) return p.from;
  }
  return null;
}
/** A thread's state for the person, most urgent first: {key (a STATUS key), label, who, post}. */
export function threadState(th, read) {
  const d = S.threads.get(th); if (!d) return { key: "none", label: "" };
  let lastYou = 0; for (const p of d.posts) if (OUTSIDE.has(p.from)) lastYou = p.t;
  let ask = null; for (let i = d.posts.length - 1; i >= 0 && d.posts[i].t > lastYou; i--) { const p = d.posts[i]; if (isAgent(p.from) && p.men.includes("person")) { ask = p; break; } }
  const run = S.live ? S.live.agents.filter(a => a.thread === th) : [];
  const ss = S.sessions.filter(s => s.thread === th), last = ss[ss.length - 1];
  if (ask && asksQuestion(ask)) return { key: "asks", label: "waiting for your answer", who: ask.from, post: ask };
  for (const a of run) { const st = agentState(a); if (st.key === "stuck") return { key: "stuck", label: "stuck: " + st.text, who: a.name }; }
  const busy = run.find(a => agentState(a).key === "working") || run[0];
  if (busy) { const st = agentState(busy); return { key: st.key, label: st.key === "working" ? "working" : st.text, who: busy.name, doing: busy.doing }; }
  const q = (S.live ? S.live.pending : []).find(p => (p.threads || []).includes(th));
  if (q) return { key: "queued", label: "waiting to start", who: q.name };
  if (last && last.end && last.limited && S.limitedNow.has(last.session)) return { key: "limited", label: "stopped at a usage limit", who: last.name, t: last.end };
  if (last && last.end && last.exit && last.exit !== 143 && !last.limited) return { key: "failed", label: "stopped with an error", who: last.name, t: last.end };
  if (ask && !(read && read(ask))) return { key: "foryou", label: "new reply for you", who: ask.from, post: ask };
  return { key: "done", label: last ? "done" : "", who: last ? last.name : null };
}
export { DAY, HOUR };
