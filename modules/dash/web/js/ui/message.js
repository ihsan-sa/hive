// One board post, as a chat message: author, who it went to, time, the words, files, and (on your own messages) whether it
// has reached the agent, in words. Used by Comms and the Agent page.
// message(post, opts) returns the whole article. Its head (.msg-h) and receipt (.msg-rc) can be redrawn on their own with
// messageHead() and messageReceipt(), so a long list refreshes names and read marks without touching the bodies.
// localMessage(item) draws a message that hasn't reached the board yet: sending, or failed with Retry.
import { h, copyText } from "../core/dom.js";
import { S, can, jget, SYS, OUTSIDE } from "../core/store.js";
import { simpleOn, hiveName } from "../core/simple.js";
import { isAgent, isWarden, isRunning, displayText, postSource, btwParent, btwText } from "../core/model.js";
import { clock, stamp, now, short, mdPlain, ago, DAY, MIN } from "../core/format.js";
import { markdown, mdCut } from "./markdown.js";
import { agentRef, threadRef } from "./refs.js";
import { agentHref } from "../core/router.js";
import { button, icon, statusDot } from "./kit.js";
import { replyTo } from "./composer.js";
import { wordButton } from "./rootword.js";

// ---- read marks ----
// Has this post reached the agent it names? Read: handed to it mid-run (the inbox hook's record, /api/delivered),
// or a session of it began after the post, whose prompt carries it.
const DELIV = { sets: {}, sig: "" };

/** Fetch /api/delivered. Resolves true when the marks changed: call it from a view's poll, every 3 s, and update if true. */
export async function pollDelivered() {
  try {
    const v = await jget("/api/delivered"), sig = JSON.stringify(v);
    if (sig === DELIV.sig) return false;
    DELIV.sig = sig; DELIV.sets = Object.fromEntries(Object.entries(v).map(([k, x]) => [k, new Set(x)]));
    return true;
  } catch (e) { return false; }
}
/** What read marks currently depend on, as a string to compare. */
export const marksSig = () => DELIV.sig + JSON.stringify((S.live && S.live.pending) || []);

/** Where a post stands with one recipient: {key: seen|waiting|queued|unseen, tone, text}. null when it doesn't apply. */
export function delivery(p, m) {
  if (m === "new") {
    const st = (S.woke[p.id] || [])[0];
    if (st) return { key: "seen", tone: "good", text: `${st.name} started on it`, who: st.name };
    return now() - p.t < 3 * MIN ? { key: "queued", tone: "info", text: "Starting an agent…" } : { key: "unseen", tone: "bad", text: "No agent has started on this" };
  }
  if (!isAgent(m) || now() - p.t > 3 * DAY || p.from === m) return null;
  // A post to the warden starts a fresh warden run (w + 4 hex), and one to a dead agent starts its heir: that run has it.
  const took = (S.woke[p.id] || []).find(s => s.name !== m && !p.men.includes(s.name) && (s.heirOf === m || m === "warden" && isWarden(s.name) && s.name !== "rootwarden"));
  if (took) return { key: "seen", tone: "good", text: `${took.name} took it for ${m}`, who: took.name };
  const q = S.pending[m], queued = q && (q.ids || []).includes(p.id);
  const set = DELIV.sets[m], last = S.lastSess[m];
  if ((set && set.has(p.id)) || (!queued && last && last.start >= p.t - 2000) || answered(p, m)) return { key: "seen", tone: "good", text: `Seen by ${m}`, who: m };
  // A running agent gets the post from its hook after its current step, even while the starter also holds it for the next start.
  if (isRunning(m)) return { key: "waiting", tone: "warn", text: `${m} sees this after its current step`, who: m };
  if (queued) return { key: "queued", tone: "info", text: `Waiting for ${m} to start`, who: m };
  // Just posted to a sleeping agent: the starter takes a moment to wake it, so it isn't "not reached" yet.
  if (now() - p.t < 2 * MIN) return { key: "queued", tone: "info", text: `Waking ${m}…`, who: m };
  return { key: "unseen", tone: "bad", text: `Hasn't reached ${m}`, who: m };
}
/** The board itself says m has it: m replied to it, or posted in its thread after it. */
function answered(p, m) {
  const d = S.threads.get(p.th); if (!d) return false;
  for (let i = d.posts.length - 1; i >= 0 && d.posts[i].t > p.t; i--) if (d.posts[i].from === m) return true;
  return S.posts.some(x => x.reply_to === p.id && x.from === m);
}
const RMARK = { seen: "✓", waiting: "…", queued: "…", unseen: "○" };

