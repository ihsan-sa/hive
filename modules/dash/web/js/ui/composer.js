// The composer: write a message, attach files (button, paste or drop), name an agent with @, reply, send.
// Up and Down walk the person's past messages like a shell's history; Tab finishes what they've started typing from them.
// App mode only (can("post")). Everything lives here, outside the render, so polls never touch what the person is doing:
// - a draft per thread (text, reply, quote, chosen recipient, name), kept in localStorage, so it survives leaving and reloading;
//   a quote ("Ask about this") and a reply are chips above the box, never text in it (R7 step 2: nothing is pasted into the box);
//   the quote still goes in front of the words on the wire (quotePrefix), since the core ignores the post's quote field;
// - interrupt mode (R7 step 2): the Interrupt link on the line above the box arms it; only then does Send (or Enter) interrupt;
// - OUT, messages on their way: they show at once as "Sending…", settle when the board has them, and a failed one keeps
//   its text and offers Retry, Edit and Discard. The board is append-only, so nothing here suggests unsending a posted one.
import { uploadFile } from "../core/upload.js";
import { h } from "../core/dom.js";
import { S, can, jget, post, poll, loadApp } from "../core/store.js";
import { simpleOn, hiveName } from "../core/simple.js";
import { foci, focusById, focusOf, focusName, listed, threadLine } from "../core/foci.js";
import { go } from "../core/router.js";
import { isAgent, isRunning, agentCard, agentStatus, threadPeople, partner, displayText, typedText, threadTitle } from "../core/model.js";
import { short, mdPlain, now } from "../core/format.js";
import { button, icon, input, statusDot } from "./kit.js";
import { openAside } from "./aside.js";
import { sixSeven } from "../core/sixseven.js";
import { startCall, phoneReady, inCall } from "./phone.js";
import { run as eggRun } from "../core/easter.js";
import { wordBoxButton, wordBoxPanel } from "./rootword.js";
import { delivery } from "./message.js";   // circular, used only at render time

// menu: which of the bar's menus is open (to | add | aa | model), or false. mode: thread → "interrupt" while armed.
// sent: thread → the interrupt just sent there ({who, it, at, seen}), for the live line's stopping → reading → normal.
const CP = { update: null, th: undefined, focus: false, ac: null, menu: false, drag: false, sig: "", hist: null, mode: {}, sent: {} };
// The secret box (the person, 11041306e4d2): a name and a hidden value saved straight to /api/secret. The value never enters the draft, the board or localStorage.
const SB = { open: false, rows: [{ name: "", value: "" }], say: "", tone: null, busy: false };
const refresh = () => CP.update && CP.update();
const IMGNAME = /\.(png|jpe?g|gif|webp)$/i;
const size = n => (n >= 1048576 ? (n / 1048576).toFixed(1) + " MB" : n >= 1024 ? Math.round(n / 1024) + " kB" : n + " B");

// ---- drafts ----
const D = new Map();
const dkey = th => "draft:" + (th || "(new)");
export function draft(th) {
  const k = dkey(th); let d = D.get(k);
  if (!d) {
    let v = {}; try { v = JSON.parse(localStorage.getItem(k) || "{}"); } catch (e) {}
    d = { th: th || null, text: v.text || "", replyId: v.reply || null, reply: null, quote: v.quote || null, to: v.to || null, name: v.name || "", naming: !!v.name, model: v.model || "", focus: v.focus || "", files: [], err: "" };
    D.set(k, d);
  }
  if (!d.reply && d.replyId && S.byId[d.replyId]) d.reply = S.byId[d.replyId];
  return d;
}
/** Is there unsent text left in this thread's composer (this browser only)? */
export function hasDraft(th) {
  const d = D.get(dkey(th)); if (d && (d.text.trim() || d.files.length)) return true;
  try { return !!(JSON.parse(localStorage.getItem(dkey(th)) || "{}").text || "").trim(); } catch (e) { return false; }
}
function keep(d) {
  const k = dkey(d.th), rid = d.reply ? d.reply.id : d.replyId;
  try { if (d.text || rid || d.quote || d.to || d.name || d.model || d.focus) localStorage.setItem(k, JSON.stringify({ text: d.text, reply: rid, quote: d.quote, to: d.to, name: d.name, model: d.model, focus: d.focus })); else localStorage.removeItem(k); } catch (e) {}
}
const field = th => document.querySelector(`.cp[data-key="${CSS.escape("cp:" + (th || "(new)"))}"] .cp-t`);

/** Start a reply to a post from anywhere: opens its thread and puts the cursor in the composer. */
export function replyTo(p) {
  const d = draft(p.th); d.reply = p; d.replyId = p.id; d.to = null; keep(d);
  CP.focus = true; go("comms", p.th); refresh();
}
/** Esc: close the @ list or a menu, else leave interrupt mode, else drop a quote, else cancel a reply. True if it did something. */
export function cancelReply() {
  if (CP.ac) { CP.ac = null; refresh(); return true; }
  if (CP.menu) { CP.menu = false; refresh(); return true; }
  if (CP.th && CP.mode[CP.th]) { disarm(CP.th); return true; }
  const d = CP.th !== undefined && D.get(dkey(CP.th));
  if (d && d.quote) { d.quote = null; keep(d); refresh(); return true; }
  if (!d || !(d.reply || d.replyId)) return false;
  d.reply = null; d.replyId = null; keep(d); refresh(); return true;
}
export const focusComposer = () => { CP.focus = true; refresh(); };
/** Address the next message in a thread to who (the To menu's choice) and put the cursor in the box. Nothing is typed for the person. */
export function sayTo(th, who) {
  const d = draft(th); if (who) { d.to = who; keep(d); }
  CP.focus = true; refresh();
}
/** Interrupt mode (R7 step 2): open th's conversation with the box armed to interrupt who. Nothing is sent until the person
 *  presses Interrupt (or Enter) there; Esc or × goes back to an ordinary message. From the Interrupt link and Agents › Now. */
export function armInterrupt(th, who) {
  if (!th) return;
  const d = draft(th);
  if (who && recipient(th).who !== who) { d.to = who; keep(d); }
  CP.mode[th] = "interrupt"; CP.menu = false; CP.ac = null; CP.focus = true;
  go("comms", th); refresh();
}
function disarm(th) { if (!CP.mode[th]) return; delete CP.mode[th]; CP.focus = true; refresh(); }

/** Quote a piece of a message above a thread's box ("Ask about this", a summary's Reply) and put the cursor there.
 *  q: {who, where, text, code}. Only one quote at a time: a new one replaces it. The person's words stay as they are. */
export function quoteRef(th, q) {
  const d = draft(th); d.quote = { who: q.who || "", where: q.where || "", text: String(q.text || ""), code: !!q.code }; keep(d);
  CP.focus = true; refresh();
}
const QMAX = 12;
/** The quote as it goes on the board: "> who, where:" and at most QMAX quoted lines, before the person's words.
 *  A longer quote keeps its start and its last two lines (where Ask puts a plot's slider values or what a page reported). */
export function quotePrefix(q) {
  const lines = q.text.replace(/\s+$/, "").split("\n");
  let body = lines.length > QMAX ? [...lines.slice(0, QMAX - 3), "…", ...lines.slice(-2)] : lines;
  body = body.map(l => (l.length > 300 ? l.slice(0, 299) + "…" : l));
  if (q.code && lines.length > 1) body = ["```", ...body, "```"];
  return `> ${[q.who, q.where].filter(Boolean).join(", ")}:\n` + body.map(l => "> " + l).join("\n") + "\n\n";
}
const QUOTE_HEAD = /^> [^\n]*:\n(?:>[^\n]*\n)*\n/;

// ---- who it goes to ----
// The server's rule (person_post in dash.py, /api/target): a plain message wakes whoever posted last in the thread.
// The person thinks of it as the agent they've been talking to, so that is the default here, and when the two differ
// the message is sent with that name in front. Typing @name, replying, or choosing in the To menu overrides it.
const TG = { key: null, v: {} };
function serverWho(th) {
  const k = th + "|" + S.ver;
  if (TG.key !== k) { TG.key = k; jget("/api/target?thread=" + encodeURIComponent(th)).then(t => { TG.v[th] = t; refresh(); }).catch(() => {}); }
  return TG.v[th] || null;
}
const MODEL_WORDS = ["opus", "sonnet", "haiku", "fable", "astra"];
/** A focus id in a message is a name too (hive post, the person 110717432468): it reaches the focus's keeper. {id, keeper} or null. */
function focusWord(w) { const f = focusById(w); return f ? { id: f.id, keeper: f.keeper || null } : null; }
/** A thread named with @ gets a copy of the message, for the agent last in it (hive post); its own thread is no target. */
const threadWord = (w, th) => (w !== th && S.threads.has(w) && listed(w) ? w : null);
/** The agent a typed @word reaches: the agent itself, a focus's keeper (a new one in the focus's own thread when nobody keeps it), or null. */
function wordTarget(w, th) {
  if (w === "new" || MODEL_WORDS.includes(w) || isAgent(w)) return w;
  const f = focusWord(w); if (f) return f.keeper || (th && (th === f.id || th === (focusById(f.id).thread || f.id)) ? "new" : null);
  return null;
}
function namedWord(text, th) {
  for (const m of text.matchAll(/(?:^|\s)@([\w.-]+)/g)) if (wordTarget(m[1], th)) return m[1];
  return null;
}
const named = (text, th) => { const w = namedWord(text, th); return w ? wordTarget(w, th) : null; };
/** {who: name | "new" | null, why: reply | named | chosen | partner | last | first, server: who the server would wake} */
export function recipient(th) {
  if (!th) return { who: "new", why: "first" };
  const d = draft(th), s = serverWho(th), sv = s && s.who;
  if (isBtw(d.text) || d.to === "btw") return { who: "new", why: "btw", server: sv };   // typed /btw, or "A new agent, on the side" in To
  if (d.reply) return { who: isAgent(d.reply.from) ? d.reply.from : sv, why: "reply", server: sv };
  const w = namedWord(d.text, th); if (w) return { who: wordTarget(w, th), why: "named", server: sv, to: w };
  if (d.to) return { who: wordTarget(d.to, th) || d.to, why: "chosen", server: sv, to: d.to };
  const p = partner(th);
  if (p) return { who: p, why: sv && sv !== p ? "partner" : "last", server: sv };
  // The target is fetched when the board changes, and a run starting doesn't change the board: an agent already running here still counts.
  const run = !sv && S.live && S.live.agents.find(a => a.thread === th && a.alive && a.name);
  return { who: sv || (run ? run.name : "new"), why: sv || run ? "last" : "first", server: sv };
}
function wire(th, d, r) {
  let t = (d.quote ? quotePrefix(d.quote) : "") + (d.text.trim() ? d.text : "");   // files with no words go as they are: the post shows them, no "Attached:" line
  if (r.why === "btw" && !isBtw(t)) t = "/btw " + t;   // picked in the To menu: the wire carries it, the box never shows it
  if (!th || r.why === "reply" || r.why === "named" || r.why === "first" || r.why === "btw") return t;   // /btw goes to a fresh agent in a side thread (person_post)
  if (!t && r.why !== "chosen") return "";   // files with no words and no To picked: nothing is added to the text (R7 step 2)
  if (r.to && r.to !== r.who && !named(t, th)) return "@" + r.to + " " + t;   // a focus picked in the To menu: hive post finds its keeper and marks the post
  if (r.who === "new") return "@new " + t;
  if (r.who && r.who !== r.server) return "@" + r.who + " " + t;
  return t;
}

