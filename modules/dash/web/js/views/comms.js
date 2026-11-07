// Comms (Chat, in the app): conversations on the left, one conversation on the right, the composer under it.
// #comms              in the app: home — start something, and what needs you. On the developer route: everything.
// #comms/~new         the same home, open on a phone (where the list and a conversation take turns)
// #comms/~all         every message · #comms/<thread> · #comms/@name by or to one name · #comms/@a+b between two names
//
// The thread list and the message list are data-own: morph leaves them alone and sync() below writes them, once the
// render has been mounted. A poll with nothing new touches nothing in them; a new post is appended, not redrawn, before
// the messages still on their way (.cm-out), which sync() keeps apart.
import { h, morph } from "../core/dom.js";
import { S, SYS, can, OUTSIDE, built, post as apiPost, poll, jget } from "../core/store.js";
import { simpleOn, plainName, hiveName } from "../core/simple.js";
import { SIDE_THREAD, agentState, agentStatus, btwParent, threadParent, threadChildren, isAgent, isRunning, tierOf, threadTitle, threadState, threadPeople, isSystemThread, SHOW, postKind, threadKinds, closingPosts, displayText, postSource, inUse, fresh7, asksQuestion, fileInfo } from "../core/model.js";
import { ago, dayName, stamp, clock, dur, short, mdPlain, now, plural, firstSentence, DAY, MIN, HOUR, money } from "../core/format.js";
import { href, threadHref, go } from "../core/router.js";
import { tier, status } from "../core/status.js";
import { askTier, askStop, stopButton } from "../actions.js";
import { seen, unreadIn, markRead, onInbox, IB } from "../core/inbox.js";
import { MN, onMail } from "../core/mailnew.js";
import { button, empty, icon, input, list, seg, statusDot } from "../ui/kit.js";
import { agentRef, avatars, copyButton, threadRef } from "../ui/refs.js";
import { message, messageHead, messageReceipt, localMessage, markWords, pollDelivered, marksSig, delivery, onJump } from "../ui/message.js";
import { asidePanel, asideOpen, closeAside } from "../ui/aside.js";
import { downloadMarkdown, printScript } from "../ui/export.js";
import { shareDialog } from "../ui/share.js";
import { pinned, important, faded, meta as tmeta, archived, setMeta, renameDialog, openCard, markSeen } from "../ui/threadmeta.js";
import { callFor, hangUp } from "../ui/phone.js";
import { brought } from "../ui/brought.js";
import { firstJobs, closeLine } from "../ui/firstjobs.js";
import { awayCard } from "../ui/away.js";
import { hiveStopped } from "../ui/shell.js";
import { composer, cancelReply, replyTo, say, outFor, outSig, actions as outActions, dropZone, recipient, draft, hasDraft, focusComposer, quoteInto } from "../ui/composer.js";

const STEP = 500, FIRST = 250, NEAR = 60, GROUP = 10 * MIN, HELPER_AFTER = 3 * MIN;
const NONE = [];
const app = () => S.app.app;

// What the person chose. C.next: the message search a thread opened from the search box starts with.
// The conversation list's width: drag its edge, or use the arrow keys on it. Kept on this device.
const SIDE_MIN = 220, SIDE_MAX = 640;
let sideW = (() => { try { return Math.min(SIDE_MAX, Math.max(SIDE_MIN, +localStorage.getItem("cm.side") || 272)); } catch (e) { return 272; } })();
const setSide = (box, w) => { sideW = Math.round(Math.min(SIDE_MAX, Math.max(SIDE_MIN, w))); box.style.setProperty("--cm-w", sideW + "px"); try { localStorage.setItem("cm.side", sideW); } catch (e) {} };
const grip = () => h("div", { class: "cm-grip", role: "separator", "aria-orientation": "vertical", "aria-label": "Resize the conversation list", tabindex: "0", "data-key": "grip", title: "Drag to resize",
  onpointerdown: e => { const box = e.target.closest(".cm"), x0 = e.clientX, w0 = sideW; e.target.setPointerCapture(e.pointerId); e.target.dataset.on = "1";
    e.target.onpointermove = m => setSide(box, w0 + m.clientX - x0);
    e.target.onpointerup = e.target.onpointercancel = () => { delete e.target.dataset.on; e.target.onpointermove = e.target.onpointerup = e.target.onpointercancel = null; }; },
  ondblclick: e => setSide(e.target.closest(".cm"), 272),
  onkeydown: e => { const d = e.key === "ArrowLeft" ? -24 : e.key === "ArrowRight" ? 24 : 0; if (d) { e.preventDefault(); setSide(e.target.closest(".cm"), sideW + d); } } });
// The side list's three views (ideas is the default) (the person, 11061410615e and 11061116958d): "topics" puts conversations under the topics of the
// Where things stand summary with helpers folded under the one they came from; "recent" is every conversation in one flat list,
// newest activity first, helpers included. hideFaded leaves out the ones marked Likely not needed. Both kept in this browser.
const VIEW_KEY = "hive.comms.view", HIDE_KEY = "hive.comms.hidefaded";
const store = (k, v) => { try { localStorage.setItem(k, v); } catch (e) { /* this page only */ } };
const C = { view: (() => { try { const v = localStorage.getItem(VIEW_KEY); return v === "recent" || v === "topics" ? v : "ideas"; } catch (e) { return "ideas"; } })(),
  hideFaded: (() => { try { return localStorage.getItem(HIDE_KEY) === "1"; } catch (e) { return false; } })(),
  err: "", imp: false, arch: false, conv: undefined, q: "", show: null, limit: FIRST, tq: "", scope: "recent", next: null, menu: null, find: false, strip: false };
let errTimer = 0;
/** A change to a conversation (pin, star, archive) is shown at once; when the server refuses it the list goes back, and this says why for a few seconds. */
const tryMeta = (th, patch, ctx) => setMeta(th, patch).catch(e => { C.err = "Couldn't save that: " + (e && e.message || "the server didn't answer") + "."; clearTimeout(errTimer); errTimer = setTimeout(() => { C.err = ""; ctx.update(); }, 8000); ctx.update(); });
// What the message list holds. matched grows as posts arrive; pend is what sync() still has to append.
const L = { key: null, src: null, seen: 0, matched: [], pend: [], evs: new Set(), rebuild: true, keep: false, shown: 0, day: "", prev: null, names: new Set(), opts: null,
  box: null, out: null, jump: null, unseen: 0, outSig: "", ro: null, stuck: true, heads: "", near: new Set(), io: null, tl: null, tlSig: "", tlConv: null, saved: null, ctx: null, jumpTo: null, marked: "" };
let qTimer = 0;
onInbox(() => L.ctx && L.ctx.update());
onMail(() => L.ctx && L.ctx.update());
// Sending clears a search, so what was just sent shows (the person, 11040214d468).
if (typeof window !== "undefined") window.addEventListener("cp:sent", () => { if (C.q.trim() || C.find) { C.q = ""; C.find = false; L.ctx && L.ctx.update(); } });

/** What the address says to show. */
function conversation(arg) {
  if (!arg) return app() ? { arg: null, home: true } : { arg: null, all: true };
  if (arg === "~new") return { arg, home: true, open: true };
  if (arg === "~all") return { arg, all: true };
  if (arg[0] !== "@") return { arg, thread: arg };
  const names = arg.slice(1).split("+");
  return names.length === 2 ? { arg, pair: names } : { arg, agent: names[0] };
}

const words = s => s.toLowerCase().split(/\s+/).filter(Boolean);
/** What a search looks through in a post: its text, author and attachment names. */
const hay = p => (p.lc ??= [p.text, p.from, ...(p.files || []).map(f => f.name)].join(" ").toLowerCase());

// What to show: any of For you, Updates and Agent comms, as a mask of SHOW bits. Kept in this browser. Searching ignores it, so a hidden message can still be found.
const SHOW_KEY = "hive.comms.kinds", OLD_KEY = "hive.comms.show", OLD = { you: SHOW.you, updates: SHOW.you | SHOW.updates, all: SHOW.ALL };
function showMask() {
  if (simpleOn()) return SHOW.you | SHOW.updates;   // fixed: there's no Show bar here
  if (C.show === null) {
    try { const v = localStorage.getItem(SHOW_KEY); C.show = v !== null ? Number(v) & SHOW.ALL : OLD[localStorage.getItem(OLD_KEY)]; } catch (e) { /* no storage: the default, for this page */ }
    if (C.show == null) C.show = app() ? SHOW.you | SHOW.updates : SHOW.ALL;
  }
  return C.show;
}
function setShow(mask, ctx) { C.show = mask; try { localStorage.setItem(SHOW_KEY, mask); } catch (e) { /* no storage: this page only */ } ctx.update(); }
/** What this conversation's messages are cut to. By or to a name you asked for by name, and a search, show everything. */
const maskFor = c => (simpleOn() || ((c.thread || c.all) && !words(C.q).length) ? showMask() : SHOW.ALL);
/** A conversation this person is part of. In simple mode the hive's own (its upkeep, its agents between themselves) stay out of the list. */
const theirs = d => !simpleOn() || d.posts.some(p => p.from === "person");
const closing = { n: -1, set: null };
const endedRuns = () => { let n = 0; for (const s of S.sessions) if (s.end) n++; return n; };
function closers(n) { const k = n + ":" + S.posts.length; if (closing.n !== k) { closing.n = k; closing.set = closingPosts(); } return closing.set; }   // the run's end and its last post can reach us in either order

function matcher(c, mask, ended) {
  const q = words(C.q), last = mask & SHOW.you ? closers(ended) : null;
  return p => {
    if (!(postKind(p) & mask) && !(last && last.has(p.id)) && c.thread !== SIDE_THREAD) return false;   // the sidebar agent's posts are in no list, but its own thread opens from Agents
    if (c.agent && p.from !== c.agent && !p.men.includes(c.agent)) return false;
    if (c.pair && !((p.from === c.pair[0] && p.men.includes(c.pair[1])) || (p.from === c.pair[1] && p.men.includes(c.pair[0])))) return false;
    if (q.length && !q.every(w => hay(p).includes(w) || (!c.thread && (p.th + " " + threadTitle(p.th)).toLowerCase().includes(w)))) return false;
    return true;
  };
}

// Things that happened to the agents in a thread, shown in the conversation in the order they happened: a stop at a usage
// limit, a run that ended in an error. Only trouble: start and end are in each message's head and the strip under the list.
function threadEvents(th) {
  const out = [];
  if (!app()) return out;
  for (const s of S.sessions) {
    if (s.thread !== th || !s.end) continue;
    if (s.limited) out.push({ ev: true, key: "ev:" + s.session + ":" + s.start, t: s.end, tone: "limited", text: simpleOn() ? "Your Hive took a break" : `${s.name} stopped at a usage limit`, sub: "It carries on by itself once there's room." });
    else if (s.exit && s.exit !== 143) out.push({ ev: true, key: "ev:" + s.session + ":" + s.start, t: s.end, tone: "failed", text: simpleOn() ? "Your Hive ran into a problem" : `${s.name} stopped with an error (exit ${s.exit})`, sub: simpleOn() ? "Send a message and it tries again." : "Its last message above is where it got to." });
  }
  return out;
}

