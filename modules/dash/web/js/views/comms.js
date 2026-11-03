// Comms (Chat, in the app): conversations on the left, one conversation on the right, the composer under it.
// #comms              in the app: home — start something, and what needs you. On the developer route: everything.
// #comms/~new         the same home, open on a phone (where the list and a conversation take turns)
// #comms/~all         every message · #comms/<thread> · #comms/@name by or to one name · #comms/@a+b between two names
//
// The thread list and the message list are data-own: morph leaves them alone and sync() below writes them, once the
// render has been mounted. A poll with nothing new touches nothing in them; a new post is appended, not redrawn, before
// the messages still on their way (.cm-out), which sync() keeps apart.
import { h, morph } from "../core/dom.js";
import { S, can, OUTSIDE, built, post as apiPost, poll } from "../core/store.js";
import { agentState, agentStatus, isAgent, isRunning, tierOf, threadTitle, threadState, threadPeople, isSystemThread, SHOW, postLevel, threadLevel, closingPosts, displayText, postSource, inUse, fresh7 } from "../core/model.js";
import { ago, dayName, stamp, clock, dur, short, mdPlain, now, plural, firstSentence, DAY, MIN, HOUR } from "../core/format.js";
import { href, threadHref, go } from "../core/router.js";
import { tier, status } from "../core/status.js";
import { askTier, askStop } from "../actions.js";
import { seen, unreadIn, markRead, onInbox } from "../core/inbox.js";
import { button, empty, input, list, seg, statusDot } from "../ui/kit.js";
import { agentRef, copyButton, threadRef } from "../ui/refs.js";
import { message, messageHead, messageReceipt, localMessage, markWords, pollDelivered, marksSig, delivery, onJump } from "../ui/message.js";
import { composer, cancelReply, replyTo, say, outFor, outSig, actions as outActions, dropZone, recipient } from "../ui/composer.js";

const STEP = 500, FIRST = 250, NEAR = 60, GROUP = 10 * MIN, HELPER_AFTER = 3 * MIN;
const NONE = [];
const app = () => S.app.app;

// What the person chose. C.next: the message search a thread opened from the search box starts with.
const C = { conv: undefined, q: "", show: null, limit: FIRST, tq: "", scope: "recent", next: null, more: false };
// What the message list holds. matched grows as posts arrive; pend is what sync() still has to append.
const L = { key: null, src: null, seen: 0, matched: [], pend: [], evs: new Set(), rebuild: true, keep: false, shown: 0, day: "", prev: null, names: new Set(), opts: null,
  box: null, out: null, jump: null, unseen: 0, outSig: "", ro: null, stuck: true, heads: "", near: new Set(), io: null, tl: null, tlSig: "", tlConv: null, saved: null, ctx: null, jumpTo: null, marked: "" };
let qTimer = 0;
onInbox(() => L.ctx && L.ctx.update());

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

// How much to show: For you, Updates or Everything. Kept in this browser. Searching ignores it, so a hidden message can still be found.
const SHOW_KEY = "hive.comms.show";
function showing() {
  if (!C.show) { try { C.show = localStorage.getItem(SHOW_KEY); } catch (e) { /* no storage: the default, for this page */ } }
  return SHOW[C.show] !== undefined ? C.show : app() ? "updates" : "all";
}
function setShowing(k, ctx) { C.show = k; try { localStorage.setItem(SHOW_KEY, k); } catch (e) { /* no storage: this page only */ } ctx.update(); }
const showLevel = () => SHOW[showing()];
/** The level this conversation's messages are cut to. By or to a name you asked for by name, and a search, show everything. */
const levelFor = c => ((c.thread || c.all) && !words(C.q).length ? showLevel() : SHOW.all);
const closing = { n: -1, set: null };
const endedRuns = () => { let n = 0; for (const s of S.sessions) if (s.end) n++; return n; };
function closers(n) { const k = n + ":" + S.posts.length; if (closing.n !== k) { closing.n = k; closing.set = closingPosts(); } return closing.set; }   // the run's end and its last post can reach us in either order