/** "/btw question": a side question about this conversation, answered in its own side thread so this one's agent never sees it. */
export const isBtw = t => /^\s*\/btw(\s|$)/i.test(t || "");

// ---- messages on their way ----
export const OUT = [];
let lid = 0;
function prune() {
  for (let i = OUT.length - 1; i >= 0; i--) if (OUT[i].id && S.byId[OUT[i].id]) { for (const f of OUT.splice(i, 1)[0].files) if (f.url) URL.revokeObjectURL(f.url); }
}
/** Messages for one thread (or null: new work) not on the board yet. */
export function outFor(th) { prune(); return OUT.filter(x => x.th === (th || null)); }
export const outSig = th => outFor(th).map(x => x.lid + x.state + (x.uploading ? "u" : "")).join();

async function deliver(it) {
  it.state = "sending"; it.err = ""; refresh();
  try {
    if (!it.refs) { it.uploading = it.files.length > 0; refresh(); const refs = []; for (const f of it.files) refs.push(await uploadFile(f.file)); it.refs = refs; it.uploading = false; }
    const res = await post("/api/post", { text: it.wire, thread: it.th || it.name || null, reply_to: it.reply ? it.reply.id : null, files: it.refs, simple: simpleOn(), model: it.model || null, quote: it.quote || null, ...(it.interrupt ? { interrupt: true } : {}) });
    it.id = res.id; it.state = "sent"; const thr = it.th;
    if (!it.th && res.thread) { it.th = res.thread; if (it.focus) post("/api/focus", { thread: res.thread, focus: it.focus }).catch(() => {}); go("comms", res.thread); loadApp(); }   // filed under the focus picked in the box; a failure leaves it in Misc, where the title menu can file it
    else if (res.thread && res.thread !== it.th) { it.th = res.thread; it.aside = true; openAside(thr, res.thread, it.text.replace(/^\s*\/btw\s*/i, "")); loadApp(); }   // /btw: it lives in the side thread; stay here
    poll();
  } catch (e) { it.state = "failed"; it.err = simpleOn() && !/sign in/.test(e.message) ? "Check your connection, then try again." : e.message; it.uploading = false; }
  refresh();
}
function queue(th, d, r, interrupt = false) {
  const it = { lid: ++lid, th: th || null, text: d.text, files: d.files, reply: d.reply, quote: d.quote || null, interrupt, to: th ? r.who : null, name: th ? null : d.name.trim() || null, wire: wire(th, d, r), model: d.model || null, focus: th ? null : d.focus || null, state: "sending", err: "", t: now() };
  OUT.push(it); deliver(it);
  return it;
}
/** Send. It interrupts (dash.py person_post, hive post --interrupt) only when the box is armed and the agent it goes to still works:
 *  Enter or Send in an ordinary box never does. */
function send(th) {
  const d = draft(th);
  if (!d.text.trim() && !d.files.length) return;
  if (d.text.trim() === "/67" && !d.files.length) { d.text = ""; keep(d); const el = field(th); if (el) el.value = ""; refresh(); sixSeven(); return; }
  if (!d.files.length && /^\/(party|bees|matrix|rave)$/.test(d.text.trim())) { const t = d.text.trim(); d.text = ""; keep(d); const el = field(th); if (el) el.value = ""; refresh(); eggRun(t); return; }   // easter egg, not sent
  const r = recipient(th), int = !!th && CP.mode[th] === "interrupt" && busyWho(th, r) === r.who;
  const it = queue(th, d, r, int);
  if (th) delete CP.mode[th];
  if (int) CP.sent[th] = { who: r.who, it, at: Date.now(), seen: 0 };
  Object.assign(d, { text: "", files: [], reply: null, replyId: null, quote: null, to: null, name: "", naming: false, err: "", model: "" }); keep(d);   // the focus stays: the next one most likely goes to the same place
  CP.ac = null; CP.menu = false; CP.hist = null; if (!SB.open) SB.say = "";
  const el = field(th); if (el) { el.value = ""; el.focus(); }   // morph leaves a focused field alone, so clear it here
  refresh(); window.dispatchEvent(new CustomEvent("cp:sent", { detail: { th } }));
}
// Lists carry on (the person, 11040216f6f2): Enter at the end of "1. x" or "- x" starts the next item,
// Enter on an empty item ends the list, and Enter anywhere else sends as before.
const ITEM = /^([ \t]*)(?:([-*•])|(\d{1,3})([.)]))[ \t]+(.*)$/;
function listEnter(th, n) {
  const v = n.value, at = n.selectionStart;
  if (at !== n.selectionEnd || (v[at] && v[at] !== "\n")) return false;
  const start = v.lastIndexOf("\n", at - 1) + 1, m = ITEM.exec(v.slice(start, at));
  if (!m) return false;
  const [, ind, bul, num, sep, rest] = m;
  let nv, caret;
  if (!rest.trim()) { nv = v.slice(0, start) + v.slice(at); caret = start; }
  else { const mark = ind + (bul || (+num + 1) + sep) + " "; nv = v.slice(0, at) + "\n" + mark + v.slice(at); caret = at + 1 + mark.length; }
  n.value = nv; n.setSelectionRange(caret, caret); mdSync(n);
  const d = draft(th); d.text = nv; keep(d); refresh();
  return true;
}
// Markdown formatting (the person, 11052100): wrap the selection in marks, or prefix its lines. Ctrl/Cmd+B bold, I italic, E code, K link.
function fmt(th, kind) {
  const n = field(th); if (!n) return;
  const v = n.value, a = n.selectionStart, b = n.selectionEnd, sel = v.slice(a, b);
  let nv, s0, s1;
  const wrap = (l, r, ph) => { const t = sel || ph; nv = v.slice(0, a) + l + t + r + v.slice(b); s0 = a + l.length; s1 = s0 + t.length; };
  if (kind === "bold") wrap("**", "**", "bold");
  else if (kind === "italic") wrap("*", "*", "italic");
  else if (kind === "code") { if (sel.includes("\n")) { nv = v.slice(0, a) + "```\n" + sel + "\n```" + v.slice(b); s0 = a + 4; s1 = s0 + sel.length; } else wrap("`", "`", "code"); }
  else if (kind === "link") { wrap("[", "](url)", "text"); s0 = a + 1 + (sel || "text").length + 2; s1 = s0 + 3; }
  else {
    const ls = v.lastIndexOf("\n", a - 1) + 1, block = v.slice(ls, b);
    const out = block.split("\n").map((x, k) => kind === "ol" ? (k + 1) + ". " + x : kind === "quote" ? "> " + x : "- " + x).join("\n");
    nv = v.slice(0, ls) + out + v.slice(b); s0 = ls; s1 = ls + out.length;
  }
  n.value = nv; n.focus(); n.setSelectionRange(s0, s1);
  const d = draft(th); d.text = nv; keep(d); n.dispatchEvent(new Event("input", { bubbles: true }));
}
const FMT = [["bold", "B", "Bold (Ctrl+B)"], ["italic", "I", "Italic (Ctrl+I)"], ["code", "</>", "Code (Ctrl+E)"], ["link", "Link", "Link (Ctrl+K)"], ["ul", "•", "Bulleted list"], ["ol", "1.", "Numbered list"], ["quote", "❝", "Quote"]];
// The format row folds behind "Aa" (design item 17): open or shut is kept in this browser. Shortcuts work either way; simple view has no Aa.
const fmtOpen = () => { try { return !simpleOn() && localStorage.getItem("hive.cp.fmt") === "1"; } catch (e) { return false; } };
const fmtToggle = () => { try { localStorage.setItem("hive.cp.fmt", fmtOpen() ? "0" : "1"); } catch (e) {} };
const fmtBar = th => h("span", { class: "cp-fmt", "data-key": "fmt", role: "group", "aria-label": "Formatting" }, FMT.map(([k, t, ti]) =>
  h("button", { type: "button", class: "cp-fb cp-fb-" + k, title: ti, "aria-label": ti, onmousedown: e => e.preventDefault(), onclick: () => fmt(th, k) }, t)));
