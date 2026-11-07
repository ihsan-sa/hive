// One needs-you count. Everything that says how much waits on the person (the Chat badge, the home's head, the Needs you page's
// lead and list, Work's header, Flow's "waits on you") reads the one list here, so the numbers agree wherever they are read.
// The list joins three sources, one entry per thread, newest first:
//   live: a thread whose last agent post asks the person something they haven't answered, or that stopped in trouble (threadState);
//   file: the 30-minute summary's waiting rows (~/needs-you.json, /api/needs-you), when nothing the person said since is newer;
//   bar: a status bar whose last line is "Needs the person:", within a week, on the same terms.
// A thread an agent is working in now is not waiting on them (the home lists it under Working now); nor is one archived by hand,
// faded, a system thread, or one the summary dropped as obsolete until the thread said something newer.
// /api/needs-you is read once here, every five minutes, for every page that wants it (S.needsYou).
import { S, jget, emit } from "./store.js";
import { threadState, isSystemThread, btwParent, displayText } from "./model.js";
import { seen } from "./inbox.js";
import { simpleOn, plainName } from "./simple.js";
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

const WAIT = new Set(["asks", "foryou", "limited", "failed", "stuck"]), BUSY = new Set(["working", "quiet", "queue", "queued"]);
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
  const who = plainName(st.who || "");
  if (st.key === "asks") return `${who} asks: ${firstSentence(mdPlain(displayText(st.post)))}`;
  if (st.key === "foryou") return `${who}: ${firstSentence(mdPlain(displayText(st.post)))}`;
  return `${who} ${st.label}`;
}
/** Everything that waits on the person, one entry per thread, newest first: {th, text, t, src: "live"|"file"|"bar", st?, post?}. */
// One page draw asks for the list many times (the badge in both bars, Work once per project): it is worked out once per task and
// handed out as copies. Nothing it reads changes inside one task, and a click or poll is a new task, so it is never stale.
let memo = null;
export function waitingOn() {
  if (!memo) { memo = waitingOnNow(); setTimeout(() => { memo = null; }, 0); }
  return memo.slice();
}
function waitingOnNow() {
  const live = liveStates(), out = [], has = new Set();
  const take = w => { if (has.has(w.th)) return; has.add(w.th); out.push(w); };
  const st = S.threadStatus || {}, v = S.needsYou && !S.needsYou.absent ? S.needsYou : null;
  const drop = (v && v.brief && v.brief.dropped) || {};
  const open = th => { const l = live.get(th); return !(l && l.busy) && shownHere(th) && !(drop[th] && drop[th] >= ((st[th] && st[th].at) || 0)); };
  for (const [th, l] of live) if (!l.busy) take({ th, text: liveText(l), t: l.st.post ? l.st.post.t : l.d.last, src: "live", st: l.st, post: l.st.post ? l.st.post.id : null });
  if (!simpleOn()) {
    const rest = [];
    // A later Done or Told to stop on the thread doesn't settle a row: a For you line waits until they answer it (answeredSince) or an agent drops it (brief.dropped). A thread with a live busy agent is out above.
    for (const w of (v && v.waiting) || []) { const t = w && w.at ? Date.parse(w.at) : 0; if (w && !w.stale && w.thread && open(w.thread)) rest.push({ th: w.thread, text: w.action || "", t, src: "file", post: w.post || null }); }
    for (const th of Object.keys(st)) { const s = standLine(st[th].text), t = st[th].at * 1e3; if (s.word === "needs the person" && now() - t < NEEDS_FOR && open(th)) rest.push({ th, text: s.line, t, src: "bar" }); }
    rest.sort((a, b) => b.t - a.t);
    for (const w of rest) if (!answeredSince(w.th, w.t)) take(w);   // once they've answered since, it's not waiting on them
  }
  return out.sort((a, b) => b.t - a.t);
}
/** The one number: how many threads wait on the person. */
export const needsN = () => waitingOn().length;
// Where the full list is read: the Needs you page, when this build has one (an installed Hive hasn't; the simple look lists the live ones on the home).
// The literal "#needs", not href("needs"): the export removes every line with that call.
export const needsHref = () => (!simpleOn() && APP_NAV.groups?.work?.includes("needs") ? "#needs" : null);   // null in simple mode and in builds without the page
