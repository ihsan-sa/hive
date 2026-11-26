// One board post, as a chat message: author, who it went to, time, the words, files, and (on your own messages) whether it
// has reached the agent, in words. Used by Comms and the Agent page.
// message(post, opts) returns the whole article. Its head (.msg-h) and receipt (.msg-rc) can be redrawn on their own with
// messageHead() and messageReceipt(), so a long list refreshes names and read marks without touching the bodies.
// localMessage(item) draws a message that hasn't reached the board yet: sending, or failed with Retry.
import { h, copyText, tr } from "../core/dom.js";
import { S, can, jget, SYS, OUTSIDE, post as apiPost } from "../core/store.js";
import { simpleOn, hiveName } from "../core/simple.js";
import { isScript, isAgent, isWarden, isRunning, inConversation, parkedCarry, capFirst, agentLabel, displayText, typedText, editNote, chatText, savedFacts, postSource, btwParent, btwText, asksQuestion, threadState, threadChildren, threadTitle } from "../core/model.js";
import { focusById, focusName, focusOf } from "../core/foci.js";
import { clock, stamp, now, short, mdPlain, ago, DAY, MIN, fix, int } from "../core/format.js";
import { markdown, mdCut, filesNamed } from "./markdown.js";
import { artifactSeries, collectPages, inChat } from "../rich/artifact.js";
import { agentRef, threadRef, agentAvatar } from "./refs.js";
import { agentHref, threadHref, href } from "../core/router.js";
import { button, icon, statusDot, copyPress, scrim } from "./kit.js";
import { replyTo, quoteRef } from "./composer.js";
import { pickBar, picked } from "./pick.js";
import { wordButton, approvalLine, cardSig } from "./rootword.js";
import { voiceMemo, VOICE } from "./voicememo.js";
import { readAloud, stopReading, reading } from "./speak.js";
import { openPreview, openDoc, previewKey, kindOf, badgeOf, remember } from "./preview.js";
import { docMeta } from "./docmeta.js";
import { boxIds, boxEmbed, boxesOn, dropBoxLines } from "./pybox.js";

// ---- read marks ----
// Has this post reached the agent it names? Read: handed to it mid-run (the inbox hook's record, /api/delivered),
// or a session of it began after the post, whose prompt carries it. The hook writes "@<unix seconds>" after each batch it hands over,
// so the ids before such a token reached the agent at that time (DELIV.at).
const DELIV = { sets: {}, at: {}, sig: "" };
function deliveredAt(x) {
  const at = new Map(); let open = [];
  for (const w of x) { const t = /^@(\d+(?:\.\d+)?)$/.exec(w); if (t) { for (const id of open) at.set(id, +t[1] * 1e3); open = []; } else open.push(w); }
  return at;
}

/** Fetch /api/delivered. Resolves true when the marks changed: call it from a view's poll, every 3 s, and update if true. */
export async function pollDelivered() {
  try {
    const v = await jget("/api/delivered"), sig = JSON.stringify(v);
    if (sig === DELIV.sig) return false;
    DELIV.sig = sig; DELIV.sets = Object.fromEntries(Object.entries(v).map(([k, x]) => [k, new Set(x)]));
    DELIV.at = Object.fromEntries(Object.entries(v).map(([k, x]) => [k, deliveredAt(x)]));
    return true;
  } catch (e) { return false; }
}
/** What read marks currently depend on, as a string to compare. */
export const marksSig = () => DELIV.sig + JSON.stringify((S.live && S.live.pending) || []) + cardSig();   // and what the approval lines say (rootword.js approvalLine)

/** Where a post stands with one recipient: {key: seen|interrupt|waiting|queued|unseen, tone, text}. null when it doesn't apply.
 *  seen says when it reached the agent when that is known: the hook's time, or the start of the session that had it. */