/** Bring the matches up to date, looking only at posts not yet seen unless the conversation or filter changed. */
function refresh(c) {
  const src = c.thread ? (S.threads.get(c.thread) || { posts: NONE }).posts : c.home ? NONE : S.posts;
  // With For you on, a run ending can promote a post already in the list, so the list is redone then. That must not move someone who has scrolled up.
  const mask = maskFor(c), ended = mask & SHOW.you ? endedRuns() : 0, base = [c.arg, C.q, mask].join("\0"), key = base + "\0" + ended, match = matcher(c, mask, ended);
  const evs = c.thread && !C.q ? threadEvents(c.thread) : [];
  if (key !== L.key || src !== L.src) {
    const m = src.filter(match);
    Object.assign(L, { key, base, src, matched: m, seen: src.length, pend: [], evs: new Set(evs.map(e => e.key)), rebuild: true, keep: base === L.base && src === L.src && !L.stuck, unseen: 0 });
    L.all = evs.length ? [...m, ...evs].sort((a, b) => a.t - b.t) : [...m];
    return;
  }
  for (let i = L.seen; i < src.length; i++) if (match(src[i])) { L.matched.push(src[i]); L.pend.push(src[i]); L.all.push(src[i]); }
  for (const e of evs) if (!L.evs.has(e.key)) { L.evs.add(e.key); L.pend.push(e); L.all.push(e); }
  L.seen = src.length;
}

// ---- the message list ----
/** A card item whose message has been on screen is seen: it leaves the card (110401502e1a). */
function cardSeen(box) {
  const ids = [...document.querySelectorAll(".tm-items li[data-key]")].map(li => li.dataset.key), b = box.getBoundingClientRect();
  const on = ids.filter(id => { const el = box.querySelector(`[data-key="${CSS.escape(id)}"]`); if (!el) return false; const r = el.getBoundingClientRect(); return r.top >= b.top - 4 && r.top < b.bottom - 40; });
  if (on.length && markSeen(on)) L.ctx && L.ctx.update();
}
const atEnd = b => b.scrollTop + b.clientHeight > b.scrollHeight - NEAR;
const sameTo = (a, b) => a.men.join() === b.men.join();
const sameSource = (a, b) => { const x = postSource(a), y = postSource(b); return (x && x.name + x.how) === (y && y.name + y.how); };

function evRow(e) {
  return h("div", { class: "cm-ev", "data-key": e.key, "data-tone": status(e.tone).tone, role: "note" }, statusDot(e.tone), h("b", null, e.text), h("span", { class: "meta" }, clock(e.t) + " · " + e.sub));
}
const KIND_NAME = { [SHOW.you]: "you", [SHOW.updates]: "updates", [SHOW.comms]: "comms" };
function rows(items) {
  const out = [];
  for (const p of items) {
    const d = dayName(p.t);
    if (d !== L.day) { L.day = d; L.prev = null; out.push(h("div", { class: "cm-day", "data-key": "d:" + d }, h("span", null, d))); }
    if (p.ev) { out.push(evRow(p)); L.prev = null; continue; }
    L.names.add(p.from); for (const m of p.men) L.names.add(m);
    const q = L.prev, cont = !!q && q.from === p.from && q.th === p.th && !p.reply_to && p.t - q.t < GROUP && sameTo(q, p) && sameSource(q, p);
    const a = message(p, { ...L.opts, cont }); a.dataset.show = KIND_NAME[postKind(p)] || ""; if (a.dataset.show === "you" && asksQuestion(p)) a.dataset.ask = "";   // For you, Updates or Agent comms, marked in the list (11040113eacf)
    L.prev = p; a._head = L.heads; L.io.observe(a); out.push(a);
  }
  return out;
}

function paint(box, ctx) {
  const shown = L.all.slice(-C.limit), older = L.all.length - shown.length;
  L.day = ""; L.prev = null; L.names = new Set(); L.shown = shown.length; L.io.disconnect(); L.outSig = "";
  L.out = h("div", { class: "cm-out", "data-key": "out" });
  L.jump = h("button", { type: "button", class: "cm-jump", hidden: true, onclick: () => { box.scrollTop = box.scrollHeight; } });
  const q = C.q.trim();   // a search narrows the messages: say so where they are, so a sent one never seems lost (11040214d468)
  box.replaceChildren(
    ...(q ? [h("div", { class: "cm-qbar", role: "status" }, h("span", null, `Showing ${plural(L.all.length, "match", "matches")} for “${short(q, 40)}”`),
      button("Clear search", () => { C.q = ""; C.find = false; ctx.update(); }, { size: "s", kind: "quiet", icon: "close" }))] : []),
    ...(older ? [h("div", { class: "cm-older" }, button(`Show older (${older} more)`, () => { C.limit += STEP; L.rebuild = true; L.keep = true; ctx.update(); }, { size: "s" }))] : []),
    ...(shown.length ? rows(shown) : [empty(C.q ? "No messages match." : L.src.length && !simpleOn() ? "Nothing here with what Show has on. Turn on Agent comms to see all the messages." : "No messages yet.")]),
    L.out, L.jump);
}
function setJump() {
  if (!L.jump) return;
  L.jump.hidden = !L.unseen;
  L.jump.textContent = L.unseen ? `${plural(L.unseen, "new message")} ↓` : "";
}
function flash(box, el) {
  // a short message sits a third down; a tall one starts near the top so it reads from its first line
  box.scrollTop = Math.max(0, el.offsetTop - (el.offsetHeight > box.clientHeight * 0.6 ? 24 : box.clientHeight / 3));
  L.stuck = atEnd(box);
  el.setAttribute("data-flash", ""); setTimeout(() => el.removeAttribute("data-flash"), 1800);
}
const jumpTo = id => {
  const p = S.byId[id], box = L.box; if (!p || !box) return;
  if (L.opts && conversation(L.ctx && L.ctx.arg).thread && p.th !== conversation(L.ctx.arg).thread) { L.jumpTo = id; return go("comms", p.th); }
  const el = box.querySelector(`[data-key="${CSS.escape(id)}"]`);
  if (el) return flash(box, el);
  const i = L.all.indexOf(p);
  if (i < 0) { if (showMask() === SHOW.ALL || !L.src.includes(p)) return; L.jumpTo = id; return setShow(SHOW.ALL, L.ctx); }   // a reply quoting a message this setting hides
  C.limit = L.all.length - i + 20; L.rebuild = true; L.jumpTo = id; L.ctx.update();
};
onJump(jumpTo);

function syncOut(c, box) {
  const th = c.thread; if (!th || !L.out) return;
  const sig = outSig(th); if (sig === L.outSig) return;
  const grew = sig.length > L.outSig.length; L.outSig = sig;
  L.out.replaceChildren(...outFor(th).map(it => localMessage(it, outActions)));
  if (grew || L.stuck) { box.scrollTop = box.scrollHeight; L.stuck = true; }
}

function sync(c, ctx) {
  const box = document.querySelector("#main .cm-msgs");
  if (box) {
    const isNew = box !== L.box;
    if (isNew) {
      if (L.ro) { L.ro.disconnect(); L.io.disconnect(); }
      L.box = box; L.rebuild = true; L.stuck = true;
      box.addEventListener("scroll", () => {
        if (L.box !== box) return;
        L.stuck = atEnd(box);
        if (L.stuck && L.unseen) { L.unseen = 0; setJump(); }
        cardSeen(box);
        L.saved = L.stuck ? null : { key: L.key, fromEnd: box.scrollHeight - box.scrollTop };
      }, { passive: true });
      // The box changes size when the composer grows, the keyboard opens or the window moves: stay at the end if that's where it was.
      L.ro = new ResizeObserver(() => { if (L.stuck) box.scrollTop = box.scrollHeight; });
      L.ro.observe(box);
      box.addEventListener("load", () => { if (L.box === box && L.stuck) box.scrollTop = box.scrollHeight; }, true);   // a picture arriving grows the list
      L.near = new Set();
      L.io = new IntersectionObserver(es => {
        for (const e of es) { if (e.isIntersecting) { L.near.add(e.target); refreshHead(e.target); } else L.near.delete(e.target); }
      }, { root: box, rootMargin: "800px 0px" });
    }
    if (L.rebuild) {
      const fromEnd = L.keep ? box.scrollHeight - box.scrollTop : isNew && L.saved && L.saved.key === L.key ? L.saved.fromEnd : null;
      paint(box, ctx);
      L.rebuild = false; L.keep = false;
      syncOut(c, box);
      if (fromEnd != null) { box.scrollTop = box.scrollHeight - fromEnd; L.stuck = atEnd(box); }
      else { box.scrollTop = box.scrollHeight; L.stuck = true; }
      if (L.jumpTo) { const el = box.querySelector(`[data-key="${CSS.escape(L.jumpTo)}"]`); L.jumpTo = null; if (el) flash(box, el); }
      for (const el of box.children) L.ro.observe(el);   // a message that grows after it lands (a table or code drawn late) keeps the list at its end
    } else if (L.pend.length) {
      const stick = atEnd(box) || L.stuck;
      box.querySelector(":scope > .empty")?.remove();
      const fresh = rows(L.pend); L.out.before(...fresh); fresh.forEach(el => el instanceof Element && L.ro.observe(el));
      L.shown += L.pend.length;
      if (stick) box.scrollTop = box.scrollHeight;
      else { L.unseen += L.pend.filter(p => !p.ev && !OUTSIDE.has(p.from)).length; setJump(); }
    }
    L.pend = [];
    syncOut(c, box);
    refreshHeads();
  }
  syncThreads(c, ctx);
}

/** Names and read marks change with no new post. Only the head and receipt of a post are morphed, only near the viewport
 *  (at 1700 posts, all of them took half a second); the others catch up as they scroll into view. */
function refreshHeads() {
  let sig = S.ver + "|" + marksSig();
  for (const n of L.names) sig += "|" + agentStatus(n).key;
  if (sig === L.heads) return;
  L.heads = sig;
  for (const a of L.near) refreshHead(a);
  // The newest ones too, whatever the observer says: that's where receipts change, and it holds when the page isn't drawn.
  let n = L.out; for (let i = 0; n && i < 40; i++) { n = n.previousElementSibling; if (n && n.matches("article")) refreshHead(n); }
}
function refreshHead(a) {
  if (a._head === L.heads) return;
  a._head = L.heads;
  const p = S.byId[a.getAttribute("data-key")]; if (!p) return;
  const hd = a.querySelector(":scope > .msg-h"), rc = a.querySelector(":scope > .msg-rc");
  if (hd) morph(hd, messageHead(p, L.opts));
  if (rc) morph(rc, messageReceipt(p));
}

// ---- the thread list ----
/** The part of a post around the first word found, so the word is still in view when the row cuts it off. */
function snippet(p, ws) {
  const text = mdPlain(displayText(p)).replace(/\s+/g, " ").trim(), lc = text.toLowerCase();
  const at = Math.min(...ws.map(w => lc.indexOf(w)).filter(i => i >= 0));
  if (at === Infinity) return short(text, 70);
  const from = Math.max(0, at - 16);
  return markWords((from ? "…" : "") + text.slice(from, from + 90), ws);
}
const authorName = n => (n === "person" ? "You" : plainName(n));
const tierLead = th => (app() ? null : h("span", { class: "ref-sw", "data-tone": tier(tierOf(th)).tone, title: tier(tierOf(th)).label }));

/** One thread's state in a few words, for the list: shape, colour and the words. */
const STATE_WORDS = { asks: () => "Waiting for your answer", working: s => (simpleOn() ? "Working on it" : `${s.who} working`), quiet: s => (simpleOn() ? null : `${s.who} quiet`), stuck: () => (simpleOn() ? "Taking longer than it should" : "Stuck"), queue: s => (simpleOn() ? "Waiting its turn" : `${s.who} in the job queue`),
  queued: () => "Waiting to start", limited: () => (simpleOn() ? "Taking a break" : "Stopped at a usage limit"), failed: () => (simpleOn() ? "Ran into a problem" : "Stopped with an error"), foryou: () => "New reply for you", done: () => null };
function stateTag(st) {
  const w = (STATE_WORDS[st.key] || (() => null))(st);
  return w ? h("span", { class: "cm-st", "data-tone": status(st.key).tone }, w) : null;
}

