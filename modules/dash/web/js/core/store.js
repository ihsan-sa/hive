// The store: one object of state, filled by polling. Board and log arrive as appended items only
// (from an offset), so each poll does work in proportion to what is new.
import { now } from "./format.js";
import { signPost } from "./sign.js";

export const OUTSIDE = new Set(["person", "hives"]);
export const SYS = new Set(["starter", "dash", "anon", "all", "400", "mention", "name"]);

// Which of the two apps this browser last saw (/api/app "app"): the first paint draws that shell in the moment before the server answers. A browser with nothing remembered draws the app's, with no pages in its bar (pending), since it can't know yet which pages this Hive shows.
const lastApp = () => { try { return localStorage.getItem("appRoute"); } catch (e) { return null; } };
export const S = {
  app: { route: "open", can: null, app: lastApp() !== "0", pending: lastApp() === null, setup: null },
  live: null, usage: null, projects: null, agentCosts: null, threadNames: {}, threadMeta: {}, shown: [], threadStatus: {}, waitsIn: {}, agentStatus: {},
  posts: [], byId: {}, threads: new Map(), off: 0, boardStart: null,
  sessions: [], lastByThread: {}, lastSess: {}, open: {}, byOut: {}, woke: {}, logOff: 0, logStart: null, history: false,
  agents: new Set(), killed: {}, limited: [], limitedSess: new Set(), limitedNow: new Set(),
  ledger: [],                 // {t, kind, parts:[string | {a:name} | {th:thread}], lc}
  running: new Map(), pending: {},
  stCache: new Map(),
  foci: undefined,            // /api/foci, read by core/foci.js
  ver: 0, uver: 0, fresh: 0, failed: false,
};
/** Saved view settings. A TV page keeps its own, apart from the normal tabs. Values are JSON. */
const prefKey = k => (document.body.classList.contains("dt-page") ? "tv:" : "") + k;
export const onTV = () => document.body.classList.contains("dt-page");
export function getPref(k, dflt) { try { const v = localStorage.getItem(prefKey(k)); return v == null ? dflt : JSON.parse(v); } catch (e) { return dflt; } }
export const setPref = (k, v) => localStorage.setItem(prefKey(k), JSON.stringify(v));
export const can = c => !S.app.can || S.app.can.includes(c);
// What the server says it has of the features designed ahead of it (/api/app "features"): stop, helper, knowledge, slack-door.
// A feature it doesn't list stays out of the app, so nobody meets a button that can't work. The preview shim lists them all.
export const built = f => (S.app.features || []).includes(f);
/** Setup is finished but no account is signed in, so nothing can start yet. */
export const needsAccount = () => { const s = S.app.setup; return !!s && !(S.app.settings && S.app.settings.setup_done === false) && !(s.signed_in || s.api_key || s.other); };
export const setupDone = () => { const s = S.app.setup; if (S.app.settings && S.app.settings.setup_done === false) return false; return !s || ((s.signed_in || s.api_key || s.other) && s.has_posts); };

// ---- change notification ----
const subs = new Set();
export const subscribe = fn => (subs.add(fn), () => subs.delete(fn));
export const emit = what => { for (const f of subs) f(what); };

// ---- API ----
// The person's own input in the last minute: sent with reads so a full sign-in from outside stays full while they use the app (dash.py FULL_IDLE_S).
let lastInput = Date.now();
for (const ev of ["pointerdown", "keydown", "wheel", "touchstart"]) addEventListener(ev, () => { lastInput = Date.now(); }, { capture: true, passive: true });
export const activeNow = () => Date.now() - lastInput < 60000 && document.visibilityState === "visible";
/** fetch, with the browser's "Failed to fetch" said in plain words: it reaches the person through the pages' error lines. */
/** An HTTP status in plain words, for the pages' error lines ("Couldn't read the settings: <this>"). */
export const said = n => n === 403 ? "this sign-in isn't allowed to do that" : n === 404 ? "the Hive doesn't have that" : n === 408 || n === 504 ? "the Hive took too long to answer"
  : n === 413 ? "it's too big" : n === 429 ? "the Hive is busy, so try again in a moment" : n === 502 || n === 503 ? "the Hive isn't answering" : n >= 500 ? "the Hive hit a problem" : "the Hive turned it down";
