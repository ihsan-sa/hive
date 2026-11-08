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
import { loadNeedsYou, needsN, needsSplit, needsHref, waitsOn, waitingOn, waitingNow, waitingOlder } from "../core/needs.js";
import { loadFoci, fociFailed, fociOrder, focusOf, focusById, activeThreads, askedOf, needAt, agentsOf, stateOf, pills, plainPills, needSource, isCode, scrub, codePill, railSay, threadLine, focusName, MISC, activityOf, shareOf, fractionWords, pct, usd, needsOf, moveFocus, foci } from "../core/foci.js";
import { newIn } from "./focus.js";
import { SIDE_THREAD, agentState, agentStatus, btwParent, threadParent, threadChildren, isAgent, isRunning, tierOf, threadTitle, threadState, threadPeople, isSystemThread, SHOW, postKind, threadKinds, closingPosts, displayText, postSource, inUse, fresh7, asksQuestion, fileInfo } from "../core/model.js";
import { ago, dayName, stamp, clock, dur, short, mdPlain, now, plural, DAY, MIN, HOUR, money } from "../core/format.js";
import { href, threadHref, go, parse } from "../core/router.js";
import { yoursSearch, yoursText } from "../core/yours.js";
import { tier, status } from "../core/status.js";
import { askTier, askStop, stopButton } from "../actions.js";
import { seen, unreadIn, markRead, onInbox, IB } from "../core/inbox.js";
import { MN, onMail } from "../core/mailnew.js";
import { button, empty, icon, input, list, scrim, seg, statusDot, statusWord } from "../ui/kit.js";
import { agentRef, avatars, copyButton, threadRef } from "../ui/refs.js";
import { message, messageHead, messageReceipt, localMessage, markWords, pollDelivered, marksSig, delivery, onJump, helperFolds } from "../ui/message.js";
import { asidePanel, asideOpen, closeAside } from "../ui/aside.js";
import { openQuick, quickOpen, closeQuick, quickPanel, laterButtons } from "../ui/quick.js";
import { when } from "../ui/when.js";
import { downloadMarkdown, printScript } from "../ui/export.js";
import { shareDialog } from "../ui/share.js";
import { pinned, important, faded, meta as tmeta, archived, setMeta, renameDialog, openCard, markSeen, threadCrumb, titleExtras } from "../ui/threadmeta.js";
import { callFor, hangUp } from "../ui/phone.js";
import { brought } from "../ui/brought.js";
import { firstJobs, closeLine } from "../ui/firstjobs.js";
import { hiveStopped } from "../ui/shell.js";
import { composer, interruptButton, cancelReply, replyTo, say, outFor, outSig, actions as outActions, dropZone, recipient, hasDraft, quoteRef, prefill } from "../ui/composer.js";

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
// The side list's two views (the person, 11071518 2bab): "focus" (the default) puts each conversation under its focus, in the order of the
// home cards, Other last, helpers folded under the one they came from; "recent" is every conversation in one flat list,
// newest activity first, helpers included. hideFaded leaves out the ones marked Likely not needed. Both kept in this browser.
const VIEW_KEY = "hive.comms.view", HIDE_KEY = "hive.comms.hidefaded";
const store = (k, v) => { try { localStorage.setItem(k, v); } catch (e) { /* this page only */ } };
const C = { view: (() => { try { const v = localStorage.getItem(VIEW_KEY); return v === "recent" ? v : "focus"; } catch (e) { return "focus"; } })(),
  hideFaded: (() => { try { return localStorage.getItem(HIDE_KEY) !== "0"; } catch (e) { return true; } })(),   // on unless the person turned it off (2026-11-08)
  err: "", imp: false, conv: undefined, q: "", show: null, limit: FIRST, tq: "", scope: "recent", next: null, menu: null, find: false, strip: false };
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
  if (arg === "~list") return { arg, home: true, list: true };
  if (arg === "~archived") return { arg, home: true, archived: true };   // the archived conversations, each with Bring back   // R14: on a phone, the rail as a page of its own (search, Show, By focus / By recency, + per focus); elsewhere the home
  if (arg[0] !== "@") return { arg, thread: arg };
  const names = arg.slice(1).split("+");
  return names.length === 2 ? { arg, pair: names } : { arg, agent: names[0] };
}

const words = s => s.toLowerCase().split(/\s+/).filter(Boolean);
/** What a search looks through in a post: its text, author and attachment names. */
const hay = p => (p.lc ??= [p.text, p.from, ...(p.files || []).map(f => f.name)].join(" ").toLowerCase());
/** A word of a search found in a post: tag:x matches its tags (tag:focus:site that tag, tag:site that id under any kind, as the board search command does); any other word its hay. */
const hit = (p, w) => (w.startsWith("tag:") && w.length > 4 ? (p.tags || []).some(t => (w.includes(":", 4) ? t.toLowerCase() === w.slice(4) : t.toLowerCase().split(":").slice(1).join(":") === w.slice(4))) : hay(p).includes(w));

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
    if (q.length && !q.every(w => hit(p, w) || (!c.thread && (p.th + " " + threadTitle(p.th)).toLowerCase().includes(w)))) return false;
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
  const src = c.thread ? (S.threads.get(c.thread) || { posts: NONE }).posts : c.home || c.focus != null || c.place ? NONE : S.posts;
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
    if (!L.opts.showThread && L.helpers) { const hs = helperFolds(p.th, p.t, L.helpers, L.opts); if (hs.length) { out.push(...hs); L.prev = null; } }   // helper conversations fold where they began (R7)
    const q = L.prev, cont = !!q && q.from === p.from && q.th === p.th && !p.reply_to && p.t - q.t < GROUP && sameTo(q, p) && sameSource(q, p);
    const a = message(p, { ...L.opts, cont }); a.dataset.show = KIND_NAME[postKind(p)] || ""; if (a.dataset.show === "you" && asksQuestion(p)) a.dataset.ask = "";   // For you, Updates or Agent comms, marked in the list (11040113eacf)
    L.prev = p; a._head = L.heads; L.io.observe(a); out.push(a);
  }
  return out;
}

