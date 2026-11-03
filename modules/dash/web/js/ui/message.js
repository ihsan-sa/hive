// One board post, as a chat message: author, who it went to, time, the words, files, and (on your own messages) whether it
// has reached the agent, in words. Used by Comms and the Agent page.
// message(post, opts) returns the whole article. Its head (.msg-h) and receipt (.msg-rc) can be redrawn on their own with
// messageHead() and messageReceipt(), so a long list refreshes names and read marks without touching the bodies.
// localMessage(item) draws a message that hasn't reached the board yet: sending, or failed with Retry.
import { h } from "../core/dom.js";
import { S, can, jget, SYS, OUTSIDE } from "../core/store.js";
import { isAgent, isRunning, displayText, postSource } from "../core/model.js";
import { clock, stamp, now, short, mdPlain, ago, DAY, MIN } from "../core/format.js";
import { markdown, mdCut } from "./markdown.js";
import { agentRef, threadRef } from "./refs.js";
import { button, statusDot } from "./kit.js";
import { replyTo } from "./composer.js";

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
  const q = S.pending[m], queued = q && (q.ids || []).includes(p.id);
  const set = DELIV.sets[m], last = S.lastSess[m];
  if ((set && set.has(p.id)) || (!queued && last && last.start >= p.t - 2000) || answered(p, m)) return { key: "seen", tone: "good", text: `Seen by ${m}`, who: m };
  if (queued) return { key: "queued", tone: "info", text: `Waiting for ${m} to start`, who: m };
  if (isRunning(m)) return { key: "waiting", tone: "warn", text: `${m} sees this after its current step`, who: m };
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
  const show = ds.filter(d => last || d.key !== "seen");
  for (const d of show) el.append(h("span", { "data-tone": d.tone }, h("b", { "aria-hidden": "true" }, RMARK[d.key]), d.text));
  return el;
}

// ---- attachments: images as thumbnails, text files open in place, anything else downloads ----
// All open in one viewer window. Text is shown through textContent only, and svg is never rendered.
export const IMG = /\.(png|jpe?g|gif|webp)$/i;
const TXT = /\.(txt|md|log|json|jsonl|csv|tsv|py|js|mjs|ts|sh|c|h|cpp|v|sv|vh|yaml|yml|toml|ini|cfg|xml|html|css|go|rs|kicad_pcb|kicad_sch|net|sp|cir|spice|lef|def|sdc|tcl)$/i;
const fileUrl = (p, f) => `/files/${encodeURIComponent(p.id)}/${encodeURIComponent(f.name)}`;
export const fsize = n => (n == null ? "" : n >= 1048576 ? (n / 1048576).toFixed(1) + " MB" : n >= 1024 ? Math.round(n / 1024) + " kB" : n + " B");
const ext = name => (name.split(".").pop() || "file").slice(0, 5).toUpperCase();

function attachment(p, f) {
  const u = fileUrl(p, f);
  if (IMG.test(f.name)) return h("button", { type: "button", class: "msg-thumb", title: f.name + ", click to view", onclick: () => viewFile(p, f) },
    h("img", { src: u, alt: f.name, loading: "lazy" }), h("span", null, f.name));
  const txt = TXT.test(f.name) && (f.size == null || f.size < 2 * 1048576);
  const props = txt ? { type: "button", onclick: () => viewFile(p, f) } : { href: u, download: f.name };
  return h(txt ? "button" : "a", { class: "msg-file", title: txt ? "Click to read" : "Download", ...props },
    h("b", null, ext(f.name)),
    h("span", null, h("span", { class: "msg-fn" }, f.name), h("span", { class: "meta" }, fsize(f.size) + (txt ? " · read" : " · download"))));
}

