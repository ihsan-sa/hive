// One needs-you count. Everything that says how much waits on the person (the Chat badge, the home's head, the Needs you page's
// lead and list, Work's header, Flow's "waits on you") reads the one list here, so the numbers agree wherever they are read.
// The list joins three sources, one entry per thread, newest first:
//   live: a thread whose last agent post asks the person something they haven't answered, or that stopped in trouble (threadState);
//   file: the 30-minute summary's waiting rows (~/needs-you.json, /api/needs-you), when nothing the person said since is newer;
//   bar: a status bar whose last line is "Needs the person:", within a week, on the same terms;
//   summary: the Needs you bullets of a focus summary (what its home card shows), one entry each, on the focus's own thread.
// A thread an agent is working in now is not waiting on them (the home lists it under Working now); nor is one archived by hand,
// faded, a system thread, or one the summary dropped as obsolete until the thread said something newer.
// /api/needs-you is read once here, every five minutes, for every page that wants it (S.needsYou).
import { S, jget, emit } from "./store.js";
import { threadState, isSystemThread, btwParent, displayText, agentLabel } from "./model.js";
import { seen } from "./inbox.js";
import { simpleOn } from "./simple.js";
import { firstSentence, mdPlain, now, DAY, MIN } from "./format.js";
import { faded, archived, meta as tmeta } from "../ui/threadmeta.js";
import { APP_NAV } from "./nav.js";

export const NEEDS_FOR = 7 * DAY, NEEDS_EVERY = 5 * MIN;
const LISTS = ["waiting", "stale", "held", "nobody", "done", "states", "built", "stalled", "decided"];
/** The summary file as the pages read it: every list present, and a file with no waiting list is "absent" (not written yet). */
// A file missing a list must not crash a page: fill the lists. No file, an empty one, or one that says so is "absent".
export const normNeeds = v => { if (!v || typeof v !== "object" || v.absent || !Object.keys(v).length) return { absent: true }; for (const k of LISTS) if (!Array.isArray(v[k])) v[k] = []; return v; };

const L = { at: 0, p: null, failed: false };
/** True when the last read of /api/needs-you failed (the pages show what they read last, and say so). */
export const needsFailed = () => L.failed;
/** Read /api/needs-you into S.needsYou (at most every five minutes unless forced); resolves when it is there. Emits on change. */
export function loadNeedsYou(force) {
  if (L.p) return L.p;
  if (!force && S.needsYou !== undefined && now() - L.at < NEEDS_EVERY) return Promise.resolve(S.needsYou);
  L.at = now();
  L.p = jget("/api/needs-you").then(v => normNeeds(v), () => ({ absent: true, failed: true })).then(v => {
    L.p = null; L.failed = !!v.failed;
    if (v.failed && S.needsYou && !S.needsYou.absent) return S.needsYou;   // keep the last good read
    const sig = x => JSON.stringify(x && [x.at, x.waiting, x.stale, x.states, x.brief]);
    if (sig(v) !== sig(S.needsYou)) { S.needsYou = v; emit({ needs: true }); }
    return v;
  });
  return L.p;
}

/** A status bar's last line, split into its state word and the words after it, without the agent's name tag. */
export function standLine(text) {
  const l = String(text || "").split("\n").filter(Boolean), last = (l[l.length - 1] || "").replace(/\s*\(a[0-9a-f]{4,5}\)/g, "").trim();
  const m = last.match(/^(Done|Blocked|Needs the person|Waiting|Told to stop):\s*/i);
  return { word: m ? m[1].toLowerCase() : "", line: m ? last.slice(m[0].length) : last, full: last };
}
/** Whether the person has said something in the thread since t. */
export function answeredSince(th, t) { const d = S.threads.get(th); if (!d) return false; for (let i = d.posts.length - 1; i >= 0; i--) { const p = d.posts[i]; if (p.t <= t) return false; if (p.from === "person") return true; } return false; }
const theirs = d => !simpleOn() || d.posts.some(p => p.from === "person");
// A thread that went quiet is archived by age, but what it waits on still counts; one archived by hand doesn't.
export const shownHere = th => { const d = S.threads.get(th); return !(btwParent(th) || faded(th) || (tmeta(th).archived && archived(th)) || (d && isSystemThread(d))); };

