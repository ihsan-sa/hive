// Foci: the person's goals as the hive keeps them (focus.jsonl in the state folder, kept by the focus command). A focus has a name, its own thread, the threads
// tagged into it, a summary agents keep current (## Now / ## Lately bullets, @agent and #thread as pills) and a to-do list.
// Every thread is in exactly one focus or in Misc; the home draws one card per focus, the rail groups by them, #focus/<id> is the page.
// /api/foci is read once here for every page (S.foci), at most every 20 s; the server caches it as long.
import { h } from "./dom.js";
import { S, jget, emit } from "./store.js";
import { threadParent, isSystemThread, btwParent, isAgent, isRunning, threadTitle } from "./model.js";
import { now, DAY } from "./format.js";
import { faded, archived } from "../ui/threadmeta.js";
import { agentRef, threadRef } from "../ui/refs.js";
import { waitingOn } from "./needs.js";

export const FOCI_EVERY = 20e3, ACTIVE_FOR = 7 * DAY;
export const MISC = { id: null, name: "Misc", misc: true, summary: null, checks: [], threads: [], links: [] };
const L = { at: 0, p: null, failed: false };
export const fociFailed = () => L.failed;
/** Read /api/foci into S.foci (at most every 20 s unless forced); resolves when it is there. Emits on change. */
export function loadFoci(force) {
  if (L.p) return L.p;
  if (!force && S.foci !== undefined && now() - L.at < FOCI_EVERY) return Promise.resolve(S.foci);
  L.at = now();
  L.p = jget("/api/foci").then(v => (v && Array.isArray(v.foci) ? v : { foci: [], tags: {}, lint: [] }), () => null).then(v => {
    L.p = null; L.failed = !v;
    if (!v) { if (S.foci === undefined) { S.foci = { foci: [], tags: {}, lint: [], failed: true }; emit({ foci: true }); } return S.foci; }
    const sig = x => JSON.stringify(x && [x.tags, x.foci.map(f => [f.id, f.name, f.summary && f.summary.at, f.checks.map(c => c.id + (c.done ? 1 : 0)).join()])]);
    if (sig(v) !== sig(S.foci)) { S.foci = v; emit({ foci: true }); }
    return v;
  });
  return L.p;
}
/** The foci in the person's order (none until read). */
export const foci = () => (S.foci && S.foci.foci) || [];
export const focusById = id => (id ? foci().find(f => f.id === id) || null : null);
/** The focus a thread is in: its tag, else the focus whose own thread it is, else its parent's, up the chain; null is Misc. */
export function focusOf(th) {
  const fs = foci(), tags = (S.foci && S.foci.tags) || {};
  let t = th;
  for (let i = 0; t && i < 8; i++) {
    if (tags[t]) return tags[t];
    const f = fs.find(x => x.thread === t || x.id === t); if (f) return f.id;
    t = btwParent(t) || threadParent(t);
  }
  return null;
}
/** Whether the person would list this thread at all: not a helper's side thread, not the hive's own, not marked away. */
export const listed = th => { const d = S.threads.get(th); return !!d && !btwParent(th) && !isSystemThread(d) && !faded(th) && !archived(th); };
const lastAt = th => { const d = S.threads.get(th), p = d && d.posts.at(-1); return p ? p.t : 0; };
/** The threads of a focus (id, or null for Misc), newest activity first: {th, t, active} where active is a post in the last week. */
export function threadsOf(id) {
  const out = [];
  for (const d of S.threads.values()) { if (!listed(d.name) || focusOf(d.name) !== id) continue; const t = lastAt(d.name); out.push({ th: d.name, t, active: now() - t < ACTIVE_FOR }); }
  return out.sort((a, b) => b.t - a.t);
}
export const activeThreads = id => threadsOf(id).filter(r => r.active);
/** The cards' order: the foci as listed, then Misc, only when something is going on there. */
export function fociOrder() { const fs = foci().slice(); if (activeThreads(null).length) fs.push(MISC); return fs; }
/** What waits on the person inside one focus (core/needs.js rows). */
export const needsOf = id => waitingOn().filter(w => focusOf(w.th) === id);
/** Agents in a focus's threads: running now, and the ones that worked there in the last day. */
export function agentsOf(id) {
  const ths = new Set(threadsOf(id).map(r => r.th)), nowOn = new Set(), day = new Set(), since = now() - DAY;
  for (const a of (S.live && S.live.agents) || []) if (a.name && a.thread && ths.has(a.thread)) nowOn.add(a.name);
  for (const s of S.sessions) if (s.thread && ths.has(s.thread) && s.name) { if (isRunning(s.name) && !s.end) nowOn.add(s.name); else if ((s.end || s.start) > since) day.add(s.name); }
  for (const n of nowOn) day.delete(n);
  return { now: [...nowOn], day: [...day] };
}
/** The decisions the agents put to the person in the focus's summary (`## Needs you`): what the card shows, and nothing else.
 *  The person does not read the raw list (too many, 11071606f4d4); agents decide by default and keep this part for what is really theirs. */
export const askedOf = id => { const f = focusById(id); return (f && f.summary && f.summary.needs) || []; };
/** One word for where a focus stands, and its tone: needs you, moving, waiting, quiet. */
export function stateOf(id) {
  if (askedOf(id).length) return { key: "needs", word: "needs you", tone: "info" };
  if (agentsOf(id).now.length) return { key: "moving", word: "moving", tone: "good" };
  if (activeThreads(id).length) return { key: "waiting", word: "waiting", tone: null };
  return { key: "quiet", word: "quiet", tone: "ended" };
}
/** A summary bullet with its @agent and #thread words as pills. */
export function pills(text) {
  const out = []; let last = 0; const re = /(^|[\s(])([@#])([A-Za-z0-9_][A-Za-z0-9_.:-]*[A-Za-z0-9_])/g; let m;
  while ((m = re.exec(text))) {
    const at = m.index + m[1].length, word = m[3];
    const el = m[2] === "@" ? (isAgent(word) || /^[a-f0-9]{5}$/.test(word) || word === "person" ? agentRef(word, { dot: false }) : null) : S.threads.has(word) ? threadRef(word) : null;
    if (!el) continue;
    out.push(text.slice(last, at), el); last = at + m[0].length - m[1].length;
  }
  out.push(text.slice(last));
  return out.filter(x => x !== "");
}
/** A thread's one-line description: its status bar's first line, else nothing (the title says what it is). */
export function threadLine(th) { const r = S.threadStatus && S.threadStatus[th]; const l = r && r.text.split("\n")[0].replace(/\s*\(a[0-9a-f]{4,5}\)/g, "").trim(); return l && l !== threadTitle(th) ? l : ""; }
/** A focus's name, said short where the long name explains more than it names. */
export const focusName = f => (f && f.name) || "Misc";
export const focusTitle = f => h("span", { class: "fc-name" }, focusName(f));
