// The composer: write a message, attach files (button, paste or drop), name an agent with @, reply, send.
// Up and Down walk the person's past messages like a shell's history; Tab finishes what they've started typing from them.
// App mode only (can("post")). Everything lives here, outside the render, so polls never touch what the person is doing:
// - a draft per thread (text, reply, chosen recipient, name), kept in localStorage, so it survives leaving and reloading;
// - OUT, messages on their way: they show at once as "Sending…", settle when the board has them, and a failed one keeps
//   its text and offers Retry, Edit and Discard. The board is append-only, so nothing here suggests unsending a posted one.
import { uploadFile } from "../core/upload.js";
import { h } from "../core/dom.js";
import { S, can, jget, post, poll, loadApp } from "../core/store.js";
import { simpleOn, hiveName } from "../core/simple.js";
import { go } from "../core/router.js";
import { isAgent, isRunning, agentCard, agentStatus, threadPeople, partner, displayText, typedText } from "../core/model.js";
import { short, mdPlain, now } from "../core/format.js";
import { button, icon, input, statusDot } from "./kit.js";
import { openAside } from "./aside.js";
import { sixSeven } from "../core/sixseven.js";
import { startCall, phoneReady, inCall } from "./phone.js";
import { run as eggRun } from "../core/easter.js";
import { wordBoxButton, wordBoxPanel } from "./rootword.js";

const CP = { update: null, th: undefined, focus: false, ac: null, menu: false, drag: false, sig: "", hist: null };
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
    d = { th: th || null, text: v.text || "", replyId: v.reply || null, reply: null, to: v.to || null, name: v.name || "", naming: !!v.name, model: v.model || "", files: [], err: "" };
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
  try { if (d.text || rid || d.to || d.name || d.model) localStorage.setItem(k, JSON.stringify({ text: d.text, reply: rid, to: d.to, name: d.name, model: d.model })); else localStorage.removeItem(k); } catch (e) {}
}
const field = th => document.querySelector(`.cp[data-key="${CSS.escape("cp:" + (th || "(new)"))}"] .cp-t`);

