// Comms: every thread and message. A list of threads, one conversation, the composer.
// #comms everything · #comms/<thread> · #comms/@name by or to one name · #comms/@a+b between two names.
//
// The thread list and the message list are data-own: morph leaves them alone and sync() below writes them, once
// the render has been mounted. A poll with nothing new touches nothing in them; a new post is appended, not redrawn.
import { h, morph } from "../core/dom.js";
import { S, can, SYS, OUTSIDE } from "../core/store.js";
import { agentStatus, isRunning, tierOf } from "../core/model.js";
import { ago, dayName, stamp, short, mdPlain, now, plural, DAY } from "../core/format.js";
import { href, threadHref } from "../core/router.js";
import { tier } from "../core/status.js";
import { askTier } from "../actions.js";
import { button, empty, input, list, seg, statusDot } from "../ui/kit.js";
import { agentRef, threadRef } from "../ui/refs.js";
import { message, messageHead, markWords, pollDelivered, marksSig } from "../ui/message.js";
import { composer, cancelReply } from "../ui/composer.js";

const STEP = 500, FIRST = 250, NEAR = 60;   // posts shown first; "Show older" adds STEP; how close to the end counts as at the end
const NONE = [];

// What the person chose. C.all: "Everything" is open on a phone, where the list and the conversation take turns.
// C.next: the message search a thread opened from the search box starts with.
const C = { conv: null, q: "", sys: false, limit: FIRST, tq: "", scope: "active", all: false, next: null };
// What the message list holds. matched grows as posts arrive; pend is what sync() still has to append.
const L = { key: null, src: null, seen: 0, matched: [], pend: [], rebuild: true, keep: false, shown: 0, day: "", names: new Set(), opts: null, box: null, ro: null, stuck: true, heads: "", near: new Set(), io: null, tl: null, tlSig: "", tlConv: null, saved: null };
let qTimer = 0;

/** What the address says to show. */
function conversation(arg) {
  if (!arg) return { arg: null };
  if (arg[0] !== "@") return { arg, thread: arg };
  const names = arg.slice(1).split("+");
  return names.length === 2 ? { arg, pair: names } : { arg, agent: names[0] };
}

const words = s => s.toLowerCase().split(/\s+/).filter(Boolean);
/** What a search looks through in a post: its text, author and attachment names. */
const hay = p => (p.lc ??= [p.text, p.from, ...(p.files || []).map(f => f.name)].join(" ").toLowerCase());

function matcher(c) {
  const q = words(C.q);
  return p => {
    if (c.agent && p.from !== c.agent && !p.men.includes(c.agent)) return false;
    if (c.pair && !((p.from === c.pair[0] && p.men.includes(c.pair[1])) || (p.from === c.pair[1] && p.men.includes(c.pair[0])))) return false;
    if (!C.sys && SYS.has(p.from) && !c.thread) return false;
    // Across threads a thread's name counts too; inside one it would match every post.
    if (q.length && !q.every(w => hay(p).includes(w) || (!c.thread && p.th.toLowerCase().includes(w)))) return false;
    return true;
  };
}

/** Bring the matches up to date, looking only at posts not yet seen unless the conversation or filter changed. */
function refresh(c) {
  const src = c.thread ? (S.threads.get(c.thread) || { posts: NONE }).posts : S.posts, key = [c.arg, C.q, C.sys].join("\0"), match = matcher(c);
  if (key !== L.key || src !== L.src) {
    Object.assign(L, { key, src, matched: src.filter(match), seen: src.length, pend: [], rebuild: true, keep: false });
    return;
  }
  for (let i = L.seen; i < src.length; i++) if (match(src[i])) { L.matched.push(src[i]); L.pend.push(src[i]); }
  L.seen = src.length;
}

// ---- the message list ----
const atEnd = b => b.scrollTop + b.clientHeight > b.scrollHeight - NEAR;

function rows(posts) {
  const out = [];
  for (const p of posts) {
    const d = dayName(p.t);
    if (d !== L.day) { L.day = d; out.push(h("div", { class: "cm-day" }, d)); }
    L.names.add(p.from); for (const m of p.men) L.names.add(m);
    const a = message(p, L.opts);
    a._head = L.heads; L.io.observe(a); out.push(a);
  }
  return out;
}