// The person's newest post in each thread carries its receipt even once seen; older ones only while not seen.
let lastMine = { ver: -1, by: {} };
function isLastMine(p) {
  if (lastMine.ver !== S.ver) { lastMine = { ver: S.ver, by: {} }; for (const x of S.posts) if (OUTSIDE.has(x.from)) lastMine.by[x.th] = x.id; }
  return lastMine.by[p.th] === p.id;
}
/** The receipt under your own message: where it is, in words. Always returns the element, empty when there's nothing to say. */
export function messageReceipt(p) {
  const el = h("div", { class: "msg-rc", role: "status" });
  if (!S.app.app || !OUTSIDE.has(p.from) || now() - p.t > 3 * DAY) return el;
  const ds = p.men.map(m => delivery(p, m)).filter(Boolean), last = isLastMine(p);
  if (simpleOn()) {  // no names and no machinery: one word, on your latest message
    if (last && ds.length) el.append(h("span", { "data-tone": "quiet" }, ds.some(d => d.key === "seen") ? "Seen" : "Sent"));
    return el;
  }
  const show = ds.filter(d => last || d.key !== "seen");
  for (const d of show) el.append(h("span", { "data-tone": d.tone }, h("b", { "aria-hidden": "true" }, RMARK[d.key]), d.text));
  return el;
}

// ---- attachments: images as thumbnails, text files open in place, anything else downloads ----
// All open in one viewer window. Text is shown through textContent only, and svg is never rendered.
export const IMG = /\.(png|jpe?g|gif|webp)$/i;
const TXT = /\.(txt|md|log|json|jsonl|csv|tsv|py|js|mjs|ts|sh|c|h|cpp|v|sv|vh|yaml|yml|toml|ini|cfg|xml|html|css|go|rs|kicad_pcb|kicad_sch|net|sp|cir|spice|lef|def|sdc|tcl)$/i;
export const fileUrl = (p, f) => `/files/${encodeURIComponent(p.id)}/${encodeURIComponent(f.name)}`;
export const PDF = /\.pdf$/i;
export const VID = /\.(mp4|webm|mov|m4v)$/i;
export const PAGE = /\.html?$/i;
export const pageUrl = (p, f) => fileUrl(p, f) + "?view=1";
export const fsize = n => (n == null ? "" : n >= 1048576 ? (n / 1048576).toFixed(1) + " MB" : n >= 1024 ? Math.round(n / 1024) + " kB" : n + " B");
const ext = name => (name.split(".").pop() || "file").slice(0, 5).toUpperCase();

// An image that won't load (gone, or not copied here) shows as a tile that says so, not the browser's broken icon. error doesn't bubble.
if (typeof document !== "undefined") document.addEventListener("error", e => {
  const t = e.target && e.target.tagName === "IMG" && e.target.closest(".msg-thumb"); if (t) t.dataset.broken = "";
}, true);
function attachment(p, f) {
  const u = fileUrl(p, f);
  if (IMG.test(f.name)) return h("button", { type: "button", class: "msg-thumb", title: f.name + ", click to view", onclick: () => viewFile(p, f) },
    h("img", { src: u, alt: f.name, loading: "lazy" }), h("span", null, f.name));
  if (simpleOn() && PAGE.test(f.name)) return h("a", { class: "msg-file", title: "Open this page in a new tab", href: pageUrl(p, f), target: "_blank", rel: "noopener noreferrer" },
    h("b", null, "PAGE"), h("span", null, h("span", { class: "msg-fn" }, f.name), h("span", { class: "meta" }, "open the page")));
  if (VID.test(f.name)) return h("button", { type: "button", class: "msg-file", title: "Click to play", onclick: () => viewFile(p, f) },
    h("b", null, "VIDEO"), h("span", null, h("span", { class: "msg-fn" }, f.name), h("span", { class: "meta" }, fsize(f.size) + " · play")));
  if (PDF.test(f.name)) return h("button", { type: "button", class: "msg-file", title: "Click to read", onclick: () => viewFile(p, f) },
    h("b", null, "PDF"), h("span", null, h("span", { class: "msg-fn" }, f.name), h("span", { class: "meta" }, fsize(f.size) + " · read")));
  const txt = TXT.test(f.name) && (f.size == null || f.size < 2 * 1048576);
  const props = txt ? { type: "button", onclick: () => viewFile(p, f) } : { href: u, download: f.name };
  return h(txt ? "button" : "a", { class: "msg-file", title: txt ? "Click to read" : "Download", ...props },
    h("b", null, ext(f.name)),
    h("span", null, h("span", { class: "msg-fn" }, f.name), h("span", { class: "meta" }, fsize(f.size) + (txt ? " · read" : " · download"))));
}

