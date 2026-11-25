// Comms (Chat, in the app): conversations on the left, one conversation on the right, the composer under it.
// #comms              in the app: home — start something, and what needs you. On the developer route: everything.
// #comms/~new         the same home, open on a phone (where the list and a conversation take turns)
// #comms/~all         every message · #comms/<thread> · #comms/@name by or to one name · #comms/@a+b between two names
//
// The thread list and the message list are data-own: morph leaves them alone and sync() below writes them, once the
// render has been mounted. A poll with nothing new touches nothing in them; a new post is appended, not redrawn, before
// the messages still on their way (.cm-out), which sync() keeps apart.
import { h, morph, tr, replaceKeep } from "../core/dom.js";
import { S, SYS, can, OUTSIDE, built, post as apiPost, poll, jget } from "../core/store.js";
import { simpleOn, plainName, hiveName } from "../core/simple.js";
import { loadNeedsYou, needsKnown, needsN, needsSplit, needsHref, waitsOn, waitOf, waitingOn, waitingNow, waitingOlder } from "../core/needs.js";
import { workBar, workBarHeld } from "../ui/workbar.js";
import { loadFoci, fociFailed, fociOrder, focusOf, focusById, activeThreads, askedOf, yoursN, needAt, agentsOf, stateOf, pills, plainPills, needSource, isCode, scrub, railSay, railTitle, threadLine, focusName, MISC, activityOf, shareOf, fractionWords, pct, usd, needsOf, moveFocus, foci, workingIn, leadOf, cardSince, newSince, hiddenAsks, markSeen as lookAt, seenLeave } from "../core/foci.js";
import { newIn } from "./focus.js";
import { SIDE_THREAD, agentLabel, capFirst, ownStep, plainStep, inConversation, labelCodes, chatText, agentState, agentStatus, btwParent, threadParent, threadChildren, isAgent, tierOf, threadTitle, threadState, threadStand, jobsFor, heldForYou, carryOn, threadPeople, isSystemThread, SHOW, postKind, threadKinds, closingPosts, displayText, postSource, inUse, limitStop, retryWords, fresh7, asksQuestion, fileInfo } from "../core/model.js";
import { ago, dayName, stamp, clock, dur, short, mdPlain, bareLinks, now, plural, DAY, MIN, HOUR, money } from "../core/format.js";
import { href, threadHref, go, parse } from "../core/router.js";
import { yoursSearch, yoursText } from "../core/yours.js";
import { tier, status } from "../core/status.js";
import { askTier, askStop, stopButton } from "../actions.js";
import { seen, unreadIn, markRead, onInbox, IB } from "../core/inbox.js";
import { MN, onMail } from "../core/mailnew.js";
import { heldRoom } from "../core/room.js";
import { bar as skBar, button, empty, icon, input, list, scrim, seg, skeleton, statusDot, statusWord } from "../ui/kit.js";
import { agentRef, avatars, copyButton, threadRef } from "../ui/refs.js";
import { message, messageHead, messageReceipt, localMessage, draftMessage, markWords, pollDelivered, marksSig, approvalLine, delivery, onJump, helperFolds, chatPages, pageEdit } from "../ui/message.js";
import { chatView } from "../rich/artifact.js";
import { openSideList, sideChats, sync as scSync } from "../ui/sidechat.js";
import { openQuick, quickOpen, closeQuick, quickPanel, laterButtons, setLater } from "../ui/quick.js";
import { pickBar, onPick } from "../ui/pick.js";
import { when } from "../ui/when.js";
import { loadTiers, focusTierChip, threadTierChip, pauseOf, askPause, pausedStrip } from "../ui/worktiers.js";
import { downloadMarkdown, printScript } from "../ui/export.js";
import { shareDialog } from "../ui/share.js";
import { pinned, important, faded, meta as tmeta, archived, setMeta, renameDialog, openCard, openFor, doneFor, markSeen, threadCrumb, titleExtras } from "../ui/threadmeta.js";
import { callFor, hangUp } from "../ui/phone.js";
import { brought } from "../ui/brought.js";
import { firstJobs, closeLine } from "../ui/firstjobs.js";
import { hiveStopped, TOPX } from "../ui/shell.js";
import { openApproval, forCard, cardWhy, pollCard } from "../ui/rootword.js";
import { composer, interruptButton, cancelReply, replyTo, say, outFor, outSig, actions as outActions, dropZone, recipient, hasDraft, quoteRef, prefill } from "../ui/composer.js";

const STEP = 500, FIRST = 250, NEAR = 60, GROUP = 10 * MIN, HELPER_AFTER = 3 * MIN;
const NONE = [];
const app = () => S.app.app;

// What the person chose. C.next: the message search a thread opened from the search box starts with.
// The conversation list's width: drag its edge, or use the arrow keys on it. Kept on this device.
const SIDE_MIN = 220, SIDE_MAX = 640, CHAT_MIN = 480;
let sideW = (() => { try { return Math.min(SIDE_MAX, Math.max(SIDE_MIN, +localStorage.getItem("cm.side") || 280)); } catch (e) { return 280; } })();
try { document.documentElement.style.setProperty("--rail-now", sideW + "px"); } catch (e) {}   // the New cell in Chat's bar follows the stored rail width (Nov 9 handoff §A.3)
// The list never takes the conversation's room: a width saved in a wide window waits for the window to grow again (the same limit is in .cm's columns, components.css).
const sideRoom = box => Math.max(SIDE_MIN, box.clientWidth - CHAT_MIN), sideNow = box => Math.min(sideW, sideRoom(box));
const setSide = (box, w) => { sideW = Math.round(Math.min(SIDE_MAX, sideRoom(box), Math.max(SIDE_MIN, w))); box.style.setProperty("--cm-w", sideW + "px"); railNow(); try { localStorage.setItem("cm.side", sideW); } catch (e) {} };
// A conversation head narrower than 600 px (a list pane dragged wide in a 1100 window) drops the crumb and says "All" for "Show every message",
// so the title keeps its room (round 16: it was cut to one letter). #main is the CSS container, so a container query can't see the column;
// a container on the head would pull in every phone rule written for #main. The render carries the mark, since morph() drops attributes it didn't set.
let headTight = false, headSeen = null;
const headRO = typeof ResizeObserver === "function" ? new ResizeObserver(es => { for (const e of es) { headTight = e.contentRect.width < 600; e.target.toggleAttribute("data-tight", headTight); } }) : null;
const watchHead = () => { const n = document.querySelector(".cm-head"); if (headRO && n && n !== headSeen) { if (headSeen) headRO.unobserve(headSeen); headRO.observe(headSeen = n); } };
// Hide the whole list so the conversation and the side panel get the full width (the person, 111412344c7d). Kept on this device; desktop only
// (the phone's list is its own page). Ctrl+\ or the head's first button toggles it; the New cell in the bar then drops its rail width.
let sideHid = (() => { try { return localStorage.getItem("cm.sidehid") === "1"; } catch (e) { return false; } })();
const railNow = () => document.documentElement.style.setProperty("--rail-now", sideHid ? "auto" : sideW + "px");
railNow();
function toggleSide() { sideHid = !sideHid; try { localStorage.setItem("cm.sidehid", sideHid ? "1" : "0"); } catch (e) {} railNow(); L.ctx && L.ctx.update(); }
if (typeof document !== "undefined") document.addEventListener("keydown", e => {
  if (e.key !== "\\" || !(e.ctrlKey || e.metaKey) || e.altKey || e.defaultPrevented || !document.querySelector(".cm .cm-side")) return;
  e.preventDefault(); toggleSide();
});
const sideBtn = () => h("button", { type: "button", class: "btn cm-sidebtn", "data-kind": "icon", "data-key": "sidebtn", "aria-pressed": String(!sideHid), "aria-label": sideHid ? "Show the conversation list" : "Hide the conversation list",
  title: (sideHid ? "Show" : "Hide") + " the conversation list (Ctrl+\\)", onclick: toggleSide }, icon("sidebar"));
const grip = () => h("div", { class: "cm-grip", role: "separator", "aria-orientation": "vertical", "aria-label": "Resize the conversation list", tabindex: "0", "data-key": "grip", title: "Drag to resize, or click to widen a step (from the widest it goes back to the narrowest); double-click resets",
  // A click that doesn't move is the one-press way (WCAG 2.5.7): a step wider, and from the widest back to the narrowest.
  onpointerdown: e => { e.preventDefault(); const g = e.target.closest(".cm-grip"), box = g.closest(".cm"), x0 = e.clientX, w0 = sideNow(box); g.setPointerCapture(e.pointerId); g.dataset.on = "1";
    g.onpointermove = m => { if (Math.abs(m.clientX - x0) >= 4) setSide(box, w0 + m.clientX - x0); };
    g.onpointercancel = () => { delete g.dataset.on; g.onpointermove = g.onpointerup = g.onpointercancel = null; };
    g.onpointerup = u => { g.onpointercancel(); if (Math.abs(u.clientX - x0) < 4) setSide(box, w0 >= Math.min(SIDE_MAX, sideRoom(box)) - 4 ? SIDE_MIN : w0 + 80); }; },
  ondblclick: e => setSide(e.target.closest(".cm"), 280),
  onkeydown: e => { const d = e.key === "ArrowLeft" ? -24 : e.key === "ArrowRight" ? 24 : 0; if (d) { e.preventDefault(); const box = e.target.closest(".cm"); setSide(box, sideNow(box) + d); } } });
// The side list's two views (the person, 11071518 2bab): "focus" (the default) puts each conversation under its focus, in the order of the
// home cards, Other last, helpers folded under the one they came from; "recent" is every conversation in one flat list,
// newest activity first, helpers included. hideFaded leaves out the ones marked Likely not needed. Both kept in this browser.
const VIEW_KEY = "hive.comms.view", HIDE_KEY = "hive.comms.hidefaded";
const store = (k, v) => { try { localStorage.setItem(k, v); } catch (e) { /* this page only */ } };
const C = { view: (() => { try { const v = localStorage.getItem(VIEW_KEY); return v === "recent" ? v : "focus"; } catch (e) { return "focus"; } })(),
  hideFaded: (() => { try { return localStorage.getItem(HIDE_KEY) !== "0"; } catch (e) { return true; } })(),   // on unless the person turned it off (2026-11-08)
  err: "", imp: false, conv: undefined, q: "", show: null, commsNew: true, limit: FIRST, tq: "", scope: "recent", next: null, menu: null, more: null, find: false, strip: false };
/** A change to a conversation (pin, star, archive) is shown at once; when the server refuses it the list goes back, and this says why for a few seconds. */
const tryMeta = (th, patch, ctx) => setMeta(th, patch).catch(e => { C.err = "Couldn't save that: " + (e && e.message || "the server didn't answer") + "."; ctx.update(); });   // the error stays until its Close: no clock (WCAG 2.2.1)
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
// The stored number also carries COMMS_NEW while Agent comms has never been on in this browser (C.commsNew): it once hid the chip (E10); since
// 2026-11-16 the chip is always drawn (showChips) and the mark is only kept so stored choices read the same.
const SHOW_KEY = "hive.comms.kinds", OLD_KEY = "hive.comms.show", OLD = { you: SHOW.you, updates: SHOW.you | SHOW.updates, all: SHOW.ALL }, COMMS_NEW = 8;
function showMask() {
  if (simpleOn()) return SHOW.you | SHOW.updates;   // fixed: there's no Show bar here
  if (C.show === null) {
    try {
      const v = localStorage.getItem(SHOW_KEY); C.show = v !== null ? Number(v) & SHOW.ALL : OLD[localStorage.getItem(OLD_KEY)];
      C.commsNew = v !== null ? !!(Number(v) & COMMS_NEW) : C.show == null;
    } catch (e) { /* no storage: the default, for this page */ }
    if (C.show == null) C.show = app() ? SHOW.you | SHOW.updates : SHOW.ALL;
    if (C.show & SHOW.comms) C.commsNew = false;
  }
  return C.show;
}
function setShow(mask, ctx) {
  C.show = mask; if (mask & SHOW.comms) C.commsNew = false;
  try { localStorage.setItem(SHOW_KEY, mask | (C.commsNew ? COMMS_NEW : 0)); } catch (e) { /* no storage: this page only */ }
  ctx.update();
}
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
    if (s.limited) out.push({ ev: true, key: "ev:" + s.session + ":" + s.start, t: s.end, tone: "limited", text: simpleOn() ? "Your Hive took a break" : `${s.name} stopped at a usage limit`, sub: (u => (u ? `It carries on by itself after ${stamp(u)}.` : "It carries on by itself once there's room."))(s === S.lastSess[s.name] && S.limitedNow.has(s.session) && carryOn(S.limitedUntil[s.session], limitStop(), now(), !!S.limitedScope[s.session]).at) });   // when it carries on while it still holds, as the strip says (carryOn, HA-15); an older stop of a session that has carried on since names no new time
    else if (s.retry) out.push({ ev: true, key: "ev:" + s.session + ":" + s.start, t: s.end, tone: "limited", text: simpleOn() ? "Your Hive took a break" : `${(w => w[0].toUpperCase() + w.slice(1))(agentLabel(s.name))} stopped (${retryWords(s.retry)})`, sub: "It carries on by itself." });
    else if (s.exit && s.exit !== 143) out.push({ ev: true, key: "ev:" + s.session + ":" + s.start, t: s.end, tone: "failed", text: simpleOn() ? "Your Hive ran into a problem" : `${(w => w[0].toUpperCase() + w.slice(1))(agentLabel(s.name))} stopped with an error (exit ${s.exit})`, sub: simpleOn() ? "Send a message and it tries again." : "Its last message above is where it got to." });
  }
  return out;
}