function viewFile(p, f) {
  const all = (p.files || []).filter(x => IMG.test(x.name) || TXT.test(x.name));
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
    const pre = h("pre", null, "Loading…"); stage.replaceChildren(pre);
    try {
      const r = await fetch(u, { cache: "no-store" }); if (!r.ok) throw Error(r.status);
      let t = await r.text(); const cut = t.length > 400000; if (cut) t = t.slice(0, 400000);
      pre.textContent = t + (cut ? "\n\n… (cut here; open the original for the rest)" : "");
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
  if (j - i < 1 || j >= b.length) return null;   // nothing to fold, or the last block alone is too long to keep: show it whole
  const mid = b.slice(i, j).join("\n\n");
  return { head: b.slice(0, i).join("\n\n"), tail: b.slice(j).join("\n\n"), lines: mid.split("\n").filter(l => l.trim()).length, chars: mid.length };
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
function markBody(el, ws) {
  if (!ws || !ws.length) return el;
  const re = wordsRe(ws), walk = document.createTreeWalker(el, NodeFilter.SHOW_TEXT), found = [];
  while (walk.nextNode()) if (walk.currentNode.nodeValue.search(re) >= 0) found.push(walk.currentNode);
  for (const n of found) n.replaceWith(...markWords(n.nodeValue, ws));
  return el;
}

function body(p, ws) {
  const t = displayText(p), f = t.length > LONG && !OPEN.has(p.id) ? fold(t) : null;
  if (!f) return h("div", { class: "msg-b" }, markBody(markdown(t), ws));
  return h("div", { class: "msg-b", "data-folded": "" },
    markBody(markdown(f.head), ws),
    button(`Show the middle: ${f.lines} more lines`, (e, b) => { OPEN.add(p.id); b.closest(".msg-b").replaceWith(body(p, ws)); }, { kind: "quiet", size: "s", cls: "msg-more" }),
    markBody(markdown(f.tail), ws));
}

// ---- jumping to the post a reply answers. The view that lists posts says how. ----
let jumper = null;
export const onJump = fn => { jumper = fn; };
const who = n => (n === "person" ? "You" : n);

function quote(p) {
  const q = S.byId[p.reply_to];
  return h("button", { type: "button", class: "msg-q", title: q ? "Go to the message this answers" : `Answers post ${p.reply_to}, not loaded here`, onclick: () => jumper && jumper(p.reply_to) },
    h("b", null, q ? who(q.from) : "An earlier message"), h("span", null, q ? short(mdPlain(displayText(q)), 140) : p.reply_to));
}

/** The line above a message: author, who it went to, thread (outside a thread), who it woke, Reply, time. */
export function messageHead(p, { showThread = true } = {}) {
  if (!S.app.app) return devHead(p, showThread);
  const mine = OUTSIDE.has(p.from) && p.from === "person";
  const woke = (S.woke[p.id] || []).filter(s => !p.men.includes(s.name) || !s.resumed), tos = p.men.filter(m => m !== "new" && m !== p.from);
  const toYou = tos.length === 1 && tos[0] === "person";
  const src = mine ? postSource(p) : null, me = S.app.signer;
  // Someone else signed in to this Hive wrote it: their name, not "You". A name you can't tell apart goes on the badge.
  const other = src && src.how === "Web" && src.name && me && src.name !== me;
  return h("div", { class: "msg-h" },
    mine ? h("b", { class: "msg-me", title: other ? src.said : "you (person)" }, other ? src.name : "You") : agentRef(p.from, { copy: true }),
    src ? h("span", { class: "msg-src", title: src.said }, (src.name && src.name !== me && !other ? src.name + " · " : "") + src.how) : null,
    tos.length ? h("span", { class: "msg-to" }, toYou ? "to you" : "to") : null,
    toYou ? null : tos.flatMap(m => {
      const d = mine ? null : delivery(p, m);
      // Between agents only trouble is worth a word: a running one sees its posts after its current step anyway.
      return [m === "person" ? h("b", { class: "msg-me" }, "you") : agentRef(m, { copy: !mine }), d && d.key === "unseen" ? h("span", { class: "msg-r", "data-tone": d.tone, title: d.text }, "hasn't seen this") : null];
    }),
    showThread ? threadRef(p.th) : null,
    woke.length && !mine ? h("span", { class: "meta" }, woke.map(s => (s.resumed ? "woke " : "started ") + s.name).join(", ")) : null,
    h("span", { class: "msg-end" },
      S.app.app && can("post") ? button("Reply", () => replyTo(p), { kind: "quiet", size: "s", cls: "msg-reply", title: "Reply to this message; it goes to its author" }) : null,
      h("time", { class: "meta num", datetime: p.at, title: stamp(p.t) }, clock(p.t))));
}

/** opts: {showThread, mark: words to highlight, cont: same author as the post above, so the head folds away} */
export function message(p, opts = {}) {
  if (!S.app.app) return devMessage(p, opts);
  const files = p.files || [], ws = opts.mark;
  if (ws && ws.length && !OPEN.has(p.id) && displayText(p).length > LONG) {
    const lc = displayText(p).toLowerCase(), f = fold(displayText(p));
    if (f && ws.some(w => lc.includes(w)) && !ws.some(w => (f.head + f.tail).toLowerCase().includes(w))) OPEN.add(p.id);
  }
  const kind = SYS.has(p.from) ? "sys" : p.from === "person" ? "me" : OUTSIDE.has(p.from) ? "out" : "agent";
  return h("article", { class: "msg", "data-key": p.id, "data-who": kind, "data-cont": opts.cont ? "" : null, "aria-label": `${who(p.from)}, ${stamp(p.t)}` },
    messageHead(p, opts),
    p.reply_to ? quote(p) : null,
    body(p, ws),
    files.length ? h("div", { class: "msg-files" }, files.map(f => attachment(p, f))) : null,
    messageReceipt(p));
}

// ---- the developer route: a dense log, as it was. Arrows and marks in the head, long posts cut short. ----
const DEV_LONG = 700, DEV_CUT = 600;
const devText = p => (p.text.length > DEV_LONG && !OPEN.has(p.id) ? mdCut(p.text, DEV_CUT) : p.text);
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
    p.reply_to ? h("span", { class: "meta", title: `This answers post ${p.reply_to}` }, `↩ ${p.reply_to}`) : null,
    woke.length ? h("span", { class: "meta" }, woke.map(s => (s.resumed ? "woke " : "started ") + s.name).join(", ")) : null,
    h("span", { class: "msg-end" }, h("time", { class: "meta num", datetime: p.at, title: p.at }, clock(p.t))));
}
function devMessage(p, opts) {
  const long = p.text.length > DEV_LONG, files = p.files || [], ws = opts.mark;
  // A word found past the cut would look like a false match: open the post so it shows.
  if (long && ws && ws.length && !OPEN.has(p.id)) {
    const lc = p.text.toLowerCase(), cut = mdCut(p.text, DEV_CUT).toLowerCase();
    if (ws.some(w => lc.includes(w)) && !ws.some(w => cut.includes(w))) OPEN.add(p.id);
  }
  return h("article", { class: "msg", "data-key": p.id, "data-who": SYS.has(p.from) ? "sys" : p.from === "person" || p.from === "box" ? "person" : null },
    devHead(p, opts.showThread !== false),
    markBody(markdown(devText(p)), ws),
    files.length ? h("div", { class: "msg-files" }, files.map(f => attachment(p, f))) : null,
    long ? button(OPEN.has(p.id) ? "Show less" : `Show all ${p.text.length} characters`, (e, b) => {
      if (OPEN.has(p.id)) OPEN.delete(p.id); else OPEN.add(p.id);
      b.closest(".msg").querySelector(".md").replaceWith(markBody(markdown(devText(p)), ws));
      b.textContent = OPEN.has(p.id) ? "Show less" : `Show all ${p.text.length} characters`;
    }, { kind: "quiet", size: "s", cls: "msg-more" }) : null);
}

