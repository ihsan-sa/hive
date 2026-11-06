// The store: one object of state, filled by polling. Board and log arrive as appended items only
// (from an offset), so each poll does work in proportion to what is new.
import { now } from "./format.js";
import { signPost } from "./sign.js";

export const OUTSIDE = new Set(["person", "hives"]);
export const SYS = new Set(["starter", "dash", "anon", "all", "400", "mention", "name"]);

export const S = {
  app: { route: "open", can: null, app: false, setup: null },
  live: null, usage: null, projects: null, agentCosts: null, threadNames: {}, threadMeta: {}, shown: [], threadStatus: {},
  posts: [], byId: {}, threads: new Map(), off: 0,
  sessions: [], lastSess: {}, open: {}, byOut: {}, woke: {}, logOff: 0,
  agents: new Set(), killed: {}, limited: [], limitedSess: new Set(), limitedNow: new Set(),
  ledger: [],                 // {t, kind, parts:[string | {a:name} | {th:thread}], lc}
  running: new Map(), pending: {},
  stCache: new Map(),
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
export async function jget(u) {
  const r = await fetch(u, { cache: "no-store", headers: activeNow() ? { "X-Hive-Active": "1" } : {} });
  if (r.status === 401) location.replace("/");
  if (!r.ok) throw Error(r.status);
  return r.json();
}
/** POST with the header the server requires; throws an Error with a plain reason. */
export async function post(path, body) {
  if (path === "/api/post") body = await signPost(body);
  const r = await fetch(path, { method: "POST", headers: { "Content-Type": "application/json", "X-Hive-Dash": "1" }, body: JSON.stringify(body) });
  if (r.redirected) throw Error("sign in again and reload the page");
  const j = await r.json().catch(() => ({}));
  if (!r.ok) { const e = Error(j.error || "the server said " + r.status); e.status = r.status; throw e; }
  return j;
}

// ---- ingest ----
const ledgerAdd = (r, words) => { r.lc = words.join(" ").toLowerCase(); S.ledger.push(r); };
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
      S.sessions.push(s); S.lastSess[e.name] = s; S.open[e.name + e.session] = s; S.agents.add(e.name);
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
      ledgerAdd({ t: p.t, kind: "thread", parts: ["New thread ", { th }, " from ", { a: p.from }] }, ["new thread", th, p.from]); }
    const tos = p.men.filter(m => m !== "new");
    ledgerAdd({ t: p.t + 0.5, kind: SYS.has(p.from) ? "hive" : "post", post: p, parts: [{ a: p.from }, tos.length ? " → " : " in ", ...(tos.length ? tos.flatMap((m, i) => (i ? [", ", { a: m }] : [{ a: m }])) : [{ th }]), tos.length ? " in " : "", tos.length ? { th } : ""] },
      [p.from, th, ...tos, String(p.text).slice(0, 600)]);
    d.posts.push(p); d.last = p.t; if (!SYS.has(p.from)) d.who.add(p.from);
  }
}

// ---- polling: live + appended log and board every 1.5 s, usage every 3 s, only while the tab is visible ----
let polling = false;
export async function poll() {
  if (polling) return; polling = true;
  try {
    const [l, lg, b] = await Promise.all([jget("/api/live"), jget("/api/log?from=" + S.logOff), jget("/api/board?from=" + S.off)]);
    S.stCache.clear(); S.live = l;
    try { const a = await jget("/api/approvals"); S.approvals = Array.isArray(a) ? a : []; } catch (e) { S.approvals = S.approvals || []; }
    try { const d = await jget("/api/feedback-drafts"); if (d.length) import("../ui/feedback.js").then(m => m.offerDraft(d[0])); } catch (e) {}   // an agent's drafted feedback: open the window prefilled
    S.running = new Map(l.agents.filter(a => a.alive).map(a => [a.name, a]));
    S.pending = Object.fromEntries(l.pending.map(p => [p.name, p]));
    const runSig = [...S.running.keys()].sort().join();
    const changed = b.items.length || lg.items.length || runSig !== S.runSig; S.runSig = runSig;
    S.logOff = lg.offset; S.off = b.offset;
    ingestLog(lg.items); ingestBoard(b.items);
    if (changed) S.ver++;
    S.fresh = now(); S.failed = false;
    emit({ live: true, posts: b.items, log: lg.items });
  } catch (e) { S.failed = true; emit({ failed: true }); }
  finally { polling = false; }
}
export async function pollUsage() {
  try { S.usage = await jget("/api/usage"); S.uver++; emit({ usage: true }); } catch (e) {}
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
  try { const v = await jget("/api/app"); const was = JSON.stringify(S.app); Object.assign(S.app, v); if (JSON.stringify(S.app) !== was) emit({ app: true }); } catch (e) {}
}
/** Readable names for number threads, from the Slack mirror's indexer. Display only: the board and every link keep the id. */
export async function loadThreadNames() {
  try { const v = await jget("/api/thread-names"); if (JSON.stringify(v) !== JSON.stringify(S.threadNames)) { S.threadNames = v; S.ver++; emit({ names: true }); } } catch (e) {}
}
/** The person's pins, archive and titles for threads (/api/thread-meta), kept beside the board. */
export async function loadThreadMeta() {
  try { const v = await jget("/api/thread-meta"); if (JSON.stringify(v) !== JSON.stringify(S.threadMeta)) { S.threadMeta = v; S.ver++; emit({ names: true }); } } catch (e) {}
}
/** Threads an agent brought up for the person (/api/shown): `hive show THREAD --why ...`. The strip drops each at its end time. */
export async function loadShown() {
  try { const v = await jget("/api/shown"); if (JSON.stringify(v) !== JSON.stringify(S.shown)) { S.shown = Array.isArray(v) ? v : []; S.ver++; emit({ names: true }); } } catch (e) {}
}
/** The two-line status bar of each thread (/api/thread-status): `hive status THREAD`. */
export async function loadThreadStatus() {
  try { const v = await jget("/api/thread-status"); if (JSON.stringify(v) !== JSON.stringify(S.threadStatus)) { S.threadStatus = v && typeof v === "object" ? v : {}; S.ver++; emit({ names: true }); } } catch (e) {}
}
export async function loadProjects() {
  if (S.projects) return; S.projects = "loading"; emit({ projects: true });
  try { const p = await jget("/api/projects"); p.sort((a, b) => (b.t || 0) - (a.t || 0)); S.projects = p; } catch (e) { S.projects = null; }
  emit({ projects: true });
}
export function start() {
  const every = (ms, f) => setInterval(() => { if (!document.hidden) f(); }, ms);
  every(1500, poll); every(3000, pollUsage); every(15000, loadApp); every(60000, loadThreadNames); every(15000, loadThreadMeta); every(5000, loadShown); every(20000, loadThreadStatus);
  document.addEventListener("visibilitychange", () => { if (!document.hidden) { poll(); pollUsage(); } });
  return Promise.all([loadApp(), pollUsage(), poll(), loadThreadNames(), loadThreadMeta(), loadShown(), loadThreadStatus()]);
}