function matcher(c, lv, ended) {
  const q = words(C.q), last = lv === SHOW.you ? closers(ended) : null;
  return p => {
    if (lv < SHOW.all && postLevel(p) > lv && !(last && last.has(p.id))) return false;
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
    if (S.limitedSess.has(s.session)) out.push({ ev: true, key: "ev:" + s.session, t: s.end, tone: "limited", text: `${s.name} stopped at a usage limit`, sub: "It carries on by itself once there's room." });
    else if (s.exit && s.exit !== 143) out.push({ ev: true, key: "ev:" + s.session, t: s.end, tone: "failed", text: `${s.name} stopped with an error (exit ${s.exit})`, sub: "Its last message above is where it got to." });
  }
  return out;
}

/** Bring the matches up to date, looking only at posts not yet seen unless the conversation or filter changed. */
function refresh(c) {
  const src = c.thread ? (S.threads.get(c.thread) || { posts: NONE }).posts : c.home ? NONE : S.posts;
  // At For you, a run ending can promote a post already in the list, so the list is redone then. That must not move someone who has scrolled up.
  const lv = levelFor(c), ended = lv === SHOW.you ? endedRuns() : 0, base = [c.arg, C.q, lv].join("\0"), key = base + "\0" + ended, match = matcher(c, lv, ended);
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
const atEnd = b => b.scrollTop + b.clientHeight > b.scrollHeight - NEAR;
const sameTo = (a, b) => a.men.join() === b.men.join();
const sameSource = (a, b) => { const x = postSource(a), y = postSource(b); return (x && x.name + x.how) === (y && y.name + y.how); };

function evRow(e) {
  return h("div", { class: "cm-ev", "data-key": e.key, "data-tone": status(e.tone).tone, role: "note" }, statusDot(e.tone), h("b", null, e.text), h("span", { class: "meta" }, clock(e.t) + " · " + e.sub));
}
function rows(items) {
  const out = [];
  for (const p of items) {
    const d = dayName(p.t);
    if (d !== L.day) { L.day = d; L.prev = null; out.push(h("div", { class: "cm-day", "data-key": "d:" + d }, h("span", null, d))); }
    if (p.ev) { out.push(evRow(p)); L.prev = null; continue; }
    L.names.add(p.from); for (const m of p.men) L.names.add(m);
    const q = L.prev, cont = app() && !!q && q.from === p.from && q.th === p.th && !p.reply_to && p.t - q.t < GROUP && sameTo(q, p) && sameSource(q, p);
    const a = message(p, { ...L.opts, cont });
    L.prev = p; a._head = L.heads; L.io.observe(a); out.push(a);
  }
  return out;
}

function paint(box, ctx) {
  const shown = L.all.slice(-C.limit), older = L.all.length - shown.length;
  L.day = ""; L.prev = null; L.names = new Set(); L.shown = shown.length; L.io.disconnect(); L.outSig = "";
  L.out = h("div", { class: "cm-out", "data-key": "out" });
  L.jump = h("button", { type: "button", class: "cm-jump", hidden: true, onclick: () => { box.scrollTop = box.scrollHeight; } });
  box.replaceChildren(
    ...(older ? [h("div", { class: "cm-older" }, button(`Show older (${older} more)`, () => { C.limit += STEP; L.rebuild = true; L.keep = true; ctx.update(); }, { size: "s" }))] : []),
    ...(shown.length ? rows(shown) : [empty(C.q ? "No messages match." : L.src.length ? "Nothing here at this setting. Choose Everything to see all the messages." : "No messages yet.")]),
    L.out, L.jump);
}
function setJump() {
  if (!L.jump) return;
  L.jump.hidden = !L.unseen;
  L.jump.textContent = L.unseen ? `${plural(L.unseen, "new message")} ↓` : "";
}
function flash(box, el) {
  box.scrollTop = Math.max(0, el.offsetTop - box.clientHeight / 3);
  L.stuck = atEnd(box);
  el.setAttribute("data-flash", ""); setTimeout(() => el.removeAttribute("data-flash"), 1800);
}
onJump(id => {
  const p = S.byId[id], box = L.box; if (!p || !box) return;
  if (L.opts && conversation(L.ctx && L.ctx.arg).thread && p.th !== conversation(L.ctx.arg).thread) { L.jumpTo = id; return go("comms", p.th); }
  const el = box.querySelector(`[data-key="${CSS.escape(id)}"]`);
  if (el) return flash(box, el);
  const i = L.all.indexOf(p);
  if (i < 0) { if (showing() === "all" || !L.src.includes(p)) return; L.jumpTo = id; return setShowing("all", L.ctx); }   // a reply quoting a message this setting hides
  C.limit = L.all.length - i + 20; L.rebuild = true; L.jumpTo = id; L.ctx.update();
});

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
    } else if (L.pend.length) {
      const stick = atEnd(box) || L.stuck;
      box.querySelector(":scope > .empty")?.remove();
      L.out.before(...rows(L.pend));
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
const authorName = n => (n === "person" ? "You" : n);
const tierLead = th => (app() ? null : h("span", { class: "ref-sw", "data-tone": tier(tierOf(th)).tone, title: tier(tierOf(th)).label }));

/** One thread's state in a few words, for the list: shape, colour and the words. */
const STATE_WORDS = { asks: () => "Waiting for your answer", working: s => `${s.who} working`, quiet: s => `${s.who} quiet`, stuck: () => "Stuck", queue: s => `${s.who} in the job queue`,
  queued: () => "Waiting to start", limited: () => "Stopped at a usage limit", failed: () => "Stopped with an error", foryou: () => "New reply for you", done: () => null };
function stateTag(st) {
  const w = (STATE_WORDS[st.key] || (() => null))(st);
  return w ? h("span", { class: "cm-st", "data-tone": status(st.key).tone }, w) : null;
}

/** Threads with a post holding every word (a word in the thread's own name counts for all its posts). Looks through all of them. */
function searchRows(c, ctx, ws) {
  const open = (name, q) => () => { if (name === (c.thread ?? null)) C.q = q; else C.next = q; ctx.update(); };
  let total = 0; const found = [];
  for (const d of S.threads.values()) {
    const lc = (d.name + " " + threadTitle(d.name)).toLowerCase(), rest = ws.filter(w => !lc.includes(w));
    if (!rest.length) { total += d.posts.length; found.push({ d, hits: d.posts, rest }); continue; }
    const hits = d.posts.filter(p => rest.every(w => hay(p).includes(w)));
    if (hits.length) { total += hits.length; found.push({ d, hits, rest }); }
  }
  found.sort((a, b) => b.hits.at(-1).t - a.hits.at(-1).t);
  const all = C.tq.trim();
  return [
    { key: "(everything)", href: href("comms", "~all"), title: "All messages", text: `${plural(total, "match", "matches")} in ${plural(found.length, app() ? "conversation" : "thread")}`, current: c.all, onClick: () => { if (c.all) C.q = all; else C.next = all; ctx.update(); } },
    ...found.map(({ d, hits, rest }) => ({ key: d.name, href: threadHref(d.name), current: c.thread === d.name, title: threadTitle(d.name), meta: ago(hits.at(-1).t).replace(" ago", ""),
      lead: tierLead(d.name), onClick: open(d.name, rest.join(" ")),
      text: rest.length ? [`${hits.length} · ${authorName(hits.at(-1).from)}: `, snippet(hits.at(-1), rest)] : `${d.posts.length} · name matches` })),
  ];
}

function threadRows(c, ctx) {
  const ws = words(C.tq);
  if (ws.length) return searchRows(c, ctx, ws);
  const t = now(), lv = showLevel(), ran = new Set(S.live ? S.live.agents.map(a => a.thread) : []);
  let ds = [...S.threads.values()].filter(d => threadLevel(d) <= lv || d.name === c.thread);
  if (C.scope === "recent") ds = ds.filter(d => d.last > t - (app() ? 7 : 1) * DAY || ran.has(d.name) || d.name === c.thread || (app() && threadState(d.name, seen).key === "asks"));
  ds.sort((a, b) => b.last - a.last);
  const rowsOut = ds.map(d => {
    const lp = d.posts[d.posts.length - 1], live = ran.has(d.name);
    // The developer route keeps its one-line log row: 100+ threads have to fit.
    if (!app()) {
      const who = lp.from.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
      return { key: d.name, href: threadHref(d.name), current: c.thread === d.name, title: threadTitle(d.name), meta: ago(d.last).replace(" ago", ""),
        lead: [tierLead(d.name), live ? h("span", { title: "an agent is on this thread now" }, statusDot("working", { live: true })) : null],
        text: `${d.posts.length} · ${lp.from}: ${short(mdPlain(lp.text).replace(new RegExp("^(@\\S+\\s+)*" + who + ":\\s*"), ""), 60)}` };
    }
    const st = threadState(d.name, seen);
    const say = short(mdPlain(displayText(lp)).replace(/\s+/g, " "), 70);
    return { key: d.name, href: threadHref(d.name), current: c.thread === d.name, title: threadTitle(d.name), meta: ago(d.last).replace(" ago", ""),
      lead: [tierLead(d.name), h("span", { title: status(st.key).label || "" }, statusDot(st.key === "done" ? "ended" : st.key, { live }))],
      text: [stateTag(st), stateTag(st) ? " · " : null, `${authorName(lp.from)}: ${say}`] };
  });
  if (!app()) rowsOut.unshift({ key: "(everything)", href: href("comms", "~all"), title: "Everything", text: `${S.posts.length} posts, newest last`, current: c.all });
  return rowsOut;
}
function syncThreads(c, ctx) {
  const tl = document.querySelector("#main .cm-tl"); if (!tl) return;
  const sig = [S.ver, C.scope, C.tq, showing(), c.arg, Math.floor(now() / 3e4), S.runSig, marksSig().length].join("|");
  if (tl === L.tl && sig === L.tlSig) return;
  const fresh = tl !== L.tl || c.arg !== L.tlConv;
  L.tl = tl; L.tlSig = sig; L.tlConv = c.arg;
  const tree = list(threadRows(c, ctx), { dense: true, empty: S.threads.size ? "Nothing here at this setting. Choose Everything to see all of them." : app() ? "No conversations yet. Start one with New." : "No threads." });
  if (tl.firstChild) morph(tl.firstChild, tree); else tl.append(tree);
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
const who = n => agentRef(n, { dot: false });
function lastMine(th) { const d = S.threads.get(th); if (!d) return null; for (let i = d.posts.length - 1; i >= 0; i--) if (d.posts[i].from === "person") return d.posts[i]; return null; }
const notBuilt = () => h("span", { class: "nb", title: "Designed, not built yet: the server needs to do this" }, "not built yet");

function strip(th) {
  const st = threadState(th, seen), out = [], run = (S.live ? S.live.agents : []).filter(a => a.thread === th), post = can("post") && app();
  if (st.key === "asks") out.push(line("asks", [who(st.who), " is waiting for your answer: ", h("q", null, short(lastQuestion(st.post), 110))],
    post ? button("Answer", () => replyTo(st.post), { size: "s" }) : null, { key: "asks" }));
  if (st.key === "limited") {
    const room = hasRoom(), r = nextReset();
    out.push(line("limited", [who(st.who), ` stopped at a usage limit ${ago(st.t)} and hasn't carried on. `, room ? "There's room again now, so it should carry on by itself soon." : r ? `It carries on by itself after ${stamp(r)}.` : "It carries on by itself once there's room."],
      room && post ? button("Carry on", () => say(th, "Please carry on where you stopped.", st.who), { size: "s" }) : null, { key: "limited" }));
  }
  if (st.key === "failed") out.push(line("failed", [who(st.who), ` stopped with an error ${ago(st.t)}. A message wakes it to try again.`], null, { key: "failed" }));
  for (const a of run) {
    if (st.key === "asks" && a.name === st.who) continue;
    const s = agentState(a);
    out.push(line(s.key, [who(a.name), " ", AGENT_WORDS[s.key] || s.text, a.doing ? ": " + a.doing : "", h("span", { class: "meta" }, " · " + (s.key === "working" ? "active " + ago(a.last * 1e3) : s.text))],
      can("stop") ? button("Stop…", () => askStop(a.name), { size: "s", kind: "quiet", title: `Stop ${a.name}` }) : null, { key: "run:" + a.name, live: true }));
  }
  // A second helper when the agent you wrote to is busy (not built): see PART1.md.
  const mine = lastMine(th), wait = mine && now() - mine.t > HELPER_AFTER && now() - mine.t < 2 * HOUR ? mine.men.map(m => delivery(mine, m)).find(x => x && x.key === "waiting") : null;
  if (wait && built("helper")) out.push(line("queue", [who(wait.who), ` is busy and hasn't seen your message for ${dur(now() - mine.t)}. If it still hasn't by ${clock(mine.t + 10 * MIN)}, a helper starts on it. `, notBuilt()],
    post ? [button("Start a helper now", async (e, b) => { b.disabled = true; try { await apiPost("/api/helper", { post: mine.id }); poll(); } catch (x) { b.disabled = false; b.title = x.message; } }, { size: "s" })] : null, { key: "helper" }));
  if (!out.length && th) {
    const r = recipient(th);
    out.push(line("done", r.who && r.who !== "new" ? `Nobody is on this right now. A message wakes ${r.who}.` : "Nobody is on this right now. A message starts an agent on it.", null, { key: "idle" }));
  }
  return h("div", { class: "cm-strip", "data-key": "strip", role: "status", "aria-live": "polite" }, out);
}
function lastQuestion(p) {
  const lines = mdPlain(displayText(p)).split(/\n|(?<=[.!])\s+/).map(s => s.trim()).filter(Boolean);
  return [...lines].reverse().find(s => s.endsWith("?")) || lines.at(-1) || "";
}

// ---- the head of a conversation ----
function moreMenu(c, ctx) {
  const th = c.thread, t = tier(tierOf(th));
  return h("div", { class: "cm-menu", role: "menu", "data-key": "more" },
    can("tier") ? button([h("span", { class: "ref-sw", "data-tone": t.tone }), "Priority: " + t.label], () => { C.more = false; askTier(th); }, { size: "s", kind: "quiet", title: "Where this sits in the hive's priority list" }) : null,
    button("On the timeline", null, { href: href("timeline"), size: "s", kind: "quiet" }),
    th !== threadTitle(th) ? h("p", { class: "meta" }, "Its id on the board: ", h("code", null, th)) : null,
    h("div", { class: "cm-menu-find" }, input({ type: "search", class: "input", placeholder: "Search here", "aria-label": "Search these messages", value: C.q, oninput: e => { C.q = e.target.value; clearTimeout(qTimer); qTimer = setTimeout(ctx.update, 120); } })));
}
function head(c, ctx) {
  const d = c.thread && S.threads.get(c.thread), n = L.matched.length, off = levelFor(c) < SHOW.all ? L.src.length - n : 0;
  const title = c.thread ? threadTitle(c.thread) : c.agent ? (c.agent === "person" ? "Everything you sent" : "Everything by or to " + c.agent) : c.pair ? `${c.pair[0]} and ${c.pair[1]}` : app() ? "All messages" : "Everything";
  const who = c.thread ? threadPeople(c.thread).filter(m => m !== "person").map(m => agentRef(m, { copy: true, state: true }))
    : c.agent ? [...new Set(S.posts.filter(p => p.from === c.agent).map(p => p.th))].slice(-14).map(t => threadRef(t)) : [];
  return h("header", { class: "cm-head", "data-key": "head" },
    h("div", { class: "cm-tt", "data-dev": app() ? null : "" },
      h("a", { class: "btn cm-back", href: href("comms"), "data-kind": "quiet", "data-size": "s" }, app() ? "‹ Chats" : "‹ Threads"),
      h("h1", { class: "cm-title" }, title),
      h("span", { class: "meta cm-count" }, (C.q.trim() ? plural(n, "match", "matches") : plural(n, app() ? "message" : "post")) + (d ? ` · since ${stamp(d.first)}` : "")),
      c.thread && !app() && title !== c.thread ? h("span", { class: "cm-id" }, h("code", { class: "meta", title: "Its id on the board" }, c.thread), copyButton(c.thread)) : null,
      c.thread && !app() ? tierControl(c.thread) : null,
      h("div", { class: "cm-tools" },
        off > 0 ? button(`Show ${off} hidden`, () => setShowing("all", ctx), { size: "s", kind: "quiet", cls: "cm-hid", title: "Show everything: messages between agents and the hive's own notices" }) : null,
        input({ type: "search", class: "input cm-find", placeholder: c.thread ? "Search here" : "Search messages", "aria-label": "Search these messages", value: C.q, oninput: e => { C.q = e.target.value; clearTimeout(qTimer); qTimer = setTimeout(ctx.update, 120); } }),
        c.thread && app() ? button("More", () => { C.more = !C.more; ctx.update(); }, { size: "s", kind: "quiet", title: "Priority, timeline" }) : null)),
    C.more && c.thread && app() ? moreMenu(c, ctx) : null,
    who.length ? h("div", { class: "cm-people" }, who) : null);
}
function tierControl(th) {
  const t = tier(tierOf(th)), sw = h("span", { class: "ref-sw", "data-tone": t.tone });
  return can("tier") ? button([sw, t.label + " ▾"], () => askTier(th), { size: "s", title: "Change this thread's tier" })
    : h("span", { class: "meta cm-tier", title: "This thread's tier" }, sw, t.label);
}

// ---- home: start something, and what needs you ----
function needs() {
  const g = { asks: [], foryou: [], trouble: [], working: [] };
  for (const d of S.threads.values()) {
    if (isSystemThread(d)) continue;
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
  const lp = d.posts[d.posts.length - 1];
  const text = st.key === "asks" ? [h("b", null, st.who + " asks: "), lastQuestion(st.post)]
    : st.key === "foryou" ? [h("b", null, st.who + ": "), firstSentence(displayText(st.post))]
    : st.key === "limited" ? [h("b", null, `${st.who} stopped at a usage limit ${ago(st.t)}. `), "Where it got to: " + firstSentence(displayText(lp))]
    : st.key === "failed" ? [h("b", null, `${st.who} stopped with an error ${ago(st.t)}.`)]
    : [h("b", null, st.who + ": "), (S.running.get(st.who) || {}).doing || st.label];
  return { key: d.name, href: threadHref(d.name), title: threadTitle(d.name), meta: ago(st.post ? st.post.t : d.last), lead: statusDot(st.key, { live: isRunning(st.who) }), text };
}
function homePane(ctx) {
  const g = needs(), w = can("post"), n = g.asks.length + g.foryou.length + g.trouble.length;
  const local = outFor(null);
  const room = hasRoom();
  const sec = (title, note, items) => items.length ? h("section", { class: "sec", "data-key": "s:" + title }, h("header", { class: "sec-h" }, h("h2", { class: "sec-t" }, title), note ? h("span", { class: "sec-n" }, note) : null), list(items.map(needRow))) : null;
  return h("div", { class: "cm-home", "data-key": "home" },
    w ? h("div", { class: "cm-new", "data-key": "new" },
      h("h1", { class: "cm-home-t" }, "What do you want done?"),
      h("p", { class: "cm-home-l" }, "Say it the way you'd tell a person. An agent starts on it and answers here; it may bring in helpers if the job has parts."),
      !room ? h("p", { class: "cm-home-warn", "data-tone": "limited", role: "status" }, statusDot("limited"), "Every Claude account is past its usage line, so new work waits until there's room. See Usage.") : null,
      composer({ thread: null, update: ctx.update, place: "home", autofocus: conversation(ctx.arg).open }),
      local.length ? h("div", { class: "cm-out", "data-key": "out" }, local.map(it => localMessage(it, outActions))) : null) : null,
    h("div", { class: "cm-needs", "data-key": "needs" },
      h("h2", { class: "cm-needs-t" }, n ? `${plural(n, "thing")} for you` : "Nothing needs you"),
      sec("Waiting for your answer", "an agent asked you something", g.asks),
      sec("New for you", "answers you haven't opened", g.foryou),
      sec("Stopped", "one stopped by a usage limit carries on by itself; others need a message", g.trouble),
      sec("Working now", null, g.working),
      !n && !g.working.length ? h("p", { class: "meta" }, "When an agent asks you something, finishes, or stops, it shows here.") : null,
      h("p", { class: "cm-home-all" }, button("All messages", null, { href: href("comms", "~all"), size: "s", kind: "quiet" }), app() ? button("Inbox", null, { href: href("inbox"), size: "s", kind: "quiet" }) : null)));
}

export default {
  id: "comms", label: () => (app() ? "Chat" : "Comms"),
  tab: true,
  badge: () => { if (!app() || !S.live) return null; const g = needs(), n = g.asks.length + g.foryou.length + g.trouble.length; return n ? String(n) : null; },
  poll: { every: 3000, run: async ctx => { if (await pollDelivered()) ctx.update(); } },
  escape: () => { if (C.more) { C.more = false; L.ctx && L.ctx.update(); return; } cancelReply(); },
  leave: () => { clearTimeout(qTimer); C.more = false; },
  render(ctx) {
    L.ctx = ctx;
    if (!S.live) return h("div", { class: "page" }, empty("Reading…", { kind: "loading" }));
    const c = conversation(ctx.arg);
    if (C.conv !== c.arg) { Object.assign(C, { conv: c.arg, q: C.next || "", next: null, limit: FIRST, more: false }); }
    L.opts = { showThread: !c.thread, mark: words(C.q) };
    refresh(c);
    queueMicrotask(() => sync(c, ctx));
    if (c.thread && app()) { const un = unreadIn(c.thread), sig = c.thread + un.map(x => x.id).join(); if (un.length && sig !== L.marked && !document.hidden) { L.marked = sig; markRead({ ids: un.map(x => x.id) }); } }
    const top = document.getElementById("top"), post = S.app.app && can("post");
    const convo = c.home ? homePane(ctx)
      : [head(c, ctx),
        // Keyed by conversation: another one is a new element, so it opens at its end. Wrapped so that swap doesn't move the composer, which would drop focus.
        h("div", { class: "cm-mw", "data-key": "mw" },
          h("div", { class: "cm-msgs", "data-key": "m:" + (c.arg || ""), "data-own": "", "data-sig": "own", role: "log", "aria-live": "off", "aria-label": "Messages", tabindex: "0" })),
        c.thread && app() ? strip(c.thread) : null,
        post && c.thread ? composer({ thread: c.thread, update: ctx.update }) : null,
        post && c.all ? h("p", { class: "cm-hint meta", "data-key": "hint" }, "To start something new, use New conversation.") : null];
    return h("div", { class: "cm", "data-key": "comms", "data-in": c.arg ? "" : null, "data-home": c.home ? "" : null, style: `--top-h:${top ? top.offsetHeight : 47}px` },
      h("nav", { class: "cm-side", "aria-label": app() ? "Conversations" : "Threads" },
        h("div", { class: "cm-filters" },
          post ? button("New conversation", null, { href: href("comms", "~new"), kind: "primary", cls: "cm-newbtn" }) : null,
          input({ type: "search", placeholder: app() ? "Search all conversations" : "Search all threads", "aria-label": "Search all conversations by name or content", value: C.tq, oninput: e => { C.tq = e.target.value; ctx.update(); } }),
          words(C.tq).length ? null : [
            seg([["you", "For you", "Only what's for you: your messages, questions to you and each job's result. The rest carries on in the background"],
              ["updates", "Updates", "Also the agents' progress updates, without what they say to each other"],
              ["all", "Everything", "Every message, including agents talking to each other and the hive's own notices"]], showing(), k => setShowing(k, ctx), { label: "How much to show" }),
            seg([["recent", app() ? "Recent" : "Last 24h"], ["all", "All"]], C.scope, k => { C.scope = k; ctx.update(); }, { label: "Which conversations" })]),
        h("div", { class: "cm-tl", "data-own": "", "data-sig": "own", tabindex: "-1" }),
        app() ? h("div", { class: "cm-side-f" }, button("All messages", null, { href: href("comms", "~all"), size: "s", kind: "quiet" })) : null),
      h("section", { class: "cm-convo", "aria-label": "Conversation", ...(post && c.thread ? dropZone(c.thread) : {}) },
        convo,
        post && c.thread ? h("div", { class: "cm-drop", "aria-hidden": "true", "data-key": "drop" }, "Drop files to attach them") : null));
  },
};