function paint(box, ctx) {
  const shown = L.all.slice(-C.limit), older = L.all.length - shown.length;
  L.day = ""; L.prev = null; L.names = new Set(); L.shown = shown.length; L.helpers = new Set();
  if (older && shown.length && !L.opts.showThread) helperFolds(shown[0].th, shown[0].t - 1, L.helpers);   // the ones begun before what is drawn stay with it L.io.disconnect(); L.outSig = "";
  L.out = h("div", { class: "cm-out", "data-key": "out" });
  L.jump = h("button", { type: "button", class: "cm-jump", hidden: true, onclick: () => { box.scrollTop = box.scrollHeight; } });
  const q = C.q.trim();   // a search narrows the messages: say so where they are, so a sent one never seems lost (11040214d468)
  box.replaceChildren(
    ...(q ? [h("div", { class: "cm-qbar", role: "status" }, h("span", null, `Showing ${plural(L.all.length, "match", "matches")} for “${short(q, 40)}”`),
      button("Clear search", () => { C.q = ""; C.find = false; ctx.update(); }, { size: "s", kind: "quiet", icon: "close" }))] : []),
    ...(older ? [h("div", { class: "cm-older" }, button(`Show older (${older} more)`, () => { C.limit += STEP; L.rebuild = true; L.keep = true; ctx.update(); }, { size: "s" }))] : []),
    ...(shown.length ? [...rows(shown), ...(L.opts.showThread ? [] : helperFolds(shown.at(-1).th, Infinity, L.helpers, L.opts))] : [empty(C.q ? "No messages match." : L.src.length && !simpleOn() ? "Nothing here with what Show has on. Turn on Agent comms to see all the messages." : "No messages yet.")]),
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
  el.setAttribute("data-flash", ""); el.setAttribute("data-hl", ""); setTimeout(() => el.removeAttribute("data-flash"), 1800); setTimeout(() => el.removeAttribute("data-hl"), 2400);
}
const jumpTo = id => {
  const p = S.byId[id], box = L.box; if (!p) return;
  // from another page (Yours), or another conversation: open the post's conversation first; paint() finishes the jump
  if (!box || parse().id !== "comms" || !box.isConnected || (L.opts && conversation(L.ctx && L.ctx.arg).thread !== p.th)) { L.jumpTo = id; return go("comms", p.th); }
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
        L.stuck = atEnd(box) || (L.stuck && !(performance.now() - L.hand < 1000));   // only the reader unsticks it: a scroll the browser makes when a message reflows late (a chart, code) keeps the end
        if (L.stuck && L.unseen) { L.unseen = 0; setJump(); }
        cardSeen(box);
        L.saved = L.stuck ? null : { key: L.key, fromEnd: box.scrollHeight - box.scrollTop };
      }, { passive: true });
      for (const t of ["wheel", "touchstart", "pointerdown", "keydown"]) box.addEventListener(t, () => { L.hand = performance.now(); }, { passive: true });
      // The box changes size when the composer grows, the keyboard opens or the window moves: stay at the end if that's where it was.
      L.ro = new ResizeObserver(() => { if (L.stuck) box.scrollTop = box.scrollHeight; });
      L.ro.observe(box);
      box.addEventListener("load", () => { if (L.box === box && L.stuck) box.scrollTop = box.scrollHeight; }, true);   // a picture arriving grows the list
      L.near = new Set();
      L.io = new IntersectionObserver(es => {
        for (const e of es) { if (e.isIntersecting) { L.near.add(e.target); refreshHead(e.target); } else L.near.delete(e.target); }
      }, { root: box, rootMargin: "800px 0px" });
    }
    const fsig = S.foci ? (S.foci.foci || []).map(f => f.id + ":" + f.name).join() : "";   // a focus chip names its focus: redraw once the foci arrive
    if (fsig !== L.fociSig) { if (L.fociSig !== undefined && L.all && L.all.some(p => p.tags && p.tags.length)) { L.rebuild = true; L.keep = true; } L.fociSig = fsig; }
    if (L.rebuild) {
      const fromEnd = L.keep ? box.scrollHeight - box.scrollTop : isNew && L.saved && L.saved.key === L.key ? L.saved.fromEnd : null;
      paint(box, ctx);
      L.rebuild = false; L.keep = false;
      syncOut(c, box);
      if (fromEnd != null) { box.scrollTop = box.scrollHeight - fromEnd; L.stuck = atEnd(box); }
      else { box.scrollTop = box.scrollHeight; L.stuck = true; }
      if (L.jumpTo) {
        const id = L.jumpTo, sel = `[data-key="${CSS.escape(id)}"]`, p = S.byId[id]; let el = box.querySelector(sel);
        if (!el && p) {   // older than what is drawn, or hidden by the Show setting: draw down to it (one more paint), or show everything
          const i = L.all.indexOf(p);
          if (i >= 0 && L.all.length - i > C.limit) { C.limit = L.all.length - i + 20; paint(box, ctx); el = box.querySelector(sel); }
          else if (i < 0 && showMask() !== SHOW.ALL && L.src.includes(p)) { setShow(SHOW.ALL, ctx); return; }
        }
        L.jumpTo = null; if (el) flash(box, el);
      }
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
  const hd = a.querySelector(":scope > .msg-h, :scope > .msg-c > .msg-h"), rc = a.querySelector(":scope > .msg-rc, :scope > .msg-c > .msg-rc");
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
const STATE_WORDS = { asks: () => "Waiting for your answer", working: s => (simpleOn() ? "Working on it" : [codePill(s.who), " working"]), quiet: s => (simpleOn() ? null : [codePill(s.who), " quiet"]), stuck: () => (simpleOn() ? "Taking longer than it should" : "Stuck"), queue: s => (simpleOn() ? "Waiting its turn" : [codePill(s.who), " in the job queue"]),
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
    const hits = d.posts.filter(p => rest.every(w => hit(p, w)));
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
// Needs you holds exactly what the one list counts (core/needs.js): a row there and a count that disagree was the R6 review's first finding.
const secOf = (th, st) => (pinned(th) ? "pin" : important(th) && !archived(th) ? "imp" : archived(th) ? "arch" : st && waitsOn(th) ? "need" : "rest");
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
/** The person's own exchanges that hold the question's words (core/yours.js): the first answer, before any agent is asked. Up to four. */
function yoursHits(q, c) {
  const hits = yoursSearch(q).slice(0, 4);
  return hits.map(p => ({ key: "y:" + p.id, href: threadHref(p.th), current: c.thread === p.th, title: (p.from === "person" ? "You" : plainName(p.from)) + " in " + threadTitle(p.th),
    meta: ago(p.t).replace(" ago", ""), text: short(yoursText(p), 160), onClick: e => { if (e.metaKey || e.ctrlKey || e.shiftKey) return; e.preventDefault(); jumpTo(p.id); } }));
}
function aiRows(c) { const q = aiQ(); return [...(q ? yoursHits(q, c) : []), ...askRows(c)]; }
function askRows(c) {
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
// By focus: a conversation sits under its focus (core/foci.js: the tag, the focus's own thread, else its parent's), the rest under Other.
const byFocusOn = () => app() && !simpleOn() && !C.imp && C.view === "focus";
// Everything in no focus, the person's own chats and untagged work alike, is one list, "Other" ("f:"), under the foci, newest first and never folded
// (the person, 11081454b6bd: Misc and Conversations were two names for the same thing). A helper follows the chat it came from up its chain.
const focusSec = th => { let x = th; for (let i = 0; i < 8 && x; i++) { const f = focusOf(x); if (f) return "f:" + f; x = threadParent(x); } return "f:"; };
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
  // By focus, the chain stops where the focus changes: a focus's own thread was started from some other conversation (the planning
  // one, 110400119446), and following that link put every focus and its helpers in one fold under Misc (the person, 110803020bbe).
  const listed = new Map(ds.map(d => [d.name, d]));
  const topic = byFocusOn() ? focusSec : null;
  const rootOf = d => { let x = d; for (let i = 0; i < 8; i++) { const p = listed.get(threadParent(x.name) || ""); if (!p || p === d || (topic && topic(p.name) !== topic(x.name))) break; x = p; } return x; };
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
    const say = railSay(mdPlain(displayText(lp)));
    const from = par ? [h("span", { class: "tm-from", title: "A helper conversation, started from " + threadTitle(par) }, "from " + short(threadTitle(par), 28)), " · "] : null;
    return { key: d.name, href: threadHref(d.name), current: c.thread === d.name, title: threadTitle(d.name), meta: ago(d.last).replace(" ago", ""),
      lead: [tierLead(d.name), h("span", { title: status(st.key).label || "" }, statusDot(st.key === "done" ? "ended" : st.key, { live }))],
      text: [from, d.name !== c.thread && hasDraft(d.name) ? [h("span", { class: "cm-draft", title: "You left unsent text in this conversation" }, "Draft"), " · "] : null, fadeTag(d.name) || stateTag(st), fadeTag(d.name) || stateTag(st) ? " · " : null, lp.from === "person" ? ["You: ", ...say] : say], sec: secOf(d.name, st), dim: faded(d.name) };   // no agent code on the row: the conversation names its agent
  };
  const dress = (r, th = r.key.replace(/~recent$/, "")) => {
    const im = important(th), pn = pinned(th), m = tmeta(th);
    if (im) r.lead = [h("span", { class: "tm-imp", title: m.impWhy ? `Important, from ${m.impBy || "an agent"}: ${m.impWhy}` : m.impBy ? `Important, marked by ${m.impBy}` : "Important" }, icon("star")), r.lead];
    if (pn && !im) r.lead = [h("span", { class: "tm-pin", title: "Pinned to the top" }, icon("pin")), r.lead];   // star and pin are set from the conversation's title menu (R6 review: two hover buttons on every row was clutter)
    if (app() && !simpleOn() && can("post")) r.acts = rowMore(th, ctx);   // the person, 11081252a9c1 / 110812537bec: one ⋯ on the right of the row, on hover, with pin, star, archive, not needed
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
  const rowsOut = !app() ? ds.map(mk) : roots.flatMap(d => {
    const r = dress(mk(d)), kids = kidsOf.get(d.name) || [], rest = topic ? topic(d.name) : "rest";   // by focus, "the rest" is the conversation's focus
    if (r.sec === "rest" || (topic && (r.sec === "imp" || r.sec === "need"))) r.sec = rest;   // By focus there are no Important or Needs you sections (the person, 11081223f77a): the row stays under its focus, its star and state tag still show
    const own = kids.filter(k => !["rest", "arch"].includes(secOf(k.name, threadState(k.name, seen))) && !(topic && ["imp", "need"].includes(secOf(k.name, threadState(k.name, seen))))).map(k => dress(mk(k)));   // a helper's own row where it needs the person
    if (C.imp || r.sec === rest || r.sec === "arch") return [r, ...kidRows(d, r.sec), ...own];
    return [r, dress({ ...mk(d), key: d.name + "~recent", sec: rest }), ...kidRows(d, rest), ...own];
  });
  if (L.here && !topic) for (const r of rowsOut) if (r.href && focusSec(r.key.replace(/~recent$/, "").split("/").pop()) === L.here) r.here = true;   // Recent, with a focus page open beside it: its conversations marked
  if (!app()) rowsOut.unshift({ key: "(everything)", href: href("comms", "~all"), title: "Everything", text: `${S.posts.length} posts, newest last`, current: c.all });
  return rowsOut;
}
const KIDS = new Set();   // conversations whose helper fold is open, this page only
// Folded sections (the person, 11040202d77c): kept in this browser.
const FOLD_KEY = "hive.comms.folded";
const FOLD = new Set((() => { try { return JSON.parse(localStorage.getItem(FOLD_KEY) || "[]"); } catch (e) { return []; } })());
// First visit in this browser (nothing saved yet): every section starts collapsed (the person, 1108133751d8).
let foldFresh = (() => { try { return localStorage.getItem(FOLD_KEY) === null; } catch (e) { return false; } })();
function saveFold() { try { localStorage.setItem(FOLD_KEY, JSON.stringify([...FOLD])); } catch (e) { /* this page only */ } }
// Collapse or expand all (the person, 11061437ab67): the section heads and the helper folds in the list now. syncThreads fills L.foldSecs and L.foldKids.
// Which way it goes follows the sections, so a list with most of them open offers Collapse all; a list with no heads goes by its helper folds.
const foldShut = () => { const ss = L.foldSecs || [], ks = L.foldKids || []; return ss.length ? ss.every(k => FOLD.has(k)) : !ks.some(n => KIDS.has(n)); };
const foldWord = () => foldShut() ? "Expand all" : "Collapse all";
const foldNone = () => !(L.foldSecs || []).length && !(L.foldKids || []).length;
function foldAll(ctx) {
  const open = foldShut(), ss = L.foldSecs || [], ks = L.foldKids || [];
  if (open) { ss.forEach(k => FOLD.delete(k)); ks.forEach(n => KIDS.add(n)); }
  else { ss.forEach(k => FOLD.add(k)); KIDS.clear(); }
  saveFold(); ctx.update();
}
// The list in parts: Pinned, Needs you, the rest, and a link to the Archived page at the end (#comms/~archived). A search is one list.
// By focus the rest is one section per focus ("f:<id>") in the home cards' order, every focus listed (its "+" starts a conversation in it), then
// Other ("f:"), every conversation in no focus, last and always open.
// The person's order of the foci (the person, 1108031067f6): drag a focus's heading in the list, or a card's head on the home, onto another; one order serves both.
// Without a mouse: Alt+Up/Down on a focus heading, or the card's move buttons. Other stays last and doesn't move.
const DND = { id: null, el: null, at: 0 }, DND_TYPE = "text/x-h2-focus";
const dropMark = el => { if (DND.el && DND.el !== el) DND.el.removeAttribute("data-drop"); DND.el = el; };
function moved(err, ctx) { if (err) C.err = "Couldn't move the focus: " + err + "."; ctx.update(); }
/** Drag-and-drop props for a focus's section (rail) or card (home): `grab` goes on the handle, `drop` on the whole block. */
function focusDnd(fid, ctx) {
  if (!fid || !can("post")) return { grab: {}, drop: {} };
  const isMe = e => DND.id && DND.id !== fid && e.dataTransfer && [...(e.dataTransfer.types || [])].includes(DND_TYPE);
  const after = (e, el) => { const r = el.getBoundingClientRect(), side = r.width < el.parentElement.getBoundingClientRect().width * 0.75;   // cards side by side: left or right half; a list: top or bottom
    return side ? e.clientX > r.left + r.width / 2 : e.clientY > r.top + r.height / 2; };
  return {
    grab: { draggable: "true",
      ondragstart: e => { DND.id = fid; DND.at = Date.now(); e.dataTransfer.effectAllowed = "move"; e.dataTransfer.setData(DND_TYPE, fid); e.dataTransfer.setData("text/plain", fid); e.stopPropagation(); },
      ondragend: () => { DND.id = null; dropMark(null); } },
    drop: {
      ondragover: (e, el) => { if (!isMe(e)) return; e.preventDefault(); e.dataTransfer.dropEffect = "move"; dropMark(el); el.setAttribute("data-drop", after(e, el) ? "after" : "before"); },
      ondragleave: (e, el) => { if (!el.contains(e.relatedTarget)) { el.removeAttribute("data-drop"); if (DND.el === el) DND.el = null; } },
      ondrop: (e, el) => { if (!isMe(e)) return; e.preventDefault(); e.stopPropagation(); const id = DND.id, aft = after(e, el); DND.id = null; dropMark(null); el.removeAttribute("data-drop");
        const ids = foci().map(f => f.id).filter(x => x !== id), i = ids.indexOf(fid);
        moveFocus(id, { before: aft ? ids[i + 1] || null : fid }).then(err => moved(err, ctx)); } } };
}
/** Alt+Up / Alt+Down on a focus's heading moves it one place (the keyboard way to drag). */
const focusKeys = (fid, ctx) => !fid || !can("post") ? null : e => {
  if (!e.altKey || (e.key !== "ArrowUp" && e.key !== "ArrowDown")) return;
  e.preventDefault(); moveFocus(fid, { step: e.key === "ArrowUp" ? -1 : 1 }).then(err => { moved(err, ctx); requestAnimationFrame(() => { const a = document.querySelector(`#main .tm-sec[data-sec="f:${fid}"] .tm-sec-link`); if (a) a.focus(); }); });
};
const SECS = [["pin", "Pinned"], ["imp", "Important"], ["need", "Needs you"], ["rest", null]];
function sections(rs, ctx, opts) {
  if (words(C.tq).length) return list(rs, opts);
  const by = { pin: [], imp: [], need: [], rest: [], arch: [] }, tops = [];
  const byFocus = byFocusOn();
  if (byFocus) for (const f of fociOrder()) { const k = "f:" + (f.id || ""); if (!by[k]) { by[k] = []; tops.push(k); } }
  for (const r of rs) { const k = r.sec || "rest"; if (k.startsWith("f:") && !by[k]) { by[k] = []; tops.push(k); } (by[k] || by.rest).push(r); }
  const order = new Map(tops.map((k, i) => [k, k === "f:" ? 1e9 : i])); tops.sort((a, b) => order.get(a) - order.get(b));
  const label = k => k === "f:" ? "Other" : focusName(focusById(k.slice(2)) || { name: k.slice(2) });
  const parts = [...SECS.slice(0, 3), ...tops.map(k => [k, label(k)]), SECS[3]].filter(([k]) => by[k].length || (byFocus && k.startsWith("f:") && k !== "f:")), heads = parts.length > 1 || by.pin.length;
  L.foldSecs = heads ? parts.map(([k]) => k).filter(k => k !== "f:") : [];   // Other has no fold
  if (foldFresh && heads) { foldFresh = false; L.foldSecs.forEach(k => k !== L.here && FOLD.add(k)); saveFold(); }   // a focus page open beside it keeps its own section open
  const sec = (k, label, rows) => {
    if (!heads) return h("div", { class: "tm-sec", "data-key": "sec:" + k, "data-sec": k }, list(rows, { ...opts, empty: "" }));
    if (k === "f:") return h("div", { class: "tm-sec", "data-key": "sec:" + k, "data-sec": k },   // Other: a plain heading, its rows always shown
      h("div", { class: "tm-sec-hw" }, h("p", { class: "tm-sec-h tm-sec-other" }, label)), list(rows, { ...opts, empty: "" }));
    const here = L.here === k, shut = FOLD.has(k), fid = k.startsWith("f:") ? k.slice(2) : null;   // the focus page open beside it: its section is marked; pressing its heading again folds it, the page stays
    const toggle = () => { FOLD.has(k) ? FOLD.delete(k) : FOLD.add(k); saveFold(); ctx.update(); };
    const dnd = focusDnd(fid, ctx);
    // A focus's heading is a link to its page (the person, 11080309f795); the chevron before it folds the section. Other headings fold as a whole.
    return h("div", { class: "tm-sec", "data-key": "sec:" + k, "data-sec": k, "data-folded": shut ? "" : null, "data-here": here ? "" : null, ...dnd.drop },
      h("div", { class: "tm-sec-hw", "aria-current": here ? "true" : null, ...dnd.grab },
        fid != null ? [
          h("button", { type: "button", class: "tm-sec-ch", "aria-expanded": String(!shut), title: shut ? "Show this section" : "Fold this section", "aria-label": (shut ? "Show " : "Fold ") + label, onclick: toggle }, icon(shut ? "chevron-right" : "chevron-down")),
          h("a", { class: "tm-sec-h tm-sec-link", href: href("focus", fid || "misc"), title: fid && can("post") ? `Open ${label}. Drag to reorder the foci (Alt+Up or Alt+Down moves it)` : `Open ${label}`, draggable: fid && can("post") ? "false" : null, onkeydown: focusKeys(fid, ctx),
            onclick: e => { if (e.button || e.metaKey || e.ctrlKey || e.shiftKey) return; if (here) { e.preventDefault(); toggle(); } else if (FOLD.has(k)) { FOLD.delete(k); saveFold(); } } }, shut ? `${label} (${rows.length})` : label)] :
        h("button", { type: "button", class: "tm-sec-h tm-fold", "aria-expanded": String(!shut), title: shut ? "Show this section" : "Fold this section", onclick: toggle },
          icon(shut ? "chevron-right" : "chevron-down"), shut ? `${label} (${rows.length})` : label),
        fid && can("post") ? h("button", { type: "button", class: "tm-sec-add", title: `New conversation in ${label}`, "aria-label": `New conversation in ${label}`, onclick: () => newIn(fid) }, icon("plus")) : null),
      shut ? null : rows.length ? list(rows, { ...opts, empty: "" }) : h("p", { class: "meta tm-sec-none" }, "Nothing this week."));
  };
  return h("div", { class: "tm-secs" },
    parts.length ? parts.map(([k, l]) => sec(k, l || (app() ? "Recent" : "Threads"), by[k])) : by.arch.length ? null : list([], opts),
    // Archived is a page of its own, where a conversation can be brought back (the person, 11081454b6bd); the list ends with the way there.
    by.arch.length ? h("div", { class: "tm-sec", "data-key": "sec:arch", "data-sec": "arch" },
      h("a", { class: "tm-sec-h tm-arch-b", href: href("comms", "~archived"), "aria-current": C.conv === "~archived" ? "page" : null, title: "Conversations out of the list; bring one back from there" },
        icon("archive"), `Archived (${by.arch.length})`)) : null);
}
function syncThreads(c, ctx) {
  const tl = document.querySelector("#main .cm-tl"); if (!tl) return;
  if (!simpleOn()) loadFoci();   // the foci (core/foci.js); a store emit when they change
  const sig = [S.ver, C.view, C.hideFaded, S.foci && S.foci.at, S.foci && (S.foci.foci || []).map(f => f.id).join(), C.scope, C.conv, C.imp, tmetaSig(), [...FOLD].join(), [...KIDS].join(), C.tq, AI.st + AI.res.length, AI.tag, S.shown && S.shown.length, showMask(), c.arg, Math.floor(now() / 3e4), S.runSig, marksSig().length].join("|");
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
const moreOpen = new Set();   // threads whose status "More" is open: a redraw (a new post) keeps it open (R15)
function statusBar(th) {
  const r = th && S.threadStatus && S.threadStatus[th]; if (!r) return null;
  const [a, ...rest] = r.text.split("\n");
  return h("div", { class: "cm-status cm-strip", "data-key": "status", "data-fold": "", role: "note", "aria-label": "Thread status" },
    h("b", { class: "cm-strip-a" }, a), rest.length ? h("span", { class: "cm-strip-b" }, rest[0]) : null,
    rest.length > 1 ? h("details", { class: "cm-status-more", open: moreOpen.has(th) || null, ontoggle: (e, n) => { if (e.target === n) n.open ? moreOpen.add(th) : moreOpen.delete(th); } }, h("summary", null, "More"), h("p", null, rest.slice(1).join(" "))) : null,
    // One agent named per conversation (R6 review): the strip names who is on it; the summary's author is on hover.
    h("span", { class: "meta cm-status-m cm-strip-m", title: "Summary by " + r.from }, `updated ${ago(r.at * 1e3)}${lastAt(th) ? " · last post " + ago(lastAt(th)) : ""}`,
      can("post") ? h("button", { type: "button", class: "cm-status-r", title: "Reply about this summary", onclick: () => quoteRef(th, { who: r.from, where: "summary", text: r.text }) }, "Reply") : null));
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
  for (const a of run) {
    if (st.key === "asks" && a.name === st.who) continue;
    const s = agentState(a);
    if (simpleOn()) { if (!out.some(x => x.dataset.key === "run")) out.push(line("working", "Your Hive is working on it.", can("stop") ? stopButton("Stop this work", () => askStop(a.name)) : null, { key: "run", live: true })); continue; }
    // Closed it reads "a7c31 Editing fetch.py"; how long ago it was active shows only when open.
    const doing = a.doing ? a.doing.charAt(0).toUpperCase() + a.doing.slice(1) : AGENT_WORDS[s.key] || s.text;
    out.push(line(s.key, [who(a.name), " ", doing, h("span", { class: "meta cm-line-m" }, " · " + (s.key === "working" ? ago(a.last * 1e3) : s.text))],
      [post && th ? interruptButton(th, a.name) : null, can("stop") ? stopButton(`Stop ${a.name}`, () => askStop(a.name)) : null], { key: "run:" + a.name, live: true }));
  }
  // after the working agents (11081336a82e: folded, the strip shows who works and its Interrupt, not where the conversation sits)
  // Where it sits (the person, 110601450d55): a helper's conversation names the one it came from, and that one lists its helpers.
  const par = app() && threadParent(th), kids = app() ? threadChildren(th) : [];
  if (par) out.push(line("ended", ["Part of ", h("a", { href: threadHref(par) }, short(threadTitle(par), 60)), simpleOn() ? null : [": ", who(d0(th)), " started it from there"]], null, { key: "parent" }));
  if (kids.length) {
    const ks = kids.map(k => ({ k, st: threadState(k, seen) })).sort((a, b) => S.threads.get(b.k).last - S.threads.get(a.k).last), on = ks.filter(x => ["working", "queued", "quiet"].includes(x.st.key)).length, ask = ks.filter(x => NEED.has(x.st.key)).length;
    const links = ks.slice(0, 5).flatMap((x, i) => [i ? ", " : "", h("a", { href: threadHref(x.k), title: status(x.st.key).label || "" }, short(threadTitle(x.k), 40))]);
    out.push(line(on ? "working" : ask ? "asks" : "ended", [plural(kids.length, "helper conversation"), on ? ` (${on} working${ask ? `, ${ask} waiting on you` : ""})` : ask ? ` (${ask} waiting on you)` : "", ": ", ...links, ks.length > 5 ? ` and ${ks.length - 5} more` : ""],
      post ? button(KIDS.has(th) ? "Hide in list" : "Show in list", () => { KIDS.has(th) ? KIDS.delete(th) : KIDS.add(th); L.ctx && L.ctx.update(); }, { size: "s" }) : null, { key: "kids", live: !!on }));
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
// A row's ⋯ (the person, 110812537bec): sits where the row's time is and shows on hover or keyboard focus. Its menu is put on <body>
// with a fixed place, so the rail's scroll box can't clip it, and goes on a pick, Escape, a press outside, or a scroll.
let rowM = null;
function closeRowMenu() { if (!rowM) return; rowM.nodes.forEach(n => n.remove()); rowM.btn.setAttribute("aria-expanded", "false"); rowM.btn.removeAttribute("data-open"); document.removeEventListener("keydown", rowM.key, true); window.removeEventListener("scroll", rowM.scroll, true); rowM = null; }
function rowMore(th, ctx) {
  return h("button", { type: "button", class: "lr-more-b", "aria-haspopup": "true", "aria-expanded": "false", title: "Pin, mark important, archive", "aria-label": "Actions for this conversation",
    onclick: e => { e.preventDefault(); e.stopPropagation(); const b = e.target.closest(".lr-more-b"), was = rowM && rowM.btn === b; closeRowMenu(); if (!was) openRowMenu(b, th, ctx); } }, icon("more"));
}
function openRowMenu(b, th, ctx) {
  const im = important(th), pn = pinned(th), ar = archived(th), fd = faded(th);
  const it = (label, ic, patch, tip) => h("button", { type: "button", class: "menu-i", role: "menuitem", title: tip || null, onclick: e => { e.stopPropagation(); closeRowMenu(); tryMeta(th, patch, ctx); } }, icon(ic), h("span", { class: "menu-i-t" }, label));
  const m = h("div", { class: "menu lr-menu", role: "menu", "aria-label": "Conversation actions" },
    it(pn ? "Unpin" : "Pin to the top", "pin", { pin: !pn }),
    it(im ? "Not important" : "Mark important", "star", { important: !im }),
    h("div", { class: "menu-sep", role: "separator" }),
    it(fd ? "Needed again" : "Not needed", "check", { faded: !fd }, fd ? null : "Grey it in the list; back by itself when something new is for you"),
    it(ar ? "Unarchive" : "Archive", "archive", { archived: !ar }, ar ? null : "Out of the list until something new is for you"));
  const sc = h("button", { type: "button", class: "scrim", "aria-label": "Close the menu", tabindex: "-1", onclick: e => { e.stopPropagation(); closeRowMenu(); } });
  document.body.append(sc, m);
  const r = b.getBoundingClientRect(), mh = m.offsetHeight, mw = m.offsetWidth;
  const top = r.bottom + 4 + mh > innerHeight - 8 ? Math.max(8, r.top - 4 - mh) : r.bottom + 4;
  Object.assign(m.style, { position: "fixed", top: top + "px", left: Math.max(8, Math.min(r.right - mw, innerWidth - mw - 8)) + "px" });
  b.setAttribute("aria-expanded", "true"); b.dataset.open = "";
  rowM = { btn: b, nodes: [sc, m], key: e => { if (e.key === "Escape") { e.stopPropagation(); closeRowMenu(); b.focus(); } }, scroll: e => { if (!m.contains(e.target)) closeRowMenu(); } };
  document.addEventListener("keydown", rowM.key, true); window.addEventListener("scroll", rowM.scroll, true);
  m.querySelector(".menu-i")?.focus();
}

function titleMenu(c, n, d, off, ctx) {
  const th = c.thread, full = th && app() && !simpleOn(), mine = can("post");
  const act = (label, ic, fn, title, val) => button(val ? [label, h("small", null, val)] : label, () => { C.menu = null; fn(); ctx.update(); }, { size: "s", kind: "quiet", icon: ic, title });
  const link = (label, ic, to) => button(label, null, { href: to, size: "s", kind: "quiet", icon: ic });
  const t = th && tier(tierOf(th)), im = full && important(th), pin = full && pinned(th), arch = full && archived(th), fd = full && faded(th);
  const part = (...xs) => xs.filter(Boolean);
  // R7b S6: the hand-off's items first (Rename, Move to a focus, Pin, Star, Files here, Yours, Copy link, Stop); ours that it lacks go under a divider.
  const own = full ? part(
    mine ? act("Rename", "edit", () => renameDialog(th), "Give it a name of your own. Only the dash shows it.") : null,
    mine ? act(pin ? "Unpin" : "Pin to the top", "pin", () => tryMeta(th, { pin: !pin }, ctx), pin ? "Back among the others" : "Keep it at the top of the list, whatever is on") : null,
    mine ? act(im ? "Not important" : "Mark important", "star", () => tryMeta(th, { important: !im }, ctx), im ? "Take the important mark off" : "Keep it above the others in the list") : null) : [];
  const more = full ? part(
    mine ? act(arch ? "Unarchive" : "Archive", "archive", () => { tryMeta(th, { archived: !arch }, ctx); if (!arch) go("comms"); }, arch ? "Back in the list" : "Out of the list until something new is for you") : null,
    mine ? act(fd ? "Needed again" : "Not needed", "check", () => tryMeta(th, { faded: !fd }, ctx), fd ? fadeWhy(th) : "Grey it in the list: still there to open and write in, and back by itself when something new is for you") : null) : [];
  const prio = th && simpleOn() ? null : th && can("tier") ? button([h("span", { class: "ref-sw", "data-tone": t.tone }), "Priority", h("small", null, t.label)], () => { C.menu = null; askTier(th); }, { size: "s", kind: "quiet", title: "Where this sits in the hive's priority list" })
    : th ? h("p", { class: "meta cm-tier", title: "This thread's priority" }, h("span", { class: "ref-sw", "data-tone": t.tone }), "Priority: " + t.label) : null;
  const fs = full && mine && S.foci ? fociOrder().filter(f => !f.misc) : [], cur = full ? focusOf(th) : null;
  const focusRow = fs.length ? h("label", { class: "cm-focus-row" }, h("span", null, "Focus"),
    h("select", { class: "cm-focus-sel", "aria-label": "The focus this conversation is filed under", title: "Which focus this conversation belongs to; No focus leaves it in Other",
      onchange: e => { const v = e.target.value; C.menu = null; apiPost("/api/focus", v ? { thread: th, focus: v } : { thread: th, off: true }).then(() => loadFoci(true), e2 => { C.err = "Couldn't file it: " + (e2 && e2.message || "the server didn't answer") + "."; }).then(() => ctx.update()); } },
      h("option", { value: "", selected: !cur ? "" : null }, "No focus"), fs.map(f => h("option", { value: f.id, selected: f.id === cur ? "" : null }, focusName(f))))) : null;
  const lead = part(...own,
    focusRow,
    full ? titleExtras(th, { files: () => import("../ui/preview.js").then(m => m.openFiles()), close: () => { C.menu = null; ctx.update(); } }) : null);
  const where = part(...more,
    prio,
    off > 0 && !simpleOn() ? act(`Show ${off} hidden`, "filter", () => setShow(SHOW.ALL, ctx), "Turn on For you, Updates and Agent comms") : null,
    link("All messages", "chat", href("comms", "~all")),
    full ? link("On the timeline", "clock", href("timeline")) : null);
  const out = full ? part(
    act("Share this result", "board", () => shareDialog(th), "Save the conversation as one page you can post: the ask, the agents at work, the result. You see it first and can remove anything."),
    act("Export as Markdown", "copy", () => downloadMarkdown(th), "Download the conversation as a .md file, with simple formatting"),
    act("Export as PDF", "board", () => printScript(th), "Lay it out like a script and save it as a PDF from the print dialog")) : [];
  const tb = document.querySelector(".cm-head .cm-title-b");   // R7b S6: under the title it opens from, not at the far right
  return h("div", { class: "cm-menu menu", role: "menu", "data-key": "title", "data-side": "bottom-start", style: tb ? `--tx:${tb.offsetLeft}px` : null },
    h("p", { class: "meta cm-cost" }, (C.q.trim() ? plural(n, "match", "matches") : plural(n, app() ? "message" : "post")) + (d ? ` \u00b7 since ${stamp(d.first)}` : "") + (full && threadCost(th) != null ? ` \u00b7 ${money(threadCost(th))}` : "")),
    lead, lead.length ? sep() : null, where, out.length ? sep() : null, out,
    full && th !== threadTitle(th) ? h("p", { class: "meta cm-menu-id" }, "Its id on the board: ", h("code", { title: "Click to select it" }, th), copyButton(th)) : null);   // copy it (the person, 11041333ab1a)
}
/** The rail's rows to the pages Chat brings together, under New conversation (the hand-off's Rail, `button.cm-nav`; links here, since each opens a page): each only where this connection may open it, with a count where there is one. Chat shows no row, so these are the way to them. */
function places() {
  const link = (label, ic, to, n, tip) => h("a", { class: "cm-place cm-nav", href: to, title: tip, "data-key": ic, "aria-current": L.place && to === href(L.place) ? "page" : null }, icon(ic), h("span", { class: "cm-place-l" }, label), n ? h("b", { class: "cm-place-n", "aria-label": n + " new" }, String(n)) : null);
  return h("div", { class: "cm-places", role: "group", "aria-label": "Also in Chat", "data-key": "places" },
    link("Yours", "yours", href("yours"), 0, "Every exchange you had, newest first"),
    can("post") ? link("Needs me", "bell", href("yours", "waits"), needsN(), needsN() ? "What waits on you, newest first" : "Nothing waits on you") : null,
    can("read") ? link("Inbox", "inbox", href("inbox"), IB.unread || 0, "Every message agents sent you") : null,
    can("files") ? link("Mail", "mail", href("mail"), MN.n, MN.n ? "Mail since you last opened it" : "Your mail") : null,
    link("Files", "file", href("made"), 0, "Files and links from your conversations"));
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
    app() && !searching && C.conv !== "~list" ? [h("p", { class: "meta cm-show-l" }, "Arrange"), viewBar(ctx)] : null,   // the phone's list page has the toggle in sight
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
// In the Show menu: By focus or By recency, and whether the conversations marked Likely not needed are listed at all.
function viewBar(ctx) {
  const nFaded = [...S.threads.keys()].filter(th => faded(th) && !archived(th)).length;
  return h("div", { class: "cm-views", "data-key": "views" },
    seg([["focus", "By focus", "Each conversation under its focus, in the order of the home cards, the rest under Other last; helper conversations folded under the one they came from"],
      ["recent", "By recency", "Every conversation in one list, newest activity first, helper conversations included, each naming the one it came from"]], C.view,
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
      c.thread && app() && !simpleOn() ? threadCrumb(c.thread) : null,
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
    C.menu === "title" || (C.menu === "asides" && c.thread) ? scrim(() => { C.menu = null; ctx.update(); }) : null,
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
  return h("div", { class: "cm-menu cm-aside-m menu", role: "menu", "data-key": "asides", "data-side": "bottom-end" },
    h("p", { class: "meta" }, "Side questions"),
    sideKids(th).reverse().map(t => { const d = S.threads.get(t), last = d && d.posts.at(-1);
      return h("a", { href: threadHref(t), class: "cm-aside-i menu-i", role: "menuitem", "data-key": t, title: threadTitle(t).replace(/^Aside: /, "") },
        h("span", { class: "cm-aside-q" }, threadTitle(t).replace(/^Aside: /, "")), last ? h("span", { class: "meta" }, ago(last.t)) : null); }));
}

// ---- home: start something, one line on what waits, then one card per focus ----
// The home is the top of four depths (the person, 1107151649a6): home card (high level) > focus page > a conversation's status bar > the conversation.
// A card shows only what agents put in the focus's summary (the focus command sets it) and the live state; nothing here is generated from the raw threads.
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
const HOME_NOW = 3, HOME_NEEDS = 2, HOME_LATELY = 2, HOME_THREADS = 4, MISC_THREADS = 6, HOME_AGENTS = 4;   // two asks on a card, the rest behind All (R6 review)
// The card and the page, v3 (designs/ui-redesign-v3/V3-HANDOFF.md 3a, 3b): what needs the person first, then a time-first timeline, compact rows.
/** A summary line as {text, at (ms)}: a line is a string (its time is the summary's) or {text, at} once the summary keeps a time per line. */
export const sumLine = (x, sum) => typeof x === "string" ? { text: x, at: ((sum && sum.at) || 0) * 1e3 || null } : { text: (x && x.text) || "", at: ((x && x.at) || (sum && sum.at) || 0) * 1e3 || null };
export const sumLines = (sum, k) => ((sum && sum[k]) || []).map(x => sumLine(x, sum)).filter(l => l.text);
/** A line in running text: agents as the app's agent pill (V3 rule: no agent code outside a pill), a #thread as a quiet link (no square), so a sentence doesn't fill with chips. */
export function lineText(text) {
  if (simpleOn()) return plainPills(text);
  return pills(text).map(x => x && x.nodeType === 1 && x.dataset.kind === "thread" ? h("a", { class: "th", href: x.getAttribute("href"), title: x.title }, x.textContent)
    : x && x.nodeType === 1 && x.dataset.kind === "agent" ? (x.classList.add("pill"), x) : x);
}
/** The same, inside a button: no link inside a button, so refs become spans. */
export const flat = xs => [].concat(xs).map(x => x && x.nodeType === 1 && x.tagName === "A" ? h("span", { class: x.className, "data-kind": x.dataset.kind || null }, ...x.childNodes) : x);
/** A needs line's trailing ", in #thread." lifted out: {text, th} (th null when there's none). */
export function liftIn(text) {
  const m = /,?\s+in\s+#([\w.-]+?)\.?\s*$/.exec(String(text || ""));
  return m && S.threads.has(m[1]) ? { text: text.slice(0, m.index), th: m[1] } : { text, th: null };
}
/** When a focus last changed: the newest of its summary and its conversations. */
export function updatedAt(f, rows) {
  let t = ((f.summary && f.summary.at) || 0) * 1e3;
  for (const r of rows) { const d = S.threads.get(r.th); if (d && d.last > t) t = d.last; }
  return t || null;
}
/** A conversation's dot: working or queued only; idle draws an empty cell so titles line up. */
export function rowDot(th) {
  const k = threadState(th).key;
  return k === "working" ? { tone: "good", word: "working" } : k === "queued" ? { tone: "warn", word: "waiting to start" } : null;
}
function cardRow(r) {
  const d = S.threads.get(r.th), dot = rowDot(r.th), title = threadTitle(r.th);
  return h("li", { "data-key": r.th }, h("a", { class: "fc-row", href: threadHref(r.th), title: title + (dot ? ", " + dot.word : "") },
    dot ? h("span", { class: "st", "data-tone": dot.tone, "aria-hidden": "true" }) : h("span", { "aria-hidden": "true" }), h("b", null, title), d ? when(d.last, "t") : null));
}
const cardEv = kind => l => h("li", { class: "fc-ev", "data-kind": kind, "data-key": kind + ":" + l.text.slice(0, 80) }, when(l.at, "t"), h("span", { class: "fc-ev-x", title: l.text }, lineText(l.text)));
const cardRows = (rows, n) => rows.length ? h("div", { class: "fc-cv" }, h("h3", { class: "fc-sub" }, "Conversations"), h("ul", { class: "fc-rows" }, rows.slice(0, n).map(cardRow))) : null;
/** The card's … menu: move it earlier or later (the keyboard's drag), and a new conversation. */
function cardMenu(f, id, ctx) {
  const all = foci().map(x => x.id), at = all.indexOf(id), mv = all.length > 1, open = C.menu === "fc:" + id, name = focusName(f);
  const close = () => { C.menu = null; ctx.update(); };
  const item = (label, fn, off) => h("button", { type: "button", class: "menu-i", role: "menuitem", disabled: off ? "" : null, onclick: () => { C.menu = null; fn(); ctx.update(); } }, label);
  return h("span", { class: "fc-more pop" },
    h("button", { type: "button", class: "btn fc-moreb", "data-kind": "icon", "data-size": "s", "aria-haspopup": "menu", "aria-expanded": String(open), "aria-label": `More for ${name}: ${mv ? "move earlier or later, " : ""}new conversation`, title: "More",
      onclick: () => { C.menu = open ? null : "fc:" + id; ctx.update(); } }, icon("more")),
    open ? [scrim(close), h("div", { class: "cm-menu menu fc-menu", role: "menu", "data-side": "bottom-end", "data-key": "fcm" },
      mv ? item("Move earlier", () => moveFocus(id, { step: -1 }).then(err => moved(err, ctx)), at <= 0) : null,
      mv ? item("Move later", () => moveFocus(id, { step: 1 }).then(err => moved(err, ctx)), at >= all.length - 1) : null,
      item("New conversation", () => newIn(id)))] : null);
}
function focusCard(f, ctx) {
  const id = f.id || null, page = href("focus", id || "misc"), rows = activeThreads(id).filter(r => !archived(r.th)), up = updatedAt(f, rows);
  if (f.misc) return h("article", { class: "fc", "data-key": "fc:misc", "data-misc": "", "data-state": "misc" },
    h("header", { class: "fc-hd" }, h("a", { class: "fc-name", href: page }, "Other"), when(up, "t", "updated"),
      h("div", { class: "fc-lead" }, h("span", { class: "meta" }, "Not filed under a focus"))),
    cardRows(rows, MISC_THREADS),
    h("footer", { class: "fc-ft" }, h("a", { href: page }, rows.length > MISC_THREADS ? `All ${rows.length} conversations` : "The focus page")));
  const st = stateOf(id), sum = f.summary, ag = agentsOf(id), asked = askedOf(id), nd = needsOf(id), n = Math.max(nd.length, asked.length);
  const dnd = focusDnd(id, ctx), now = sumLines(sum, "now"), lately = sumLines(sum, "lately");
  const pillsOf = ag.now.length ? ag.now : [];
  return h("article", { class: "fc", "data-key": "fc:" + id, "data-state": st.key, "data-needs": asked.length ? "" : null, ...dnd.drop },
    h("header", { class: "fc-hd", ...dnd.grab },
      h("a", { class: "fc-name", href: page, title: focusName(f) + (f.aim ? ": " + f.aim : ""), draggable: dnd.grab.draggable ? "false" : null }, focusName(f)),
      when(up, "t", "updated"),
      h("div", { class: "fc-lead" }, statusWord(st.tone, st.word),
        pillsOf.length ? h("span", { class: "fc-ag" }, pillsOf.slice(0, HOME_AGENTS).map(a => simpleOn() ? null : agentRef(a, { pill: true })),
          !simpleOn() && pillsOf.length > HOME_AGENTS ? h("span", { class: "pill pill-more", title: pillsOf.slice(HOME_AGENTS).join(", ") }, "+" + (pillsOf.length - HOME_AGENTS)) : null) : null)),
    // Needs you: the summary's own asks (the person's rule), at most two; the count and All N from the one list.
    asked.length ? h("section", { class: "fc-ask", "aria-label": "Needs you" },
      h("h3", { class: "fc-ask-h" }, h("span", { class: "st", "data-tone": "info", "aria-hidden": "true" }), `Needs you · ${n}`, n > HOME_NEEDS ? h("a", { href: page }, `All ${n}`) : null),
      h("ul", null, asked.slice(0, HOME_NEEDS).map((t, i) => askItem(f, i, t)))) : null,
    !asked.length && nd.length ? h("p", { class: "fc-ask fc-ask-n" }, h("span", { class: "st", "data-tone": "info", "aria-hidden": "true" }), h("a", { href: page }, `${nd.length} wait${nd.length === 1 ? "s" : ""} on you in its conversations`)) : null,
    now.length || lately.length ? h("div", { class: "fc-tl" },
      now.length ? [h("h3", { class: "fc-sub" }, "Now"), h("ol", null, now.slice(0, HOME_NOW).map(cardEv("now")))] : null,
      lately.length ? [h("h3", { class: "fc-sub" }, "Lately"), h("ol", null, lately.slice(0, HOME_LATELY).map(cardEv("lately")))] : null)
      : h("p", { class: "fc-none" }, sum ? "Nothing listed as moving." : "No summary yet. The agents on it write one as they work."),
    cardRows(rows, HOME_THREADS),
    h("footer", { class: "fc-ft" }, h("a", { href: page }, rows.length > HOME_THREADS ? `All ${rows.length} conversations` : "The focus page"),
      can("post") ? button("New", () => newIn(id), { kind: "quiet", size: "s", icon: "plus", title: "New conversation in " + focusName(f) }) : null,
      can("post") ? cardMenu(f, id, ctx) : null));
}
/** Needs you on the home (the hand-off's Home, R7b S5): each thing that waits, from the same list as the count above it and the Chat badge,
 *  with the ask in full, its conversation, who asked and when, and Answer to the place to answer it. The rest behind All N. */
// At most three rows, the rest behind All N, so the first focus card is within one screen of the top on a phone (R17).
const HOME_WAITS = 3;
/** Opens the quick-answer panel (ui/quick.js) for w, or null where this address can't post: then the row links to the conversation. */
export const answerer = w => (can("post") ? (e, n) => { if (e.metaKey || e.ctrlKey || e.shiftKey || e.button > 0) return; e.preventDefault(); const el = n || e.target, r = el.closest("li"), b = el.closest(".fc-ask-i, .fp-ask") || (r && r.querySelector(".cm-need-a, .fc-ask-a")); openQuick(w, b || el); } : null);
/** The summary's i-th ask for focus f as a waiting item (the one list's own entry when it has it), so it opens the panel. */
export function summaryWait(f, i, text) {
  const key = `sum:${f.id}:${i}`, s = f.summary, j = s && s.needs ? s.needs.indexOf(text) : -1;
  return waitingOn().find(w => w.src === "summary" && w.focus === f.id && w.text === text) || { th: f.thread || f.id, text, t: needAt(s, j >= 0 ? j : i), src: "summary", focus: f.id, key };
}
/** One ask on a card: the whole item is a button that opens the quick-answer panel. Plain line where this address can't post. */
export function askItem(f, i, text) {
  const w = summaryWait(f, i, text), open = answerer(w), lf = liftIn(text), th = lf.th || (S.threads.has(w.th) ? w.th : null);
  const body = [h("span", { class: "fc-ask-x" }, flat(lineText(lf.text))), when(w.t, "t"), th ? h("span", { class: "fc-ask-in" }, threadTitle(th)) : null];
  if (!open) return h("li", { "data-key": w.key }, h("div", { class: "fc-ask-i" }, body));
  return h("li", { "data-key": w.key }, h("button", { type: "button", class: "fc-ask-i", "aria-haspopup": "dialog", title: "Answer: " + text, onclick: open }, body));
}
function homeNeeds(ws) {
  if (!ws.length) return null;
  const row = w => {
    const d = S.threads.get(w.th), p = w.post && d ? d.posts.find(x => x.id === w.post) : null, who = (w.st && w.st.who) || (p && p.from) || null;
    const to = w.src === "summary" && w.focus ? href("focus", w.focus) : threadHref(w.th), open = answerer(w), lf = liftIn(w.text);
    // The whole row opens the panel (the question holds agent and thread links, so it can't be a button itself); Answer is the keyboard's way.
    return h("li", { class: "cm-need", "data-key": w.key || w.th + ":" + (w.post || w.src), "data-open": open ? "" : null, onclick: open ? (e, n) => { if (!e.target.closest("a,button")) open(e, n); } : null },
      // A trailing ", in #thread" goes to the meta line, as on the card (V3 handoff point 9), so no pill sits mid-sentence.
      h("p", { class: "cm-need-q" }, lineText(lf.text)),
      h("p", { class: "cm-need-m meta" }, h("a", { href: to }, needSource(w)), lf.th && lf.th !== w.th ? [" \u00b7 in ", h("a", { class: "th", href: threadHref(lf.th) }, threadTitle(lf.th))] : null,
        who ? [" \u00b7 ", simpleOn() || !isCode(who) ? plainName(who) : agentRef(who, { dot: false, pill: true })] : null, w.t ? [" \u00b7 ", when(w.t)] : null),
      open ? h("div", { class: "cm-need-b" }, laterButtons(w), h("button", { type: "button", class: "btn cm-need-a", "data-size": "s", onclick: open }, "Answer"))
        : h("a", { class: "btn cm-need-a", "data-size": "s", href: to }, "Answer"));
  };
  return h("ul", { class: "cm-needs", "data-key": "needs", "aria-label": "Needs you" }, ws.slice(0, HOME_WAITS).map(row),
    ws.length > HOME_WAITS ? h("li", { class: "cm-need-all" }, h("a", { href: needsHref() }, `All ${ws.length}`)) : null);
}
/** Stuff to address (the person, 11081245f002, 110813526b10): what waited on them past its time (core/needs.js olderWhy: a day if its work moved on, two if quiet), folded into one group instead of the
 * needs-you list, to go through with an agent when they have time. Shut by default; the button puts the ask in the home box for them to send. */
const OLDER_ASK = "Go through the older things in Stuff to address with me: clear the ones that are done, obsolete or no longer the focus, decide what an agent can, and bring me only what really needs me, one at a time.";
function homeOlder(ws) {
  if (!ws.length) return null;
  const row = w => h("li", { class: "cm-old", "data-key": w.key || w.th + ":" + (w.post || w.src) },
    h("a", { href: w.src === "summary" && w.focus ? href("focus", w.focus) : threadHref(w.th) }, needSource(w)),
    h("span", { class: "cm-old-q" }, " \u2014 ", firstLine(w.text)), w.t ? h("span", { class: "meta" }, " \u00b7 ", when(w.t)) : null);
  return h("section", { class: "cm-older", "data-key": "older", "aria-label": "Stuff to address" },
    h("details", { class: "cm-older-d" },
      h("summary", null, h("b", null, "Stuff to address"), h("span", { class: "meta" }, ` \u00b7 ${plural(ws.length, "older thing")}, likely moved past or not urgent`)),
      h("ul", { class: "cm-old-l" }, ws.slice(0, 12).map(row),
        ws.length > 12 && needsHref() ? h("li", { class: "cm-need-all" }, h("a", { href: needsHref() }, `All ${ws.length}`)) : null)),
    can("post") ? button("Go through them with an agent", () => prefill(null, OLDER_ASK), { kind: "quiet", size: "s" }) : null);
}
const firstLine = t => { const s = mdPlain(String(t || "")).replace(/\s+/g, " ").trim(); return s.length > 140 ? s.slice(0, 139) + "\u2026" : s; };
function fociGrid(ctx) {
  if (S.foci === undefined) return h("p", { class: "meta cm-foci-msg" }, fociFailed() ? "Couldn't read the foci. It keeps trying." : "Reading\u2026");
  const fs = fociOrder();
  if (!fs.length) return h("p", { class: "meta cm-foci-msg" }, "No focus yet. Agents make one when work has a goal; every conversation is in the list on the left.");
  return h("div", { class: "cm-foci", "data-key": "foci" }, fs.map(f => focusCard(f, ctx)));
}
/** #comms/~archived: every archived conversation, newest first, each with Bring back (the person, 11081454b6bd). Archived by hand or by age (ui/threadmeta.js). */
function archivedPane(c, ctx) {
  const mask = showMask(), back = th => tryMeta(th, { archived: false }, ctx);
  const ds = [...S.threads.values()].filter(d => archived(d.name) && (threadKinds(d) & mask) && theirs(d)).sort((a, b) => b.last - a.last);
  const rows = ds.map(d => { const lp = d.posts.at(-1);
    return { key: d.name, href: threadHref(d.name), title: threadTitle(d.name), meta: ago(d.last).replace(" ago", ""), text: lp.from === "person" ? ["You: ", ...railSay(mdPlain(displayText(lp)))] : railSay(mdPlain(displayText(lp))),
      acts: can("post") ? button("Bring back", () => back(d.name), { size: "s", kind: "quiet", icon: "archive", title: "Back in the list", cls: "cm-arch-back" }) : null }; });
  return h("div", { class: "cm-home cm-arch", "data-key": "archived" },
    h("a", { class: "cm-arch-up only-phone", href: href("comms", "~list") }, icon("chevron-left"), "Conversations"),
    h("h1", { class: "cm-home-t" }, "Archived"),
    h("p", { class: "meta" }, "Out of the list: archived by hand, or quiet for a day (a week when it ran longer). Something new for you brings one back by itself."),
    list(rows, { empty: "Nothing archived." }));
}
function homePane(ctx) {
  const w = can("post"), n = needsN(), local = outFor(null), room = hasRoom(), digest = app();   // n: the one count (core/needs.js), the same as the Chat badge
  if (digest) { loadFoci(); loadNeedsYou(); }   // the one needs-you load (core/needs.js): the line, the badge and the cards read S.needsYou
  // Only on arriving: taking focus on every render pulled the page back to the box after any real click elsewhere on it.
  const arrived = C.arrived; C.arrived = false;
  // The line counts the last three days and names the rest as older, so new things don't hide behind stale ones (the person, 110812234faa); the Chat badge and Yours keep the whole count.
  // Older ones (olderWhy: a day on work that moved on, two on quiet work) are out of the count and folded into Stuff to address below (the person, 11081245f002).
  const sp = needsSplit();
  const waits = (n ? `${plural(n, "thing")} ${n === 1 ? "waits" : "wait"} on you` : "Nothing new waits on you") + (sp.older ? `, ${plural(sp.older, "older one")} in Stuff to address` : "");
  const line = n || sp.older ? (needsHref() ? h("a", { href: needsHref(), title: "What waits on you, newest first, each in its conversation" }, waits) : waits) : simpleOn() || S.live.signin?.length ? null : IB.unread ? "No agent is waiting on you" : "Nothing needs you";
  return h("div", { class: "cm-home", "data-key": "home" },
    w ? h("div", { class: "cm-new", "data-key": "new" },
      h("h1", { class: "cm-home-t" }, "What do you want done?"),
      h("p", { class: "cm-home-l" }, simpleOn() ? "Say it the way you'd tell a person. Your Hive starts on it and answers here." : "Say it the way you'd tell a person. An agent starts on it and answers here; it may bring in helpers if the job has parts."),
      !room && !hiveStopped() ? h("p", { class: "cm-home-warn", "data-tone": "limited", role: "status" }, statusDot("limited"), simpleOn() ? "Your Hive has used up its allowance for now, so new work waits until it comes back." : (((S.app && S.app.setup && S.app.setup.logins) || []).some(l => l.signed_in) ? "Every Claude account is past its usage line, so new work waits until there's room. See Usage." : "No Claude account is signed in yet. Sign in under Settings > Accounts so new work can start.")) : null,
      composer({ thread: null, update: ctx.update, place: "home", autofocus: arrived && conversation(ctx.arg).open }),
      !n && !local.length && ![...S.threads.values()].some(d => d.posts.some(p => p.from === "person")) ? firstJobs() : null,
      local.some(it => it.state !== "failed") ? closeLine() : null,
      local.length ? h("div", { class: "cm-out", "data-key": "out" }, local.map(it => localMessage(it, outActions))) : null) : null,
    line ? h("p", { class: "cm-hline", "data-key": "line", "data-needs": n ? "" : null }, line) : null,
    n ? homeNeeds(waitingNow()) : null,
    sp.older ? homeOlder(waitingOlder()) : null,
    digest ? fociGrid(ctx) : null,
    phoneList());
}
/** On a phone the rail is a page of its own; home lists the latest conversations instead (css/phone.css shows it only there). */
function phoneList() {
  const ds = [...S.threads.values()].filter(d => !isSystemThread(d) && !archived(d.name) && !faded(d.name)).sort((a, b) => (pinned(b.name) - pinned(a.name)) || (b.last - a.last)).slice(0, 20);
  if (!ds.length) return null;
  return h("section", { class: "sec only-phone cm-plist", "data-key": "plist", "aria-label": "Conversations" },
    h("header", { class: "sec-h" }, h("h2", { class: "sec-t" }, "Conversations")),
    h("ul", { class: "list" }, ds.map(d => { const st = threadState(d.name, seen);
      return h("li", { class: "lr", "data-key": d.name }, h("a", { class: "lr-in", href: threadHref(d.name) },
        h("span", { class: "lr-t" }, statusDot(st.key), h("b", null, threadTitle(d.name)), h("span", { class: "meta" }, d.last ? ago(d.last) : "")),
        st.label && st.key !== "done" ? h("span", { class: "lr-x" }, st.label) : null)); })));
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
  badge: () => { if (!app() || !S.live) return null; const n = needsN(); return n ? String(n) : null; },
  poll: { every: 3000, run: async ctx => { if (await pollDelivered()) ctx.update(); } },
  escape: () => { if (closeQuick()) return; if (C.menu || C.find) { C.menu = null; if (!C.q.trim()) C.find = false; L.ctx && L.ctx.update(); return; } if (!cancelReply() && asideOpen()) closeAside(); },
  enter: () => { C.arrived = true; },
  leave: () => { clearTimeout(qTimer); C.menu = null; C.find = false; },
  render(ctx) {
    if (!S.live) return h("div", { class: "page" }, S.failed ? empty("Couldn't read the conversations. It keeps trying.", { kind: "error" }) : empty("Reading…", { kind: "loading" }));
    return frame(ctx, conversation(ctx.arg));
  },
};

/** A focus page in the chat's own layout (views/focus.js): the rail as in a conversation, the page in the conversation's place, its section marked in the rail. */
export function inChat(ctx, id, page) {
  const pane = h("div", { class: "cm-fpane", "data-key": "focus" },
    h("a", { class: "btn cm-back", href: href("comms"), "data-kind": "icon", "aria-label": "Chats", title: "Chats" }, icon("chevron-left")),
    page);
  return frame(ctx, { arg: "focus:" + (id || ""), focus: id || "" }, pane);
}
/** One of Chat's own pages (Files and links, made.js) the same way: the rail beside it, its link in the rail's foot lit (review M2: the design's Files has the rail). */
export function inChatPlace(ctx, place, page) {
  const pane = h("div", { class: "cm-fpane", "data-key": "place" },
    h("a", { class: "btn cm-back", href: href("comms"), "data-kind": "icon", "aria-label": "Chats", title: "Chats" }, icon("chevron-left")),
    page);
  return frame(ctx, { arg: "place:" + place, place }, pane);
}
// Functions, not consts: focus.js reads them while this module, which imports it, is still loading.
export function chatEscape() { if (closeQuick()) return; if (C.menu) { C.menu = null; L.ctx && L.ctx.update(); } }
export function chatLeave() { clearTimeout(qTimer); C.menu = null; C.find = false; }

/** The layout: the rail, and in the other pane the home, a conversation, or what pane brings (a focus). */
function frame(ctx, c, pane) {
    L.ctx = ctx;
    if (C.conv !== c.arg) { Object.assign(C, { conv: c.arg, q: C.next || "", next: null, limit: FIRST, menu: null, find: false }); }
    const here = c.focus != null ? "f:" + c.focus : null;
    if (here && here !== L.here && FOLD.delete(here)) saveFold();   // opening a focus page opens its section in the rail; its heading folds it again
    L.here = here; L.place = c.place || null;
    L.opts = { showThread: !c.thread, mark: words(C.q) };
    refresh(c);
    queueMicrotask(() => sync(c, ctx));
    if (c.thread && app()) { const un = unreadIn(c.thread), sig = c.thread + un.map(x => x.id).join(); if (un.length && sig !== L.marked && !document.hidden) { L.marked = sig; markRead({ ids: un.map(x => x.id) }); } }
    const top = document.getElementById("top"), post = S.app.app && can("post");
    const convo = pane || (c.archived ? archivedPane(c, ctx) : c.home ? homePane(ctx)
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
        post && c.all ? h("p", { class: "cm-hint meta", "data-key": "hint" }, "To start something new, use New conversation.") : null]);
    return h("div", { class: "cm", "data-key": "comms", "data-in": c.arg ? "" : null, "data-home": c.home && !c.list ? "" : null, "data-list": c.list ? "" : null, style: `--top-h:${top ? top.offsetHeight : 47}px;--cm-w:${sideW}px` },
      h("nav", { class: "cm-side", "aria-label": app() ? "Conversations" : "Threads" }, grip(),
        h("div", { class: "cm-filters" },
          // R14: the phone's way to the rail. Home has one row that opens it as a page; that page has a way back (css/phone.css shows these only there).
          app() && c.home && !c.list ? h("a", { class: "cm-listbtn only-phone", href: href("comms", "~list"), "data-key": "listbtn" }, icon("search"), h("span", null, "Conversations"), h("span", { class: "meta" }, "search, filter, by focus"), icon("chevron-right")) : null,
          app() && c.list ? h("div", { class: "cm-lhead only-phone", "data-key": "lhead" }, h("a", { class: "btn cm-back", href: href("comms"), "data-kind": "icon", "aria-label": "Chats", title: "Chats" }, icon("chevron-left")), h("h1", null, "Conversations")) : null,
          post ? button("New conversation", null, { href: href("comms", "~new"), kind: "quiet", icon: "new", cls: "cm-newbtn" }) : null,
          app() && !simpleOn() ? places() : null,
          h("div", { class: "cm-srow" },
            h("div", { class: "cm-search" }, icon("search"),
              input({ type: "search", placeholder: app() ? "Search, or @ to ask" : "Search threads", "aria-label": "Search all conversations by name or content. Start with @ to ask an agent, which answers here and brings up the conversations that help", value: C.tq, oninput: e => { C.tq = e.target.value; ctx.update(); }, onkeydown: e => { if (e.key === "Enter" && aiQ()) { e.preventDefault(); aiAsk(ctx); return; } if (e.key === "Escape") { e.stopPropagation(); C.tq = ""; e.target.value = ""; e.target.blur(); ctx.update(); } } })),
            simpleOn() ? null : showBtn(ctx)),
          app() && c.list && !simpleOn() ? viewBar(ctx) : null,
          simpleOn() || C.menu !== "show" ? null : showMenu(ctx),
          app() ? brought() : null,
          C.err ? h("p", { class: "fmsg", "data-tone": "bad", role: "alert" }, C.err) : null),
        h("div", { class: "cm-tl", "data-own": "", "data-sig": "own", tabindex: "-1" })),
      h("section", { class: "cm-convo", "aria-label": "Conversation", ...(post && c.thread ? dropZone(c.thread) : {}) },
        convo,
        !c.thread ? quickPanel(ctx.update) : null,
        post && c.thread ? h("div", { class: "cm-drop", "aria-hidden": "true", "data-key": "drop" }, "Drop files to attach them") : null));
}

/** The read-only footer: a lock, one sentence, and (in the developer look, which has no strip) the thread's live agents inline, no actions. */
function readOnly(th) {
  const run = app() ? [] : (S.live ? S.live.agents : []).filter(a => a.thread === th && a.alive);
  return h("div", { class: "cm-ro", "data-key": "ro" }, icon("lock"), h("span", null, "Read-only here. This address can't post."),
    run.length ? h("span", { class: "cm-ro-a" }, run.map(a => h("span", { class: "cm-ro-l", "data-key": a.name, title: a.doing || null }, statusDot(agentState(a).key), agentRef(a.name), a.doing ? h("span", { class: "meta" }, short(a.doing, 48)) : null))) : null);
}

// A header menu (title, More) closes on a press anywhere outside it and its button (the person, 2026-11-04, post 11040100216b).
// So does an empty "Search here" box (11040131d9c5); with words in it, it stays, since it's what the messages are filtered by.
if (typeof document !== "undefined") document.addEventListener("pointerdown", e => {
  const menu = C.menu && !e.target.closest?.(".cm-menu, .cm-head [aria-haspopup], .fc-moreb, .cm-showbtn, .scrim");
  const find = C.find && !C.q.trim() && !e.target.closest?.(".cm-find, .cm-tools [aria-label=\"Search here\"]");
  if (!menu && !find) return;
  if (menu) C.menu = null; if (find) C.find = false;
  L.ctx && L.ctx.update();
}, true);