/** Threads with a post holding every word (a word in the thread's own name counts for all its posts). Looks through all of them. */
function searchRows(c, ctx, ws) {
  const open = (name, q) => () => { if (name === (c.thread ?? null)) C.q = q; else C.next = q; ctx.update(); };
  let total = 0; const found = [];
  for (const d of S.threads.values()) {
    if (!theirs(d) || d.name === SIDE_THREAD) continue;
    const lc = (d.name + " " + threadTitle(d.name)).toLowerCase(), rest = ws.filter(w => !lc.includes(w));
    if (!rest.length) { total += d.posts.length; found.push({ d, hits: d.posts, rest }); continue; }
    const hits = d.posts.filter(p => rest.every(w => hay(p).includes(w)));
    if (hits.length) { total += hits.length; found.push({ d, hits, rest }); }
  }
  found.sort((a, b) => b.hits.at(-1).t - a.hits.at(-1).t);
  const all = C.tq.trim();
  return [
    simpleOn() ? null : { key: "(everything)", href: href("comms", "~all"), title: "All messages", text: `${plural(total, "match", "matches")} in ${plural(found.length, app() ? "conversation" : "thread")}`, current: c.all, onClick: () => { if (c.all) C.q = all; else C.next = all; ctx.update(); } },
    ...found.map(({ d, hits, rest }) => ({ key: d.name, href: threadHref(d.name), current: c.thread === d.name, title: threadTitle(d.name), meta: ago(hits.at(-1).t).replace(" ago", ""),
      lead: tierLead(d.name), onClick: open(d.name, rest.join(" ")),
      text: rest.length ? [`${hits.length} · ${authorName(hits.at(-1).from)}: `, snippet(hits.at(-1), rest)] : `${d.posts.length} · name matches` })),
  ];
}

const NEED = new Set(["asks", "foryou", "stuck", "failed", "limited"]);
// A greyed row says so in words, and why on hover: the agents' guess that the person needn't look at it any more.
const fadeWhy = th => { const m = tmeta(th); return m.fadedBy ? `Marked by ${m.fadedBy === "auto" ? "the hive" : m.fadedBy}${m.fadedWhy ? ": " + m.fadedWhy : ""}. Write in it, or choose Needed again under More, and it comes back.` : "You marked this as not needed. Write in it, or choose Needed again under More, and it comes back."; };
const fadeTag = th => (faded(th) ? h("span", { class: "tm-faded", title: fadeWhy(th) }, "Likely not needed") : null);
const secOf = (th, st) => (pinned(th) ? "pin" : important(th) && !archived(th) ? "imp" : archived(th) ? "arch" : st && NEED.has(st.key) && !faded(th) ? "need" : "rest");
// "@question" in the side search asks the sidebar agent (dash.py, side_ask): a normal agent in the hidden thread `side` that answers in one line, shown here, and brings threads up with `hive show` into the box above the list.
// Enter sends it; plain words search as before. A sign-in that can't post can't wake an agent, so it asks Haiku which threads fit (thread_find.py).
const AI = { q: "", st: "", res: [], tag: "", at: 0 };
const AI_WAIT = 2 * MIN;
const aiQ = () => (C.tq.trim().startsWith("@") ? C.tq.trim().slice(1).trim() : "");
async function aiAsk(ctx) {
  const q = aiQ(); if (!q || (AI.q === q && AI.st && AI.st !== "error")) return;
  Object.assign(AI, { q, st: "loading", res: [], tag: "", at: now() }); ctx.update();
  try {
    if (can("post")) { const r = await apiPost("/api/side-ask", { q }); if (AI.q === q) Object.assign(AI, { st: "asked", tag: r.tag }); }
    else { const r = await jget("/api/thread-find?q=" + encodeURIComponent(q)); if (AI.q === q) Object.assign(AI, r.error ? { st: "error" } : { st: "done", res: r.threads || [] }); }
  } catch (e) { if (AI.q === q) AI.st = "error"; }
  ctx.update();
}
/** The agent's post that answers the question asked: it starts with the question's tag. Its text without the tag, and who wrote it. */
function aiAnswer() {
  const d = AI.tag && S.threads.get(SIDE_THREAD), head = `[${AI.tag}]`;
  const p = d && d.posts.find(p => !SYS.has(p.from) && String(p.text).trim().startsWith(head));
  return p ? { from: p.from, at: p.t, text: mdPlain(String(p.text).trim().slice(head.length)).trim() } : null;
}
function aiRows(c) {
  const q = aiQ(), note = (title, text, extra) => [{ key: "(ai)", title, text, href: null, ...extra }];
  if (!q) return note("Ask anything", "Type your question, then press Enter");
  if (AI.q !== q || !AI.st) return note("Press Enter to ask", can("post") ? "An agent looks it up and answers here" : "Haiku reads the conversations and picks the ones that fit");
  if (AI.st === "error") return note("Couldn't ask", "Try again: press Enter");
  if (AI.st === "done") {
    const rows = AI.res.filter(r => S.threads.has(r.thread)).map(r => { const d = S.threads.get(r.thread);
      return { key: r.thread, href: threadHref(r.thread), current: c.thread === r.thread, title: threadTitle(r.thread), meta: ago(d.last).replace(" ago", ""), lead: tierLead(r.thread), text: r.why || "fits" }; });
    return rows.length ? rows : note("Nothing fits", "Try other words");
  }
  const a = aiAnswer();
  if (!a) return now() - AI.at < AI_WAIT ? note("Looking…", "An agent is looking it up") : note("No answer yet", "It shows here if it comes. Claude may be at its limit");
  const also = (S.shown || []).filter(r => r.from === a.from && r.at * 1000 >= AI.at - 5000 && S.threads.has(r.thread));   // the app's box above the list holds these; the developer look has no box
  return [...note(q, a.text, { whole: true }), ...(app() ? [] : also.map(r => ({ key: r.thread, href: threadHref(r.thread), current: c.thread === r.thread, title: threadTitle(r.thread), text: r.why || "" })))];
}
// The topics come from the Where things stand summary (needs_you.py, every 30 min): each of its top-level entries lists its
// threads, and a focus or project names its own thread. A conversation not in either takes its parent's topic, else Other.
const TOPICS = { at: null, map: new Map() };
function briefTopics() {
  const n = STAND.needs, b = n && n.brief, at = (b && b.at) + "|" + ((n && n.states) || []).length;
  if (TOPICS.at === at) return TOPICS.map;
  const map = new Map();
  if (b) for (const g of ["needs", "going", "done"]) for (const top of b[g] || []) for (const it of top.items || []) if (it.thread && !map.has(it.thread)) map.set(it.thread, top.title);
  for (const st of (n && n.states) || []) if (st.thread && st.name && !map.has(st.thread)) map.set(st.thread, st.name);
  TOPICS.at = at; TOPICS.map = map; return map;
}
const topicsOn = () => app() && !simpleOn() && !C.imp && C.view === "topics" && briefTopics().size > 0;
const OTHER = "Other";
function topicOf(listed) {
  const map = briefTopics();
  return th => { let x = th; for (let i = 0; i < 8 && x; i++) { if (map.has(x)) return map.get(x); x = threadParent(x); } return OTHER; };
}
function threadRows(c, ctx) {
  if (C.tq.trim().startsWith("@")) return aiRows(c);
  const ws = words(C.tq);
  if (ws.length) return searchRows(c, ctx, ws);
  const t = now(), mask = showMask(), ran = new Set(S.live ? S.live.agents.map(a => a.thread) : []);
  // Pinned ones are always listed; archived ones go to their own fold at the end, unless something new is for the person (threads: a3100).
  let ds = [...S.threads.values()].filter(d => pinned(d.name) || important(d.name) || ((threadKinds(d) & mask) && theirs(d)) || d.name === c.thread);
  if (C.scope === "recent" && !simpleOn()) ds = ds.filter(d => pinned(d.name) || important(d.name) || archived(d.name) || d.last > t - (app() ? 7 : 1) * DAY || ran.has(d.name) || d.name === c.thread || (app() && threadState(d.name, seen).key === "asks"));
  if (C.imp) ds = ds.filter(d => important(d.name));
  if (C.hideFaded && app()) ds = ds.filter(d => !faded(d.name) || d.name === c.thread);
  const flat = !app() || C.imp || C.view === "recent";
  // A thread an agent started from a listed conversation folds under it (the person, 110601450d55: one chat made eight side entries
  // and the original got lost). A root is the top of its chain among the listed ones; its row sorts by the latest activity in the family.
  const listed = new Map(ds.map(d => [d.name, d]));
  const rootOf = d => { let x = d; for (let i = 0; i < 8; i++) { const p = listed.get(threadParent(x.name) || ""); if (!p || p === d) break; x = p; } return x; };
  const kidsOf = new Map(), act = new Map();
  if (!flat) for (const d of ds) { const r = rootOf(d); if (r !== d) { (kidsOf.get(r.name) || kidsOf.set(r.name, []).get(r.name)).push(d); act.set(r.name, Math.max(act.get(r.name) || r.last, d.last)); } }
  const roots = ds.filter(d => flat || rootOf(d) === d);
  roots.sort((a, b) => (act.get(b.name) || b.last) - (act.get(a.name) || a.last));
  const mk = d => {
    const lp = d.posts[d.posts.length - 1], live = ran.has(d.name);
    // The developer route keeps its one-line log row: 100+ threads have to fit.
    if (!app()) {
      const who = lp.from.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
      return { key: d.name, href: threadHref(d.name), current: c.thread === d.name, title: threadTitle(d.name), meta: ago(d.last).replace(" ago", ""),
        lead: [tierLead(d.name), live ? h("span", { title: "an agent is on this thread now" }, statusDot("working", { live: true })) : null],
        text: `${d.posts.length} · ${lp.from}: ${short(mdPlain(lp.text).replace(new RegExp("^(@\\S+\\s+)*" + who + ":\\s*"), ""), 60)}`, sec: secOf(d.name, null), dim: faded(d.name) };
    }
    const st = threadState(d.name, seen), par = flat && threadParent(d.name);
    const say = short(mdPlain(displayText(lp)).replace(/\s+/g, " "), 70);
    const from = par ? [h("span", { class: "tm-from", title: "A helper conversation, started from " + threadTitle(par) }, "from " + short(threadTitle(par), 28)), " · "] : null;
    return { key: d.name, href: threadHref(d.name), current: c.thread === d.name, title: threadTitle(d.name), meta: ago(d.last).replace(" ago", ""),
      lead: [tierLead(d.name), h("span", { title: status(st.key).label || "" }, statusDot(st.key === "done" ? "ended" : st.key, { live }))],
      text: [from, d.name !== c.thread && hasDraft(d.name) ? [h("span", { class: "cm-draft", title: "You left unsent text in this conversation" }, "Draft"), " · "] : null, fadeTag(d.name) || stateTag(st), fadeTag(d.name) || stateTag(st) ? " · " : null, st.who && st.who === lp.from && ["working", "quiet"].includes(st.key) && !simpleOn() ? say : `${authorName(lp.from)}: ${say}`], sec: secOf(d.name, st), dim: faded(d.name) };
  };
  const dress = (r, th = r.key.replace(/~recent$/, "")) => {
    const im = important(th), pn = pinned(th), m = tmeta(th);
    if (im) r.lead = [h("span", { class: "tm-imp", title: m.impWhy ? `Important, from ${m.impBy || "an agent"}: ${m.impWhy}` : m.impBy ? `Important, marked by ${m.impBy}` : "Important" }, icon("star")), r.lead];
    if (can("post")) r.acts = [
      h("button", { type: "button", class: "lr-act", "aria-pressed": String(im), "aria-label": im ? "Not important" : "Mark important", title: im ? "Take the important mark off" : "Mark important", onclick: () => tryMeta(th, { important: !im }, ctx) }, icon("star")),
      h("button", { type: "button", class: "lr-act", "aria-pressed": String(pn), "aria-label": pn ? "Unpin" : "Pin to the top", title: pn ? "Unpin" : "Pin to the top", onclick: () => tryMeta(th, { pin: !pn }, ctx) }, icon("pin"))];
    return r;
  };
  // Recent holds every conversation by last activity, so a pinned, important or needs-you one also keeps its place there (the person, 11060006f821):
  // a second row built fresh, since a row's icons are DOM nodes and one node can sit in one place only. Archived ones stay in their fold.
  // Helpers sit in a fold under the conversation they came from, in its Recent row; one that needs the person also keeps its own
  // row in Needs you (or Pinned, Important), so nothing waiting on them hides in a fold. The current conversation's fold is open.
  const kidRows = (d, sec) => {
    const kids = kidsOf.get(d.name) || []; if (!kids.length) return [];
    const open = KIDS.has(d.name) || kids.some(k => k.name === c.thread), sts = kids.map(k => threadState(k.name, seen).key);
    const n = k => sts.filter(x => k.includes(x)).length, parts = [[["working", "queued", "quiet"], "working"], [[...NEED], "needs you"], [["done"], "done"]].map(([ks, w]) => (n(ks) ? `${n(ks)} ${w}` : null)).filter(Boolean);
    return [{ key: d.name + "~kids", sec, href: null, depth: 1, whole: true, expanded: open, title: plural(kids.length, "helper conversation"), meta: parts.length > 1 ? h("span", { title: parts.join(" · ") }, parts[0]) : parts[0] || null,   // first part only, the rest in the tooltip
      lead: h("span", { class: "cm-stand-ch", "data-open": open ? "" : null }, icon("chevron-down")),
      onClick: e => holdScroll(e.currentTarget, () => { KIDS.has(d.name) ? KIDS.delete(d.name) : KIDS.add(d.name); ctx.update(); }) },
      ...(open ? kids.sort((a, b) => b.last - a.last).map(k => dress({ ...mk(k), key: d.name + "/" + k.name, sec, depth: 2 }, k.name)) : [])];
  };
  const topic = topicsOn() ? topicOf(listed) : null;
  const rowsOut = !app() ? ds.map(mk) : roots.flatMap(d => {
    const r = dress(mk(d)), kids = kidsOf.get(d.name) || [], rest = topic ? "t:" + topic(d.name) : "rest";   // in the topics view, "the rest" is the conversation's topic
    if (r.sec === "rest") r.sec = rest;
    const own = kids.filter(k => !["rest", "arch"].includes(secOf(k.name, threadState(k.name, seen)))).map(k => dress(mk(k)));   // a helper's own row where it needs the person
    if (C.imp || r.sec === rest || r.sec === "arch") return [r, ...kidRows(d, r.sec), ...own];
    return [r, dress({ ...mk(d), key: d.name + "~recent", sec: rest }), ...kidRows(d, rest), ...own];
  });
  if (!app()) rowsOut.unshift({ key: "(everything)", href: href("comms", "~all"), title: "Everything", text: `${S.posts.length} posts, newest last`, current: c.all });
  return rowsOut;
}
const KIDS = new Set();   // conversations whose helper fold is open, this page only
// Folded sections (the person, 11040202d77c): kept in this browser.
const FOLD_KEY = "hive.comms.folded";
const FOLD = new Set((() => { try { return JSON.parse(localStorage.getItem(FOLD_KEY) || "[]"); } catch (e) { return []; } })());
function saveFold() { try { localStorage.setItem(FOLD_KEY, JSON.stringify([...FOLD])); } catch (e) { /* this page only */ } }
// Collapse or expand all (the person, 11061437ab67): the section heads and the helper folds in the list now. syncThreads fills L.foldSecs and L.foldKids.
// Which way it goes follows the sections, so a list with most of them open offers Collapse all; a list with no heads goes by its helper folds.
const foldShut = () => { const ss = L.foldSecs || [], ks = L.foldKids || []; return ss.length ? ss.every(k => FOLD.has(k)) : !ks.some(n => KIDS.has(n)); };
const foldWord = () => foldShut() ? "Expand all" : "Collapse all";
const foldNone = () => !(L.foldSecs || []).length && !(L.foldKids || []).length;
function foldAll(ctx) {
  const open = foldShut(), ss = L.foldSecs || [], ks = L.foldKids || [];
  if (open) { ss.forEach(k => FOLD.delete(k)); ks.forEach(n => KIDS.add(n)); }
  else { ss.forEach(k => FOLD.add(k)); KIDS.clear(); C.arch = false; }   // Expand all leaves Archived shut: it is out of the way on purpose
  saveFold(); ctx.update();
}
// The list in parts: Pinned, Needs you, the rest, and Archived folded at the end. A search is one list.
// In the topics view the rest is one section per topic ("t:<title>"), in order of their latest activity, with Other last.
const SECS = [["pin", "Pinned"], ["imp", "Important"], ["need", "Needs you"], ["rest", null]];
function sections(rs, ctx, opts) {
  if (words(C.tq).length) return list(rs, opts);
  const by = { pin: [], imp: [], need: [], rest: [], arch: [] }, tops = [];
  for (const r of rs) { const k = r.sec || "rest"; if (k.startsWith("t:") && !by[k]) { by[k] = []; tops.push(k); } (by[k] || by.rest).push(r); }
  tops.sort((a, b) => (a === "t:" + OTHER) - (b === "t:" + OTHER));
  const parts = [...SECS.slice(0, 3), ...tops.map(k => [k, k.slice(2)]), SECS[3]].filter(([k]) => by[k].length), heads = parts.length > 1 || by.pin.length;
  L.foldSecs = heads ? parts.map(([k]) => k) : [];
  const sec = (k, label, rows) => {
    if (!heads) return h("div", { class: "tm-sec", "data-key": "sec:" + k, "data-sec": k }, list(rows, { ...opts, empty: "" }));
    const shut = FOLD.has(k);
    return h("div", { class: "tm-sec", "data-key": "sec:" + k, "data-sec": k, "data-folded": shut ? "" : null },
      h("button", { type: "button", class: "tm-sec-h tm-fold", "aria-expanded": String(!shut), title: shut ? "Show this section" : "Fold this section",
        onclick: () => { FOLD.has(k) ? FOLD.delete(k) : FOLD.add(k); saveFold(); ctx.update(); } },
        icon(shut ? "chevron-right" : "chevron-down"), shut ? `${label} (${rows.length})` : label),
      shut ? null : list(rows, { ...opts, empty: "" }));
  };
  return h("div", { class: "tm-secs" },
    parts.length ? parts.map(([k, l]) => sec(k, l || (app() ? "Recent" : "Threads"), by[k])) : by.arch.length ? null : list([], opts),
    by.arch.length ? h("div", { class: "tm-sec", "data-key": "sec:arch", "data-sec": "arch" },
      h("button", { type: "button", class: "tm-sec-h tm-arch-b", "aria-expanded": String(!!C.arch), onclick: () => { C.arch = !C.arch; ctx.update(); } },
        icon(C.arch ? "chevron-down" : "chevron-right"), `Archived (${by.arch.length})`),
      C.arch ? list(by.arch, { ...opts, empty: "" }) : null) : null);
}
function syncThreads(c, ctx) {
  const tl = document.querySelector("#main .cm-tl"); if (!tl) return;
  if (app() && !simpleOn() && C.view === "topics") loadStanding(ctx);
  const sig = [S.ver, C.view, C.hideFaded, C.view === "topics" && STAND.needs && STAND.needs.brief && STAND.needs.brief.at, C.scope, C.arch, C.imp, tmetaSig(), [...FOLD].join(), [...KIDS].join(), C.tq, AI.st + AI.res.length, AI.tag, S.shown && S.shown.length, showMask(), c.arg, Math.floor(now() / 3e4), S.runSig, marksSig().length].join("|");
  if (tl === L.tl && sig === L.tlSig) return;
  const fresh = tl !== L.tl || c.arg !== L.tlConv;
  L.tl = tl; L.tlSig = sig; L.tlConv = c.arg;
  const rows = threadRows(c, ctx);
  L.foldKids = rows.filter(r => r.key.endsWith("~kids")).map(r => r.key.slice(0, -5));
  const tree = sections(rows, ctx, { dense: true, empty: simpleOn() ? "No conversations yet. Start one with New conversation." : S.threads.size ? "Nothing here with what Show has on. Turn on Agent comms to see all of them." : app() ? "No conversations yet. Start one with New." : "No threads." });
  if (tl.firstChild) morph(tl.firstChild, tree); else tl.append(tree);
  const fb = document.querySelector("#main .cm-foldall");   // the list is built after the bar, so the bar's word catches up here
  if (fb) { fb.hidden = foldNone(); if (fb.textContent !== foldWord()) fb.textContent = foldWord(); }
  if (fresh) { const cur = tl.querySelector("[aria-current]"); if (cur) { const r = cur.getBoundingClientRect(), b = tl.getBoundingClientRect(); if (r.top < b.top || r.bottom > b.bottom) tl.scrollTop += r.top - b.top - 40; } }
}