const ask = (u, o) => fetch(u, o).catch(() => { throw Error("can't reach the server"); });
export async function jget(u) {
  const r = await ask(u, { cache: "no-store", headers: activeNow() ? { "X-Hive-Active": "1" } : {} });
  if (r.status === 401) location.replace("/");
  if (!r.ok) { const e = Error(said(r.status)); e.status = r.status; throw e; }
  return r.json();
}
/** POST with the header the server requires; throws an Error with a plain reason. */
export async function post(path, body) {
  if (path === "/api/post") body = await signPost(body);
  const r = await ask(path, { method: "POST", headers: { "Content-Type": "application/json", "X-Hive-Dash": "1" }, body: JSON.stringify(body) });
  if (r.redirected) throw Error("sign in again and reload the page");
  const j = await r.json().catch(() => ({}));
  if (!r.ok) { const e = Error(j.error || said(r.status)); e.status = r.status; throw e; }
  return j;
}

// ---- ingest ----
const ledgerAdd = (r, words) => { r.lc = words.join(" ").toLowerCase(); S.ledger.push(r); };
const boardRow = (r, words) => { r.b = 1; ledgerAdd(r, words); };   // a row made from a board post: dropped and remade when older posts arrive
const LOGTEXT = { delivered: "had queued posts handed to it mid-run", kill_dropped: "had its queued wakes dropped after a kill", model_set: "was moved to another model from its panel", paced: "start was paced", provider_held: "waits for its provider", fallback_held: "waits: no fallback has room", unknown_mention: "was mentioned, but nobody has that name", free_spawn_ignored: "tried to start agents from a free model; ignored", reaped: "was ended by the starter after going quiet", moved: "moved to another login", login_rejected: "hit a rejected login", helper: "started as a helper: the agent asked was busy" };
function logRow(e, t) {
  const k = e.event, n = e.name, a = { a: n };
  switch (k) {
    case "start": case "end": return null;
    case "killed": return { t, kind: "trouble", parts: [a, " was killed", e.by ? " by " + e.by : ""] };
    case "limited": return { t, kind: "trouble", parts: [a, " stopped at a usage limit"] };
    case "login_rejected": return { t, kind: "trouble", parts: [a, " hit a rejected login"] };
    case "paused": return { t, kind: "hive", parts: ["The hive stopped starting agents: usage"] };
    case "resumed": return { t, kind: "hive", parts: ["The hive started agents again"] };
    case "logins_paused": return { t, kind: "hive", parts: ["Logins paused: " + ((e.logins || []).length || "none")] };
    case "keeper": return { t, kind: "hive", parts: ["The keeper started a round (" + (e.why || "periodic") + ")"] };
    case "stop_dropped": return { t, kind: "hive", parts: ["Stop: queued starts dropped in ", { th: e.thread }] };
    case "fallback": return { t, kind: "agent", parts: [a, " went to a fallback model"] };
  }
  const words = LOGTEXT[k];
  return { t, kind: k === "reaped" ? "trouble" : "hive", parts: n ? [a, " " + (words || k)] : [words || k] };
}
function ingestLog(items) {
  for (const e of items) {
    const t = Date.parse(e.at);
    const r = logRow(e, t); if (r) ledgerAdd(r, [e.event, e.name || "", e.thread || "", e.by || ""]);
    if (e.event === "start") {
      S.limitedNow.delete(e.session); // resumed: limitedSess keeps the history, limitedNow means still stopped
      const s = { out: e.out ? String(e.out).split("/").pop().replace(/\.jsonl$/, "") : null, name: e.name, session: e.session, start: t, end: null, exit: null,
        thread: e.thread, from: e.from, post: e.post, resumed: !!e.resumed, heirOf: e.heir_of || null, model: e.model || null, effort: e.effort || null, harness: e.harness || null, provider: e.provider || null, login: e.login || null };
      S.sessions.push(s); if (e.thread) S.lastByThread[e.thread] = s; S.lastSess[e.name] = s; S.open[e.name + e.session] = s; S.agents.add(e.name);
      const by = e.from, re = !!e.resumed, known = by && by !== "anon";
      ledgerAdd({ t, kind: "agent", parts: [known ? { a: by } : "", known ? (re ? " woke " : " started ") : (re ? "Woke " : "Started "), { a: e.name }, e.thread ? " in " : "", e.thread ? { th: e.thread } : ""] }, ["started woke", e.name, by || "", e.thread || ""]);
      if (s.out) S.byOut[s.out] = s;
      if (e.post) (S.woke[e.post] ||= []).push(s);
    } else if (e.event === "end") {
      const s = S.open[e.name + e.session];
      // -9 right after the starter said it was handing over or tidying up is the starter's own doing, not a crash: it counts as 143, a run the hive ended.
      const handed = e.exit === -9 && s && s.byStarter, exit = handed ? 143 : e.exit;
      if (s) { s.end = t; s.exit = exit; delete S.open[e.name + e.session]; }
      const th = s && s.thread, bad = exit && !handed;
      ledgerAdd({ t, kind: bad ? "trouble" : "agent", parts: [{ a: e.name }, bad ? ` failed (exit ${exit})` : " ended", th ? " in " : "", th ? { th } : ""] }, [bad ? "failed" : "ended", e.name, th || ""]);
    } else if (e.event === "woken" || e.event === "reaped") { const s = S.open[e.name + e.session]; if (s) s.byStarter = true; }
    else if (e.event === "killed") S.killed[e.name] = t;
    else if (e.event === "heir") { const s = S.lastSess[e.name]; if (s) { s.replacedBy = e.heir; S.limitedNow.delete(s.session); } } // a new agent took over: the old name isn't "stopped" any more
    else if (e.event === "limited") {
      // Mark the run that just ended, not the session: a resumed agent keeps its session id, so a session-wide mark
      // would call every later normal end of that agent a usage limit.
      const s = S.lastSess[e.name]; if (s && s.session === e.session && s.end) s.limited = true;
      S.limited.push(t); S.limitedSess.add(e.session); S.limitedNow.add(e.session);
    }
  }
}
function ingestBoard(items) {
  for (const p of items) {
    p.t = Date.parse(p.at); p.men = (p.to || []).map(x => String(x).replace(/^@/, ""));
    const th = p.thread || "(none)"; p.th = th;
    S.posts.push(p); S.byId[p.id] = p;
    let d = S.threads.get(th);
    if (!d) { d = { name: th, posts: [], who: new Set(), first: p.t, last: p.t }; S.threads.set(th, d);
      boardRow({ t: p.t, kind: "thread", parts: ["New thread ", { th }, " from ", { a: p.from }] }, ["new thread", th, p.from]); }
    const tos = p.men.filter(m => m !== "new");
    boardRow({ t: p.t + 0.5, kind: SYS.has(p.from) ? "hive" : "post", post: p, parts: [{ a: p.from }, tos.length ? " → " : " in ", ...(tos.length ? tos.flatMap((m, i) => (i ? [", ", { a: m }] : [{ a: m }])) : [{ th }]), tos.length ? " in " : "", tos.length ? { th } : ""] },
      [p.from, th, ...tos, String(p.text).slice(0, 600)]);
    d.posts.push(p); d.last = p.t; if (OUTSIDE.has(p.from)) d.lastYou = p.t; if (!SYS.has(p.from)) d.who.add(p.from);
  }
}