/** Bring the matches up to date, looking only at posts not yet seen unless the conversation or filter changed. */
function refresh(c) {
  const src = c.thread ? (S.threads.get(c.thread) || { posts: NONE }).posts : c.home || c.focus != null || c.place ? NONE : S.posts;
  // With For you on, a run ending can promote a post already in the list, so the list is redone then. That must not move someone who has scrolled up.
  L.th = c.thread || null; if (c.thread && L.ctx) loadShown(c.thread, L.ctx);
  const mask = maskFor(c), ended = mask & SHOW.you ? endedRuns() : 0, base = [c.arg, C.q, mask].join("\0"), key = base + "\0" + ended, match = matcher(c, mask, ended);
  const evs = c.thread && !C.q ? threadEvents(c.thread) : [];
  // One "New" rule above the first For-you message not yet on screen, taken once as the conversation opens (the Nov 8 handoff §8.2)
  // Taken again on every visit (leave() forgets it), and not before the conversation's posts are read; "Got it" on the card clears it too.
  if (c.thread !== L.newTh && (!c.thread || S.threads.has(c.thread))) { L.newTh = c.thread; const o = c.thread ? openFor(c.thread) : null; L.newId = o && doneFor(c.thread) !== o.last ? o.items.reduce((a, p) => (p.t < a.t ? p : a)).id : null; }
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
// The message the reader is on (the first one in view) and where it sits, so a list drawn again puts that one back in the same place:
// keeping the distance from the end moved it by whatever changed below it (a new question, a Retry going away when a run starts).
const readAt = b => { const top = b.getBoundingClientRect().top; for (const c of b.children) { if (!c.dataset || !c.dataset.key) continue; const r = c.getBoundingClientRect(); if (r.bottom > top + 1) return [c.dataset.key, r.top]; } return null; };
const putBack = (b, at) => { const el = at && b.querySelector(`:scope > [data-key="${CSS.escape(at[0])}"]`); if (!el) return false; b.scrollTop += el.getBoundingClientRect().top - at[1]; return true; };
const sameTo = (a, b) => a.men.join() === b.men.join();
const sameSource = (a, b) => { const x = postSource(a), y = postSource(b); return (x && x.name + x.how) === (y && y.name + y.how); };

// One-line rule (the person, 11101818169f): the server says per post whether to draw it full, as one line or not at all (/api/thread-shown, modules/dash/shown_rule.py).
// A line opens on click or Enter and stays open for the visit; "Show every message" is one switch per conversation, remembered, so nobody is stuck behind the rule.
const SH = new Map(), OPENED = new Set(), ALL_KEY = "h2.showAllPosts";
const showAll = th => { try { return (JSON.parse(localStorage.getItem(ALL_KEY)) || []).includes(th); } catch { return false; } };
function setShowAll(th, on) { try { const l = new Set(JSON.parse(localStorage.getItem(ALL_KEY)) || []); on ? l.add(th) : l.delete(th); localStorage.setItem(ALL_KEY, JSON.stringify([...l])); } catch { /* this visit only */ } }
function loadShown(th, ctx) {
  const e = SH.get(th); if (!th || !app() || (e && now() - e.at < 5 * MIN)) return;
  // The first time a conversation opens, its messages wait for the rule (about a second) rather than draw in full and then fold
  // to a fifth of their height under the reader's eyes; past HOLD_MS, or if the rule fails, they draw in full.
  const first = !(e && e.map), done = v => { const ok = v && v.posts; SH.set(th, { map: ok ? v.posts : e ? e.map : null, at: ok ? now() : now() - 5 * MIN + 15e3 });   /* a failed read (the Hive away) keeps the last rule and tries again in 15 s, not after 5 min */ L.rebuild = true; L.keep = !first; ctx.update(); };
  SH.set(th, { map: e ? e.map : null, at: now(), wait: first });
  if (first) setTimeout(() => { const x = SH.get(th); if (x && x.wait) { x.wait = false; L.rebuild = true; ctx.update(); } }, HOLD_MS);
  jget("/api/thread-shown?thread=" + encodeURIComponent(th)).then(done).catch(() => done(null));
}
const HOLD_MS = 3000;
// The person's own posts never fold, so a conversation of only theirs (a new one) shows what was just sent at once.
const shownHeld = items => { if (C.q.trim() || !L.th || showAll(L.th) || !items.some(p => !p.ev && p.from !== "person")) return false; const e = SH.get(L.th); return !!(e && e.wait && !e.map); };
const shownOf = p => { if (C.q.trim() || !L.th || showAll(L.th)) return null; const e = SH.get(L.th); return e && e.map ? e.map[p.id] || null : null; };
function lineRow(p, sh, ctx) {
  // "14:10 ac5f5: ac5f5 taking round 17" read the agent's name twice once codes became names (design round 1111b): drop the author's own code when the post starts with it
  // named as the conversation's own messages name the agent ("Agent A"), not as it is named elsewhere ("Friction dash review2's agent A", a0ad9 #4)
  const line = inConversation(L.th || p.th, () => labelCodes(String(sh.line || "").replace(/^(\d\d:\d\d (@?[\w-]+): )@?\2\b[:,]?\s*/, "$1")));
  // The server writes the line's time from the board's UTC stamp; show it in the viewer's own clock, as every other time here is (the person, 112012278aed).
  const t = p.t || Date.parse(p.at), lt = t ? line.replace(/^\d\d:\d\d /, clock(t) + " ") : line;
  const open = () => { OPENED.add(p.id); L.rebuild = true; L.keep = true; L.ctx && L.ctx.update(); };
  return h("div", { class: "cm-oneline", "data-key": p.id, "data-show": KIND_NAME[postKind(p)] || "" },
    h("button", { type: "button", class: "cm-one-b", "aria-label": tr("Open the whole message") + ": " + tr(lt), title: "Open the whole message", onclick: open }, h("span", { class: "cm-one-t" }, lt)),
    (sh.also || []).map(a => h("p", { class: "cm-one-also meta" }, inConversation(L.th || p.th, () => labelCodes(a)))));
}
// Notes sent from a document's viewer (ui/docviewer.js) or a file preview (preview/markup.js) are the app's words, not the person's:
// the chat draws them as one line, "Comments on DOC sent", opened in full on a click; only what the person typed shows as their message (the person, 112211212a89).
const NOTES_RE = /^(?:\([^)\n]*\)\s*)?(?:@[\w-]+\s+)*Notes on (?:"([^"\n]+)"|([^\n]+?)) \([^)\n]*\), from (?:the side panel in this conversation|the Documents page|the file preview in this conversation)\.\s*\n/;
export function docNotes(p) {
  if (!p || p.ev || p.from !== "person") return null;
  const t = String(p.text || ""), m = NOTES_RE.exec(t);
  if (!m) return null;
  const n = (t.slice(m[0].length).match(/^(?:#\d+ |\d+\. )/gm) || []).length;
  return { title: m[1] || m[2], n };
}
function notesRow(p, dn) {
  const line = tr("Comments on") + " “" + dn.title + "” " + tr("sent") + (dn.n > 1 ? " · " + dn.n + " " + tr("notes") : "");
  const open = () => { OPENED.add(p.id); L.rebuild = true; L.keep = true; L.ctx && L.ctx.update(); };
  return h("div", { class: "cm-oneline cm-notes", "data-key": p.id, "data-show": "you", role: "note" },
    h("button", { type: "button", class: "cm-one-b", "aria-label": tr("Open the whole message") + ": " + line, title: "Open the whole message", onclick: open },
      icon("doc"), h("span", { class: "cm-one-t" }, line), h("span", { class: "meta" }, " · " + clock(p.t || Date.parse(p.at)))));
}
function evRow(e) {
  return h("div", { class: "cm-ev", "data-key": e.key, "data-tone": status(e.tone).tone, role: "note" }, statusDot(e.tone), h("b", null, e.text), h("span", { class: "meta" }, clock(e.t) + " · " + e.sub));
}
const KIND_NAME = { [SHOW.you]: "you", [SHOW.updates]: "updates", [SHOW.comms]: "comms" };
// ---- E12: edited messages and replies tried again, with the older versions kept ----
// The board only grows, so an edit is a new post with edit_of (its first version's id) and a retry a hidden post with retry_of (the reply's first id).
// A version runs from where it starts to where the next one starts; the chat draws the chosen one (the newest unless arrows picked another).
const VSEL = new Map(), V = { info: new Map(), old: false, wait: false, edit: null, retry: null, retryRoot: null };
// Only in one conversation, in the app's look, with no search: elsewhere every version shows as it was posted.
// Only the person's edit_of/retry_of count (posting refuses them from anyone else; this is the page's own check too).
function versioned(items) {
  V.info = new Map(); V.old = V.wait = false; V.edit = V.retry = V.retryRoot = null;
  if (!L.th || !app() || C.q.trim()) return items;
  const starts = new Map();
  for (const p of items) { const r = !p.ev && p.from === "person" && (p.edit_of || p.retry_of); if (r) (starts.get(r) || starts.set(r, []).get(r)).push(p); }
  const out = starts.size ? walk(items, starts) : items;
  if (!V.old) {   // Edit on your last message (its files go with the edit); Retry on the last reply once nothing of yours follows it.
    // Edit works while the reply is still being written: the starter stops that run and hands the agent the new ask (E12d). Retry waits until the run ends: under a "Taking this:" line it read as if that post had failed (friction-dash 3); Interrupt is there meanwhile.
    const posts = out.filter(p => !p.ev && !p.retry_of), li = posts.findLastIndex(p => p.from === "person" && !docNotes(p)), lastMe = posts[li];   // notes from a document aren't a message to edit
    V.edit = lastMe && !pageEdit(lastMe) ? lastMe.id : null;   // a page edited by hand is changed with the page's own Edit (E43)
    const after = posts.slice(li + 1).filter(p => isAgent(p.from)), lastAg = after.at(-1);
    if (lastAg && !V.wait && !workingIn(L.th)) { V.retry = lastAg.id; V.retryRoot = (V.info.get(after[0].id) || {}).root || after[0].id; }
  }
  return out;
}
// A version's part runs from its start to the next one's. A retry no reply has come to yet has no part to draw: the one before stays, and V.wait says one is coming.
function walk(seg, starts) {
  const ins = new Set(seg), i = seg.findIndex(p => !p.ev && starts.has(p.id) && starts.get(p.id).some(s => ins.has(s)));
  if (i < 0) return seg.filter(p => !p.retry_of);
  const root = seg[i], vs = [root, ...starts.get(root.id).filter(s => ins.has(s))], pos = vs.map(v => seg.indexOf(v));
  const part = j => seg.slice(pos[j], j + 1 < vs.length ? pos[j + 1] : seg.length);
  const idx = vs.map((_, j) => j).filter(j => part(j).some(p => !p.ev && !p.retry_of)), n = idx.length;
  if (n < vs.length) V.wait = true;
  const k = VSEL.has(root.id) ? Math.min(VSEL.get(root.id), n - 1) : n - 1; if (k < n - 1) V.old = true;
  const pt = part(idx[k]), head = pt.find(p => !p.ev && !p.retry_of);
  if (n > 1) V.info.set(head.id, { root: root.id, i: k, n });
  return [...seg.slice(0, i).filter(p => !p.retry_of), ...walk(pt, starts)];
}
const verRedraw = () => { L.rebuild = true; L.keep = true; L.ctx && L.ctx.update(); };
onPick(() => { L.rebuild = true; L.keep = true; });   // a one-tap choice held, undone or sent: the conversation's cached messages redraw
const verPick = (root, i, n) => { if (i >= n - 1) VSEL.delete(root); else VSEL.set(root, i); verRedraw(); };
const verOpts = p => ({ ver: V.info.get(p.id) || null, canEdit: V.edit === p.id, canRetry: V.retry === p.id, retryRoot: V.retry === p.id ? V.retryRoot : null, pick: verPick, redraw: verRedraw });
// E13: in one conversation, in the app's look, with no search, a page posted again shows as the newest version of the earlier one.
const pagesOn = () => L.th && app() && !C.q.trim();
function pagesOf(all) { if (pagesOn()) chatPages(L.th, all); }
const pageRows = items => (pagesOn() ? chatView(L.th, () => rows(items)) : rows(items));
function rows(items) {
  const out = [];
  for (const p of items) {
    const d = dayName(p.t);
    if (d !== L.day) { L.day = d; L.prev = null; out.push(h("div", { class: "cm-day", "data-key": "d:" + d }, h("span", null, d))); }
    if (p.ev) { out.push(evRow(p)); L.prev = null; continue; }
    const sh = shownOf(p);
    if (sh && sh.show === "hidden") continue;
    const dn = !OPENED.has(p.id) && docNotes(p);
    if (dn) { out.push(notesRow(p, dn)); L.prev = null; continue; }
    if (sh && sh.show === "line" && sh.line && !OPENED.has(p.id)) { out.push(lineRow(p, sh)); L.prev = null; continue; }
    L.names.add(p.from); for (const m of p.men) L.names.add(m);
    if (!L.opts.showThread && L.helpers) { const hs = helperFolds(p.th, p.t, L.helpers, L.opts); if (hs.length) { out.push(...hs); L.prev = null; } }   // helper conversations fold where they began (R7)
    if (p.id === L.newId) { out.push(h("p", { class: "msg-new", role: "separator", "aria-label": "New messages", "data-key": "new:" + p.id }, "New")); L.prev = null; }
    const q = L.prev, cont = !!q && q.from === p.from && q.th === p.th && !p.reply_to && p.t - q.t < GROUP && sameTo(q, p) && sameSource(q, p);
    const pm = docNotes(p) ? { ...p, text: p.text.replace(/\n(?=(?:#\d+ |\d+\. ))/g, "\n\n") } : p;   // opened notes: one paragraph per comment, not one run-on block
    const a = message(pm, { ...L.opts, cont, ...verOpts(p) }); a.dataset.show = KIND_NAME[postKind(p)] || ""; if (a.dataset.show === "you" && asksQuestion(p)) a.dataset.ask = "";   // For you, Updates or Agent comms, marked in the list (11040113eacf)
    L.prev = p; a._head = L.heads; L.io.observe(a); out.push(a);
  }
  return out;
}

function paint(box, ctx) {
  const all = versioned(L.all), shown = all.slice(-C.limit), older = all.length - shown.length;
  pagesOf(all);
  L.day = ""; L.prev = null; L.names = new Set(); L.shown = shown.length; L.helpers = new Set();
  if (older && shown.length && !L.opts.showThread) helperFolds(shown[0].th, shown[0].t - 1, L.helpers);   // the ones begun before what is drawn stay with it
  L.io.disconnect(); L.outSig = "";   // a fresh .cm-out: syncOut must fill it again
  L.out = h("div", { class: "cm-out", "data-key": "out" });
  L.jump = h("button", { type: "button", class: "cm-jump", hidden: true, onclick: () => { box.scrollTop = box.scrollHeight; } });
  const q = C.q.trim();   // a search narrows the messages: say so where they are, so a sent one never seems lost (11040214d468)
  replaceKeep(box,
    ...(q ? [h("div", { class: "cm-qbar", role: "status" }, h("span", null, `Showing ${plural(L.all.length, "match", "matches")} for “${short(q, 40)}”`),
      button("Clear search", () => { C.q = ""; C.find = false; ctx.update(); }, { size: "s", kind: "quiet", icon: "close" }))] : []),
    ...(older ? [h("div", { class: "cm-older" }, button(`Show older (${older} more)`, () => { C.limit += STEP; L.rebuild = true; L.keep = true; ctx.update(); }, { size: "s" }))] : []),
    ...(shown.length && shownHeld(shown) ? [h("div", { class: "empty cm-held", "data-kind": "loading", role: "status", "aria-busy": "true" }, "Loading the conversation…")]
      : shown.length ? [...pageRows(shown), ...(L.opts.showThread ? [] : helperFolds(shown.at(-1).th, Infinity, L.helpers, L.opts))] : [empty(C.q ? "No messages match." : L.src.length && !simpleOn() ? (app() ? "Nothing here for you yet. The rest is agents talking among themselves: Show hidden, in the title's menu, shows it." : "Nothing here with what Show has on. Turn on Agent comms to see all the messages.") : "No messages yet.")]),
    L.out, L.drafts = h("div", { class: "cm-drafts", "data-key": "drafts", "aria-live": "polite" }), L.jump);
  L.draftSig = "";
}
function setJump() {
  if (!L.jump) return;
  L.jump.hidden = !L.unseen;
  L.jump.textContent = L.unseen ? tr(`${plural(L.unseen, "new message")} ↓`) : "";
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

// E11: a reply still being written shows under the messages and grows each read (0.1-0.2 s while it is written, E11b/E41; else the 1.5 s poll); it goes once the post is on the board.
function syncDrafts(c, box) {
  const th = c.thread; if (!th || !L.drafts) return;
  const posted = a => a.draft && (S.threads.get(th)?.posts || []).some(p => p.from === a.name && p.t > now() - 10 * MIN && String(p.text || "").includes(a.draft.text.trim().slice(0, 60)));
  const fast = L.fast && L.fast.th === th ? L.fast.ds : [];   // E11b: the 0.3 s read is newer than the 1.5 s one
  // E42b: a fast read also brings the code each run here has run since its last post (steps), with or without a draft
  const ds = [...fast.map(f => ({ ...(S.running?.get(f.name) || {}), name: f.name, draft: f.draft, steps: f.steps || [], more: f.more || 0 })),
    ...(S.live ? S.live.agents : []).filter(a => a.alive !== false && a.draft && a.draft.thread === th && !fast.some(f => f.name === a.name))]
    .map(a => posted(a) ? { ...a, draft: null } : a).filter(a => a.draft || (a.steps && a.steps.length));
  const sig = ds.map(a => a.name + ":" + (a.draft ? a.draft.text.length : "-") + ":" + (a.steps || []).map(s => (s.running ? "r" : "d") + (s.out || "").length).join("/")).join();
  if (sig === L.draftSig) return;
  const stick = atEnd(box) || L.stuck; L.draftSig = sig;
  L.drafts.replaceChildren(...ds.map(a => inConversation(th, () => draftMessage(a))));   // the same scope as message(): a run still writing is named as its posts are
  if (stick) { box.scrollTop = box.scrollHeight; L.stuck = true; }
}

/** E11b: while the person's message here waits for its first reply (up to a minute), read just the drafts every 0.3 s,
 *  so the reply's first words show as they are written rather than at the next 1.5 s poll. */
function waitingReply(th) {
  const ps = S.threads.get(th)?.posts || [], last = ps[ps.length - 1], out = outFor(th).filter(x => x.state !== "failed").pop();
  if (last && pageEdit(last)) return false;   // a page edited by hand asks nobody (E43)
  const t = Math.max(out ? out.t : 0, last && last.from === "person" ? last.t : 0);
  return t > now() - MIN && !(last && last.from !== "person" && last.t >= t);
}
/** E41: a reply is being written in this chat right now (the last fast read had one, or the 1.5 s poll saw one): keep reading it
 *  fast to its last word, so a long reply, or a second post in the same run, streams instead of growing in 1.5 s steps. */
function draftLive(th) {
  return !!(L.fast && L.fast.th === th && L.fast.ds.some(d => d.draft)) || (S.live ? S.live.agents : []).some(a => a.alive !== false && a.draft && a.draft.thread === th);
}
/** E42b: an agent is working on this chat (its run is for this thread): read its steps every 0.5 s, so each command shows as it runs. */
function workingHere(th) {
  return (S.live ? S.live.agents : []).some(a => a.alive !== false && a.thread === th) || !!(L.fast && L.fast.th === th && L.fast.ds.length);
}
function fastDrafts(th) {
  if (!th || !can("read") || L.fastTh === th) return;   // its loop runs (L.fastTh is cleared only by the loop that owns it)
  if (!waitingReply(th) && !draftLive(th) && !workingHere(th)) { if (L.fast && L.fast.th === th) { L.fast = null; syncDrafts({ thread: th }, L.box); } return; }
  clearTimeout(L.fastTimer); L.fastTh = th;
  const tick = async () => {
    L.fastTimer = null;
    if (L.fastTh !== th) return;
    if (!L.box || !L.box.isConnected) { L.fastTh = null; return; }
    if (L.th !== th) { L.fast = null; L.fastTh = null; return; }   // the person opened another conversation: this one's reply must not be typed out there (person 11131127c75f)
    try { const ds = await jget("/api/drafts?steps=1&thread=" + encodeURIComponent(th)); if (L.fastTh === th && L.th === th) { L.fast = { th, ds: Array.isArray(ds) ? ds : [] }; syncDrafts({ thread: th }, L.box); } } catch (e) { /* the 1.5 s poll still carries drafts */ }
    if (L.fastTh !== th) return;   // another thread opened: its own loop runs
    if (waitingReply(th)) L.fastTimer = setTimeout(tick, 100);   // E11d: 0.1 s, not 0.3: only while a chat waits for its first reply (a few seconds), and the read is cached per out file
    else if (draftLive(th)) L.fastTimer = setTimeout(tick, 200);   // E41: 0.2 s while a draft grows (the model's text arrives every 0.2 s or less; a read costs under 1 ms)
    else if (workingHere(th)) L.fastTimer = setTimeout(tick, 500);   // E42b: 0.5 s while an agent works here, for its steps (the read is cached per log size)
    else { L.fast = null; L.fastTh = null; syncDrafts({ thread: th }, L.box); }
  };
  L.fastTimer = setTimeout(tick, 0);
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
        const end = atEnd(box); if (end) L.endTop = box.scrollTop;
        L.stuck = end || (L.stuck && !(performance.now() - L.hand < 1000));   // only the reader unsticks it: a scroll the browser makes when a message reflows late (a chart, code) keeps the end
        if (L.stuck && L.unseen) { L.unseen = 0; setJump(); }
        cardSeen(box);
        L.saved = L.stuck ? null : { key: L.key, fromEnd: box.scrollHeight - box.scrollTop };
      }, { passive: true });
      for (const t of ["wheel", "touchstart", "pointerdown", "keydown"]) box.addEventListener(t, () => { L.hand = performance.now(); }, { passive: true });
      // A wheel over a page in the chat (artifact.js) scrolls the chat without the chat hearing it; the page tells us, a moment late.
      // If the chat already moved up from the end by then, that was the reader: let go of the end (growth never moves it up).
      box.addEventListener("rc-hand", () => {
        L.hand = performance.now();
        if (L.stuck && L.endTop != null && box.scrollTop < L.endTop - 4) { L.stuck = false; L.saved = { key: L.key, fromEnd: box.scrollHeight - box.scrollTop }; }
      });
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
    const wsig = L.th + ":" + workingIn(L.th);   // Retry shows once the run here ends, which brings no post of its own
    if (wsig !== L.workSig) { if (L.workSig && L.workSig.startsWith(L.th + ":") && (V.retry || V.edit)) { L.rebuild = true; L.keep = !L.stuck && !atEnd(box); } L.workSig = wsig; }
    // E12: a new post can move the Edit and Retry buttons or add a version: draw the list again, where the reader is (at the end if there)
    if (!L.rebuild && L.pend.length && (V.info.size || V.wait || V.edit || V.retry || L.pend.some(p => p.edit_of || p.retry_of))) { L.rebuild = true; L.keep = !L.stuck && !atEnd(box); }
    if (L.rebuild) {
      const fromEnd = L.keep ? box.scrollHeight - box.scrollTop : isNew && L.saved && L.saved.key === L.key ? L.saved.fromEnd : null, at = L.keep ? readAt(box) : null;
      paint(box, ctx);
      L.rebuild = false; L.keep = false;
      syncOut(c, box);
      if (fromEnd != null) { if (!putBack(box, at)) box.scrollTop = box.scrollHeight - fromEnd; L.stuck = atEnd(box); }
      else { box.scrollTop = box.scrollHeight; L.stuck = true; }
      if (L.jumpTo) {
        const id = L.jumpTo, sel = `[data-key="${CSS.escape(id)}"]`, p = S.byId[id]; let el = box.querySelector(sel);
        if (!el && p) {   // older than what is drawn, or hidden by the Show setting: draw down to it (one more paint), or show everything
          const i = L.all.indexOf(p);
          if (i >= 0 && L.all.length - i > C.limit) { C.limit = L.all.length - i + 20; paint(box, ctx); el = box.querySelector(sel); }
          else if (i < 0 && showMask() !== SHOW.ALL && L.src.includes(p)) { setShow(SHOW.ALL, ctx); return; }
        }
        const held = !el && SH.get(L.th) && SH.get(L.th).wait;   // the messages wait for the one-line rule on a first visit: keep the jump until they are drawn
        if (!held) { L.jumpTo = null; if (el) flash(box, el); }
      }
      for (const el of box.children) L.ro.observe(el);   // a message that grows after it lands (a table or code drawn late) keeps the list at its end
    } else if (L.pend.length) {
      const stick = atEnd(box) || L.stuck;
      box.querySelector(":scope > .empty")?.remove();
      pagesOf(versioned(L.all));   // E12: Edit and Retry move to the newest message and reply; E13: a page posted again repaints its older copies
      for (const b of box.querySelectorAll(".msg-vact")) { const k = b.closest("[data-key]")?.dataset.key; if (k !== V.edit && k !== V.retry) b.remove(); }
      const fresh = pageRows(L.pend); L.out.before(...fresh); fresh.forEach(el => el instanceof Element && L.ro.observe(el));
      L.shown += L.pend.length;
      if (stick) box.scrollTop = box.scrollHeight;
      else { L.unseen += L.pend.filter(p => !p.ev && !OUTSIDE.has(p.from)).length; setJump(); }
    }
    L.pend = [];
    syncOut(c, box);
    syncDrafts(c, box); fastDrafts(c.thread);
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
  const ap = approvalLine(p);   // an approval line (rootword.js) is redrawn whole: what it says changes with root's record, and it may only now have become one
  if (ap || a.classList.contains("msg-appr")) { const n = ap || message(p, L.opts); n.dataset.show = a.dataset.show || ""; n._head = L.heads; const b = morph(a, n); b._head = L.heads; return; }
  const hd = a.querySelector(":scope > .msg-h, :scope > .msg-c > .msg-h"), rc = a.querySelector(":scope > .msg-rc, :scope > .msg-c > .msg-rc");
  if (hd) morph(hd, L.opts.showThread === false ? inConversation(p.th, () => messageHead(p, L.opts)) : messageHead(p, L.opts));   // the same scope as message(), or a refresh renames the agent
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
  found.sort((a, b) => a.rest.length - b.rest.length || b.hits.at(-1).t - a.hits.at(-1).t);   // a title or id that holds every search word before a thread that matched only in its posts (ae868, sidebar-sim)
  const all = C.tq.trim();
  return [
    simpleOn() ? null : { key: "(everything)", href: href("comms", "~all"), title: "All messages", text: found.length ? `${plural(total, "match", "matches")} in ${plural(found.length, app() ? "conversation" : "thread")}` : "Nothing matches", current: c.all, onClick: () => { if (c.all) C.q = all; else C.next = all; ctx.update(); } },
    ...found.map(({ d, hits, rest }) => ({ key: d.name, href: threadHref(d.name), current: c.thread === d.name, title: railTitle(d.name), meta: ago(hits.at(-1).t).replace(" ago", ""),
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
      return { key: r.thread, href: threadHref(r.thread), current: c.thread === r.thread, title: railTitle(r.thread), meta: ago(d.last).replace(" ago", ""), lead: tierLead(r.thread), text: r.why || "fits" }; });
    return rows.length ? rows : note("Nothing fits", "Try other words");
  }
  const a = aiAnswer();
  if (!a) return now() - AI.at < AI_WAIT ? note("Looking…", "An agent is looking it up") : note("No answer yet", "It shows here if it comes. Claude may be at its limit");
  const also = (S.shown || []).filter(r => r.from === a.from && r.at * 1000 >= AI.at - 5000 && S.threads.has(r.thread));   // the app's box above the list holds these; the developer look has no box
  return [...note(q, a.text, { whole: true }), ...(app() ? [] : also.map(r => ({ key: r.thread, href: threadHref(r.thread), current: c.thread === r.thread, title: railTitle(r.thread), text: r.why || "" })))];
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
  if (C.hideFaded && app()) ds = ds.filter(d => !faded(d.name) || workingIn(d.name) || d.name === c.thread);   // live work stays in sight (IM-1b)
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
      return { key: d.name, href: threadHref(d.name), current: c.thread === d.name, title: railTitle(d.name), meta: ago(d.last).replace(" ago", ""),
        lead: [tierLead(d.name), live ? h("span", { title: "an agent is on this thread now" }, statusDot("working", { live: true })) : null],
        text: `${d.posts.length} · ${lp.from}: ${short(mdPlain(lp.text).replace(new RegExp("^(@\\S+\\s+)*" + who + ":\\s*"), ""), 60)}`, sec: secOf(d.name, null), dim: faded(d.name) };
    }
    // The row says where the conversation stands in the mark and the word the top of its chat says it in (stand(), model.js threadStand), so the list
    // never says less or other than the chat it opens (HA-11; the board on 2026-11-16: 30 of 54 rows said nothing where the chat's top said Done,
    // Waiting on others, Asked earlier, Needs you, Stopped or Blocked). A conversation nothing was written about stands on who wrote last (HA-21: Quiet, or
    // Not picked up when the person wrote last and no agent read it), so only one with no message at all has a plain mark and no word.
    const s = stand(d.name), par = flat && threadParent(d.name), sLine = standLine(s);
    const say = inConversation(d.name, () => railSay(mdPlain(chatText(lp)))), fd = inConversation(d.name, () => fadeTag(d.name));
    const tag = standWord(s, sLine);
    const from = par ? [h("span", { class: "tm-from", title: "A helper conversation, started from " + threadTitle(par) }, "from " + short(threadTitle(par), 28)), " · "] : null;
    return { key: d.name, href: threadHref(d.name), current: c.thread === d.name, title: railTitle(d.name), meta: ago(d.last).replace(" ago", ""),
      lead: [tierLead(d.name), standMark(s)],
      text: [from, d.name !== c.thread && hasDraft(d.name) ? [h("span", { class: "cm-draft", title: "You left unsent text in this conversation" }, "Draft"), " · "] : null, tag, tag ? " · " : null, fd, fd ? " · " : null, lp.from === "person" ? ["You: ", ...say] : say], sec: secOf(d.name, true), dim: faded(d.name) };   // no agent code on the row: the conversation names its agent
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
    const open = KIDS.has(d.name) || kids.some(k => k.name === c.thread), { parts } = kidCount(kids.map(k => k.name));   // counted as their rows say it
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
// HA-18: the open conversation always has a row. When every section that holds it is folded, the first of them opens for this visit (L.lift); FOLD,
// the person's own choice, is not touched, so the fold is back when they leave. Folding it again during the visit keeps it shut until the next one.
// A section once opened so stays open for the visit, so opening another one that holds the conversation does not fold it again (k2408: run_important).
const VISIT = { conv: undefined, shut: new Set(), lift: null };
const isShut = k => FOLD.has(k) && k !== L.lift;
const foldShut = () => { const ss = L.foldSecs || [], ks = L.foldKids || []; return ss.length ? ss.every(isShut) : !ks.some(n => KIDS.has(n)); };
const foldWord = () => foldShut() ? "Expand all" : "Collapse all";
const foldNone = () => !(L.foldSecs || []).length && !(L.foldKids || []).length;
function foldAll(ctx) {
  const open = foldShut(), ss = L.foldSecs || [], ks = L.foldKids || [];
  if (open) { ss.forEach(k => FOLD.delete(k)); ks.forEach(n => KIDS.add(n)); }
  else { ss.forEach(k => FOLD.add(k)); KIDS.clear(); if (L.lift) VISIT.shut.add(L.lift); }
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
// Recent view: Important shows its 10 newest rows; the rest sit behind an "N more" row (sidebar-sim, a9ac6/ae868).
let impAll = false;
const IMP_CAP = 10, capImp = (k, rows) => k === "imp" && C.view === "recent" && !impAll && rows.length > IMP_CAP ? rows.filter((r, i) => i < IMP_CAP || r.current) : rows;   // the open conversation keeps its row (HA-18)
const moreImp = (k, rows, ctx) => k === "imp" && C.view === "recent" && !impAll && rows.length > IMP_CAP
  ? h("button", { type: "button", class: "tm-sec-more meta", onclick: () => { impAll = true; ctx.update(); } }, `${rows.length - IMP_CAP} more`) : null;
function sections(rs, ctx, opts) {
  if (words(C.tq).length) return list(rs, opts);
  const by = { pin: [], imp: [], need: [], rest: [], arch: [] }, tops = [];
  const byFocus = byFocusOn();
  if (byFocus) for (const f of fociOrder()) { const k = "f:" + (f.id || ""); if (!by[k]) { by[k] = []; tops.push(k); } }
  for (const r of rs) { const k = r.sec || "rest"; if (k.startsWith("f:") && !by[k]) { by[k] = []; tops.push(k); } (by[k] || by.rest).push(r); }
  // The rail's Focus view puts the focuses in the List's groups, Needs you first, while the home shows the List (the cards keep the person's own order, which the rail then keeps too), and marks each with the List's own dot (HA-7: one function, groupStand).
  const stands = new Map(); if (byFocus) for (const k of tops) { const f = k === "f:" ? null : focusById(k.slice(2)); if (f) stands.set(k, groupStand(f)); }
  const order = new Map(tops.map((k, i) => [k, k === "f:" ? 1e9 : (stands.get(k) && fociView() === "list" ? stands.get(k).at : 0) * 1e4 + i])); tops.sort((a, b) => order.get(a) - order.get(b));
  const label = k => k === "f:" ? "Other" : focusName(focusById(k.slice(2)) || { name: k.slice(2) });
  const parts = [...(C.view === "recent" ? [SECS[0], SECS[2], SECS[1]] : SECS.slice(0, 3)), ...tops.map(k => [k, label(k)]), SECS[3]].filter(([k]) => by[k].length || (byFocus && k.startsWith("f:") && k !== "f:")), heads = parts.length > 1 || by.pin.length;
  L.foldSecs = heads ? parts.map(([k]) => k).filter(k => k !== "f:") : [];   // Other has no fold
  if (foldFresh && heads) { foldFresh = false; L.foldSecs.forEach(k => k !== L.here && FOLD.add(k)); saveFold(); }   // a focus page open beside it keeps its own section open
  if (VISIT.conv !== C.conv) { VISIT.conv = C.conv; VISIT.shut.clear(); VISIT.lift = null; }
  const curAt = heads ? parts.map(([k]) => k).filter(k => by[k].some(r => r.current)) : [];
  if (curAt.length && curAt.every(k => k !== "f:" && FOLD.has(k)) && !curAt.some(k => VISIT.shut.has(k))) VISIT.lift = curAt[0];
  L.lift = VISIT.lift && curAt.includes(VISIT.lift) && FOLD.has(VISIT.lift) && !VISIT.shut.has(VISIT.lift) ? VISIT.lift : null;
  const sec = (k, label, rows) => {
    if (!heads) return h("div", { class: "tm-sec", "data-key": "sec:" + k, "data-sec": k }, list(rows, { ...opts, empty: "" }));
    if (k === "f:") return h("div", { class: "tm-sec", "data-key": "sec:" + k, "data-sec": k },   // Other: a plain heading, its rows always shown
      h("div", { class: "tm-sec-hw" }, h("p", { class: "tm-sec-h tm-sec-other" }, label)), list(rows, { ...opts, empty: "" }));
    const here = L.here === k, shut = isShut(k), fid = k.startsWith("f:") ? k.slice(2) : null;   // the focus page open beside it: its section is marked; pressing its heading again folds it, the page stays
    // folding a section the open conversation is in keeps it shut for this visit too, or the lift above would open it again at once (k2408: drive_threads)
    const toggle = () => { if (k === L.lift) VISIT.shut.add(k); else { if (FOLD.has(k)) FOLD.delete(k); else { FOLD.add(k); VISIT.shut.add(k); } saveFold(); } ctx.update(); };
    // The count is its own span after the name, with a dot before it when a row waits on you or is working (the Nov 8 handoff §2.1); + takes its place on hover.
    const tone = rows.some(r => waitsOn(r.th)) ? "info" : rows.some(r => threadState(r.th).key === "working") ? "good" : null;
    const count = h("span", { class: "tm-sec-n", title: plural(rows.length, "conversation") + (tone === "info" ? ", one waits on you" : tone ? ", one working" : "") }, tone ? h("span", { class: "st", "data-tone": tone, "aria-hidden": "true" }) : null, String(rows.length));
    const dnd = focusDnd(fid, ctx);
    // A focus's heading is a link to its page (the person, 11080309f795); the chevron before it folds the section. Other headings fold as a whole.
    return h("div", { class: "tm-sec", "data-key": "sec:" + k, "data-sec": k, "data-folded": shut ? "" : null, "data-here": here ? "" : null, ...dnd.drop },
      h("div", { class: "tm-sec-hw", "aria-current": here ? "true" : null, ...dnd.grab },
        fid != null ? [
          h("button", { type: "button", class: "tm-sec-ch", "aria-expanded": String(!shut), title: shut ? "Show this section" : "Fold this section", "aria-label": (shut ? "Show " : "Fold ") + label, onclick: toggle }, icon(shut ? "chevron-right" : "chevron-down")),
          stands.get(k) ? h("span", { class: "tm-sec-st", "data-key": "sec-st", "data-state": stands.get(k).key || "none", title: stands.get(k).head }, h("span", { class: "st", "data-tone": stands.get(k).tone, "aria-hidden": "true" }), h("span", { class: "vh" }, stands.get(k).head + ": ")) : null,
          h("a", { class: "tm-sec-h tm-sec-link", href: href("focus", fid || "misc"), title: fid && can("post") ? `Open ${label}. Drag to reorder the focuses (Alt+Up or Alt+Down moves it)` : `Open ${label}`, draggable: fid && can("post") ? "false" : null, onkeydown: focusKeys(fid, ctx),
            onclick: e => { if (e.button || e.metaKey || e.ctrlKey || e.shiftKey) return; if (here) { e.preventDefault(); toggle(); } else if (FOLD.has(k)) { FOLD.delete(k); saveFold(); } } }, h("span", { class: "tm-sec-nm" }, label)), count] :
        h("button", { type: "button", class: "tm-sec-h tm-fold", "aria-expanded": String(!shut), title: shut ? "Show this section" : "Fold this section", onclick: toggle },
          icon(shut ? "chevron-right" : "chevron-down"), h("span", { class: "tm-sec-nm" }, label), count),
        fid && can("post") ? h("button", { type: "button", class: "tm-sec-add", title: `New conversation in ${label}`, "aria-label": `New conversation in ${label}`, onclick: () => newIn(fid) }, icon("plus")) : null),
      shut ? null : rows.length ? [list(capImp(k, rows), { ...opts, empty: "" }), moreImp(k, rows, ctx)] : h("p", { class: "meta tm-sec-none" }, "Nothing this week."));
  };
  return h("div", { class: "tm-secs" },
    parts.length ? parts.map(([k, l]) => sec(k, l || (app() ? "Recent" : "Threads"), by[k])) : by.arch.length ? null : list([], opts),
    // Archived is a page of its own, where a conversation can be brought back (the person, 11081454b6bd); the list ends with the way there.
    by.arch.length ? h("div", { class: "tm-sec", "data-key": "sec:arch", "data-sec": "arch" },
      h("a", { class: "tm-sec-h tm-arch-b", href: href("comms", "~archived"), "aria-current": C.conv === "~archived" ? "page" : null, title: "Conversations out of the list; bring one back from there" },
        icon("archive"), `Archived (${by.arch.length})`)) : null);
}
// What a row says is read from three things that are replaced whole when they change and don't move S.ver: the inbox's read marks, the summary file
// and the foci. Each object gets a number, so the list is redrawn when one of them is new and a row says what the top of its chat says (HA-11: a row
// kept "For you" for up to half a minute after the conversation was opened, and said nothing until then when the inbox landed after the first draw).
const readNo = new WeakMap(); let readN = 0;
const readSig = o => (o && typeof o === "object" ? readNo.get(o) || (readNo.set(o, ++readN), readN) : String(o));
// What moves a row's state between two board or log lines, so S.ver stays put: an agent joins or leaves the queue, and a running one goes quiet, gets
// stuck or picks its work up again (model.js agentState reads the clock and the live read's jobs). A row kept "working" for up to half a minute after
// the top of its conversation said "stuck" (HA-11). A stop at a limit or an error arrives as a log line, which moves S.ver by itself.
const liveSig = () => (S.live ? S.live.agents : []).map(a => a.name + ":" + (a.thread || "") + ":" + agentState(a).key).sort().join() + "/" +
  (S.live ? S.live.pending : []).map(p => p.name + ":" + (p.threads || []).join("+")).sort().join();
function syncThreads(c, ctx) {
  const tl = document.querySelector("#main .cm-tl"); if (!tl) return;
  if (!simpleOn()) loadFoci();   // the foci (core/foci.js); a store emit when they change
  const held = byFocusOn() && S.foci === undefined && !C.tq.trim();   // Focus groups its rows by the focuses: grey rows until they're read, not a flat list that re-sorts
  const sig = [held, S.ver, C.view, impAll, C.hideFaded, S.foci && S.foci.at, S.foci && (S.foci.foci || []).map(f => f.id).join(), C.scope, C.conv, C.imp, tmetaSig(), [...FOLD].join(), [...VISIT.shut].join(), [...KIDS].join(), C.tq, AI.st + AI.res.length, AI.tag, S.shown && S.shown.length, showMask(), c.arg, Math.floor(now() / 3e4), S.runSig, liveSig(), marksSig().length, readSig(IB.items), readSig(S.needsYou), readSig(S.foci)].join("|");
  if (tl === L.tl && sig === L.tlSig) return;
  const fresh = tl !== L.tl || c.arg !== L.tlConv || L.tlHeld;
  L.tl = tl; L.tlSig = sig; L.tlConv = c.arg;
  if (C.tq !== L.tlQ) { L.tlQ = C.tq; tl.scrollTop = 0; }   // a new search starts at its best match, not wherever the list was scrolled to
  if (held || L.tlHeld) tl.replaceChildren();   // a skeleton and a list share no rows to morph
  L.tlHeld = held;
  if (held) { L.foldKids = []; tl.append(skeleton("rail")); return; }
  const rows = threadRows(c, ctx);
  L.foldKids = rows.filter(r => r.key.endsWith("~kids")).map(r => r.key.slice(0, -5));
  const tree = sections(rows, ctx, { dense: true, empty: simpleOn() ? "No conversations yet. Start one with New conversation." : S.threads.size ? (app() ? "Nothing here for you yet. The rest is agents talking among themselves." : "Nothing here with what Show has on. Turn on Agent comms to see all of them.") : app() ? "No conversations yet. Start one with New." : "No threads." });
  if (tl.firstChild) morph(tl.firstChild, tree); else tl.append(tree);
  const fb = document.querySelector("#main .cm-foldall");   // the list is built after the bar, so the bar's word catches up here
  if (fb) { fb.hidden = foldNone(); const w = tr(foldWord()); if (fb.title !== w) fb.title = w; }   // set by hand, so through tr as h() would
  if (fresh) { const cur = tl.querySelector("[aria-current]"); if (cur) { const r = cur.getBoundingClientRect(), b = tl.getBoundingClientRect(); if (r.top < b.top || r.bottom > b.bottom) tl.scrollTop += r.top - b.top - 40; } }
}

// ---- what's happening: the strip above the composer ----
function hasRoom() { return !limitStop(); }
const AGENT_WORDS = { working: "is working", quiet: "is here, quiet", stuck: "looks stuck", queue: "is waiting for the machine" };
function line(key, text, act, opts = {}) {
  return h("div", { class: "cm-line", "data-key": opts.key || key, "data-tone": status(key).tone }, statusDot(key, { live: opts.live }), h("span", { class: "cm-line-x" }, text), act ? h("span", { class: "cm-line-a" }, act) : null);
}
/** An agent's name in a strip line: a link to it, without a second dot (the line has its own). */
const who = (n, cap = true) => (simpleOn() ? hiveName() : agentRef(n, { dot: false, cap }));
const d0 = th => { const d = S.threads.get(th); return d && d.posts[0] ? d.posts[0].from : "an agent"; };
function lastMine(th) { const d = S.threads.get(th); if (!d) return null; for (let i = d.posts.length - 1; i >= 0; i--) if (d.posts[i].from === "person") return d.posts[i]; return null; }
const notBuilt = () => h("span", { class: "nb", title: "Designed, not built yet: the server needs to do this" }, "not built yet");

/**
 * Where the conversation stands, in its head (the person, 11160014bf8b: "tell me what I need to know when"). An agent's summary (`hive status`) opens with
 * what the conversation is about, which its title says already, and ends with its state line ("Waiting: ...", "Needs the person: ..."), which the head
 * used to cut off. threadStand() reads the summary against the needs list and what is happening now; the bar leads with that state as a mark and a word,
 * then the one line, and opens to the whole summary. It says "Needs you" exactly when the one count has the conversation (needs.js waitOf), in the list's
 * words, so a conversation on the list has a bar even before an agent wrote it a summary.
 */
const stOpen = new Set();   // threads whose summary is open (Nov 8 §8.4): a redraw (a new post) keeps it open
// The stand as the app look says it: no post id, ask id or leading step id in its lines (R16: the person cannot read them, and the home and the
// focus pages never showed them). Taken out here, once, so a row, the line under a phone's title and the top of the chat say the same words
// (test/drive_focus_leaks.mjs, test/drive_stand_words.mjs). The developer look keeps them: a developer follows an id.
const standSay = s => (s && app() ? { ...s, text: scrub(s.text), also: scrub(s.also), more: s.more.map(scrub).filter(Boolean), lines: s.lines.map(scrub).filter(Boolean) } : s);
const stand = th => inConversation(th, () => standSay(threadStand(th, seen, waitOf(th))));   // not held back until the count is known: like the rail's Needs you group, it settles as the reads land
// A stand in one line, for the places with no bar: the word, why the count doesn't have it when it is on the list but out of it ("You put this off":
// the bar says that as its second fact), then the one line. standLine is that line as a hover text; standTail is what follows the word where it is
// drawn (the line under a phone's title, a row on a phone's home), each part a text node of its own so the language table can match it whole.
const standLine = s => (s ? [s.word, s.why, s.text].filter(Boolean).map(tr).join(" · ") : "");   // each part through the language table by itself: the joined line is in no table
const standTail = s => [s.why ? " · " : null, s.why || null, s.text ? " · " : null, s.text || null];
// The mark and the word of a row, made here for every list of conversations (the side list, a phone's home, the Archived page), so no list can say
// a stand its own way: the mark is the state's dot, pulsing while an agent is on the conversation, and the word is the one the top of the chat leads with.
const standMark = s => h("span", { class: "cm-row-st", "data-state": s ? s.key : "", title: standLine(s) }, statusDot(s ? s.key : "ended", { live: !!(s && s.running) }));
const standWord = (s, title) => (s ? h("span", { class: "cm-st", "data-state": s.key, "data-tone": status(s.key).tone, title: title || null }, s.word) : null);
// A helper conversation, counted for the fold in the list and the line in the chat (both count with this):
//   "need": the one count has it (waitsOn), so the number is the one the Needs you group and the home count give. Its row says Needs you, or Failed
//           or Stuck when the list has it because its run broke: those wait on the person too (a message is what moves them), and the row's word
//           says which kind it is.
//   "run":  an agent is on it now: its row says Working, Working quietly, Waiting its turn or Waiting to start.
//   "done": its row says Done. Not one that says Quiet or Not picked up: no agent said that work was finished (the review of HA-11: "3 done" over rows
//           that said Quiet).
// A helper that says anything else (Quiet, Not picked up, Paused, Stopped, Waiting on others, Blocked, Asked, You replied, or a failure the count
// doesn't have) is in no part, so the parts need not add up to the number of helpers.
const kidStand = th => { const s = stand(th); return waitsOn(th) ? "need" : s && s.running ? "run" : s && s.word === "Done" ? "done" : ""; };
function kidCount(ths) {
  const sts = ths.map(kidStand), n = k => sts.filter(x => x === k).length;
  return { run: n("run"), need: n("need"), parts: [["run", "working"], ["need", "needs you"], ["done", "done"]].map(([k, w]) => (n(k) ? `${n(k)} ${w}` : null)).filter(Boolean) };
}
const stAge = s => `updated ${ago(s.at)}${s.since ? ` · ${plural(s.since, "message")} since` : ""}`, stBy = (s, th) => `Summary by ${inConversation(th, () => agentLabel(s.from))}`;   // whole lines, so the language table can match them (lang-es-6.js); the app look names the author as the conversation does, not by its code
// HA-12: the chat's top offers what the Needs you list offers, where the person is reading. While the conversation waits on them (Needs you,
// Asked earlier) it has Later and Not needed; put off (Later, Not needed) it has Bring back. The same item as its row on the list (waitOf's w),
// so the count, the rail's group and the top's word change together. menu: the phone's title menu, which closes on a pick like its other items.
// Not the thread's own Not needed (the title menu's fade): that greys the conversation; this settles the ask.
const topWait = th => { const wo = th && can("post") ? waitOf(th) : null; return wo && wo.w ? wo : null; };
function topActs(th, menu) {
  const wo = topWait(th); if (!wo) return null;
  const done = () => { if (menu) C.menu = null; L.ctx && L.ctx.update(); };
  const bs = wo.where === "now" || wo.where === "older" ? laterButtons(wo.w)
    : [button("Bring back", e => { if (e) e.stopPropagation(); setLater(wo.w, "back"); }, { size: "s", kind: "quiet", cls: "nd-back" })];
  for (const b of bs) { const go = b._on && b._on.click; (b._on ||= {}).click = e => { go && go(e); done(); }; }
  return h("span", { class: "cm-st-acts", role: "group", "aria-label": "Later or not needed" }, bs);
}
function statusBar(th) {
  const s = th && stand(th); if (!s) return null;
  // The head opens to the whole summary. With no summary it opens only when its one line may be cut short (a second fact, which a narrow bar
  // drops, or a long line): a bare "Working · started 5m ago" is all there, so it is not a button that does nothing.
  const opens = !s.bare || !!s.also || s.text.length > 60, open = opens && stOpen.has(th), r = (S.threadStatus || {})[th];
  if (can("tier") && !simpleOn()) loadTiers(L.ctx && L.ctx.update);
  // Paused (ad748, friction-redesign): a "Paused" chip and Resume in place of the tier chip, so stopping it again is one press.
  const tierChip = can("tier") && !simpleOn() ? pausedStrip("thread", th, focusOf(th), L.ctx && L.ctx.update) || threadTierChip(th, focusOf(th)) : null;
  return h("div", { class: "cm-status cm-strip", "data-key": "status", "data-open": open ? "" : null, "data-state": s.key, "data-tone": status(s.key).tone, role: "note", "aria-label": "Where it stands" },
    h("b", opens ? { class: "cm-strip-a", role: "button", tabindex: "0", "aria-expanded": String(open), title: s.bare ? (open ? "Show less" : "Show all of it") : open ? "Hide the summary" : "Show the whole summary",
      onclick: e => { e.stopPropagation(); open ? stOpen.delete(th) : stOpen.add(th); L.ctx && L.ctx.update(); },
      onkeydown: e => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); e.currentTarget.click(); } } } : { class: "cm-strip-a" },
      // the state and its one line stay together; the second fact is dropped first when the row is short (chat.css)
      h("span", { class: "cm-st-main" }, statusDot(s.key, { live: s.running }), h("span", { class: "cm-st-w" }, s.word), s.text ? h("span", { class: "cm-st-t" }, " · ", s.text) : null),
      // data-dup: the open summary says this line already, so it shows only while the bar is closed
      s.also ? h("span", { class: "cm-st-also", "data-dup": s.dup ? "" : null }, s.also) : null),
    s.more.length ? h("span", { class: "cm-strip-b" }, s.more.map(l => h("span", { class: "cm-st-l" }, l))) : null,
    // The work tier, only when it isn't its focus's (a9179, rung WT-1): the focus card already says the focus's.
    tierChip,
    topActs(th),
    // bare: on the list with no summary yet, so there is no age and nothing to reply about; the conversation's own messages say the rest
    s.bare ? null : h("span", { class: "meta cm-st-age", title: stBy(s, th) }, stAge(s)),
    // One agent named per conversation (R6 review): the strip names who is on it; the summary's author is on hover.
    // Reply: the chip above the box and the quote on the board name the author as the conversation does (a message's Quote does the same); the quoted
    // lines go as the agent wrote them, codes and all, since an agent reads them.
    s.bare ? null : h("span", { class: "meta cm-status-m cm-strip-m", title: stBy(s, th) }, stAge(s),
      can("post") ? h("button", { type: "button", class: "cm-status-r", title: "Reply about this summary", onclick: () => quoteRef(th, { who: inConversation(th, () => capFirst(agentLabel(r.from))), where: "summary", text: r.text }) }, "Reply") : null));
}

/**
 * HA-24: under the bar, each job running for this conversation with the newest line it wrote and how old that line is (model.js jobsFor,
 * dash.py job_lines): a long build or sweep said nothing in the chat until it ended, so the person asked how it was going. Then the jobs of a
 * conversation this one says it waits on, each a link to that conversation. One line a row, the line cut, at every width.
 * The simple look says none of it; a sign-in without the right to read agents' work gets who runs a job and since when, not its command or line.
 */
function jobRows(th) {
  if (simpleOn()) return null;
  const js = inConversation(th, () => jobsFor(th)); if (!js.length) return null;
  const say = t => (app() ? scrub(t) : t);
  return h("div", { class: "cm-jobs", "data-key": "jobs", role: "status", "aria-label": "Jobs running" },
    js.slice(0, 4).map(j => {
      const age = j.line && j.at ? ago(j.at * 1e3) : j.since ? `started ${ago(j.since * 1e3)}` : "";
      // The app look shows no shell command: "mkdir -p $TMPDIR/… && cd …" told the person nothing (friction-dash, 11-25). It is on hover; the job's own line says how it goes.
      const lead = j.there ? h("a", { class: "cm-job-w", href: threadHref(j.th), title: `A job in ${threadTitle(j.th)}` }, short(threadTitle(j.th), 40)) : h("span", { class: "cm-job-w", title: app() && j.cmd ? plainStep("Running: " + j.cmd) : null }, "Job");
      const what = app() ? (j.line ? null : "no output yet") : j.cmd ? short(say(j.cmd), 60) : inConversation(th, () => `${capFirst(agentLabel(j.agent))}'s job`);
      return h("div", { class: "cm-job", "data-key": `job:${j.agent}:${j.kind}:${Math.round(j.since || 0)}`, "data-there": j.there ? "" : null },
        statusDot("working", { live: true }), lead,
        what ? h("span", { class: "cm-job-c" + (app() ? " cm-job-who" : ""), title: app() ? null : j.cmd || null }, what) : null,
        j.line ? h("span", { class: "cm-job-l", title: j.line }, say(j.line)) : null,
        age ? h("span", { class: "meta cm-job-age" }, age) : null);
    }),
    js.length > 4 ? h("span", { class: "meta cm-job-more" }, `and ${js.length - 4} more`) : null);
}

const strip = th => inConversation(th, () => stripLines(th));
function stripLines(th) {
  const st = threadState(th, seen), out = [], run = (S.live ? S.live.agents : []).filter(a => a.thread === th), post = can("post") && app();
  const call = callFor(th);
  if (call) out.push(line("working", [who(call.agent), call.state === "hold" ? " is putting you through on your call" : " is on a call with you", h("span", { class: "meta cm-line-m" }, " · " + dur(now() - call.started * 1e3))], post ? button("Hang up", () => hangUp(call.id), { size: "s" }) : null, { key: "call", live: true }));
  // One line per state, no quoted question and no Answer: the composer is already addressed to it (the Nov 8 handoff §8.6).
  if (st.key === "asks") out.push(line("asks", [who(st.who), " waits for your answer"], null, { key: "asks" }));
  // A paused run reads what the person wrote when it carries on, and not before: the starter starts nothing for a name it is about to resume, so a
  // message does not wake it sooner (the line used to say it did, after a lost connection). With a message of theirs held for it, the line says so.
  const held = st.key === "limited" && heldForYou(th, st) ? " It reads your message then." : null;
  if (st.key === "limited" && st.retry) out.push(line("limited", [who(st.who), simpleOn() ? " is taking a break and carries on by itself." : ` stopped (${retryWords(st.retry)}) ${ago(st.t)} and carries on by itself.`, held], null, { key: "limited" }));
  else if (st.key === "limited") {
    // the run's own limit (a model's own share, days away) or the Hive's (the top bar's banner time): core/model.js carryOn, as the stand line says
    const { room, at, own } = carryOn(st.until, limitStop(), now(), st.scoped);
    out.push(line("limited", [who(st.who), simpleOn() ? " is taking a break. " : ` stopped at a usage limit ${ago(st.t)} and hasn't carried on. `, room ? "There's room again now, so it should carry on by itself soon." : at ? `It carries on by itself after ${stamp(at)}.` : "It carries on by itself once there's room.",
      // the starter resumes a run held on its model's own limit at once on another model a message asks for (core/starter.py resume_model_switched)
      own && !simpleOn() ? " A message that picks another model has it carry on now." : null, held],
      room && post && !held ? button("Carry on", () => say(th, "Please carry on where you stopped.", st.who), { size: "s" }) : null, { key: "limited" }));
  }
  if (st.key === "failed") out.push(line("failed", [who(st.who), simpleOn() ? " ran into a problem. Send a message and it tries again." : ` stopped with an error ${ago(st.t)}. A message wakes it to try again.`], null, { key: "failed" }));
  for (const a of run) {
    if (st.key === "asks" && a.name === st.who) continue;
    const s = agentState(a);
    if (simpleOn()) { if (!out.some(x => x.dataset.key === "run")) out.push(line("working", "Your Hive is working on it.", can("stop") ? stopButton("Stop this work", () => askStop(a.name)) : null, { key: "run", live: true })); continue; }
    // Closed it reads "a7c31 Editing fetch.py"; how long ago it was active shows only when open.
    const step = a.doing && app() ? plainStep(a.doing) : a.doing;   // a bare command reads as plain words in the app look (a0ad9's re-check)
    const doing = step && !(app() && ownStep(a.doing)) ? step.charAt(0).toUpperCase() + step.slice(1) : AGENT_WORDS[s.key] || s.text;   // E10: its own board commands are not a step to read
    out.push(line(s.key, [who(a.name), " ", doing, h("span", { class: "meta cm-line-m" }, " · " + (s.key === "working" ? ago(a.last * 1e3) : s.text))],
      [post && th ? interruptButton(th, a.name) : null, can("stop") ? stopButton(`Stop ${agentLabel(a.name)}`, () => askStop(a.name, agentLabel(a.name))) : null], { key: "run:" + a.name, live: true }));
  }
  // after the working agents (11081336a82e: folded, the strip shows who works and its Interrupt, not where the conversation sits)
  // Where it sits (the person, 110601450d55): a helper's conversation names the one it came from, and that one lists its helpers.
  const par = app() && threadParent(th), kids = app() ? threadChildren(th) : [];
  if (par) out.push(line("ended", ["Part of ", h("a", { href: threadHref(par) }, short(threadTitle(par), 60)), simpleOn() ? null : [": ", who(d0(th), false), " started it from there"]], null, { key: "parent" }));
  if (kids.length) {
    // Counted as the list counts them under this conversation's row (kidCount), in the same words, and each link's hover is its row's word:
    // the fold said "2 needs you" where this line said "1 waiting on you", and a helper waiting to start was working in one and not in the other.
    const ks = [...kids].sort((a, b) => S.threads.get(b).last - S.threads.get(a).last), { run: on, need: ask, parts } = kidCount(ks);
    const links = ks.slice(0, 5).flatMap((k, i) => [i ? ", " : "", h("a", { href: threadHref(k), title: standLine(stand(k)) }, short(threadTitle(k), 40))]);
    out.push(line(on ? "working" : ask ? "asks" : "ended", [plural(kids.length, "helper conversation"), parts.length ? [" (", parts.flatMap((p, i) => (i ? [", ", p] : [p])), ")"] : "", ": ", ...links, ks.length > 5 ? ` and ${ks.length - 5} more` : ""],
      post ? button(KIDS.has(th) ? "Hide in list" : "Show in list", () => { KIDS.has(th) ? KIDS.delete(th) : KIDS.add(th); L.ctx && L.ctx.update(); }, { size: "s" }) : null, { key: "kids", live: !!on }));
  }
  // A second helper when the agent you wrote to is busy (not built): see PART1.md.
  const mine = lastMine(th), wait = mine && now() - mine.t > HELPER_AFTER && now() - mine.t < 2 * HOUR ? mine.men.map(m => delivery(mine, m)).find(x => x && x.key === "waiting") : null;
  if (wait && built("helper") && !simpleOn()) out.push(line("queue", [who(wait.who), ` is busy and hasn't seen your message for ${dur(now() - mine.t)}. If it still hasn't by ${clock(mine.t + 10 * MIN)}, a helper starts on it. `, notBuilt()],
    post ? [button("Start a helper now", async (e, b) => { b.disabled = true; try { await apiPost("/api/helper", { post: mine.id }); poll(); } catch (x) { b.disabled = false; b.title = x.message; } }, { size: "s" })] : null, { key: "helper" }));
  // The app look says nothing when nobody is working (E10: "Nobody is on this right now. A message wakes a1b2c" read as hive talk around a plain answer); the developer look keeps the line.
  if (!out.length && th && !(app() && !simpleOn())) {
    const r = recipient(th);
    out.push(line("done", simpleOn() ? "Your Hive has stopped for now. Send a message when you want more." : r.who && r.who !== "new" ? `Nobody is on this right now. A message wakes ${r.who}.` : "Nobody is on this right now. A message starts an agent on it.", null, { key: "idle" }));
  }
  const more = out.length - 1, open = !C.strip ? false : true;
  // Closed it is one line: the second agent rides on it as a quiet note.
  const k2 = out.length > 1 && !open ? String(out[1].dataset.key) : "", x0 = out[0] && out[0].querySelector(".cm-line-x");
  if (/^run:/.test(k2) && x0) x0.append(h("span", { class: "meta cm-line-2" }, " · and " + agentLabel(k2.slice(4))));
  return h("div", { class: "cm-strip", "data-key": "strip", "data-open": open ? "" : null, role: "status", "aria-live": "polite" }, out,
    more > 0 && !open ? h("span", { class: "meta num cm-strip-n", "data-key": "n" }, "+" + more) : null,
    out.length > 1 || out.some(x => x.querySelector(".cm-line-a") || /^run:/.test(x.dataset.key)) ? stripToggle(open) : null);
}
function stripToggle(open) {
  const b = button(open ? "Show less" : "Show all", () => { C.strip = !open; L.ctx && L.ctx.update(); }, { kind: "icon", size: "s", icon: open ? "chevron-down" : "chevron-up", cls: "cm-strip-t", title: open ? "Show less" : "Show everyone and what you can do" });
  b.setAttribute("aria-expanded", String(open)); return b;
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
function closeRowMenu() { if (!rowM) return; rowM.nodes.forEach(n => n.remove()); for (const x of new Set([rowM.btn, ...document.querySelectorAll(".lr-more-b[data-open]")])) { x.setAttribute("aria-expanded", "false"); x.removeAttribute("data-open"); } document.removeEventListener("keydown", rowM.key, true); window.removeEventListener("scroll", rowM.scroll, true); rowM = null; }
function rowMore(th, ctx) {
  const open = !!rowM && rowM.th === th;   // drawn from the open menu, so a poll's re-render of the row while it is open doesn't reset the button (churn walk, 390, 2026-11-21)
  return h("button", { type: "button", class: "lr-more-b", "aria-haspopup": "true", "aria-expanded": open ? "true" : "false", "data-open": open ? "" : null, title: "Pin, mark important, archive", "aria-label": "Actions for this conversation",
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
  rowM = { btn: b, th, nodes: [sc, m], key: e => { if (e.key === "Escape") { e.stopPropagation(); closeRowMenu(); b.focus(); } }, scroll: e => { if (!m.contains(e.target)) closeRowMenu(); } };
  document.addEventListener("keydown", rowM.key, true); window.addEventListener("scroll", rowM.scroll, true);
  m.querySelector(".menu-i")?.focus();
}

// §8.4 phone: the head shows where it stands in one line under the title; the title's menu says it in full, with the rest of the summary (hidden above 699 px). Its own scroll box (chat.css), so a long summary doesn't push the menu's rows off the sheet.
function stMenu(th) {
  const s = th && stand(th); if (!s) return null;
  return h("div", { class: "cm-menu-st", role: "note", "aria-label": "Where it stands", "data-state": s.key, "data-tone": status(s.key).tone },
    h("p", { class: "cm-menu-st-a" }, statusDot(s.key, { live: s.running }), h("b", null, s.word), s.text ? " · " : null, s.text || null),
    s.also && !s.dup ? h("p", { class: "cm-menu-st-also" }, s.also) : null,   // the second fact, unless the summary's lines below say it
    s.more.map(l => h("p", null, l)),
    s.bare ? null : h("p", { class: "meta", title: stBy(s, th) }, stAge(s)),
    topActs(th, true));
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
    // while it waits on the person, the Not needed above (topActs) is the one: two of that name in one menu would do different things
    mine && (fd || !topWait(th)) ? act(fd ? "Needed again" : "Not needed", "check", () => tryMeta(th, { faded: !fd }, ctx), fd ? fadeWhy(th) : "Grey it in the list: still there to open and write in, and back by itself when something new is for you") : null) : [];
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
  // Pause / Resume (ad748): through the work tiers, which the starter obeys; the dialog says what it does.
  const pz = full && can("tier") ? pauseOf("thread", th, focusOf(th)) : null;
  const pause = pz ? act(pz.held ? "Resume" : "Pause…", pz.held ? "play" : "pause", () => askPause("thread", th, focusOf(th), ctx.update), pz.held ? "Let agents start on it again" : "Nothing new starts on it until you resume it; an agent already at work carries on until it stops") : null;
  const where = part(...more,
    pause,
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
    stMenu(th), lead, lead.length ? sep() : null, where, out.length ? sep() : null, out,
    full && th !== threadTitle(th) ? h("p", { class: "meta cm-menu-id" }, "Its id on the board: ", h("code", { title: "Click to select it" }, th), copyButton(th)) : null);   // copy it (the person, 11041333ab1a)
}
// Chat's fixed bar of buttons: New conversation, then the pages Chat brings together, each with a count where there is one, only where this
// connection may open it. A row of its own under the top bar (ui/shell.js TOPX), above the list, not in it, so dragging the list wider or narrower
// leaves it alone (the person, 110819287bb8, 110819316eed "a static button bar", 11081933e722 "dont put it in the sidebar", 11081937e3e8).
function places() {
  const at = location.hash || "#comms";
  const link = (label, ic, to, n, tip, word = "new") => h("a", { class: "cm-place", href: to, title: tip, "data-key": ic, "aria-current": at.split("/")[0] === to.split("/")[0] ? "page" : null }, icon(ic), h("span", { class: "cm-place-l" }, label), n ? h("b", { class: "cm-place-n", "aria-label": n + " " + word }, String(n)) : null);
  return h("div", { class: "cm-places", role: "group", "aria-label": "Chat", "data-key": "places" },
    can("post") ? h("a", { class: "cm-place cm-newbtn", href: href("comms", "~new"), title: "Start a new conversation", "aria-label": "New conversation", "data-key": "new", "aria-current": at === href("comms", "~new") ? "page" : null }, icon("new"), h("span", { class: "cm-place-l" }, "New conversation")) : null,
    h("nav", { class: "subnav", "aria-label": "Chat's pages" },   // the same sub-bar as Agents' and Usage's rows (R49)
    // Needs me merged into Yours (Nov 9 handoff §A.2): its count, and its list when something waits
    can("post") && needsN() ? link("Yours", "yours", href("yours", "waits"), needsN(), "What waits on you, newest first; every exchange you had is under it", "waiting on you") : link("Yours", "yours", href("yours"), 0, "Every exchange you had, newest first"),
    can("read") ? link("Inbox", "inbox", href("inbox"), IB.unread || 0, "Every message agents sent you; the count is unread messages, not what waits on you", "unread") : null,
    can("files") ? link("Mail", "mail", href("mail"), MN.n, MN.n ? "Mail since you last opened it" : "Your mail") : null,
    link("Files", "file", href("made"), 0, "Files and links from your conversations"),
    can("files") ? link("Documents", "doc", href("docs"), 0, "The library's PDFs: comment on one and an agent revises it") : null));
}
TOPX.chat = () => app() ? places() : null;
// The choices about the list, always open over it (the person, 110819287bb8, after 11071400de5b had put them behind one button):
// what shows, how it is grouped, whether the not-needed ones are listed, fold and reach.
function showMenu(ctx) {
  const searching = words(C.tq).length > 0;
  return h("div", { class: "cm-menu cm-show-m", role: "menu", "data-key": "showmenu", "aria-label": "Show and group" },
    showChips(ctx),   // three rows, no kickers (Nov 9 handoff §B)
    app() && !searching && C.conv !== "~list" ? viewBar(ctx) : null,   // the phone's list page has the toggle in sight
    searching ? null : h("div", { class: "cm-scope" }, h("span", null, app() ? "Conversations" : "Threads"),
      h("button", { type: "button", class: "cm-foldall", hidden: foldNone(), title: foldWord(), "aria-label": "Fold or open every section", onclick: () => foldAll(ctx) }, icon("expand")),
      h("button", { type: "button", class: "cm-scope-b", title: C.scope === "recent" ? (app() ? "Only the last 7 days are listed. Show every conversation." : "Only the last 24 hours are listed. Show every thread.") : "List only the recent ones",
        onclick: () => { C.scope = C.scope === "recent" ? "all" : "recent"; ctx.update(); } }, C.scope === "recent" ? "All" : "Recent")));
}
// What shows: For you, Updates, Agent comms, each on or off: three toggles, in the Show menu since 2026-11-07 (first over the list: the person, 11040113eacf).
const tmetaSig = () => Object.entries(S.threadMeta || {}).map(([k, v]) => k + (v.important || 0) + (v.pin || 0) + (v.faded || 0)).join();
function showChips(ctx) {
  const mask = showMask();
  const group = h("div", { class: "tm-show", role: "group", "aria-label": "Show", title: showSays(mask) },
    // All three, always. E10 (1110232186c2) once left Agent comms out until it had been on in this browser; the person then found it missing on every new address
    // (a fresh Hive, 1116002301a8), and simple mode offers no Show N hidden to bring it back, so the chip is drawn from the start (it stays off by default).
    SHOW_PARTS.map(([b, l, t]) => h("button", { type: "button", class: "tm-chip", "data-show": KIND_NAME[b], "aria-pressed": String(!!(mask & b)), title: (mask & b ? "Hide " : "Show ") + t,
      onclick: () => setShow(showMask() ^ b, ctx) }, h("i", { "aria-hidden": "true" }), l)),
    mask ? null : h("p", { class: "meta tm-show-says" }, showSays(mask)));
  return app() ? h("div", { class: "tm-showwrap" }, group) : group;
}
// Important only: beside the search, so the three toggles keep one row in a 280 px rail. It stays while searching.
const importantStar = ctx => h("button", { type: "button", class: "tm-chip tm-chip-imp", "aria-pressed": String(!!C.imp), title: C.imp ? "Show every conversation again" : "Only conversations marked important", "aria-label": "Important only",
  onclick: () => { C.imp = !C.imp; ctx.update(); } }, icon("star"));
// In the Show menu: By focus or By recency, and whether the conversations marked Likely not needed are listed at all.
function viewBar(ctx) {
  const nFaded = [...S.threads.keys()].filter(th => faded(th) && !archived(th) && !workingIn(th)).length;
  return h("div", { class: "cm-views", "data-key": "views" },
    seg([["focus", "Focus", "Each conversation under its focus, in the order of the home cards, the rest under Other last; helper conversations folded under the one they came from"],
      ["recent", "Recent", "Every conversation in one list, newest activity first, helper conversations included, each naming the one it came from"]], C.view,
      v => { C.view = v; store(VIEW_KEY, v); ctx.update(); }, { label: "How the list is arranged" }),
    h("button", { type: "button", class: "tm-chip cm-hidefaded", "aria-pressed": String(C.hideFaded),
      title: C.hideFaded ? `Show the ${nFaded} marked Likely not needed again` : `Hide the ${nFaded} conversations marked Likely not needed`,
      onclick: () => { C.hideFaded = !C.hideFaded; store(HIDE_KEY, C.hideFaded ? "1" : "0"); ctx.update(); } }, h("i", { "aria-hidden": "true" }), "Hide not needed"));
}
// The phone's line under the title: the state, why the count doesn't have it when it is on the list but out of it ("You put this off": the bar says that as its second fact, and this line has no room for one), then the one line.
const titleSub = th => { const s = th && stand(th); return s ? h("span", { class: "cm-title-sub", "data-state": s.key, "data-tone": status(s.key).tone }, statusDot(s.key, { live: s.running }), h("b", null, s.word), standTail(s)) : null; };
function head(c, ctx) {
  if (c.thread && app()) import("../ui/preview.js").then(m => m.autoFiles(c.thread));
  const d = c.thread && S.threads.get(c.thread), n = L.matched.length, off = maskFor(c) !== SHOW.ALL && !C.q.trim() ? L.src.length - n : 0;
  const title = c.thread ? threadTitle(c.thread) : c.agent ? (c.agent === "person" ? "Everything you sent" : "Everything by or to " + c.agent) : c.pair ? `${c.pair[0]} and ${c.pair[1]}` : app() ? "All messages" : "Everything";
  const people = simpleOn() || !c.thread ? [] : threadPeople(c.thread).filter(m => m !== "person");
  const who = simpleOn() || c.thread ? [] : c.agent ? [...new Set(S.posts.filter(p => p.from === c.agent).map(p => p.th))].slice(-14).map(t => threadRef(t)) : [];
  const find = C.find || !!C.q.trim();
  const more = !!c.thread && C.more === c.thread;   // the folded tools are open (chat.css shows the More button under 600 px only)
  requestAnimationFrame(watchHead);
  return h("header", { class: "cm-head", "data-key": "head", "data-find": find ? "" : null, "data-tight": headTight ? "" : null, "data-more": more ? "" : null },
    h("div", { class: "cm-tt", "data-dev": app() ? null : "" },
      sideBtn(),
      h("a", { class: "btn cm-back", href: href("comms"), "data-kind": "icon", "aria-label": app() ? "Chats" : "Threads", title: app() ? "Chats" : "Threads" }, icon("chevron-left")),
      c.thread && app() && !simpleOn() ? threadCrumb(c.thread) : null,
      h("button", { type: "button", class: "cm-title-b", "data-hid": off > 0 && !simpleOn() ? "" : null, title: off > 0 && !simpleOn() ? `${off} hidden: open for the way to show them` : null, "aria-haspopup": "true", "aria-expanded": String(C.menu === "title"), onclick: e => { e.stopPropagation(); C.menu = C.menu === "title" ? null : "title"; ctx.update(); } },
        h("h1", { class: "cm-title" }, title), icon("chevron-down"),
        titleSub(c.thread)),   // §8.4 phone: where it stands, under the title
      h("span", { class: "meta cm-count" }, (C.q.trim() ? plural(n, "match", "matches") : plural(n, app() ? "message" : "post")) + (d ? ` · since ${stamp(d.first)}` : "")),
      c.thread && !app() && title !== c.thread ? h("span", { class: "cm-id" }, h("code", { class: "meta", title: "Its id on the board" }, c.thread), copyButton(c.thread)) : null),
    h("div", { class: "cm-tools" },
      people.length ? inConversation(c.thread, () => avatars(people)) : null,
      d?.posts.some(p => p.text.length > 2400) ? button("Contents", () => import("../rich/rail.js").then(m => m.openRail()), { kind: "icon", icon: "bands", cls: "rc-railbtn", title: "Contents: what's in this thread" }) : null,
      input({ type: "search", class: "input cm-find", placeholder: c.thread ? "Search here" : "Search messages", "aria-label": "Search these messages", value: C.q, oninput: e => { C.q = e.target.value; clearTimeout(qTimer); qTimer = setTimeout(ctx.update, 120); }, onkeydown: e => { if (e.key === "Escape") { e.stopPropagation(); C.q = ""; C.find = false; e.target.blur(); ctx.update(); } } }),
      h("button", { type: "button", class: "btn", "data-kind": "icon", "aria-label": "Search here", title: "Search here", "aria-pressed": String(find),
        onclick: () => { C.find = !find; if (!C.find) C.q = ""; ctx.update(); if (C.find) queueMicrotask(() => document.querySelector(".cm-find")?.focus()); } }, icon("search")),
      c.thread && SH.get(c.thread)?.map && Object.values(SH.get(c.thread).map).some(x => x.show !== "full") ? h("button", { type: "button", class: "btn cm-showall", "data-size": "s", "aria-pressed": String(showAll(c.thread)), title: "Show every message in full, not shortened to one line", "aria-label": "All: show every message", onclick: () => { setShowAll(c.thread, !showAll(c.thread)); L.rebuild = true; L.keep = true; ctx.update(); } }, h("span", { class: "cm-sa-l" }, "Show every message"), h("span", { class: "cm-sa-s", "aria-hidden": "true" }, "All")) : null,
      c.thread ? button("Files", () => import("../ui/preview.js").then(m => m.openFiles()), { kind: "icon", icon: "folder", cls: "cm-files", title: "Files: this conversation's and the projects'" }) : null,
      c.thread ? asideBtn(c.thread) : null,
      c.thread ? h("button", { type: "button", class: "btn cm-more-b", "data-kind": "icon", "aria-label": "More tools", title: "More tools", "aria-expanded": String(more),
        onclick: e => { e.stopPropagation(); C.more = more ? null : c.thread; ctx.update(); } }, icon("more")) : null),
    statusBar(c.thread),   // where it stands: its own row under the title and the tools (hidden on a phone, where the title's sub line says it)
    c.thread ? jobRows(c.thread) : null,   // HA-24: the jobs running for it, each with its newest line
    C.menu === "title" ? scrim(() => { C.menu = null; ctx.update(); }) : null,
    C.menu === "title" ? titleMenu(c, n, d, off, ctx) : null,
    who.length ? h("div", { class: "cm-people" }, who) : null);
}
// Side chats (ui/sidechat.js; /btw side threads): a button in the head's tools opens the list of this conversation's side chats in
// the side panel, with New side chat and Draw a box. From a side chat opened as its own conversation, it goes back.
function asideBtn(th) {
  const up = btwParent(th);
  if (up) return button("Back to " + short(threadTitle(up), 28), null, { href: threadHref(up), size: "s", kind: "quiet", icon: "chevron-left", cls: "cm-aside-b", title: "This is a side chat about " + threadTitle(up) });
  if (!app() || !can("post")) return null;
  const n = sideChats(th).length;
  return h("button", { type: "button", class: "btn cm-aside-b", "data-kind": "icon", "aria-label": n ? `Side chats (${n})` : "Side chats", title: n ? `Side chats (${n}): ask about part of this conversation on the side` : "Side chats: ask about part of this conversation on the side",
    onclick: () => openSideList(th) }, icon("chat"), n ? h("span", { class: "cm-sc-n", "aria-hidden": "true" }, String(n)) : null);
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
export const sumLine = (x, sum, at) => typeof x === "string" ? { text: x, at: ((at || (sum && sum.at) || 0) * 1e3) || null } : { text: (x && x.text) || "", at: ((x && x.at) || at || (sum && sum.at) || 0) * 1e3 || null };
/** The lines of part k ("now", "lately"), each with its own time (focus.py now_at: when the line was first written, not when the summary was
 *  last rewritten) and its place i in the summary (the server's `hidden` indexes count every line, so the empty ones are dropped after). */
export const sumLines = (sum, k) => ((sum && sum[k]) || []).map((x, i) => ({ ...sumLine(x, sum, sum[k + "_at"] && sum[k + "_at"][i]), i })).filter(l => l.text);
// "Box logins: all four are dead" opens with what it is about. That topic in bold lets ten lines be scanned for the one that matters
// (the person, 11160014bf8b: too much to read without the context). Only a short label counts: up to six words before the colon, a bracket
// of refs aside; a date ("11-16:") or a sentence that happens to hold a colon stays plain.
const TOPIC = /^([A-Z0-9][^:\n]{1,70}?)(\s*\([^()\n]{0,60}\))?:\s+(?=\S)/;
/** {key, rest} when a line opens with a short topic and a colon ("Topic: ..." or "Topic (#thread): ..."), else null. */
export function topicOf(text) {
  const t = String(text || ""), m = TOPIC.exec(t); if (!m) return null;
  const key = m[1].trim();
  if (!/[A-Za-z]/.test(key) || key.length > 48 || key.split(/\s+/).length > 6 || /[.!?;]\s|https?$|[@#`]/.test(key)) return null;
  return { key, rest: t.slice(key.length) };
}
/** A line in running text: agents as the app's agent pill (V3 rule: no agent code outside a pill), a #thread as a quiet link (no square), so a sentence doesn't fill with chips. */
export function lineText(text, { pill = true, topic = true } = {}) {   // pill: false draws an agent code as bold mono in the sentence (the Nov 8 handoff §1.5)
  if (topic) text = scrub(text);   // ids out first: a step id ("IM-3: …") or a post id is not what the line is about
  const tp = topic ? topicOf(text) : null;
  if (tp) return [h("b", { class: "ln-k" }, tp.key), ...(/^\s/.test(tp.rest) ? [" "] : []), ...lineText(tp.rest.trimStart(), { pill, topic: false })];   // the rest's own space was lost: "Ultra(Jlc quote hifi lite)" (a0ad9)
  if (simpleOn()) return [].concat(plainPills(text));
  return pills(text).map(x => x && x.nodeType === 1 && x.dataset.kind === "thread" ? h("a", { class: "th", href: x.getAttribute("href"), title: x.title }, x.textContent)
    : x && x.nodeType === 1 && x.dataset.kind === "agent" ? (pill && x.classList.add("pill"), x) : x);
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
// "Finished since you last looked" (SU-3): most bars end in Done, so an old Done says nothing; only work that finished after this browser's previous visit is marked.
const SEEN_BEFORE = (() => { try { const k = "h2.lastSeen", prev = Number(localStorage.getItem(k)) || 0, mark = () => localStorage.setItem(k, String(now())); mark(); setInterval(mark, 60000); addEventListener("pagehide", mark); return prev || now() - DAY; } catch { return now() - DAY; } })();
/** Standing threads (keeper, improve, alarms) say Done after every run and are never the person's own work: only a conversation the person started counts as finished for them. */
const startedByPerson = th => { const d = S.threads.get(th); return !!(d && d.posts.length && d.posts[0].from === "person"); };
/** Finished since the last visit (SU-3): asks of the person's that closed first (the ask register, /api/progress), then conversations they started that went Done. Ten shown, the rest behind one click. */
const FIN = { asks: null, at: 0, open: true };   // open: shut, the reader saw "11 conversations" and no titles (reading 1812)
function loadFinished(ctx) { if (now() - FIN.at < 5 * MIN) return; FIN.at = now(); jget("/api/progress").then(v => { FIN.asks = (v && v.asks) || []; ctx.update(); }).catch(() => { FIN.at = 0; }); }
/** What came of a closed ask: the first line of its evidence post, one line; the ask's own words read as an order under a "Finished" heading (reading 20261110-2009). No post found: no words.
 *  Post ids are hex: "\d{12}" missed "112415478b8d", and that row said nothing (friction-dash, 2026-11-25). */
function outcome(ev) { const ids = String(ev || "").match(/\b[0-9a-f]{12}\b/g) || [], p = ids.map(id => S.byId[id]).find(Boolean); return p ? finishWords(displayText(p)) : ""; }
/**
 * What a finished conversation came to, in one line a person reads (a0ad9's re-check, friction-dash-review2): the closing post's opening words without
 * footnote marks ("[^1]"), list dashes, commit hashes or file paths (a path reads as its file's name). A post that opens on housekeeping ("False start:
 * the first launch ran without my H2_NAME …") says nothing of what was done, so the row shows no line for it rather than that one.
 */
// Housekeeping is the agent's note about its own run: "False start: …", "Restarted after the crash", "Retrying.", "Heartbeat". A restart or retry with an
// object or an outcome is work and shows ("Restarted the timer and fixed the plate check", "Retrying didn't help: the cart is still empty", a0ad9 #3).
const HOUSE = /^(?:false start|note to self|heartbeat|ignore (?:this|that))/i, REDO = /^(?:restart(?:ed|ing)?|relaunch(?:ed|ing)?|retry(?:ing)?|re-?ran|re-?running|resum(?:ed|ing))\b/i;
// "Restarted: the dash crashed, fixed the leak" and "Relaunched — the dash is back with the fix" go on to the work, so they show; "Retrying: rate limit." doesn't (a0ad9 #3)
const house = l => { if (HOUSE.test(l)) return true; const r = REDO.exec(l); if (!r) return false; const rest = l.slice(r[0].length), p = /^\s*[:\u2014\u2013-]\s*/.exec(rest);
  return /^\s+(?:after|since|because|now|here|myself|this run|the run|the session|from scratch|in a fresh session)\b|^\s*(?:[.!]|\(|$)/i.test(rest) || !!p && rest.slice(p[0].length).split(/\s+/).filter(Boolean).length < 4; };
export function finishWords(t) {
  let coded = false;
  const ls = bareLinks(mdPlain(String(t || ""))).split("\n").filter(l => !/^\s*\[\^[^\]]+\]:/.test(l)).map(l => l
    .replace(/\[\^[^\]]+\]/g, "").replace(/^\s*(?:[-*+\u2022]|\d+[.)])\s+/, "").replace(/^(?:@\S+\s+)+/, "")
    .replace(/^([0-9a-f]{5})(\s*\([^)]*\))?:\s+/, (all, c, paren) => /\d/.test(c) || paren || isAgent(c) ? (coded = true, "") : all)   // a leading agent code, "a5db4 (keeper): " (friction re-check 2026-11-25)
    // a hash goes with the words that only pointed at it: "see core 0b3a004" and "in core 0b3a004, e837fd5" leave nothing behind (a0ad9 #3)
    .replace(/\b(?:commits?\s+)?(?=[0-9a-f]*\d)(?=[0-9a-f]*[a-f])[0-9a-f]{7,40}\s+and\s+(?!(?:commits?\s+)?(?=[0-9a-f]*\d)[0-9a-f]{7,40}\b)/g, "")
    .replace(/(?:,?\s*\b(?:see|in|at|as|to|on)(?:\s+(?:core|the repo|repo|git|branch\s+[\w./-]+))?)?(?:,?\s*(?:and\s+)?\b(?:commits?\s+)?(?=[0-9a-f]*\d)(?=[0-9a-f]*[a-f])[0-9a-f]{7,40}\b)+(?=\s*(?:[.,;:!?()]|$))/g, "")
    .replace(/(?:~\/|\.{1,2}\/|\/)?(?:[\w.@-]+\/)+([\w@-]+(?:\.\w+)*)(?::\d+(?:-\d+)?)?(?=[\s,.;:)]|$)/g, "$1")
    .replace(/\(\s*[,;]?\s*\)/g, "").replace(/\s+([,.;:])/g, "$1").replace(/\s+/g, " ").trim()).filter(l => l && !/^[\s\W]*$/.test(l));
  if (!ls.length || house(ls[0])) return "";
  const x = ls.join(" "), m = x.match(/^.{20,200}?[.!?](\s|$)/);
  let one = (m ? m[0] : x).trim();
  if (coded && /^[a-z]+\b/.test(one)) one = one[0].toUpperCase() + one.slice(1);   // "a5db4: built the deck." reads "Built the deck.", not "built …"; "ipv6's route" keeps its case
  return one.length > 140 ? one.slice(0, 139) + "\u2026" : one;
}
/** The ask whose closing post a finished conversation's row shows: its own ask (the one its first post made) when it was done since the last visit, else
 *  the newest done one. A later side ask's answer ("the answer to your warden question", a quiz workbook) read as what the conversation finished (ad748,
 *  2026-11-25); an own ask done days before would put an old answer under a new date (a54ea's review). A slug-named thread's first post is its own ask. */
const ownAsk = b => { const d = S.threads.get(b.thread); return b.post === b.thread || !!(d && d.posts.length && b.post === d.posts[0].id); };
const closer = (a, done) => done.find(b => b.thread === a.thread && ownAsk(b) && b.done_at && Date.parse(b.done_at) > SEEN_BEFORE) || a;
/** Another ask still open in a finished thread: say whose move it is. "Still open" alone read as a wait on the person (reading 20261113-1736: three of four readers of an all-answered copy listed two such threads as waiting). */
const moreAsk = a => { const o = (FIN.asks || []).filter(b => b.thread === a.thread && b.id !== a.id); return o.some(b => b.status === "person") ? "person" : o.some(b => b.status === "open" || b.status === "owned") ? "hive" : null; };
function finishedItems() {
  const out = [], seenTh = new Set(), waits = new Set(waitingOn().map(w => w.th)), live = th => !waits.has(th) && S.threads.has(th) && !isSystemThread(S.threads.get(th)) && !btwParent(th);
  const done = (FIN.asks || []).filter(a => a.status === "done");
  for (const a of done.filter(a => a.done_at && Date.parse(a.done_at) > SEEN_BEFORE && live(a.thread)).sort((a, b) => (a.done_at < b.done_at ? 1 : -1))) {
    if (seenTh.has(a.thread)) continue; seenTh.add(a.thread); out.push({ th: a.thread, t: Date.parse(a.done_at), what: outcome(closer(a, done).evidence) || outcome(a.evidence), more: moreAsk(a) });
  }
  const rest = [...S.threads.values()].filter(d => !seenTh.has(d.name) && live(d.name) && d.last > SEEN_BEFORE && startedByPerson(d.name) && /(^|\n)Done:/.test(String((S.threadStatus && S.threadStatus[d.name] || {}).text || "").trim().split("\n").pop() || "") && !waitingOn().some(w => w.th === d.name))
    .sort((a, b) => b.last - a.last);
  for (const d of rest) out.push({ th: d.name, t: d.last, what: "" });
  return out;
}
const TIP = { v: null, asked: false };   // the day-2 / day-5 tip from /api/tip, fetched once per page load, shown until closed
function homeTip() {
  if (!TIP.asked) { TIP.asked = true; jget("/api/tip").then(v => { TIP.v = v; poll(); }).catch(() => {}); }
  const t = TIP.v; if (!t || !t.tip || !t.text) return null;
  return h("section", { class: "cm-tip", "data-key": "tip", "aria-label": "A tip" },
    h("span", null, t.text, " "),
    button("Got it", () => { const k = t.tip; TIP.v = null; apiPost("/api/tip-dismiss", { tip: k }).catch(() => {}); poll(); }, { size: "s", cls: "cm-tip-x" }));
}
const AWAY = { v: null, asked: false, shut: false };   // /api/away-banner: the gap and job counts after a visit that follows 3+ quiet days, fetched once per page load
function awayLine() {
  if (!AWAY.asked) { AWAY.asked = true; jget("/api/away-banner").then(v => { AWAY.v = v; poll(); }).catch(() => {}); }
  const a = AWAY.v; if (AWAY.shut || !a || !a.jobs) return null;
  const w = waitingOn().length, n = k => `${k} job${k === 1 ? "" : "s"}`;
  return h("section", { class: "cm-away", "data-key": "away", "aria-label": "While you were away" },
    h("span", null, `While you were away (${plural(a.days, "day")}): ${n(a.jobs)} finished, ${a.ok} without a problem.`, " ",
      w ? (w === 1 ? "One question is waiting for you, above." : `${w} questions are waiting for you, above.`) : "Nothing is waiting for you."),
    button("Got it", () => { AWAY.shut = true; apiPost("/api/away-dismiss", {}).catch(() => {}); poll(); }, { size: "s", cls: "cm-tip-x" }));
}
function homeFinished() {
  const it = finishedItems(); if (!it.length) return null;
  const row = x => h("li", { class: "cm-fin-r", "data-key": "fin:" + x.th }, h("a", { href: threadHref(x.th) }, threadTitle(x.th).replace(/\s*\((?=[0-9a-f]*\d)(?=[0-9a-f]*[a-f])[0-9a-f]{7,40}\)/g, "")), x.what ? h("span", { class: "cm-old-q" }, " \u2014 ", x.what) : null, h("span", { class: "meta" }, " \u00b7 ", when(x.t)), x.more ? h("span", { class: "meta cm-fin-more" }, x.more === "person" ? " \u00b7 another ask here waits on you" : " \u00b7 the Hive is still on another ask here") : null);
  return h("section", { class: "cm-older cm-fin", "data-key": "fin", "aria-label": "Finished since your last visit" },
    h("details", { class: "cm-older-d", open: FIN.open ? "" : null, ontoggle: (e, d) => { if (e.target === d) FIN.open = d.open; } },
      h("summary", null, h("b", null, "Finished since your last visit"), h("span", { class: "meta" }, ` \u00b7 ${plural(it.length, "conversation")}`)),
      h("ul", { class: "cm-old-l" }, it.slice(0, 80).map(row), it.length > 80 ? h("li", { class: "meta" }, `${it.length - 80} older ones not listed`) : null)));
}
/**
 * A conversation's row outside the lists here (a focus card, the focus page): the mark and the word the conversation list and the top of the chat say
 * (stand(), model.js threadStand), so a card can't word a state its own way. It did (HA-11 review): "paused" where the chat said Stopped, "waiting"
 * for Waiting on others, "failed, not fixed" for Blocked, "needs you" on an ask the person had put off, and nothing at all on a Done or a Paused one.
 * fresh is the one thing a stand doesn't say: work the person started that went Done since this browser's last visit (SU-3).
 */
export function rowStand(th) {
  const s = stand(th), d = S.threads.get(th), line = standLine(s);
  return { s, line, mark: s ? standMark(s) : null, word: standWord(s, line),
    fresh: !!(s && s.word === "Done" && d && d.last > SEEN_BEFORE && startedByPerson(th)) ? h("span", { class: "rmk", "data-tone": "good", title: "Finished since your last visit" }, "new") : null };
}
function cardRow(r) {
  const d = S.threads.get(r.th), title = threadTitle(r.th), x = rowStand(r.th);
  // four cells always (the mark's and the word's are empty when the conversation stands on nothing), so the titles and the times line up down the card
  return h("li", { "data-key": r.th }, h("a", { class: "fc-row", href: threadHref(r.th), title },
    x.mark || h("span", { "aria-hidden": "true" }), h("b", null, title), h("span", { class: "fc-rw" }, x.word, x.fresh), d ? when(d.last, "t") : h("span", { "aria-hidden": "true" })));
}
// A card line names a file, not its path: "`projects/x/build/cut.mp4`" reads "cut.mp4" (home_plain: the cards show no file path). The title keeps the whole line.
const fileNames = t => t.replace(/`?(?:~\/|\/home\/[^\s`/]+\/)?(?:projects|kb|modules|core)\/(?:[^\s`]*\/)?([^\s`/]+)`?/g, "$1");
/** The mark on what is new to the person since they last looked (core/foci.js cardSince, pageSince): read out as "New:", drawn as a bar by the row's data-new. */
export const newMark = () => h("span", { class: "vh" }, "New: ");
const cardEv = (kind, since) => l => { const nw = newSince(l.at, since);
  return h("li", { class: "fc-ev", "data-kind": kind, "data-new": nw ? "" : null, "data-key": kind + ":" + l.text.slice(0, 80) }, when(l.at, "t"), h("span", { class: "fc-ev-x", title: l.text }, nw ? newMark() : null, lineText(labelCodes(fileNames(l.text)), { pill: false }))); };
// With an In short the card leads with it and gives the lines less room: two of Now and one of Lately, the ones new to the person first in line for
// the room (their order kept). The page has every line.
const LEAD_NOW = 2, LEAD_LATELY = 1, LEAD_OLD = 36 * 3600e3;
/** n lines for a card: in their own order, but when not all fit, the ones new to the person take the room first. */
function pickLines(lines, n, since) {
  if (lines.length <= n) return lines;
  const fresh = lines.filter(l => newSince(l.at, since)).slice(0, n), keep = new Set(fresh);
  for (const l of lines) { if (keep.size >= n) break; keep.add(l); }
  return lines.filter(l => keep.has(l));
}
/** A focus's state word in the interface language. A language that says a focus's state differently from a conversation's (Spanish: "bloqueado",
 *  "bloqueada") keeps the focus's word under "focus: <state>" (core/lang-es-4.js); without one it is the plain word. */
const focusState = s => { const k = "focus: " + s, t = tr(k); return t === k ? s : t; };
/** The state an In short had before it turned, as words: "was at risk", and "no longer needs you" (never 'was needs you'; and not 'needed you',
 *  which readers of a List row, where the tag stands alone under its group's heading, took for a focus that might still need them: the lab's
 *  readings 20261116-0820, -0851 and -0917, 5 of 12 readers of a copy where nothing waited). The two phrases the app never shows are in single
 *  quotes so test/lang_left.mjs, which reads every double-quoted string in js/, does not list them as English left. */
const wasWord = w => (w === "needs you" ? "no longer needs you" : "was " + w);
/** An In short's state as the card's and the page's word. Two things keep it honest (review C1, C2): a word said more than LEAD_OLD ago is drawn
 *  muted with its age beside it, since "on track" from two days back is a memory, not a reading; and while the change is new to the person
 *  (`since`: their last look), the state it had before is said after it ("was on track"). moving: agents are on the focus now. */
export function leadWord(lead, since, moving) {
  const old = !!lead.at && Date.now() - lead.at > LEAD_OLD, turned = !!lead.was && newSince(lead.turned, since);
  const title = (old ? `In short, said ${ago(lead.at)} and not since: ${lead.state}` : `In short: ${lead.state}`) + (turned ? ", " + wasWord(lead.was) : "") + (moving ? ", and agents are on it now" : "");
  return [statusWord(old ? "quiet" : lead.tone, focusState(lead.state), title, old ? "fc-st fc-st-old" : "fc-st"),
    turned ? h("span", { class: "fc-was", "data-key": "fc-was" }, wasWord(lead.was)) : null,
    old ? h("span", { class: "fc-short-t", "data-key": "fc-said" }, "said ", when(lead.at, "t")) : null];
}
/** How many things on a focus's card are new to the person (core/foci.js cardSince): its In short, and every Now and Lately line, drawn on the card or not. */
export const cardNews = f => { const since = f && !f.misc ? cardSince(f) : null, lead = since ? leadOf(f) : null;
  return since ? (lead && newSince(lead.since, since) ? 1 : 0) + [...sumLines(f.summary, "now"), ...sumLines(f.summary, "lately")].filter(l => newSince(l.at, since)).length : 0; };
const cardRows = (rows, n) => rows.length ? h("div", { class: "fc-cv" }, h("ul", { class: "fc-rows", "aria-label": "Conversations" }, rows.slice(0, n).map(cardRow))) : null;
/** The card's … menu: move it earlier or later (the keyboard's drag), and a new conversation. */
function cardMenu(f, id, ctx) {
  const all = foci().map(x => x.id), at = all.indexOf(id), mv = all.length > 1, open = C.menu === "fc:" + id, name = focusName(f);
  const close = () => { C.menu = null; ctx.update(); };
  const pz = !simpleOn() && can("tier") ? pauseOf("focus", id) : null;
  const item = (label, fn, off) => h("button", { type: "button", class: "menu-i", role: "menuitem", disabled: off ? "" : null, onclick: () => { C.menu = null; fn(); ctx.update(); } }, label);
  return h("span", { class: "fc-more pop" },
    h("button", { type: "button", class: "btn fc-moreb", "data-kind": "icon", "data-size": "s", "aria-haspopup": "menu", "aria-expanded": String(open), "aria-label": `More for ${name}: ${mv ? "move earlier or later, " : ""}new conversation`, title: "More",
      onclick: () => { C.menu = open ? null : "fc:" + id; ctx.update(); } }, icon("more")),
    open ? [scrim(close), h("div", { class: "cm-menu menu fc-menu", role: "menu", "data-side": "bottom-end", "data-key": "fcm" },
      mv ? item("Move earlier", () => moveFocus(id, { step: -1 }).then(err => moved(err, ctx)), at <= 0) : null,
      mv ? item("Move later", () => moveFocus(id, { step: 1 }).then(err => moved(err, ctx)), at >= all.length - 1) : null,
      item("New conversation", () => newIn(id)),
      pz ? item(pz.held ? "Resume" : "Pause…", () => askPause("focus", id, null, ctx.update)) : null)] : null);
}
function focusCard(f, ctx, onlyNew) {
  const id = f.id || null, page = href("focus", id || "misc"), rows = activeThreads(id).filter(r => !archived(r.th)), up = updatedAt(f, rows);
  if (f.misc) return h("article", { class: "fc", "data-key": "fc:misc", "data-misc": "", "data-state": "misc" },
    h("header", { class: "fc-hd" }, h("a", { class: "fc-name", href: page, title: rows.length > MISC_THREADS ? `Other: all ${rows.length} conversations` : "Other" }, "Other"), h("span", { class: "fc-tools" }),
      h("div", { class: "fc-lead" }, h("span", { class: "meta" }, "Not filed under a focus"), when(up, "t", "updated"))),
    cardRows(rows, MISC_THREADS));
  const st = stateOf(id), sum = f.summary, ag = agentsOf(id), asked = askedOf(id), nd = needsOf(id), n = Math.max(nd.length, asked.length);
  const dnd = focusDnd(id, ctx), since0 = onlyNew ? cardSince(f) : null, keepNew = l => !onlyNew || newSince(l.at, since0);   // New only (HA-8): the lines the person has seen are not drawn
  const now = sumLines(sum, "now").filter(keepNew), lately = sumLines(sum, "lately").filter(keepNew);
  const pillsOf = ag.now.length ? ag.now : [];
  // The In short (core/foci.js leadOf): its state is the card's word, and the live one ("moving", "needs you") is said by the agent pills beside it
  // and the Needs you block under it. What is new to the person since they last looked at the home or this focus's page is marked.
  const lead = leadOf(f), since = cardSince(f), leadNew = !!lead && newSince(lead.since, since), hid = hiddenAsks(f);
  const nNow = lead ? LEAD_NOW : HOME_NOW, nLately = lead ? (now.length ? LEAD_LATELY : HOME_LATELY) : HOME_LATELY;
  const more = Math.max(0, now.length - nNow) + Math.max(0, lately.length - nLately);   // with or without an In short: a card never drops lines without saying so
  const shown = [...pickLines(now, nNow, since), ...pickLines(lately, nLately, since)], moreNew = more ? [...now, ...lately].filter(l => newSince(l.at, since) && !shown.includes(l)).length : 0;
  const word = lead && lead.state ? leadWord(lead, since, st.key === "moving" || st.moving) : statusWord(st.tone, st.word);
  return h("article", { class: "fc", "data-key": "fc:" + id, "data-state": st.key, "data-lead": lead ? lead.key || "" : null, "data-needs": asked.length ? "" : null, ...dnd.drop },
    h("header", { class: "fc-hd", ...dnd.grab },
      h("a", { class: "fc-name", href: page, title: focusName(f) + (f.aim ? ": " + f.aim : ""), draggable: dnd.grab.draggable ? "false" : null }, focusName(f)),
      // + and … sit where the time was and show on hover or focus (the Nov 8 handoff §1.4); the time ends the lead line.
      h("span", { class: "fc-tools" }, can("post") ? button("New conversation in " + focusName(f), () => newIn(id), { kind: "icon", size: "s", icon: "plus", cls: "fc-new" }) : null,
        can("post") ? cardMenu(f, id, ctx) : null),
      h("div", { class: "fc-lead" }, word, simpleOn() || !can("tier") ? null : pausedStrip("focus", id, null, ctx.update) || focusTierChip(id),
        pillsOf.length ? h("span", { class: "fc-ag" }, pillsOf.slice(0, HOME_AGENTS).map(a => simpleOn() ? null : agentRef(a, { pill: true })),
          !simpleOn() && pillsOf.length > HOME_AGENTS ? h("span", { class: "pill pill-more", title: pillsOf.slice(HOME_AGENTS).join(", ") }, "+" + (pillsOf.length - HOME_AGENTS)) : null) : null,
        when(up, "t", "updated"))),
    lead && (!onlyNew || leadNew) ? h("p", { class: "fc-short", "data-new": leadNew ? "" : null, "data-key": "fc-short", title: lead.body }, leadNew ? newMark() : null, lineText(labelCodes(fileNames(lead.body)), { pill: false, topic: false })) : null,
    // Needs you: the summary's own asks (the person's rule), at most two; the count and All N from the one list.
    asked.length ? h("section", { class: "fc-ask", "aria-label": "Needs you" },
      h("h3", { class: "fc-ask-h" }, h("span", { class: "st", "data-tone": "info", "aria-hidden": "true" }), `Needs you · ${n}`, n > HOME_NEEDS ? h("a", { href: page }, `All ${n}`) : null),
      h("ul", null, asked.slice(0, HOME_NEEDS).map((t, i) => askItem(f, i, t)))) : null,
    !asked.length && nd.length ? h("p", { class: "fc-ask fc-ask-n" }, h("span", { class: "st", "data-tone": "info", "aria-hidden": "true" }), h("a", { href: page }, `${nd.length} wait${nd.length === 1 ? "s" : ""} on you in its conversations`)) : null,
    // A Now line that reads as the person's to do though no ask is listed (focus.py `hidden`): the card points at it, the page lists it under Needs you.
    // Beside listed asks too ("2 more…"), so the card never counts fewer things than the page lists under Needs you (review C6).
    hid.length ? h("p", { class: "fc-ask fc-ask-n fc-ask-h2", "data-key": "fc-hid" }, h("span", { class: "st", "data-tone": "info", "aria-hidden": "true" }),
      h("a", { href: page }, asked.length || nd.length ? `${hid.length} more line${hid.length === 1 ? "" : "s"} here may be yours` : hid.length === 1 ? "1 line here may be yours to act on" : `${hid.length} lines here may be yours to act on`)) : null,
    now.length || lately.length ? h("div", { class: "fc-tl" },
      now.length ? h("ol", { "aria-label": "Now" }, pickLines(now, nNow, since).map(cardEv("now", since))) : null,
      lately.length ? h("ol", { class: "fc-lately", "aria-label": "Lately" }, pickLines(lately, nLately, since).map(cardEv("lately", since))) : null,
      more ? h("a", { class: "fc-more-l", href: page, "data-key": "fc-more-l" }, `${more} more line${more === 1 ? "" : "s"}`, moreNew ? `, ${moreNew} new` : null) : null)
      : lead || onlyNew ? null : h("p", { class: "fc-none" }, sum ? "Nothing listed as moving." : "No summary yet. The agents on it write one as they work."),
    onlyNew ? null : cardRows(rows, HOME_THREADS));
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
/** The phone's one button on a waiting row: a two-item menu with Later and Not needed (the three buttons don't fit at 390). */
function needMore(w) {
  const k = "nd:" + (w.key || w.th + ":" + (w.post || w.src)), open = C.menu === k, upd = () => L.ctx && L.ctx.update();
  const [later, moot] = laterButtons(w), item = b => { b.classList.add("menu-i"); b.setAttribute("role", "menuitem"); b.removeAttribute("data-kind"); b.removeAttribute("data-size"); const go = b._on && b._on.click; (b._on ||= {}).click = e => { C.menu = null; go && go(e); upd(); }; return b; };
  return h("span", { class: "pop cm-need-mp" },
    h("button", { type: "button", class: "btn cm-need-more", "data-kind": "icon", "aria-haspopup": "menu", "aria-expanded": String(open), "aria-label": "Later or not needed", title: "Later or not needed",
      onclick: e => { e.stopPropagation(); C.menu = open ? null : k; upd(); } }, icon("more")),
    open ? [scrim(() => { C.menu = null; upd(); }), h("div", { class: "cm-menu menu", role: "menu", "data-side": "bottom-end", "data-key": "ndm" }, item(later), item(moot))] : null);
}
/** Goals the person approved (projects/word-shell/GOALS.md): each one lets work inside it go ahead without asking again, until it ends or is withdrawn.
 *  From root's published list (/api/goals); nothing shows until it has one. Withdraw opens the same approval card as any approval, with the goal's file named. */
const GOALS = { list: null, err: false, at: 0, open: true };
function loadGoals(ctx) { if (now() - GOALS.at < MIN) return; GOALS.at = now(); jget("/api/goals").then(v => { const l = (v && v.present && Array.isArray(v.goals)) ? v.goals : [], err = !!(v && v.present && v.error); if (err !== GOALS.err || JSON.stringify(l) !== JSON.stringify(GOALS.list)) { GOALS.list = l; GOALS.err = err; ctx.update(); } }).catch(() => { GOALS.at = 0; }); }
const goalEnds = e => { const d = new Date(e); return isNaN(d) ? "" : d.toLocaleDateString(undefined, { weekday: "short", month: "short", day: "numeric" }); };
/** The card's withdrawal line for a goal: its file, then a short reminder of its words. The reminder keeps to plain letters (the card refuses others) and
 *  drops anything shaped like a goal's file name: root withdraws every file the line names, so a goal quoting another's would withdraw both. */
const WORD_FILE = /\d{8}T\d{6}Z-[A-Za-z0-9-]{1,60}\.txt/g;
export const withdrawWords = g => {
  let t = String(g.goal || "").replace(/[^\x20-\x7e]/g, " "), was;
  do { was = t; t = t.replace(WORD_FILE, " "); } while (t !== was);
  t = t.replace(/[()]/g, " ").replace(/\s+/g, " ").trim();
  return `withdraw standing: ${g.file}` + (t ? ` (${t.length > 60 ? t.slice(0, 59).trim() + "\u2026" : t})` : "");
};
function homeGoals() {
  const gs = GOALS.list || []; if (!gs.length && !GOALS.err) return null;
  const at = g => g.thread && S.threads.has(g.thread) ? g.thread : "goals";   // withdrawn in the thread it was given in, else a thread of its own
  const row = g => h("li", { class: "cm-goal", "data-key": "goal:" + g.file },
    h("p", { class: "cm-goal-t" }, g.goal),
    h("p", { class: "cm-goal-m meta" }, `Until ${goalEnds(g.ends)}`, typeof g.uses === "number" ? ` \u00b7 ${g.uses ? "used " + plural(g.uses, "time") : "not used yet"}` : null,
      g.thread && S.threads.has(g.thread) ? [" \u00b7 from ", h("a", { href: threadHref(g.thread) }, threadTitle(g.thread))] : null),
    can("post") && forCard(at(g), withdrawWords(g)) && !cardWhy(withdrawWords(g)) ? button("Withdraw", () => openApproval(at(g), withdrawWords(g)), { kind: "quiet", size: "s", cls: "cm-goal-w", title: "Opens a card to withdraw this goal. After that, work it covered asks you again." }) : null);
  return h("section", { class: "cm-older cm-goals", "data-key": "goals", "aria-label": "Your goals" },
    h("details", { class: "cm-older-d", open: GOALS.open ? "" : null, ontoggle: (e, d) => { if (e.target === d) GOALS.open = d.open; } },
      h("summary", null, h("b", null, "Your goals"), gs.length ? h("span", { class: "meta" }, ` \u00b7 ${plural(gs.length, "goal")} in force`) : null),
      h("p", { class: "cm-goal-say meta" }, GOALS.err ? "Your goals can't be read right now. They still count; this list will be back shortly." : "Work inside these goes ahead without asking you again. Anything else still asks."),
      gs.length ? h("ul", { class: "cm-old-l" }, gs.map(row)) : null));
}
function homeNeeds(ws) {
  if (!ws.length) return null;
  const row = w => {
    const d = S.threads.get(w.th), p = w.post && d ? d.posts.find(x => x.id === w.post) : null, who = (w.st && w.st.who) || (p && p.from) || null;
    const p0 = p || (w.post && S.byId[w.post]) || null;   // the question itself, for its one-tap choices (ui/pick.js)
    const to = w.src === "summary" && w.focus ? href("focus", w.focus) : threadHref(w.th), open = answerer(w), lf = liftIn(w.text);
    // The whole row opens the panel (the question holds agent and thread links, so it can't be a button itself); Answer is the keyboard's way.
    return h("li", { class: "cm-need", "data-hover-acts": open ? "" : null, "data-key": w.key || w.th + ":" + (w.post || w.src), "data-open": open ? "" : null, onclick: open ? (e, n) => { if (!e.target.closest("a,button")) open(e, n); } : null },
      // A trailing ", in #thread" goes to the meta line, as on the card (V3 handoff point 9), so no pill sits mid-sentence.
      h("p", { class: "cm-need-q" }, lineText(lf.text, { pill: false })),
      h("p", { class: "cm-need-m meta" }, h("a", { href: to }, needSource(w)), lf.th && lf.th !== w.th ? [h("span", { class: "cm-need-in" }, " \u00b7 in "), h("a", { class: "th", href: threadHref(lf.th) }, threadTitle(lf.th))] : null,
        who ? [h("span", { class: "cm-need-wsep" }, " \u00b7 "), simpleOn() || !isCode(who) ? h("span", { class: "cm-need-who" }, plainName(who)) : agentRef(who, { dot: false, pill: true })] : null, w.t ? [" \u00b7 ", when(w.t)] : null),
      open && w.src === "live" && p0 ? pickBar({ ...p0, disp: displayText(p0) }, { cls: "cm-need-pk" }) : null,
      // Its time rests at the right; on hover or focus the actions take its place (data-hover-acts, css §0). On a phone one … holds Later and Not needed and the row tap is Answer.
      open ? h("div", { class: "cm-need-b" }, w.t ? h("span", { class: "row-t meta" }, when(w.t, "t")) : null,
        h("span", { class: "row-acts" }, laterButtons(w), h("button", { type: "button", class: "btn cm-need-a", "data-size": "s", onclick: open }, "Answer")), needMore(w))
        : h("div", { class: "cm-need-b" }, h("a", { class: "btn cm-need-a", "data-size": "s", href: to }, "Answer")));
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
    h("details", { class: "cm-older-d", open: C.olderOpen ? "" : null, ontoggle: (e, d) => { if (e.target === d) C.olderOpen = d.open; } },   // kept in view state: a live re-render shut it (the person, 110903147963)
      h("summary", null, h("b", null, "Stuff to address"), h("span", { class: "meta" }, ` \u00b7 ${plural(ws.length, "older thing")}, likely moved past or not urgent`)),
      h("ul", { class: "cm-old-l" }, ws.slice(0, 12).map(row),
        ws.length > 12 && needsHref() ? h("li", { class: "cm-need-all" }, h("a", { href: needsHref() }, `All ${ws.length}`)) : null)),
    can("post") ? button("Go through them with an agent", () => prefill(null, OLDER_ASK), { kind: "quiet", size: "s" }) : null);
}
const firstLine = t => { const s = mdPlain(String(t || "")).replace(/\s+/g, " ").trim(); return s.length > 140 ? s.slice(0, 139) + "\u2026" : s; };
/** Recently moved (the person, 110901466282): which conversations moved between focuses in the last three days, so one that left the
 * place you knew it in can be found. From focus.py's moved list (the focus CLI's moved command); shut by default, like Stuff to address. */
const MOVED_FOR = 3 * DAY;
function homeMoved() {
  const ms = ((S.foci && S.foci.moved) || []).filter(m => m && m.at * 1e3 > now() - MOVED_FOR && S.threads.has(m.thread) && !isSystemThread(S.threads.get(m.thread)) && !btwParent(m.thread));
  if (!ms.length) return null;
  const place = id => id ? h("a", { href: href("focus", id) }, (focusById(id) || {}).name || id) : h("span", null, "Other");
  const row = m => h("li", { class: "cm-old cm-mv", "data-key": m.thread + ":" + m.at },
    h("a", { href: threadHref(m.thread) }, threadTitle(m.thread)),
    h("span", { class: "cm-old-q" }, m.was ? [place(m.was), " \u2192 "] : "into ", place(m.to)),
    h("span", { class: "meta" }, ago(m.at * 1e3), m.by === "person" ? " \u00b7 by you" : m.by && m.by !== "anon" ? [" \u00b7 by ", agentRef(m.by, { dot: false })] : null));
  return h("section", { class: "cm-older cm-moved", "data-key": "moved", "aria-label": "Recently moved" },
    h("details", { class: "cm-older-d", open: C.movedOpen ? "" : null, ontoggle: (e, d) => { if (e.target === d) C.movedOpen = d.open; } },   // kept in view state: a live re-render shut it and its threads vanished (the person, 110903147963)
      h("summary", { title: `${plural(ms.length, "conversation")} put in a focus or moved in the last 3 days` }, h("b", null, "Recently moved"), h("span", { class: "cm-mv-n" }, String(ms.length))),   // the sentence in the title (Nov 9 handoff §C.1)
      h("ul", { class: "cm-old-l" }, ms.slice(0, 40).map(row))));
}
// While the home's reads are out: grey in the places the line, its rows and the cards land, so they arrive where these stood (R65).
const WAIT_W = ["72%", "54%", "64%", "46%"];
// R67: at the height the line and rows took last time, or Focuses is pushed down when they land (384 px on a phone).
const waitsRoom = () => { const home = document.querySelector(".cm-home"), foci = home && home.querySelector(".wkb:not(.wkb-held),.cm-foci-h:not([data-wait])"), first = home && home.querySelector(".cm-hline,.cm-needs,.cm-older:not(.cm-moved)"); return foci && first ? foci.getBoundingClientRect().top - first.getBoundingClientRect().top - parseFloat(getComputedStyle(home).rowGap || 0) : 0; };
const waitsHeld = px => h("div", { class: "cm-hwait", "data-key": "waits-held", role: "status", style: px ? `height:${px}px;flex:none;overflow:hidden` : null }, h("span", { class: "vh" }, "Reading\u2026"),
  h("div", { class: "cm-hwait-l" }, skBar("18%", 11)),
  h("div", { class: "cm-hwait-b" }, [0, 1, 2].map(i => h("div", { class: "cm-hwait-r", "data-key": "r" + i }, skBar(WAIT_W[i], 12), skBar("34%")))));
const fociHeld = (room, barPx, list) => [workBarHeld(barPx), h("div", { class: "sec-h cm-foci-h", "data-key": "foci-h", "data-wait": "" }, skBar("84px", 11)),
  list ? h("div", { class: "cm-foci", "data-key": "foci", "data-view": "list", style: room }, h("div", { class: "fo-g" }, WAIT_W.map((w, i) => h("div", { class: "fo-r cm-fwait", "data-key": "w" + i }, skBar("34%", 12), skBar(w, 11)))))
    : h("div", { class: "cm-foci", "data-key": "foci", style: room }, WAIT_W.map((w, i) => h("div", { class: "skel-card cm-fwait", "data-key": "w" + i }, skBar("40%", 12), skBar(w, 11), skBar("84%"), skBar("62%"))))];
/** Beside "Focuses": how many of them say something new since the person last looked at the home, with the bar that marks the new lines on
 *  their cards (so the bar is explained where it is first met). Nothing before their second visit: there is no look to count from. */
function fociNews(fs) {
  const hs = S.foci && S.foci.home_seen; if (!hs || !hs.since) return null;
  const n = fs.filter(f => cardNews(f)).length;
  return h("span", { class: "cm-foci-new", "data-key": "foci-new", "data-new": n ? "" : null }, n ? h("i", { "aria-hidden": "true" }) : null,
    n ? `${n} with news` : "Nothing new", h("span", { class: "cm-foci-new-t" }, " since you last looked"), h("span", { class: "sec-n" }, " \u00b7 ", when(hs.since * 1e3, "t")));   // a phone drops the tail (phone.css)
}
// The home's Focuses as a list (the person, 11160014bf8b: "I really can't get a good sense of what's going on"; nineteen cards were five screens
// of equal weight). One row a focus, under what its In short says, the ones that wait on the person first: the In short in full, and beside the
// name what is new to them since they looked, what waits on them and how many agents are on it. The cards are one click away and the choice is
// kept in this browser; with fewer than LIST_FROM focuses the cards fit a screen and stay the default. Suite: drive_home_list.
const FOCI_VIEW_KEY = "hive.home.foci", FOCI_DRAWN_KEY = "hive.home.foci.drawn", LIST_FROM = 7;
const viewRead = k => { try { const v = localStorage.getItem(k); return v === "list" || v === "cards" ? v : null; } catch (e) { return null; } };
const FV = { pick: viewRead(FOCI_VIEW_KEY), drawn: viewRead(FOCI_DRAWN_KEY) };
/** "list" or "cards": the person's pick, else by how many focuses there are; while they are still being read, as it was last drawn. */
const fociView = () => FV.pick || (S.foci === undefined ? FV.drawn || "cards" : foci().length >= LIST_FROM ? "list" : "cards");
// The groups in the order the person reads them: what is theirs, what is in trouble, what is fine, what is set down. "" holds a focus with no In
// short and one whose In short opens with no state.
const FO_GROUPS = [["needs you", "Needs you", "info"], ["blocked", "Blocked", "bad"], ["at risk", "At risk", "warn"], ["on track", "On track", "good"], ["waiting", "Waiting on others", "quiet"], ["paused", "Paused", "ended"], ["done", "Done", "ended"], ["", "No state yet", "quiet"]];
/** How many asks of this focus are listed as the person's (the card's "Needs you" count; the ones that only may be theirs are not in it). */
const yoursOf = f => yoursN(f.id || null);
/** The state a focus's own In short opens with, "" when it has none or one this list has no group for (so no row is ever dropped). */
const leadKey = f => { const k = (leadOf(f) || {}).key || ""; return FO_GROUPS.some(g => g[0] === k) ? k : ""; };
/** The group a focus is listed under: Needs you as soon as an ask of it waits on the person, whatever its In short says; else its own state. */
const groupOf = f => (f.misc ? null : yoursOf(f) ? "needs you" : leadKey(f));
/** The group a focus is listed in, its heading and tone, and its place in the order: the one answer the home List and the rail's Focus view both read (HA-7). */
function groupStand(f) { const key = groupOf(f), i = FO_GROUPS.findIndex(g => g[0] === (key || "")), g = FO_GROUPS[i < 0 ? FO_GROUPS.length - 1 : i]; return { key: g[0], head: g[1], tone: g[2], at: i < 0 ? FO_GROUPS.length - 1 : i }; }
function focusRow(f, onlyNew) {
  const id = f.id || null, page = href("focus", id || "misc"), rows = activeThreads(id).filter(r => !archived(r.th)), up = updatedAt(f, rows);
  if (f.misc) return h("li", { class: "fo-r", "data-key": "fo:misc", "data-misc": "" },
    h("div", { class: "fo-top" }, h("a", { class: "fo-name", href: page, title: "Other: conversations not filed under a focus" }, "Other"), h("div", { class: "fo-m" }, when(up, "t", "updated"))),
    h("p", { class: "fo-short fo-none" }, `${plural(rows.length, "conversation")} not filed under a focus.`));
  const lead = leadOf(f), since = cardSince(f), news = cardNews(f), working = agentsOf(id).now.length, asked = askedOf(id), nd = needsOf(id), hid = hiddenAsks(f);
  const listed = Math.max(nd.length, asked.length), mine = listed + hid.length, now = sumLines(f.summary, "now"), name = focusName(f);
  // Its state is the group's heading; the row says only what the heading can't: its own state word when an ask put it under Needs you, that the
  // word is old, or what it was before it turned.
  const own = leadKey(f), moved = groupOf(f) !== own && own ? FO_GROUPS.find(g => g[0] === own) : null;
  const old = !!lead && !!lead.state && !!lead.at && Date.now() - lead.at > LEAD_OLD, turned = !!lead && !!lead.was && newSince(lead.turned, since);
  const say = t => lineText(labelCodes(fileNames(t)), { pill: false, topic: false });
  const seenNew = onlyNew ? [...sumLines(f.summary, "now"), ...sumLines(f.summary, "lately")].filter(l => newSince(l.at, since)) : null;   // New only (HA-8): the In short only if it is new, else the newest new line
  const body = onlyNew ? (lead && newSince(lead.since, since) ? h("p", { class: "fo-short", "data-key": "fo-short" }, say(lead.body)) : seenNew.length ? h("p", { class: "fo-short fo-none", "data-key": "fo-short" }, say(seenNew[0].text)) : null)
    : lead ? h("p", { class: "fo-short", "data-key": "fo-short" }, say(lead.key && !own ? f.lead.text : lead.body))   // a state this list has no group for stays in its sentence
    : now.length ? h("p", { class: "fo-short fo-none", "data-key": "fo-short" }, say(now[0].text))
    : h("p", { class: "fo-short fo-none", "data-key": "fo-short" }, f.summary ? "Nothing listed as moving." : "No summary yet. The agents on it write one as they work.");
  // One link a row for the focus itself, stretched over it (the name); a link inside the In short keeps its own stop and sits above it.
  return h("li", { class: "fo-r", "data-key": "fo:" + id, "data-new": news ? "" : null, "data-lead": lead ? lead.key || "" : null, "data-you": mine ? "" : null },
    h("div", { class: "fo-top" },
      h("a", { class: "fo-name", href: page, title: name + (f.aim ? ": " + f.aim : "") }, name),
      h("div", { class: "fo-m" },
        moved ? h("span", { class: "fo-st sw", "data-key": "fo-st", "data-tone": moved[2] }, h("span", { class: "st", "data-tone": moved[2], "aria-hidden": "true" }), h("span", null, moved[1])) : null,
        turned ? h("span", { class: "fo-was", "data-key": "fo-was" }, wasWord(lead.was)) : null,
        old ? h("span", { class: "fo-was", "data-key": "fo-said" }, "said ", when(lead.at, "t")) : null,
        news ? h("span", { class: "fo-new", "data-key": "fo-new" }, `${news} new`) : null,
        listed ? h("span", { class: "fo-you", "data-key": "fo-you" }, h("span", { class: "st", "data-tone": "info", "aria-hidden": "true" }), `${listed} for you`) : null,
        hid.length ? h("span", { class: "fo-maybe", "data-key": "fo-maybe" }, `${hid.length} may be yours`) : null,
        working ? h("span", { class: "fo-ag", "data-key": "fo-ag" }, h("span", { class: "st", "data-tone": "good", "data-live": "", "aria-hidden": "true" }), `${working} working`) : null,
        when(up, "t", "updated"))),
    body);
}
/** HA-13: what waits on the person in a conversation no listed focus holds (in no focus, or in one that is gone), by conversation: [th, [waits]].
 *  The badge counts these too, and the List home had no row for them (2 of the 9 counted on 2026-11-16), so its Needs you group said less than the badge. */
const looseWaits = () => { const ids = new Set(foci().map(f => f.id)), m = new Map();
  for (const w of waitingNow()) { if (!w.th || ids.has(w.focus || focusOf(w.th))) continue; if (!m.has(w.th)) m.set(w.th, []); m.get(w.th).push(w); }
  return [...m].sort((a, b) => Math.max(...b[1].map(w => w.t || 0)) - Math.max(...a[1].map(w => w.t || 0))); };
/** Such a conversation's row in Needs you: its title, one tap from its chat, with the line its top says (what it asks), as a focus row has its In short. */
function looseRow([th, ws]) {
  const s = stand(th), t = Math.max(0, ...ws.map(w => w.t || 0)), line = (s && [s.why, s.text].filter(Boolean).map(tr).join(" \u00b7 ")) || firstLine(ws[0].text);
  return h("li", { class: "fo-r fo-th", "data-key": "fo-th:" + th, "data-th": th, "data-you": "" },
    h("div", { class: "fo-top" },
      h("a", { class: "fo-name", href: threadHref(th), title: threadTitle(th) }, railTitle(th)),
      h("div", { class: "fo-m" }, h("span", { class: "fo-was", "data-key": "fo-nofocus" }, "in no focus"),
        h("span", { class: "fo-you", "data-key": "fo-you" }, h("span", { class: "st", "data-tone": "info", "aria-hidden": "true" }), `${ws.length} for you`), t ? when(t, "t") : null)),
    line ? h("p", { class: "fo-short", "data-key": "fo-short" }, line) : null);
}
/** The loose conversations to draw: under New only (HA-8), only those with a wait newer than the person's last look (k2408: drive_new_only). */
const looseShown = onlyNew => { const hs = S.foci && S.foci.home_seen, since = hs && hs.since ? hs.since * 1e3 : null;
  return onlyNew ? looseWaits().filter(([, ws]) => ws.some(w => newSince(w.t, since))) : looseWaits(); };
function fociList(fs, onlyNew) {
  const loose = looseShown(onlyNew);
  const group = ([key, head, tone]) => { const g = fs.filter(f => groupOf(f) === key), more = key === "needs you" ? loose : []; if (!g.length && !more.length) return null;
    const n = g.length + more.length, what = !more.length ? (n === 1 ? " focus" : " focuses") : !g.length ? (n === 1 ? " conversation" : " conversations") : " focuses and conversations";
    return h("section", { class: "fo-g", "data-key": "fog:" + key, "data-state": key || "none" },
      h("h3", { class: "fo-gh" }, h("span", { class: "sw", "data-tone": tone }, h("span", { class: "st", "data-tone": tone, "aria-hidden": "true" }), h("span", null, head)), h("span", { class: "fo-gn" }, h("span", { class: "vh" }, ", "), String(n), h("span", { class: "vh" }, what))),
      h("ul", { class: "fo-rows" }, g.map(f => focusRow(f, onlyNew)), more.map(looseRow))); };
  const misc = fs.filter(f => f.misc);
  return [FO_GROUPS.map(group), misc.length ? h("section", { class: "fo-g", "data-key": "fog:misc", "data-state": "misc" }, h("h3", { class: "fo-gh" }, h("span", { class: "sw", "data-tone": "quiet" }, h("span", null, "Not in a focus"))), h("ul", { class: "fo-rows" }, misc.map(focusRow))) : null];
}
// Said in words the first times the list is drawn unasked, since the cards turned into rows by themselves: why, and where the cards are. It goes
// with Got it or a pick, and after TOLD_LOADS page loads on its own, so a line nobody reads does not stay.
const FOCI_TOLD_KEY = "hive.home.foci.told", TOLD_LOADS = 3;
const TOLD = { n: (() => { try { return +localStorage.getItem(FOCI_TOLD_KEY) || 0; } catch (e) { return TOLD_LOADS; } })(), counted: false };
const toldOver = () => { TOLD.n = TOLD_LOADS; store(FOCI_TOLD_KEY, String(TOLD_LOADS)); };
function fociTold(ctx) {
  if (FV.pick || TOLD.n >= TOLD_LOADS) return null;
  if (!TOLD.counted) { TOLD.counted = true; store(FOCI_TOLD_KEY, String(TOLD.n + 1)); }
  return h("p", { class: "cm-foci-told", "data-key": "foci-told", role: "note" },
    h("span", null, "With this many focuses, the home lists them: the ones that wait on you come first. Cards, beside the heading, shows every focus with its lines."),
    button("Got it", () => { toldOver(); ctx.update(); }, { kind: "quiet", size: "s", title: "Hide this line" }));
}
/** List or Cards, beside the Focuses heading: the person's pick, kept in this browser. */
const fociPick = (view, ctx) => seg([["list", "List", "One row a focus, under what its In short says"], ["cards", "Cards", "A card a focus, with its lines and conversations"]], view,
  v => { FV.pick = v; store(FOCI_VIEW_KEY, v); toldOver(); ctx.update(); }, { label: "Show the focuses as" });
// "New only" (HA-8): one switch beside the pick; it hides every focus, row and card line the person has seen, so a return shows what changed since. Kept in this browser.
const NEWONLY_KEY = "hive.home.newonly", NEWONLY = { on: (() => { try { return localStorage.getItem(NEWONLY_KEY) === "1"; } catch (e) { return false; } })() };
const newOnlyPick = ctx => h("button", { type: "button", class: "btn cm-newonly", "data-kind": "quiet", "data-size": "s", "data-key": "newonly", "aria-pressed": String(NEWONLY.on), title: "Hide what you have already seen",
  onclick: () => { NEWONLY.on = !NEWONLY.on; store(NEWONLY_KEY, NEWONLY.on ? "1" : "0"); ctx.update(); } }, "New only");
function fociGrid(ctx, settled) {
  const view = fociView(), list = view === "list";
  const px = heldRoom(list ? "home-foci-list" : "home-foci", ".cm-foci", { settled }), room = px ? `min-height:${px}px` : null;   // the cards gain lines as the waits arrive
  // A failed first read leaves an empty stand-in (core/foci.js): say so, not "no focus" (the person, 110901516bcd: cards gone after a dash restart).
  const barPx = heldRoom("home-workbar", ".wkb:not(.wkb-held)", { settled });
  if (S.foci === undefined && !fociFailed()) return fociHeld(room, barPx, list);
  if (S.foci === undefined || S.foci.failed && fociFailed()) return h("p", { class: "meta cm-foci-msg" }, "Couldn't read the focuses. It keeps trying.");
  const fs = fociOrder();
  const hs = S.foci.home_seen, canNew = !!(hs && hs.since), onlyNew = canNew && NEWONLY.on, shown = onlyNew ? fs.filter(f => !f.misc && cardNews(f)) : fs;
  if (!fs.length) return h("p", { class: "meta cm-foci-msg" }, "No focus yet. Agents make one when work has a goal; every conversation is in the list on the left.");
  if (FV.drawn !== view) { FV.drawn = view; store(FOCI_DRAWN_KEY, view); }   // so the next visit's grey stand-in has the same shape
  return [workBar(S.foci.activity), h("div", { class: "sec-h cm-foci-h", "data-key": "foci-h" }, h("h2", { class: "sec-t" }, "Focuses"), h("span", { class: "sec-n" }, String(fs.filter(f => !f.misc).length)), fociNews(fs), canNew ? newOnlyPick(ctx) : null, fociPick(view, ctx), h("a", { href: href("flow") }, "Flow")),
    onlyNew && !shown.length && !looseShown(true).length ? h("p", { class: "cm-foci-none", "data-key": "foci-none", role: "status" }, "Nothing new since you last looked, ", when(hs.since * 1e3, "t"), ".")
    : list ? h("div", { class: "cm-foci", "data-key": "foci", "data-view": "list", "data-new-only": onlyNew ? "" : null, style: room }, fociTold(ctx), fociList(shown, onlyNew))
      : h("div", { class: "cm-foci", "data-key": "foci", "data-new-only": onlyNew ? "" : null, style: room }, shown.map(f => focusCard(f, ctx, onlyNew)))];
}
/** #comms/~archived: every archived conversation, newest first, each with Bring back (the person, 11081454b6bd). Archived by hand or by age (ui/threadmeta.js). */
function archivedPane(c, ctx) {
  const mask = showMask(), back = th => tryMeta(th, { archived: false }, ctx);
  const ds = [...S.threads.values()].filter(d => archived(d.name) && (threadKinds(d) & mask) && theirs(d)).sort((a, b) => b.last - a.last);
  // a row here says where its conversation stands as the top of its chat does, like every other list of conversations (HA-11)
  const rows = ds.map(d => { const lp = d.posts.at(-1), s = stand(d.name), tag = standWord(s, standLine(s)), say = inConversation(d.name, () => railSay(mdPlain(chatText(lp))));
    return { key: d.name, href: threadHref(d.name), title: railTitle(d.name), meta: ago(d.last).replace(" ago", ""), lead: standMark(s), text: [tag, tag ? " · " : null, lp.from === "person" ? ["You: ", ...say] : say],
      acts: can("post") ? button("Bring back", () => back(d.name), { size: "s", kind: "quiet", icon: "archive", title: "Back in the list", cls: "cm-arch-back" }) : null }; });
  return h("div", { class: "cm-home cm-arch", "data-key": "archived" },
    h("a", { class: "cm-arch-up only-phone", href: href("comms", "~list") }, icon("chevron-left"), "Conversations"),
    h("h1", { class: "cm-home-t" }, "Archived"),
    h("p", { class: "meta" }, "Out of the list: archived by hand, or quiet for a day (a week when it ran longer). Something new for you brings one back by itself."),
    list(rows, { empty: "Nothing archived." }));
}
// E56h: on a phone the home's composer is docked at the foot of the pane (css/phone.css) and the list ends --cm-dock above it. A fixed
// reserve let a long draft (the box grows to 120 px) cover the last rows, so the reserve follows the composer's real height. It is set on
// <html> (morph() would drop a style it didn't render) as --cm-dock-live, only while the composer is docked (position:absolute: phone width).
let dockSeen = null;
const dockSet = () => { const n = dockSeen, de = document.documentElement;
  const px = n && n.isConnected && getComputedStyle(n).position === "absolute" ? Math.ceil(n.getBoundingClientRect().height) : 0;
  if (px) de.style.setProperty("--cm-dock-live", px + "px"); else de.style.removeProperty("--cm-dock-live"); };
const dockRO = typeof ResizeObserver === "function" ? new ResizeObserver(dockSet) : null;
const watchDock = () => { const n = document.querySelector(".cm-home>.cm-new"); if (dockRO && n !== dockSeen) { if (dockSeen) dockRO.unobserve(dockSeen); dockSeen = n; if (n) dockRO.observe(n); } dockSet(); };
if (typeof window !== "undefined") window.addEventListener("resize", () => requestAnimationFrame(dockSet));
function homePane(ctx) {
  requestAnimationFrame(watchDock);
  const w = can("post"), n = needsN(), local = outFor(null), room = hasRoom(), digest = app();   // n: the one count (core/needs.js), the same as the Chat badge
  if (digest) { loadFoci(); loadNeedsYou(); loadFinished(ctx); loadGoals(ctx); if (can("tier")) loadTiers(ctx.update); }   // work tiers: the chip on each focus card   // the one needs-you load (core/needs.js): the line, the badge and the cards read S.needsYou
  // Only on arriving: taking focus on every render pulled the page back to the box after any real click elsewhere on it.
  const arrived = C.arrived; C.arrived = false;
  // The line counts the last three days and names the rest as older, so new things don't hide behind stale ones (the person, 110812234faa); the Chat badge and Yours keep the whole count.
  // Older ones (olderWhy: a day on work that moved on, two on quiet work) are out of the count and folded into Stuff to address below (the person, 11081245f002).
  const sp = needsSplit();
  // The list is built from the foci and the needs file, and each arrives on its own: until both are read its count is partial (it read 8, then 13, then 10 while loading, and "Nothing needs you" before that), so the line and rows wait.
  const settled = !digest || (needsKnown() && S.foci !== undefined && S.needsYou !== undefined);
  const waitsPx = heldRoom("home-waits", waitsRoom, { settled, usual: !C.olderOpen });
  // The waits head (the Nov 8 handoff §1.2): the count as plain words, and All to the full list on the right when there is more than the rows show.
  const waits = n ? `${n} ${n === 1 ? "waits" : "wait"} on you` : "Nothing new waits on you";
  const all = (n > HOME_WAITS || sp.older) && needsHref() ? h("a", { class: "cm-hline-all", href: needsHref(), title: sp.older ? `${plural(sp.older, "older one")} in Stuff to address` : "What waits on you, newest first, each in its conversation" }, "All") : null;
  // One count on the line (friction-dash 5): "19 wait on you · 42 older ones below · Inbox: 29 unread" put three numbers where the person wanted one.
  // The older ones have their own count on Stuff to address just below, and the Inbox count is unread messages, said where it shows (the rail's "N unread").
  const line = n || sp.older ? [h("span", null, waits), all] : simpleOn() || S.live.signin?.length ? null : IB.unread ? "No agent is waiting on you" : "Nothing needs you";
  return h("div", { class: "cm-home", "data-key": "home" },
    w ? h("div", { class: "cm-new", "data-key": "new" },
      h("h1", { class: "cm-home-t" }, "What do you want done?"),
      !room && !hiveStopped() ? h("p", { class: "cm-home-warn", "data-tone": "limited", role: "status" }, statusDot("limited"), simpleOn() ? "Your Hive has used up its allowance for now, so new work waits until it comes back." : (((S.app && S.app.setup && S.app.setup.logins) || []).some(l => l.signed_in) ? "Every Claude account is past its usage line, so new work waits until there's room. See Usage." : "No Claude account is signed in yet. Sign in under Settings > Accounts so new work can start.")) : null,
      composer({ thread: null, update: ctx.update, place: "home", autofocus: arrived && conversation(ctx.arg).open }),
      !n && !local.length && ![...S.threads.values()].some(d => d.posts.some(p => p.from === "person")) ? firstJobs() : null,
      local.some(it => it.state !== "failed") ? closeLine() : null,
      local.length ? h("div", { class: "cm-out", "data-key": "out" }, local.map(it => localMessage(it, outActions))) : null) : null,
    settled ? null : waitsHeld(waitsPx),
    settled && line ? h("p", { class: "cm-hline", "data-key": "line", "data-needs": n ? "" : null }, line) : null,
    settled && n ? homeNeeds(waitingNow()) : null,
    settled && sp.older ? homeOlder(waitingOlder()) : null,
    digest && settled ? homeGoals() : null,
    settled ? homeTip() : null,
    digest && settled ? awayLine() : null,
    digest && settled ? homeFinished() : null,
    digest ? fociGrid(ctx, settled) : null,
    digest ? homeMoved() : null,
    phoneList());
}
/** On a phone the rail is a page of its own; home lists the latest conversations instead (css/phone.css shows it only there). */
function phoneList() {
  const ds = [...S.threads.values()].filter(d => !isSystemThread(d) && !archived(d.name) && !faded(d.name)).sort((a, b) => (pinned(b.name) - pinned(a.name)) || (b.last - a.last)).slice(0, 20);
  if (!ds.length) return null;
  return h("section", { class: "sec only-phone cm-plist", "data-key": "plist", "aria-label": "Conversations" },
    h("header", { class: "sec-h" }, h("h2", { class: "sec-t" }, "Conversations")),
    // each row says where its conversation stands as the top of its chat does (HA-11): the mark, the word, then the one line under the title
    h("ul", { class: "list" }, ds.map(d => { const s = stand(d.name);
      return h("li", { class: "lr", "data-key": d.name }, h("a", { class: "lr-in", href: threadHref(d.name) },
        h("span", { class: "lr-t" }, standMark(s), h("b", null, threadTitle(d.name)), h("span", { class: "meta" }, d.last ? ago(d.last) : "")),
        s ? h("span", { class: "lr-x" }, standWord(s), standTail(s)) : null)); })));
}

// Over the whole page, not in the list: it sets what you see of the hive's talk, both in a conversation and in which ones are listed. Each one turns on and off by itself.
const SHOW_PARTS = [[SHOW.you, "For you", "your messages, what agents ask or tell you, and each job's result"],
  [SHOW.updates, "Updates", "agents' progress updates"],
  [SHOW.comms, app() ? "Behind the scenes" : "Agent comms", "agent comms: agents talking to each other and the hive's own notices"]];
function showSays(mask) {
  if (words(C.tq).length || words(C.q).length) return "Search looks through everything, whatever is on here.";
  if (!mask) return "Nothing is on, so no messages show. Turn one on.";
  const on = SHOW_PARTS.filter(([b]) => mask & b).map(x => x[2]), last = on.pop();
  return (on.length ? on.join(", ") + " and " + last : last).replace(/^./, c => c.toUpperCase()) + (mask === SHOW.ALL ? "." : ". The rest runs in the background.");
}

let opened = "";   // the thread the beacon last went out for; leaving the page forgets it
export default {
  id: "comms", label: () => (app() ? "Chat" : "Comms"),
  title: a => (a === "~list" ? (app() ? "Conversations" : "Threads") : a === "~new" ? (app() ? "New conversation" : "New thread") : null),   // the tab's title (app.js where): a thread is named by its h1
  tab: true,
  badge: () => { if (!app() || !S.live) return null; if (!simpleOn()) { loadFoci(); loadNeedsYou(); } if (!needsKnown()) return null; const n = needsN(); return n ? String(n) : null; },
  poll: { every: 3000, run: async ctx => { const [a, b] = await Promise.all([pollDelivered(), pollCard()]); if (a || b) ctx.update(); } },
  escape: () => { if (closeQuick()) return; if (C.menu || C.find) { C.menu = null; if (!C.q.trim()) C.find = false; L.ctx && L.ctx.update(); return; } if (C.more) { C.more = null; L.ctx && L.ctx.update(); return; } cancelReply(); },
  // The home is where the focus cards are: arriving on it is the person's look at them (core/foci.js markSeen; never from a driven browser).
  enter: ctx => { C.arrived = true; const c = conversation(ctx && ctx.arg); if (app() && c.home && !c.list && !c.archived) lookAt(null, true); },
  leave: () => { clearTimeout(qTimer); C.menu = null; C.more = null; C.find = false; L.newTh = undefined; opened = ""; seenLeave(); },
  render(ctx) {
    if (!S.live) return h("div", { class: "page" }, S.failed ? empty("Couldn't read the conversations. It keeps trying.", { kind: "error" }) : empty("Reading…", { kind: "loading" }));
    const cv = conversation(ctx.arg);
    if (cv.thread && cv.thread !== opened) { opened = cv.thread; apiPost("/api/opened", { thread: cv.thread }).catch(() => {}); }   // R107: the open beacon, once per thread open, no UI, no retry
    return frame(ctx, cv);
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
  const pane = h("div", { class: "cm-fpane", "data-key": "place:" + place },
    h("a", { class: "btn cm-back", href: href("comms"), "data-kind": "icon", "aria-label": "Chats", title: "Chats" }, icon("chevron-left")),
    page);
  return frame(ctx, { arg: "place:" + place, place }, pane);
}
/** A page that belongs to Chat (Yours, Inbox, Mail, Needs you) keeps the rail beside it, as Files does, so the content doesn't jump and no blank column is left under New conversation. A function so a view can use it while this module is still loading. */
export function chatPlace(place, view) {
  const { render, leave, escape } = view;
  return { ...view,
    render: ctx => { const p = render(ctx); if (S.live) return S.app.app ? inChatPlace(ctx, place, p) : p;
      // Before the data lands: S.app.app is already the look this browser last saw (store.js), so draw the frame's empty rail and the page doesn't start in the wide column and slide right when the rail arrives.
      return S.app.app ? h("div", { class: "cm", "data-key": "comms", "data-in": "", style: `--cm-w:${sideW}px` }, h("nav", { class: "cm-side", "aria-hidden": "true" }), h("section", { class: "cm-convo", "aria-label": "Conversation" }, h("div", { class: "cm-fpane", "data-key": "place:" + place }, p))) : p; },
    leave: ctx => { chatLeave(); return leave && leave(ctx); },
    escape: e => { chatEscape(); return escape && escape(e); } };
}
// Functions, not consts: focus.js reads them while this module, which imports it, is still loading.
export function chatEscape() { if (closeQuick()) return; if (C.menu || C.more) { C.menu = null; C.more = null; L.ctx && L.ctx.update(); } }
export function chatLeave() { clearTimeout(qTimer); C.menu = null; C.more = null; C.find = false; L.newTh = undefined; seenLeave(); }

/** The layout: the rail, and in the other pane the home, a conversation, or what pane brings (a focus). */
function frame(ctx, c, pane) {
    L.ctx = ctx;
    if (C.conv !== c.arg) { Object.assign(C, { conv: c.arg, q: C.next || "", next: null, limit: FIRST, menu: null, find: false }); }
    const here = c.focus != null ? "f:" + c.focus : null;
    if (here && here !== L.here && FOLD.delete(here)) saveFold();   // opening a focus page opens its section in the rail; its heading folds it again
    L.here = here; L.place = c.place || null;
    L.opts = { showThread: !c.thread, mark: words(C.q) };
    refresh(c);
    queueMicrotask(() => { sync(c, ctx); scSync(); });
    if (c.thread && app()) { const un = unreadIn(c.thread), sig = c.thread + un.map(x => x.id).join(); if (un.length && sig !== L.marked && !document.hidden) { L.marked = sig; markRead({ ids: un.map(x => x.id) }); } }
    const post = S.app.app && can("post");
    const convo = pane || (c.archived ? archivedPane(c, ctx) : c.home ? homePane(ctx)
      : [head(c, ctx),
        // Keyed by conversation: another one is a new element, so it opens at its end. Wrapped so that swap doesn't move the composer, which would drop focus.
        h("div", { class: "cm-mw", "data-key": "mw" },
          h("div", { class: "cm-msgs", "data-key": "m:" + (c.arg || ""), "data-own": "", "data-sig": "own", role: "log", "aria-live": "off", "aria-label": "Messages", tabindex: "0" }),
          h("div", { class: "sc-layer", "data-key": "sc", "data-own": "", "data-sig": "sc" })),   // side chats' marks over the messages (ui/sidechat.js)
        c.thread && app() && !(post && c.thread) ? strip(c.thread) : null,
        c.thread && app() ? openCard(c.thread, { jump: jumpTo, answer: p => replyTo(p), update: ctx.update, atEnd: !L.box || L.box.dataset.key !== "m:" + (c.arg || "") || L.stuck }) : null,   // what's open for the person, so they needn't read back (110401090fd2)
        c.thread && !can("post") ? readOnly(c.thread) : null,   // §5b: where this address can't post, say so in the composer's place
        post && c.thread ? composer({ thread: c.thread, update: ctx.update, top: app() ? strip(c.thread) : null }) : null,
        post && c.all ? h("p", { class: "cm-hint meta", "data-key": "hint" }, "To start something new, use New conversation.") : null]);
    return h("div", { class: "cm", "data-key": "comms", "data-in": c.arg ? "" : null, "data-home": c.home && !c.list ? "" : null, "data-list": c.list ? "" : null, "data-side": sideHid && c.arg ? "hidden" : null, style: `--cm-w:${sideW}px` },
      h("nav", { class: "cm-side", "aria-label": app() ? "Conversations" : "Threads" },
        h("div", { class: "cm-filters" },
          // R14: the phone's way to the rail. Home has one row that opens it as a page; that page has a way back (css/phone.css shows these only there).
          app() && c.home && !c.list ? h("a", { class: "cm-listbtn only-phone", href: href("comms", "~list"), "data-key": "listbtn" }, icon("search"), h("span", null, "Conversations"), h("span", { class: "meta" }, "search, filter, by focus"), icon("chevron-right")) : null,
          app() && c.list ? h("div", { class: "cm-lhead only-phone", "data-key": "lhead" }, h("a", { class: "btn cm-back", href: href("comms"), "data-kind": "icon", "aria-label": "Chats", title: "Chats" }, icon("chevron-left")), h("h1", null, "Conversations")) : null,
          // New conversation: in the top bar's Chat buttons on a computer (places, above); here in simple view, and on the phone's list page, where the top bar has no room
          post ? button("New conversation", null, { href: href("comms", "~new"), kind: "quiet", icon: "new", cls: "cm-newbtn" + (app() && !simpleOn() ? " only-phone" : "") }) : null,
          h("div", { class: "cm-srow" },
            h("div", { class: "cm-search" }, icon("search"),
              input({ type: "search", placeholder: app() ? "Search, or @ to ask" : "Search threads", "aria-label": "Search all conversations by name or content. Start with @ to ask an agent, which answers here and brings up the conversations that help", value: C.tq, oninput: e => { C.tq = e.target.value; ctx.update(); }, onkeydown: e => { if (e.key === "Enter" && aiQ()) { e.preventDefault(); aiAsk(ctx); return; } if (e.key === "Escape") { e.stopPropagation(); C.tq = ""; e.target.value = ""; e.target.blur(); ctx.update(); } } })),
            app() && !simpleOn() ? importantStar(ctx) : null,
),
          app() && c.list && !simpleOn() ? viewBar(ctx) : null,
          simpleOn() ? null : showMenu(ctx),   // always open (the person, 110819287bb8: "always show the filters"), no button to open it
          app() ? brought() : null,
          C.err ? h("p", { class: "fmsg", "data-tone": "bad", role: "alert" }, C.err, " ", button("Close", () => { C.err = ""; ctx.update(); }, { size: "s", kind: "quiet" })) : null),
        h("div", { class: "cm-tl", "data-own": "", "data-sig": "own", tabindex: "-1" }),
        grip()),   // last, so Tab reaches it after the list it sits on the right edge of (WCAG 2.4.3)
      h("section", { class: "cm-convo", "aria-label": "Conversation", ...(post && c.thread ? dropZone(c.thread) : {}) },
        convo,
        !c.thread ? quickPanel(ctx.update) : null,
        post && c.thread ? h("div", { class: "cm-drop", "aria-hidden": "true", "data-key": "drop" }, "Drop files to attach them") : null));
}

/** The read-only footer: a lock, one sentence, and (in the developer look, which has no strip) the thread's live agents inline, no actions. */
function readOnly(th) {
  const run = app() ? [] : (S.live ? S.live.agents : []).filter(a => a.thread === th && a.alive);
  return h("div", { class: "cm-ro", "data-key": "ro" }, icon("lock"), h("span", null, "Read-only here. This address can't post."),
    run.length ? h("span", { class: "cm-ro-a" }, run.map(a => { const d = a.doing && app() ? (ownStep(a.doing) ? "" : plainStep(a.doing)) : a.doing; return h("span", { class: "cm-ro-l", "data-key": a.name, title: d || null }, statusDot(agentState(a).key), agentRef(a.name), d ? h("span", { class: "meta" }, short(d, 48)) : null); })) : null);
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