// ---- what's happening: the strip above the composer ----
function hasRoom() { const ls = (S.usage && S.usage.logins) || (S.live && S.live.logins) || []; return !ls.length || ls.some(l => inUse(l, S.usage)); }
function nextReset() {
  let t = null;
  for (const l of (S.live && S.live.logins) || []) { const L7 = l.limits && l.limits.seven_day, L5 = l.limits && l.limits.five_hour;
    for (const w of [L5, L7]) if (w && w.u >= 0.95 && (!t || w.r * 1e3 < t)) t = w.r * 1e3; }
  return t;
}
const AGENT_WORDS = { working: "is working", quiet: "is here, quiet", stuck: "looks stuck", queue: "is waiting for the machine" };
function line(key, text, act, opts = {}) {
  return h("div", { class: "cm-line", "data-key": opts.key || key, "data-tone": status(key).tone }, statusDot(key, { live: opts.live }), h("span", { class: "cm-line-x" }, text), act ? h("span", { class: "cm-line-a" }, act) : null);
}
/** An agent's name in a strip line: a link to it, without a second dot (the line has its own). */
const who = n => (simpleOn() ? hiveName() : agentRef(n, { dot: false }));
const d0 = th => { const d = S.threads.get(th); return d && d.posts[0] ? d.posts[0].from : "an agent"; };
function lastMine(th) { const d = S.threads.get(th); if (!d) return null; for (let i = d.posts.length - 1; i >= 0; i--) if (d.posts[i].from === "person") return d.posts[i]; return null; }
const notBuilt = () => h("span", { class: "nb", title: "Designed, not built yet: the server needs to do this" }, "not built yet");

/** The two lines an agent (or the status refresher) wrote with `hive status`: what the thread is about, where it stands. */
const lastAt = th => { const d = S.threads.get(th), p = d && d.posts.at(-1); return p ? p.t : 0; };
function statusBar(th) {
  const r = th && S.threadStatus && S.threadStatus[th]; if (!r) return null;
  const [a, ...rest] = r.text.split("\n");
  return h("div", { class: "cm-status", "data-key": "status", role: "note", "aria-label": "Thread status" },
    h("b", null, a), rest.length ? h("span", null, rest.join(" ")) : null,
    h("span", { class: "meta cm-status-m" }, `${r.from} · updated ${ago(r.at * 1e3)}${lastAt(th) ? " · last post " + ago(lastAt(th)) : ""}`,
      can("post") ? h("button", { type: "button", class: "cm-status-r", title: "Reply about this summary", onclick: () => { const d = draft(th); if (!d.text) d.text = "> " + r.text.split("\n").join("\n> ") + "\n\n"; focusComposer(); } }, "Reply") : null));
}