function paint(box, ctx) {
  const shown = L.matched.slice(-C.limit), older = L.matched.length - shown.length;
  L.day = ""; L.names = new Set(); L.shown = shown.length; L.io.disconnect();
  box.replaceChildren(
    ...(older ? [h("div", { class: "cm-older" }, button(`Show older (${older} more)`, () => { C.limit += STEP; L.rebuild = true; L.keep = true; ctx.update(); }, { size: "s" }))] : []),
    ...(shown.length ? rows(shown) : [empty("No posts match.")]));
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
        L.saved = L.stuck ? null : { key: L.key, fromEnd: box.scrollHeight - box.scrollTop };
      }, { passive: true });
      // The box changes size when the composer grows, the keyboard opens or the window moves: stay at the end if that's where it was.
      L.ro = new ResizeObserver(() => { if (L.stuck) box.scrollTop = box.scrollHeight; });
      L.ro.observe(box);
      L.near = new Set();
      L.io = new IntersectionObserver(es => {
        for (const e of es) { if (e.isIntersecting) { L.near.add(e.target); refreshHead(e.target); } else L.near.delete(e.target); }
      }, { root: box, rootMargin: "800px 0px" });
    }
    if (L.rebuild) {
      const fromEnd = L.keep ? box.scrollHeight - box.scrollTop : isNew && L.saved && L.saved.key === L.key ? L.saved.fromEnd : null;
      paint(box, ctx);
      L.rebuild = false; L.keep = false;
      if (fromEnd != null) { box.scrollTop = box.scrollHeight - fromEnd; L.stuck = atEnd(box); }
      else { box.scrollTop = box.scrollHeight; L.stuck = true; }
    } else if (L.pend.length) {
      const stick = atEnd(box) || L.stuck;
      box.querySelector(":scope > .empty")?.remove();
      box.append(...rows(L.pend));
      L.shown += L.pend.length;
      if (stick) box.scrollTop = box.scrollHeight;
    }
    L.pend = [];
    refreshHeads();
  }
  syncThreads(c, ctx);
}

/** Names and read marks change with no new post. Only the head of a post is morphed, only near the viewport
 *  (at 1700 posts, all of them took half a second); the others catch up as they scroll into view. */
function refreshHeads() {
  let sig = S.ver + "|" + marksSig();
  for (const n of L.names) sig += "|" + agentStatus(n).key;
  if (sig === L.heads) return;
  L.heads = sig;
  for (const a of L.near) refreshHead(a);
}
function refreshHead(a) {
  if (a._head === L.heads) return;
  a._head = L.heads;
  const p = S.byId[a.getAttribute("data-key")];
  if (p) morph(a.firstElementChild, messageHead(p, L.opts));
}

// ---- the thread list ----
/** The part of a post around the first word found, so the word is still in view when the row cuts it off. */
function snippet(p, ws) {
  const text = mdPlain(p.text).replace(/\s+/g, " ").trim(), lc = text.toLowerCase();
  const at = Math.min(...ws.map(w => lc.indexOf(w)).filter(i => i >= 0));
  if (at === Infinity) return short(text, 70);
  const from = Math.max(0, at - 16);
  return markWords((from ? "…" : "") + text.slice(from, from + 90), ws);
}

