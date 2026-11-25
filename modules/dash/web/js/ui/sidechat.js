// Side chats (the person, 11251257161d): ask about one part of a conversation without leaving it. Highlight words, draw a box
// over part of a message, or right-click a message, and a side chat opens in the side panel (preview.js), the way a comment
// opens beside a document. Each one is a /btw side thread (dash.py BTW: a fresh agent that reads the main conversation, answers
// there and touches nothing), so the main conversation and its agent never see it. What was asked about stays marked in the
// chat: the words highlighted, a box drawn round them, and a numbered pin at the message's edge that opens that side chat.
//
// Where a mark sits is kept in this browser (localStorage "sc.anchors": the post, the offset and the words), and the first
// message of the side thread carries the quote too, so another browser still shows the pin and the quote, finding the words again.
import { h, tr, morph } from "../core/dom.js";
import { S, post, poll, subscribe, can } from "../core/store.js";
import { threadHref } from "../core/router.js";
import { displayText, chatText, btwText, btwParent, threadTitle } from "../core/model.js";
import { ago, short, clock } from "../core/format.js";
import { button, icon } from "./kit.js";
import { markdown } from "./markdown.js";
import { jump } from "./message.js";
import { openPreview, closePreview, previewKey, fullBtn } from "./preview.js";
import { textOf, selected, askQuote } from "../rich/ask.js";