function strip(th) {
  const st = threadState(th, seen), out = [], run = (S.live ? S.live.agents : []).filter(a => a.thread === th), post = can("post") && app();
  const call = callFor(th);
  if (call) out.push(line("working", [who(call.agent), call.state === "hold" ? " is putting you through on your call" : " is on a call with you", h("span", { class: "meta cm-line-m" }, " · " + dur(now() - call.started * 1e3))], post ? button("Hang up", () => hangUp(call.id), { size: "s" }) : null, { key: "call", live: true }));
  if (st.key === "asks") out.push(line("asks", [who(st.who), " is waiting for your answer: ", h("q", null, short(lastQuestion(st.post), 110))],
    post ? button("Answer", () => replyTo(st.post), { size: "s" }) : null, { key: "asks" }));
  if (st.key === "limited") {
    const room = hasRoom(), r = nextReset();
    out.push(line("limited", [who(st.who), simpleOn() ? " is taking a break. " : ` stopped at a usage limit ${ago(st.t)} and hasn't carried on. `, room ? "There's room again now, so it should carry on by itself soon." : r ? `It carries on by itself after ${stamp(r)}.` : "It carries on by itself once there's room."],
      room && post ? button("Carry on", () => say(th, "Please carry on where you stopped.", st.who), { size: "s" }) : null, { key: "limited" }));
  }
  if (st.key === "failed") out.push(line("failed", [who(st.who), simpleOn() ? " ran into a problem. Send a message and it tries again." : ` stopped with an error ${ago(st.t)}. A message wakes it to try again.`], null, { key: "failed" }));
  // Where it sits (the person, 110601450d55): a helper's conversation names the one it came from, and that one lists its helpers.
  const par = app() && threadParent(th), kids = app() ? threadChildren(th) : [];
  if (par) out.push(line("ended", ["Part of ", h("a", { href: threadHref(par) }, short(threadTitle(par), 60)), simpleOn() ? "" : `: ${who(d0(th))} started it from there`], null, { key: "parent" }));
  if (kids.length) {
    const ks = kids.map(k => ({ k, st: threadState(k, seen) })).sort((a, b) => S.threads.get(b.k).last - S.threads.get(a.k).last), on = ks.filter(x => ["working", "queued", "quiet"].includes(x.st.key)).length, ask = ks.filter(x => NEED.has(x.st.key)).length;
    const links = ks.slice(0, 5).flatMap((x, i) => [i ? ", " : "", h("a", { href: threadHref(x.k), title: status(x.st.key).label || "" }, short(threadTitle(x.k), 40))]);
    out.push(line(on ? "working" : ask ? "asks" : "ended", [plural(kids.length, "helper conversation"), on ? ` (${on} working${ask ? `, ${ask} waiting on you` : ""})` : ask ? ` (${ask} waiting on you)` : "", ": ", ...links, ks.length > 5 ? ` and ${ks.length - 5} more` : ""],
      post ? button(KIDS.has(th) ? "Hide in list" : "Show in list", () => { KIDS.has(th) ? KIDS.delete(th) : KIDS.add(th); L.ctx && L.ctx.update(); }, { size: "s" }) : null, { key: "kids", live: !!on }));
  }
  for (const a of run) {
    if (st.key === "asks" && a.name === st.who) continue;
    const s = agentState(a);
    if (simpleOn()) { if (!out.some(x => x.dataset.key === "run")) out.push(line("working", "Your Hive is working on it.", can("stop") ? stopButton("Stop this work", () => askStop(a.name)) : null, { key: "run", live: true })); continue; }
    // Closed it reads "a7c31 Editing fetch.py"; how long ago it was active shows only when open.
    const doing = a.doing ? a.doing.charAt(0).toUpperCase() + a.doing.slice(1) : AGENT_WORDS[s.key] || s.text;
    out.push(line(s.key, [who(a.name), " ", doing, h("span", { class: "meta cm-line-m" }, " · " + (s.key === "working" ? ago(a.last * 1e3) : s.text))],
      can("stop") ? stopButton(`Stop ${a.name}`, () => askStop(a.name)) : null, { key: "run:" + a.name, live: true }));
  }
  // A second helper when the agent you wrote to is busy (not built): see PART1.md.
  const mine = lastMine(th), wait = mine && now() - mine.t > HELPER_AFTER && now() - mine.t < 2 * HOUR ? mine.men.map(m => delivery(mine, m)).find(x => x && x.key === "waiting") : null;
  if (wait && built("helper") && !simpleOn()) out.push(line("queue", [who(wait.who), ` is busy and hasn't seen your message for ${dur(now() - mine.t)}. If it still hasn't by ${clock(mine.t + 10 * MIN)}, a helper starts on it. `, notBuilt()],
    post ? [button("Start a helper now", async (e, b) => { b.disabled = true; try { await apiPost("/api/helper", { post: mine.id }); poll(); } catch (x) { b.disabled = false; b.title = x.message; } }, { size: "s" })] : null, { key: "helper" }));
  if (!out.length && th) {
    const r = recipient(th);
    out.push(line("done", simpleOn() ? "Your Hive has stopped for now. Send a message when you want more." : r.who && r.who !== "new" ? `Nobody is on this right now. A message wakes ${r.who}.` : "Nobody is on this right now. A message starts an agent on it.", null, { key: "idle" }));
  }
  const more = out.length - 1, open = !C.strip ? false : true;
  // Closed it is one line: the second agent rides on it as a quiet note.
  const k2 = out.length > 1 && !open ? String(out[1].dataset.key) : "", x0 = out[0] && out[0].querySelector(".cm-line-x");
  if (/^run:/.test(k2) && x0) x0.append(h("span", { class: "meta cm-line-2" }, " · and " + k2.slice(4)));
  return h("div", { class: "cm-strip", "data-key": "strip", "data-open": open ? "" : null, role: "status", "aria-live": "polite" }, out,
    more > 0 && !open ? h("span", { class: "meta num cm-strip-n", "data-key": "n" }, "+" + more) : null,
    out.length > 1 || out.some(x => x.querySelector(".cm-line-a") || /^run:/.test(x.dataset.key)) ? stripToggle(open) : null);
}
function stripToggle(open) {
  const b = button(open ? "Show less" : "Show all", () => { C.strip = !open; L.ctx && L.ctx.update(); }, { kind: "icon", size: "s", icon: open ? "chevron-down" : "chevron-up", cls: "cm-strip-t", title: open ? "Show less" : "Show everyone and what you can do" });
  b.setAttribute("aria-expanded", String(open)); return b;
}
function lastQuestion(p) {
  const lines = mdPlain(displayText(p)).split(/\n|(?<=[.!])\s+/).map(s => s.trim()).filter(Boolean);
  return [...lines].reverse().find(s => s.endsWith("?")) || lines.at(-1) || "";
}