// Live rendering (the person, 11052110): the text box stays a plain textarea (its text made transparent); a layer behind it
// draws the same characters styled. Nothing changes width, so the two stay aligned: the marks stay, dimmed, around styled text.
const INLINE = /(`[^`\n]+`)|(\*\*[^*\n]+\*\*|__[^_\n]+__)|(\*[^*\s][^*\n]*\*|(?<![\w])_[^_\n]+_(?![\w]))|(\[[^\]\n]+\]\([^)\n]*\))/g;
function mdInline(line, out) {
  let last = 0;
  for (const m of line.matchAll(INLINE)) {
    if (m.index > last) out.push(line.slice(last, m.index));
    const cls = m[1] ? "md-code" : m[2] ? "md-b" : m[3] ? "md-i" : "md-a";
    const k = m[4] ? 0 : m[1] || m[3] ? 1 : 2, t = m[0];   // mark width: ` and * and _ are 1, ** and __ are 2
    out.push(k ? h("span", { class: cls }, h("span", { class: "md-x" }, t.slice(0, k)), t.slice(k, -k), h("span", { class: "md-x" }, t.slice(-k))) : h("span", { class: cls }, t));
    last = m.index + m[0].length;
  }
  if (last < line.length) out.push(line.slice(last));
}
function mdLayer(text) {
  const out = []; let fence = false;
  text.split("\n").forEach((line, i, all) => {
    if (i) out.push("\n");
    if (/^\s*```/.test(line)) { fence = !fence; out.push(h("span", { class: "md-code md-fence" }, line)); return; }
    if (fence) { out.push(h("span", { class: "md-code" }, line)); return; }
    const m = /^(\s*)(?:(#{1,6}[ \t]+)|(>[ \t]?)|([-*•][ \t]+)|(\d{1,3}[.)][ \t]+))/.exec(line);
    if (m) { if (m[1]) out.push(m[1]); out.push(h("span", { class: m[2] ? "md-h" : "md-mk" }, m[0].slice(m[1].length))); line = line.slice(m[0].length); if (m[2]) { out.push(h("span", { class: "md-h" }, line)); return; } }
    mdInline(line, out);
  });
  out.push("\n");   // keeps the last empty line's height, as a textarea does
  return out;
}
/** Redraw the layer behind a text box. The box's own text turns transparent only while the layer provably lines up
 *  (same wrapped height as the box); otherwise the layer is dropped and the box shows its plain text, so it can never glitch. */
function mdSync(n) {
  const tw = n.parentNode, bd = tw && tw.querySelector(".cp-bd"); if (!bd) return;
  if (!n.value) { tw.removeAttribute("data-md"); bd.setAttribute("data-sig", ""); CP.md = false; return; }
  const fresh = bd.getAttribute("data-sig") !== n.value;
  if (fresh) { bd.replaceChildren(...mdLayer(n.value)); bd.setAttribute("data-sig", n.value); }
  // Reads that force a layout (computed style, widths, heights) run only when the text or the box's width changed:
  // typing and idle re-renders no longer reflow the whole page each time.
  const w = n.clientWidth, h = n.scrollHeight, key = w + "|" + h + "|" + n.className;
  if (!fresh && bd.getAttribute("data-geo") === key) { if (CP.md) { tw.setAttribute("data-md", ""); bd.scrollTop = n.scrollTop; } return; }
  tw.setAttribute("data-md", "");   // shown so it can be measured; dropped below if it doesn't line up
  if (bd.getAttribute("data-w") !== String(w) || !bd.style.font) {
    const c = getComputedStyle(n);
    for (const k of ["font", "padding", "lineHeight", "letterSpacing", "wordSpacing", "tabSize", "textIndent"]) bd.style[k] = c[k];
    bd.style.width = w + "px"; bd.setAttribute("data-w", String(w));   // the box's wrapping width, scrollbar excluded
  }
  const ok = Math.abs(bd.scrollHeight - h) <= 1;
  bd.setAttribute("data-geo", key);
  if (ok) bd.scrollTop = n.scrollTop;
  if (!ok) tw.removeAttribute("data-md"); CP.md = ok;   // CP.md: a redraw keeps the attribute, so the text never flashes plain
}
let mdRaf = 0;
/** One layer redraw per frame, however many keys or re-renders asked for it. */
function mdSoon(th) { if (mdRaf) return; mdRaf = requestAnimationFrame(() => { mdRaf = 0; const t = field(th); if (t) mdSync(t); }); }
/** Send a short message for the person from a button (Carry on, Answer). Shows like any other. */
export function say(th, text, to) {
  const r = to ? { who: to, why: "chosen", server: (TG.v[th] || {}).who } : recipient(th);
  queue(th, { text, files: [], reply: null, quote: null, name: "" }, r); refresh();
}
export const actions = {
  retry: it => deliver(it),
  edit: it => {
    const d = draft(it.th);
    d.text = d.text.trim() ? d.text + "\n\n" + it.text : it.text; d.files = d.files.concat(it.files); d.reply ||= it.reply; d.quote ||= it.quote; keep(d);
    OUT.splice(OUT.indexOf(it), 1); CP.focus = true; const el = field(it.th); if (el) el.value = d.text; refresh();
  },
  discard: it => { OUT.splice(OUT.indexOf(it), 1); for (const f of it.files) if (f.url) URL.revokeObjectURL(f.url); refresh(); },
};

// ---- voice memo: a record button, shown only while the voice module's service is proven working (S.app.voice) ----
// Seamless (the person, 11042046fbe8): the bar switches to recording on pointerdown, before the mic opens; pressing the
// mic again (or letting go after holding it) sends the memo with whatever is typed; × throws it away; Esc too.
// The waveform canvas sits in a data-own span so re-renders never reset it, and the timer is written straight to the DOM.
const REC = { gen: 0, state: null, rec: null, stream: null, chunks: [], t0: 0, th: undefined, sendOnStop: true, down: 0, ctx: null };
function recStart(th) {
  if (REC.state) return;
  const d = draft(th);
  if (!navigator.mediaDevices || !window.MediaRecorder) { d.err = "This browser can't record audio."; refresh(); return; }
  Object.assign(REC, { state: "starting", th, chunks: [], t0: 0, sendOnStop: true });
  const AC = window.AudioContext || window.webkitAudioContext;
  try { REC.ctx = AC ? new AC() : null; } catch (e) { REC.ctx = null; }   // made inside the gesture, so it isn't born suspended
  refresh(); startWave();
  navigator.mediaDevices.getUserMedia({ audio: true }).then(stream => {
    if (REC.state !== "starting") { stream.getTracks().forEach(t => t.stop()); return; }   // stopped before the mic opened
    const rec = new MediaRecorder(stream);
    Object.assign(REC, { rec, stream });
    rec.ondataavailable = e => { if (e.data.size) REC.chunks.push(e.data); };
    rec.onstop = () => {
      stream.getTracks().forEach(t => t.stop());
      const type = rec.mimeType || "audio/webm", ext = /mp4/.test(type) ? "m4a" : /ogg/.test(type) ? "ogg" : "webm", t = new Date(), p = n => String(n).padStart(2, "0");
      const f = new File(REC.chunks, `voice-${p(t.getHours())}${p(t.getMinutes())}${p(t.getSeconds())}.${ext}`, { type });
      const th = REC.th, go = REC.sendOnStop && f.size > 0;
      recReset();
      if (go) { addFiles(th, [f]); if (draft(th).files.some(x => x.file === f)) send(th); } else refresh();
    };
    if (REC.ctx) { try { REC.ctx.createMediaStreamSource(stream).connect(REC.an); } catch (e) {} }
    rec.start(); REC.t0 = Date.now(); REC.state = "rec"; refresh();
  }).catch(() => { recReset(); d.err = "The microphone isn't available: allow it for this page and try again."; refresh(); });
}
function recStop(sendIt) {
  if (!REC.state) return;
  REC.sendOnStop = sendIt;
  if (REC.rec && REC.rec.state !== "inactive") REC.rec.stop();
  else { recReset(); refresh(); }
}
function recReset() {
  if (REC.ctx) REC.ctx.close().catch(() => {});
  Object.assign(REC, { state: null, rec: null, stream: null, chunks: [], ctx: null, an: null, down: 0 });
}
// Press on the mic: start at once. Held past 450 ms and let go: that was push-to-talk, so send.
function micDown(th, e) {
  if (e.button !== 0 || REC.state) return;
  REC.down = Date.now(); recStart(th);
  window.addEventListener("pointerup", () => { REC.upAt = Date.now(); if (REC.down && Date.now() - REC.down > 450 && REC.th === th) recStop(true); }, { once: true });
}
// Live waveform, drawn every frame: the mic level (fast attack, slower fall) adds a bar every 45 ms and the bars glide left between.
function startWave() {
  const ctx = REC.ctx, gen = ++REC.gen; let an = null;
  if (ctx) { an = ctx.createAnalyser(); an.fftSize = 512; an.smoothingTimeConstant = 0; REC.an = an; if (ctx.state === "suspended") ctx.resume().catch(() => {}); }
  const buf = new Uint8Array(512), lv = [], STEP = 45, BW = 3;
  let lvl = 0, last = performance.now();
  const frame = now => {
    if (!REC.state || REC.gen !== gen) return;
    if (an && REC.state === "rec") {
      an.getByteTimeDomainData(buf);
      let sq = 0; for (const v of buf) sq += (v - 128) * (v - 128);
      const x = Math.min(1, Math.sqrt(sq / buf.length) / 40);
      lvl = x > lvl ? x : lvl * 0.85 + x * 0.15;
    }
    while (now - last > STEP) { last += STEP; lv.push(lvl); if (lv.length > 300) lv.shift(); }
    const c = document.querySelector(".cp-wave"), tm = document.querySelector(".cp-rtime");
    if (tm) tm.textContent = recTime();
    if (c) {
      const w = c.clientWidth, hgt = c.clientHeight, dpr = window.devicePixelRatio || 1, cw = Math.round(w * dpr), chh = Math.round(hgt * dpr);
      if (c.width !== cw || c.height !== chh) { c.width = cw; c.height = chh; }
      const g = c.getContext("2d"); g.setTransform(dpr, 0, 0, dpr, 0, 0); g.clearRect(0, 0, w, hgt);
      g.fillStyle = c._col || (c._col = getComputedStyle(c).color || "#888");
      const off = ((now - last) / STEP) * BW, n = Math.ceil(w / BW) + 1;
      for (let i = 0; i < n; i++) {
        const v = i < lv.length ? lv[lv.length - 1 - i] : 0, bh = Math.max(2, v * hgt), xx = w - (i + 1) * BW - off;
        if (xx < -BW) break;
        g.globalAlpha = i < lv.length ? 1 : 0.35; g.fillRect(xx, (hgt - bh) / 2, 2, bh);
      }
      g.globalAlpha = 1;
    }
    requestAnimationFrame(frame);
  };
  requestAnimationFrame(frame);
}
const recTime = () => { const s = REC.t0 ? Math.floor((Date.now() - REC.t0) / 1000) : 0; return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`; };
function recBar(th) {
  if (!(S.app && S.app.voice)) return null;
  // Keyed, so the mic's node isn't morphed into × under the pointer; and the click that ends a press is ignored.
  const keyed = (k, b) => { b.setAttribute("data-key", k); return b; }, guard = f => () => { if (Date.now() - (REC.upAt || 0) >= 500) f(); };
  if (REC.state && REC.th === th) return [
    keyed("rx", button("Discard the voice memo", guard(() => recStop(false)), { kind: "icon", size: "s", icon: "close", cls: "cp-rx", title: "Throw this recording away (Esc)." })),
    h("span", { class: "cp-wavebox", "data-key": "wave", "data-own": "", "data-sig": "w", "data-state": REC.state }, h("canvas", { class: "cp-wave", "aria-label": "Live recording level" })),
    h("span", { class: "meta cp-rtime", "data-key": "rtime", "aria-live": "off" }, recTime()),
    keyed("rec", button("Send the voice memo", guard(() => recStop(true)), { kind: "icon", size: "s", icon: "stop", cls: "cp-rec", title: "Stop and send the voice memo with what you've typed." }))];
  const b = keyed("mic", button("Record a voice memo", guard(() => recStart(th)), { kind: "icon", size: "s", icon: "mic", cls: "cp-mic", title: "Record a voice memo: press to start, press again to send, or hold to talk and let go. It is transcribed and summarized on the machine's GPU." }));
  b._on = { ...b._on, pointerdown: e => micDown(th, e) };
  return b;
}

// ---- files ----
function addFiles(th, list) {
  const d = draft(th); d.err = "";
  for (let f of list) {
    if (f.size > 100 * 1048576) { d.err = `${f.name} is over 100 MB, so it can't be attached.`; continue; }
    if (/^image\.\w+$/i.test(f.name)) { const t = new Date(), p = n => String(n).padStart(2, "0"); f = new File([f], `pasted-${p(t.getHours())}${p(t.getMinutes())}${p(t.getSeconds())}.${f.name.split(".").pop()}`, { type: f.type }); }
    d.files.push({ file: f, name: f.name, size: f.size, url: IMGNAME.test(f.name) ? URL.createObjectURL(f) : null });
  }
  refresh();
}
const hasFiles = e => e.dataTransfer && [...(e.dataTransfer.types || [])].includes("Files");
/** Props for the element files can be dropped on (the whole conversation). */
export function dropZone(th) {
  return {
    "data-drag": CP.drag ? "" : null,
    ondragover: e => { if (!hasFiles(e)) return; e.preventDefault(); if (!CP.drag) { CP.drag = true; refresh(); } },
    ondragleave: (e, n) => { if (CP.drag && !n.contains(e.relatedTarget)) { CP.drag = false; refresh(); } },
    ondrop: e => { if (!hasFiles(e)) return; e.preventDefault(); CP.drag = false; addFiles(th, e.dataTransfer.files); CP.focus = true; refresh(); },
  };
}

// ---- model picker: shows what the message will start on (default, the picked one, or an @model typed in the text) ----
const MODEL_LABELS = { opus: "Opus", sonnet: "Sonnet", haiku: "Haiku", fable: "Fable", astra: "Astra" };
function typedModel(text) { const w = named(text); return MODEL_WORDS.includes(w) ? w : null; }
/** A new conversation's focus (the person, 110715182bab): the home card it is filed under, Misc when none. Prefilled when it starts from a focus. */
function focusPick(d) {
  const fs = foci(); if (!fs.length && !d.focus) return null;
  return h("select", { class: "cp-focus", "aria-label": "Focus", title: "Which focus this conversation is filed under on the home; No focus leaves it in Misc",
    onchange: e => { d.focus = e.target.value; keep(d); refresh(); } },
    h("option", { value: "", selected: !d.focus ? "" : null }, "No focus"),
    fs.map(f => h("option", { value: f.id, selected: f.id === d.focus ? "" : null }, f.name || f.id)),
    d.focus && !fs.some(f => f.id === d.focus) ? h("option", { value: d.focus, selected: "" }, d.focus) : null);
}
// ---- @ names: agents, foci and threads, each with where it is and what it's on (the person, 11071752e4c2) ----
/** The words a row is found by, lower-cased: name, model, thread name and title, its status line, its focus. */
const words = (...xs) => xs.filter(Boolean).join(" ").toLowerCase();
const agentThread = n => { const L = S.running.get(n), s = S.lastSess[n]; return (L && L.thread) || (s && s.thread) || null; };
const modelOf = n => { const r = agentCard(n)[0]; return r && r.v !== "login default" ? r.short : ""; };
/** The words a thread's tags give it (R8's `tags` on posts, "kind:id"): the ids, and the focus's or thread's name for each. */
function tagWords(th) {
  const d = th && S.threads.get(th), out = new Set();
  for (const p of d ? d.posts.slice(-80) : []) for (const t of Array.isArray(p.tags) ? p.tags : []) {
    const [k, ...rest] = String(t).split(":"), id = rest.join(":") || k; out.add(id);
    if (k === "focus") { const f = focusById(id); if (f) out.add(focusName(f)); } else if (k === "thread") out.add(threadTitle(id));
  }
  return [...out].join(" ");
}
/** What an agent says about itself: `hive me --about` first, else its `hive me` line. */
const selfLine = n => { const m = S.agentStatus && S.agentStatus[n]; return (m && (m.about || m.text)) || ""; };
/** One row: {kind: agent | focus | thread | word, word (what @ inserts), head (its name words), hay (every word it is found by), ...what the row shows}. */
function agentRow(n) {
  const th = agentThread(n), f = th ? focusById(focusOf(th)) : null, me = S.agentStatus && S.agentStatus[n];
  const line = (th && threadLine(th)) || selfLine(n);
  return { kind: "agent", word: n, model: modelOf(n), th, title: th ? threadTitle(th) : "", line, focus: f ? focusName(f) : "", head: words(n, th, th && threadTitle(th)),   // found through its thread's name, it is as close as the thread
    hay: words(n, modelOf(n), th, th && threadTitle(th), line, me && me.about, me && me.text, f && focusName(f), tagWords(th)) };
}
function focusRow(f) {
  return { kind: "focus", word: f.id, name: focusName(f), keeper: f.keeper || null, aim: f.aim || "", head: words(f.id, focusName(f)),
    hay: words(f.id, f.name, f.aim, f.keeper, f.thread, f.summary && f.summary.text, f.thread && tagWords(f.thread), "focus") };
}
function threadRow(th) {
  const f = focusById(focusOf(th));
  return { kind: "thread", word: th, title: threadTitle(th), line: threadGist(th), focus: f ? focusName(f) : "", head: words(th, threadTitle(th)),
    hay: words(th, threadTitle(th), threadLine(th), f && focusName(f), tagWords(th), "thread conversation") };
}
/** A conversation's line in the @ list: its status line, or else who spoke last and the first words they said (R10 #3: a row with only its id
 *  told nobody which conversation it was). */
function threadGist(th) {
  const l = threadLine(th); if (l) return l;
  const d = S.threads.get(th), p = d && d.posts[d.posts.length - 1]; if (!p) return "";
  const t = mdPlain(String(displayText(p) || "")).split("\n").map(x => x.trim()).find(Boolean) || "";
  return t ? `${p.from === "person" ? "You" : p.from}: ${short(t, 90)}` : "";
}
/** Rows in the order they're offered: this thread's agents, running agents, the foci, recent agents, then threads active this week, new and the model words. */
function acRows(th) {
  const seen = new Set(), out = [], add = r => { if (r && !seen.has(r.word)) { seen.add(r.word); out.push(r); } };
  if (th) for (const n of threadPeople(th)) if (isAgent(n)) add(agentRow(n));
  for (const n of S.running.keys()) add(agentRow(n));
  for (const f of foci()) add(focusRow(f));
  for (let i = S.sessions.length - 1; i >= 0 && i > S.sessions.length - 40; i--) if (S.sessions[i].name) add(agentRow(S.sessions[i].name));
  const week = now() - 7 * 86400e3;
  for (let i = S.posts.length - 1; i >= 0 && S.posts[i].t > week; i--) { const t = S.posts[i].th; if (t && t !== th && listed(t)) add(threadRow(t)); }
  add({ kind: "word", word: "new", head: "new", hay: "new a new agent" });
  MODEL_WORDS.forEach(w => add({ kind: "word", word: w, head: w, hay: w + " model" }));
  return out;
}
/** The @ list's three groups, in this order, and how many rows each shows. A "word" row (new, a model) sits with the agents. */
const GROUPS = [["agent", "Agents", 5], ["focus", "Focuses", 3], ["thread", "Conversations", 4]];
const groupOf = r => (r.kind === "word" ? "agent" : r.kind);
/** How close a row is to the typed words: a name that starts with a word beats one that holds it, which beats a title, which
 *  beats a hit only in its status line, tags or summary. */
function closeness(r, ws) {
  let n = 0;
  for (const w of ws) n += r.word.toLowerCase().startsWith(w) ? 4 : r.word.toLowerCase().includes(w) ? 3 : (r.head || "").includes(w) ? 2 : 1;
  return n;
}
/** The rows for what's typed after @, grouped Agents, Focuses, Conversations, the closest first in each and the closest group first. Nothing typed: this thread's
 *  agents, the foci, then other agents, and a new one. Typed: every word must appear in the row's words, so "ui design" finds the
 *  agent whose thread's status line, tags or focus say so. */
function acNames(th, q) {
  const rows = acRows(th), ws = q.split(/\s+/).filter(Boolean);
  let pick;
  if (!ws.length) {
    const here = new Set(th ? threadPeople(th) : []), own = rows.filter(r => r.kind === "agent" && here.has(r.word)), fx = rows.filter(r => r.kind === "focus");
    const rest = rows.filter(r => r.kind === "agent" && !here.has(r.word)).slice(0, Math.max(0, 9 - own.length - fx.length));
    pick = [...own, ...rest, rows.find(r => r.word === "new"), ...fx].filter(Boolean);
    return GROUPS.flatMap(([k]) => pick.filter(r => groupOf(r) === k));
  }
  const hit = rows.filter(r => ws.every(w => r.hay.includes(w))).map((r, i) => ({ r, i, c: closeness(r, ws) }));
  hit.sort((a, b) => b.c - a.c || a.i - b.i);
  const best = k => { const x = hit.find(x => groupOf(x.r) === k); return x ? x.c : -1; };   // the group holding the closest row leads; ties keep Agents, Focuses, Conversations
  return GROUPS.map((g, i) => [g, i]).sort((a, b) => best(b[0][0]) - best(a[0][0]) || a[1] - b[1])
    .flatMap(([[k, , max]]) => hit.filter(x => groupOf(x.r) === k).slice(0, max).map(x => x.r));
}
/** The @ list's rows with a small heading over each group (the # list has one kind, so no headings). */
function acList(th) {
  const a = CP.ac, out = []; let last = null;
  if (a.none) return [h("div", { class: "cp-none meta", role: "status", "data-key": "none" }, `No one matches “${a.q.trim()}”`)];
  a.items.forEach((r, i) => {
    const g = a.mark === "#" ? null : groupOf(r);
    if (g && g !== last) { out.push(h("div", { class: "menu-h", role: "presentation", "data-key": "h:" + g }, GROUPS.find(x => x[0] === g)[1])); last = g; }
    out.push(h("button", { type: "button", role: "option", class: "cp-opt", "data-key": r.word, "data-kind": r.kind, "aria-selected": String(i === a.i), onclick: () => acPick(th, r.word) }, ...optRow(r, a.mark || "@")));
  });
  return out;
}
/** Threads that start with q, most recently active first. */
function acThreads(q) {
  const out = [];
  for (let i = S.posts.length - 1; i >= 0 && out.length < 8; i--) { const t = S.posts[i].th; if (t && !out.includes(t) && t.toLowerCase().startsWith(q)) out.push({ kind: "thread", word: t, title: threadTitle(t), line: threadLine(t) }); }
  return out;
}
/** The list opens at @ or # and follows what's typed; after @ the words may have spaces in them (a search), until the first word is
 *  a name the list knows (then the person has their name and is writing the message), or nothing matches any more. */
function acCheck(el, th) {
  const before = el.value.slice(0, el.selectionStart), m = simpleOn() ? null : before.match(/(?:^|\s)@([\w.-]*(?: [\w.-]*){0,3})$/) || before.match(/(?:^|\s)(#)([\w.-]*)$/);
  const mark = m ? (m[1] === "#" ? "#" : "@") : "@", q = m ? (mark === "#" ? m[2] : m[1]) : "";
  const first = q.split(" ")[0].toLowerCase(), done = mark === "@" && first && (q.includes(" ") || q === first) && !!wordTarget(first, th) || (mark === "@" && !!threadWord(first, th) && q !== first);
  const items = m && !done ? (mark === "#" ? acThreads(q.toLowerCase()) : acNames(th, q.toLowerCase())) : [];
  const at = el.selectionStart - q.length - 1;
  const next = items.length && !(items.length === 1 && items[0].word === q) ? { q, mark, at, items, i: 0 }
    : m && !done && mark === "@" && q.trim() && !items.length ? { q, mark, at, items: [], i: 0, none: true } : null;   // R10 #8: say nothing matches rather than vanish
  const key = a => a && (a.none ? "\u0000none:" + a.q.trim() : a.items.map(r => r.word).join(" "));
  if (key(next) !== key(CP.ac)) { CP.ac = next; return true; }
  if (next) CP.ac.at = next.at, CP.ac.q = next.q;
  return false;
}
function acPick(th, n) {
  const el = field(th), a = CP.ac; if (!el || !a) return;
  const d = draft(th), v = el.value.slice(0, a.at) + (a.mark || "@") + n + " " + el.value.slice(a.at + 1 + a.q.length);
  el.value = v; d.text = v; keep(d);
  const c = a.at + n.length + 2; el.focus(); el.setSelectionRange(c, c);
  CP.ac = null; refresh();
}
// ---- past messages ----
/** What the person has sent, from every thread and from any door, newest first, each text once. Messages still on their way count: Up right after Enter brings back that one. */
function pastSent() {
  const out = [], seen = new Set(), add = t => { t = t.trim(); if (t && !seen.has(t)) { seen.add(t); out.push(t); } };
  for (let i = OUT.length - 1; i >= 0; i--) if (!OUT[i].id || !S.byId[OUT[i].id]) add(OUT[i].text);
  for (let i = S.posts.length - 1; i >= 0 && out.length < 500; i--) if (S.posts[i].from === "person") { const t = typedText(S.posts[i]).replace(QUOTE_HEAD, ""); if (!/^Attached: /.test(t)) add(t); }   // a files-only post's "Attached: <names>" line was never typed
  return out;
}
function recallTo(th, n, text) {
  const d = draft(th); d.text = text; keep(d);
  n.value = text; n.setSelectionRange(text.length, text.length); refresh();   // morph leaves a focused field alone
}
/** Still on what Up, Down or Tab put there: not edited, and the caret not moved into it (then the arrows are the caret's again). */
const walking = n => { const w = CP.hist; return !!w && n.value === (w.i < 0 ? w.was : w.list[w.i]) && n.selectionStart === n.value.length && n.selectionEnd === n.value.length; };
/** Up, Down: true if it took the key. Up starts only from an empty box, so it never takes the place of a draft. */
function histStep(th, n, up) {
  if (!walking(n)) {
    if (!up || n.value.trim()) return false;
    const list = pastSent(); if (!list.length) return false;
    CP.hist = { list, i: -1, was: n.value, tab: false };
  }
  const w = CP.hist;
  if (up ? w.i >= w.list.length - 1 : w.i < 0) return true;
  w.i += up ? 1 : -1;
  recallTo(th, n, w.i < 0 ? w.was : w.list[w.i]);
  if (w.i < 0) CP.hist = null;
  return true;
}
/** Tab: the newest past message that starts with what's typed; Tab again, the one before. False with nothing to offer, so Tab still leaves the box. */
function histTab(th, n) {
  if (walking(n) && CP.hist.tab) { const w = CP.hist; w.i = (w.i + 1) % w.list.length; recallTo(th, n, w.list[w.i]); return true; }
  const typed = n.value, low = typed.toLowerCase();
  if (!typed.trim() || n.selectionStart !== typed.length || n.selectionEnd !== typed.length) return false;
  const list = pastSent().filter(t => t.length > typed.length && t.toLowerCase().startsWith(low));
  if (!list.length) return false;
  CP.hist = { list, i: 0, was: typed, tab: true };
  recallTo(th, n, list[0]);
  return true;
}
/** What a row of the @ list or the To menu shows: the dot, the word, then agent · model · its thread · that thread's status line;
 *  a focus with who keeps it; a thread with its title and status line. No working / not-working words: the dot says that. */
function optRow(r, mark = "@", { here } = {}) {
  const m = (x, cls = "meta") => (x ? h("span", { class: cls }, x) : null), sep = x => (x ? h("span", { class: "meta cp-sep", "aria-hidden": "true" }, "·") : null);
  if (r.kind === "focus") return [h("span", { class: "cp-fx", "aria-hidden": "true" }, "◆"), h("b", null, mark + r.word), m(r.name !== r.word ? r.name : ""), sep(1), m(r.keeper ? `kept by ${r.keeper}` : "nobody keeping it: a new agent in its thread", "meta cp-line")];
  if (r.kind === "thread") return mark === "#" ? [h("span", { class: "cp-fx", "aria-hidden": "true" }, "#"), h("b", null, mark + r.word), m(r.title !== r.word ? r.title : "", "meta cp-title"), sep(r.line), m(r.line)]
    : [h("span", { class: "cp-fx", "aria-hidden": "true" }, "#"), h("b", { class: "cp-ttl" }, r.title || r.word), m("@" + r.word, "meta cp-id"), sep(r.line), m(r.line, "meta cp-line")];   // the title leads, as on the rail; the id is what gets inserted
  if (r.kind === "word") return [statusDot("none"), h("b", null, mark + r.word), m(r.word === "new" ? "a new agent, with this conversation to read" : "a new agent on this model")];
  const inHere = here && r.th === here;
  return [statusDot(agentStatus(r.word).key, { live: S.running.has(r.word) }), h("b", null, mark + r.word), m(r.model, "meta cp-model"), sep(r.model && r.th && !inHere),
    inHere ? null : m(r.th ? (r.title && r.title !== r.th ? r.title : r.th) : "", "meta cp-title"), sep(r.line), m(r.line, "meta cp-line")];
}

// ---- secret box ----
const SECRET_NAME = /^[A-Za-z0-9][A-Za-z0-9_.-]{1,63}$/, NOT_NAME = /\.(md|py|js|mjs|json|jsonl|sh|txt|png|toml|ya?ml|html|css)$/i;
/** The names in one post that it asks to have saved: backticked names in a sentence that both says secret/token/lock/API key and
 * asks for it (save, paste, add...), or in the list a "Save these with the lock:" opens. A post only talking about secrets names nothing
 * (test_login and ring were offered as secrets because a post mentioned the box, the person, 11050054b0b9). */
function askedNames(text) {
  const out = []; let list = false;
  for (const s of String(text).split(/(?<=[.!?])\s+|\n/)) {
    const prose = s.replace(/`[^`]*`/g, ""), item = /^\s*([-*]|\d+\.)\s/.test(s);
    const ask = /\b(secrets?|tokens?|lock|api keys?)\b/i.test(prose) && /\b(save|paste|add|put|store|drop|enter)\b/i.test(prose);
    if (ask || (list && item)) for (const m of s.matchAll(/`([^`\s]+)`/g)) {
      const n = m[1]; if (SECRET_NAME.test(n) && !NOT_NAME.test(n) && !/^[0-9a-f]{5,12}$/.test(n)) out.push(n);
    }
    if (ask) list = prose.trimEnd().endsWith(":"); else if (!item && s.trim()) list = false;
  }
  return out;
}
/** The secret names agents asked for in this thread, newest ask first, without the ones already saved (the person, 11041441b379). */
export function wantedSecrets(th, have = []) {
  const d = th && S.threads.get(th); if (!d) return [];
  const saved = new Set(have.map(r => r.name)), out = [];   // by name: board times carry no zone, so "saved since the ask" can't be told reliably
  for (const p of d.posts.slice(-12).reverse()) {
    if (p.from === "person" || !(isAgent(p.from) || /^[a-z][0-9a-f]{4,}$/.test(p.from))) continue;
    const named = askedNames(p.text);
    for (const n of named) if (!saved.has(n) && !out.includes(n)) out.push(n);
    if (named.length) break;   // the newest ask only, even if it's all saved: an older one may name secrets the agent has since dropped
  }
  return out.slice(0, 8);
}
async function openSecrets(th) {
  SB.open = true; SB.say = ""; SB.rows = [{ name: "", value: "" }]; refresh();
  let have = []; try { have = await jget("/api/secrets"); } catch (e) {}
  const want = wantedSecrets(th, Array.isArray(have) ? have : []);
  if (want.length && SB.open && !SB.rows.some(r => r.name || r.value)) { SB.rows = want.map(name => ({ name, value: "", asked: true })); refresh(); }
  queueMicrotask(() => document.querySelector(SB.rows[0].asked ? ".cp-sval" : ".cp-sname")?.focus());
}
const closeSecrets = () => { SB.open = false; SB.rows = [{ name: "", value: "" }]; refresh(); };
const dropSecrets = () => { SB.say = ""; closeSecrets(); };   // the person closed it: an old complaint goes with it, unlike the "Saved" line a save leaves
async function saveSecret() {
  const rows = SB.rows.filter(r => r.name.trim() || r.value);
  if (!rows.length || rows.some(r => !r.name.trim())) { SB.say = "Give it a name and a value."; SB.tone = "bad"; return refresh(); }
  const todo = rows.filter(r => r.value);
  if (!todo.length) { SB.say = "Paste a value first."; SB.tone = "bad"; return refresh(); }
  SB.busy = true; SB.say = ""; refresh();
  const done = [];
  try { for (const r of todo) { await post("/api/secret", { name: r.name.trim(), value: r.value }); done.push(r.name.trim()); r.value = ""; } }
  catch (e) { SB.say = (done.length ? `Saved ${done.join(", ")}. ` : "") + e.message; SB.tone = "bad"; }
  SB.rows = SB.rows.filter(r => !done.includes(r.name.trim()));
  if (!SB.say) {
    SB.say = `Saved ${done.join(", ")}. ${done.length > 1 ? "They" : "It"} can't be shown again; tell the agent ${done.length > 1 ? "their names" : "its name"}.`; SB.tone = "good";
    if (!SB.rows.length) closeSecrets(); else SB.open = true;
  }
  SB.busy = false; refresh();
}
function secretRow() {
  const one = SB.rows.length === 1, asked = SB.rows.some(r => r.asked);
  return h("div", { class: "cp-secret", "data-key": "secret" },
    h("div", { class: "cp-shead", "data-key": "head" },   // the buttons on top, clear of the rows (the person, 11041445a01e)
      h("p", { class: "meta cp-sask" }, asked ? (one ? "The agent asked for this one. Paste its value:" : "The agent asked for these. Paste each value; empty ones are skipped:") : "Saved on the machine, never posted in chat."),
      button(one ? "Save secret" : "Save secrets", saveSecret, { kind: "primary", size: "s", busy: SB.busy, title: "Saved to ~/.config/h2/secrets/<name>, mode 600. Never posted; the board only gets its name." }),
      h("button", { type: "button", class: "cp-x", title: "Close", "aria-label": "Close the secret box", onclick: dropSecrets }, "×")),
    SB.rows.map((r, i) => h("div", { class: "cp-srow", "data-key": "r" + i },
      input({ class: "input cp-sname", placeholder: "Name, like github-token", "aria-label": "Secret name", autocomplete: "off", value: r.name, oninput: (e, n) => { r.name = n.value; } }),
      input({ class: "input cp-sval", type: "password", placeholder: r.asked ? "Value for " + r.name : "Value", "aria-label": "Secret value" + (r.name ? " for " + r.name : ""), autocomplete: "new-password", value: r.value, oninput: (e, n) => { r.value = n.value; },
        onkeydown: e => { if (e.key === "Enter") { e.preventDefault(); saveSecret(); } else if (e.key === "Escape") dropSecrets(); } }))));
}

// ---- pieces ----
// The bar's menus (To, +, Aa, model) close on a click outside their pop, and on Esc (cancelReply).
document.addEventListener("pointerdown", e => {
  if (CP.menu && !e.target.closest?.(".cp .pop")) { CP.menu = false; refresh(); }
}, true);
const toggle = m => () => { CP.menu = CP.menu === m ? false : m; CP.ac = null; refresh(); };
const mono = n => /^[a-z][0-9a-f]{4,}$/.test(n) ? n.slice(1, 3) : String(n).slice(0, 2);
/** The avatar box: two letters and the status dot for an agent, a glyph for anything else. */
const av = (n, glyph) => h("span", { class: "av", "data-size": "s", "aria-hidden": "true" }, glyph || mono(n), glyph ? null : statusDot(agentStatus(n).key, { live: S.running.has(n) }));
const menuHead = (t, key) => h("div", { class: "menu-h", role: "presentation", "data-key": key || "h:" + t }, t);
const menuSep = key => h("div", { class: "menu-sep", role: "presentation", "data-key": key });
/** One menu row: a bold label over a small line. off: no backing yet, so it shows dimmed with the title "not yet". */
function menuItem(label, sub, onClick, { ic, key, cls, off, role = "menuitem", sel, right, title } = {}) {
  return h("button", { type: "button", class: ["menu-i", cls], role, "data-key": key || label, "aria-disabled": off ? "true" : null, title: off ? "not yet" : title || null,
    "aria-selected": role === "option" ? String(!!sel) : null, "aria-checked": role === "menuitemcheckbox" ? String(!!sel) : null, onclick: off ? null : onClick },
    ic ? icon(ic) : null, h("span", { class: "menu-i-t" }, h("b", null, label), sub ? h("small", null, sub) : null), right ? h("span", { class: "menu-i-r" }, right) : null);
}

// Text size in conversations (Aa): --read-scale, kept in this browser.
const SIZES = [["0.9", "Small"], ["1", "Default"], ["1.15", "Large"], ["1.3", "Larger"]];
const readScale = () => { try { const v = localStorage.getItem("hive.cp.read"); return SIZES.some(s => s[0] === v) ? v : "1"; } catch (e) { return "1"; } };
function setRead(v) { try { localStorage.setItem("hive.cp.read", v); } catch (e) {} document.documentElement.dataset.read = v; refresh(); }
document.documentElement.dataset.read = readScale();
const sizeWord = () => SIZES.find(s => s[0] === readScale())[1];

/** The To button: who this goes to, as an avatar and a name. Why (a reply, a name typed) is in its title. */
function toChip(th, r) {
  const f = r.to && focusWord(r.to), n = r.who, btw = r.why === "btw";
  const label = btw ? "a new agent, on the side" : f ? focusName(focusById(f.id)) + (n && n !== "new" ? ` → ${n}` : " → a new agent") : n === "new" ? "a new agent" : n || "…";
  const why = { reply: "you're replying to it", named: "named in your message", chosen: "you chose it" }[r.why];
  return h("button", { type: "button", class: "cp-to", "aria-haspopup": "listbox", "aria-expanded": String(CP.menu === "to"),
    title: btw ? "A side question: it answers in its own thread, and this one's agent never sees it." : `Who this goes to${why ? ` (${why})` : ""}. Change it here, or type @ and a name.`, onclick: toggle("to") },
    h("span", { class: "cp-hp" }, "To"), f ? av(null, "◆") : n && n !== "new" && isAgent(n) ? av(n) : av(null, "+"), h("b", null, label), icon("chevron"));
}
function toMenu(th, r) {
  const d = draft(th), names = threadPeople(th).filter(isAgent), pick = n => { d.to = n; keep(d); CP.menu = false; refresh(); field(th)?.focus(); };
  const chosen = r.why === "btw" ? "btw" : r.to || r.who;
  const opt = (rr, glyph, label, sub) => h("button", { type: "button", role: "option", class: "menu-i cp-opt", "data-kind": rr.kind, "data-key": rr.word, "aria-selected": String(chosen === rr.word), onclick: () => pick(rr.word) },
    glyph ? av(null, glyph) : av(rr.word), h("span", { class: "menu-i-t" }, h("b", null, label), sub ? h("small", null, sub) : null));
  const agentOpt = n => { const a = agentRow(n), where = a.th && a.th !== th ? (a.title && a.title !== a.th ? a.title : a.th) : "";
    return opt(a, null, n, [a.model, where, a.line, n === partner(th) ? "you've been talking to it" : ""].filter(Boolean).join(" · ")); };
  return h("div", { class: "menu cp-menu", role: "listbox", "aria-label": "Send to", "data-side": "top-end", "data-w": "wide", "data-key": "menu" },
    menuHead("Send to"),
    names.map(agentOpt),
    opt({ kind: "word", word: "new" }, "+", "A new agent", "Starts here, with this conversation to read"),
    foci().length ? menuHead("A focus: its keeper gets it, marked with the focus", "h:foci") : null,
    foci().map(f => { const x = focusRow(f); return opt(x, "◆", x.name, x.keeper ? `kept by ${x.keeper}` : "nobody keeping it: a new agent in its thread"); }),
    menuSep("sep"),
    opt({ kind: "word", word: "btw" }, "+", "A new agent, on the side", "Answers in its own conversation; nobody here sees it"),
    h("p", { class: "cp-menu-n", "data-key": "note" }, r.why === "partner"
      ? `${r.server} wrote here last, so a message with no name would wake it. You've been talking to ${r.who}, so Hive sends it there.`
      : "Typing @ in your message searches agents, foci and threads; a name there sends it to that agent instead."));
}

// ---- model: what the message starts (or moves) its agent on; the default, the picked one, or an @model typed in the text ----
const MODELS = [["opus", "Opus 5.5", "Most capable, uses the most of your plan"], ["sonnet", "Sonnet 5.5", "Fast and capable, for most jobs"], ["haiku", "Haiku 5.5", "Quickest, for small jobs"], ["fable", "Fable 5.1", ""], ["astra", "Astra", ""]];
function modelPick(d, r) {
  // An agent already running stays on its model for this run; a pick here is kept by the starter and holds from its next run.
  const who = r && r.who, live = who && who !== "new" && isRunning(who);
  const def = (S.app && S.app.default_model) || "", typed = typedModel(d.text), cur = typed || d.model || "";
  // A sleeping agent wakes on the model it last ran on, not the default for new agents.
  const known = who && who !== "new" && !MODEL_WORDS.includes(who), row = known && agentCard(who).find(x => x.k === "Model");
  const nm = row && (row.v === "login default" ? "Default model" : row.v);
  const first = live ? nm || "Its model" : row ? nm : def ? MODEL_LABELS[def] || def : "Model";
  const note = live ? `${who} is on ${row ? row.v : "its model"} for this run` : row ? `${who} wakes on ${row.v}` : def ? `New agents start on ${MODEL_LABELS[def] || def}` : "The model a new agent starts on";
  const label = cur ? MODEL_LABELS[cur] || cur : first, open = CP.menu === "model";
  const pick = v => { d.model = v; keep(d); CP.menu = false; refresh(); };
  return h("div", { class: "pop cp-mpop", "data-key": "model" },
    h("button", { type: "button", class: "dd cp-model", "data-picked": d.model ? "" : null, "data-value": cur, "aria-haspopup": "listbox", "aria-expanded": String(open), "aria-label": "Model: " + label, title: note + ". Pick another to change it.", onclick: toggle("model") }, label, icon("chevron")),
    open ? h("div", { class: "menu cp-mmenu", role: "listbox", "aria-label": "Model", "data-side": "top-end", "data-w": "wide", "data-key": "mm" },
      menuHead(note, "h"),
      [["", first, live || row ? "Leave it as it is" : "The default"], ...MODELS.filter(([v]) => live || row || v !== def).map(([v, l, s]) => [v, l, s])].map(([v, l, s]) =>
        menuItem(l, s, () => pick(v), { role: "option", key: "m:" + v, sel: cur === v, cls: "cp-mopt" })),
      menuSep("sep"),
      h("p", { class: "cp-menu-n", "data-key": "foot" }, "It holds on that agent from its next run. Typing @opus or @sonnet in the message does the same.")) : null);
}

/** The lock's click: open the secret box, or say why it can't open in limited mode. */
function secretClick(th) {
  if (!can("secrets")) { SB.open = false; SB.tone = "bad"; SB.say = "Saving a secret needs full mode, which ends after 30 minutes without using the app. Press \"Limited · sign in for full\" at the top, then press the lock again."; refresh(); return; }
  if (SB.open) dropSecrets(); else openSecrets(th);
}
const secretsOn = () => can("secrets") || !!(S.app && S.app.secrets_on && S.app.mode === "expired");

/** +: files, a voice memo, a secret, a call; at home, Name it…; on a phone, Text size (Aa is hidden there). */
function addPop(th, d) {
  const open = CP.menu === "add", then = f => (e, b) => { const cp = b.closest(".cp"); CP.menu = false; refresh(); f(cp); };
  const voice = !!(S.app && S.app.voice), call = phoneReady() && !inCall();
  return h("div", { class: "pop cp-addpop", "data-key": "add" },
    h("button", { type: "button", class: "btn cp-attach", "data-kind": "icon", "data-size": "s", "aria-label": "Attach", title: "Attach files, a voice memo or a secret", "aria-haspopup": "menu", "aria-expanded": String(open), onclick: toggle("add") }, icon("plus")),
    open ? h("div", { class: "menu cp-addmenu", role: "menu", "aria-label": "Add to the message", "data-side": "top-start", "data-key": "am" },
      menuItem("Files from this device", "Or paste or drop them in the box", then(cp => cp.querySelector(".cp-pick").click()), { ic: "file", key: "files" }),
      menuItem("Voice memo", "Transcribed on this computer", then(() => recStart(th)), { ic: "mic", key: "voice", off: !voice }),
      menuItem("A secret", "Saved on this computer; only its name is posted", then(() => secretClick(th)), { ic: "lock", key: "secret", off: !secretsOn() }),
      menuItem("Voice call", th ? "A new agent picks up, reads this conversation and talks with you" : "Talk with the Hive", then(() => startCall(th ? { thread: th } : {})), { ic: "phone", key: "call", cls: "cp-call", off: !call }),
      !th && !simpleOn() ? [menuSep("sep-n"), menuItem("Name it…", "Otherwise it is called by its first words", then(() => { d.naming = true; refresh(); queueMicrotask(() => document.querySelector(".cp-name")?.focus()); }), { ic: "edit", key: "name" })] : null,
      h("div", { class: "only-phone", "data-key": "ph", role: "presentation" }, menuSep("sep-t"),
        menuItem("Text size", null, () => { const i = SIZES.findIndex(s => s[0] === readScale()); setRead(SIZES[(i + 1) % SIZES.length][0]); }, { key: "size", right: sizeWord(), title: "Text size in conversations; press again for the next size" }))) : null);
}
/** Aa: text size in conversations, and the formatting bar. */
function aaPop(th) {
  const open = CP.menu === "aa", cur = readScale();
  return h("div", { class: "pop cp-aapop hide-phone", "data-key": "aa" },
    h("button", { type: "button", class: "btn cp-aa", "data-kind": "icon", "data-size": "s", "aria-label": "Text size and formatting", title: "Text size and formatting", "aria-haspopup": "menu", "aria-expanded": String(open), "data-on": fmtOpen() ? "" : null, onmousedown: e => e.preventDefault(), onclick: toggle("aa") }, "Aa"),
    open ? h("div", { class: "menu cp-aamenu", role: "menu", "aria-label": "Text size", "data-side": "top-start", "data-key": "aam" },
      menuHead("Text size in conversations", "h"),
      h("div", { class: "menu-seg", "data-key": "seg" }, h("div", { class: "seg", role: "radiogroup", "aria-label": "Text size", onkeydown: e => {   // arrows move the choice, as in any radio group
          const k = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[e.key]; if (!k) return;
          e.preventDefault(); const i = SIZES.findIndex(s => s[0] === readScale()), v = SIZES[(i + k + SIZES.length) % SIZES.length][0]; setRead(v);
          requestAnimationFrame(() => document.querySelector(`.cp-aamenu [data-size-v="${v}"]`)?.focus()); } },
        SIZES.map(([v, w]) => h("button", { type: "button", role: "radio", "aria-checked": String(cur === v), "data-size-v": v, tabindex: cur === v ? "0" : "-1", onclick: () => setRead(v) }, w)))),
      menuSep("sep"),
      menuItem("Formatting bar", "Bold, code, links, lists", () => { fmtToggle(); refresh(); }, { role: "menuitemcheckbox", key: "fmt", sel: fmtOpen(), cls: "cp-fmtbox" })) : null);
}

/** After an interrupt is sent, the live line follows it: stopping (until the agent has the message), then reading for 20 s, then normal. */
function livePhase(th, who) {
  const s = CP.sent[th]; if (!s || s.who !== who) return null;
  if (s.it.state === "failed") { delete CP.sent[th]; return null; }
  const p = s.it.id && S.byId[s.it.id];
  if (!s.seen) {
    if (p && delivery(p, who)?.key === "seen") { s.seen = Date.now(); setTimeout(refresh, 20100); }
    else if (Date.now() - s.at > 300e3) { delete CP.sent[th]; return null; }
    else return "stopping";
  }
  if (Date.now() - s.seen < 20e3) return "reading";
  delete CP.sent[th]; return null;
}
function doingWords(who) {
  const L = S.running.get(who), x = L && String(L.doing || "").trim();
  if (!x) return "is working";
  return /^[A-Z][a-z]+ing\b/.test(x) ? "is " + x[0].toLowerCase() + x.slice(1) : "is working: " + x;
}
/** The line above the box while the agent this goes to works: what it is doing, and the way to reach it now. */
function liveLine(th, who, phase, armed) {
  const txt = phase === "stopping" ? "is stopping its current step…" : phase === "reading" ? "stopped and is reading your message" : doingWords(who);
  let arm = null;
  if (!phase && !armed) { arm = button("Interrupt", () => armInterrupt(th, who), { kind: "quiet", size: "s", icon: "pause", cls: "cp-arm", title: `Reach ${who} now, without waiting for its step to end` }); arm.setAttribute("aria-describedby", "cp-arm-d"); }
  return h("div", { class: "cp-live", "data-key": "live", "data-phase": phase || null },
    statusDot(agentStatus(who).key, { live: !phase || phase === "stopping" }),
    h("span", { class: "grow" }, h("b", null, who), " " + txt),
    arm, arm ? h("span", { class: "vh", id: "cp-arm-d", "data-key": "armd" }, "Opens interrupt mode. Nothing is sent until you press Interrupt again.") : null);
}
function intNote(th, who) {
  return h("div", { class: "cp-int", role: "status", "data-key": "int" }, icon("pause"),
    h("span", null, h("b", null, `Interrupt ${who}.`), " It stops its current step and reads this at once. Work in that step may be done again. Esc goes back to an ordinary message."),
    h("button", { type: "button", class: "cp-x", title: "Cancel the interrupt (Esc)", "aria-label": "Cancel the interrupt", onclick: () => disarm(th) }, "×"));
}
const firstWords = t => short(t.replace(/\s+/g, " ").trim(), 60);
function replyChip(d) {
  const p = d.reply;
  return h("div", { class: "cp-reply", "data-key": "reply" }, icon("reply"),
    h("span", null, h("b", null, "Replying to " + (p.from === "person" ? "yourself" : simpleOn() ? hiveName() : p.from)), " · ", h("q", null, firstWords(mdPlain(displayText(p))))),
    h("button", { type: "button", class: "cp-x", title: "Cancel the reply (Esc)", "aria-label": "Cancel the reply", onclick: () => { d.reply = null; d.replyId = null; keep(d); refresh(); } }, "×"));
}
function quoteChip(d) {
  const q = d.quote, first = q.text.split("\n").find(l => l.trim()) || "";
  return h("div", { class: "cp-reply cp-quote", "data-key": "quote" }, icon("reply"),
    h("span", null, h("b", null, "Quoting " + ([q.who, q.where].filter(Boolean).join(", ") || "a message")), " · ", h("q", null, firstWords(first))),
    h("button", { type: "button", class: "cp-x", title: "Remove the quote (Esc)", "aria-label": "Remove the quote", onclick: () => { d.quote = null; keep(d); refresh(); } }, "×"));
}
function filesRow(th, d) {
  return h("div", { class: "cp-files", "data-key": "files" }, d.files.map((f, i) => h("span", { class: "cp-file", "data-key": i + f.name, "data-img": f.url ? "" : null },
    f.url ? h("img", { src: f.url, alt: "" }) : h("b", null, (f.name.split(".").pop() || "file").slice(0, 4).toUpperCase()),
    h("span", { class: "cp-fn" }, h("span", null, f.name), h("span", { class: "meta" }, size(f.size))),
    h("button", { type: "button", class: "cp-x", title: "Remove " + f.name, "aria-label": "Remove " + f.name, onclick: () => { const [x] = d.files.splice(i, 1); if (x.url) URL.revokeObjectURL(x.url); refresh(); } }, "×"))));
}
/** Who the message would reach while it works (the agent it goes to, running now), or null. Only then can it interrupt. */
function busyWho(th, r) { return th && !simpleOn() && r.who && r.who !== "new" && r.why !== "btw" && isAgent(r.who) && isRunning(r.who) ? r.who : null; }

/** composer({thread, update, place}): thread null is new work. place: thread | home | start (only changes the wording). */
export function composer({ thread, update, place = "thread", autofocus, top }) {
  CP.update = update;
  const th = thread || null;
  if (CP.th !== th) { CP.th = th; CP.ac = null; CP.menu = false; CP.hist = null; }
  const d = draft(th), r = recipient(th), k = "cp:" + (th || "(new)"), empty = !d.text.trim() && !d.files.length;
  if (CP.focus || autofocus === true) { CP.focus = false; queueMicrotask(() => { const t = field(th); if (t && document.activeElement !== t) { t.focus(); t.setSelectionRange(t.value.length, t.value.length); } }); }
  mdSoon(th);   // after the render lands, so the layer copies the box's font and padding
  CP.sig = (named(d.text, th) || "") + "|" + empty + "|" + isBtw(d.text);
  const busy = busyWho(th, r);
  if (th && CP.mode[th] && !busy) delete CP.mode[th];   // the agent stopped (or the To changed): nothing left to interrupt
  const armed = !!(th && CP.mode[th] === "interrupt");
  const lw = busy || (th && CP.sent[th] && CP.sent[th].who), phase = th && lw ? livePhase(th, lw) : null;
  const ph = armed ? `Tell ${busy} what to do instead…` : !th ? "Say what you want done" : simpleOn() ? "Message" : r.who === "new" ? "Message a new agent" : r.who ? `Reply to ${r.who}…` : "Message";
  const sendWord = armed ? "Interrupt" : place === "thread" ? "Send" : "Start";
  return h("div", { class: "cp", "data-key": k, "data-place": place, "data-mode": armed ? "interrupt" : null, "data-fmt": fmtOpen() ? "" : null, ...(th ? {} : dropZone(null)) },  // a thread's composer is inside its conversation's zone
    d.err ? h("p", { class: "cp-err", role: "alert", "data-key": "err" }, d.err) : null,
    wordBoxPanel(th, update),
    top || null,  // the conversation's status strip: a tab on the box's top edge
    (busy || phase) ? liveLine(th, lw, phase, armed) : null,
    h("div", { class: "cp-box", "data-key": "box" },
      armed || d.reply || d.quote || d.files.length || (SB.open && can("secrets")) || SB.say || (!th && d.naming) ? h("div", { class: "cp-top", "data-key": "top" },
        armed ? intNote(th, busy) : null,
        d.reply ? replyChip(d) : null,
        d.quote ? quoteChip(d) : null,
        d.files.length ? filesRow(th, d) : null,
        SB.open && can("secrets") ? secretRow() : null,
        SB.say ? h("p", { class: "meta cp-ssay", "data-tone": SB.tone, role: "status", "data-key": "ssay" }, SB.say) : null,
        !th && d.naming ? h("div", { class: "cp-name-row", "data-key": "name" }, input({ class: "input cp-name", placeholder: "A short name, like fix-contact-form", "aria-label": "Name for this job", value: d.name, oninput: (e, n) => { d.name = n.value; keep(d); } })) : null) : null,
      CP.ac ? h("div", { class: "cp-ac", role: "listbox", "aria-label": "Names", "data-key": "ac" }, acList(th)) : null,
      fmtOpen() ? fmtBar(th) : null,
      h("div", { class: "cp-tw", "data-key": "tw", ...(CP.md ? { "data-md": "" } : {}) },
      // The layer carries the draft's own signature and nodes: with an empty one the morph wiped it on every re-render and the
      // next frame drew it back, a one-frame blank of the text under the box (drive_md caught it one run in six).
      h("div", { class: "cp-bd", "data-own": "", "data-sig": d.text, "aria-hidden": "true" }, ...(d.text ? mdLayer(d.text) : [])),
      h("textarea", { class: "cp-t", "data-key": "t", rows: 1, placeholder: ph, "aria-label": th ? "Message" : "What do you want done?", value: d.text, enterkeyhint: "send",
        oninput: (e, n) => {
          d.text = n.value; keep(d); CP.hist = null; mdSync(n);
          const moved = acCheck(n, th), sig = (named(d.text, th) || "") + "|" + (!d.text.trim() && !d.files.length) + "|" + isBtw(d.text);
          if (moved || sig !== CP.sig) refresh();
        },
        onkeydown: (e, n) => {
          const a = CP.ac;
          if (a && a.none && e.key === "Tab") { e.preventDefault(); return; }
          if (a && a.items.length && (e.key === "ArrowDown" || e.key === "ArrowUp")) { e.preventDefault(); a.i = (a.i + (e.key === "ArrowDown" ? 1 : a.items.length - 1)) % a.items.length; refresh(); return; }
          if (a && a.items.length && (e.key === "Enter" || e.key === "Tab") && !e.shiftKey) { e.preventDefault(); acPick(th, a.items[a.i].word); return; }
          if (!e.shiftKey && !e.altKey && !e.ctrlKey && !e.metaKey && !e.isComposing) {
            if ((e.key === "ArrowUp" || e.key === "ArrowDown") && histStep(th, n, e.key === "ArrowUp")) { e.preventDefault(); return; }
            if (e.key === "Tab" && histTab(th, n)) { e.preventDefault(); return; }
          }
          if ((e.ctrlKey || e.metaKey) && !e.shiftKey && !e.altKey) { const k = { b: "bold", i: "italic", e: "code", k: "link" }[e.key.toLowerCase()]; if (k) { e.preventDefault(); fmt(th, k); return; } }
          if (e.key === "Escape") { if (REC.state && REC.th === th) { e.preventDefault(); recStop(false); return; } if (cancelReply()) { e.stopPropagation(); e.preventDefault(); } return; }
          if (e.key === "Enter" && e.shiftKey && !e.isComposing && listEnter(th, n)) { e.preventDefault(); return; }
          if (e.key === "Enter" && !e.shiftKey && !e.isComposing) { e.preventDefault(); if (e.ctrlKey || e.metaKey || !listEnter(th, n)) send(th); }
        },
        onscroll: (e, n) => { const bd = n.parentNode.querySelector(".cp-bd"); if (bd) bd.scrollTop = n.scrollTop; },
        onpaste: e => { const fs = e.clipboardData && [...e.clipboardData.files]; if (fs && fs.length) { e.preventDefault(); addFiles(th, fs); } } })),
      h("div", { class: "cp-bar", "data-key": "bar" },
        h("input", { type: "file", multiple: true, hidden: true, class: "cp-pick", onchange: e => { addFiles(th, e.target.files); e.target.value = ""; } }),
        addPop(th, d),
        simpleOn() ? null : aaPop(th),
        secretsOn() ? h("button", { type: "button", class: "btn cp-lock hide-phone", "data-kind": "icon", "data-size": "s", "aria-label": "Send a secret", title: "Send a secret: saved on this computer, only its name is posted", "aria-pressed": String(!!SB.open), onclick: () => secretClick(th) }, icon("lock")) : null,
        recBar(th),
        wordBoxButton(update),
        h("div", { class: "cp-r", "data-key": "r" },
          simpleOn() ? null : th ? h("div", { class: "pop cp-topop", "data-key": "to" }, toChip(th, r), CP.menu === "to" ? toMenu(th, r) : null)
            : d.naming ? h("span", { class: "meta cp-new" }, "A new agent starts on this") : null,
          !th && S.app && !simpleOn() ? focusPick(d) : null,
          simpleOn() ? null : modelPick(d, r),
          button(sendWord, () => send(th), { kind: "primary", icon: armed ? "pause" : "arrow-up", disabled: empty, cls: "cp-send",
            title: armed ? "Interrupt and send (Enter)" : sendWord + " (Enter) · Shift+Enter for a new line · ↑ past messages · Tab completes" })))),
    h("div", { class: "cp-hint", "data-key": "hint" },
      h("span", null, armed ? "Enter interrupts and sends · Esc cancels" : busy ? `Enter sends; ${busy} reads it after its current step` : "Enter sends · Shift+Enter for a new line"),
      h("span", null, "Sent messages can't be edited or taken back")));
}