const MD = /\.(md|markdown)$/i;
function viewFile(p, f) {
  const all = (p.files || []).filter(x => IMG.test(x.name) || TXT.test(x.name) || PDF.test(x.name) || VID.test(x.name));
  let i = Math.max(0, all.findIndex(x => x.name === f.name));
  const stage = h("div", { class: "viewer-s" }), cap = h("div", { class: "meta viewer-c" }), open = h("a", { class: "btn", "data-size": "s", target: "_blank", rel: "noopener" }, "Open original");
  const dlg = h("dialog", { class: "viewer", "aria-label": "File viewer" },
    h("div", { class: "viewer-b" }, cap,
      all.length > 1 ? button("‹ Prev", () => show(i - 1), { size: "s" }) : null,
      all.length > 1 ? button("Next ›", () => show(i + 1), { size: "s" }) : null,
      open, button("Close", () => dlg.close(), { size: "s" })),
    stage);
  async function show(k) {
    i = (k + all.length) % all.length;
    const g = all[i], u = fileUrl(p, g);
    open.href = u; cap.textContent = `${g.name} · ${fsize(g.size)} · from ${p.from === "person" ? "you" : p.from}, ${stamp(p.t)}`;
    if (IMG.test(g.name)) {
      const im = h("img", { src: u, alt: g.name, title: "Click to zoom" });
      im.addEventListener("click", () => im.toggleAttribute("data-full"));
      stage.replaceChildren(im); return;
    }
    if (VID.test(g.name)) { open.href = pageUrl(p, g); stage.replaceChildren(h("video", { class: "viewer-vid", src: pageUrl(p, g), controls: true, autoplay: true, playsinline: true, title: g.name })); return; }
    if (PDF.test(g.name)) { open.href = pageUrl(p, g); stage.replaceChildren(h("iframe", { class: "viewer-pdf", src: pageUrl(p, g), title: g.name })); return; }
    const pre = h("pre", null, "Loading…"); stage.replaceChildren(pre);
    try {
      const r = await fetch(u, { cache: "no-store" }); if (!r.ok) throw Error(r.status);
      let t = await r.text(); const cut = t.length > 400000; if (cut) t = t.slice(0, 400000);
      pre.textContent = t + (cut ? "\n\n… (cut here; open the original for the rest)" : "");
      if (MD.test(g.name)) {   // Markdown shows formatted, with the plain text one press away
        const md = markdown(t + (cut ? "\n\n… (cut here; open the original for the rest)" : ""));
        let plain = false; const flip = button("Plain text", () => { plain = !plain; stage.replaceChildren(plain ? pre : md, flip); flip.textContent = plain ? "Formatted" : "Plain text"; }, { size: "s", cls: "viewer-t" });
        stage.replaceChildren(md, flip);
      }
    } catch (e) { pre.textContent = "Couldn't load it: " + e.message; }
  }
  dlg.addEventListener("keydown", e => { if (all.length > 1 && e.key === "ArrowRight") show(i + 1); if (all.length > 1 && e.key === "ArrowLeft") show(i - 1); });
  dlg.addEventListener("click", e => { if (e.target === dlg) dlg.close(); });
  dlg.addEventListener("close", () => dlg.remove());
  document.body.append(dlg); dlg.showModal(); show(i);
}