// ---- the head of a conversation ----
// One menu, opened by the title: the count, what you can do to the conversation, what shows, and the way out (export, share).
const sep = () => h("div", { class: "cm-sep", role: "separator" });
/** What this conversation's agent runs have cost: its usage files' totals, those that belong to this thread. null while usage hasn't come back. */
function threadCost(th) {
  const U = S.usage; if (!U || !U.files) return null; let t = 0;
  for (const [k, f] of Object.entries(U.files)) if (fileInfo(k).thread === th) t += f.c || 0;
  return t;
}
function titleMenu(c, n, d, off, ctx) {
  const th = c.thread, full = th && app() && !simpleOn(), mine = can("post");
  const act = (label, ic, fn, title, val) => button(val ? [label, h("small", null, val)] : label, () => { C.menu = null; fn(); ctx.update(); }, { size: "s", kind: "quiet", icon: ic, title });
  const link = (label, ic, to) => button(label, null, { href: to, size: "s", kind: "quiet", icon: ic });
  const t = th && tier(tierOf(th)), im = full && important(th), pin = full && pinned(th), arch = full && archived(th), fd = full && faded(th);
  const part = (...xs) => xs.filter(Boolean);
  const own = full ? part(
    mine ? act(im ? "Not important" : "Mark important", "star", () => tryMeta(th, { important: !im }, ctx), im ? "Take the important mark off" : "Keep it above the others in the list") : null,
    mine ? act(pin ? "Unpin" : "Pin to the top", "pin", () => tryMeta(th, { pin: !pin }, ctx), pin ? "Back among the others" : "Keep it at the top of the list, whatever is on") : null,
    mine ? act("Rename", "edit", () => renameDialog(th), "Give it a name of your own. Only the dash shows it.") : null,
    mine ? act(arch ? "Unarchive" : "Archive", "archive", () => { tryMeta(th, { archived: !arch }, ctx); if (!arch) go("comms"); }, arch ? "Back in the list" : "Out of the list until something new is for you") : null,
    mine ? act(fd ? "Needed again" : "Not needed", "check", () => tryMeta(th, { faded: !fd }, ctx), fd ? fadeWhy(th) : "Grey it in the list: still there to open and write in, and back by itself when something new is for you") : null) : [];
  const prio = th && simpleOn() ? null : th && can("tier") ? button([h("span", { class: "ref-sw", "data-tone": t.tone }), "Priority", h("small", null, t.label)], () => { C.menu = null; askTier(th); }, { size: "s", kind: "quiet", title: "Where this sits in the hive's priority list" })
    : th ? h("p", { class: "meta cm-tier", title: "This thread's priority" }, h("span", { class: "ref-sw", "data-tone": t.tone }), "Priority: " + t.label) : null;
  const where = part(
    off > 0 && !simpleOn() ? act(`Show ${off} hidden`, "filter", () => setShow(SHOW.ALL, ctx), "Turn on For you, Updates and Agent comms") : null,
    link("All messages", "chat", href("comms", "~all")),
    full ? link("On the timeline", "clock", href("timeline")) : null,
    prio);
  const out = full ? part(
    act("Share this result", "board", () => shareDialog(th), "Save the conversation as one page you can post: the ask, the agents at work, the result. You see it first and can remove anything."),
    act("Export as Markdown", "copy", () => downloadMarkdown(th), "Download the conversation as a .md file, with simple formatting"),
    act("Export as PDF", "board", () => printScript(th), "Lay it out like a script and save it as a PDF from the print dialog")) : [];
  return h("div", { class: "cm-menu", role: "menu", "data-key": "title" },
    h("p", { class: "meta cm-cost" }, (C.q.trim() ? plural(n, "match", "matches") : plural(n, app() ? "message" : "post")) + (d ? ` \u00b7 since ${stamp(d.first)}` : "") + (full && threadCost(th) != null ? ` \u00b7 ${money(threadCost(th))}` : "")),
    own, own.length ? sep() : null, where, out.length ? sep() : null, out,
    full && th !== threadTitle(th) ? h("p", { class: "meta cm-menu-id" }, "Its id on the board: ", h("code", { title: "Click to select it" }, th), copyButton(th)) : null);   // copy it (the person, 11041333ab1a)
}
/** The rail's links to the pages Chat brings together: each only where this connection may open it, with a count where there is one. Needs you moved to Work's row (2026-11-07). */
function places() {
  const link = (label, to, n, tip) => h("a", { class: "cm-place", href: to, title: tip }, label, n ? h("b", { class: "cm-place-n", "aria-label": n + " new" }, String(n)) : null);
  return h("nav", { class: "cm-places", "aria-label": "Also in Chat", "data-key": "places" },
    can("read") ? link("Inbox", href("inbox"), IB.unread || 0, "Every message agents sent you") : null,
    can("files") ? link("Mail", href("mail"), MN.n, MN.n ? "Mail since you last opened it" : "Your mail") : null,
    link("Files", href("made"), 0, "Files and links from your conversations"));
}
// One button over the list holds every choice about it (the person, 11071400de5b: too many controls everywhere; the hand-off's §4): what shows, how it is grouped,
// whether the not-needed ones are listed, fold and reach. Above the list there are New conversation, search and this. The button carries a dot when a choice is not the default.
const showOff = () => showMask() !== SHOW.ALL || C.imp || C.hideFaded;
function showBtn(ctx) {
  return h("button", { type: "button", class: "btn cm-showbtn", "data-kind": "icon", "data-on": showOff() ? "" : null, "aria-label": "Show and group", title: "What the list shows and how it is grouped", "aria-haspopup": "true", "aria-expanded": String(C.menu === "show"),
    onclick: e => { e.stopPropagation(); C.menu = C.menu === "show" ? null : "show"; ctx.update(); } }, icon("filter"));
}
function showMenu(ctx) {
  const searching = words(C.tq).length > 0;
  return h("div", { class: "cm-menu cm-show-m", role: "menu", "data-key": "showmenu", "aria-label": "Show and group" },
    h("p", { class: "meta cm-show-l" }, "Show"), showChips(ctx),
    app() && !searching ? [h("p", { class: "meta cm-show-l" }, "Group by"), viewBar(ctx)] : null,
    searching ? null : h("div", { class: "cm-scope" }, h("span", null, app() ? "Conversations" : "Threads"),
      h("button", { type: "button", class: "cm-foldall", hidden: foldNone(), title: "Fold or open every section and helper list here", onclick: () => foldAll(ctx) }, foldWord()),
      h("button", { type: "button", class: "cm-scope-b", title: C.scope === "recent" ? (app() ? "Only the last 7 days are listed. Show every conversation." : "Only the last 24 hours are listed. Show every thread.") : "List only the recent ones",
        onclick: () => { C.scope = C.scope === "recent" ? "all" : "recent"; ctx.update(); } }, C.scope === "recent" ? "Show all" : "Recent only")));
}
// What shows: For you, Updates, Agent comms, each on or off: three toggles, in the Show menu since 2026-11-07 (first over the list: the person, 11040113eacf).
const tmetaSig = () => Object.entries(S.threadMeta || {}).map(([k, v]) => k + (v.important || 0) + (v.pin || 0) + (v.faded || 0)).join();
function showChips(ctx) {
  const mask = showMask();
  const group = h("div", { class: "tm-show", role: "group", "aria-label": "Show", title: showSays(mask) },
    SHOW_PARTS.map(([b, l, t]) => h("button", { type: "button", class: "tm-chip", "data-show": KIND_NAME[b], "aria-pressed": String(!!(mask & b)), title: (mask & b ? "Hide " : "Show ") + t,
      onclick: () => setShow(showMask() ^ b, ctx) }, h("i", { "aria-hidden": "true" }), l)),
    mask ? null : h("p", { class: "meta tm-show-says" }, showSays(mask)));
  return app() ? h("div", { class: "tm-showwrap" }, group, app() ? h("button", { type: "button", class: "tm-chip tm-chip-imp", "aria-pressed": String(!!C.imp), title: C.imp ? "Show every conversation again" : "Only conversations marked important", "aria-label": "Important only",
      onclick: () => { C.imp = !C.imp; ctx.update(); } }, icon("star")) : null) : group;
}
// Over the list: Topics or Recent, and whether the conversations marked Likely not needed are listed at all.
function viewBar(ctx) {
  const nFaded = [...S.threads.keys()].filter(th => faded(th) && !archived(th)).length;
  return h("div", { class: "cm-views", "data-key": "views" },
    seg([["ideas", "Idea", "The main conversations, newest activity first, helper conversations folded under the one they came from"],
      ["topics", "Group", "Under the sections of the Needs-you overview, the rest under Other, helper conversations folded under the one they came from"],
      ["recent", "None", "Every conversation in one list, newest activity first, helper conversations included, each naming the one it came from"]], C.view,
      v => { C.view = v; store(VIEW_KEY, v); ctx.update(); }, { label: "How the list is arranged" }),
    h("button", { type: "button", class: "tm-chip cm-hidefaded", "aria-pressed": String(C.hideFaded),
      title: C.hideFaded ? `Show the ${nFaded} marked Likely not needed again` : `Hide the ${nFaded} conversations marked Likely not needed`,
      onclick: () => { C.hideFaded = !C.hideFaded; store(HIDE_KEY, C.hideFaded ? "1" : "0"); ctx.update(); } }, h("i", { "aria-hidden": "true" }), "Hide not needed"));
}
function head(c, ctx) {
  if (c.thread && app()) import("../ui/preview.js").then(m => m.autoFiles(c.thread));
  const d = c.thread && S.threads.get(c.thread), n = L.matched.length, off = maskFor(c) !== SHOW.ALL && !C.q.trim() ? L.src.length - n : 0;
  const title = c.thread ? threadTitle(c.thread) : c.agent ? (c.agent === "person" ? "Everything you sent" : "Everything by or to " + c.agent) : c.pair ? `${c.pair[0]} and ${c.pair[1]}` : app() ? "All messages" : "Everything";
  const people = simpleOn() || !c.thread ? [] : threadPeople(c.thread).filter(m => m !== "person");
  const who = simpleOn() || c.thread ? [] : c.agent ? [...new Set(S.posts.filter(p => p.from === c.agent).map(p => p.th))].slice(-14).map(t => threadRef(t)) : [];
  const find = C.find || !!C.q.trim();
  return h("header", { class: "cm-head", "data-key": "head", "data-find": find ? "" : null },
    h("div", { class: "cm-tt", "data-dev": app() ? null : "" },
      h("a", { class: "btn cm-back", href: href("comms"), "data-kind": "icon", "aria-label": app() ? "Chats" : "Threads", title: app() ? "Chats" : "Threads" }, icon("chevron-left")),
      h("button", { type: "button", class: "cm-title-b", "data-hid": off > 0 && !simpleOn() ? "" : null, title: off > 0 && !simpleOn() ? `${off} hidden: open for the way to show them` : null, "aria-haspopup": "true", "aria-expanded": String(C.menu === "title"), onclick: e => { e.stopPropagation(); C.menu = C.menu === "title" ? null : "title"; ctx.update(); } },
        h("h1", { class: "cm-title" }, title), icon("chevron-down")),
      h("span", { class: "meta cm-count" }, (C.q.trim() ? plural(n, "match", "matches") : plural(n, app() ? "message" : "post")) + (d ? ` · since ${stamp(d.first)}` : "")),
      c.thread && !app() && title !== c.thread ? h("span", { class: "cm-id" }, h("code", { class: "meta", title: "Its id on the board" }, c.thread), copyButton(c.thread)) : null),
    h("div", { class: "cm-tools" },
      people.length ? avatars(people) : null,
      d?.posts.some(p => p.text.length > 2400) ? button("Contents", () => import("../rich/rail.js").then(m => m.openRail()), { kind: "icon", icon: "bands", cls: "rc-railbtn", title: "Contents: what's in this thread" }) : null,
      input({ type: "search", class: "input cm-find", placeholder: c.thread ? "Search here" : "Search messages", "aria-label": "Search these messages", value: C.q, oninput: e => { C.q = e.target.value; clearTimeout(qTimer); qTimer = setTimeout(ctx.update, 120); }, onkeydown: e => { if (e.key === "Escape") { e.stopPropagation(); C.q = ""; C.find = false; e.target.blur(); ctx.update(); } } }),
      h("button", { type: "button", class: "btn", "data-kind": "icon", "aria-label": "Search here", title: "Search here", "aria-pressed": String(find),
        onclick: () => { C.find = !find; if (!C.find) C.q = ""; ctx.update(); if (C.find) queueMicrotask(() => document.querySelector(".cm-find")?.focus()); } }, icon("search")),
      c.thread ? button("Files", () => import("../ui/preview.js").then(m => m.openFiles()), { kind: "icon", icon: "folder", cls: "cm-files", title: "Files: this conversation's and the projects'" }) : null,
      c.thread ? asideBtn(c.thread, ctx) : null),
    C.menu === "title" ? titleMenu(c, n, d, off, ctx) : null,
    C.menu === "asides" && c.thread ? asideMenu(c.thread) : null,
    who.length ? h("div", { class: "cm-people" }, who) : null);
}
// /btw side questions (the person, 11040224589b): a small button in the head's tools, like the others.
// From the main conversation it opens a menu with a row per side question; from a side one, it goes back.
const sideKids = th => [...S.threads.keys()].filter(t => btwParent(t) === th);
function asideBtn(th, ctx) {
  const up = btwParent(th);
  if (up) return button("Back to " + short(threadTitle(up), 28), null, { href: threadHref(up), size: "s", kind: "quiet", icon: "chevron-left", cls: "cm-aside-b", title: "This is a side question about " + threadTitle(up) });
  const kids = sideKids(th);
  return kids.length ? h("button", { type: "button", class: "btn cm-aside-b", "data-size": "s", "data-kind": "quiet", "aria-haspopup": "true", "aria-expanded": String(C.menu === "asides"), title: "Side questions you asked with /btw here",
    onclick: e => { e.stopPropagation(); C.menu = C.menu === "asides" ? null : "asides"; ctx.update(); } }, h("span", null, "Side", h("span", { class: "cm-aside-w" }, " questions"), ` (${kids.length})`)) : null;
}
function asideMenu(th) {
  return h("div", { class: "cm-menu cm-aside-m", role: "menu", "data-key": "asides" },
    h("p", { class: "meta" }, "Side questions"),
    sideKids(th).reverse().map(t => { const d = S.threads.get(t), last = d && d.posts.at(-1);
      return h("a", { href: threadHref(t), class: "cm-aside-i", role: "menuitem", "data-key": t, title: threadTitle(t).replace(/^Aside: /, "") },
        h("span", { class: "cm-aside-q" }, threadTitle(t).replace(/^Aside: /, "")), last ? h("span", { class: "meta" }, ago(last.t)) : null); }));
}