/** Start a reply to a post from anywhere: opens its thread and puts the cursor in the composer. */
export function replyTo(p) {
  const d = draft(p.th); d.reply = p; d.replyId = p.id; d.to = null; keep(d);
  CP.focus = true; go("comms", p.th); refresh();
}
/** Esc: close the @ list or the To menu, else cancel a reply. True if it did something. */
export function cancelReply() {
  if (CP.ac) { CP.ac = null; refresh(); return true; }
  if (CP.menu) { CP.menu = false; refresh(); return true; }
  const d = CP.th !== undefined && D.get(dkey(CP.th));
  if (!d || !(d.reply || d.replyId)) return false;
  d.reply = null; d.replyId = null; keep(d); refresh(); return true;
}
export const focusComposer = () => { CP.focus = true; refresh(); };
/** Add quoted text to a thread's draft and put the cursor there. It only adds text: the person still presses Send. */
export function quoteInto(th, text) {
  const d = draft(th), f = field(th), had = f ? f.value : d.text;
  d.text = had + (had && !had.endsWith("\n\n") ? (had.endsWith("\n") ? "\n" : "\n\n") : "") + text;
  if (f) f.value = d.text;
  keep(d); CP.focus = true; refresh();
}

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
function named(text) {
  for (const m of text.matchAll(/(?:^|\s)@([\w.-]+)/g)) if (m[1] === "new" || MODEL_WORDS.includes(m[1]) || isAgent(m[1])) return m[1];
  return null;
}
/** {who: name | "new" | null, why: reply | named | chosen | partner | last | first, server: who the server would wake} */
export function recipient(th) {
  if (!th) return { who: "new", why: "first" };
  const d = draft(th), s = serverWho(th), sv = s && s.who;
  if (isBtw(d.text)) return { who: "new", why: "btw", server: sv };
  if (d.reply) return { who: isAgent(d.reply.from) ? d.reply.from : sv, why: "reply", server: sv };
  const m = named(d.text); if (m) return { who: m, why: "named", server: sv };
  if (d.to) return { who: d.to, why: "chosen", server: sv };
  const p = partner(th);
  if (p) return { who: p, why: sv && sv !== p ? "partner" : "last", server: sv };
  // The target is fetched when the board changes, and a run starting doesn't change the board: an agent already running here still counts.
  const run = !sv && S.live && S.live.agents.find(a => a.thread === th && a.alive && a.name);
  return { who: sv || (run ? run.name : "new"), why: sv || run ? "last" : "first", server: sv };
}
function wire(th, d, r) {
  let t = d.text.trim() ? d.text : "Attached: " + d.files.map(f => f.name).join(", ");
  if (!th || r.why === "reply" || r.why === "named" || r.why === "first" || r.why === "btw") return t;   // /btw goes to a fresh agent in a side thread (person_post)
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
    const res = await post("/api/post", { text: it.wire, thread: it.th || it.name || null, reply_to: it.reply ? it.reply.id : null, files: it.refs, simple: simpleOn(), model: it.model || null });
    it.id = res.id; it.state = "sent"; const thr = it.th;
    if (!it.th && res.thread) { it.th = res.thread; go("comms", res.thread); loadApp(); }
    else if (res.thread && res.thread !== it.th) { it.th = res.thread; it.aside = true; openAside(thr, res.thread, it.text.replace(/^\s*\/btw\s*/i, "")); loadApp(); }   // /btw: it lives in the side thread; stay here
    poll();
  } catch (e) { it.state = "failed"; it.err = simpleOn() && !/sign in/.test(e.message) ? "Check your connection, then try again." : e.message; it.uploading = false; }
  refresh();
}
function queue(th, d, r) {
  const it = { lid: ++lid, th: th || null, text: d.text, files: d.files, reply: d.reply, to: th ? r.who : null, name: th ? null : d.name.trim() || null, wire: wire(th, d, r), model: d.model || null, state: "sending", err: "", t: now() };
  OUT.push(it); deliver(it);
  return it;
}
function send(th) {
  const d = draft(th);
  if (!d.text.trim() && !d.files.length) return;
  if (d.text.trim() === "/67" && !d.files.length) { d.text = ""; keep(d); const el = field(th); if (el) el.value = ""; refresh(); sixSeven(); return; }
  if (!d.files.length && /^\/(party|bees|matrix|rave)$/.test(d.text.trim())) { const t = d.text.trim(); d.text = ""; keep(d); const el = field(th); if (el) el.value = ""; refresh(); eggRun(t); return; }   // easter egg, not sent
  queue(th, d, recipient(th));
  Object.assign(d, { text: "", files: [], reply: null, replyId: null, to: null, name: "", naming: false, err: "", model: "" }); keep(d);
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
  queue(th, { text, files: [], reply: null, name: "" }, r); refresh();
}
export const actions = {
  retry: it => deliver(it),
  edit: it => {
    const d = draft(it.th);
    d.text = d.text.trim() ? d.text + "\n\n" + it.text : it.text; d.files = d.files.concat(it.files); d.reply ||= it.reply; keep(d);
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
function modelPick(d, r) {
  // An agent already running stays on its model for this run; a pick here is kept by the starter and holds from its next run.
  const who = r && r.who, live = who && who !== "new" && isRunning(who);
  const def = (S.app && S.app.default_model) || "", typed = typedModel(d.text), cur = typed || d.model || "";
  // A sleeping agent wakes on the model it last ran on, not the default for new agents.
  const known = who && who !== "new" && !MODEL_WORDS.includes(who), row = known && agentCard(who).find(x => x.k === "Model");
  const nm = row && (row.v === "login default" ? "Default model" : row.v);   // the name alone; the title says whether it is running now or the default
  const first = live ? nm || "Its model" : row ? nm : def ? MODEL_LABELS[def] || def : "Model";
  return h("select", { class: "cp-model meta", "data-picked": d.model ? "" : null, "aria-label": "Model", title: (d.model ? `Picked: ${MODEL_LABELS[d.model] || d.model}, used when you send this message (the agent card shows it after that). ` : "") + (live ? `${who} is running on ${row ? row.v : "its model"}. Pick another and it moves to it from its next run (it can't switch mid-run), and stays there.` : row ? `${who} last ran on ${row.v} and wakes on it. Pick another and it moves to that one and stays there.` : (def ? `New agents start on ${MODEL_LABELS[def] || def} unless you pick another. ` : "") + "Model for a new or sleeping agent this message starts. It holds on that agent from then on. Typing @opus, @sonnet… in the message sets this too."),
    onchange: e => { d.model = e.target.value; keep(d); refresh(); } },
    [["", first], ...Object.entries(MODEL_LABELS).filter(([v]) => live || row || v !== def)].map(([v, l]) => h("option", { value: v, selected: cur === v }, l)));
}

// ---- @ names ----
function acNames(th, q) {
  const seen = new Set(), out = [], add = n => { if (n && !seen.has(n) && n.startsWith(q)) { seen.add(n); out.push(n); } };
  if (th) for (const n of threadPeople(th)) if (isAgent(n)) add(n);
  for (const n of S.running.keys()) add(n);
  for (let i = S.sessions.length - 1; i >= 0 && out.length < 8; i--) add(S.sessions[i].name);
  add("new"); MODEL_WORDS.forEach(add);
  return out.slice(0, 8);
}
/** Threads that start with q, most recently active first. */
function acThreads(q) {
  const out = [];
  for (let i = S.posts.length - 1; i >= 0 && out.length < 8; i--) { const t = S.posts[i].th; if (t && !out.includes(t) && t.toLowerCase().startsWith(q)) out.push(t); }
  return out;
}
function acCheck(el, th) {
  const before = el.value.slice(0, el.selectionStart), m = simpleOn() ? null : before.match(/(?:^|\s)([@#])([\w.-]*)$/);
  const mark = m ? m[1] : "@", q = m ? m[2] : "";
  const items = m ? (mark === "#" ? acThreads(q.toLowerCase()) : acNames(th, q.toLowerCase())) : [];
  const next = items.length && !(items.length === 1 && items[0] === q) ? { q, mark, at: el.selectionStart - q.length - 1, items, i: 0 } : null;
  if (JSON.stringify(next && next.items) !== JSON.stringify(CP.ac && CP.ac.items)) { CP.ac = next; return true; }
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
  for (let i = S.posts.length - 1; i >= 0 && out.length < 500; i--) if (S.posts[i].from === "person") { const t = typedText(S.posts[i]); if (!/^Attached: /.test(t)) add(t); }
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
const stateWords = n => (n === "new" ? "starts a new agent" : agentStatus(n).label || "");

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
// The To menu closes on a click anywhere outside it (and on Esc, above).
document.addEventListener("pointerdown", e => {
  if (CP.menu && !e.target.closest?.(".cp-menu, .cp-to")) { CP.menu = false; refresh(); }
}, true);
function toChip(th, r) {
  const n = r.who, label = n === "new" ? "a new agent" : n || "…";
  const why = { reply: "you're replying to it", named: "named in your message", chosen: "you chose it", btw: "side question" }[r.why];
  return h("button", { type: "button", class: "cp-to", "aria-haspopup": "listbox", "aria-expanded": String(CP.menu), title: r.why === "btw" ? "A side question: it answers in its own thread, and this one's agent never sees it." : "Who this goes to. Change it here, or type @ and a name.",
    onclick: () => { CP.menu = !CP.menu; refresh(); } },
    h("span", { class: "meta" }, "To"), n && n !== "new" ? statusDot(agentStatus(n).key, { live: S.running.has(n) }) : null, h("b", null, label),
    why ? h("span", { class: "meta cp-why" }, why) : null, h("span", { "aria-hidden": "true", class: "meta" }, "▾"));
}
function toMenu(th, r) {
  const d = draft(th), names = threadPeople(th).filter(isAgent), pick = n => { d.to = n; keep(d); CP.menu = false; refresh(); field(th)?.focus(); };
  return h("div", { class: "cp-menu", role: "listbox", "aria-label": "Send to", "data-key": "menu" },
    names.map(n => h("button", { type: "button", role: "option", class: "cp-opt", "aria-selected": String(r.who === n), "data-key": n, onclick: () => pick(n) },
      statusDot(agentStatus(n).key, { live: S.running.has(n) }), h("b", null, n), h("span", { class: "meta" }, stateWords(n)),
      n === partner(th) ? h("span", { class: "meta" }, "· you've been talking to it") : null)),
    h("button", { type: "button", role: "option", class: "cp-opt", "aria-selected": String(r.who === "new"), "data-key": "new", onclick: () => pick("new") },
      statusDot("none"), h("b", null, "A new agent"), h("span", { class: "meta" }, "starts fresh, with this conversation to read")),
    h("p", { class: "cp-menu-n" }, r.why === "partner"
      ? `${r.server} wrote here last, so a message with no name would wake it. You've been talking to ${r.who}, so Hive sends it there.`
      : "Typing @ and a name in your message sends it to that agent instead."));
}
function replyChip(d) {
  const p = d.reply;
  return h("div", { class: "cp-reply", "data-key": "reply" },
    h("span", { class: "meta" }, "Replying to"), h("b", null, p.from === "person" ? "yourself" : simpleOn() ? hiveName() : p.from),
    h("span", { class: "cp-reply-x" }, short(mdPlain(displayText(p)), 120)),
    h("button", { type: "button", class: "cp-x", title: "Cancel the reply (Esc)", "aria-label": "Cancel the reply", onclick: () => { d.reply = null; d.replyId = null; keep(d); refresh(); } }, "×"));
}
function filesRow(th, d) {
  return h("div", { class: "cp-files", "data-key": "files" }, d.files.map((f, i) => h("span", { class: "cp-file", "data-key": i + f.name, "data-img": f.url ? "" : null },
    f.url ? h("img", { src: f.url, alt: "" }) : h("b", null, (f.name.split(".").pop() || "file").slice(0, 4).toUpperCase()),
    h("span", { class: "cp-fn" }, h("span", null, f.name), h("span", { class: "meta" }, size(f.size))),
    h("button", { type: "button", class: "cp-x", title: "Remove " + f.name, "aria-label": "Remove " + f.name, onclick: () => { const [x] = d.files.splice(i, 1); if (x.url) URL.revokeObjectURL(x.url); refresh(); } }, "×"))));
}

/** composer({thread, update, place}): thread null is new work. place: thread | home | start (only changes the wording). */
export function composer({ thread, update, place = "thread", autofocus, top }) {
  CP.update = update;
  const th = thread || null;
  if (CP.th !== th) { CP.th = th; CP.ac = null; CP.menu = false; CP.hist = null; }
  const d = draft(th), r = recipient(th), k = "cp:" + (th || "(new)"), empty = !d.text.trim() && !d.files.length;
  if (CP.focus || autofocus === true) { CP.focus = false; queueMicrotask(() => { const t = field(th); if (t && document.activeElement !== t) { t.focus(); t.setSelectionRange(t.value.length, t.value.length); } }); }
  mdSoon(th);   // after the render lands, so the layer copies the box's font and padding
  CP.sig = (named(d.text) || "") + "|" + empty + "|" + isBtw(d.text);
  const ph = !th ? "Say what you want done" : simpleOn() ? "Message" : r.who === "new" ? "Message a new agent" : r.who ? `Reply to ${r.who}…` : "Message";
  return h("div", { class: "cp", "data-key": k, "data-place": place, "data-fmt": fmtOpen() ? "" : null, ...(th ? {} : dropZone(null)) },  // a thread's composer is inside its conversation's zone
    d.err ? h("p", { class: "cp-err", role: "alert", "data-key": "err" }, d.err) : null,
    wordBoxPanel(th, update),
    top || null,  // the conversation's status strip: a tab on the box's top edge
    h("div", { class: "cp-box", "data-key": "box" },
      d.reply ? replyChip(d) : null,  // inside the box, its first row
      CP.ac ? h("div", { class: "cp-ac", role: "listbox", "aria-label": "Names", "data-key": "ac" }, CP.ac.items.map((n, i) => h("button", { type: "button", role: "option", class: "cp-opt", "data-key": n, "aria-selected": String(i === CP.ac.i), onclick: () => acPick(th, n) },
        CP.ac.mark === "#" ? null : n === "new" ? statusDot("none") : statusDot(agentStatus(n).key, { live: S.running.has(n) }), h("b", null, (CP.ac.mark || "@") + n), CP.ac.mark === "#" ? null : h("span", { class: "meta" }, stateWords(n))))) : null,
      d.files.length ? filesRow(th, d) : null,
      SB.open && can("secrets") ? secretRow() : null,
      SB.say ? h("p", { class: "meta cp-ssay", "data-tone": SB.tone, role: "status", "data-key": "ssay" }, SB.say) : null,
      !th && d.naming ? h("div", { class: "cp-name-row", "data-key": "name" }, input({ class: "input cp-name", placeholder: "A short name, like fix-contact-form", "aria-label": "Name for this job", value: d.name, oninput: (e, n) => { d.name = n.value; keep(d); } })) : null,
      h("div", { class: "cp-tw", "data-key": "tw", ...(CP.md ? { "data-md": "" } : {}) },
      h("div", { class: "cp-bd", "data-own": "", "data-sig": "", "aria-hidden": "true" }),
      h("textarea", { class: "cp-t", "data-key": "t", rows: 1, placeholder: ph, "aria-label": th ? "Message" : "What do you want done?", value: d.text, enterkeyhint: "send",
        oninput: (e, n) => {
          d.text = n.value; keep(d); CP.hist = null; mdSync(n);
          const moved = acCheck(n, th), sig = (named(d.text) || "") + "|" + (!d.text.trim() && !d.files.length) + "|" + isBtw(d.text);
          if (moved || sig !== CP.sig) refresh();
        },
        onkeydown: (e, n) => {
          const a = CP.ac;
          if (a && (e.key === "ArrowDown" || e.key === "ArrowUp")) { e.preventDefault(); a.i = (a.i + (e.key === "ArrowDown" ? 1 : a.items.length - 1)) % a.items.length; refresh(); return; }
          if (a && (e.key === "Enter" || e.key === "Tab") && !e.shiftKey) { e.preventDefault(); acPick(th, a.items[a.i]); return; }
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
        button("Attach files", (e, b) => b.parentNode.querySelector(".cp-pick").click(), { kind: "icon", size: "s", icon: "plus", cls: "cp-attach", title: "Attach files. You can also paste or drop them here." }),
        simpleOn() ? null : h("button", { type: "button", class: "btn cp-aa", "data-kind": "icon", "data-size": "s", title: "Formatting", "aria-label": "Formatting", "aria-pressed": fmtOpen() ? "true" : "false", onmousedown: e => e.preventDefault(), onclick: () => { fmtToggle(); refresh(); } }, "Aa"),
        recBar(th),
        simpleOn() ? null : fmtBar(th),
        wordBoxButton(update),
        phoneReady() && !(REC.state && REC.th === th) ? button("Call", () => { if (!inCall()) startCall(th ? { thread: th } : {}); },
          { kind: "icon", size: "s", icon: "phone", cls: "cp-call", title: th ? "Voice call about this thread: a new agent picks up, reads the thread and talks with you. To talk with an agent itself, hover its name and press Call." : "Voice call with the Hive. Ask it anything, or ask to be put through to whoever is working on something." }) : null,
        can("secrets") || (S.app && S.app.secrets_on && S.app.mode === "expired") ? button("Send a secret", () => {
          if (!can("secrets")) { SB.open = false; SB.tone = "bad"; SB.say = "Saving a secret needs full mode, which ends after 30 minutes without using the app. Press \"Limited · sign in for full\" at the top, then press the lock again."; refresh(); return; }
          if (SB.open) dropSecrets(); else openSecrets(th); }, { kind: "icon", size: "s", icon: "lock", cls: "cp-lock", title: "Send a secret" }) : null,
        h("span", { class: "cp-sp" }),
        simpleOn() ? null : th ? toChip(th, r) : d.naming ? h("span", { class: "meta cp-new" }, "A new agent starts on this") : button("Name it", () => { d.naming = true; refresh(); queueMicrotask(() => document.querySelector(".cp-name")?.focus()); }, { kind: "quiet", size: "s", title: "Give this job a short name (optional). Without one it's called by its first words." }),
        simpleOn() ? null : modelPick(d, r),
        button(place === "thread" ? "Send" : "Start", () => send(th), { kind: "primary", icon: "arrow-up", disabled: empty, cls: "cp-send", title: (place === "thread" ? "Send" : "Start") + " (Enter) · Shift+Enter for a new line · ↑ past messages · Tab completes" }))),
    CP.menu && th ? toMenu(th, r) : null);
}