export function delivery(p, m) {
  if (m === "new") {
    const st = (S.woke[p.id] || [])[0];
    if (st) return { key: "seen", tone: "good", text: S.app && S.app.app ? "An agent started on it" : `${st.name} started on it`, who: st.name };   // the app look names no code (E10)
    // E83: a reply from an agent after the question is proof one started on it, even when no start was recorded for it (an old or imported thread)
    const rep = (S.threads.get(p.th)?.posts || []).find(x => x.t > p.t && x.from !== p.from && !OUTSIDE.has(x.from) && !isScript(x.from));
    if (rep) return { key: "seen", tone: "good", text: S.app && S.app.app ? "An agent started on it" : `${rep.from} started on it`, who: rep.from };
    return now() - p.t < 3 * MIN ? { key: "queued", tone: "info", text: "Starting an agent…" } : { key: "unseen", tone: "bad", text: "No agent has started on this" };
  }
  if (!isAgent(m) || now() - p.t > 3 * DAY || p.from === m) return null;
  // A post to the warden starts a fresh warden run (w + 4 hex), and one to a dead agent starts its heir: that run has it.
  const took = (S.woke[p.id] || []).find(s => s.name !== m && !p.men.includes(s.name) && (s.heirOf === m || m === "warden" && isWarden(s.name) && s.name !== "rootwarden"));
  if (took) return { key: "seen", tone: "good", text: S.app && S.app.app ? "Read" : `${took.name} took it for ${m}`, who: took.name };   // E203i: the app look names no code ("a5c5c took it for a3d4e" after carrying on a stop)
  const q = S.pending[m], queued = q && (q.ids || []).includes(p.id);
  const set = DELIV.sets[m], last = S.lastSess[m];
  const sess = !queued && last && last.start >= p.t - 2000;
  if ((set && set.has(p.id)) || sess || answered(p, m)) {
    const t = (DELIV.at[m] && DELIV.at[m].get(p.id)) || (sess ? Math.max(p.t, last.start) : 0);
    return { key: "seen", tone: "good", text: t ? `Reached ${m} · ${clock(t)}` : `Reached ${m}`, who: m, t };
  }
  // The Interrupt button's post: the starter stops a long command the agent is waiting on, so it reads this at once.
  if (p.interrupt && isRunning(m)) return { key: "interrupt", tone: "info", text: `Interrupt sent · asking ${m} to stop its step`, who: m };
  // A running agent gets the post from its hook after its current step, even while the starter also holds it for the next start.
  if (isRunning(m)) return { key: "waiting", tone: "quiet", text: `Sent · reaches ${m} after its current step`, who: m };
  // held for a run stopped at a usage limit: it reads this when that run carries on, which can be days away (a model's own share), so the receipt
  // names that time, from the function the line over the box uses (core/model.js parkedCarry, HA-16); "to start" read as seconds
  if (queued) { const co = parkedCarry(m), at = co && !co.room ? co.at : null;
    return { key: "queued", tone: "info", text: co && !co.room ? (at ? `Waiting for ${m} · it reads this after ${stamp(at)}` : `Waiting for ${m} · it reads this when ${m} is free`) : `Waiting for ${m} to start`, who: m, start: true, at, held: !!co && !co.room }; }
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
const RMARK = { seen: "✓", interrupt: "!", waiting: "…", queued: "…", unseen: "○" };

// The person's newest post in each thread carries its receipt even once seen; older ones only while not seen.
let lastMine = { ver: -1, by: {} };
function isLastMine(p) {
  if (lastMine.ver !== S.ver) { lastMine = { ver: S.ver, by: {} }; for (const x of S.posts) if (OUTSIDE.has(x.from) && !x.retry_of) lastMine.by[x.th] = x.id; }   // a Retry draws no message of its own, so the receipt stays where it was
  return lastMine.by[p.th] === p.id;
}
/** The receipt under your own message: one status line, in words. Always returns the element, empty when there's nothing to say.
 *  The full look (R7): the state that needs you most, for the recipient it concerns: an interrupt reaching its agent, then one
 *  that hasn't reached, waiting for a start, queued behind a step, and last Read by. Sent and Read show on your newest message,
 *  on every one with ?receipts=1. */
const RECEIPTS = () => /[?&]receipts=1\b/.test(location.search + location.hash);
const RANK = { interrupt: 0, unseen: 1, queued: 2, waiting: 3, seen: 4 };
const names = ms => ms.length < 3 ? ms.join(" and ") : ms.slice(0, -1).join(", ") + " and " + ms.at(-1);
export function messageReceipt(p) {
  const el = h("div", { class: "msg-rc msg-st", role: "status" });
  if (!S.app.app || !OUTSIDE.has(p.from) || now() - p.t > 3 * DAY) return el;
  const ds = p.men.map(m => delivery(p, m)).filter(Boolean), last = isLastMine(p), all = RECEIPTS();
  if (simpleOn()) {  // no names and no machinery: one word, on your latest message
    if ((last || all) && ds.length) el.append(h("span", { "data-tone": "quiet" }, ds.some(d => d.key === "seen") ? "Seen" : "Sent"));
    return el;
  }
  if (p.from !== "person") {   // a message from outside (Slack, mail) keeps its per-recipient words
    for (const d of ds.filter(d => last || all || d.key !== "seen")) el.append(h("span", { "data-tone": d.tone }, h("b", { "aria-hidden": "true" }, RMARK[d.key]), d.text));
    return el;
  }
  if (!ds.length) { if (last || all) el.append(icon("check"), h("span", null, "Sent")); return el; }
  const top = ds.reduce((a, d) => RANK[d.key] < RANK[a.key] ? d : a), same = ds.filter(d => d.key === top.key && d.who).map(d => d.who);
  const app = S.app && S.app.app, X = names((same.length ? same : [top.who || "the agent"]).map(n => app && isAgent(n) ? inConversation(p.th, () => agentLabel(n)) : n));   // E10: no agent code in the app look
  el.dataset.key = top.key;
  // A Stop and send has one status line, this one, and says "stopped" only when the starter's log says a step was stopped.
  const cut = p.interrupt ? S.cut[p.id] : null, stopped = !!cut && cut.did === "stopped the running command";
  if (top.key === "interrupt") { el.dataset.tone = "info"; el.dataset.int = "";
    el.append(icon("stop"), h("span", null, !cut ? `Sent · asking ${X} to stop its step` : stopped ? `${capFirst(X)} stopped its step · it reads this now` : `Sent · ${X} reads it when its current step ends`)); }
  else if (top.key === "unseen") { el.dataset.tone = "bad"; el.append(h("span", { class: "st", "data-tone": "bad", "aria-hidden": "true" }), h("span", null, top.who ? `Hasn't reached ${X}` : top.text)); }
  else if (top.key === "queued") {   // a held message names its agent as the rest do (X: no code in the app look) and, when its run is paused at a limit, when it reads it (HA-16)
    const at = same.length < 2 && top.at, words = !top.start ? top.text : top.held ? (at ? `Waiting for ${X} · it reads this after ${stamp(at)}` : `Waiting for ${X} · it reads this when ${X} ${same.length > 1 ? "are" : "is"} free`) : `Waiting for ${X} to start`;
    el.dataset.tone = "warn"; if (at) el.dataset.at = String(at); el.append(h("span", { class: "st", "data-tone": "warn", "aria-hidden": "true" }), h("span", null, words)); }
  else if (top.key === "waiting") { el.dataset.tone = "warn"; el.append(h("span", { class: "st", "data-tone": "warn", "aria-hidden": "true" }), h("span", null, `Queued · ${X} reads it after its current step`)); }
  else if (stopped) {
    const t = cut.t || 0; el.dataset.int = "";
    el.append(icon("stop"), h("span", null, `Read · ${X} stopped its step${t ? " at " + clock(t) : ""}`));
  } else if (last || all) {
    const t = same.length === 1 && top.t;
    el.append(icon("check"), h("span", null, /started on it|took it for/.test(top.text) ? top.text : app ? "Read" : `Read by ${X}`), t ? h("span", { class: "msg-st-t" }, "· " + clock(t)) : "");   // append(null) would write "null"
  }
  return el;
}

// ---- attachments: images as thumbnails, everything else as a chip; a click opens the file in the preview panel (preview.js) ----
export const IMG = /\.(png|jpe?g|gif|webp)$/i;
export const fileUrl = (p, f) => `/files/${encodeURIComponent(p.id)}/${encodeURIComponent(f.name)}`;
export const PDF = /\.pdf$/i;
export const VID = /\.(mp4|webm|mov|m4v)$/i;
export const PAGE = /\.html?$/i;
export const pageUrl = (p, f) => fileUrl(p, f) + "?view=1";
export const fsize = n => (n == null ? "" : n >= 1048576 ? fix((n / 1048576), 1) + " MB" : n >= 1024 ? Math.round(n / 1024) + " kB" : n + " B");
export const TYPE = { svg: "Picture", video: "Video", audio: "Audio", pdf: "PDF", html: "Web page", md: "Document", table: "Table", notebook: "Notebook", json: "Data", xlsx: "Spreadsheet", docx: "Document", pptx: "Slides", diff: "Changes", mermaid: "Diagram", code: "Code", text: "Text" };
const OPEN_VERB = { video: "Play", audio: "Play" };

// An image that won't load (gone, or not copied here) shows as a tile that says so, not the browser's broken icon. error doesn't bubble.
if (typeof document !== "undefined") document.addEventListener("error", e => {
  const t = e.target && e.target.tagName === "IMG" && e.target.closest(".msg-thumb"); if (t) t.dataset.broken = "";
}, true);
const items = p => (p.files || []).map(f => remember({ id: p.id, name: f.name, size: f.size, from: p.from, t: p.t }));
const viewFile = (p, f) => openPreview(items(p), Math.max(0, (p.files || []).findIndex(x => x.name === f.name)));
function attachment(p, f) {
  const key = p.id + "/" + f.name, on = previewKey() === key ? "" : null;
  if (IMG.test(f.name)) return h("button", { type: "button", class: "msg-thumb", title: f.name + ", click to view", "data-pv-key": key, "data-pv-on": on, onclick: () => viewFile(p, f) },
    h("img", { src: fileUrl(p, f), alt: f.name, loading: "lazy" }), h("span", null, f.name));
  const k = kindOf(f.name);
  // A voice memo or audio file plays in place: a waveform card, with the file's own panel one click away on its name.
  if (VOICE.test(f.name)) return voiceMemo(fileUrl(p, f) + "?view=1", f.name, fsize(f.size), f.size, { key, on, open: () => viewFile(p, f) });
  // A card, the way Claude shows a file it made: a page with the type on it, the name, type and size, and Open.
  // A file the panel can't show (a zip, a binary) downloads straight away instead (the person, 2026-11-07).
  const body = [h("b", { "aria-hidden": "true" }, badgeOf(f.name)),
    h("span", null, h("span", { class: "msg-fn" }, f.name), h("span", { class: "meta" }, [TYPE[k] || badgeOf(f.name) + " file", fsize(f.size)].filter(Boolean).join(" · ")))];
  if (k === "other") return h("a", { class: "msg-file", href: fileUrl(p, f), download: f.name, title: "Download " + f.name, "data-kind": k },
    ...body, h("span", { class: "msg-open" }, icon("download"), "Download"));
  return h("button", { type: "button", class: "msg-file", title: "Open " + f.name + " beside the conversation", "data-pv-key": key, "data-pv-on": on, "data-kind": k, onclick: () => viewFile(p, f) },
    ...body, h("span", { class: "msg-open" }, OPEN_VERB[k] || "Open"));
}

// Ink the person sent (ink/snap.js): ink-<sheet>.json (the strokes the page redraws) and ink-<sheet>.png (the picture the agent got)
// show as one quiet line under the message, however many sheets the send touched: "Ink on 2 messages · Show 1 2", each number going
// to the ink where it lies and the small picture beside it opening what the agent saw. The "✎ …" lines the post carries for the
// agent don't show in the bubble (inkText), so a send of ink alone is that line and nothing else (person, thread 112515205dbf).
const INKF = /^ink-(.+)\.(json|png)$/, INKL = /^✎ .*$\n?/gm;
const inkFiles = (p, files) => p.from === "person" && files.some(f => INKF.test(f.name));
const inkText = t => t.replace(INKL, "").replace(/\s+$/, "");
function inkLine(p, ink) {
  const kinds = [...ink.keys()].map(sid => /^m-/.test(sid) ? "m" : sid === "board" ? "b" : "s");
  const n = k => kinds.filter(x => x === k).length;
  const what = [n("m") && (n("m") > 1 ? `${n("m")} messages` : "a message"), n("b") && "the whiteboard", n("s") && (n("s") > 1 ? `${n("s")} writing spaces` : "a writing space")].filter(Boolean);
  const one = ink.size === 1;
  const go = sid => import("../ink/sheet.js").then(m => m.goSheet(p.th, sid, p.id)).catch(() => {});
  return h("div", { class: "ink-line", "data-sent": "", "data-key": "ink" }, icon("pen"),
    h("span", { class: "ink-what", title: "Ink on " + (what.length > 1 ? what.slice(0, -1).join(", ") + " and " + what.at(-1) : what[0]) }, one ? "Ink on " + what[0] : `Ink in ${ink.size} places`),
    one ? null : h("span", { class: "ink-sep", "aria-hidden": "true" }, "·", h("span", null, " Show")),
    [...ink].map(([sid, o], i) => h("span", { class: "ink-one" },
      h("button", { type: "button", class: "ink-go", title: "Show it where it was drawn", onclick: () => go(sid) }, one ? "Show where" : String(i + 1)),
      o.png ? h("button", { type: "button", class: "ink-pic", title: "The picture the agent got", "aria-label": `The picture the agent got${one ? "" : " (" + (i + 1) + ")"}`, onclick: () => viewFile(p, o.png) }, icon("file")) : null)));
}
function fileRow(p, files) {
  const ink = new Map(), rest = [];
  for (const f of files) { const m = INKF.exec(f.name); if (m && p.from === "person") { const o = ink.get(m[1]) || {}; o[m[2]] = f; ink.set(m[1], o); } else rest.push(f); }
  return [ink.size ? inkLine(p, ink) : null, ...rest.map(f => attachment(p, f))];
}

// A library document linked in a post ([title](#docs/<id>)) also gets a card under it, like an attached file: its revision and
// how many notes wait, and a click opens it beside the conversation to mark up (ui/preview.js openDoc; design ~/designs/doc-side).
const DOCRX = /\[([^\]\n]+)\]\(#docs\/([\w.-]+)\)/g;
function docCards(p) {
  const seen = new Set(), out = [];
  for (const [, name, id] of (p.text || "").matchAll(DOCRX)) {
    if (seen.has(id)) continue; seen.add(id);
    const m = docMeta(id), key = "doc:" + id, title = (m && m.title) || name;
    const st = m && (m.drafts ? [`${m.drafts} note${m.drafts > 1 ? "s" : ""} to send`, "info"] : m.open ? [`${m.open} note${m.open > 1 ? "s" : ""} with the agent`, null] : null);
    out.push(h("button", { type: "button", class: "msg-file", "data-kind": "doc", "data-key": key, "data-pv-key": key, "data-pv-on": previewKey() === key ? "" : null,
        title: `Open ${title} beside the conversation, to mark it up`, onclick: () => openDoc(id, title) },
      h("b", { "aria-hidden": "true" }, "DOC"),
      h("span", null, h("span", { class: "msg-fn" }, title),
        h("span", { class: "meta" }, ["Document", m && m.rev && "rev " + m.rev, m && m.pages && m.pages + (m.pages > 1 ? " pages" : " page")].filter(Boolean).join(" · ")),
        st ? h("span", { class: "msg-st", "data-tone": st[1] }, st[0]) : null),
      h("span", { class: "msg-open" }, "Open")));
  }
  return out;
}

// ---- long posts: the start and the end show, the middle folds, so a report's conclusion is never the hidden part ----
const LONG = 2400, HEAD = 900, TAIL = 700, OPEN = new Set();
// A chart, plot, table or drawing fence shows as one picture, so its source doesn't count toward the length that folds a post (E181).
const DRAWN = /^([ \t]*(`{3,}|~{3,})[ \t]*(?:chart|plot|csv|svg|mermaid)\b[^\n]*\n)[\s\S]*?(^[ \t]*\2[`~]*[ \t]*$)/gim;
const readLen = t => t.replace(DRAWN, "$1$3").length;
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
  while (i < j && a < HEAD) a += readLen(b[i++]);
  while (j > i && z + readLen(b[j - 1]) < TAIL) z += readLen(b[--j]);
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

// A post's markdown, where a bare file name links to the file its conversation means. Rebuilt when the thread grows.
const NAMED = new Map();
function mdIn(t, p) {
  const d = p && S.threads.get(p.th); if (!d) return markdown(t, null, !!p);
  let c = NAMED.get(p.th);
  if (!c || c.n !== d.posts.length) NAMED.set(p.th, c = { n: d.posts.length, files: filesNamed(d.posts) });
  return inChat(p.th, p.id, () => markdown(t, c.files, true));
}
// The pages the chat view shows (posts: what it draws, hidden versions left out), as versions of each other. Parsed again only when they change.
const PAGES = new Map();
/** A page the person edited by hand (E43, dash.py page_edit_post): drawn as that page's newest version, asking nobody. */
export const pageEdit = p => (p.tags || []).includes("page:edit");
export function chatPages(th, posts) {
  const items = posts.filter(p => !p.ev && (p.from !== "person" || pageEdit(p)) && /(`{3,}|~{3,})\s*html/i.test(p.text || "")).map(p => [p.id, chatText(p)]);
  const sig = items.map(([id, t]) => id + ":" + t.length).join(",");
  if (PAGES.get(th) === sig) return;
  PAGES.set(th, sig); if (PAGES.size > 50) PAGES.delete(PAGES.keys().next().value);
  artifactSeries(th, collectPages(t => markdown(t, null, true), items));
}

// Fold parsed blocks, so fences, footnotes and inline formatting remain intact.
// Native details keep their children (and selection); the owned body survives morphs.
const SECTIONS = new Map();
function report(t, id, ws) {
  const md = mdIn(t, S.byId[id]), nodes = [...md.children];
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
const flash = (b, text) => copyText(text).then(() => { const was = b.textContent; b.dataset.ok = ""; b.textContent = tr("Copied"); setTimeout(() => { delete b.dataset.ok; b.textContent = was; }, 1400); }, () => {});
function closeMenu() {
  document.querySelector(".rc-menu")?.remove(); document.querySelector(".rc-more>.scrim")?.remove();
  document.querySelector('.rc-moreb[aria-expanded="true"], .msg-moreb[aria-expanded="true"]')?.setAttribute("aria-expanded", "false");
  menuFor = null;
}
addEventListener("click", e => { if (menuFor && !e.target.closest?.(".rc-more,dialog.fb")) closeMenu(); }, true);
// Capture, so Escape closes only this menu and doesn't also cancel a reply or close the conversation's side panel.
addEventListener("keydown", e => { if (e.key === "Escape" && menuFor && !document.querySelector("dialog[open]")) { e.stopPropagation(); closeMenu(); } }, true);
const moreMenu = (p, ws) => h("div", { class: "rc-menu menu", role: "menu", "data-side": "bottom-end" },
  h("button", { type: "button", class: "menu-i", role: "menuitem", onclick: () => { copyText(displayText(p)); closeMenu(); } }, "Copy as Markdown"),
  h("button", { type: "button", class: "menu-i", role: "menuitem", onclick: () => { copyText(mdPlain(displayText(p))); closeMenu(); } }, "Copy as plain text"),
  h("button", { type: "button", class: "menu-i", role: "menuitem", onclick: (e, b) => {
    const was = b.closest(".msg")?.querySelector(".msg-b"); closeMenu();
    if (SOURCE.has(p.id)) SOURCE.delete(p.id); else SOURCE.add(p.id);
    was?.replaceWith(body(p, ws));
  } }, SOURCE.has(p.id) ? "Back to the post" : "View source"));
function more(p, ws) {
  const open = menuFor === p.id;
  return h("span", { class: "rc-more pop" },
    h("button", { type: "button", class: "rc-t rc-moreb", "aria-haspopup": "menu", "aria-expanded": String(open), "aria-label": "More for this message", onclick: (e, b) => {
      const was = menuFor === p.id;
      closeMenu();
      if (!was) { menuFor = p.id; b.setAttribute("aria-expanded", "true"); b.parentNode.append(scrim(closeMenu), moreMenu(p, ws)); }
    } }, "⋯"),
    open ? [scrim(closeMenu), moreMenu(p, ws)] : null);
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
  const t = chatText(p);
  if (readLen(t) > LONG) { const r = report(t, p.id, ws); if (r) return r; }
  if (!t.trim()) return (p.files || []).length ? null : h("div", { class: "msg-b" }, h("p", { class: "meta" }, "No text"));
  // A post with footnotes shows whole: head and tail parse apart, so a note hidden in the middle leaves its mark as literal text and the Sources list renumbers (E59).
  const f = readLen(t) > LONG && !OPEN.has(p.id) && !/\[\^[^\]\s]+\]/.test(t) ? fold(t) : null;
  if (!f) return h("div", { class: "msg-b" }, markBody(mdIn(t, p), ws));
  return h("div", { class: "msg-b", "data-folded": "" },
    markBody(mdIn(f.head, p), ws),
    button(`Show the middle: ${f.lines > 2 || f.words == null ? f.lines + " more lines" : int(f.words) + " more words"}`, (e, b) => { OPEN.add(p.id); b.closest(".msg-b").replaceWith(body(p, ws)); }, { kind: "quiet", size: "s", cls: "msg-more" }),
    markBody(mdIn(f.tail, p), ws));
}

// ---- jumping to the post a reply answers. The view that lists posts says how. ----
let jumper = null;
export const onJump = fn => { jumper = fn; };
/** Open post id in its conversation, highlighted (Chat says how; before Chat has drawn, it opens the conversation first). */
export const jump = id => { if (jumper) jumper(id); else location.hash = "#comms"; };
const who = n => (n === "person" ? "You" : simpleOn() ? hiveName() : agentLabel(n) === n ? n : capFirst(agentLabel(n)));   // a code stays as written

// A post passed on from another thread (a relay, or @thread in the text) says where it came from; the original says where its copies went.
let copyFor = [], copies = new Map();
function copiesOf(p) {
  if (copyFor[0] !== S.posts || copyFor[1] !== S.posts.length) { copyFor = [S.posts, S.posts.length]; copies = new Map(); for (const q of S.posts) if (q.relay_of) { const l = copies.get(q.relay_of); if (l) l.push(q); else copies.set(q.relay_of, [q]); } }
  return copies.get(p.id) || [];
}
export function relayLine(p) {
  const out = [];
  if (p.relay_of) { const o = S.byId[p.relay_of], m = /in thread (\S+) \(/.exec(String(p.text || "")), from = (o && o.th) || (m && m[1]); out.push(h("span", { class: "msg-relay" }, icon("reply"), `Passed on${p.relay_by ? " by " + p.relay_by : ""} from `, from ? threadRef(from) : "another thread"));
    // R36c: the relayer's why is its own line, in the relayer's name, never inside the quoted post (which is the original author's).
    if (p.relay_why) out.push(h("span", { class: "msg-relay msg-relay-why" }, `${p.relay_by || "The relayer"} says: ${p.relay_why}`)); }
  const cs = copiesOf(p); if (cs.length) out.push(h("span", { class: "msg-relay" }, "Passed on to ", ...cs.flatMap((c, i) => (i ? [", ", threadRef(c.th)] : [threadRef(c.th)]))));
  for (const id of p.foci || []) { const f = focusById(id); out.push(h("span", { class: "msg-relay" }, "For the focus ", f ? focusName(f) : id)); }
  return out.length ? h("div", { class: "msg-relays" }, ...out) : null;
}
// R8: the post's tags (hive post --tag, plus the thread's focus, the agents and threads it names), one small chip each, in plain words: the focus's name,
// the agent's name, the conversation's title. A chip opens the focus page, the agent's own thread (its page when it has none) or the conversation.
// A focus this page doesn't know, or a kind it can't name, is left off: no raw ids on a chip. The simple view has none.
// The conversation's own focus is left off too: the header already says it, and every post in it carries it (the person, 11090228efdc).
export function tagChips(p) {
  if (!(p.tags || []).length || simpleOn()) return null;
  const chips = [];
  for (const t of p.tags) {
    const i = String(t).indexOf(":"), kind = String(t).slice(0, i).toLowerCase(), id = String(t).slice(i + 1);
    if (i < 1 || !id) continue;
    if (kind === "focus") { const f = focusById(id.toLowerCase()); if (f && f.id !== focusOf(p.th)) chips.push([kind, focusName(f), href("focus", f.id), `Open the focus ${focusName(f)}`]); }
    else if (kind === "agent") { const app = S.app && S.app.app; if (!(app && p.from === "person" && (p.men || []).includes(id))) chips.push([kind, who(id), agentHref(id), `Open ${app ? agentLabel(id) : id}`]); }   // the agent a person's own message went to needs no chip under it (E10)
    else if (kind === "thread") { if (id !== p.th) chips.push([kind, threadTitle(id), threadHref(id), `Open the conversation ${threadTitle(id)}`]); }
  }
  return chips.length ? h("div", { class: "msg-tags", "aria-label": "Tags" }, chips.map(([kind, label, to, title]) => h("a", { class: "msg-tag", "data-kind": kind, href: to, title }, h("span", null, label)))) : null;
}
function quote(p) {
  const q = S.byId[p.reply_to];
  return h("button", { type: "button", class: "msg-q", title: q ? "Go to the message this answers" : `Answers post ${p.reply_to}, not loaded here`, onclick: () => jumper && jumper(p.reply_to) },
    icon("reply"), h("b", null, q ? who(q.from) : "An earlier message"), h("span", null, q ? short(mdPlain(displayText(q)), 140) : p.reply_to));
}

// Copy the message's text. The tick shows for a moment, then the icon comes back.
function copyBtn(p) {
  return button("Copy", (e, b) => copyPress(b, displayText(p)), { kind: "icon", size: "s", icon: "copy", cls: "msg-copy" });
}

// Between agents only trouble is worth a word (a running one sees its posts after its current step anyway), and it says who (the person, 11041326b458).
function unseenNote(ms) {
  if (!ms.length) return null;
  const said = ms.map(agentLabel), joined = said.length === 1 ? said[0] : said.slice(0, -1).join(", ") + " and " + said.at(-1), names = said[0] === ms[0] ? joined : capFirst(joined);   // a code stays as written
  return h("span", { class: "msg-r", "data-tone": "bad", title: said.map(m => `Hasn't reached ${m}`).join("\n") }, `${names} ${ms.length === 1 ? "hasn't" : "haven't"} seen this`);
}
/** The line above a message: author, who it went to, thread (outside a thread), who it woke, Reply, time. */
export function messageHead(p, { showThread = true, mark, asks } = {}) {
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
    mine ? h("b", { class: "msg-me", title: other ? src.said : "you (person)" }, other ? src.name : "You") : p.stopped && p.from === "dash" ? (typeof p.stopped === "string" && p.stopped ? agentRef(p.stopped, { dot: false, cap: true }) : h("b", null, hiveName())) : agentRef(p.from, { dot: false, cap: true }),   // E203c: the Stop notice is the Hive's word, not "Dash"; E203i: a kept reply is the stopped agent's, as stoppedMark draws it (comms.js refreshHead redraws this head alone); §10.2: the consumer head has no dot
    src ? h("span", { class: "msg-src", title: src.said }, (src.name && src.name !== me && !other ? src.name + " · " : "") + src.how) : null,
    tos.length ? h("span", { class: "msg-to" }, toYou ? "to you" : "to") : null,
    toYou ? null : tos.map(m => m === "person" ? h("b", { class: "msg-me" }, "you") : agentRef(m, { dot: false })),
    toYou ? null : unseenNote(mine ? [] : tos.filter(m => delivery(p, m)?.key === "unseen")),
    showThread ? threadRef(p.th) : null,
    woke.length && !mine ? h("span", { class: "meta" }, woke.map(s => (s.resumed ? "woke " : "started ") + agentLabel(s.name)).join(", ")) : null,
    h("time", { class: "meta num msg-time", datetime: p.at, title: stamp(p.t) }, clock(p.t)),
    asks ? h("span", { class: "msg-mark" }, "Waits on you") : null);   // the last thing on the head line (the Nov 8 handoff §8.3)
}

/** Under the message, shown on hover or focus: Reply, Copy, ⋯ and the time. Own messages carry the To in Reply's title. */
function messageActions(p, mark) {
  const mine = p.from === "person", tos = p.men.filter(m => m !== "new" && m !== p.from);
  return h("div", { class: "msg-end" },
    can("post") ? button("Reply", () => replyTo(p), { kind: "icon", size: "s", icon: "reply", cls: "msg-reply", title: mine && tos.length ? `Reply · this went to ${tos.map(agentLabel).join(", ")}` : "Reply to this message; it goes to its author" }) : null,
    copyBtn(p),
    more(p, mark),
    h("time", { class: "meta num", datetime: p.at, title: stamp(p.t) }, (mine && tos.length ? `to ${tos.map(agentLabel).join(", ")} · ` : "") + clock(p.t)));
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
// The transcript the phone module posts at hang-up: one closed row, phone icon and "Phone call", the whole transcript when opened.
function phoneCall(p) {
  const m = p.from === "phone" && String(p.text || "").match(/^Phone call, (about \d+ min)[^\n]*\. Transcript:\n+([\s\S]*)$/);
  if (!m) return null;
  const lines = m[2].split("\n").filter(Boolean);
  return h("article", { class: "msg msg-call", "data-key": p.id, "data-who": "sys", "aria-label": `Phone call, ${stamp(p.t)}` },
    h("details", { class: "msg-call-d", open: OPEN.has(p.id) ? "" : null, ontoggle: (e, d) => { if (d.open) OPEN.add(p.id); else OPEN.delete(p.id); } },
      h("summary", null, icon("phone"), h("b", null, "Phone call"), h("span", { class: "meta" }, ` · ${m[1]} · ${lines.length} lines · ${clock(p.t)}`)),
      h("div", { class: "msg-call-t" }, lines.map(l => h("p", { class: l.startsWith("(") ? "meta" : null }, l)))));
}
// E203e (GAPS row 22, the E203c reader): the person's Stop leaves one quiet line under the reply, as Claude.ai's "Stopped" does, not a
// message from "Your Hive" with an avatar and a head. Its steps fold under it as a run's do; the conversation's header says it too.
function stoppedMark(p, opts = {}) {
  // E203k: the same row under it as under any reply: the versions' arrows, and while it is the last reply, Retry
  STOP_OPTS.set(p.id, opts);
  const said = stoppedSaidText(p), kept = said ? { ...p, text: said, disp: said } : null;   // Copy takes what it had written, not the notice
  const acts = versionBar(p, opts, kept);
  // the line says the time only where no head above it does (nothing was written yet)
  const line = h("p", { class: "msg-stop-l meta" }, icon("stop"), h("span", null, displayText(p)), said ? null : h("time", { class: "num", datetime: p.at, title: stamp(p.t) }, " · " + clock(p.t)));
  if (!said) return h("article", { class: "msg msg-stopped", tabindex: "-1", "data-key": p.id, "data-who": "sys", "aria-label": `You stopped it, ${stamp(p.t)}` },
    h("div", { class: "msg-c" }, line, ranFold(p), acts));
  // E203i (the E203g reader): what it had started writing stays in place as the agent's answer, in its own type, with the stopped line
  // under it, as Claude.ai and ChatGPT keep a half-written reply; not a grey quote after the line.
  const name = typeof p.stopped === "string" && p.stopped ? p.stopped : null;
  return h("article", { class: "msg msg-stopped msg-stopped-said", tabindex: "-1", "data-key": p.id, "data-who": "agent", "data-name": name || "", "aria-label": `Stopped reply, ${stamp(p.t)}` },
    name ? agentAvatar(name) : msgAvatar(p, "sys"),
    h("div", { class: "msg-c" },
      h("div", { class: "msg-h" }, simpleOn() || !name ? h("b", null, hiveName()) : agentRef(name, { dot: false, cap: true }),
        h("time", { class: "meta num msg-time", datetime: p.at, title: stamp(p.t) }, clock(p.t))),
      h("div", { class: "msg-b msg-stop-said" }, markBody(mdIn(said, p))),
      line, ranFold(p), acts),
    simpleOn() || opts.canRetry ? null : h("div", { class: "msg-tools", role: "toolbar", "aria-label": "Message actions" }, copyBtn(kept)));   // the last reply's Copy is in the row under it
}
const STOP_OPTS = new Map();   // how each stop was last drawn (Retry, versions), for the redraw when its kept reply arrives
// E203g: what it had started writing when you stopped it (kill_run keeps it with the steps, /api/ran returns it as said).
function stoppedSaidText(p) { const e = RAN.get(p.id), t = e && e.v && e.v.said; return typeof t === "string" && t.trim() ? t : ""; }
export { approvalLine };
export function message(p, opts = {}) {
  if (!S.app.app) return devMessage(p, opts);
  return opts.showThread === false ? inConversation(p.th, () => appMessage(p, opts)) : appMessage(p, opts);   // read in its own conversation, it names agents by their place there
}
function appMessage(p, opts) {
  const files = p.files || [], ws = opts.mark, docs = docCards(p);
  if (ws && ws.length && !OPEN.has(p.id) && readLen(displayText(p)) > LONG) {
    const lc = displayText(p).toLowerCase(), f = fold(displayText(p));
    if (f && ws.some(w => lc.includes(w)) && !ws.some(w => (f.head + f.tail).toLowerCase().includes(w))) OPEN.add(p.id);
  }
  if (btwOpener(p)) return btwMessage(p, ws);
  const call = phoneCall(p); if (call) return call;
  if (p.stopped && p.from === "dash" && EDIT.id !== p.id) return stoppedMark(p, opts);
  if (EDIT.id !== p.id) { const ap = approvalLine(p); if (ap) return ap; }   // the person's yes:, yes, standing: or withdraw standing: as one line saying whether it counts
  const kind = SYS.has(p.from) || isScript(p.from) ? "sys" : p.from === "person" ? "me" : OUTSIDE.has(p.from) ? "out" : "agent";
  const asks = kind === "agent" && !simpleOn() && asksYou(p), split = kind === "agent" && !SOURCE.has(p.id) ? didSplit(p) : null;
  let q = split ? { ...p, disp: split.text } : p;
  const boxes = boxesOn() && EDIT.id !== p.id ? boxIds(p.text) : [];   // a Python box inline (ui/pybox.js): the frame stands in for its line
  if (boxes.length) q = { ...q, disp: dropBoxLines(displayText(q)) };
  const inkOnly = inkFiles(p, files) && EDIT.id !== p.id && (q = { ...q, disp: inkText(displayText(q)) }, !q.disp.trim() && !docs.length && files.every(f => INKF.test(f.name)));
  return h("article", { class: "msg", "data-ink-only": inkOnly ? "" : null, tabindex: "-1", "data-key": p.id, "data-who": kind, "data-cont": opts.cont ? "" : null, "data-report": displayText(p).length > LONG ? "" : null,
    "data-state": kind === "me" && p.interrupt ? "interrupt" : null, "data-asks": asks ? "" : null, "aria-label": `${who(p.from)}, ${stamp(p.t)}` },
    msgAvatar(p, kind),
    h("div", { class: "msg-c" },
      messageHead(p, { ...opts, asks }),
      relayLine(p),
      p.reply_to ? quote(p) : null,
      EDIT.id === p.id ? editBox(p, opts) : boxes.length && !q.disp ? null : body(q, ws),
      boxes.length ? h("div", { class: "msg-boxes", "data-key": "boxes" }, boxes.map(id => boxEmbed(p.id, id))) : null,
      files.length || docs.length ? h("div", { class: "msg-files" }, fileRow(p, files), docs) : null,
      kind === "agent" ? savedNote(p) : null,
      tagChips(p),
      split ? didFold(p, split) : null,
      kind === "agent" || p.stopped ? ranFold(p) : null,   // E203c: a stopped run's steps stay under its stopped line
      wordButton(p),
      messageReceipt(p),
      versionBar(p, opts),
      kind === "agent" && (asks || picked(p) === true || choiceOpen(p)) ? askActions(p) : null),
    simpleOn() ? null : msgTools(p, ws, opts));
}

// ---- E12: edit a sent message, ask for another reply; the older versions stay behind the arrows ----
// comms.js works out which version is drawn and passes opts.ver ({root, i, n}), opts.canEdit, opts.canRetry, opts.pick and opts.redraw.
const EDIT = { id: null, text: "", err: "", busy: false };
function versionBar(p, opts, copyOf = p) {
  // E203k: in the full look Edit sits in the message's one action bar (msgTools); a second row under it held a blank line before the reply
  const v = opts.ver, ed = simpleOn() ? editButton(p, opts) : null, re = opts.canRetry && can("post");
  // E203l (the E203k reader): the last reply shows Copy beside Retry without a pointer over it, as the chat apps do
  const cp = opts.canRetry && copyOf ? copyBtn(copyOf) : null;
  if (!v && !ed && !re && !cp) return null;
  const root = v ? v.root : p.id, what = p.from === "person" ? "message" : "reply";
  return h("div", { class: "msg-ver" },
    v ? h("span", { class: "msg-ver-n", role: "group", "aria-label": `Version ${v.i + 1} of ${v.n} of this ${what}` },
      h("button", { type: "button", class: "msg-ver-b", disabled: v.i === 0 ? "" : null, title: `Older version`, "aria-label": `Older version of this ${what}`, onclick: () => opts.pick && opts.pick(root, v.i - 1, v.n) }, "‹"),
      h("span", { class: "meta", "aria-live": "polite" }, `${v.i + 1} / ${v.n}`),
      h("button", { type: "button", class: "msg-ver-b", disabled: v.i === v.n - 1 ? "" : null, title: `Newer version`, "aria-label": `Newer version of this ${what}`, onclick: () => opts.pick && opts.pick(root, v.i + 1, v.n) }, "›")) : null,
    ed,
    cp,
    re ? button("Retry", e => retry(p, opts.retryRoot || root, e), { size: "s", kind: "quiet", icon: "retry", cls: "msg-vact msg-retry", title: "Ask for another answer; this one stays behind the arrows" }) : null);
}
function editButton(p, opts, kind = "quiet") {
  if (!opts.canEdit || !can("post") || EDIT.id === p.id) return null;
  return button("Edit", () => { Object.assign(EDIT, { id: p.id, text: typedText(p).replace(/^@new\s+/, ""), err: "", busy: false, focused: false }); opts.redraw && opts.redraw(); }, { size: "s", kind, icon: "edit", cls: "msg-vact msg-edit", title: "Change this message and get a new answer; the old one stays behind the arrows" });
}
async function retry(p, root, e) {
  const b = e && e.currentTarget; if (b) { b.disabled = true; b.textContent = tr("Asking…"); }
  try { await apiPost("/api/post", { thread: p.th, retry_of: root }); }
  catch (err) { if (b) { b.disabled = false; b.textContent = tr("Retry failed"); b.title = err.message; } }
}
function editBox(p, opts) {
  const root = opts.ver ? opts.ver.root : p.id;
  const ta = h("textarea", { class: "msg-edit-t", rows: Math.min(12, Math.max(3, EDIT.text.split("\n").length + 1)), "aria-label": "Edit your message", oninput: e => { EDIT.text = e.target.value; }, onkeydown: e => {
    if (e.key === "Escape") { e.preventDefault(); cancel(); }
    else if (e.key === "Enter" && !e.shiftKey && !e.isComposing) { e.preventDefault(); save(); } } }, EDIT.text);
  const cancel = () => { Object.assign(EDIT, { id: null, text: "", err: "" }); opts.redraw && opts.redraw(); };
  const save = async () => {
    const text = EDIT.text.trim(); if (!text || EDIT.busy) return;
    if (text === typedText(p).replace(/^@new\s+/, "").trim()) return cancel();
    EDIT.busy = true; EDIT.err = "";
    try { await apiPost("/api/post", { text: editNote(root) + text, thread: p.th, edit_of: root, simple: simpleOn(), ...((p.files || []).length ? { files_from: p.id } : {}) }); Object.assign(EDIT, { id: null, text: "", busy: false }); opts.redraw && opts.redraw(); }
    catch (err) { EDIT.busy = false; EDIT.err = err.message; opts.redraw && opts.redraw(); }
  };
  if (!EDIT.focused) { EDIT.focused = true; setTimeout(() => { if (ta.isConnected) { ta.focus(); ta.setSelectionRange(ta.value.length, ta.value.length); } }, 0); }
  return h("div", { class: "msg-editor" }, ta,
    EDIT.err ? h("p", { class: "msg-edit-err", role: "alert" }, "Not sent: " + EDIT.err) : null,
    h("div", { class: "msg-edit-acts" },
      button("Save and send", save, { size: "s", kind: "primary", cls: "msg-edit-save" }),
      button("Cancel", cancel, { size: "s", kind: "quiet", cls: "msg-edit-cancel" }),
      h("span", { class: "meta" }, (p.files || []).length ? "Its files go with it. The old version and its answer stay behind the arrows." : "The old version and its answer stay behind the arrows.")));
}

// ---- R7 step 4: the full look's message parts (the design hand-off, src/parts/30-thread.html) ----
// The author's mark beside the words: an agent's links to it; yours, the outside senders' and the system's are plain.
function msgAvatar(p, kind) {
  if (kind === "agent") return agentAvatar(p.from);
  return h("span", { class: "av", "aria-hidden": "true", "data-name": p.from }, h("span", null, kind === "sys" ? icon("bell") : kind === "me" ? "Y" : String(simpleOn() && kind !== "out" ? hiveName() : p.from).slice(0, 2)));
}

/** Is this agent message the last one to you, unanswered (the thread's state names it)? Its numbered choices then show as one-tap buttons
 *  even when it doesn't end on a question mark ("Pick one:" and a list), and in the simple look too. */
function choiceOpen(p) {
  if (!isAgent(p.from) || !p.men.includes("person")) return false;
  const st = threadState(p.th); return (st.key === "asks" || st.key === "foryou") && !!st.post && st.post.id === p.id;
}
/** Is this agent message the question the conversation is waiting on you for (the thread's state names it)? */
function asksYou(p) {
  if (!isAgent(p.from) || !p.men.includes("person") || !asksQuestion(p)) return false;
  const st = threadState(p.th); return st.key === "asks" && !!st.post && st.post.id === p.id;
}
// One tap on a numbered choice sends "Go with N." with 10 s to undo (ui/pick.js); no Answer: the composer is addressed to it and the hover bar has Reply (the Nov 8 handoff §8.3).
function askActions(p) {
  if (!can("post")) return null;
  return pickBar({ ...p, disp: displayText(p) }, { cls: "msg-acts" });
}

// "What it did": a closing section of steps, commands or checks folds under the words (the hand-off: technical detail, folded by default).
// A section whose heading names it ("What I did", "Steps", "Checks", "Commands", "Technical detail", "How I checked") and runs to the end, or
// to a last status line (Done:, Blocked: …), which stays in the open.
const DID_HEAD = /^(?:#{1,6}\s*|\*\*)?(what (?:i|it|we) did|steps(?: taken)?|commands(?: run)?|checks(?: run)?|how i checked( it)?|technical(?: detail[s]?| notes)?|details|tool calls)\b[^\n]*$/im;
const STATUS_LAST = /^(Done|Blocked|Waiting|Needs the person|Told to stop):/;
const DIDS = new Map();
function didSplit(p) {
  const t = chatText(p), hit = DIDS.get(p.id);
  if (hit && hit.src === t) return hit.v;
  let v = null;
  const m = t.length > 200 && !/^\s*(```|~~~)/m.test(t.slice(0, m0(t))) ? DID_HEAD.exec(t) : null;
  if (m && m.index > 40) {
    let rest = t.slice(m.index + m[0].length), tail = "";
    const lines = rest.replace(/\s+$/, "").split("\n"), last = lines[lines.length - 1] || "";
    if (STATUS_LAST.test(last.trim())) { tail = last.trim(); lines.pop(); }
    const steps = lines.filter(l => /^\s*(?:[-*+]|\d+[.)])\s+/.test(l)).length || lines.filter(l => l.trim() && !/^\s*(```|~~~)/.test(l)).length;
    if (steps >= 1) v = { text: (t.slice(0, m.index).replace(/\s+$/, "") + (tail ? "\n\n" + tail : "")), title: m[1], fold: lines.join("\n").replace(/^\s+|\s+$/g, ""), steps };
  }
  DIDS.set(p.id, { src: t, v });
  return v;
}
const m0 = t => { const i = t.search(DID_HEAD); return i < 0 ? 0 : i; };   // a fence open where the heading sits means it is inside code: not a section
const DID_OPEN = new Set();
function didFold(p, s) {
  return h("details", { class: "msg-fold", open: DID_OPEN.has(p.id) || null, ontoggle: (e, d) => { if (e.target !== d) return; if (d.open) DID_OPEN.add(p.id); else DID_OPEN.delete(p.id); } },
    h("summary", null, icon("chevron-right"), `What it did · ${s.steps} ${s.steps === 1 ? "step" : "steps"}`),
    h("div", { class: "msg-fold-b" }, mdIn(s.fold, p)));
}

// "Code it ran" (everyday E42, GAPS row 4: you never see it run): the commands and code files the reply's run ran before it wrote this,
// each with its output, folded under the answer. /api/ran reads them from the run's session log; one read asks for every reply drawn at once.
const RAN = new Map(), RAN_OPEN = new Set(), RAN_STEP_OPEN = new Set();
let ranQ = null;
const ranWait = p => now() - p.t < 30e3 ? 2e3 : 15e3;   // ask again for a fresh reply's code every 2 s for its first half minute, then every 15 s
function ranAsk(p) {
  const e = RAN.get(p.id);
  if (e && (e.v === null || e.v.steps.length || now() - p.t > 3 * MIN || now() - e.at < ranWait(p))) return;   // a reply just sent may beat its log: ask again (ranFetch sets a timer) for its first 3 minutes
  RAN.set(p.id, { v: e ? e.v : null, at: now(), busy: true });
  if (!ranQ) { ranQ = new Map(); queueMicrotask(ranFetch); }
  ranQ.set(p.id, p.t);
}
async function ranFetch() {
  const q = [...ranQ]; ranQ = null;
  for (let i = 0; i < q.length; i += 40) {
    const part = q.slice(i, i + 40);
    let v = {};
    try { v = await jget(`/api/ran?ids=${part.map(x => x[0]).join(",")}&at=${part.map(x => Math.floor(x[1] / 1e3)).join(",")}`); } catch (e) { v = null; }
    for (const [id] of part) {
      const r = v && v[id] && Array.isArray(v[id].steps) ? v[id] : { steps: [] };
      RAN.set(id, { v: r, at: now() });
      const p0 = S.byId[id];
      // E203i: a stopped run's kept reply comes with its steps, after the first draw: draw the stop again whole, reply and all
      if (p0 && p0.stopped && p0.from === "dash" && r.said) { for (const el of document.querySelectorAll(`.msg.msg-stopped[data-key="${CSS.escape(id)}"]:not(.msg-stopped-said)`)) el.replaceWith(inConversation(p0.th, () => stoppedMark(p0, STOP_OPTS.get(p0.id)))); continue; }   // named as in its conversation, not "Your Hive" (the E203i reader)
      // E42b: the post lands on the board a moment before its command's result reaches the run's log, so a fresh reply's first ask often finds nothing. Ask again on a timer, not on the next redraw, which may not come.
      if (!r.steps.length) { if (p0 && now() - p0.t < 3 * MIN) setTimeout(() => { for (const slot of document.querySelectorAll(`.msg[data-key="${CSS.escape(id)}"] .msg-ran-slot`)) slot.replaceWith(ranFold(S.byId[id] || p0)); }, ranWait(p0) + 100); continue; }
      for (const slot of document.querySelectorAll(`.msg[data-key="${CSS.escape(id)}"] .msg-ran-slot`)) { const p = S.byId[id]; if (p) slot.replaceWith(ranFold(p)); }
    }
  }
}
// E179: what the reply saved to memory, as a small note under the bubble. Copy and the message text leave it out; Settings → Memory lists and deletes them.
function savedNote(p) {
  const fs = savedFacts(p);
  return fs.length ? h("div", { class: "msg-saved", "data-key": "saved" }, fs.map(f => h("div", { class: "msg-saved-i" }, icon("archive"), h("span", { class: "msg-saved-l" }, "Saved to memory"), h("span", null, f)))) : null;
}
function ranFold(p) {
  const e = RAN.get(p.id);
  if (!e || !e.v || !e.v.steps.length) { ranAsk(p); return h("span", { class: "msg-ran-slot", hidden: true }); }
  const r = e.v, n = r.steps.length + (r.more || 0);
  if (RAN_LIVE_OPEN.has(p.from) && now() - p.t < 3 * MIN) { if (RAN_LIVE_OPEN.get(p.from)) RAN_OPEN.add(p.id); RAN_LIVE_OPEN.delete(p.from); }
  return h("details", { class: "msg-fold msg-ran", open: RAN_OPEN.has(p.id) || null, ontoggle: (ev, d) => { if (ev.target !== d) return; if (d.open) RAN_OPEN.add(p.id); else RAN_OPEN.delete(p.id); } },
    h("summary", null, icon("chevron-right"), `${ranTitle(r)} · ${n} ${n === 1 ? "step" : "steps"}`),
    h("div", { class: "ran-b" },
      r.more ? h("div", { class: "meta ran-more" }, `${r.more} earlier ${r.more === 1 ? "step" : "steps"} not shown`) : null,
      r.steps.map((s, i) => ranStep(p, s, i))));
}
// E42b: the same fold while the agent works, under its draft (or its working line before it writes), one step more as each command runs.
// Its open or shut state carries over to the reply's fold when the reply lands, so a reader watching the steps keeps them open.
function ranLiveFold(p, r, run) {
  const n = r.steps.length + (r.more || 0), name = p.id.slice(6);
  return h("details", { class: "msg-fold msg-ran", "data-live": "", open: RAN_OPEN.has(p.id) || null, ontoggle: (ev, d) => { if (ev.target !== d) return; if (d.open) RAN_OPEN.add(p.id); else RAN_OPEN.delete(p.id); RAN_LIVE_OPEN.set(name, d.open); } },
    h("summary", null, icon("chevron-right"), `${ranTitle(r)} · ${n} ${n === 1 ? "step" : "steps"}${run ? " · running" : ""}`),
    h("div", { class: "ran-b" },
      r.more ? h("div", { class: "meta ran-more" }, `${r.more} earlier ${r.more === 1 ? "step" : "steps"} not shown`) : null,
      r.steps.map((s, i) => ranStep(p, s, i))));
}
// E203f: a run that used a skill did more than run code, so its fold says so, as the chat apps' step list does.
const ranTitle = r => r.steps.some(s => s.lang === "skill") ? "Steps it took" : "Code it ran";
const RAN_LIVE_OPEN = new Map();   // agent name -> the live fold was opened (true) or shut: its next reply's fold starts the same way
const RAN_LANG = { python: "Python", node: "JavaScript", shell: "Shell", file: "File" };
function ranStep(p, s, i) {
  const k = p.id + ":" + i, long = (s.out || "").split("\n").length > 12 && !RAN_STEP_OPEN.has(k);
  const out = long ? s.out.split("\n").slice(0, 12).join("\n") : s.out;
  if (s.lang === "skill") return h("div", { class: "ran-s ran-skill", "data-key": "skill" },   // E203d: a skill the run used, in words: no code, no "No output"
    h("div", { class: "ran-h" }, h("span", { class: "ran-lang" }, "Skill"), h("span", { class: "ran-what" }, s.what), s.t ? h("span", { class: "meta ran-t" }, clock(s.t * 1e3)) : null),
    s.about ? h("div", { class: "meta ran-none" }, s.about) : null);
  return h("div", { class: "ran-s", "data-err": s.err ? "" : null },
    h("div", { class: "ran-h" },
      h("span", { class: "ran-lang" }, s.lang === "file" ? s.file || "File" : RAN_LANG[s.lang] || s.lang),
      s.what ? h("span", { class: "ran-what" }, s.what) : null,
      s.t ? h("span", { class: "meta ran-t" }, clock(s.t * 1e3)) : null),
    h("pre", { class: "ran-code" }, h("code", null, s.code)),
    s.out ? h("pre", { class: "ran-out", "aria-label": s.err ? "Output, with an error" : "Output" }, h("code", null, out)) : s.lang === "file" ? null : h("div", { class: "meta ran-none" }, s.running ? "Running…" : "No output"),
    long ? button(`Show all ${s.out.split("\n").length} lines`, (ev, b) => { RAN_STEP_OPEN.add(k); b.closest(".ran-s").replaceWith(ranStep(p, s, i)); }, { size: "s", kind: "quiet", cls: "ran-all" }) : null);
}

// The hover toolbar (.msg-tools): Reply, Copy text, and … for Quote, Copy link, Open in Yours and Details. Nothing edits or takes back a sent message.
const linkOf = p => `${location.origin}${location.pathname}${location.search}#comms/${encodeURIComponent(p.th)}/${p.id}`;
const DETAILS = { id: null };
function toolsMenu(p, ws) {
  const item = (label, fn, sub, cls) => h("button", { type: "button", class: "menu-i" + (cls ? " " + cls : ""), role: "menuitem", onclick: fn }, label, sub ? h("small", null, sub) : null);
  if (DETAILS.id === p.id) {
    const tos = p.men.filter(m => m !== p.from);
    return h("div", { class: "rc-menu menu msg-det", role: "dialog", "aria-label": "Message details", "data-side": "bottom-end" },
      h("dl", { class: "kv" },
        h("dt", null, "From"), h("dd", null, who(p.from)),
        h("dt", null, "To"), h("dd", null, tos.length ? tos.map(m => m === "person" ? "you" : m).join(", ") : "the conversation"),
        h("dt", null, "Sent"), h("dd", null, stamp(p.t)),
        h("dt", null, "Id"), h("dd", { class: "num" }, p.id)),
      h("div", { class: "menu-h" }, "Your last message can be edited, and the last reply tried again: the old versions stay behind the arrows."),
      button("Close", () => { DETAILS.id = null; closeMenu(); }, { size: "s", kind: "quiet" }));
  }
  return h("div", { class: "rc-menu menu", role: "menu", "data-side": "bottom-end" },
    can("post") ? item("Quote", () => { closeMenu(); quoteRef(p.th, { who: who(p.from), where: clock(p.t), text: quoteText(p) }); }, "Selected words, or the first lines", "msg-quote") : null,
    item("Copy link", (e, b) => { copyText(linkOf(p)); closeMenu(); }, null, "msg-link"),
    item("Copy as Markdown", () => { copyText(displayText(p)); closeMenu(); }),
    p.from !== "person" && (window.speechSynthesis || (S.app && S.app.phone)) ? (reading(p.id)
      ? item("Stop reading", () => { stopReading(); closeMenu(); }, null, "msg-read")
      : item("Read aloud", () => { readAloud(p.id, displayText(p)); closeMenu(); }, null, "msg-read")) : null,
    item("Open in Yours", () => { closeMenu(); location.hash = "#yours"; }, null, "msg-yours"),
    item(SOURCE.has(p.id) ? "Back to the post" : "View source", (e, b) => {
      const was = b.closest(".msg")?.querySelector(".msg-b"); closeMenu();
      if (SOURCE.has(p.id)) SOURCE.delete(p.id); else SOURCE.add(p.id);
      was?.replaceWith(body(p, ws));
    }),
    h("div", { class: "menu-sep", role: "separator" }),
    item("Details", (e, b) => { const box = b.closest(".rc-more"); closeMenu(); DETAILS.id = p.id; menuFor = p.id; box.firstChild.setAttribute("aria-expanded", "true"); box.append(scrim(closeMenu), toolsMenu(p, ws)); }, null, "msg-details"),
    h("div", { class: "menu-h" }, "Your last message can be edited, and the last reply tried again: the old versions stay behind the arrows."));
}
// Quote: the words selected inside this message, else its first lines.
function quoteText(p) {
  const sel = getSelection && getSelection(), el = document.querySelector(`.msg[data-key="${CSS.escape(p.id)}"]`);
  if (sel && el && !sel.isCollapsed && el.contains(sel.anchorNode)) return String(sel).trim();
  return mdPlain(displayText(p)).split("\n").filter(l => l.trim()).slice(0, 4).join("\n");
}
function msgTools(p, ws, opts = {}) {
  const mine = p.from === "person", tos = p.men.filter(m => m !== "new" && m !== p.from), open = menuFor === p.id;
  return h("div", { class: "msg-tools", role: "toolbar", "aria-label": "Message actions" },
    can("post") ? button("Reply", () => replyTo(p), { kind: "icon", size: "s", icon: "reply", cls: "msg-reply", title: mine && tos.length ? `Reply · this went to ${tos.map(agentLabel).join(", ")}` : "Reply to this message; it goes to its author" }) : null,
    opts.canRetry ? null : copyBtn(p),   // the last reply's Copy shows in the row under it (versionBar)
    mine ? editButton(p, opts, "icon") : null,
    h("span", { class: "rc-more pop" },
      h("button", { type: "button", class: "btn msg-moreb", "data-kind": "icon", "data-size": "s", "aria-haspopup": "menu", "aria-expanded": String(open), "aria-label": "More actions", title: "More actions", onclick: (e, b) => {
        const was = menuFor === p.id; closeMenu();
        if (!was) { menuFor = p.id; b.setAttribute("aria-expanded", "true"); b.parentNode.append(scrim(closeMenu), toolsMenu(p, ws)); }
      } }, icon("more")),
      open ? [scrim(closeMenu), toolsMenu(p, ws)] : null));
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
  return markBody(mdIn(t, p), ws);
}
const moreLabel = p => OPEN.has(p.id) ? "Show less" : `Show all · ${devBody(p).length.toLocaleString("en-US")} characters`;
// On hover, top right: Copy text, Copy link, and ⋯ for the raw post, its id and the author's page. Reply only where this address can post.
function devTools(p, ws) {
  const open = menuFor === p.id, menu = () => h("div", { class: "rc-menu menu", role: "menu", "data-side": "bottom-end" },
    h("button", { type: "button", class: "menu-i", role: "menuitem", onclick: (e, b) => {
      const m = b.closest(".msg"); closeMenu();
      if (RAW.has(p.id)) RAW.delete(p.id); else RAW.add(p.id);
      m?.querySelector(".md")?.replaceWith(devBodyEl(p, ws));
    } }, RAW.has(p.id) ? "Back to the post" : "Raw post"),
    h("button", { type: "button", class: "menu-i", role: "menuitem", onclick: () => { copyText(p.id); closeMenu(); } }, `Copy post id · ${p.id}`),
    SYS.has(p.from) || OUTSIDE.has(p.from) ? null : h("a", { class: "menu-i", role: "menuitem", href: agentHref(p.from), onclick: closeMenu }, `${p.from}'s page`));
  return h("div", { class: "msg-tools" },
    can("post") ? button("Reply", () => replyTo(p), { kind: "icon", size: "s", icon: "reply", title: "Reply" }) : null,
    button("Copy text", (e, b) => copyPress(b, devBody(p)), { kind: "icon", size: "s", icon: "copy", title: "Copy text" }),
    button("Copy link", (e, b) => copyPress(b, `${location.origin}${location.pathname}${location.search}#comms/${encodeURIComponent(p.th)}/${p.id}`), { kind: "icon", size: "s", icon: "link", title: "Copy link" }),
    h("span", { class: "rc-more pop" },
      h("button", { type: "button", class: "btn msg-moreb", "data-kind": "icon", "data-size": "s", "aria-haspopup": "menu", "aria-expanded": String(open), "aria-label": "More for this post", title: "More", onclick: (e, b) => {
        const was = menuFor === p.id; closeMenu();
        if (!was) { menuFor = p.id; b.setAttribute("aria-expanded", "true"); b.parentNode.append(scrim(closeMenu), menu()); }
      } }, icon("more")), open ? [scrim(closeMenu), menu()] : null));
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
  const long = devBody(p).length > DEV_LONG, files = p.files || [], ws = opts.mark, docs = docCards(p);
  // A word found past the cut would look like a false match: open the post so it shows.
  if (long && ws && ws.length && !OPEN.has(p.id)) {
    const lc = p.text.toLowerCase(), cut = mdCut(p.text, DEV_CUT).toLowerCase();
    if (ws.some(w => lc.includes(w)) && !ws.some(w => cut.includes(w))) OPEN.add(p.id);
  }
  const cut = el => { if (long && !OPEN.has(p.id) && !RAW.has(p.id)) el.dataset.cut = ""; return el; };
  return h("article", { class: "msg", tabindex: "-1", "data-key": p.id, "data-who": SYS.has(p.from) ? "sys" : p.from === "person" || p.from === "box" ? "person" : null, "data-cont": opts.cont ? "" : null, "data-report": long ? "" : null },
    devHead(p, opts.showThread !== false),
    devTools(p, ws),
    cut(devBodyEl(p, ws)),
    files.length || docs.length ? h("div", { class: "msg-files" }, fileRow(p, files), docs) : null,
    tagChips(p),
    long ? button(moreLabel(p), (e, b) => {
      if (OPEN.has(p.id)) OPEN.delete(p.id); else OPEN.add(p.id);
      b.closest(".msg").querySelector(".md").replaceWith(cut(devBodyEl(p, ws)));
      b.querySelector("span").textContent = tr(moreLabel(p));
      b.setAttribute("aria-expanded", String(OPEN.has(p.id)));
    }, { kind: "quiet", size: "s", cls: "msg-more", icon: "chevron" }) : null);
}

/** A message on its way: {lid, text, files:[File], url per image, state: sending|sent|failed, err}. actions: {retry, edit, discard}
 *  Failed (R7): the bubble turns red and one line says why, with Try again and Copy text; Discard drops it. */
export function localMessage(it, { retry, edit, discard }) {
  const failed = it.state === "failed";
  const kids = [
    h("div", { class: "msg-h" }, h("b", { class: "msg-me" }, "You"),
      it.to && !simpleOn() ? [h("span", { class: "msg-to" }, "to"), it.to === "new" ? h("span", { class: "muted" }, "a new agent") : agentRef(it.to)] : null,
      h("span", { class: "msg-end" }, h("time", { class: "meta num msg-time" }, clock(it.t)))),
    it.reply ? h("div", { class: "msg-q", "data-static": "" }, icon("reply"), h("b", null, who(it.reply.from)), h("span", null, short(mdPlain(displayText(it.reply)), 140))) : null,
    it.text.trim() ? h("div", { class: "msg-b" }, markdown(it.text)) : null,
    it.files.length ? h("div", { class: "msg-files" }, it.files.map(f => f.url
      ? h("span", { class: "msg-thumb" }, h("img", { src: f.url, alt: f.name }), h("span", null, f.name))
      : h("span", { class: "msg-file" }, h("b", null, badgeOf(f.name)), h("span", null, h("span", { class: "msg-fn" }, f.name), h("span", { class: "meta" }, fsize(f.size)))))) : null,
    h("div", { class: "msg-rc msg-st", role: failed ? "alert" : "status", "data-tone": failed ? "bad" : null },
      failed ? [h("span", { "data-tone": "bad" }, h("b", { "aria-hidden": "true" }, "!"), `Not sent: ${String(it.err || "the Hive gave no reason").replace(/\.+$/, "")}.`),
        button("Try again", () => retry(it), { size: "s", cls: "msg-retry" }),
        button("Copy text", (e, b) => copyPress(b, it.text), { size: "s", kind: "quiet", cls: "msg-copytext" }),
        button("Discard", () => discard(it), { size: "s", kind: "quiet", cls: "msg-discard", title: "Drop this unsent message" })]
        : h("span", { "data-tone": "quiet" }, statusDot("sending"), it.state === "sent" ? "Sent" : it.files.length && it.uploading ? "Uploading…" : "Sending…"))];
  return h("article", { class: "msg", "data-key": "local:" + it.lid, "data-who": "me", "data-state": it.state, "aria-label": failed ? "Your message, not sent" : "Your message, sending" },
    S.app.app ? [h("span", { class: "av", "aria-hidden": "true" }, h("span", null, "Y")), h("div", { class: "msg-c" }, kids)] : kids);
}

// ---- helper conversations, folded where they started (R7 step 4) ----
// A conversation an agent here started with a helper (a child thread) shows as one line at the point it began:
// "a7c31 asked a2e90 to write tests · 4 messages · last: …". Opened, its messages show indented under it.
const HELP_OPEN = new Set();
let kidsAt = { ver: -1, by: new Map() };
function helpersOf(th) {
  if (kidsAt.ver !== S.ver) kidsAt = { ver: S.ver, by: new Map() };
  let v = kidsAt.by.get(th);
  if (!v) {
    v = threadChildren(th).filter(name => !btwParent(name)).map(name => ({ name, posts: (S.threads.get(name) || {}).posts || [] })).filter(k => k.posts.length)
      .map(k => ({ ...k, t: k.posts[0].t })).sort((a, b) => a.t - b.t);
    kidsAt.by.set(th, v);
  }
  return v;
}
/** The folds for th's helper conversations begun at or before t (all of them when t is Infinity) and not yet in done (a Set of names it fills). */
export function helperFolds(th, t, done, opts = {}) {
  if (!S.app.app) return [];
  return helpersOf(th).filter(k => k.t <= t && !done.has(k.name)).map(k => { done.add(k.name); return inConversation(th, () => helperFold(k, opts)); });
}
function helperFold(k, opts) {
  const first = k.posts[0], lastP = k.posts[k.posts.length - 1];
  const asker = first.from, helper = first.men.find(m => m !== asker && m !== "new" && m !== "person") || (S.woke[first.id] || [])[0]?.name || k.posts.find(p => p.from !== asker)?.from;
  const task = String(threadTitle(k.name) || k.name).replace(/^./, c => c.toLowerCase());
  const n = k.posts.length, span = clock(first.t) + (lastP.t - first.t > MIN ? "–" + clock(lastP.t) : "");
  const fill = d => { const b = d.querySelector(".cm-helper-b"); if (!b.firstChild) b.append(...k.posts.map((p, i) => message(p, { ...opts, showThread: false, cont: i > 0 && k.posts[i - 1].from === p.from && p.t - k.posts[i - 1].t < 5 * MIN }))); };
  const d = h("details", { class: "cm-helper", "data-key": "helper:" + k.name, "data-th": k.name,
      ontoggle: (e, el) => { if (e.target !== el) return; if (el.open) { HELP_OPEN.add(k.name); fill(el); } else HELP_OPEN.delete(k.name); } },
    h("summary", null, icon("chevron-right"),
      h("span", { class: "avs" }, [asker, helper].filter(Boolean).map(x => agentAvatar(x, { size: "s" }))),
      h("span", { class: "cm-helper-t" }, h("b", null, capFirst(agentLabel(asker))), helper ? " asked a helper: " : " started: ", task.length > 70 ? short(task, 70) : task),
      h("span", { class: "meta cm-helper-m" }, `${n} ${n === 1 ? "message" : "messages"} · last: ${short(mdPlain(displayText(lastP)).replace(/\s+/g, " "), 60)}`),
      h("span", { class: "meta num cm-helper-when" }, span)),
    h("div", { class: "cm-helper-b" }));
  if (HELP_OPEN.has(k.name)) { d.open = true; fill(d); }
  return d;
}

/** E11: the reply an agent is still writing (/api/live's draft, read from its streamed post command), drawn as its message will be and grown as it is written. */
export function draftMessage(a) {
  const t = String(a.draft ? a.draft.text || "" : "").replace(/^(?:[ \t]*@[\w.-]+[,:]?[ \t]+)+/, "");
  const steps = a.steps || [], run = steps.some(s => s.running);
  const p = { id: "draft:" + a.name, from: a.name, th: a.draft ? a.draft.thread : null, t: (a.draft ? a.draft.at : steps.length ? steps.at(-1).t || now() / 1e3 : now() / 1e3) * 1e3, men: [] };
  return h("article", { class: "msg", "data-key": p.id, "data-who": "agent", "data-state": a.draft ? "draft" : "working", "aria-label": `${who(a.name)}, ${a.draft ? "writing" : "working"}`, "aria-busy": "true" },
    S.app.app ? msgAvatar(p, "agent") : null,
    h("div", { class: "msg-c" },
      h("div", { class: "msg-h" }, h("b", null, who(a.name)), h("span", { class: "msg-end" }, h("span", { class: "meta" }, a.draft ? "writing…" : run ? "running code…" : "working…"))),
      a.draft ? h("div", { class: "msg-b" }, markdown(t)) : null,
      steps.length ? ranLiveFold(p, { steps, more: a.more || 0 }, run) : null));
}