const app = () => !!(S.app && S.app.app);
const hashThread = () => { const m = /^#comms\/([^/?]+)/.exec(location.hash); return m ? decodeURIComponent(m[1]) : null; };
/** The main conversation on screen, if side chats can be opened from it here. */
const here = () => { const th = hashThread(); return th && app() && can("post") && !btwParent(th) ? th : null; };

// ---- the side chats of a conversation, and what each is about ----
const firstT = th => { const d = S.threads.get(th); return (d && (d.first || (d.posts[0] && d.posts[0].t))) || 0; };
/** main's side chats, oldest first: side chat 1 is the first one asked. */
// dash.py cuts the parent's name in a side thread's name to 90 characters, so a longer one matches by its start.
const ofMain = (t, main) => { const p = btwParent(t); return p === main || (!!p && main.length > 90 && p === main.slice(0, 90)); };
export const sideChats = main => [...S.threads.keys()].filter(t => ofMain(t, main)).sort((a, b) => firstT(a) - firstT(b));

const KEY = "sc.anchors", KEEP = 400;
const AN = (() => { try { return JSON.parse(localStorage.getItem(KEY)) || {}; } catch (e) { return {}; } })();
function saveAnchors() {
  const ks = Object.keys(AN);
  if (ks.length > KEEP) ks.sort((a, b) => (AN[a].t || 0) - (AN[b].t || 0)).slice(0, ks.length - KEEP).forEach(k => delete AN[k]);
  try { localStorage.setItem(KEY, JSON.stringify(AN)); } catch (e) {}
}
// The first message of a side chat: the question, then a quote of what it is about (quoteBlock).
const HEAD_RE = /\n\n> (Highlighted in|Boxed in|About) (.+?)'s message ([0-9a-f]{6,20}):\n((?:>[^\n]*(?:\n|$))*)\s*$/;
const KINDS = { "Highlighted in": "text", "Boxed in": "box", "About": "msg" };
const HEADS = { text: "Highlighted in", box: "Boxed in", msg: "About" };
const FIRST = new Map();   // post id + length -> {q, kind, who, post, quote}
/** What the first message says: the question, and what it quotes. */
function firstOf(th) {
  const d = S.threads.get(th), p = d && d.posts[0];
  if (!p) return null;
  const k = p.id + ":" + (p.text || "").length;
  if (FIRST.has(k)) return FIRST.get(k);
  const t = displayText(p), m = HEAD_RE.exec(t);
  const out = m ? { q: btwText(t.slice(0, m.index)).trim(), kind: KINDS[m[1]], who: m[2], post: m[3], quote: m[4].split("\n").map(l => l.replace(/^> ?/, "")).join("\n").trim() }
    : { q: btwText(t).trim(), kind: null, who: null, post: null, quote: "" };
  FIRST.set(k, out);
  return out;
}
/** Where a side chat's mark is: the one this browser kept, else the post its first message quotes. */
function anchorOf(th) {
  if (AN[th]) return AN[th];
  const f = firstOf(th);
  return f && f.post ? { kind: f.kind, post: f.post, raw: f.kind === "msg" ? "" : quoteRaw(f.quote) } : null;
}
/** The words to find for a quote: a formula is quoted as its LaTeX, which the chat doesn't show as such, so it becomes a gap the words on either side are found around. */
export const quoteRaw = q => q.replace(/＠/g, "@").replace(/\$\$?[^$]*\$\$?/g, GAP).replace(/\s+/g, " ").trim().slice(0, 400);
const whoOf = p => (p.from === "person" ? "your" : p.from + "'s");
const said = (who) => (who === "your" ? "your message" : who.replace(/'s$/, "") + "'s message");

// ---- the text of a message, as offsets: what a mark holds on to ----
const SKIP = "button,input,textarea,.rc-bar,.rc-tools,.rc-fig-note,svg,.katex-mathml,annotation,style,script,.sc-layer";
const bodyOf = art => art && (art.querySelector(".msg-b .md") || art.querySelector(".msg-b") || art.querySelector(".md"));
function texts(root) {
  const out = [], w = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, { acceptNode: n => (n.parentElement && n.parentElement.closest(SKIP) ? NodeFilter.FILTER_REJECT : NodeFilter.FILTER_ACCEPT) });
  let n, at = 0;
  while ((n = w.nextNode())) { out.push({ n, at }); at += n.nodeValue.length; }
  return out;
}
/** A range's place in a message: {at, len, raw}. */
function offsets(r, root) {
  let a = -1, z = -1, raw = "";
  for (const { n, at } of texts(root)) {
    if (!r.intersectsNode(n)) continue;
    const s = n === r.startContainer ? r.startOffset : 0, e = n === r.endContainer ? r.endOffset : n.nodeValue.length;
    if (e <= s) continue;
    if (a < 0) a = at + s;
    z = at + e; raw += n.nodeValue.slice(s, e);
  }
  return a < 0 ? null : { at: a, len: z - a, raw: raw.slice(0, 300) };
}
const GAP = "\u2063";   // where a formula was in a quote
/** Where `words` are in `all`, ignoring spaces and line breaks (a quote has them between paragraphs, the text nodes don't): [start, end] or null. */
function findLoose(all, words, from = 0) {
  const map = [], hay = [];
  for (let i = 0; i < all.length; i++) if (!/\s/.test(all[i])) { hay.push(all[i]); map.push(i); }
  const nd = words.replace(/＠/g, "@").replace(/\s+/g, "");
  if (!nd) return null;
  const st = map.findIndex(i => i >= from), k = st < 0 ? -1 : hay.join("").indexOf(nd, st);
  return k < 0 ? null : [map[k], map[k + nd.length - 1] + 1];
}
/** The range a mark covers now: at its offset if the words are still there, else where the words are found. */
export function rangeAt(root, a) {
  if (!a || !a.raw) return null;
  const ts = texts(root), all = ts.map(t => t.n.nodeValue).join("");
  let at = a.at, len = a.len || a.raw.length;
  if (a.raw.includes(GAP) || all.slice(at, at + a.raw.length) !== a.raw) {
    // the words on either side of any formula: from the first to the last, found in order
    const bits = a.raw.split(GAP).map(b => b.trim()).filter(b => b.replace(/\s/g, "").length > 1);
    let first = bits.length ? findLoose(all, bits[0]) : null, last = first;
    if (first && bits.length > 1) last = findLoose(all, bits.at(-1), first[1]) || first;
    if (!first) { const bit = (bits[0] || "").slice(0, 40); first = last = bit.replace(/\s/g, "").length > 3 ? findLoose(all, bit) : null; }
    if (!first) return null;
    at = first[0]; len = last[1] - first[0];
  }
  const r = document.createRange(), end = at + len;
  let set = false;
  for (const { n, at: o } of ts) {
    const l = n.nodeValue.length;
    if (!set && at < o + l) { r.setStart(n, at - o); set = true; }
    if (set && end <= o + l) { r.setEnd(n, end - o); return r; }
  }
  return null;
}

/** What a side chat is about, from a range in message `art`: kind "text" (highlighted) or "box" (box: its rectangle, relative to the message). */
export function anchorFromRange(r, art, kind = "text", box = null, words = null) {
  const p = S.byId[art.dataset.key], root = bodyOf(art);
  if (!p || !root) return null;
  const o = r ? offsets(r, root) : null;
  const quote = (words != null ? words : r ? textOf(r.cloneContents()) : "").replace(/\n{3,}/g, "\n\n").trim();
  return { kind, post: p.id, who: whoOf(p), at: o ? o.at : 0, len: o ? o.len : 0, raw: o ? o.raw : "", box, quote, t: Date.now() };
}
/** What a side chat about the whole message holds: the start of what it says. */
function anchorOfMsg(art) {
  const p = S.byId[art.dataset.key], root = bodyOf(art);
  if (!p) return null;
  const all = root ? textOf(root).replace(/\n{3,}/g, "\n\n").trim() : displayText(p);
  return { kind: "msg", post: p.id, who: whoOf(p), at: 0, len: 0, raw: "", box: null, quote: all.length > 600 ? all.slice(0, 600).trimEnd() + " …" : all, t: Date.now() };
}
// The quote under the question in the first message, for the agent. An @ would wake whoever it names (dash.py calls_someone), so it can't stay one.
function quoteBlock(a) {
  if (!a || !a.post) return "";
  const q = (a.quote || "(no text: a picture or figure)").slice(0, 1500).replace(/@/g, "＠");
  return `\n\n> ${HEADS[a.kind] || "About"} ${a.who === "your" ? "the person" : a.who.replace(/'s$/, "")}'s message ${a.post}:\n` + q.split("\n").map(l => "> " + l).join("\n");
}

// ---- opening one in the side panel ----
const PENDING = new Map();   // thread -> {q, n}: a message sent and not on the board yet (shown until the thread has more than n posts)
const DRAFT = new Map();      // thread (or "new:" + main) -> what's typed and not sent
const OPENQ = new Set();      // threads whose quote is shown in full
const ERR = new Map();         // thread (or "new:" + main) -> why the last send failed
const FLY = new Set();         // thread (or "new:" + main) -> a message on its way: one at a time, wherever the panel is
/** The key a chat's draft, error and sending are kept under: a new side chat's is its conversation's, so each has its own. */
const keyOf = it => (it.chat === "new" ? "new:" + it.main : it.chat);
let FLASH = null;
const itemOf = (th, main) => ({ chat: th, main, name: tr("Side chat") });

/** Open side thread `th` of `main` in the side panel; `pending` is the question just sent, shown until the board has it. */
export function openSideChat(th, { main = btwParent(th), pending } = {}) {
  if (!main) return;
  if (pending) PENDING.set(th, { q: pending, n: 0 });
  const kids = sideChats(main), items = kids.map(t => itemOf(t, main));
  let i = kids.indexOf(th);
  if (i < 0) { items.push(itemOf(th, main)); i = items.length - 1; }
  openPreview(items, i);
}
/** A new side chat about `anchor` (null: about the conversation as a whole), its box ready to type in. */
export function newSideChat(main, anchor) {
  if (!main) return;
  const items = sideChats(main).map(t => itemOf(t, main));
  items.push({ chat: "new", main, name: tr("New side chat"), anchor: anchor || null });
  openPreview(items, items.length - 1);
}
/** The list of main's side chats, with New side chat and Draw a box. */
export function openSideList(main) { if (main) openPreview([{ chat: "list", main, name: tr("Side chats") }], 0); }

let cur = null;   // {it, api, unsub, sig}
/** Draw item `it` in the panel (preview.js show): api is the panel's head, body, foot and what it can do. */
export function showChat(it, api) {
  if (cur && cur.unsub) cur.unsub();
  cur = { it, api, sig: "" };
  api.root.setAttribute("aria-label", tr(it.chat === "list" ? "Side chats" : "Side chat"));
  if (it.chat === "list") { api.foot.hidden = true; api.foot.replaceChildren(); }
  else { api.foot.hidden = false; api.foot.replaceChildren(footer(it)); }
  api.body.replaceChildren(h("div", { class: "sc" }));
  api.body.scrollTop = 0;
  draw(true);
  cur.unsub = subscribe(() => { if (!api.alive()) { stop(); return; } draw(); });
  if (it.chat !== "list" && !matchMedia("(pointer:coarse)").matches) setTimeout(() => { const ta = api.foot.querySelector("textarea"); if (ta && api.alive()) ta.focus({ preventScroll: true }); }, 0);
  paint();
}
function stop() { if (cur && cur.unsub) cur.unsub(); cur = null; paint(); }

function draw(force) {
  if (!cur || !cur.api.alive()) return stop();
  const { it, api } = cur;
  const th = it.chat, d = th !== "new" && th !== "list" ? S.threads.get(th) : null, posts = d ? d.posts : [];
  const kids = sideChats(it.main);
  const k = keyOf(it);
  const sig = [th, posts.length, posts.length && posts.at(-1).id, posts.length && (posts.at(-1).text || "").length, PENDING.has(k), ERR.get(k) || "", FLY.has(k), OPENQ.has(th), kids.length, th === "list" ? kids.map(t => (S.threads.get(t) || { posts: [] }).posts.length).join() : ""].join("|");
  if (!force && sig === cur.sig) return;
  cur.sig = sig;
  api.head.replaceChildren(headOf(it, kids));
  const box = api.body, atEnd = box.scrollHeight - box.scrollTop - box.clientHeight < 48;
  const wrap = box.firstChild, next = th === "list" ? listOf(it, kids) : chatOf(it, posts);
  if (wrap) morph(wrap, next); else box.append(next);
  if (th !== "list" && (atEnd || force)) box.scrollTop = box.scrollHeight;
  const send = api.foot.querySelector(".sc-send"); if (send) send.toggleAttribute("disabled", FLY.has(k));
  const err = api.foot.querySelector(".sc-err"); if (err) { err.textContent = ERR.get(k) || ""; err.hidden = !ERR.get(k); }
}

function headOf(it, kids) {
  const th = it.chat, i = kids.indexOf(th), f = i >= 0 ? firstOf(th) : null, a = th === "new" ? it.anchor : i >= 0 ? anchorOf(th) : null;
  const title = th === "list" ? tr("Side chats") : th === "new" ? tr("New side chat") : f && f.q ? short(f.q.split("\n")[0], 80) : tr("Side chat");
  const fa = f && f.kind ? f : a;
  const about = fa && fa.kind ? `${HEADS[fa.kind] === "About" ? "About" : HEADS[fa.kind]} ${fa.who === "your" || fa.who === "the person" ? "your message" : said(fa.who)}` : th === "list" ? "" : "About the whole conversation";
  const meta = th === "list" ? `${kids.length ? kids.length + " in " : "About "}${short(threadTitle(it.main), 40)}` : [i >= 0 ? `Side chat ${i + 1} of ${kids.length}` : null, about].filter(Boolean).join(" · ");
  const { items, i: at } = cur.api, n = items.length;
  return h("div", { class: "pv-t" },
    th === "list" ? null : button("All side chats", () => openSideList(it.main), { kind: "icon", icon: "bands", size: "s", cls: "pv-files sc-all" }),
    h("b", { class: "pv-badge", "data-kind": "chat" }, i >= 0 ? String(i + 1) : th === "list" ? String(kids.length) : "+"),
    h("div", { class: "pv-n" }, h("span", { class: "pv-fn", title }, title), meta ? h("span", { class: "meta" }, meta) : null),
    n > 1 ? h("span", { class: "pv-nav" },
      button("Previous side chat", () => cur.api.go(-1), { kind: "icon", icon: "chevron", size: "s", cls: "pv-prev" }),
      h("span", { class: "meta" }, `${at + 1} / ${n}`),
      button("Next side chat", () => cur.api.go(1), { kind: "icon", icon: "chevron", size: "s", cls: "pv-next" })) : null,
    h("span", { class: "pv-tools" },
      i >= 0 ? h("a", { class: "btn", "data-kind": "icon", "data-size": "s", href: threadHref(th), title: tr("Open as its own conversation"), "aria-label": tr("Open as its own conversation") }, icon("open")) : null,
      fullBtn(),
      button("Close (Esc)", closePreview, { kind: "icon", icon: "close", size: "s", cls: "pv-x" })));
}

// The panel's body for one side chat: what it is about, then the messages.
function chatOf(it, posts) {
  const th = it.chat, f = th !== "new" ? firstOf(th) : null, a = th === "new" ? it.anchor : anchorOf(th);
  const quote = (f && f.quote) || (a && a.quote) || (a && a.raw) || "", kind = (f && f.kind) || (a && a.kind), postId = (f && f.post) || (a && a.post);
  const who = (f && f.who) || (a && a.who) || "";
  const pk = keyOf(it), pend = PENDING.get(pk);
  if (pend && posts.length > pend.n) PENDING.delete(pk);
  const still = PENDING.get(pk), last = posts.at(-1);
  const long = quote.length > 280 || quote.split("\n").length > 4;
  const label = kind === "box" ? "In the box" : kind === "msg" ? "About" : kind === "text" ? "Highlighted" : null;
  return h("div", { class: "sc" },
    kind ? h("section", { class: "sc-about", "data-kind": kind, "data-key": "about", "data-open": OPENQ.has(th) ? "" : null },
      h("div", { class: "sc-about-h" },
        icon(kind === "box" ? "marquee" : kind === "msg" ? "chat" : "pin"),
        h("span", null, `${label} · ${who === "your" || who === "the person" ? "your message" : said(who)}`),
        postId ? h("button", { type: "button", class: "btn sc-show", "data-size": "s", "data-kind": "quiet", onclick: () => showInChat(th === "new" ? null : th, postId, a) }, "Show in chat") : null),
      quote ? h("blockquote", { class: "sc-q" }, kind === "msg" ? short(quote.replace(/\s+/g, " "), OPENQ.has(th) ? 600 : 200) : quote) : null,
      quote && long ? h("button", { type: "button", class: "sc-more", onclick: () => { OPENQ.has(th) ? OPENQ.delete(th) : OPENQ.add(th); draw(); } }, OPENQ.has(th) ? "Show less" : "Show all") : null)
      : th === "new" ? h("section", { class: "sc-about", "data-kind": "all", "data-key": "about" }, h("div", { class: "sc-about-h" }, icon("chat"), h("span", null, "About this conversation as a whole"))) : null,
    h("div", { class: "sc-log", role: "log", "aria-live": "polite", "aria-label": "Side chat messages", "data-key": "log:" + th },
      posts.map((p, k) => bubble(p, k === 0 && f ? f.q : null)),
      still ? h("div", { class: "sc-b", "data-me": "", "data-key": "pend", "data-pend": "" }, h("div", { class: "sc-bt" }, h("p", null, still.q)), h("span", { class: "meta sc-st" }, "Sending…")) : null,
      (last && last.from === "person") || (still && th !== "new") || (still && !posts.length) ? h("p", { class: "sc-wait meta", "data-key": "wait" }, h("span", { class: "sc-dots", "aria-hidden": "true" }, h("i"), h("i"), h("i")), "Thinking…") : null,
      !posts.length && !still ? h("div", { class: "sc-empty", "data-key": "empty" },
        h("p", null, kind ? "Ask anything about this part of the conversation." : "Ask anything about this conversation."),
        h("p", { class: "meta" }, "A helper reads the conversation and answers here, so the main chat isn't interrupted. You can keep asking follow-ups.")) : null));
}
function bubble(p, q) {
  const me = p.from === "person";
  return h("div", { class: "sc-b", "data-me": me ? "" : null, "data-key": p.id },
    me ? null : h("span", { class: "sc-who" }, icon("chat"), p.from),
    h("div", { class: "sc-bt md" }, me ? h("p", null, q != null ? q : btwText(displayText(p))) : markdown(chatText(p))),
    h("span", { class: "meta sc-st", title: new Date(p.t).toLocaleString() }, clock(p.t)));
}

function footer(it) {
  const kind = it.anchor ? it.anchor.kind : null;
  const ph = it.chat !== "new" ? "Ask a follow-up…" : kind === "box" ? "Ask about what's in the box…" : kind === "text" ? "Ask about the highlighted text…" : kind === "msg" ? "Ask about this message…" : "Ask a side question…";
  const fit = ta => { ta.style.height = "auto"; ta.style.height = Math.min(ta.scrollHeight, 180) + "px"; };
  const ta = h("textarea", { class: "input sc-in", rows: 1, placeholder: ph, "aria-label": ph.replace("…", ""), enterkeyhint: "send",
    oninput: (e, el) => { DRAFT.set(keyOf(it), el.value); fit(el); },
    onkeydown: (e, el) => {
      if (e.key === "Enter" && !e.shiftKey && !e.isComposing) { e.preventDefault(); send(el); }
      else if (e.key === "Escape" && !el.value) { e.preventDefault(); closePreview(); }
    } });
  ta.value = DRAFT.get(keyOf(it)) || "";
  queueMicrotask(() => fit(ta));
  return h("div", { class: "sc-f" },
    h("p", { class: "sc-err", role: "alert", hidden: true }),
    h("div", { class: "sc-row" }, ta,
      button("Send (Enter)", () => send(ta), { kind: "icon", icon: "arrow-up", cls: "sc-send", tone: "accent" })),
    h("p", { class: "meta sc-tip" }, it.chat === "new" ? "Enter sends · Shift+Enter for a new line · the main chat isn't interrupted" : "Enter sends · Shift+Enter for a new line"));
}

async function send(ta) {
  if (!cur) return;
  const it = cur.it, raw = ta.value, q = raw.trim(), k = keyOf(it);
  if (!q || FLY.has(k)) return;   // one message at a time per side chat, even if the panel was closed and opened again meanwhile
  const was = it.chat, d = was !== "new" ? S.threads.get(was) : null;
  FLY.add(k); ERR.delete(k);
  let nk = k;
  try {
    if (was === "new") {
      PENDING.set(k, { q, n: 0 }); draw(true);
      const res = await post("/api/post", { text: "/btw " + q + quoteBlock(it.anchor), thread: it.main });
      if (!res || !res.thread) throw Error("the side chat didn't start; try again");
      PENDING.delete(k);
      if (it.anchor) { const { kind, post: pid, who, at, len, raw: words, box } = it.anchor; AN[res.thread] = { kind, post: pid, who, at, len, raw: words, box, t: Date.now() }; saveAnchors(); }
      PENDING.set(res.thread, { q, n: 0 });
      it.chat = res.thread; it.name = tr("Side chat"); nk = res.thread;
      if (cur && cur.it === it) { cur.api.mark(); const ph = "Ask a follow-up…"; ta.placeholder = tr(ph); ta.setAttribute("aria-label", tr("Ask a follow-up")); const tip = cur.api.foot.querySelector(".sc-tip"); if (tip) tip.textContent = tr("Enter sends · Shift+Enter for a new line"); }
    } else {
      PENDING.set(k, { q, n: d ? d.posts.length : 0 }); draw(true);
      await post("/api/post", { text: q, thread: was });
    }
    // what was sent leaves the box; anything typed while it was sending stays
    const now = DRAFT.has(k) ? DRAFT.get(k) : raw, rest = now.startsWith(raw) ? now.slice(raw.length).replace(/^\s+/, "") : now.trim() === q ? "" : now;
    DRAFT.delete(k); if (rest) DRAFT.set(nk, rest);
    if (ta.isConnected && cur && cur.it === it) { ta.value = rest; ta.style.height = "auto"; if (rest) ta.style.height = Math.min(ta.scrollHeight, 180) + "px"; }
    poll();
  } catch (e) {
    PENDING.delete(k);
    ERR.set(k, String(e && e.message || e));
  }
  FLY.delete(k);
  if (cur) draw(true);
  paint();
}

// The list of side chats: one row each, with New side chat and Draw a box above.
function listOf(it, kids) {
  const main = it.main;
  return h("div", { class: "sc sc-list" },
    h("div", { class: "sc-acts", "data-key": "acts" },
      button("New side chat", () => newSideChat(main, null), { icon: "plus", size: "s" }),
      button("Draw a box to ask", () => { if (innerWidth < 700) closePreview(); startBox(main); }, { icon: "marquee", size: "s", kind: "quiet" })),
    kids.length ? h("ol", { class: "sc-rows", "data-key": "rows" }, kids.map((t, k) => {
      const f = firstOf(t) || { q: "" }, d = S.threads.get(t), last = d && d.posts.at(-1), waiting = last && last.from === "person";
      return h("li", { "data-key": t }, h("button", { type: "button", class: "sc-row-b", onclick: () => openSideChat(t, { main }) },
        h("b", { class: "sc-num" }, String(k + 1)),
        h("span", { class: "sc-row-t" },
          h("span", { class: "sc-row-q" }, short((f.q || threadTitle(t)).split("\n")[0], 120)),
          f.quote ? h("span", { class: "sc-row-s meta" }, (f.kind === "box" ? "Box: " : f.kind === "msg" ? "Message: " : "“") + short(f.quote.replace(/\s+/g, " "), 90) + (f.kind === "text" ? "”" : "")) : null),
        h("span", { class: "meta sc-row-a", "data-wait": waiting ? "" : null }, waiting ? "Thinking…" : last ? ago(last.t) : "")));
    })) : null,
    kids.length ? null : h("div", { class: "sc-empty sc-how", "data-key": "how" },
      h("p", null, h("b", null, "Ask about part of this conversation, on the side.")),
      h("ul", null,
        h("li", null, "Select some words in a message, then press Side chat (or right-click them)."),
        h("li", null, "Right-click a message and choose Draw a box, to box a table, a formula or part of a picture."),
        h("li", null, "Or start a side chat about the conversation as a whole.")),
      h("p", { class: "meta" }, "A helper answers in the side panel. The main conversation and its agent aren't interrupted, and what you asked about stays marked in the chat.")));
}

// Show in chat: scroll the conversation to the mark and make it blink.
function showInChat(th, postId, a) {
  const box = document.querySelector("#main .cm-msgs"), art = box && box.querySelector(`article.msg[data-key="${CSS.escape(postId)}"]`);
  if (!art) { jump(postId); return; }
  const r = a && a.raw ? rangeAt(bodyOf(art), a) : null, rr = r && r.getBoundingClientRect(), br = box.getBoundingClientRect();
  const top = rr && rr.height ? rr.top : art.getBoundingClientRect().top;
  box.scrollTop += top - br.top - box.clientHeight / 3;
  FLASH = th || "new"; paint();
  if (!th) { art.setAttribute("data-flash", ""); setTimeout(() => art.removeAttribute("data-flash"), 1800); }
  setTimeout(() => { FLASH = null; paint(); }, 1600);
}

// ---- the marks in the chat: a layer over the messages ----
let raf = 0;
/** Draw the marks again (soon): after a render, a scroll, a new side chat. */
export function paint() { if (!raf) raf = requestAnimationFrame(drawMarks); }
const RCACHE = new Map();   // thread -> {art, n, r}: the range found last time, while the message's text is the same
function marksOf(th, a, art) {
  const root = bodyOf(art);
  if (!root || !a.raw) return null;
  const n = root.textContent.length, c = RCACHE.get(th);
  if (c && c.art === art && c.n === n) return c.r;
  const r = rangeAt(root, a);
  RCACHE.set(th, { art, n, r });
  return r;
}
function drawMarks() {
  raf = 0;
  const layer = document.querySelector("#main .sc-layer"), box = document.querySelector("#main .cm-msgs");
  if (!layer || !box) return;
  const main = here();
  if (!main || box.dataset.key !== "m:" + main) { if (layer.firstChild) layer.replaceChildren(); return; }
  const mw = layer.parentElement.getBoundingClientRect(), br = box.getBoundingClientRect();
  Object.assign(layer.style, { left: br.left - mw.left + "px", top: br.top - mw.top + "px", width: box.clientWidth + "px", height: br.height + "px" });
  const pk = previewKey(), open = pk && pk.startsWith("chat:") ? pk.slice(5) : null;
  const kids = sideChats(main), out = [], used = new Map();
  const add = (th, num, a) => {
    const art = box.querySelector(`article.msg[data-key="${CSS.escape(a.post)}"]`);
    if (!art) return;
    const ar = art.getBoundingClientRect();
    if (ar.bottom < br.top - 200 || ar.top > br.bottom + 200) return;
    const on = th === open ? "" : null, fl = th === FLASH ? "" : null;
    const r = a.kind !== "msg" ? marksOf(th, a, art) : null;
    const rs = r ? [...r.getClientRects()].filter(x => x.width > 1 && x.height > 1) : [];
    let y = ar.top + 8;
    if (rs.length) y = rs[0].top;
    if (a.kind === "text") rs.forEach((x, j) => out.push(h("i", { class: "sc-hl", "data-k": `hl:${th}:${j}`, "data-th": th, "data-on": on, "data-flash": fl, style: `left:${x.left - br.left}px;top:${x.top - br.top}px;width:${x.width}px;height:${x.height}px` })));
    if (a.kind === "box") {
      let L, T, R, B;
      if (a.box && Math.abs(a.box.aw - ar.width) < 2) { L = ar.left + a.box.x; T = ar.top + a.box.y; R = L + a.box.w; B = T + a.box.h; }
      else if (rs.length) { L = Math.min(...rs.map(x => x.left)) - 6; T = Math.min(...rs.map(x => x.top)) - 4; R = Math.max(...rs.map(x => x.right)) + 6; B = Math.max(...rs.map(x => x.bottom)) + 4; }
      if (L != null) { out.push(h("i", { class: "sc-box", "data-k": "box:" + th, "data-th": th, "data-on": on, "data-flash": fl, style: `left:${L - br.left}px;top:${T - br.top}px;width:${R - L}px;height:${B - T}px` })); y = T; }
    }
    // the pin: at the message's right edge, level with the mark; pins on one message stack so none hides another
    const taken = used.get(art) || []; used.set(art, taken);
    while (taken.some(v => Math.abs(v - y) < 26)) y += 26;
    taken.push(y);
    const f = firstOf(th), label = f && f.q ? `Side chat ${num}: ${short(f.q, 80)}` : `Side chat ${num}`;
    out.push(h("button", { type: "button", class: "sc-pin", "data-k": "pin:" + th, "data-th": th, "data-on": on, "data-flash": fl, "data-wait": waitingIn(th) ? "" : null, title: label, "aria-label": label,
      style: `left:${ar.right - br.left - 13}px;top:${y - br.top - 2}px`, onclick: () => openSideChat(th, { main }) }, String(num)));
  };
  kids.forEach((th, k) => { const a = anchorOf(th); if (a && a.post) add(th, k + 1, a); });
  if (open === "new" && cur && cur.it.chat === "new" && cur.it.anchor) add("new", "+", cur.it.anchor);
  // Keep the marks already there and move them: a scroll changes only where they are, so a pin keeps focus and hover.
  const had = new Map([...layer.children].map(el => [el.dataset.k, el])), keep = new Set();
  for (const n of out) {
    const o = had.get(n.dataset.k);
    if (!o) { layer.append(n); continue; }
    keep.add(o);
    for (const { name, value } of n.attributes) if (o.getAttribute(name) !== value) o.setAttribute(name, value);
    for (const { name } of [...o.attributes]) if (name !== "data-hot" && !n.hasAttribute(name)) o.removeAttribute(name);
    if (o.textContent !== n.textContent) o.textContent = n.textContent;
  }
  for (const el of had.values()) if (!keep.has(el)) el.remove();
}
const waitingIn = th => { const d = S.threads.get(th), l = d && d.posts.at(-1); return !!(l && l.from === "person"); };

// Hovering a pin lights up its mark; a click on highlighted words opens their side chat.
document.addEventListener("pointerover", e => {
  const pin = e.target.closest && e.target.closest(".sc-layer .sc-pin"), layer = document.querySelector("#main .sc-layer");
  if (!layer) return;
  const th = pin ? pin.dataset.th : null;
  for (const el of layer.children) el.toggleAttribute("data-hot", !!th && el.dataset.th === th);
});
document.addEventListener("click", e => {
  if (e.defaultPrevented || e.button !== 0 || !here() || document.body.hasAttribute("data-sc-box")) return;
  if (!e.target.closest || !e.target.closest("#main .cm-msgs article.msg") || e.target.closest("a,button,input,textarea,summary,[contenteditable]")) return;
  const s = getSelection(); if (s && !s.isCollapsed) return;
  for (const el of document.querySelectorAll("#main .sc-layer .sc-hl")) {
    const r = el.getBoundingClientRect();
    if (e.clientX >= r.left && e.clientX <= r.right && e.clientY >= r.top && e.clientY <= r.bottom) { openSideChat(el.dataset.th, { main: here() }); return; }
  }
});
let mo = null, moBox = null;
function watch() {
  const box = document.querySelector("#main .cm-msgs");
  if (box === moBox) return;
  if (mo) mo.disconnect();
  moBox = box; mo = null;
  if (box) { mo = new MutationObserver(() => { RCACHE.clear(); paint(); }); mo.observe(box, { childList: true, subtree: true, characterData: true }); }
}
addEventListener("scroll", e => { if (e.target === document || (e.target.closest && e.target.closest("#main"))) paint(); }, { capture: true, passive: true });
addEventListener("resize", () => { RCACHE.clear(); paint(); });
addEventListener("previewchange", () => paint());
addEventListener("hashchange", () => { closeMenu(); stopBox(); paint(); });
subscribe(() => paint());
/** After the chat view renders (comms.js frame): watch its messages and draw the marks. */
export function sync() { watch(); paint(); }

// ---- the right-click menu on a message ----
let menu = null;
function closeMenu() {
  if (!menu) return;
  const m = menu; menu = null; m.remove();
  removeEventListener("pointerdown", away, true); removeEventListener("keydown", menuKey, true); removeEventListener("scroll", closeOnScroll, true); removeEventListener("blur", closeMenu); removeEventListener("resize", closeMenu);
}
const away = e => { if (menu && !menu.contains(e.target)) closeMenu(); };
const closeOnScroll = e => { if (menu && !menu.contains(e.target)) closeMenu(); };
function menuKey(e) {
  if (!menu) return;
  const all = [...menu.querySelectorAll("[role=menuitem]")], i = all.indexOf(document.activeElement);
  if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); closeMenu(); return; }
  if (e.key === "Tab") { e.preventDefault(); return; }
  const d = e.key === "ArrowDown" ? 1 : e.key === "ArrowUp" ? -1 : 0;
  if (d) { e.preventDefault(); e.stopPropagation(); all[(i + d + all.length) % all.length].focus(); }
  else if (e.key === "Home" || e.key === "End") { e.preventDefault(); all[e.key === "Home" ? 0 : all.length - 1].focus(); }
}
function openMenu(x, y, art, p) {
  closeMenu();
  const main = here(), sel = selected(), mine = sel && sel.m === art ? sel : null;
  const item = (label, ic, fn, hint) => h("button", { type: "button", role: "menuitem", class: "menu-i sc-mi", tabindex: -1, onclick: () => { closeMenu(); fn(); } }, icon(ic), h("span", null, label), hint ? h("span", { class: "meta sc-mk" }, hint) : null);
  const kids = sideChats(main), onThis = kids.map((t, k) => [t, k + 1]).filter(([t]) => { const a = anchorOf(t); return a && a.post === p.id; });
  const range = mine && mine.r.cloneRange();
  menu = h("div", { class: "sc-menu menu", role: "menu", "aria-label": "Side chat" },
    mine ? [
      item("Ask about the highlighted text", "chat", () => { newSideChat(main, anchorFromRange(range, art, "text")); getSelection().removeAllRanges(); }),
      item("Quote in reply", "reply", () => askQuote(art, mine.text, p.at ? clock(p.at) : "", true)),
      item("Copy", "copy", () => navigator.clipboard && navigator.clipboard.writeText(mine.text)),
      h("hr")] : null,
    item("Ask about this message", "chat", () => newSideChat(main, anchorOfMsg(art))),
    item("Draw a box to ask…", "marquee", () => startBox(main)),
    item("New side chat", "plus", () => newSideChat(main, null)),
    onThis.length ? [h("hr"), h("p", { class: "meta sc-mh" }, "On this message"),
      onThis.map(([t, k]) => { const f = firstOf(t); return item(short((f && f.q) || "Side chat", 46), "pin", () => openSideChat(t, { main }), String(k)); })] : null,
    kids.length ? item(`All side chats (${kids.length})`, "bands", () => openSideList(main)) : null,
    h("p", { class: "meta sc-mfoot" }, "Shift + right-click: the browser's menu"));
  document.body.append(menu);
  const w = menu.offsetWidth, ht = menu.offsetHeight;
  menu.style.left = Math.max(8, Math.min(x, innerWidth - w - 8)) + "px";
  menu.style.top = Math.max(8, y + ht > innerHeight - 8 ? y - ht : y) + "px";
  addEventListener("pointerdown", away, true); addEventListener("keydown", menuKey, true); addEventListener("scroll", closeOnScroll, true); addEventListener("blur", closeMenu); addEventListener("resize", closeMenu);
  const first = menu.querySelector("[role=menuitem]"); if (first) first.focus({ preventScroll: true });
}
document.addEventListener("contextmenu", e => {
  if (e.shiftKey || e.defaultPrevented || e.pointerType === "touch" || !here() || document.body.hasAttribute("data-sc-box")) return;
  const art = e.target.closest && e.target.closest("#main .cm-msgs article.msg");
  if (!art || e.target.closest("a,img,video,audio,iframe,canvas,input,textarea,[contenteditable],.sc-pin")) return;
  const p = S.byId[art.dataset.key];
  if (!p) return;
  e.preventDefault();
  if (performance.now() - kbAt < 800) return;   // the key already opened it (below)
  if (!e.clientX && !e.clientY) { menuAt(art, p); return; }   // from the keyboard: beside the message, not the page's corner
  openMenu(e.clientX, e.clientY, art, p);
});
// The menu key, or Shift+F10, on a message (or something in it) opens the same menu beside it, or beside the words selected in it.
let kbAt = 0;
function menuAt(art, p) {
  const sel = selected(), r = (sel && sel.m === art ? sel.r : null), rr = r && r.getBoundingClientRect(), ar = art.getBoundingClientRect();
  if (rr && rr.height) openMenu(rr.left, rr.bottom + 4, art, p);
  else openMenu(ar.left + 24, Math.max(ar.top, 0) + 24, art, p);
}
document.addEventListener("keydown", e => {
  if (!(e.key === "ContextMenu" || (e.key === "F10" && e.shiftKey)) || menu || !here() || document.body.hasAttribute("data-sc-box")) return;
  const art = e.target.closest && e.target.closest("#main .cm-msgs article.msg");
  if (!art || e.target.closest("input,textarea,[contenteditable]")) return;
  const p = S.byId[art.dataset.key];
  if (!p) return;
  e.preventDefault(); kbAt = performance.now();
  menuAt(art, p);
});