/** Threads with a post holding every word (a word in the thread's own name counts for all its posts). Looks through all of them, whatever the Last 24h / All choice. */
function searchRows(c, ctx, ws) {
  const open = (name, q) => () => { if (name === (c.arg ?? null)) C.q = q; else C.next = q; ctx.update(); };
  let total = 0, found = [];
  for (const d of S.threads.values()) {
    const lc = d.name.toLowerCase(), rest = ws.filter(w => !lc.includes(w));
    if (!rest.length) { total += d.posts.filter(p => C.sys || !SYS.has(p.from)).length; found.push({ d, hits: d.posts, rest }); continue; }
    const hits = d.posts.filter(p => rest.every(w => hay(p).includes(w)));
    if (hits.length) { total += hits.filter(p => C.sys || !SYS.has(p.from)).length; found.push({ d, hits, rest }); }
  }
  found.sort((a, b) => b.hits.at(-1).t - a.hits.at(-1).t);
  const all = C.tq.trim();
  return [
    { key: "(everything)", href: href("comms"), title: "Everything", text: `${plural(total, "match", "matches")} in ${plural(found.length, "thread")}`, current: !c.arg, onClick: () => { C.all = true; open(null, all)(); } },
    ...found.map(({ d, hits, rest }) => ({ key: d.name, href: threadHref(d.name), current: c.thread === d.name, title: d.name, meta: ago(hits.at(-1).t).replace(" ago", ""),
      lead: h("span", { class: "ref-sw", "data-tone": tier(tierOf(d.name)).tone, title: tier(tierOf(d.name)).label }),
      onClick: open(d.name, rest.join(" ")),
      text: rest.length ? [`${hits.length} · ${hits.at(-1).from}: `, snippet(hits.at(-1), rest)] : `${d.posts.length} · name matches` })),
  ];
}

function threadRows(c, ctx) {
  const ws = words(C.tq);
  if (ws.length) return searchRows(c, ctx, ws);
  const t = now(), ran = new Set(S.live ? S.live.agents.map(a => a.thread) : []);
  let ds = [...S.threads.values()];
  if (C.scope === "active") ds = ds.filter(d => d.last > t - DAY || ran.has(d.name) || d.name === c.thread);
  ds.sort((a, b) => b.last - a.last);
  return [
    { key: "(everything)", href: href("comms"), title: "Everything", text: `${S.posts.length} posts, newest last`, current: !c.arg, onClick: () => { C.all = true; ctx.update(); } },
    ...ds.map(d => {
      const lp = d.posts[d.posts.length - 1], live = ran.has(d.name), who = lp.from.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
      return { key: d.name, href: threadHref(d.name), current: c.thread === d.name, title: d.name, meta: ago(d.last).replace(" ago", ""),
        lead: [h("span", { class: "ref-sw", "data-tone": tier(tierOf(d.name)).tone, title: tier(tierOf(d.name)).label }), live ? h("span", { title: "an agent is on this thread now" }, statusDot("working", { live: true })) : null],
        text: `${d.posts.length} · ${lp.from}: ${short(mdPlain(lp.text).replace(new RegExp("^(@\\S+\\s+)*" + who + ":\\s*"), ""), 60)}` };
    }),
  ];
}
function syncThreads(c, ctx) {
  const tl = document.querySelector("#main .cm-tl"); if (!tl) return;
  const sig = [S.ver, C.scope, C.tq, C.sys, c.arg, Math.floor(now() / 3e4), S.runSig].join("|");
  if (tl === L.tl && sig === L.tlSig) return;
  const fresh = tl !== L.tl || c.arg !== L.tlConv;
  L.tl = tl; L.tlSig = sig; L.tlConv = c.arg;
  const tree = list(threadRows(c, ctx), { dense: true });
  if (tl.firstChild) morph(tl.firstChild, tree); else tl.append(tree);
  if (fresh) tl.querySelector("[aria-current]")?.scrollIntoView({ block: "nearest" });
}

// ---- the page ----
function members(th) {
  const d = S.threads.get(th), m = new Set(d ? d.who : []);
  for (const s of S.sessions) if (s.thread === th) m.add(s.name);
  for (const a of (S.live ? S.live.agents : [])) if (a.thread === th && a.name) m.add(a.name);
  for (const p of (S.live ? S.live.pending : [])) if ((p.threads || []).includes(th)) m.add(p.name);
  const rank = n => (OUTSIDE.has(n) ? 0 : isRunning(n) ? 1 : S.pending[n] ? 2 : 3);
  return [...m].sort((a, b) => rank(a) - rank(b));
}