// "limited" is not in WAIT: the hive restarts an agent stopped at a usage limit by itself; nothing is asked of the person (the person, 110812234faa).
const WAIT = new Set(["asks", "foryou", "failed", "stuck"]), BUSY = new Set(["working", "quiet", "queue", "queued"]);
/** The live state of every thread the person could be waiting on, by thread: {st} for one that waits, {busy: true} for one an agent is in. */
function liveStates() {
  const m = new Map();
  for (const d of S.threads.values()) {
    if (isSystemThread(d) || !theirs(d)) continue;
    const st = threadState(d.name, seen);
    if (WAIT.has(st.key)) m.set(d.name, { d, st });
    else if (BUSY.has(st.key)) m.set(d.name, { d, st, busy: true });
  }
  return m;
}
/** The words for a live entry: who asks or says what, or how the agent stopped. */
function liveText({ st }) {
  const who = (w => w && w[0].toUpperCase() + w.slice(1))(agentLabel(st.who || ""));   // the app look says what the agent is on, not its code
  if (st.key === "asks") return `${who} asks: ${firstSentence(mdPlain(displayText(st.post)))}`;
  if (st.key === "foryou") return `${who}: ${firstSentence(mdPlain(displayText(st.post)))}`;
  return `${who} ${st.label}`;
}
/** Everything that waits on the person, one entry per thread, newest first: {th, text, t, src: "live"|"file"|"bar", st?, post?}. */
// One page draw asks for the list many times (the badge in both bars, Work once per project): it is worked out once per task and
// handed out as copies. Nothing it reads changes inside one task, and a click or poll is a new task, so it is never stale.
// A read can land before that timer has fired, so the list also goes when what it reads has changed: the badge once kept 2 for a poll's length where the count was 3 (R74).
const readsNow = () => [S.ver, S.foci, S.needsYou], sameReads = (a, b) => !!a && a.every((x, i) => x === b[i]);   // the foci and needs reads don't move S.ver
let memo = null, memoAt = null;
const all = () => { const at = readsNow(); if (!memo || !sameReads(memoAt, at)) { memo = waitingOnNow(); memoAt = at; setTimeout(() => { memo = null; }, 0); } return memo; };
export function waitingOn() { return all().filter(w => !laterOf(w)); }
/** Later and Not needed (the person, 1108133400fb): the person puts a needs-you item off, or says it isn't needed any more.
 *  It leaves the top level (the badge, the home, the rail) and a Later one lists on its focus page until they bring it back.
 *  The key is the item's: its post for a question, its words for a summary bullet (the bullets are renumbered when a summary is rewritten).
 *  A newer item on the same key (a new question in the thread) is not put off: it shows again. */