// ---- drawing a box over part of the chat ----
let boxing = null;   // {main, hint, drag, x0, y0}
/** Box mode: the next drag over the messages draws a box, and a side chat opens about what is in it. */
export function startBox(main) {
  if (!main || boxing) return;
  closeMenu();
  getSelection().removeAllRanges();
  const hint = h("div", { class: "sc-hint", role: "status" }, icon("marquee"), h("span", null, "Drag a box over what you want to ask about"),
    button("Cancel (Esc)", () => stopBox(), { size: "s", kind: "quiet" }));
  boxing = { main, hint, drag: null };
  document.body.append(hint);
  document.body.setAttribute("data-sc-box", "");
  addEventListener("pointerdown", boxDown, true); addEventListener("keydown", boxKey, true);
}
function stopBox() {
  if (!boxing) return;
  const b = boxing; boxing = null;
  b.hint.remove(); if (b.drag) b.drag.remove();
  document.body.removeAttribute("data-sc-box");
  removeEventListener("pointerdown", boxDown, true); removeEventListener("keydown", boxKey, true);
  removeEventListener("pointermove", boxMove, true); removeEventListener("pointerup", boxUp, true); removeEventListener("pointercancel", boxCancel, true);
}
const boxKey = e => { if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); stopBox(); } };
function boxDown(e) {
  if (!boxing || e.button !== 0 || boxing.hint.contains(e.target)) return;
  const box = e.target.closest && e.target.closest("#main .cm-msgs");
  if (!box) { stopBox(); return; }
  e.preventDefault(); e.stopPropagation();
  boxing.x0 = e.clientX; boxing.y0 = e.clientY;
  boxing.drag = h("div", { class: "sc-drag", style: `left:${e.clientX}px;top:${e.clientY}px;width:0;height:0` });
  document.body.append(boxing.drag);
  addEventListener("pointermove", boxMove, true); addEventListener("pointerup", boxUp, true); addEventListener("pointercancel", boxCancel, true);
}
const rectOf = (e) => ({ left: Math.min(boxing.x0, e.clientX), top: Math.min(boxing.y0, e.clientY), right: Math.max(boxing.x0, e.clientX), bottom: Math.max(boxing.y0, e.clientY) });
function boxMove(e) {
  if (!boxing || !boxing.drag) return;
  e.preventDefault();
  const R = rectOf(e);
  Object.assign(boxing.drag.style, { left: R.left + "px", top: R.top + "px", width: R.right - R.left + "px", height: R.bottom - R.top + "px" });
}
const boxCancel = () => stopBox();
function boxUp(e) {
  if (!boxing) return;
  e.preventDefault(); e.stopPropagation();
  const R = rectOf(e), main = boxing.main, small = R.right - R.left < 10 && R.bottom - R.top < 10;
  stopBox();
  const box = document.querySelector("#main .cm-msgs");
  if (!box) return;
  // the message the box covers most of
  let art = null, best = 0;
  for (const m of box.querySelectorAll("article.msg")) {
    const r = m.getBoundingClientRect();
    const ov = small ? (R.left >= r.left && R.left <= r.right && R.top >= r.top && R.top <= r.bottom ? 1 : 0) : Math.max(0, Math.min(r.right, R.right) - Math.max(r.left, R.left)) * Math.max(0, Math.min(r.bottom, R.bottom) - Math.max(r.top, R.top));
    if (ov > best) { best = ov; art = m; }
  }
  if (!art || !S.byId[art.dataset.key]) return;
  if (small) { newSideChat(main, anchorOfMsg(art)); return; }
  const got = wordsIn(art, R), ar = art.getBoundingClientRect();
  const cl = { left: Math.max(R.left, ar.left), top: Math.max(R.top, ar.top), right: Math.min(R.right, ar.right), bottom: Math.min(R.bottom, ar.bottom) };
  const a = anchorFromRange(got.r, art, "box", { x: Math.round(cl.left - ar.left), y: Math.round(cl.top - ar.top), w: Math.round(cl.right - cl.left), h: Math.round(cl.bottom - cl.top), aw: Math.round(ar.width) }, got.text);
  if (a) newSideChat(main, a);
}
// The words whose middle is inside the box (a formula counts whole, as its LaTeX), in reading order, and the range from the first to the last.
function wordsIn(art, R) {
  const root = bodyOf(art), out = [];
  if (!root) return { r: null, text: "" };
  const inside = r => { const x = (r.left + r.right) / 2, y = (r.top + r.bottom) / 2; return r.width > 0 && x >= R.left && x <= R.right && y >= R.top && y <= R.bottom; };
  const hits = (r) => r.right >= R.left && r.left <= R.right && r.bottom >= R.top && r.top <= R.bottom;
  let first = null, last = null, lastY = null;
  const seenTex = new Set(), rg = document.createRange();
  for (const { n } of texts(root)) {
    rg.selectNodeContents(n);
    if (!hits(rg.getBoundingClientRect())) continue;
    const kx = n.parentElement.closest(".katex");
    if (kx) {
      if (seenTex.has(kx)) continue; seenTex.add(kx);
      const kr = kx.getBoundingClientRect();
      if (!inside(kr)) continue;
      const tex = kx.querySelector('annotation[encoding="application/x-tex"]');
      push(tex ? (kx.closest(".katex-display") ? `$$${tex.textContent}$$` : `$${tex.textContent}$`) : kx.textContent, kr, n, 0, n, n.nodeValue.length);
      continue;
    }
    for (const m of n.nodeValue.matchAll(/\S+/g)) {
      rg.setStart(n, m.index); rg.setEnd(n, m.index + m[0].length);
      const r = rg.getBoundingClientRect();
      if (inside(r)) push(m[0], r, n, m.index, n, m.index + m[0].length);
    }
  }
  function push(w, r, sn, so, en, eo) {
    if (lastY != null) out.push(Math.abs(r.top - lastY) > r.height * 0.6 ? "\n" : " ");
    out.push(w); lastY = r.top;
    if (!first) first = [sn, so];
    last = [en, eo];
  }
  let r = null;
  if (first) { r = document.createRange(); r.setStart(first[0], first[1]); r.setEnd(last[0], last[1]); }
  return { r, text: out.join("").replace(/ *\n */g, "\n") };
}