// ---- home: start something, and what needs you ----
function needs() {
  const g = { asks: [], foryou: [], trouble: [], working: [] };
  for (const d of S.threads.values()) {
    if (isSystemThread(d) || !theirs(d)) continue;
    const st = threadState(d.name, seen);
    if (st.key === "asks") g.asks.push({ d, st });
    else if (st.key === "foryou") g.foryou.push({ d, st });
    else if (st.key === "limited" || st.key === "failed" || st.key === "stuck") g.trouble.push({ d, st });
    else if (st.key === "working" || st.key === "quiet" || st.key === "queue" || st.key === "queued") g.working.push({ d, st });
  }
  for (const k in g) g[k].sort((a, b) => b.d.last - a.d.last);
  return g;
}
function needRow({ d, st }) {
  const lp = d.posts[d.posts.length - 1], plain = simpleOn(), name = plainName(st.who);
  const text = st.key === "asks" ? [h("b", null, name + " asks: "), lastQuestion(st.post)]
    : st.key === "foryou" ? [h("b", null, name + ": "), firstSentence(displayText(st.post))]
    : plain && st.key === "limited" ? [h("b", null, "Your Hive is taking a break. "), "It carries on by itself."]
    : plain && st.key === "failed" ? [h("b", null, "Something went wrong. "), "Send a message and it tries again."]
    : plain ? [h("b", null, "Your Hive is working on it.")]
    : st.key === "limited" ? [h("b", null, `${st.who} stopped at a usage limit ${ago(st.t)}. `), "Where it got to: " + firstSentence(displayText(lp))]
    : st.key === "failed" ? [h("b", null, `${st.who} stopped with an error ${ago(st.t)}.`)]
    : [h("b", null, st.who + ": "), (S.running.get(st.who) || {}).doing || st.label];
  return { key: d.name, href: threadHref(d.name), title: threadTitle(d.name), meta: ago(st.post ? st.post.t : d.last), lead: statusDot(st.key, { live: isRunning(st.who) }), text };
}
// "Where things stand": the answer to "what was done, what needs me" without asking in a new chat and losing the answer.
// Read from agents' status bars (S.threadStatus) and the 30-minute file behind the Needs you page, whose brief is an agent's
// hourly top-down view: the few top-level things, each opening onto its threads. No brief (an installed Hive), just the threads.
// The agent only groups and words the top level: a status line written after it wins at once, here and in which group the thread sits.
const BRIEF_FOR = 6 * HOUR, STAND_LIMIT = 4, TOP_LIMIT = { needs: 12, done: 4, going: 4 }, HOME_LIMIT = 5, STAND_EVERY = 5 * MIN, DONE_FOR = 2 * DAY, GOING_FOR = 3 * DAY, NEEDS_FOR = 7 * DAY;
const STAND = { needs: null, at: 0, more: {}, open: {}, checks: [] };   // checks: the checklist under each Needs you entry (server: /api/checklist)
/** Tick or untick one checklist item. It stays ticked until an agent has checked it was done and removes it. */
function tick(it, ticked, ctx, from) {
  const at = scrollersOf(from), upd = () => { ctx.update(); keepScroll(at); };
  it.ticked = ticked; upd();
  apiPost("/api/checklist", { id: it.id, ticked }).then(r => { if (r && r.items) { STAND.checks = r.items; upd(); } }, () => { it.ticked = !ticked; upd(); });
}
/** The checklist items that belong under one Needs you entry: those on one of its threads, or naming it. */
const checksFor = r => { const ths = new Set((r.kids || []).map(k => k.th).concat(r.th ? [r.th] : [])); return STAND.checks.filter(c => (c.cat && c.cat === r.title) || (!c.cat && ths.has(c.thread)) || (c.cat && !r.title && ths.has(c.thread))); };
/** Run a re-render without the page moving: every scroller around the row (and the window) goes back to where it was. */
function holdScroll(from, fn) {
  const at = scrollersOf(from);
  fn(); keepScroll(at);
}
/** Every scroller around a node (and the window) with where it is now, to put back after a re-render. */
function scrollersOf(from) {
  const at = [document.scrollingElement]; for (let n = from && from.parentElement; n; n = n.parentElement) if (n.scrollTop) at.push(n);
  return at.map(el => [el, el.scrollTop]);
}
function keepScroll(at) {
  const put = () => at.forEach(([el, y]) => { if (el.isConnected && el.scrollTop !== y) el.scrollTop = y; });
  put(); queueMicrotask(put); requestAnimationFrame(put);
}
function loadStanding(ctx) {
  if (now() - STAND.at < STAND_EVERY) return;
  STAND.at = now();
  jget("/api/checklist").then(v => { const n = (v && v.items) || []; if (JSON.stringify(n) !== JSON.stringify(STAND.checks)) { STAND.checks = n; ctx.update(); } }, () => {});
  jget("/api/needs-you").then(v => { const sig = n => JSON.stringify(n && [n.waiting, n.states, n.brief]); if (v && sig(v) !== sig(STAND.needs)) { STAND.needs = v; ctx.update(); } }, () => {});
}
/** A status bar's last line, split into its state word and the words after it, without the agent's name tag. */
function standLine(text) {
  const l = text.split("\n").filter(Boolean), last = (l[l.length - 1] || "").replace(/\s*\(a[0-9a-f]{4,5}\)/g, "").trim();
  const m = last.match(/^(Done|Blocked|Needs the person|Waiting|Told to stop):\s*/i);
  return { word: m ? m[1].toLowerCase() : "", line: m ? last.slice(m[0].length) : last, full: last };
}
// A thread that went quiet is archived by age, but what it waits on still counts here; one archived by hand doesn't.
const shownHere = th => { const d = S.threads.get(th); return !(btwParent(th) || faded(th) || (tmeta(th).archived && archived(th)) || (d && isSystemThread(d))); };
const askedByThem = th => { const d = S.threads.get(th); return important(th) || pinned(th) || (!!d && d.posts.some(p => p.from === "person")); };
function standing(g) {
  const seenAbove = new Set([...g.asks, ...g.foryou, ...g.trouble, ...g.working].map(x => x.d.name)), used = new Set();
  const rows = { needs: [], done: [], going: [], projects: [] };
  const take = (group, th, text, t) => { if (used.has(th)) return; used.add(th); rows[group].push({ th, text, t }); };
  const st = S.threadStatus || {};
  const drop = (STAND.needs && STAND.needs.brief && STAND.needs.brief.dropped) || {};   // an agent took it out of the summary as obsolete, until the thread says something newer
  const open = th => !seenAbove.has(th) && shownHere(th) && !(drop[th] && drop[th] >= ((st[th] && st[th].at) || 0));
  const bars = Object.keys(st).map(th => ({ th, r: st[th], s: standLine(st[th].text), t: st[th].at * 1e3 })).sort((a, b) => b.t - a.t);
  const b0 = STAND.needs && STAND.needs.brief, bt = b0 ? Date.parse(b0.at) : 0, brief = b0 && now() - bt < BRIEF_FOR ? b0 : null;
  const waits = ((STAND.needs && STAND.needs.waiting) || []).filter(w => !w.stale && w.thread && open(w.thread)).map(w => ({ th: w.thread, text: w.action, t: w.at ? Date.parse(w.at) : 0 }));
  for (const b of bars) if (b.s.word === "needs the person" && now() - b.t < NEEDS_FOR && open(b.th)) waits.push({ th: b.th, text: b.s.line, t: b.t });
  waits.sort((a, b) => b.t - a.t);
  const waiting = waits.filter(w => { const mine = lastMine(w.th); return !mine || mine.t < w.t; });   // once they've answered since, it's not waiting on them
  if (brief) {   // threads first, so a thread stays under its top-level thing; live state decides what's still in each
    const when = th => st[th] ? st[th].at * 1e3 : bt, latest = new Map(); for (const w of waiting) if (!latest.has(w.th)) latest.set(w.th, w);   // newest first
    // What a thread says since the brief, or null. A newer Done doesn't take it out of Needs you: a For you line still waits until they answer it.
    const since = (group, th) => {
      if (group === "needs") { const w = latest.get(th); return w && w.t > bt ? { text: w.text, t: w.t } : null; }
      const s = st[th] && st[th].at * 1e3 > bt ? standLine(st[th].text) : null;
      return s && { group: s.word === "done" ? "done" : s.word === "told to stop" || s.word === "needs the person" ? "" : "going", text: s.word === "done" ? s.line : s.full, t: st[th].at * 1e3 };
    };
    for (const group of ["needs", "done", "going"]) for (const top of brief[group] || []) {
      const kids = (top.items || []).filter(r => {
        if (group === "needs") return latest.has(r.thread);
        const s = since(group, r.thread);   // moved to another group: the bars below place it there
        return open(r.thread) && !used.has(r.thread) && (!s || s.group === group);
      });
      if (!kids.length) continue;
      rows[group].push({ top: group + ":" + top.title, title: top.title, text: top.text, t: bt, kids: kids.map(r => { used.add(r.thread); const s = since(group, r.thread); return { th: r.thread, text: s ? s.text : r.text, t: s ? s.t : when(r.thread), fresh: !!s }; }) });
    }
    if (brief.headline) { rows.headline = brief.headline; rows.at = bt; }
  }
  for (const w of waiting) take("needs", w.th, w.text, w.t);
  for (const b of bars) {
    if (!open(b.th) || !askedByThem(b.th)) continue;
    if (b.s.word === "done" && now() - b.t < DONE_FOR) take("done", b.th, b.s.line, b.t);
    else if (b.s.word !== "done" && b.s.word !== "told to stop" && b.s.word !== "needs the person" && now() - b.t < GOING_FOR) take("going", b.th, b.s.full, b.t);
  }
  if (brief) for (const [group, title] of [["needs", "New since this summary"], ["done", "Also done"], ["going", "Also going"]]) {   // nothing that isn't in the brief vanishes; it just doesn't take over the view
    const loose = rows[group].filter(r => !r.kids);
    if (loose.length > 1) { const last = loose.reduce((a, b) => b.t > a.t ? b : a); rows[group] = [...rows[group].filter(r => r.kids), { top: group + ":" + title, title, text: plural(loose.length, "thread") + " not in the summary. Latest: " + last.text, t: last.t, kids: loose }]; }
  }
  rows.projects = ((STAND.needs && STAND.needs.states) || []).filter(s => s.state).map(s => ({ th: s.thread, name: s.name, key: s.key, text: s.state, t: s.at ? Date.parse(s.at) : 0 }));
  return rows;
}
/** Adds the row to the new chat's prompt, with the thread's id so the agent that gets it can read that thread. */
// A status bar's first line says what the thread is about, which beats its first post ("@fable") unless the person named it.
const standTitle = r => { if (r.name) return r.name; if (r.title) return r.title; const own = tmeta(r.th).title, bar = S.threadStatus && S.threadStatus[r.th], l = bar && bar.text.split("\n"); return own || (l && l.length > 1 && l[0].replace(/\s*\(a[0-9a-f]{4,5}\)/g, "")) || threadTitle(r.th); };
const standReply = r => {
  const ids = r.kids ? "\n> Threads: " + r.kids.map(k => k.th).join(", ") : "";
  const q = "> " + standTitle(r) + "\n> " + r.text.split("\n").join("\n> ") + ids + "\n\n";
  const d = draft(null);
  if (!d.text.includes(q.trim())) quoteInto(null, q); else focusComposer();
};
function standRow(r, more) {
  return { key: r.key || r.th, href: r.th ? threadHref(r.th) : "#needs", title: standTitle(r), meta: r.t ? ago(r.t) : null, text: mdPlain(r.text).replace(/^[a-z]/, c => c.toUpperCase()), ...more,
    acts: can("post") ? h("button", { type: "button", class: "lr-act", title: "Add to your new message", "aria-label": "Add this to your new message", onclick: () => standReply(r) }, icon("reply")) : null };
}
// A top-level thing opens onto its threads; with only one, the row just goes there.
function checkRow(c, ctx) {
  const box = h("input", { type: "checkbox", class: "cm-tick", checked: c.ticked || null, disabled: can("post") ? null : true, "aria-label": c.text, onchange: e => tick(c, e.target.checked, ctx, e.target) });
  return { key: "ck:" + c.id, href: null, lead: box, title: c.text, whole: true, dim: c.ticked, depth: 1 };
}
function standRows(r, ctx, group) {
  const cks = group === "needs" ? checksFor(r) : [];
  if (!r.kids && !cks.length) return [standRow(r)];
  if (r.kids && r.kids.length === 1 && !cks.length) return [standRow({ ...r, key: r.top, th: r.kids[0].th, text: r.kids[0].fresh ? r.kids[0].text : r.text }, { whole: true, meta: r.kids[0].t ? ago(r.kids[0].t) : null })];
  const kids = r.kids || [], open = !!STAND.open[r.top], fresh = kids.filter(k => k.fresh).length, done = cks.filter(c => c.ticked).length;
  const meta = [cks.length ? `${done}/${cks.length} ticked` : null, kids.length ? plural(kids.length, "thread") + (fresh ? `, ${fresh} updated` : "") : null].filter(Boolean).join(" · ");
  return [standRow({ ...r, key: r.top }, { href: null, meta, whole: true, expanded: open, lead: h("span", { class: "cm-stand-ch", "data-open": open ? "" : null }, icon("chevron-down")), onClick: (e, n) => holdScroll(n, () => { STAND.open[r.top] = !open; ctx.update(); }) }),
    ...(open ? [...cks.map(c => checkRow(c, ctx)), ...kids.map(k => standRow({ ...k, key: r.top + "/" + k.th }, { depth: 1 }))] : [])];
}
function standSec(id, title, rows, ctx) {
  if (!rows.length) return null;
  // The limit counts what the section lists, so opening one doesn't push the next behind Show more.
  const most = STAND.more[id] ? 0 : TOP_LIMIT[id] && rows[0].kids ? TOP_LIMIT[id] : STAND_LIMIT, flat = rows.map(r => standRows(r, ctx, id));
  return h("section", { class: "sec", "data-key": "st:" + id, "data-stand": id }, h("header", { class: "sec-h" }, h("h3", { class: "sec-t" }, title)),
    list(flat.flat(), { limit: most && rows.length > most ? flat.slice(0, most).flat().length : 0, moreText: () => `Show ${rows.length - most} more`, onMore: () => { STAND.more[id] = true; ctx.update(); } }));
}
function standingPart(r, ctx) {
  if (!r.needs.length && !r.done.length && !r.going.length && !r.projects.length) return null;
  return h("div", { class: "cm-stand", "data-key": "stand" },
    h("div", { class: "cm-stand-hd" }, h("h2", { class: "cm-needs-t" }, "Where things stand"), h("a", { class: "cm-stand-by", href: href("work"), title: "Every project on one page: what it did today, where it stands, what comes next" }, "By project"),
      r.at ? h("span", { class: "cm-stand-at", title: "When the headline and group wording were last written. The lists and agents' status lines under them are live." }, "Summary updated " + ago(r.at)) : null),
    r.headline ? h("p", { class: "cm-stand-brief" }, mdPlain(r.headline)) : null,
    standSec("needs", "Needs you", r.needs, ctx),
    standSec("done", "Done lately", r.done, ctx),
    standSec("going", "Still going", r.going, ctx),
    standSec("projects", "Your projects", r.projects, ctx));
}
function homePane(ctx) {
  const g = needs(), w = can("post"), n = g.asks.length + g.foryou.length + g.trouble.length;
  const local = outFor(null);
  const room = hasRoom(), digest = app() && !simpleOn();
  if (digest) loadStanding(ctx);
  const stand = digest ? standing(g) : null;
  // Only on arriving: taking focus on every render pulled the page back to the box after any real click elsewhere on it.
  const arrived = C.arrived; C.arrived = false;
  // Long lists fold here, so what's under them stays in reach (the side list always has them all).
  const sec = (title, note, items) => items.length ? h("section", { class: "sec", "data-key": "s:" + title }, title ? h("header", { class: "sec-h" }, h("h2", { class: "sec-t" }, title), note ? h("span", { class: "sec-n" }, note) : null) : null,
    list(items.map(needRow), { limit: digest && !STAND.more[title] ? HOME_LIMIT : 0, onMore: () => { STAND.more[title] = true; ctx.update(); } })) : null;
  return h("div", { class: "cm-home", "data-key": "home" },
    awayCard(ctx),
    w ? h("div", { class: "cm-new", "data-key": "new" },
      h("h1", { class: "cm-home-t" }, "What do you want done?"),
      h("p", { class: "cm-home-l" }, simpleOn() ? "Say it the way you'd tell a person. Your Hive starts on it and answers here." : "Say it the way you'd tell a person. An agent starts on it and answers here; it may bring in helpers if the job has parts."),
      !room && !hiveStopped() ? h("p", { class: "cm-home-warn", "data-tone": "limited", role: "status" }, statusDot("limited"), simpleOn() ? "Your Hive has used up its allowance for now, so new work waits until it comes back." : (((S.app && S.app.setup && S.app.setup.logins) || []).some(l => l.signed_in) ? "Every Claude account is past its usage line, so new work waits until there's room. See Usage." : "No Claude account is signed in yet. Sign in under Settings > Accounts so new work can start.")) : null,
      composer({ thread: null, update: ctx.update, place: "home", autofocus: arrived && conversation(ctx.arg).open }),
      !n && !local.length && ![...S.threads.values()].some(d => d.posts.some(p => p.from === "person")) ? firstJobs() : null,
      local.some(it => it.state !== "failed") ? closeLine() : null,
      local.length ? h("div", { class: "cm-out", "data-key": "out" }, local.map(it => localMessage(it, outActions))) : null) : null,
    h("div", { class: "cm-needs", "data-key": "needs" },
      n || (!simpleOn() && !S.live.signin?.length && !(stand && stand.needs.length)) ? h("h2", { class: "cm-needs-t" }, n ? `${plural(n, "thing")} for you` : IB.unread ? "No agent is waiting on you" : "Nothing needs you") : null,
      sec(null, null, [...g.asks, ...g.foryou, ...g.trouble]),   // one head above ("N things for you"); each row's shape and first words say which kind
      stand ? standingPart(stand, ctx) : null,
      sec("Working now", null, g.working),
      !n && !g.working.length && !S.live.signin?.length ? h("p", { class: "meta" }, simpleOn() ? "When your Hive asks you something or finishes, it shows here." : "When an agent asks you something, finishes, or stops, it shows here.") : null));
}

