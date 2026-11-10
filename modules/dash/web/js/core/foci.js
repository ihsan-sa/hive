// Foci: the person's goals as the hive keeps them (focus.jsonl in the state folder, kept by the focus command). A focus has a name, its own thread, the threads
// tagged into it, a summary agents keep current (## Now / ## Lately bullets, @agent and #thread as pills) and a to-do list.
// Every thread is in exactly one focus or in Misc; the home draws one card per focus, the rail groups by them, #focus/<id> is the page.
// /api/foci is read once here for every page (S.foci), at most every 20 s; the server caches it as long.
import { h } from "./dom.js";
import { S, jget, emit, post } from "./store.js";
import { threadParent, isSystemThread, btwParent, isAgent, isRunning, threadTitle, agentLabel } from "./model.js";
import { now, DAY, short } from "./format.js";
import { faded, archived } from "../ui/threadmeta.js";
import { agentRef, threadRef } from "../ui/refs.js";
import { waitingNow , laterOf } from "./needs.js";
import { plainName } from "./simple.js";

export const FOCI_EVERY = 20e3, ACTIVE_FOR = 7 * DAY;
export const MISC = { id: null, name: "Other", misc: true, summary: null, checks: [], threads: [], links: [] };
const L = { at: 0, p: null, failed: false };
export const fociFailed = () => L.failed;
/** Read /api/foci into S.foci (at most every 20 s unless forced); resolves when it is there. Emits on change. */
export function loadFoci(force) {
  if (L.p) return L.p;
  if (!force && S.foci !== undefined && now() - L.at < (L.failed ? 5e3 : FOCI_EVERY)) return Promise.resolve(S.foci);   // a failed read (a dash restart) retries soon
  L.at = now();
  L.p = jget("/api/foci").then(v => (v && v.error ? null : v && Array.isArray(v.foci) ? v : { foci: [], tags: {}, lint: [] }), () => null).then(v => {
    L.p = null; L.failed = !v;
    if (!v) { if (S.foci === undefined) { S.foci = { foci: [], tags: {}, lint: [], failed: true }; emit({ foci: true }); } return S.foci; }
    if (L.want) { if (now() - L.want.at > 60e3 || sameOrder(v.foci, L.want.ids)) L.want = null; else v.foci = arrange(v.foci, L.want.ids); }   // a read that left before the move landed keeps the moved order
    const sig = x => JSON.stringify(x && [x.tags, (x.moved || []).length && x.moved[0].at, x.foci.map(f => [f.id, f.name, f.summary && f.summary.at, f.checks.map(c => c.id + (c.done ? 1 : 0)).join()])]);
    if (sig(v) !== sig(S.foci)) { S.foci = v; emit({ foci: true }); }
    return v;
  });
  return L.p;
}
// The person's own order (the person, 1108031067f6): one order for the rail and the home, kept by the server (the focus order command) so every page and
// the command line's focus list agree. A move shows at once; the read after it confirms it.
const arrange = (fs, ids) => { const at = new Map(ids.map((id, i) => [id, i])), n = ids.length; return fs.map((f, i) => [f, at.has(f.id) ? at.get(f.id) : n + i]).sort((a, b) => a[1] - b[1]).map(x => x[0]); };
const sameOrder = (fs, ids) => { const got = fs.map(f => f.id).filter(id => ids.includes(id)); return got.join() === ids.filter(id => fs.some(f => f.id === id)).join(); };
/** Put focus `id` just before `before` (an id), or last with before null; or move it by `step` places (-1 up, +1 down). Resolves to the error, or null. */
export function moveFocus(id, { before, step } = {}) {
  const ids = foci().map(f => f.id), from = ids.indexOf(id); if (from < 0) return Promise.resolve(null);
  ids.splice(from, 1);
  let to = step != null ? Math.max(0, Math.min(ids.length, from + step)) : before == null ? ids.length : ids.indexOf(before);
  if (to < 0) to = ids.length;
  ids.splice(to, 0, id);
  if (ids.join() === foci().map(f => f.id).join()) return Promise.resolve(null);
  L.want = { ids, at: now() };
  S.foci = { ...S.foci, foci: arrange(S.foci.foci, ids) }; emit({ foci: true });
  return post("/api/focus", { order: ids }).then(() => { L.at = 0; return loadFoci(true).then(() => null); },
    e => { L.want = null; L.at = 0; loadFoci(true); return (e && e.message) || "the server didn't answer"; });
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
export const needsOf = id => waitingNow().filter(w => (w.focus ? w.focus === id : focusOf(w.th) === id));
/** Agents in a focus's threads: running now, and the ones that worked there in the last day. */
export function agentsOf(id) {
  const ths = new Set(threadsOf(id).map(r => r.th)), nowOn = new Set(), day = new Set(), since = now() - DAY;
  for (const a of (S.live && S.live.agents) || []) if (a.name && a.thread && ths.has(a.thread)) nowOn.add(a.name);
  for (const s of S.sessions) if (s.thread && ths.has(s.thread) && s.name) { if (isRunning(s.name) && !s.end) nowOn.add(s.name); else if ((s.end || s.start) > since) day.add(s.name); }
  // Who posted in its threads, and who wrote its summary, in the last day: a run started elsewhere still counts (R21: Now from an agent 22 min ago
  // beside "No agent has been on this in the last day").
  for (const th of ths) { const d = S.threads.get(th); if (!d) continue; for (let i = d.posts.length - 1; i >= 0 && d.posts[i].t > since; i--) { const n = d.posts[i].from; if (n && isAgent(n)) day.add(n); } }
  const f = id && focusById(id), sm = f && f.summary; if (sm && sm.from && isAgent(sm.from) && sm.at * 1e3 > since) day.add(sm.from);
  for (const n of nowOn) day.delete(n);
  return { now: [...nowOn], day: [...day] };
}
/** The decisions the agents put to the person in the focus's summary (`## Needs you`): what the card shows, and nothing else.
 *  The person does not read the raw list (too many, 11071606f4d4); agents decide by default and keep this part for what is really theirs. */
// Less the ones the person put off (Later or Not needed, core/needs.js laterOf).
export const askedOf = id => { const f = focusById(id), s = f && f.summary; return ((s && s.needs) || []).filter((text, i) => text && !laterOf({ src: "summary", focus: id, text, t: needAt(s, i) })); };
/** When the summary's i-th ask was first raised (focus.py needs_at), in ms. */
export const needAt = (s, i) => (((s && s.needs_at && s.needs_at[i]) || (s && s.at) || 0) * 1e3) || null;
/** One word for where a focus stands, and its tone: needs you, moving, waiting, quiet. */
/** A focus's share of the hive's work: focus.py's runs and agent posts in its threads, with the dash's cost laid on them, over the last day and week.
 *  { day: {runs, posts, cost}, week: {...}, total: {day, week}, windows } or null when the API has none yet. id null is Misc. */
export function activityOf(id) {
  const a = S.foci && S.foci.activity; if (!a || !a.total) return null;
  const b = id ? a.foci && a.foci[id] : a.misc; if (!b) return null;
  return { day: b.day, week: b.week, total: a.total, windows: a.windows };
}
/** Work is runs and posts together, cost is money: each as this focus's fraction of the hive's over the window, 0..1 (0 when the hive did nothing). */
export function shareOf(act, win, what) {
  if (!act || !act[win] || !act.total[win]) return 0;
  const v = what === "cost" ? (act[win].cost || 0) : (act[win].runs || 0) + (act[win].posts || 0);
  const t = what === "cost" ? (act.total[win].cost || 0) : (act.total[win].runs || 0) + (act.total[win].posts || 0);
  return t > 0 ? Math.min(1, v / t) : 0;
}
/** A fraction in words, for simple view: it reads as a share, not a number. */
export function fractionWords(x) {
  return x >= 0.9 ? "nearly all" : x >= 0.6 ? "most" : x >= 0.4 ? "about half" : x >= 0.28 ? "a third" : x >= 0.18 ? "a quarter" : x >= 0.12 ? "a sixth" : x >= 0.07 ? "a tenth" : x > 0 ? "a little" : "none";
}
export const pct = x => `${Math.round(x * 100)}%`;
export const usd = c => (c >= 100 ? `$${Math.round(c)}` : `$${c.toFixed(2)}`);   // one money format under $100 (R6 review: "$6.40 … $30.1" read as two)

export function stateOf(id) {
  // Both at once (the person, 110812313986: a focus can need them and still be working): the needs part first, then "moving".
  if (needsOf(id).length) return { key: "needs", word: agentsOf(id).now.length ? "needs you, moving" : "needs you", tone: "info", moving: !!agentsOf(id).now.length };
  if (agentsOf(id).now.length) return { key: "moving", word: "moving", tone: "good" };
  if (activeThreads(id).length) return { key: "waiting", word: "no agent running", tone: null };   // not "waiting": readers take that for waiting on them (SU-3)
  return { key: "quiet", word: "quiet", tone: "ended" };
}
/** A summary bullet with its @agent and #thread words as pills. */
// No ids on the home or the focus pages (the person, 1107151649a6; R16): an agent code, backticked or bare, is the chat's agent pill; a 12-hex post id
// and an ask's own id are dropped; a leading step word ("R14:") goes. Agents' text is kept, only what the person cannot read is taken out.
const CODE = /^[a-z](?=[0-9a-f]{0,3}\d)[0-9a-f]{4}$/;   // a letter and 4 hex with a digit in it: "a9179", not "added"
export const isCode = w => isAgent(w) || CODE.test(w);
/** Ask ids out and nothing else, for text in the person's own words. A bracket of nothing but ask ids goes whole; a real agent's code stays. */
export const noAskIds = text => String(text || "")
  .replace(/\s*\(\s*k\d{4}(?:\s*(?:,|;|\/|&|and)\s*k\d{4})*\s*\)/g, m => (m.match(/k\d{4}/g).some(isAgent) ? m : ""))
  .replace(/\bk\d{4}'s\s+/g, m => (isAgent(m.slice(0, 5)) ? m : "the ")).replace(/\bk\d{4}\b/g, w => (isAgent(w) ? w : "an ask"));
/** The text with post ids, ask ids and a leading step word taken out, and the punctuation they leave tidied. */
export function scrub(text) {
  return noAskIds(String(text || "")
    .replace(/^\s*(?:rung\s+)?R\d+[a-z]?(?:\s*(?:,|and|\+|&|-|–|\/)\s*R\d+[a-z]?)*\s*[:.–-]\s+(\S)/i, (m, c) => c.toUpperCase())
    .replace(/\s*\((?:post\s+)?[0-9a-f]{12}\)/g, "")
    .replace(/(?:\b(?:in|from|see|at)\s+)?\bpost\s+[0-9a-f]{12}\b/gi, "")
    .replace(/(^|[\s(:,])(?:(?:on|in|from|at|see|by)\s+)?[0-9a-f]{12}\b/g, "$1"))
    .replace(/\(\s*[,;]?\s*\)/g, "").replace(/\s+([,.;:)])/g, "$1").replace(/([,;:])\s*([.,;:])/g, "$2").replace(/[:,;]\s*$/, "").replace(/\s{2,}/g, " ").trim();
}
const WORD = /(^|[\s(\[,;:"'/])(?:([@#])([A-Za-z0-9_][A-Za-z0-9_.:-]*[A-Za-z0-9_])|`([a-z][0-9a-f]{4})`|([a-z][0-9a-f]{4})(?![\w-]))/g;
/** Summary, status and needs text as nodes: @agent, a backticked or bare agent code and #thread as the chat's pills, ids scrubbed (above). */
export function pills(text) {
  text = scrub(text);
  const out = []; let last = 0, m; WORD.lastIndex = 0;
  while ((m = WORD.exec(text))) {
    const at = m.index + m[1].length, word = m[3] || m[4] || m[5];
    const el = m[2] === "#" ? (S.threads.has(word) ? threadRef(word) : null)
      : m[2] === "@" ? (isAgent(word) || /^[a-f0-9]{5}$/.test(word) || word === "person" ? agentRef(word, { dot: false }) : null)
      : m[4] || isCode(word) ? agentRef(word, { dot: false }) : null;
    if (!el && m[2] === "@" && /^(new|free|claude|codex|opus|sonnet|haiku|fable|astra)$/.test(word)) { out.push(text.slice(last, at), word); last = at + m[0].length - m[1].length; continue; }   // "@new builder" read as a word, not a mention
    if (!el) continue;
    out.push(text.slice(last, at), el); last = at + m[0].length - m[1].length;
  }
  out.push(text.slice(last));
  return out.filter(x => x !== "");
}
/** An agent code as a pill that is not a link, for text that already sits inside one (a rail row, a Yours entry); anything else as it is. */
export const codePill = n => (n && isCode(n) ? h("span", { class: "ref pill", "data-kind": "agent", title: n }, agentLabel(n)) : n || "an agent");
/** A one-line preview said for the person (R21): ids scrubbed, a leading "Rung:"/"Next:" word and any step word gone, agent codes as codePill. */
export const sayPlain = (text, max = 70) => short(scrub(String(text || "").replace(/(^|[\s(\[,;:"'])#([\w.-]*\w)/g, (m, a, w) => (S.threads.has(w) ? a + threadTitle(w) : m)).replace(/`/g, "").replace(/\s+/g, " ").replace(/^\s*(?:rung(?:\s+done)?|next|step)\s*:\s*/i, "")).replace(/\b(?:(?:the|rung)\s+)?R\d+[a-z]?\b/g, "the step").replace(/\b(?:[Ll]eftovers\s+)?rung(?:\s+\d+[a-z]?(?:\s+part\s+\w+)?)?(?=\s*[:(" ]|$)/g, "step").replace(/\bstep\s*:\s*/g, ""), max);
/** A thread's title for a list row: a 12-hex post id in it is dropped. */
export const railTitle = t => { const x = scrub(threadTitle(t)); return x && x !== "Untitled" ? x : threadTitle(t); };
export function railSay(text, max = 70) {
  const t = sayPlain(text, max);
  const out = []; let last = 0, m; const re = /(^|[^\w`])`?([a-z][0-9a-f]{4})`?(?![\w-])/g;
  while ((m = re.exec(t))) { if (!isCode(m[2])) continue; const at = m.index + m[1].length; out.push(t.slice(last, at), codePill(m[2])); last = re.lastIndex; }
  out.push(t.slice(last)); return out.filter(x => x !== "");
}
/** The same text for the simple view: ids scrubbed and every agent named as the hive, as plain words. */
export const plainPills = text => scrub(text).replace(WORD, (m, a, at, w1, w2, w3) => (at === "#" ? m : at === "@" || w2 || isCode(w3) ? a + plainName(w1 || w2 || w3) : m));
/** A thread's one-line description: its status bar's first line, else nothing (the title says what it is). */
export function threadLine(th) { const r = S.threadStatus && S.threadStatus[th]; const l = r && scrub(r.text.split("\n")[0].replace(/\s*\(a[0-9a-f]{4,5}\)/g, "")); return l && l !== threadTitle(th) ? l : ""; }
/** Where a needs row comes from, said for the person: its focus's name, else the thread's status line 1, else its title. */
export function needSource(w) { const f = w.focus ? focusById(w.focus) : focusById(focusOf(w.th)); return f ? focusName(f) : (w.th && (threadLine(w.th) || threadTitle(w.th))) || ""; }
/** A focus's name, said short where the long name explains more than it names. */
/** The focus's name from its record or its id (the thread header's crumb and the focus page's links pass ids; handed an id this said Misc for every filed thread, the person, 11081224cf6a); "Misc" for none. */
export const focusName = f => { if (typeof f === "string") f = focusById(f); return (f && f.name) || "Other"; };
export const focusTitle = f => h("span", { class: "fc-name" }, focusName(f));