function tierControl(th) {
  const t = tier(tierOf(th)), sw = h("span", { class: "ref-sw", "data-tone": t.tone });
  return can("tier") ? button([sw, t.label + " ▾"], () => askTier(th), { size: "s", title: "Change this thread's tier" })
    : h("span", { class: "meta cm-tier", title: "This thread's tier" }, sw, t.label);
}

function head(c, ctx) {
  const d = c.thread && S.threads.get(c.thread), n = L.matched.length;
  const title = c.thread || (c.agent ? "Everything by or to " + c.agent : c.pair ? `${c.pair[0]} and ${c.pair[1]}` : "Everything");
  const who = c.thread ? members(c.thread).map(m => agentRef(m, { copy: true, state: true }))
    : c.agent ? [...new Set(S.posts.filter(p => p.from === c.agent).map(p => p.th))].slice(-14).map(t => threadRef(t)) : [];
  return h("header", { class: "cm-head", "data-key": "head" },
    h("div", { class: "cm-tt" },
      h("a", { class: "btn cm-back", href: href("comms"), "data-kind": "quiet", "data-size": "s", onclick: () => { C.all = false; ctx.update(); } }, "‹ Threads"),
      h("h1", { class: "cm-title" }, title),
      h("span", { class: "meta" }, (C.q.trim() ? plural(n, "match", "matches") : plural(n, "post")) + (d ? ` · since ${stamp(d.first)}` : "")),
      c.thread ? tierControl(c.thread) : null,
      h("div", { class: "cm-tools" },
        c.thread ? null : h("label", { class: "cm-sys meta" }, h("input", { type: "checkbox", checked: C.sys, onchange: e => { C.sys = e.target.checked; ctx.update(); } }), "starter notices"),
        input({ type: "search", placeholder: "Search messages", "aria-label": "Search messages", value: C.q, oninput: e => { C.q = e.target.value; clearTimeout(qTimer); qTimer = setTimeout(ctx.update, 120); } }))),
    who.length ? h("div", { class: "cm-people" }, who) : null);
}

export default {
  id: "comms", label: "Comms", tab: true,
  poll: { every: 3000, run: async ctx => { if (await pollDelivered()) ctx.update(); } },
  escape: () => cancelReply(),
  leave: () => clearTimeout(qTimer),
  render(ctx) {
    if (!S.live) return h("div", { class: "page" }, empty("Reading…", { kind: "loading" }));
    const c = conversation(ctx.arg);
    if (C.conv !== c.arg) { Object.assign(C, { conv: c.arg, q: C.next || "", next: null, limit: FIRST }); if (c.arg) C.all = false; }
    L.opts = { showThread: !c.thread, mark: words(C.q) };
    refresh(c);
    queueMicrotask(() => sync(c, ctx));
    const top = document.getElementById("top");
    return h("div", { class: "cm", "data-key": "comms", "data-in": c.arg || C.all ? "" : null, style: `--top-h:${top ? top.offsetHeight : 47}px` },
      h("nav", { class: "cm-side", "aria-label": "Threads" },
        h("div", { class: "cm-filters" },
          input({ type: "search", placeholder: "Search all threads", "aria-label": "Search all threads by name or content", value: C.tq, oninput: e => { C.tq = e.target.value; ctx.update(); } }),
          words(C.tq).length ? null : seg([["active", "Last 24h"], ["all", "All"]], C.scope, k => { C.scope = k; ctx.update(); }, { label: "Which threads" })),
        h("div", { class: "cm-tl", "data-own": "", "data-sig": "own", tabindex: "-1" })),
      h("section", { class: "cm-convo", "aria-label": "Conversation" },
        head(c, ctx),
        // Keyed by conversation: another one is a new element, so it opens at its end. Wrapped so that swap doesn't move the composer, which would drop focus.
        h("div", { class: "cm-mw", "data-key": "mw" },
          h("div", { class: "cm-msgs", "data-key": "m:" + (c.arg || ""), "data-own": "", "data-sig": "own", role: "log", "aria-live": "off", "aria-label": "Messages", tabindex: "0" })),
        S.app.app && can("post") && !c.agent && !c.pair ? composer({ thread: c.thread || null, update: ctx.update }) : null));
  },
};
