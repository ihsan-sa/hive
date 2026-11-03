// What the data means: statuses, the agent card, hive stats, usage pace. Pure reads of the store.
import { S, OUTSIDE, SYS } from "./store.js";
import { now, ago, dur, median, short, mdPlain, MIN, HOUR, DAY } from "./format.js";

export const isAgent = n => S.agents.has(n);
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
  else if (S.limitedSess.has(s.session)) v = { key: "limited", label: "stopped at a limit" };
  else if (s.exit) v = { key: "failed", label: `failed (exit ${s.exit})` };
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
    failed: ended.filter(s => s.exit === 1 && !S.limitedSess.has(s.session)).length, limited: S.limited.filter(t => t >= from && t <= to).length };
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
/** A login's weekly share: the newest figure the session logs report, else the starter's own reading. */
export function fresh7(l) { const w = l.limits && l.limits.seven_day; return w && w.r * 1e3 > now() ? w.u * 100 : l.weekly; }
export const weeklyStop = (l, U) => { const st = l.stop || {}; return st.weekly ?? st.global ?? (U && U.stop_pct) ?? 98; };
/** In use: an account the starter can still give new runs to (not paused, under its weekly stop line). */
export function inUse(l, U) { if (l.paused) return false; const v = fresh7(l); return v == null || v < weeklyStop(l, U); }
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
// ---- conversations: what a thread is called, who is in it, what state it is in ----
/** What the person typed, without the routing the server writes into the text ("@a7c31 ", "reply to message <id>: ").
 *  The board keeps the text as it is; this is for display only. */
// The developer route reads the board as it is: mentions, reply lines and thread ids stay.
export function displayText(p) {
  if (!S.app.app) return String(p.text || "");
  if (p.disp != null) return p.disp;
  let t = String(p.text || "").replace(/^(?:[ \t]*@[\w.-]+[,:]?[ \t]+)+/, "");
  t = t.replace(/^reply to message [0-9a-f]+:\s*/i, "");
  return (p.disp = t);
}
const HEX = /^[0-9a-f]{12}$/;
/** A thread's title: its name, or for an unnamed job (a hex id) the start of its first message. */
export function threadTitle(th) {
  if (!S.app.app || !HEX.test(th || "")) return th;
  const d = S.threads.get(th), p = d && d.posts[0];
  return p ? short(mdPlain(displayText(p)).split("\n")[0], 64) : th;
}
/** Only the starter, keeper or box ever posted here. */
export const isSystemThread = d => !!d && d.posts.every(p => SYS.has(p.from));
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
  if (last && last.end && S.limitedSess.has(last.session)) return { key: "limited", label: "stopped at a usage limit", who: last.name, t: last.end };
  if (last && last.end && last.exit && last.exit !== 143 && !S.limitedSess.has(last.session)) return { key: "failed", label: "stopped with an error", who: last.name, t: last.end };
  if (ask && !(read && read(ask))) return { key: "foryou", label: "new reply for you", who: ask.from, post: ask };
  return { key: "done", label: last ? "done" : "", who: last ? last.name : null };
}
export { DAY, HOUR };