// The first read takes only the last FIRST_BYTES of the board and LOG_FIRST_BYTES of the log (each about a day), so the pages draw in about a second;
// the older parts are fetched right after and the state is rebuilt in order with them. S.boardStart and S.logStart are where what's held begins (0: all of it); S.history turns true once it is all in, and what counts across old threads (the waits) waits for it.
const FIRST_BYTES = 2_000_000, LOG_FIRST_BYTES = 600_000;
let logHeld = [];   // the log items read so far, kept until the older part has arrived: ingestLog keeps no raw events to rebuild from
let filling = false;   // the older part is on its way: a poll that lands meanwhile has nothing older of its own, and must not call the history complete (R73)
function resetLog() {
  Object.assign(S, { sessions: [], lastByThread: {}, lastSess: {}, open: {}, byOut: {}, woke: {}, agents: new Set(), killed: {}, limited: [], limitedSess: new Set(), limitedNow: new Set() });
  S.ledger = S.ledger.filter(r => r.b);
}
async function backfill(boardUpto, logUpto) {
  for (let i = 0; i < 5; i++) {
    try {
      const [b, lg] = await Promise.all([boardUpto ? jget("/api/board?from=0&upto=" + boardUpto) : null, logUpto ? jget("/api/log?from=0&upto=" + logUpto) : null]);
      if (lg) { const recent = logHeld; logHeld = null; resetLog(); ingestLog(lg.items); ingestLog(recent); S.logStart = 0; }
      if (b) {
        const recent = S.posts;
        S.posts = []; S.byId = {}; S.threads = new Map(); S.ledger = S.ledger.filter(r => !r.b);
        ingestBoard(b.items); ingestBoard(recent);
        S.boardStart = 0;
      }
      S.history = true; S.ver++; emit({ live: true, older: true });
      return;
    } catch (e) { await new Promise(r => setTimeout(r, 3000 * (i + 1))); }
  }
  logHeld = null; S.history = true; S.ver++; emit({ live: true, older: true });   // gave up: what's held is all there will be, and the pages stop waiting for the rest
}