export const laterKey = w => (w.src === "summary" ? `sum:${w.focus}:${String(w.text || "").replace(/\s+/g, " ").slice(0, 120)}` : w.key || (w.post ? "p:" + w.post : w.src + ":" + w.th));
const laterMap = () => { const m = new Map(); for (const e of (S.foci && S.foci.later) || []) if (e && e.key && !m.has(e.key)) m.set(e.key, e); return m; };
/** The later entry that holds this item off, or null. */
export function laterOf(w) {
  const e = laterMap().get(laterKey(w));
  const et = e && e.t && e.t < 1e11 ? e.t * 1000 : e && e.t;   // the later action's t takes seconds; the page sends ms
  return e && (!w.t || !et || et >= w.t || (e.text && e.text === w.text)) ? e : null;
}
/** The items put off for later (not the Not needed ones), newest first, each with its entry: {...w, later: e}. */
export const waitingPutOff = (what = "later") => all().map(w => ({ w, e: laterOf(w) })).filter(x => x.e && x.e.what === what).map(x => ({ ...x.w, later: x.e }));
export const waitingLater = () => waitingPutOff("later");
/** Not needed is an archive, not a delete (the person, 11081355ecce): the full Waits on you page lists them, to bring one back. */
export const waitingMoot = () => waitingPutOff("moot");
function waitingOnNow() {
  const live = liveStates(), out = [], has = new Set();
  const take = w => { const k = w.key || w.th; if (has.has(k)) return; has.add(k); out.push(w); };
  const st = S.threadStatus || {}, v = S.needsYou && !S.needsYou.absent ? S.needsYou : null;
  const drop = (v && v.brief && v.brief.dropped) || {};
  const open = th => { const l = live.get(th); return !(l && l.busy) && shownHere(th) && !(drop[th] && drop[th] >= ((st[th] && st[th].at) || 0)); };
  for (const [th, l] of live) if (!l.busy) take({ th, text: liveText(l), t: l.st.post ? l.st.post.t : l.d.last, src: "live", st: l.st, post: l.st.post ? l.st.post.id : null });
  // The summary's bullets are the agents' own words for what they put to the person (the person, 11071606f4d4: the card shows these), so they count as long as the summary keeps them.
  for (const f of (S.foci && S.foci.foci) || []) for (const [i, b] of ((f.summary && f.summary.needs) || []).entries()) if (b) take({ th: f.thread || f.id, text: b, t: ((((f.summary.needs_at || [])[i] || f.summary.at) || 0) * 1e3) || now(), src: "summary", focus: f.id, key: `sum:${f.id}:${i}` });
  if (!simpleOn()) {
    const rest = [];
    // A later Done or Told to stop on the thread doesn't settle a row: a For you line waits until they answer it (answeredSince) or an agent drops it (brief.dropped). A thread with a live busy agent is out above.
    for (const w of (v && v.waiting) || []) { const t = w && w.at ? Date.parse(w.at) : 0; if (w && !w.stale && w.thread && open(w.thread)) rest.push({ th: w.thread, text: w.action || "", t, src: "file", post: w.post || null }); }
    for (const th of Object.keys(st)) { const s = standLine(st[th].text), t = st[th].at * 1e3; if (s.word === "needs the person" && now() - t < NEEDS_FOR && open(th)) rest.push({ th, text: s.line, t, src: "bar" }); }
    rest.sort((a, b) => b.t - a.t);
    for (const w of rest) if (!answeredSince(w.th, w.t)) take(w);   // once they've answered since, it's not waiting on them
  }
  foldSameAct(out, v && v.brief && v.brief.needs);
  return out.filter(w => !w.foldedIn).sort((a, b) => b.t - a.t);
}
/** One act of the person's that settles several threads is one thing to do (the summary's `also` names the others): those threads' rows fold into the row that carries the act, which keeps its own key, words and time, so a folded thread's Later or Not needed neither revives nor hides it.
 *  A thread whose carrying row isn't on the list stays a row of its own. A focus bullet is never folded. */
function foldSameAct(rows, briefNeeds) {
  const by = new Map(rows.filter(w => w.src !== "summary").map(w => [w.th, w]));
  for (const r of briefNeeds || []) for (const it of (r && r.items) || []) {
    const head = by.get(it.thread);
    if (!head || head.foldedIn) continue;
    for (const th of it.also || []) {
      const w = by.get(th);
      if (!w || w === head || w.foldedIn || w.folded) continue;
      w.foldedIn = head.th; (head.folded = head.folded || []).push(w); (head.also = head.also || []).push(th);
    }
  }
}
/** Older things leave the needs-you list and fold into one "Stuff to address" group, to go through with an agent when the person has time
 * (the person, 11081245f002: needs-me piles up, and often it went obsolete or is no longer the focus). How old is older depends on how
 * fast that work moves (the person, 110813526b10): an ask on work that moved on a lot since (MOVED posts in its conversation or its
 * focus's) is likely obsolete after FAST_FOR; on quiet work it stays relevant, just not first, and leaves the count after RECENT_FOR. */
