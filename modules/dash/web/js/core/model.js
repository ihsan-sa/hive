// What the data means: statuses, the agent card, hive stats, usage pace. Pure reads of the store.
import { S, OUTSIDE, SYS } from "./store.js";
import { simpleOn, plainName } from "./simple.js";
import { LOC, money, now, ago, dur, median, short, mdPlain, bareLinks, firstSentence, onWords, namesNoWork, MIN, HOUR, DAY } from "./format.js";

export const isAgent = n => S.agents.has(n);
/** A warden judges whether work crosses the hard limits. The starter names them w + 4 hex (core/starter.py is_warden); `warden` and `rootwarden` are the fixed names. */
export const isWarden = n => /^(warden|rootwarden|w[0-9a-f]{4})$/.test(n);
export const WARDEN_HINT = "A warden: it judges whether work crosses the hard limits, and doesn't take ordinary work.";
export const isRunning = n => S.running.has(n);
export const sEnd = s => s.end ?? (isRunning(s.name) ? now() : s.start);
export function agentThread(n) { for (let i = S.sessions.length - 1; i >= 0; i--) if (S.sessions[i].name === n) return S.sessions[i].thread; return null; }

/** What the app look calls an agent: its conversation's agent ("Project 66's agent"), or "an agent". Outside a conversation the code stays in the hover title; it is always on the agent page, and the developer look shows it. */
// Inside a conversation an agent is named by its place in that conversation, not by whatever it last worked on (R-1110e-b..e): one agent is "the agent", several are
// "Agent A", "Agent B" in the order they first appeared, so a name and its avatar letter hold still as the agents move on. Drawing code scopes it with inConversation(th, fn).
let HERE = null;
export const inConversation = (th, fn) => { const was = HERE; HERE = th || null; try { return fn(); } finally { HERE = was; } };
const CODE_NAME = /^a[0-9a-f]{4}$/, CAST = new Map();
// Sessions by thread, rebuilt when the session list grows: cast runs for every agent named outside its conversation, and a scan of every session each time was slow.
let BY_TH = { n: -1, m: new Map() };
function sessionsIn(th) {
  if (BY_TH.n !== S.sessions.length) { const m = new Map(); for (const s of S.sessions) { let l = m.get(s.thread); if (!l) m.set(s.thread, l = []); l.push(s); } BY_TH = { n: S.sessions.length, m }; }
  return BY_TH.m.get(th) || [];
}
function cast(th) {
  const d = S.threads.get(th), n = d ? d.posts.length : 0, c = CAST.get(th);
  if (c && c.n === n && c.s === S.sessions.length && c.live === S.live) return c.names;
  // An heir (a fresh agent the starter hands the chat to after its agent said Done) carries on the same conversation: it is the same agent to the person (E12b),
  // so it takes the name of the one it took over from, and the cast counts the chain once.
  const heirOf = new Map(); for (const s of sessionsIn(th)) if (s.heirOf && s.heirOf !== s.name) heirOf.set(s.name, s.heirOf);
  const root = a => { for (let i = 0; i < 50 && heirOf.has(a); i++) a = heirOf.get(a); return a; };
  const first = new Map(), saw = (a, t) => { if (!CODE_NAME.test(a)) return; a = root(a); if (!(first.get(a) <= t)) first.set(a, t); };
  if (d) for (const p of d.posts) saw(p.from, p.t);
  for (const s of sessionsIn(th)) saw(s.name, s.start);
  for (const a of threadPeople(th)) if (CODE_NAME.test(a) && !first.has(root(a))) first.set(root(a), Infinity);   // running or queued here, nothing posted yet: after the ones already seen
  const order = [...first].sort((a, b) => a[1] - b[1] || (a[0] < b[0] ? -1 : 1)).map(x => x[0]), names = new Map();
  // Past Z a letter runs out and "Agent 30" tells a person nothing (R-1110f-f): say where it lives, else what it was asked here, else two letters.
  const taken = new Set(), late = (a, i) => {
    for (const l of [homeLabel(a, th), askedName(a, th)]) if (l && !taken.has(l)) { taken.add(l); return l; }
    return "Agent " + String.fromCharCode(65 + Math.floor(i / 26) - 1) + String.fromCharCode(65 + i % 26);
  };
  order.forEach((a, i) => names.set(a, order.length === 1 ? "the agent" : i < 26 ? "Agent " + String.fromCharCode(65 + i) : late(a, i)));
  for (const a of heirOf.keys()) if (names.has(root(a))) names.set(a, names.get(root(a)));
  CAST.set(th, { n, s: S.sessions.length, live: S.live, names });
  return names;
}
/** True while drawing inside one conversation (inConversation): the app look's hovers there name no code either (E10). */
export const inScope = () => HERE != null;
/** A step that is only the agent talking to its Hive (reading the board, posting, setting its status): nothing a person needs to read in the app look. */
export const ownStep = d => /^Running:\s*(?:cd\s+\S+\s*(?:&&|;)\s*)?(?:hive|h2|kb)\b/.test(String(d || ""));
// A title too long to say whole: its first words, cut at a word and never on a little word ("Fix the bug in's agent").
const titleName = t => { let w = t.split(/\s+/).slice(0, 4); while (w.length > 1 && /^(a|an|the|of|in|on|to|for|and|or|with|at|by|from|is|its)$/i.test(w.at(-1))) w.pop(); return w.join(" ").replace(/[\s,;:.\u2013\u2014-]+$/, ""); };
export function agentLabel(n) {
  if (!n || !S.app || !S.app.app) return n;
  if (simpleOn()) return plainName(n);
  if (n === "person") return "you";
  if (n === "starter") return "the Hive";   // a scheduled wake ("Asked by starter: Hop 13") is the Hive asking, and "starter" is a builder word
  if (isWarden(n)) return "the warden";
  if (!CODE_NAME.test(n)) return n;
  const here = HERE && cast(HERE).get(n); if (here) return here;
  return awayLabel(n) || "an agent";
}
/** Outside its conversation an agent is "X's agent", and when X has had several agents, with the mark it has inside X ("UI design loop's agent B", "Feedback's agent on the zoom"),
 *  so two agents of one conversation never share a name on Now, Map hand-offs or a focus's agent list (round 1111a), and the name matches what the conversation calls it. */