// Over the whole page, not in the list: it sets what you see of the hive's talk, both in a conversation and in which ones are listed. Each one turns on and off by itself.
const SHOW_PARTS = [[SHOW.you, "For you", "your messages, what agents ask or tell you, and each job's result"],
  [SHOW.updates, "Updates", "agents' progress updates"],
  [SHOW.comms, "Agent comms", "agent comms: agents talking to each other and the hive's own notices"]];
function showSays(mask) {
  if (words(C.tq).length || words(C.q).length) return "Search looks through everything, whatever is on here.";
  if (!mask) return "Nothing is on, so no messages show. Turn one on.";
  const on = SHOW_PARTS.filter(([b]) => mask & b).map(x => x[2]), last = on.pop();
  return (on.length ? on.join(", ") + " and " + last : last).replace(/^./, c => c.toUpperCase()) + (mask === SHOW.ALL ? "." : ". The rest runs in the background.");
}

export default {
  id: "comms", label: () => (app() ? "Chat" : "Comms"),
  tab: true,
  badge: () => { if (!app() || !S.live) return null; const g = needs(), n = g.asks.length + g.foryou.length + g.trouble.length; return n ? String(n) : null; },
  poll: { every: 3000, run: async ctx => { if (await pollDelivered()) ctx.update(); } },
  escape: () => { if (C.menu || C.find) { C.menu = null; if (!C.q.trim()) C.find = false; L.ctx && L.ctx.update(); return; } if (!cancelReply() && asideOpen()) closeAside(); },
  enter: () => { C.arrived = true; },
  leave: () => { clearTimeout(qTimer); C.menu = null; C.find = false; },
  render(ctx) {
    L.ctx = ctx;
    if (!S.live) return h("div", { class: "page" }, S.failed ? empty("Couldn't read the conversations. It keeps trying.", { kind: "error" }) : empty("Reading…", { kind: "loading" }));
    const c = conversation(ctx.arg);
    if (C.conv !== c.arg) { Object.assign(C, { conv: c.arg, q: C.next || "", next: null, limit: FIRST, menu: null, find: false }); }
    L.opts = { showThread: !c.thread, mark: words(C.q) };
    refresh(c);
    queueMicrotask(() => sync(c, ctx));
    if (c.thread && app()) { const un = unreadIn(c.thread), sig = c.thread + un.map(x => x.id).join(); if (un.length && sig !== L.marked && !document.hidden) { L.marked = sig; markRead({ ids: un.map(x => x.id) }); } }
    const top = document.getElementById("top"), post = S.app.app && can("post");
    const convo = c.home ? homePane(ctx)
      : [head(c, ctx),
        statusBar(c.thread),
        // Keyed by conversation: another one is a new element, so it opens at its end. Wrapped so that swap doesn't move the composer, which would drop focus.
        h("div", { class: "cm-mw", "data-key": "mw" },
          h("div", { class: "cm-msgs", "data-key": "m:" + (c.arg || ""), "data-own": "", "data-sig": "own", role: "log", "aria-live": "off", "aria-label": "Messages", tabindex: "0" })),
        c.thread && app() && !(post && c.thread) ? strip(c.thread) : null,
        post && c.thread ? asidePanel(c.thread, ctx.update) : null,
        c.thread && app() ? openCard(c.thread, { jump: jumpTo, answer: p => replyTo(p), update: ctx.update, atEnd: !L.box || L.box.dataset.key !== "m:" + (c.arg || "") || L.stuck }) : null,   // what's open for the person, so they needn't read back (110401090fd2)
        c.thread && !can("post") ? readOnly(c.thread) : null,   // §5b: where this address can't post, say so in the composer's place
        post && c.thread ? composer({ thread: c.thread, update: ctx.update, top: app() ? strip(c.thread) : null }) : null,
        post && c.all ? h("p", { class: "cm-hint meta", "data-key": "hint" }, "To start something new, use New conversation.") : null];
    return h("div", { class: "cm", "data-key": "comms", "data-in": c.arg ? "" : null, "data-home": c.home ? "" : null, style: `--top-h:${top ? top.offsetHeight : 47}px;--cm-w:${sideW}px` },
      h("nav", { class: "cm-side", "aria-label": app() ? "Conversations" : "Threads" }, grip(),
        h("div", { class: "cm-filters" },
          post ? button("New conversation", null, { href: href("comms", "~new"), kind: "quiet", icon: "new", cls: "cm-newbtn" }) : null,
          h("div", { class: "cm-srow" },
            h("div", { class: "cm-search" }, icon("search"),
              input({ type: "search", placeholder: app() ? "Search, or @ to ask" : "Search threads", "aria-label": "Search all conversations by name or content. Start with @ to ask an agent, which answers here and brings up the conversations that help", value: C.tq, oninput: e => { C.tq = e.target.value; ctx.update(); }, onkeydown: e => { if (e.key === "Enter" && aiQ()) { e.preventDefault(); aiAsk(ctx); return; } if (e.key === "Escape") { e.stopPropagation(); C.tq = ""; e.target.value = ""; e.target.blur(); ctx.update(); } } })),
            simpleOn() ? null : showBtn(ctx)),
          simpleOn() || C.menu !== "show" ? null : showMenu(ctx),
          app() ? brought() : null,
          C.err ? h("p", { class: "fmsg", "data-tone": "bad", role: "alert" }, C.err) : null),
        h("div", { class: "cm-tl", "data-own": "", "data-sig": "own", tabindex: "-1" }),
        app() && !simpleOn() ? places() : null),
      h("section", { class: "cm-convo", "aria-label": "Conversation", ...(post && c.thread ? dropZone(c.thread) : {}) },
        convo,
        post && c.thread ? h("div", { class: "cm-drop", "aria-hidden": "true", "data-key": "drop" }, "Drop files to attach them") : null));
  },
};

/** The read-only footer: a lock, one sentence, and (in the developer look, which has no strip) the thread's live agents inline, no actions. */
function readOnly(th) {
  const run = app() ? [] : (S.live ? S.live.agents : []).filter(a => a.thread === th && a.alive);
  return h("div", { class: "cm-ro", "data-key": "ro" }, icon("lock"), h("span", null, "Read-only here. This address can't post."),
    run.length ? h("span", { class: "cm-ro-a" }, run.map(a => h("span", { class: "cm-ro-l", "data-key": a.name, title: a.doing || null }, statusDot(agentState(a).key), agentRef(a.name), a.doing ? h("span", { class: "meta" }, short(a.doing, 48)) : null))) : null);
}

// A header menu (title, More) closes on a press anywhere outside it and its button (the person, 2026-11-04, post 11040100216b).
// So does an empty "Search here" box (11040131d9c5); with words in it, it stays, since it's what the messages are filtered by.
if (typeof document !== "undefined") document.addEventListener("pointerdown", e => {
  const menu = C.menu && !e.target.closest?.(".cm-menu, .cm-head [aria-haspopup], .cm-showbtn");
  const find = C.find && !C.q.trim() && !e.target.closest?.(".cm-find, .cm-tools [aria-label=\"Search here\"]");
  if (!menu && !find) return;
  if (menu) C.menu = null; if (find) C.find = false;
  L.ctx && L.ctx.update();
}, true);