export const RECENT_FOR = 2 * DAY, FAST_FOR = DAY, MOVED = 15;
let pace = null, paceAt = null;
const paceMemo = () => { const at = readsNow(); if (!pace || !sameReads(paceAt, at)) { pace = new Map(); paceAt = at; setTimeout(() => { pace = null; }, 0); } return pace; };
/** Posts since t in w's conversation and the other conversations of its focus. */
function movedSince(w) {
  const m = paceMemo(), k = (w.key || w.th) + "@" + w.t; if (m.has(k)) return m.get(k);
  const tags = (S.foci && S.foci.tags) || {}, fid = w.focus || tags[w.th];
  const ths = new Set([w.th]); if (fid) for (const [th, f] of Object.entries(tags)) if (f === fid) ths.add(th);
  let n = 0;
  for (const th of ths) { const d = S.threads.get(th); if (!d) continue; for (let i = d.posts.length - 1; i >= 0 && d.posts[i].t > w.t; i--) n++; }
  m.set(k, n); return n;
}
/** Why w is older: "moved" (the work went on without it, likely obsolete), "quiet" (old, on work that's been quiet), or null. */
export function olderWhy(w) {
  const why = olderOne(w);
  return why && (w.folded || []).every(olderOne) ? why : null;   // a row is as recent as the newest thread folded into it
}
function olderOne(w) {
  if (!w.t) return null;
  const age = now() - w.t;
  if (age < FAST_FOR) return null;
  if (movedSince(w) >= MOVED) return "moved";
  return age >= RECENT_FOR ? "quiet" : null;
}
export const isOlder = w => !!olderWhy(w);
/** What waits on the person now: the list without the older ones. */
export const waitingNow = () => waitingOn().filter(w => !isOlder(w));
/** The "Stuff to address" group: waiting, but past its time (olderWhy), newest first. */
export const waitingOlder = () => waitingOn().filter(isOlder);
/** The one number: how many things wait on the person now (the older ones are in Stuff to address, not counted). */
export const needsN = () => waitingNow().length;
/** Whether the count can be trusted yet: without the focuses (Later, Not needed, each focus's asks) and the summary it is a different number, 11 where the answer was 5.
 *  So is it before the older board and log are in (S.history): 2 where the answer was 5. The Chat badge is on every page, so it asks for both (at most every 20 s and 5 min); a page outside Chat doesn't load them itself.
 *  And before the thread marks and status bars: a faded thread counts until the marks say it's faded, 6 where the answer was 5 (R62). */
export const needsKnown = () => S.waitsIn.meta && S.waitsIn.status && S.history && (simpleOn() || (S.foci !== undefined && S.needsYou !== undefined));
/** {recent, older}: the count and the size of Stuff to address. */
export const needsSplit = () => { const w = waitingOn(), recent = w.filter(x => !isOlder(x)).length; return { recent, older: w.length - recent }; };
/** Whether this thread is on the list: the rail's Needs you group and the one count read the same answer. */
/** Does this conversation itself wait on the person (a live ask, a Needs line, a state word)? A focus summary's asks belong to the focus and show on its card, not here. */
export const waitsOn = th => waitingNow().some(w => (w.th === th || (w.also || []).includes(th)) && w.src !== "summary");
// Where the full list is read: the Needs you page, when this build has one (an installed Hive hasn't; the simple look lists the live ones on the home).
// The literal "#needs", not href("needs"): the export removes every line with that call.
const hasNeeds = () => Object.values(APP_NAV.groups || {}).some(g => g.includes("needs")) || !!(APP_NAV.belongs && APP_NAV.belongs.needs) || (APP_NAV.more || []).includes("needs");
export const needsPage = () => (!simpleOn() && hasNeeds() ? "#needs" : null);   // null in simple mode and in builds without the page
/** Where the one line and the badge send the person: Yours, cut to what waits (views/yours.js), in every build and mode. */
export const needsHref = () => "#yours/waits";