function awayLabel(n) {
  let base = homeLabel(n); if (!base) return "";
  const th = agentThread(n), q = apart(base, th); if (q) base += " (" + q + ")";
  const names = cast(th); if (names.size < 2) return base;
  const c = names.get(n) || "", m = /^Agent ([A-Z]{1,2})$/.exec(c);
  return m ? base + " " + m[1] : /^Agent on /.test(c) ? base + c.slice(5) : base;
}
// Two conversations can shorten to one name ("Phone call handling's agent" for ten of them, R-1111a-a): such a name takes what differs in its title, else its day.
let BASES = null, BASES_N = -1;
function apart(base, th) {
  if (BASES_N !== S.sessions.length) {
    BASES_N = S.sessions.length; BASES = new Map();
    const seen = new Set(); for (let i = S.sessions.length - 1; i >= 0; i--) { const t = S.sessions[i].thread; if (!t || seen.has(t)) continue; seen.add(t); const b = threadBase(t); if (b) (BASES.get(b) || BASES.set(b, []).get(b)).push(t); }
  }
  const ths = BASES.get(base); if (!ths || ths.length < 2 || !ths.includes(th)) return "";
  const rest = t => { const x = threadTitle(t) || "", i = x.indexOf(":"); return i > 0 && i < 40 ? titleName(x.slice(i + 1).trim().replace(/^(?:(?:a|an|the|of|in|on|to|for|and|or|with|at|by|from)\s+)+/i, "")) : ""; };
  const d = t => { const f = S.sessions.find(x => x.thread === t); return f && f.start ? new Date(f.start).toLocaleDateString(LOC, { month: "short", day: "numeric" }) : ""; };
  const lab = t => { const m = rest(t); return ths.some(o => o !== t && rest(o) === m) ? [m, d(t)].filter(Boolean).join(", ") : m || d(t); };
  const mine = lab(th), twins = ths.filter(t => lab(t) === mine);   // the same words on the same day: told apart by number
  return twins.length > 1 ? mine + (mine ? ", " : "") + "no. " + (twins.indexOf(th) + 1) : mine;
}
/** "X's agent" from the title of the thread an agent lives in; "" when that says nothing, or (skip) it is the conversation being read. */
function homeLabel(n, skip) { const th = agentThread(n); return th && th !== skip ? threadBase(th) : ""; }
/** "X's agent" for a conversation by its own title: apart() files each conversation under this, not under the base of whichever agent spoke there last (that agent may live elsewhere now). */
function threadBase(th) {
  const t = threadTitle(th);
  if (!t || t === "A conversation" || /\ba[0-9a-f]{4}\b/.test(t)) return "";
  // "RSI lab: measure the hive and keep improving it" is "RSI lab's agent": the part before the colon names it, and a name cut in the middle read badly beside "'s agent".
  const head = /^Aside:/.test(t) ? "" : (t.match(/^([^:]{3,32}):\s/) || [])[1];
  const w = (head || (t.length <= 32 ? t : titleName(t))).replace(/[\s?!.,;:\u2026]+$/, "");
  return w + (/[^suiSUI]s$/.test(w) ? "' agent" : "'s agent");   // "RSI lab rules' agent", not "rules's"; "Hive status's agent" keeps its s   // "Do modules update?" is "Do modules update's agent", never "?'s agent"
}
/** What an agent was first asked in this conversation, in a few words ("Agent on mesh page: the zoom"): the post that woke it, without its mentions, codes or "Brief:" lead. */
function askedName(n, th) {
  const s = S.sessions.find(x => x.name === n && x.thread === th && x.post), p = s && S.byId[s.post];
  if (!p || !p.text || S.sessions.some(x => x.name === p.from && x.post && (S.byId[x.post] || {}).from === n)) return "";   // a reply to its own brief asked it nothing
  let t = String(p.text).split(/\n/).find(l => l.trim() && !/^\s*(?:@\S+\s*)+$/.test(l)) || "";
  t = t.replace(/@\S+/g, " ").replace(/\([^)]*\)/g, " ").replace(/\b[a-z](?=[0-9a-f]{0,3}\d)[0-9a-f]{4}\b|\bk\d{3,}\b/g, " ")
    .replace(/^\s*(?:keeper\s+)?(?:brief|round)\b[^:]{0,70}:\s*/i, "").replace(/^\s*feedback on the app,\s*from the\s*/i, "")
    .replace(/[`*_#>]/g, "").replace(/\s+/g, " ").trim().split(/[.!?;](?:\s|$)/)[0];   // its first sentence: "R79 is done. Inbox" read as one name
  const w = titleName(t.replace(/^(?:(?:a|an|the|of|in|on|to|for|and|or|with|at|by|from)\s+)+/i, "")).replace(/[\s?!.,;:\u2026]+$/, "");
  // a rung or post id says nothing to a person ("UI redesign's agent on R79 is done", R-1111a-e): such an agent takes two letters instead
  return w.length >= 3 && !/\b[A-Z]{1,2}-?\d|\b[0-9a-f]{12}\b/.test(w) ? "Agent on " + w : "";
}

/** An agent's two marks on its avatar: a8779 → 87, other names their first two letters. The app look shows no code there either (E10): the first letter of what agentLabel calls it ("Why is the sky…'s agent" → W). */
export function agentMono(n) {
  if (!/^[a-z][0-9a-f]{4,}$/.test(n)) return String(n).slice(0, 2);
  if (!S.app || !S.app.app) return n.slice(1, 3);
  const l = agentLabel(n), letter = /^Agent (\S+)$/.exec(l); if (letter) return letter[1];
  const on = /^Agent on (.)/u.exec(l); if (on) return on[1].toUpperCase();
  const c = l && l !== n && l !== "an agent" && l !== "the agent" ? (l.match(/[\p{L}\p{N}]/u) || [])[0] : "";
  return c ? c.toUpperCase() : "A";
}

/**
 * Agent codes inside a line of text, as agentLabel says them (the app look only; a known agent, or a + 4 hex with a digit, so "added" stays a word
 * and so does "c0ffe", which no agent is called).
 * A code that is part of something else stays as written: a path, a flag or a file name (a "/", "\", "=" or "-" before it, a "/", "\" or -_. and a
 * letter after it: tmp/a0f4b/x, --to=a0f4b, a6aea-mesh.png), a web address, and a `code span` that holds more than the code. A span that is only the
 * code reads as the agent, without its backticks.
 * unnamed: a Map the caller keeps for one piece of text of several lines (a summary). Two agents with no name of their own both read "an agent",
 * which says they are one: with the map the second is "another agent", the third "a third agent", in the order the lines name them.
 */
const CODE_IN = /(^|[^\w@/\\=-])@?([a-z][0-9a-f]{4})\b(?![-_.]\w|[/\\])/g, CODE_SKIP = /`([^`\n]+)`|\bhttps?:\/\/[^\s<>"'`]+/g, CODE_ONLY = /^\s*@?([a-z][0-9a-f]{4})\s*$/;
const NTH = ["an agent", "another agent", "a third agent", "a fourth agent", "a fifth agent", "a sixth agent"];
export function labelCodes(text, unnamed) {
  if (!text || !S.app || !S.app.app) return text;
  text = String(text);
  const say = (n, lead) => {
    if (!isAgent(n) && !(n[0] === "a" && /\d/.test(n))) return null;   // an agent the board knows, whatever letter its code starts with (older ones: w299e); else only a + 4 hex with a digit
    let l = agentLabel(n);
    if (l === n) return null;   // the label is the code itself (an agent of a code shape that has no label): left as written, not given a capital
    if (unnamed && l === "an agent") { if (!unnamed.has(n)) unnamed.set(n, NTH[unnamed.size] || "one more agent"); l = unnamed.get(n); }
    return lead ? capFirst(l) : l;
  };
  // lead: the code opens the text or a sentence of it, so its label takes a capital
  const lead = i => /(?:^\s*|[.!?]\s+|\n\s*)["'(\[*_“‘]*$/.test(text.slice(0, i));
  const plain = (s, off) => s.replace(CODE_IN, (m, a, n, i) => { const l = say(n, lead(off + i + a.length)); return l == null ? m : a + l; });
  let out = "", at = 0;
  for (const m of text.matchAll(CODE_SKIP)) {
    out += plain(text.slice(at, m.index), at);
    const only = m[1] != null && CODE_ONLY.exec(m[1]), l = only ? say(only[1], lead(m.index)) : null;
    out += l == null ? m[0] : l; at = m.index + m[0].length;
  }
  return out + plain(text.slice(at), at);
}
export const capFirst = s => (s ? s[0].toUpperCase() + s.slice(1) : s);

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
/** Why the starter stopped a run it will resume by itself (the "died" log event's why), in the starter's own words (core/starter.py WHY_WORDS). */
const RETRY_WORDS = { connection: "no connection to Claude", restart: "the machine restarted", stalled: "went silent with nothing running", other: "ended before it finished", budget: "reached its budget or turn limit" };
export const retryWords = why => RETRY_WORDS[why] || RETRY_WORDS.other;
/** Whether a run is parked: it stopped at a usage limit, or broke off in a way the starter retries, and the starter will carry it on by itself. */
const parkedSess = s => !!(s && s.end && ((s.limited && S.limitedNow.has(s.session)) || (s.retry && S.diedNow.has(s.session))));
/** Any name's status: {key, label}. Cached per poll. */
export function agentStatus(n) {
  let v = S.stCache.get(n); if (v) return v;
  const L = S.running.get(n), s = S.lastSess[n];
  if (OUTSIDE.has(n)) v = { key: "outside", label: "" };
  else if (L) { const st = agentState(L); v = { key: st.key, label: st.text }; }
  else if (S.pending[n] && !parkedSess(s)) v = { key: "queued", label: "queued" };   // parked for a resume with a message queued for it: still stopped, the two rules below say how (threadRun)
  else if (!s) v = { key: "none", label: "hasn't run" };
  else if (S.killed[n] >= s.start) v = { key: "killed", label: "killed" };
  else if (s.replacedBy) v = { key: "ended", label: "replaced by " + s.replacedBy };
  else if (S.limitedNow.has(s.session)) v = { key: "limited", label: "stopped at a limit" };
  else if (s.retry && S.diedNow.has(s.session)) v = { key: "limited", label: "stopped, carries on by itself" };
  else if (s.exit && s.exit !== 143) v = { key: "failed", label: `failed (exit ${s.exit})` };
  else v = { key: "ended", label: "ended " + ago(sEnd(s)) };
  S.stCache.set(n, v); return v;
}
/** What an agent is on, in its own words (`hive me`) or, failing that, the first sentence of the post that started its last run.
 *  {text, at (ms), said: it set the line itself, before: the line predates its current run (said in an earlier one)} or null. */
export function agentOn(n) {
  const me = S.agentStatus[n], s = S.lastSess[n];
  if (me && me.text) return { text: me.text, at: me.at * 1e3, said: true, before: !!(s && S.running.get(n) && me.at * 1e3 < s.start - MIN) };
  const p = s && S.byId[s.post]; if (!p) return null;
  const asked = firstSentence(displayText(p));
  // these posts don't say what this one is on, so its thread's status bar does: a reply from another agent or rootwarden on a wake (often a result, "Rung done: ..."), a post meant for the agent an heir took over from, a person's question that names no work.
  // A first run's post is its brief (a status bar there can be another job's: a warden asked for a ruling in someone's thread), a person's instruction stays, and a system post (keeper, post-land check, release train) says what the run is for
  const notIt = p.from === "person" ? namesNoWork(displayText(p)) : s.heirOf || (s.resumed && (S.agents.has(p.from) || p.from === "rootwarden"));
  const bar = notIt && S.threadStatus[p.thread], line = bar && bar.text && bar.text.split("\n").map(x => x.trim()).find(Boolean);
  return { text: line || onWords(asked), asked, bar: !!line, at: s.start, said: false, before: false };
}

/** What the Now page's "Doing now" and the agent page's header say an agent is on: its own summary, never the command it is running
 *  (the person, 11081450f3e1 and 11081941703e). Its `hive me` line, else the first line of its conversation's status bar, else what its brief asks for.
 *  {text, at?, before?, own?} or null. */
export function agentSay(n, thread) {
  const on = agentOn(n);
  if (on && on.said) return { text: on.text, at: on.at, before: on.before, own: true };
  const bar = thread && S.threadStatus && S.threadStatus[thread], line = bar && bar.text && bar.text.split("\n").map(x => x.trim()).find(Boolean);
  return line ? { text: line } : on && on.text && !on.asked ? { text: on.text } : null;
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
    claude: h === "claude-code" && !prov, hint: eff ? "reasoning effort, as the harness reports it" : L ? "shows once it runs its first tool" : "this session's start didn't record an effort" });
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

/** True until the first read of the transcripts has come back: session logs alone would show today's agents at $0.00 and a total a fraction of the real one. No h2-cost: at stays 0, refreshing false, and this is false. */
export const costsPending = () => !S.agentCosts || (!S.agentCosts.at && !!S.agentCosts.refreshing);

/** An agent's cost so far in USD over all its runs (agentSpend over its sessions' logs), or null if nothing is recorded. */
export function agentCost(n) {
  const files = (S.usage && S.usage.files) || {}, st = { c: 0, t: [0, 0, 0, 0], m: {} }; let seen = false;
  for (const x of S.sessions) if (x.name === n && x.out && files[x.out]) {
    const f = files[x.out]; seen = true; st.c += f.c || 0;
    for (let i = 0; i < 4; i++) st.t[i] += (f.t && f.t[i]) || 0;
    for (const k in f.m || {}) st.m[k] = (st.m[k] || 0) + f.m[k];
  }
  const A = S.agentCosts && S.agentCosts.by && S.agentCosts.by[n];
  return seen || A ? agentSpend(n, st).usd : null;
}

/** "Sonnet 5.5 · $1.91": an agent's model and cost so far in one short line, for lists of many agents. Cached until the sessions or usage change. */
let briefFor = [], briefs = new Map();
export function agentBrief(n) {
  const key = [S.sessions, S.sessions.length, S.usage && S.usage.files, S.agentCosts, S.live];
  if (key.some((k, i) => k !== briefFor[i])) { briefFor = key; briefs = new Map(); }
  if (!briefs.has(n)) {
    const m = (agentCard(n)[0] || {}).short, c = agentCost(n);
    briefs.set(n, [m, c != null ? money(c) : null].filter(Boolean).join(" · "));
  }
  return briefs.get(n);
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
/** The login's own 5-hour stop line, when it has one and its 5-hour window is still open (the starter's pick_login skips it past that line too). */
export function fiveFull(l) { const st = l.stop || {}, w = l.limits && l.limits.five_hour, r5 = l.resets && l.resets.five_hour;
  const used = w && w.r * 1e3 > now() ? w.u * 100 : l.five != null && r5 && r5 * 1e3 > now() ? l.five : null;
  return st.five_hour != null && used != null && used >= st.five_hour; }
export function inUse(l, U) { if (l.paused || fiveFull(l)) return false; const v = fresh7(l); return v == null || v < weeklyStop(l, U); }
/** The one answer to "is the Hive held at a usage limit?": {why: "limit"|"paused", until} or null. The starter's hold (S.live.limit_until, while it lasts)
 *  or every Claude login past its line / paused; with a Codex login, a key or a fallback chain (setup.other, setup.fallback) agents still start, so nothing is held. Every banner, pill
 *  and strip line reads this (shell.js hiveStopped, comms.js hasRoom), so two of them can't disagree. */
export function limitStop() {
  const su = (S.app && S.app.setup) || {}; if (su.other || su.fallback) return null;   // a Codex login, a key or a fallback chain: runs still start
  const all = (S.usage && S.usage.logins) || (S.live && S.live.logins) || [], hold = S.live && S.live.limit_until;
  if (hold && hold * 1e3 > now()) return { why: "limit", until: hold * 1e3 };
  if (all.length && !all.some(l => inUse(l, S.usage))) {
    const r = all.filter(l => !l.paused).map(l => runway(l, S.usage).toReset).filter(x => x != null).sort((a, b) => a - b)[0];
    // The person\'s own posts still start past the stop line, up to a real limit (starter.login_for, a fixed 100 that setup.person_stop reports): say so, so nothing claims "nothing starts".
    const line = su.person_stop ?? 100, own = all.some(l => !l.paused && (fresh7(l) == null || fresh7(l) < line));
    return { why: all.every(l => l.paused) ? "paused" : "limit", until: r != null && r > 0 ? now() + r : null, own };
  }
  return null;
}
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
// E12: the line the server puts before an edited message for the agent to read (dash.py person_post); the chat shows the version arrows instead.
export const EDIT_NOTE = /^(?:\(edited message [0-9a-f]+: [^\n]*\)\n)+/i;
// The note an edit carries for the agent; must match EDIT_NOTE in dash.py byte for byte, since the page signs it with the words.
export const editNote = v => `(edited message ${v}: this version replaces it. Answer it instead, as if that message and your answer to it were never sent; anything already done for it stays done, so don't redo it.)\n`;
export function displayText(p) {
  if (!S.app.app) return String(p.text || "");
  if (p.disp != null) return p.disp;
  let t = String(p.text || "").replace(MENTIONS, "");
  t = t.replace(/^reply to message [0-9a-f]+:\s*/i, "");
  if (p.retry_of) return (p.disp = "Asked for another answer");   // E12: the chat hides it and shows the reply after it as the reply's newer version
  t = t.replace(EDIT_NOTE, "");
  // Where the person wrote it: the server and the Slack door head the text with it for agents to read. In the app it's a badge (postSource).
  // Only on the person's own posts, so an agent quoting one keeps its words.
  const m = p.from === "person" && t.match(SOURCE);
  p.src = m ? { name: m[1] || null, how: SOURCE_LABEL[m[2]], said: m[0].trim().slice(1, -1) } : null;
  if (m) t = t.slice(m[0].length).replace(MENTIONS, "").replace(EDIT_NOTE, "");
  // An older post of only files carried "Attached: <their names>" as its text; the files show themselves, so it shows none.
  if (p.from === "person" && (p.files || []).length && t.trim() === "Attached: " + p.files.map(f => f.name).join(", ")) t = "";
  // an agent's "@person" on a line of its own words: the post already says "to you", so the bare handle is noise
  if (p.from !== "person") t = t.replace(/^([ \t]*)@person\b[,:]?[ \t]*/gm, "$1").replace(/[ \t]+@person[.!]?[ \t]*$/gm, "");
  // an ask id (k1812) means nothing to a person; the timer's note says "a request" instead
  if (p.from !== "person" && isScript(p.from)) t = t.replace(/\bk\d{4}\b/g, "a request");
  return (p.disp = t);
}
// An agent's closing "Done: …" line is its reason for stopping, for the status bar and the log, not for the chat (the person, 110814274633).
// Hidden from the message, the side list's preview and Yours when other words remain; "View source" and the thread's status bar still have it.
// Blocked/Waiting/Needs the person stay. A line of @names after it (the "end your result post with @name" hand-back) goes with it (R25).
const DONE_LAST = /\n[ \t]*(?:\*\*)?Done:[^\n]*(?:\s*\n[ \t]*(?:@[\w.-]+[ \t,.]*)+)?(?=\s*$|\s*\n+[ \t]*(?:\*\*)?(?:Next|Measure):[^\n]*(?:\n[ \t]*(?:\*\*)?(?:Next|Measure):[^\n]*)*\s*$)/;
export const chatText = p => { const t = displayText(p); if (p.from === "person" || !DONE_LAST.test(t)) return t; const r = t.replace(DONE_LAST, "").replace(/\s+$/, ""); return r.trim() && !/^(?:[ \t]*@[\w.-]+[ \t,.]*)+$/.test(r) ? r : t; };
/** What the person typed, for sending again: no reply line, no source head and no agent's name (that agent may have moved on, so the To chip decides). @new and the like stay, since the person often types them. On every route, unlike displayText. */
export function typedText(p) {
  let t = String(p.text || ""); const kept = [];
  const heads = () => { const m = t.match(MENTIONS); if (m) { t = t.slice(m[0].length); kept.push(...m[0].trim().split(/[\s,:]+/)); } };
  heads(); t = t.replace(/^reply to message [0-9a-f]+:\s*/i, "").replace(EDIT_NOTE, "");
  const m = t.match(SOURCE); if (m) t = t.slice(m[0].length).replace(EDIT_NOTE, "");
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
/** The conversation a thread was started from, or null. The first post carries `parent` (hive post stamps the thread its agent
 * was woken in); older boards have none, so it falls back to the author's session at that moment: an agent that opens a thread
 * while running in another was sent from there (the person, 110601450d55: helpers' threads swamped the Chat list). */
export function threadParent(th) {
  const d = S.threads.get(th); if (!d || !d.posts.length) return null;
  if (d.parentN === d.posts.length && d.parentAt === S.sessions.length) return d.parent;
  d.parentN = d.posts.length; d.parentAt = S.sessions.length;
  const p = d.posts[0]; let par = p.parent || btwParent(th);
  if (!par && isAgent(p.from) && p.from !== "person") {
    let best = null;
    for (const s of S.sessions) if (s.name === p.from && s.thread && s.thread !== th && s.start <= p.t && p.t <= (s.end == null ? Infinity : s.end + 5000) && (!best || s.start > best.start)) best = s;
    par = best ? best.thread : null;
    if (best && best.end == null) d.parentN = -1;   // the session is still open: look again once it has ended
  }
  return d.parent = par && par !== th && S.threads.has(par) ? par : null;
}
/** Threads started from this one, oldest first. */
export function threadChildren(th) { const out = []; for (const d of S.threads.values()) if (d.name !== th && threadParent(d.name) === th) out.push(d.name); return out; }
// A slug read as words: first letter up, and the acronyms people write in capitals ("ui-test-loop" is "UI test loop", not "Ui test loop").
const ACRONYM = /\b(?:ui|ux|ai|rsi|pcb|fpga|adc|pll|gps|sbc|api|cli|llm|gpu|cpu|mcp|pdf|url|usb|kb|h[0-9])\b/gi;
const slugWords = th => (t => t[0].toUpperCase() + t.slice(1))(th.replace(/[-_]+/g, " ").replace(ACRONYM, m => m.toUpperCase()));
export function threadTitle(th) {
  const own = S.threadMeta && S.threadMeta[th] && S.threadMeta[th].title; if (own) return own;   // the person renamed it
  if (btwParent(th)) { const d = S.threads.get(th), p = d && d.posts[0]; return "Aside: " + (p ? short(bareLinks(btwText(mdPlain(displayText(p))).split("\n")[0]), 60) : btwParent(th)); }
  if (!HEX.test(th || "")) return (simpleOn() || S.app.app) && th ? ({ keeper: "Hive upkeep", watchdog: "Hive watch" }[th] || slugWords(th)) : th;   // "keeper" and "watchdog" are words only the builders use
  if (S.threadNames[th]) return S.threadNames[th];
  if (!S.app.app) return th;
  const d = S.threads.get(th), p = d && d.posts[0];
  return p ? short(bareLinks(mdPlain(displayText(p)).split("\n")[0]), 64) : "A conversation";
}
/** The agent behind "@question" in the side search works here (dash.py, side_ask): an agent like any other in Agents, but its thread is no conversation, so no chat list holds it. */
export const SIDE_THREAD = "side";
/** Only the starter, keeper or box ever posted here, or it's the sidebar agent's. */
/** The hive's own upkeep conversations, as against the work: Agents leaves their runs out of the counts, Timeline folds their rows. */
export const HOUSE_THREADS = new Set(["thread-status", "keeper", "improve", "watchdog"]);
export const isSystemThread = d => !!d && (d.name === SIDE_THREAD || d.posts.every(p => SYS.has(p.from)));

// ---- how much of the board someone wants to see: Chat's For you, Updates and Agent comms ----
export const SHOW = { you: 1, updates: 2, comms: 4, ALL: 7 };   // bits: any of them can be on
const OPS = new Set(["watchdog", "memguard"]), HOUSE = new Set(["keeper", "improve", "watchdog"]);   // the hive looking after itself
/** A line starting "For you:" marks what an agent wants the person to know, do or answer (110401502e1a). */
export const FOR_YOU = /^\s*(?:[-*>]\s*)?(?:\*\*|__)?for you(?:\*\*|__)?\s*:\s*(?:\*\*|__)?\s*(.+)$/im;
const ANSWER_FOR = 24 * 3600 * 1000;
/** The person's latest post in the thread before p (set once per post as the thread grows). */
function askBefore(p) {
  const d = S.threads.get(p.th); if (!d) return null;
  if (d.askN !== d.posts.length) { let cur = d.askN ? d.posts[d.askN - 1].ask || null : null; for (let i = d.askN || 0; i < d.posts.length; i++) { const q = d.posts[i]; q.ask = cur; if (q.from === "person") cur = q; } d.askN = d.posts.length; }
  return p.ask || null;
}
/** An agent's answer to the person: it replies to their post, carries a "For you:" line, or is what the agent they named says back in the same thread within a day
 *  (agents answer in the thread without naming the person, so by mentions alone every answer was an Update: 110803115633). */
function answersPerson(p) {
  if (!isAgent(p.from)) return false;
  if (p.reply_to) { const q = S.byId[p.reply_to]; if (q && q.from === "person") return true; }
  if (FOR_YOU.test(p.text || "")) return true;
  const a = askBefore(p);
  return !!a && a.men.includes(p.from) && p.t - a.t < ANSWER_FOR;
}
/** A timer or checker posting under its own name (asks, progress, release-train, report-triage...), as against an agent, the person or another Hive.
 * What a script writes is for agents, so it is Agent comms, unless it names the person, replies to them or carries a "For you:" line. Any sender that
 * isn't known to be someone counts as a script: the lists of system senders here went stale and a new script's notes then landed in the person's chat
 * as Updates (the person, 11101818169f). The two scripts that write for the person without naming them are listed. */
const FOR_PERSON = new Set(["phone", "morning"]);
export const isScript = n => !/^[a-z][0-9a-f]{4,5}$/.test(n) && !OUTSIDE.has(n) && !FOR_PERSON.has(n) && !/^hive-/.test(n) && !isAgent(n);
/** Which one a post is. For you: from or to the person, or an agent answering them. Updates: an agent or outsider saying how it's going. Agent comms: agents talking to each other, and the hive's own notices, a script's among them. */
export function postKind(p) {
  if (p.lv !== undefined) return p.lv;
  if (p.th === SIDE_THREAD) return p.lv = 0;
  if (p.from === "person" || p.men.includes("person")) return p.lv = SHOW.you;
  if (SYS.has(p.from) || OPS.has(p.from) || isWarden(p.from) || HOUSE.has(p.th)) return p.lv = SHOW.comms;
  if (p.men.some(m => m !== p.from && !OUTSIDE.has(m))) return p.lv = SHOW.comms;
  if (isScript(p.from)) return FOR_YOU.test(p.text || "") || (p.reply_to && S.byId[p.reply_to]?.from === "person") ? SHOW.you : SHOW.comms;   // not kept in p.lv: a named agent is only known once its first start has loaded
  return p.lv = answersPerson(p) ? SHOW.you : SHOW.updates;
}
/** Every kind of post a thread has: a thread with none of what's on isn't listed. */
export function threadKinds(d) {
  if (btwParent(d.name)) return SHOW.updates;   // a side question is a process the hive runs, not the conversation: listed under Updates only (11041319bbd0)
  if (d.lvA !== S.agents.size) { d.lvN = 0; d.lvA = S.agents.size; }   // a newly known agent's posts stop counting as a script's
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
export const asksQuestion = p => { const lines = mdPlain(displayText(p)).trim().split("\n").filter(l => l.trim()); return /\?["')]*\s*$/.test(lines[lines.length - 1] || ""); };
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
// Running agents by thread, built once per /api/live reply rather than filtered per thread.
let rbt = { live: undefined, m: new Map() };
function runsByThread() {
  if (rbt.live === S.live) return rbt.m;
  const m = new Map(); for (const a of (S.live ? S.live.agents : [])) { const l = m.get(a.thread); if (l) l.push(a); else m.set(a.thread, [a]); }
  rbt = { live: S.live, m }; return m;
}
/**
 * What a thread's run is doing now, or how its last one broke, most urgent first: {key, label, who, ...}, or null when no agent is on it and its last
 * run ended well (or it never ran). The part of threadState that is about the run and not about the person: threadStand reads it apart, so an ask the
 * needs list doesn't count can't hide an agent that is working, stuck or failed under it.
 */
export function threadRun(th) {
  const run = runsByThread().get(th) || [], last = S.lastByThread[th];
  for (const a of run) { const st = agentState(a); if (st.key === "stuck") return { key: "stuck", label: "stuck: " + st.text, who: a.name }; }
  const busy = run.find(a => agentState(a).key === "working") || run[0];
  if (busy) { const st = agentState(busy); return { key: st.key, label: st.key === "working" ? "working" : st.text, who: busy.name, doing: busy.doing }; }
  // A message for a parked agent (stopped at a usage limit, or broken off and due a retry) sits in the starter's queue until that run carries on: the
  // starter starts nothing new for a name it is about to resume, resumes it once there is room (or the retry is due), and the run reads the queue
  // then (starter.py: "resume limit-stopped sessions first", and the queue loop skips a name in st["resume"]). So that conversation is still paused,
  // not about to start (HA-14: the person's two messages in two weeks that met this were read 73 minutes later, under "Waiting to start"; a retry
  // after a lost connection can be an hour away). ids: the messages the queue holds for it.
  // An agent is parked by its own last run, wherever that was (S.lastSess, not this conversation's last run): the queue is one per agent, so a
  // message written here for an agent whose paused run was in another conversation waits just the same. Reading the conversation's last run said
  // "Waiting to start" for it, with Carry on beside it (the review of HA-14).
  const qs = (S.live ? S.live.pending : []).filter(p => (p.threads || []).includes(th));
  const q = qs.find(p => !parkedSess(S.lastSess[p.name]));
  if (q) return { key: "queued", label: "waiting to start", who: q.name };
  const paused = (s, ids) => (s.limited && S.limitedNow.has(s.session) ? { key: "limited", label: "stopped at a usage limit", who: s.name, t: s.end, ids }
    : { key: "limited", label: "stopped, carries on by itself", who: s.name, t: s.end, retry: s.retry, ids });
  // What is paused here: this conversation's own last run, then every agent the queue holds a message for (all of them parked, or q had one). The
  // one that holds a message of the person's leads, since that is the one their message waits on.
  const lp = parkedSess(last), own = lp ? qs.find(p => p.name === last.name) : null;
  const c = [...(lp ? [own ? paused(S.lastSess[last.name], own.ids || []) : paused(last, [])] : []), ...qs.filter(p => p !== own).map(p => paused(S.lastSess[p.name], p.ids || []))];
  if (c.length) return (c.length > 1 && c.find(x => heldForYou(th, x))) || c[0];
  if (last && last.end && last.exit && last.exit !== 143 && !last.limited) return { key: "failed", label: "stopped with an error", who: last.name, t: last.end };
  return null;
}
/**
 * Whether the starter holds a message of the person's for this conversation's paused run (run: threadRun's or threadState's answer). The queue holds
 * unread messages, so they are among the conversation's last: the look back is bounded.
 */
export function heldForYou(th, run) {
  const d = S.threads.get(th);
  if (!d || !run || run.key !== "limited" || !run.ids || !run.ids.length) return false;
  for (let i = d.posts.length - 1, n = 0; i >= 0 && n < 400; i--, n++) if (d.posts[i].from === "person" && run.ids.includes(d.posts[i].id)) return true;
  return false;
}
/** A thread's state for the person, most urgent first: {key (a STATUS key), label, who, post}. */
export function threadState(th, read) {
  const d = S.threads.get(th); if (!d) return { key: "none", label: "" };
  const lastYou = d.lastYou || 0;   // kept by the store as posts arrive: scanning every post of every thread on each redraw froze the page
  let ask = null; for (let i = d.posts.length - 1; i >= 0 && d.posts[i].t > lastYou; i--) { const p = d.posts[i]; if (isAgent(p.from) && p.men.includes("person")) { ask = p; break; } }
  if (ask && asksQuestion(ask)) return { key: "asks", label: "waiting for your answer", who: ask.from, post: ask };
  const run = threadRun(th); if (run) return run;
  if (ask && !(read && read(ask))) return { key: "foryou", label: "new reply for you", who: ask.from, post: ask };
  const last = S.lastByThread[th];
  return { key: "done", label: last ? "done" : "", who: last ? last.name : null };
}
// ---- where a conversation stands: its summary read against the needs list and what is happening now ----
// An agent ends its summary (`status`) with one state line: "Waiting: ...", "Done: ...", "Needs the person: ...", "Blocked: ...", "Told to stop: ...".
// The head used to show the summary's first line, which says what the conversation is about (its title says that already), and hid the state line.
const ST_RE = /^(needs the person|blocked|waiting|done|told to stop):\s*(.*)$/i, ST_TAG = /\s*\(a[0-9a-f]{4,5}\)/g;
// "Waiting on others", the focus overview's word for it (comms.js FO_GROUPS): a bare "Waiting" can read as waiting on the person, which is "Needs you".
const ST_KIND = { "needs the person": ["asks", "Needs you"], blocked: ["stuck", "Blocked"], waiting: ["quiet", "Waiting on others"], done: ["done", "Done"], "told to stop": ["ended", "Stopped"] };
/** How long a summary's "Needs the person:" line counts as an open ask (needs.js NEEDS_FOR is this). */
export const ASK_FOR = 7 * DAY;
/**
 * A summary's text as {lines, about, state, last}. The one reader of a state line: the needs list (needs.js standLine) and the head (threadStand) both
 * use it, so they can't disagree about whether a summary asks.
 * state is the LAST non-blank line when it starts with a state label and a colon ({kind, key, word, tail, line, full}), or null: a label further up is a
 * note the agent wrote on the way, not where the conversation stands. tail is the line's words without the label and without the agent's name tag
 * ("(a1b2c)"); line is the line as written; full and last are it without the tag. about is every other line.
 */
export function stRead(text) {
  const lines = String(text || "").split("\n").map(l => l.trim()).filter(Boolean), at = lines.length - 1;
  const last = at < 0 ? "" : lines[at].replace(ST_TAG, "").trim(), m = ST_RE.exec(last);
  if (!m) return { lines, about: lines, state: null, last };
  const kind = m[1].toLowerCase(), [key, word] = ST_KIND[kind];
  return { lines, about: lines.slice(0, at), state: { kind, key, word, tail: m[2].trim(), line: lines[at], full: last }, last };
}
const RUN_WORD = { working: "Working", quiet: "Working quietly", queue: "Waiting its turn", queued: "Waiting to start" };   // "Waiting to start", not "Starting": at the cap on agents at once it can wait a long while, and Agents and the guide call it that
const NOW_BAD = new Set(["stuck", "failed", "limited"]);   // what a conversation without a summary still stands on: its run broke or paused
// A conversation that is on the needs list but out of the count (needs.js waitOf): the head's word, why it isn't counted, and its label as a second fact.
const OFF_COUNT = { later: ["Later", "You put this off", "Later"], older: ["Asked earlier", "In Stuff to address", "Asked earlier"], moot: ["Not needed", "You said it isn't needed", "Not needed"] };
// An entry that asks nothing (an unread reply written for the person, needs.js "foryou") is not "asked": past its time it reads "For you".
const OFF_FORYOU = ["For you", "In Stuff to address", "Earlier"];
const sameWords = (a, b) => String(a || "").replace(/\s+/g, " ").trim() === String(b || "").replace(/\s+/g, " ").trim();
/**
 * Where a conversation stands, for its head: {key (a STATUS key), word, text, also, why, dup, more, lines, at, from, since, running, bare}, or null when it
 * has neither a summary nor a place on the needs list and nothing is happening in it now. word + text is the one line to read and also a second, quieter fact; why is set when the
 * conversation is on the needs list but out of the count ("You put this off"), for the places that show no second fact (a phone's line under the title);
 * more is every summary line that text doesn't already say in full; since counts the messages after the summary; bare means there is no summary, only
 * the list's entry or what its run is doing.
 *
 * waits is the conversation's own entry on the needs list (needs.js waitOf: {where: "now"|"older"|"later"|"moot", text, src, st}) or null. The head says
 * "Needs you" exactly when the count has the conversation, in the list's words (for a run that broke: "Failed" or "Stuck", which says what to do about
 * it), so the head, the rail's group and the badge can't disagree. Read alone, a summary got it wrong both ways (the board on 2026-11-16: of 8 summaries
 * ending "Needs the person:", 2 were answered, put off or dropped and not in the count, and 2 counted conversations had a newer summary saying Done or
 * Waiting).
 * After that, what is true now leads: a summary is a note from when it was written, and an agent that started or failed since then is newer than it.
 */
export function threadStand(th, read, waits) {
  if (!th) return null;
  const r = S.threadStatus && S.threadStatus[th], has = !!(r && String(r.text || "").trim()), live = threadState(th, read);
  // cur: what is happening in it now. threadState puts a message that asks the person above the run under it, and the list is the one place that
  // decides what waits on the person: when the list counts that ask, rule 1 says so; when it doesn't (a system conversation, one the simple look
  // keeps off the list because the person never wrote in it, an ask that was put off or said not needed), the run under it is where the
  // conversation stands, so an agent at work there still reads Working, and one that broke Stuck or Failed.
  const cur = (live.key === "asks" && threadRun(th)) || live;
  // Neither a summary nor a place on the list: it still stands somewhere while an agent is on it, stopped answering, failed or paused at a limit
  // (HA-11: a row in the conversation list says that, so the top of the chat it opens says it too). An ask or a new reply the list doesn't have
  // is not said here, for the reason above.
  if (!has && !waits && !RUN_WORD[cur.key] && !NOW_BAD.has(cur.key)) return null;
  const d = S.threads.get(th), at = has ? (r.at || 0) * 1e3 : 0, sr = stRead(has ? r.text : ""), b = sr.state;
  // a summary opens with what the conversation is about, which its title says already: that line is a second fact only when it is the whole summary
  const first = sr.about[0] || "", detail = sr.about.length > 1 ? sr.about.at(-1) : "", only = sr.about.at(-1) || "";
  const Name = n => { const l = (n && agentLabel(n)) || "the agent"; return CODE_NAME.test(l) ? l : capFirst(l); };
  // since: the messages after the summary. you: the person's own last message after it (the store's lastYou also counts the mail box and the other Hives,
  // which are not the person answering). back: an agent's last message after that.
  let since = 0, you = null, back = null;
  if (d && has) for (let i = d.posts.length - 1; i >= 0 && d.posts[i].t > at; i--) { const p = d.posts[i]; since++; if (!you) { if (p.from === "person") you = p; else if (!back && isAgent(p.from)) back = p; } }
  // used: the summary line that text is (a state line's words after its label count as that line)
  // dup: the second fact is one of the lines in more (or the summary's ask, re-worded), so the open summary doesn't say it twice
  const out = (key, word, text, also, used, why = "") => {
    const more = sr.lines.filter(l => l !== used); also = also && also !== text && also !== used ? also : "";
    // The app look says an agent by what it is on, never by its code (E10), and a summary is written by agents who name each other by code: its
    // words are said here through labelCodes, so the row, its hover, the top of the chat and the title's menu all get them the same way. The
    // comparisons above are on the words as written.
    // One map of the agents with no name of their own for the whole stand, filled in the order it is read: the line the row and the top show, the
    // second fact, then the rest of the summary. So "an agent" and "another agent" are the same two agents in each of those places.
    const un = new Map(), say = l => labelCodes(l, un), t = say(text || ""), a = say(also), m = more.map(say);
    return { key, word, text: t, also: a, why, dup: !!also && (more.includes(also) || /^Asked: /.test(also)), more: m, lines: sr.lines.map(say), at, from: has ? r.from : "", since, running: !!RUN_WORD[cur.key], bare: !has };
  };
  const asked = !!b && b.key === "asks";
  // trouble: the list has it because its run broke, and the run is still broken (the list and the head read the same threadState, so they agree)
  const counted = !!waits && waits.where === "now", trouble = waits && waits.st && (waits.st.key === "failed" || waits.st.key === "stuck") && live.key === waits.st.key ? live.key : "";
  const wtext = waits ? String(waits.text || "").trim() : "", foryou = !!(waits && waits.st && waits.st.key === "foryou");
  const off = waits && !counted ? (waits.where === "older" && foryou ? OFF_FORYOU : OFF_COUNT[waits.where] || OFF_COUNT.older) : null;
  // an entry out of the count, as the second fact: its words, or (a run that broke says its own words already) why it isn't counted. A row the list
  // built from a message ("A0001 asks: ...") says who and what by itself, so it follows "Earlier: ", not a second "asked".
  const offAlso = off ? (wtext && !trouble ? `${waits.where === "older" && waits.src === "live" ? "Earlier" : off[2]}: ${wtext}` : off[1]) : "";
  // 1. the count has it as something to answer or read: the list's words lead, whatever the summary says
  if (counted && !trouble) {
    const text = wtext || (asked && b.tail) || `${Name(live.who)} waits for your answer`, same = asked && sameWords(text, b.tail);
    // the summary's own ask, when it words it differently, is the second fact; else its state line (a newer Done or Waiting), else what it is on
    return out("asks", "Needs you", text, asked ? (same || !b.tail ? detail : `Asked: ${b.tail}`) : b ? b.line : detail, same ? b.line : undefined);
  }
  // 2. an agent stopped answering
  if (cur.key === "stuck") return out("stuck", "Stuck", String(cur.label || "").replace(/^stuck:\s*/, "") || `${Name(cur.who)} stopped answering`, offAlso || (b ? b.line : detail));
  // 3. an agent is on it now: the list doesn't count a conversation an agent is working in, so an ask in the summary is the second fact
  if (RUN_WORD[cur.key]) {
    // A run that began before the summary wrote it on the way and is still going; one that began after it came back to the conversation.
    const run = (runsByThread().get(th) || []).find(a => a.name === cur.who), began = run && run.since ? run.since * 1e3 : 0;
    const w = RUN_WORD[cur.key], again = cur.key === "queued" ? "about to carry on from the summary" : !began ? "on it now" : began > at ? "on it again since the summary" : "still on it after the summary";
    // Nothing written yet: when this run began is the one fact there is (the line under it names the agent and its step). One still waiting to
    // start has no fact to add, so its word stands alone.
    if (!has) return out(cur.key, w, cur.key === "queued" ? "" : began ? `started ${ago(began)}` : "on it now", offAlso);
    if (asked) return out(cur.key, w, you ? "carrying on after your reply" : again, offAlso || (b.tail ? `Asked: ${b.tail}` : detail));
    // "Waiting: on my test run" from an agent that is running is its own note of what it is on; Done, Blocked and Told to stop are from before this run
    if (b && b.kind === "waiting") return b.tail ? out(cur.key, w, b.tail, offAlso || detail, b.line) : out(cur.key, w, only, offAlso, only);
    if (b) return out(cur.key, w, again, offAlso || b.line);
    return out(cur.key, w, first, offAlso || detail, first);
  }
  // 4. the last run ended badly: the list has it, or it happened after the summary was written, so the summary can't know
  //    (with no summary there is nothing older than it to weigh it against)
  // wrote: the starter holds a message of the person's for the paused run (one written since it stopped, or just before, that it never got to).
  // Nobody reads it until there is room again and the run carries on, and nothing else on the page said so (HA-14), so the line says the message
  // is not lost. A summary can't know of it either, so this leads whatever the summary's age. The same after a lost connection: the message
  // waits for the retry: the starter starts nothing for a name it is about to resume, so a message does not wake it sooner either.
  const wrote = heldForYou(th, cur);
  if ((cur.key === "failed" && (trouble === "failed" || !has || (cur.t || 0) > at)) || (cur.key === "limited" && (!has || (cur.t || 0) > at || wrote))) {
    // a state line written after the failure is the newer word and follows it; else why the count doesn't have it, else the summary's state
    const simple = simpleOn(), also = b && at > (cur.t || 0) ? b.line : offAlso || (b ? b.line : detail);
    const on = "it carries on by itself" + (wrote ? " and reads your message then" : "");
    return cur.key === "limited" ? out("limited", "Paused", simple ? `taking a break; ${on}` : cur.retry ? `stopped (${retryWords(cur.retry)}) ${ago(cur.t)}; ${on}` : `stopped at a usage limit ${ago(cur.t)}; ${on}`, also)
      : out("failed", "Failed", simple ? "ran into a problem; a message makes it try again" : `stopped with an error ${ago(cur.t)}; a message wakes it to try again`, also);
  }
  // 5. on the list but out of the count: put off, said not needed, or old enough to sit in Stuff to address
  if (off) {
    if (b && !asked) return b.tail ? out(b.key, b.word, b.tail, offAlso, b.line) : out(b.key, b.word, only, offAlso, only);
    const text = wtext || (asked ? b.tail : "");
    return out("quiet", off[0], text, off[1], asked && sameWords(text, b.tail) ? b.line : undefined, off[1]);
  }
  // 6. not on the list: the summary's state line is the newest word. One that asks is no longer open: the person answered, it was dropped as obsolete,
  //    it is over a week old, or (the simple look) the list doesn't read summaries.
  if (asked) {
    if (you) {
      // what happened to the reply: an agent wrote back, or a run began after it and wrote nothing, or nothing at all
      const last = S.lastByThread[th], ran = !!(last && (last.start || 0) > you.t);
      const then = back ? `${Name(back.from)} wrote back ${ago(back.t)}` : ran ? `${Name(last.name)} picked it up and has written nothing back yet` : "not picked up yet";
      return out("quiet", "You replied", then, b.tail ? `Asked: ${b.tail}` : detail);
    }
    // "earlier" only when it is: a summary written within the week asks now, whatever the reason the count doesn't have it
    const word = now() - at < ASK_FOR ? "Asked" : "Asked earlier";
    return b.tail ? out("quiet", word, b.tail, detail, b.line) : out("quiet", word, only, "", only);
  }
  if (b) return b.tail ? out(b.key, b.word, b.tail, detail, b.line) : out(b.key, b.word, only, "", only);
  if (!has) return null;   // a guard, not a case: every stand without a summary returned above (the list's entry in 1 and 5, the run in 2 to 4)
  return out("done", "Quiet", first, detail, first);
}
export { DAY, HOUR };