// ---- polling: live + appended log and board every 1.5 s, usage every 3 s, only while the tab is visible ----
let polling = false;
export async function poll() {
  if (polling) return; polling = true;
  try {
    // All read together, then applied in one go: S.live set with a wait before the board is ingested let a page draw "No conversations yet" while they were still on their way.
    const [l, lg, b, a, d] = await Promise.all([jget("/api/live"), jget("/api/log?from=" + S.logOff + (S.logOff || S.logStart != null ? "" : "&last=" + LOG_FIRST_BYTES)), jget("/api/board?from=" + S.off + (S.off || S.boardStart != null ? "" : "&last=" + FIRST_BYTES)), jget("/api/approvals").catch(() => null), jget("/api/feedback-drafts").catch(() => null)]);
    S.stCache.clear(); S.live = l;
    if (typeof l.starter === "boolean" && S.app.setup && S.app.setup.starter !== l.starter) { S.app.setup.starter = l.starter; emit({ app: true }); }   // the Hive stopped or is back: from the live read, not /api/app's 15 s one
    S.approvals = a === null ? S.approvals || [] : Array.isArray(a) ? a : [];
    if (d && d.length) import("../ui/feedback.js").then(m => m.offerDraft(d[0]));   // an agent's drafted feedback: open the window prefilled
    S.running = new Map(l.agents.filter(a => a.alive).map(a => [a.name, a]));
    S.pending = Object.fromEntries(l.pending.map(p => [p.name, p]));
    const runSig = [...S.running.keys()].sort().join();
    const changed = b.items.length || lg.items.length || runSig !== S.runSig; S.runSig = runSig;
    S.logOff = lg.offset; S.off = b.offset;
    const older = S.boardStart == null && b.start > 0, olderLog = S.logStart == null && lg.start > 0; if (S.boardStart == null) S.boardStart = b.start || 0; if (S.logStart == null) S.logStart = lg.start || 0;
    if (S.logStart && logHeld) logHeld.push(...lg.items);
    ingestLog(lg.items); ingestBoard(b.items);
    filling ||= older || olderLog;
    S.history ||= !filling;
    if (older || olderLog) setTimeout(() => backfill(older ? b.start : 0, olderLog ? lg.start : 0), 50);
    if (changed) S.ver++;
    S.fresh = now(); S.failed = false;
    emit({ live: true, posts: b.items, log: lg.items });
  } catch (e) { S.failed = true; emit({ failed: true }); }
  finally { polling = false; }
}
export async function pollUsage() {
  // After the first read only the session files that changed come (?since=, delta: true; ~2 MB a read otherwise). The files object
  // is kept as it is when none changed, so what is cached on it stays good, and nothing redraws when nothing moved.
  try {
    const was = S.usage, q = was && was.epoch && was.ver != null ? `?since=${was.ver}&epoch=${encodeURIComponent(was.epoch)}` : "";
    const v = await jget("/api/usage" + q), fresh = S.usageFailed;
    if (v.delta && was) {
      const n = Object.keys(v.files).length;
      v.files = n ? { ...was.files, ...v.files } : was.files; delete v.delta;
      const rest = x => JSON.stringify([x.logins, x.scanning, x.stop_pct, x.providers]);
      S.usage = v; S.usageFailed = false;
      if (n || fresh || rest(v) !== rest(was)) { S.uver++; emit({ usage: true }); }
    } else { S.usage = v; S.usageFailed = false; S.uver++; emit({ usage: true }); }
  }
  catch (e) { if (!S.usageFailed) { S.usageFailed = true; emit({ usage: true }); } }
}
/** Cost and tokens per agent from the transcripts. Asked for only while a page that shows them is open; the server refreshes at most every 5 minutes. */
export async function loadAgentCosts() {
  try {
    const was = S.agentCosts, v = await jget("/api/agent-costs");
    S.agentCosts = v;
    if (!was || v.at !== was.at || v.refreshing !== was.refreshing) { S.uver++; emit({ usage: true }); }
    return v;
  } catch (e) { return S.agentCosts; }
}
export async function loadApp() {
  try { const v = await jget("/api/app"); const was = JSON.stringify(S.app); Object.assign(S.app, v); delete S.app.pending; try { localStorage.setItem("appRoute", S.app.app ? "1" : "0"); } catch (e) {} if (JSON.stringify(S.app) !== was) emit({ app: true }); } catch (e) {}
}
/** Readable names for number threads, from the Slack mirror's indexer. Display only: the board and every link keep the id. */
export async function loadThreadNames() {
  try { const v = await jget("/api/thread-names"); if (JSON.stringify(v) !== JSON.stringify(S.threadNames)) { S.threadNames = v; S.ver++; emit({ names: true }); } } catch (e) {}
}
// Which of the reads the count of waits depends on have answered once, right or wrong (needsKnown): a read that fails mustn't keep the number away for good.
const answered = k => { if (S.waitsIn[k]) return; S.waitsIn[k] = true; S.ver++; emit({ names: true }); };
/** The person's pins, archive and titles for threads (/api/thread-meta), kept beside the board. */
export async function loadThreadMeta() {
  try { const v = await jget("/api/thread-meta"); if (JSON.stringify(v) !== JSON.stringify(S.threadMeta)) { S.threadMeta = v; S.ver++; emit({ names: true }); } } catch (e) {}
  answered("meta");
}
/** Threads an agent brought up for the person (/api/shown): `hive show THREAD --why ...`. The strip drops each at its end time. */
export async function loadShown() {
  try { const v = await jget("/api/shown"); if (JSON.stringify(v) !== JSON.stringify(S.shown)) { S.shown = Array.isArray(v) ? v : []; S.ver++; emit({ names: true }); } } catch (e) {}
}
/** The two-line status bar of each thread (/api/thread-status): `hive status THREAD`. */
export async function loadThreadStatus() {
  try { const v = await jget("/api/thread-status"); if (JSON.stringify(v) !== JSON.stringify(S.threadStatus)) { S.threadStatus = v && typeof v === "object" ? v : {}; S.ver++; emit({ names: true }); } } catch (e) {}
  answered("status");
}
/** What each agent says it is on (/api/agent-status): `hive me "..."`. */
export async function loadAgentStatus() {
  try { const v = await jget("/api/agent-status"); if (JSON.stringify(v) !== JSON.stringify(S.agentStatus)) { S.agentStatus = v && typeof v === "object" ? v : {}; S.ver++; emit({ names: true }); } } catch (e) {}
}
export async function loadProjects() {
  if (S.projects) return; S.projects = "loading"; emit({ projects: true });
  try { const p = await jget("/api/projects"); p.sort((a, b) => (b.t || 0) - (a.t || 0)); S.projects = p; } catch (e) { S.projects = null; }
  emit({ projects: true });
}
export function start() {
  const every = (ms, f) => setInterval(() => { if (!document.hidden) f(); }, ms);
  every(1500, poll); every(3000, pollUsage); every(15000, loadApp); every(60000, loadThreadNames); every(15000, loadThreadMeta); every(5000, loadShown); every(20000, loadThreadStatus); every(20000, loadAgentStatus);
  document.addEventListener("visibilitychange", () => { if (!document.hidden) { poll(); pollUsage(); } });
  return Promise.all([loadApp(), pollUsage(), poll(), loadThreadNames(), loadThreadMeta(), loadShown(), loadThreadStatus(), loadAgentStatus()]);
}