/** A message on its way: {lid, text, files:[File], url per image, state: sending|sent|failed, err}. actions: {retry, edit, discard} */
export function localMessage(it, { retry, edit, discard }) {
  const failed = it.state === "failed";
  return h("article", { class: "msg", "data-key": "local:" + it.lid, "data-who": "me", "data-state": it.state, "aria-label": failed ? "Your message, not sent" : "Your message, sending" },
    h("div", { class: "msg-h" }, h("b", { class: "msg-me" }, "You"),
      it.to ? [h("span", { class: "msg-to" }, "to"), it.to === "new" ? h("span", { class: "muted" }, "a new agent") : agentRef(it.to)] : null,
      h("span", { class: "msg-end" }, h("time", { class: "meta num" }, clock(it.t)))),
    it.reply ? h("div", { class: "msg-q", "data-static": "" }, h("b", null, who(it.reply.from)), h("span", null, short(mdPlain(displayText(it.reply)), 140))) : null,
    it.text.trim() ? h("div", { class: "msg-b" }, markdown(it.text)) : null,
    it.files.length ? h("div", { class: "msg-files" }, it.files.map(f => f.url
      ? h("span", { class: "msg-thumb" }, h("img", { src: f.url, alt: f.name }), h("span", null, f.name))
      : h("span", { class: "msg-file" }, h("b", null, ext(f.name)), h("span", null, h("span", { class: "msg-fn" }, f.name), h("span", { class: "meta" }, fsize(f.size)))))) : null,
    h("div", { class: "msg-rc", role: failed ? "alert" : "status" },
      failed ? [h("span", { "data-tone": "bad" }, h("b", { "aria-hidden": "true" }, "!"), `Not sent: ${it.err}.`),
        button("Retry", () => retry(it), { size: "s" }), button("Edit", () => edit(it), { size: "s", kind: "quiet" }), button("Discard", () => discard(it), { size: "s", kind: "quiet" })]
        : h("span", { "data-tone": "quiet" }, statusDot("sending"), it.state === "sent" ? "Sent" : it.files.length && it.uploading ? "Uploading…" : "Sending…")));
}