// ---- long posts: the start and the end show, the middle folds, so a report's conclusion is never the hidden part ----
const LONG = 2400, HEAD = 900, TAIL = 700, OPEN = new Set();
/** Split into blocks at blank lines outside code fences. */
function blocks(t) {
  const out = []; let cur = [], fence = false;
  for (const line of t.split("\n")) {
    if (/^\s*(```|~~~)/.test(line)) fence = !fence;
    if (!fence && !line.trim() && cur.length) { out.push(cur.join("\n")); cur = []; continue; }
    cur.push(line);
  }
  if (cur.length) out.push(cur.join("\n"));
  return out;
}
function fold(t) {
  const b = blocks(t); let i = 0, j = b.length, a = 0, z = 0;
  while (i < j && a < HEAD) a += b[i++].length;
  while (j > i && z + b[j - 1].length < TAIL) z += b[--j].length;
  if (j - i < 1 || j >= b.length) return cut(t);   // nothing to fold by blocks, or the last block alone is too long to keep: cut inside it
  const mid = b.slice(i, j).join("\n\n");
  return { head: b.slice(0, i).join("\n\n"), tail: b.slice(j).join("\n\n"), lines: mid.split("\n").filter(l => l.trim()).length, chars: mid.length };
}

// One block too long to keep whole (a wall of text, or hundreds of lines with no blank one between): cut it at a line end near
// HEAD and TAIL, else at a space. A text with a fence isn't cut, since the code block folds itself.
function cut(t) {
  if (/^\s*(```|~~~)/m.test(t)) return null;
  const at = (i, back) => {
    const nl = back ? t.lastIndexOf("\n", i) : t.indexOf("\n", i);
    if (nl > 0 && Math.abs(nl - i) < 300) return nl;
    const sp = back ? t.lastIndexOf(" ", i) : t.indexOf(" ", i);
    return sp > 0 && Math.abs(sp - i) < 200 ? sp : i;
  };
  const nth = (n, back) => { let k = back ? t.length : -1; for (let c = 0; c < n; c++) { k = back ? t.lastIndexOf("\n", k - 1) : t.indexOf("\n", k + 1); if (k < 0) break; } return k; };
  let a = at(HEAD, true), z = at(t.length - TAIL, false);
  const a2 = nth(20, false), z2 = nth(12, true);   // short lines: keep about 20 at the top and 12 at the end, not 900 characters of them
  if (a2 > 0 && a2 < a) a = a2;
  if (z2 > z) z = z2;
  if (z - a < LONG / 4) return null;
  const mid = t.slice(a, z);
  return { head: t.slice(0, a), tail: t.slice(z).replace(/^\s+/, ""), lines: mid.split("\n").filter(l => l.trim()).length, words: mid.split(/\s+/).filter(Boolean).length, chars: mid.length };
}

// Fold parsed blocks, so fences, footnotes and inline formatting remain intact.
// Native details keep their children (and selection); the owned body survives morphs.
const SECTIONS = new Map();
function report(t, id, ws) {
  const md = markdown(t), nodes = [...md.children];
  const heads = nodes.filter(n => /^H[1-6]$/.test(n.tagName));
  if (heads.length < 3) return null;
  const level = [1, 2, 3, 4, 5, 6].find(l => heads.filter(n => n.tagName === "H" + l).length >= 3);
  if (!level) return null;
  const starts = nodes.map((n, i) => n.tagName === "H" + level ? i : -1).filter(i => i >= 0);
  if (starts.length < 3) return null;
  for (let k = 1; k < starts.length - 1; k++) {
    const first = nodes[starts[k]], content = nodes.slice(starts[k] + 1, starts[k + 1]);
    const key = id + ":" + k, match = ws?.some(w => content.some(n => n.textContent.toLowerCase().includes(w.toLowerCase())));
    const section = h("details", { class: "rc-sec", open: match || SECTIONS.get(key) || false,
      ontoggle: (e, d) => { if (e.target === d) SECTIONS.set(key, d.open); } }, h("summary", null, first.cloneNode(true)), content);
    first.replaceWith(section);
  }
  return h("div", { class: "msg-b", "data-own": "", "data-sig": t + ":" + (ws || []).join() }, markBody(md, ws));
}

// ---- the whole post: copy it, or see it as written ----
const SOURCE = new Set(); let menuFor = null;
function sourceView(p, ws) {
  return h("div", { class: "msg-b" },
    h("pre", { class: "rc-raw", tabindex: "0" }, p.text),
    h("div", { class: "rc-rawnote" }, h("span", null, "What is on the board, as written."),
      h("button", { type: "button", class: "rc-t", onclick: (e, b) => flash(b, p.text) }, "Copy"),
      h("button", { type: "button", class: "rc-t", onclick: (e, b) => { SOURCE.delete(p.id); b.closest(".msg-b").replaceWith(body(p, ws)); } }, "Back to the post")));
}
const flash = (b, text) => copyText(text).then(() => { const was = b.textContent; b.dataset.ok = ""; b.textContent = "Copied"; setTimeout(() => { delete b.dataset.ok; b.textContent = was; }, 1400); }, () => {});
function closeMenu() {
  document.querySelector(".rc-menu")?.remove();
  document.querySelector('.rc-moreb[aria-expanded="true"], .msg-moreb[aria-expanded="true"]')?.setAttribute("aria-expanded", "false");
  menuFor = null;
}
addEventListener("click", e => { if (menuFor && !e.target.closest?.(".rc-more,dialog.fb")) closeMenu(); }, true);
const moreMenu = (p, ws) => h("div", { class: "rc-menu", role: "menu" },
  h("button", { type: "button", role: "menuitem", onclick: () => { copyText(displayText(p)); closeMenu(); } }, "Copy as Markdown"),
  h("button", { type: "button", role: "menuitem", onclick: () => { copyText(mdPlain(displayText(p))); closeMenu(); } }, "Copy as plain text"),
  h("button", { type: "button", role: "menuitem", onclick: (e, b) => {
    const was = b.closest(".msg")?.querySelector(".msg-b"); closeMenu();
    if (SOURCE.has(p.id)) SOURCE.delete(p.id); else SOURCE.add(p.id);
    was?.replaceWith(body(p, ws));
  } }, SOURCE.has(p.id) ? "Back to the post" : "View source"));
function more(p, ws) {
  const open = menuFor === p.id;
  return h("span", { class: "rc-more" },
    h("button", { type: "button", class: "rc-t rc-moreb", "aria-haspopup": "menu", "aria-expanded": String(open), "aria-label": "More for this message", onclick: (e, b) => {
      const was = menuFor === p.id;
      closeMenu();
      if (!was) { menuFor = p.id; b.setAttribute("aria-expanded", "true"); b.parentNode.append(moreMenu(p, ws)); }
    } }, "⋯"),
    open ? moreMenu(p, ws) : null);
}

// ---- search marks ----
const wordsRe = ws => new RegExp(ws.map(w => w.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).join("|"), "gi");
/** A string as text and <mark> nodes, for h(). */
export function markWords(text, ws) {
  const re = wordsRe(ws), out = []; let at = 0;
  for (const m of text.matchAll(re)) { if (m.index > at) out.push(text.slice(at, m.index)); out.push(h("mark", null, m[0])); at = m.index + m[0].length; }
  if (at < text.length) out.push(text.slice(at));
  return out;
}
/** Mark the words in a rendered post, whatever formatting they sit in. Only touches text nodes, so a post's HTML stays text. */
export function markBody(el, ws) {
  if (!ws || !ws.length) return el;
  const re = wordsRe(ws), walk = document.createTreeWalker(el, NodeFilter.SHOW_TEXT), found = [];
  while (walk.nextNode()) if (walk.currentNode.nodeValue.search(re) >= 0 && !walk.currentNode.parentNode.closest(".rc-bar,.rc-eq-a,.rc-foldb")) found.push(walk.currentNode);
  for (const n of found) n.replaceWith(...markWords(n.nodeValue, ws));
  // A code block or equation keeps its drawing until its source changes: tell it the marks did, going in and coming out of a search.
  for (const o of el.querySelectorAll("[data-own][data-sig]")) if (o.querySelector("mark")) o.dataset.sig += ":" + ws.join();
  return el;
}

function body(p, ws) {
  if (SOURCE.has(p.id)) return sourceView(p, ws);
  const t = displayText(p);
  if (t.length > LONG) { const r = report(t, p.id, ws); if (r) return r; }
  if (!t.trim()) return (p.files || []).length ? null : h("div", { class: "msg-b" }, h("p", { class: "meta" }, "No text"));
  const f = t.length > LONG && !OPEN.has(p.id) ? fold(t) : null;
  if (!f) return h("div", { class: "msg-b" }, markBody(markdown(t), ws));
  return h("div", { class: "msg-b", "data-folded": "" },
    markBody(markdown(f.head), ws),
    button(`Show the middle: ${f.lines > 2 || f.words == null ? f.lines + " more lines" : f.words.toLocaleString() + " more words"}`, (e, b) => { OPEN.add(p.id); b.closest(".msg-b").replaceWith(body(p, ws)); }, { kind: "quiet", size: "s", cls: "msg-more" }),
    markBody(markdown(f.tail), ws));
}

// ---- jumping to the post a reply answers. The view that lists posts says how. ----
let jumper = null;
export const onJump = fn => { jumper = fn; };
const who = n => (n === "person" ? "You" : simpleOn() ? hiveName() : n);

function quote(p) {
  const q = S.byId[p.reply_to];
  return h("button", { type: "button", class: "msg-q", title: q ? "Go to the message this answers" : `Answers post ${p.reply_to}, not loaded here`, onclick: () => jumper && jumper(p.reply_to) },
    icon("reply"), h("b", null, q ? who(q.from) : "An earlier message"), h("span", null, q ? short(mdPlain(displayText(q)), 140) : p.reply_to));
}

// Copy the message's text. The tick shows for a moment, then the icon comes back.
function copyBtn(p) {
  return button("Copy", (e, b) => copyText(displayText(p)).then(() => { b.dataset.done = ""; b.title = "Copied"; setTimeout(() => { delete b.dataset.done; b.title = "Copy"; }, 1400); }), { kind: "icon", size: "s", icon: "copy", cls: "msg-copy" });
}

// Between agents only trouble is worth a word (a running one sees its posts after its current step anyway), and it says who (the person, 11041326b458).
function unseenNote(ms) {
  if (!ms.length) return null;
  const names = ms.length === 1 ? ms[0] : ms.slice(0, -1).join(", ") + " and " + ms.at(-1);
  return h("span", { class: "msg-r", "data-tone": "bad", title: ms.map(m => `Hasn't reached ${m}`).join("\n") }, `${names} ${ms.length === 1 ? "hasn't" : "haven't"} seen this`);
}
/** The line above a message: author, who it went to, thread (outside a thread), who it woke, Reply, time. */
export function messageHead(p, { showThread = true, mark } = {}) {
  if (!S.app.app) return devHead(p, showThread);
  if (btwOpener(p)) return btwHead(p);
  const mine = OUTSIDE.has(p.from) && p.from === "person";
  const woke = (S.woke[p.id] || []).filter(s => !p.men.includes(s.name) || !s.resumed), tos = p.men.filter(m => m !== "new" && m !== p.from);
  const toYou = tos.length === 1 && tos[0] === "person";
  const src = mine ? postSource(p) : null, me = S.app.signer;
  // Someone else signed in to this Hive wrote it: their name, not "You". A name you can't tell apart goes on the badge.
  const other = src && src.how === "Web" && src.name && me && src.name !== me;
  if (simpleOn()) return h("div", { class: "msg-h" },
    mine ? h("b", { class: "msg-me" }, other ? src.name : "You") : h("b", null, OUTSIDE.has(p.from) ? who(p.from) : hiveName()),
    h("span", { class: "msg-end" }, more(p, mark), h("time", { class: "meta num", datetime: p.at, title: stamp(p.t) }, clock(p.t))));
  return h("div", { class: "msg-h" },
    mine ? h("b", { class: "msg-me", title: other ? src.said : "you (person)" }, other ? src.name : "You") : agentRef(p.from),
    src ? h("span", { class: "msg-src", title: src.said }, (src.name && src.name !== me && !other ? src.name + " · " : "") + src.how) : null,
    tos.length ? h("span", { class: "msg-to" }, toYou ? "to you" : "to") : null,
    toYou ? null : tos.map(m => m === "person" ? h("b", { class: "msg-me" }, "you") : agentRef(m)),
    toYou ? null : unseenNote(mine ? [] : tos.filter(m => delivery(p, m)?.key === "unseen")),
    showThread ? threadRef(p.th) : null,
    woke.length && !mine ? h("span", { class: "meta" }, woke.map(s => (s.resumed ? "woke " : "started ") + s.name).join(", ")) : null,
    h("time", { class: "meta num msg-time", datetime: p.at, title: stamp(p.t) }, clock(p.t)));
}

/** Under the message, shown on hover or focus: Reply, Copy, ⋯ and the time. Own messages carry the To in Reply's title. */
function messageActions(p, mark) {
  const mine = p.from === "person", tos = p.men.filter(m => m !== "new" && m !== p.from);
  return h("div", { class: "msg-end" },
    can("post") ? button("Reply", () => replyTo(p), { kind: "icon", size: "s", icon: "reply", cls: "msg-reply", title: mine && tos.length ? `Reply · this went to ${tos.join(", ")}` : "Reply to this message; it goes to its author" }) : null,
    copyBtn(p),
    more(p, mark),
    h("time", { class: "meta num", datetime: p.at, title: stamp(p.t) }, (mine && tos.length ? `to ${tos.join(", ")} · ` : "") + clock(p.t)));
}

// A side thread's first post: the hive opened it from a /btw, so it reads as the hive asking, not as a message the person typed here (11041319bbd0).
const btwOpener = p => { if (p.from !== "person" || !btwParent(p.th)) return false; const d = S.threads.get(p.th); return !!d && !!d.posts[0] && d.posts[0].id === p.id; };
function btwMessage(p, ws) {
  const q = { ...p, disp: btwText(displayText(p)) };
  return h("article", { class: "msg msg-btw", "data-key": p.id, "data-who": "agent", "aria-label": `${hiveName()}, side question, ${stamp(p.t)}` },
    btwHead(p), body(q, ws));
}
function btwHead(p) {
  return h("div", { class: "msg-h" }, h("b", null, hiveName()), h("span", { class: "meta" }, "side question from"), threadRef(btwParent(p.th)),
    h("time", { class: "meta num msg-time", datetime: p.at, title: stamp(p.t) }, clock(p.t)));
}

/** opts: {showThread, mark: words to highlight, cont: same author as the post above, so the head folds away} */
export function message(p, opts = {}) {
  if (!S.app.app) return devMessage(p, opts);
  const files = p.files || [], ws = opts.mark;
  if (ws && ws.length && !OPEN.has(p.id) && displayText(p).length > LONG) {
    const lc = displayText(p).toLowerCase(), f = fold(displayText(p));
    if (f && ws.some(w => lc.includes(w)) && !ws.some(w => (f.head + f.tail).toLowerCase().includes(w))) OPEN.add(p.id);
  }
  if (btwOpener(p)) return btwMessage(p, ws);
  const kind = SYS.has(p.from) ? "sys" : p.from === "person" ? "me" : OUTSIDE.has(p.from) ? "out" : "agent";
  return h("article", { class: "msg", "data-key": p.id, "data-who": kind, "data-cont": opts.cont ? "" : null, "data-report": displayText(p).length > LONG ? "" : null, "aria-label": `${who(p.from)}, ${stamp(p.t)}` },
    messageHead(p, opts),
    p.reply_to ? quote(p) : null,
    body(p, ws),
    files.length ? h("div", { class: "msg-files" }, files.map(f => attachment(p, f))) : null,
    wordButton(p),
    messageReceipt(p),
    simpleOn() ? null : messageActions(p, ws));
}

// ---- the developer route: a dense log, as it was. Arrows and marks in the head, long posts cut short. ----
const DEV_LONG = 700, DEV_CUT = 600, RAW = new Set();
// The leading run of @names, when every one is a recipient the head already shows, and the "reply to message <id>:" line when the post answers one. Mentions inside the sentence stay; ⋯ › Raw post shows it all.
function devBody(p) {
  if (p.dev != null) return p.dev;
  let t = String(p.text || ""); const m = t.match(/^(?:@[a-z][\w-]*[\s,:]*)+/i);
  if (m && [...m[0].matchAll(/@([a-z][\w-]*)/gi)].every(x => p.men.includes(x[1]))) t = t.slice(m[0].length);
  if (p.reply_to) t = t.replace(/^reply to message [0-9a-f]+:\s*/i, "");
  return (p.dev = t);
}
const devText = p => (devBody(p).length > DEV_LONG && !OPEN.has(p.id) ? mdCut(devBody(p), DEV_CUT) : devBody(p));
function devBodyEl(p, ws) {
  if (RAW.has(p.id)) return h("pre", { class: "md msg-raw" }, p.text);
  const t = devText(p);
  if (OPEN.has(p.id) && t.length > LONG) {
    const r = report(t, p.id, ws);
    if (r) { const md = r.firstElementChild; md.dataset.own = ""; md.dataset.sig = r.dataset.sig; return md; }
  }
  return markBody(markdown(t), ws);
}
const moreLabel = p => OPEN.has(p.id) ? "Show less" : `Show all · ${devBody(p).length.toLocaleString("en-US")} characters`;
// On hover, top right: Copy text, Copy link, and ⋯ for the raw post, its id and the author's page. Reply only where this address can post.
function devTools(p, ws) {
  const flash = b => { b.dataset.done = ""; setTimeout(() => delete b.dataset.done, 1400); };
  const open = menuFor === p.id, menu = () => h("div", { class: "rc-menu", role: "menu" },
    h("button", { type: "button", role: "menuitem", onclick: (e, b) => {
      const m = b.closest(".msg"); closeMenu();
      if (RAW.has(p.id)) RAW.delete(p.id); else RAW.add(p.id);
      m?.querySelector(".md")?.replaceWith(devBodyEl(p, ws));
    } }, RAW.has(p.id) ? "Back to the post" : "Raw post"),
    h("button", { type: "button", role: "menuitem", onclick: () => { copyText(p.id); closeMenu(); } }, `Copy post id · ${p.id}`),
    SYS.has(p.from) || OUTSIDE.has(p.from) ? null : h("a", { role: "menuitem", href: agentHref(p.from), onclick: closeMenu }, `${p.from}'s page`));
  return h("div", { class: "msg-tools" },
    can("post") ? button("Reply", () => replyTo(p), { kind: "icon", size: "s", icon: "reply", title: "Reply" }) : null,
    button("Copy text", (e, b) => copyText(devBody(p)).then(() => flash(b)), { kind: "icon", size: "s", icon: "copy", title: "Copy text" }),
    button("Copy link", (e, b) => copyText(`${location.origin}${location.pathname}${location.search}#comms/${encodeURIComponent(p.th)}/${p.id}`).then(() => flash(b)), { kind: "icon", size: "s", icon: "link", title: "Copy link" }),
    h("span", { class: "rc-more" },
      h("button", { type: "button", class: "btn msg-moreb", "data-kind": "icon", "data-size": "s", "aria-haspopup": "menu", "aria-expanded": String(open), "aria-label": "More for this post", title: "More", onclick: (e, b) => {
        const was = menuFor === p.id; closeMenu();
        if (!was) { menuFor = p.id; b.setAttribute("aria-expanded", "true"); b.parentNode.append(menu()); }
      } }, icon("more")), open ? menu() : null));
}
function devReceipt(p, m) {
  const d = delivery(p, m); if (!d || m === "new") return null;
  return h("span", { class: "msg-r", "data-tone": d.tone, title: d.text, "aria-label": d.text }, RMARK[d.key]);
}
function devHead(p, showThread) {
  const woke = (S.woke[p.id] || []).filter(s => !p.men.includes(s.name) || !s.resumed), tos = p.men.filter(m => m !== "new");
  return h("div", { class: "msg-h" },
    agentRef(p.from, { copy: true }),
    tos.length ? h("span", { class: "msg-to", "aria-label": "to" }, "→") : null,
    tos.flatMap(m => [agentRef(m, { copy: true }), devReceipt(p, m)]),
    showThread ? threadRef(p.th) : null,
    p.reply_to ? h("span", { class: "msg-chip meta", title: `This answers post ${p.reply_to}` }, `↩ ${p.reply_to}`) : null,
    woke.length ? h("span", { class: "msg-chip meta" }, woke.map(s => (s.resumed ? "woke " : "started ") + s.name).join(", ")) : null,
    h("span", { class: "msg-end" }, h("time", { class: "meta num", datetime: p.at, title: p.at }, clock(p.t))));
}
function devMessage(p, opts) {
  const long = devBody(p).length > DEV_LONG, files = p.files || [], ws = opts.mark;
  // A word found past the cut would look like a false match: open the post so it shows.
  if (long && ws && ws.length && !OPEN.has(p.id)) {
    const lc = p.text.toLowerCase(), cut = mdCut(p.text, DEV_CUT).toLowerCase();
    if (ws.some(w => lc.includes(w)) && !ws.some(w => cut.includes(w))) OPEN.add(p.id);
  }
  const cut = el => { if (long && !OPEN.has(p.id) && !RAW.has(p.id)) el.dataset.cut = ""; return el; };
  return h("article", { class: "msg", "data-key": p.id, "data-who": SYS.has(p.from) ? "sys" : p.from === "person" || p.from === "box" ? "person" : null, "data-cont": opts.cont ? "" : null, "data-report": long ? "" : null },
    devHead(p, opts.showThread !== false),
    devTools(p, ws),
    cut(devBodyEl(p, ws)),
    files.length ? h("div", { class: "msg-files" }, files.map(f => attachment(p, f))) : null,
    long ? button(moreLabel(p), (e, b) => {
      if (OPEN.has(p.id)) OPEN.delete(p.id); else OPEN.add(p.id);
      b.closest(".msg").querySelector(".md").replaceWith(cut(devBodyEl(p, ws)));
      b.querySelector("span").textContent = moreLabel(p);
      b.setAttribute("aria-expanded", String(OPEN.has(p.id)));
    }, { kind: "quiet", size: "s", cls: "msg-more", icon: "chevron" }) : null);
}

/** A message on its way: {lid, text, files:[File], url per image, state: sending|sent|failed, err}. actions: {retry, edit, discard} */
export function localMessage(it, { retry, edit, discard }) {
  const failed = it.state === "failed";
  return h("article", { class: "msg", "data-key": "local:" + it.lid, "data-who": "me", "data-state": it.state, "aria-label": failed ? "Your message, not sent" : "Your message, sending" },
    h("div", { class: "msg-h" }, h("b", { class: "msg-me" }, "You"),
      it.to && !simpleOn() ? [h("span", { class: "msg-to" }, "to"), it.to === "new" ? h("span", { class: "muted" }, "a new agent") : agentRef(it.to)] : null,
      h("span", { class: "msg-end" }, h("time", { class: "meta num" }, clock(it.t)))),
    it.reply ? h("div", { class: "msg-q", "data-static": "" }, icon("reply"), h("b", null, who(it.reply.from)), h("span", null, short(mdPlain(displayText(it.reply)), 140))) : null,
    it.text.trim() ? h("div", { class: "msg-b" }, markdown(it.text)) : null,
    it.files.length ? h("div", { class: "msg-files" }, it.files.map(f => f.url
      ? h("span", { class: "msg-thumb" }, h("img", { src: f.url, alt: f.name }), h("span", null, f.name))
      : h("span", { class: "msg-file" }, h("b", null, ext(f.name)), h("span", null, h("span", { class: "msg-fn" }, f.name), h("span", { class: "meta" }, fsize(f.size)))))) : null,
    h("div", { class: "msg-rc", role: failed ? "alert" : "status" },
      failed ? [h("span", { "data-tone": "bad" }, h("b", { "aria-hidden": "true" }, "!"), `Not sent: ${it.err}.`),
        button("Retry", () => retry(it), { size: "s" }), button("Edit", () => edit(it), { size: "s", kind: "quiet" }), button("Discard", () => discard(it), { size: "s", kind: "quiet" })]
        : h("span", { "data-tone": "quiet" }, statusDot("sending"), it.state === "sent" ? "Sent" : it.files.length && it.uploading ? "Uploading…" : "Sending…")));
}
