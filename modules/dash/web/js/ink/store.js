// Ink in the chat (thread 112515205dbf; design ~/designs/ink-chat/HANDOFF.md): what is drawn where, folded from the board.
// Nothing new is kept on the server. A sheet is what its posts say:
//  - an agent's ```ink fence with no on= offers a sheet (the latest offer of an id is its home; earlier ones show as cards),
//  - an agent's ```ink on=<id> is a layer of marks on it,
//  - the person's ink goes as attachments of their next message: ink-<sheet>.json (the strokes) and ink-<sheet>.png (what the agent looks at).
// Until it is sent, the person's ink is pending in this browser (localStorage ink:<thread>), like a draft.
// Sheet ids: the fence's id=, else <postid>.<n> (the nth offer in that post); m-<postid> is the ink over a message; "board" the whiteboard.
import { S, OUTSIDE } from "../core/store.js";
import { parseInk, fences, W, LINE } from "./grammar.js";

export { W, LINE };
const say = th => dispatchEvent(new CustomEvent("ink:change", { detail: { th } }));

// ---- the tool in hand (one for the whole page) ----
export const TOOL = { t: "pen", c: "ink", pen: false, pencil: false };
export function setTool(k, v) { TOOL[k] = v; dispatchEvent(new CustomEvent("ink:tool")); }
export const PENS = { ink: "var(--pen-ink)", blue: "var(--pen-blue)", green: "var(--pen-green)" };

// ---- pending: the person's ink not sent yet ----
const PK = th => "ink:" + th;
const PEND = new Map();
export function pend(th) {
  let v = PEND.get(th);
  if (!v) { try { v = JSON.parse(localStorage.getItem(PK(th)) || "null"); } catch (e) { v = null; } if (!v || typeof v.sheets !== "object") v = { sheets: {} }; PEND.set(th, v); }
  return v;
}
function save(th) {
  const v = pend(th);
  for (const [k, s] of Object.entries(v.sheets)) if (!s.strokes.length && !s.erase.length) delete v.sheets[k];
  try { Object.keys(v.sheets).length ? localStorage.setItem(PK(th), JSON.stringify(v)) : localStorage.removeItem(PK(th)); } catch (e) {}
  say(th);
}
const pSheet = (th, sid, meta) => { const v = pend(th); return (v.sheets[sid] ||= { strokes: [], erase: [], ...(meta || {}) }); };
// Undo and redo stay in this page: [{th, sid, add: stroke} | {th, sid, erase: id} | {th, sid, drop: stroke}]
const UNDO = [], REDO = [];
export function addStroke(th, sid, st, meta) {
  const p = pSheet(th, sid, meta); if (meta) Object.assign(p, meta);
  p.strokes.push(st); UNDO.push({ th, sid, add: st }); REDO.length = 0; save(th);
}
/** The eraser: the person's own strokes only. One not sent yet goes; a sent one is named in the next message's erase list. */
export function eraseStroke(th, sid, id) {
  const p = pSheet(th, sid), i = p.strokes.findIndex(x => x.id === id);
  if (i >= 0) { const [st] = p.strokes.splice(i, 1); UNDO.push({ th, sid, drop: st, at: i }); }
  else if (!p.erase.includes(id)) { p.erase.push(id); UNDO.push({ th, sid, erase: id }); }
  else return;
  REDO.length = 0; save(th);
}
export function dropStroke(th, sid, id) {   // a loop handed to a side chat, or Clear in the bubble: gone without an undo step
  const p = pend(th).sheets[sid]; if (!p) return;
  p.strokes = p.strokes.filter(x => x.id !== id); save(th);
}
function apply(op, back) {
  const p = pSheet(op.th, op.sid);
  if (op.add) { if (back) p.strokes = p.strokes.filter(x => x.id !== op.add.id); else p.strokes.push(op.add); }
  else if (op.drop) { if (back) p.strokes.splice(Math.min(op.at, p.strokes.length), 0, op.drop); else p.strokes = p.strokes.filter(x => x.id !== op.drop.id); }
  else if (op.erase) { if (back) p.erase = p.erase.filter(x => x !== op.erase); else p.erase.push(op.erase); }
  save(op.th);
}
export const canUndo = th => UNDO.some(o => o.th === th), canRedo = th => REDO.some(o => o.th === th);
export function undo(th) { for (let i = UNDO.length - 1; i >= 0; i--) if (UNDO[i].th === th) { const [o] = UNDO.splice(i, 1); apply(o, true); REDO.push(o); return true; } return false; }
export function redo(th) { for (let i = REDO.length - 1; i >= 0; i--) if (REDO[i].th === th) { const [o] = REDO.splice(i, 1); apply(o, false); UNDO.push(o); return true; } return false; }
/** How much is pending in this chat: {sheets: n, strokes: n}, or null. */
export function inkPending(th) {
  if (!th) return null;
  const v = pend(th), ks = Object.keys(v.sheets).filter(k => v.sheets[k].strokes.length || v.sheets[k].erase.length);
  return ks.length ? { sheets: ks, strokes: ks.reduce((n, k) => n + v.sheets[k].strokes.length, 0) } : null;
}
// Sent ink stays on screen until the board brings it back (its post, then its .json): no blink between Send and the fold.
const HOLD = new Map();   // th -> {sid -> {strokes, erase, t}}
export function clearPending(th, sid, { sent = false } = {}) {
  const v = pend(th);
  if (sent) { const hd = HOLD.get(th) || HOLD.set(th, {}).get(th); for (const k of sid ? [sid] : Object.keys(v.sheets)) if (v.sheets[k]) hd[k] = { strokes: v.sheets[k].strokes, erase: v.sheets[k].erase, t: Date.now() }; }
  if (sid) delete v.sheets[sid]; else v.sheets = {};
  for (const l of [UNDO, REDO]) for (let i = l.length - 1; i >= 0; i--) if (l[i].th === th && (!sid || l[i].sid === sid)) l.splice(i, 1);
  save(th);
}
addEventListener("storage", e => { if (e.key && e.key.startsWith("ink:") && e.key !== "ink:played") { const th = e.key.slice(4); PEND.delete(th); say(th); } });

export const sid = () => Date.now().toString(36).slice(-5) + Math.random().toString(36).slice(2, 6);

// ---- what was sent: the board's posts, folded ----
const JSON_CACHE = new Map();   // "<post>/<name>" -> data | "wait" | "err"
const fileUrl = (p, f) => `/files/${encodeURIComponent(p.id)}/${encodeURIComponent(f.name)}`;
const INKF = /^ink-(.+)\.json$/;
function inkJson(th, p, f) {
  const k = p.id + "/" + f.name, v = JSON_CACHE.get(k);
  if (v !== undefined) return v === "wait" || v === "err" ? null : v;
  JSON_CACHE.set(k, "wait");
  fetch(fileUrl(p, f)).then(r => r.ok ? r.json() : Promise.reject(r.status)).then(j => { JSON_CACHE.set(k, j && j.v === 1 ? j : "err"); FOLD.delete(th); say(th); }, () => JSON_CACHE.set(k, "err"));
  return null;
}
export const isPerson = who => OUTSIDE.has(who);
const FOLD = new Map();   // th -> {n, sheets}
/** Every sheet in chat th: Map sid -> {sid, offers:[{post, n, parsed}], home, title, paper, h, layers:[...]}.
 *  A layer: {who, post, t, own: true, strokes, erase} or {who, post, t, marks, skipped}. */
export function fold(th) {
  const d = S.threads.get(th), posts = d ? d.posts : [];
  const c = FOLD.get(th); if (c && c.n === posts.length && c.last === (posts.at(-1) || {}).id) return c;
  const sheets = new Map(), agents = new Map(), byPost = new Map();
  const get = sid => { let s = sheets.get(sid); if (!s) { s = { sid, offers: [], home: null, title: null, paper: "ruled", h: 6 * LINE, layers: [] }; sheets.set(sid, s); } return s; };
  for (const p of posts) {
    if (isPerson(p.from)) {
      for (const f of p.files || []) {
        const m = INKF.exec(f.name); if (!m) continue;
        const j = inkJson(th, p, f), s = get(m[1]);
        s.layers.push({ who: p.from, own: true, post: p.id, t: p.t, strokes: j ? j.strokes || [] : [], erase: j ? j.erase || [] : [], wait: !j, h: j && j.h });
        if (!s.title && j && j.title) s.title = j.title;
      }
      continue;
    }
    if (!p.text || !/ink/i.test(p.text)) continue;
    let n = 0;
    for (const f of fences(p.text)) {
      const r = parseInk(f.info, f.code);
      if (r.info.on) {
        const s = get(r.info.on);
        if (!agents.has(p.from)) agents.set(p.from, agents.size % 4 + 1);
        if (r.marks.some(m => m.k === "clear")) for (const l of s.layers) if (l.who === p.from && !l.own) l.cleared = true;
        const l = { who: p.from, post: p.id, t: p.t, marks: r.marks.filter(m => m.k !== "clear"), skipped: r.skipped, code: f.code };
        s.layers.push(l); (byPost.get(p.id) || byPost.set(p.id, []).get(p.id)).push({ sid: r.info.on, layer: l });
      } else {
        const id = r.info.id || p.id + "." + n; n++;
        const s = get(id);
        s.offers.push({ post: p.id, who: p.from, n: n - 1, parsed: r, code: f.code });
        s.home = p.id; s.homeN = n - 1; s.title = r.info.title || s.title; s.paper = r.info.paper; s.h = r.info.h; s.lines = r.info.lines; s.by = p.from;
        if (r.marks.length && !agents.has(p.from)) agents.set(p.from, agents.size % 4 + 1);
      }
    }
  }
  const out = { n: posts.length, last: (posts.at(-1) || {}).id, sheets, agents, byPost };
  FOLD.set(th, out);
  return out;
}
addEventListener("ink:change", e => { if (e.detail && e.detail.th && !e.detail.pendOnly) FOLD.delete(e.detail.th); });
export const agentPen = (th, who) => { const n = fold(th).agents.get(who); return `var(--ag-${n || 1})`; };
/** The sheet id an offer fence in post id gets: its id=, else <post>.<n>, n counting the offers in that post (matched by its text). */
export function offerId(post, info, code) {
  const r = parseInk(info, code); if (r.info.id) return r.info.id;
  let n = 0;
  for (const f of fences(post && post.text)) { const q = parseInk(f.info, f.code); if (q.info.on) continue; if (!q.info.id && f.code === code) return post.id + "." + n; n++; }
  return (post ? post.id : "x") + "." + 0;
}

// ---- who is shown: per sheet, by author (this page only) ----
const HIDE = new Map();
export const hidden = (th, sid) => HIDE.get(th + "\0" + sid) || new Set();
export function toggleHidden(th, sid, who) {
  const k = th + "\0" + sid, s = new Set(hidden(th, sid)); s.has(who) ? s.delete(who) : s.add(who); HIDE.set(k, s); say(th);
}

// ---- the write-on plays once, and only for a fresh post ----
const PLAYED = "ink:played";
const played = () => { try { return new Set(JSON.parse(localStorage.getItem(PLAYED) || "[]")); } catch (e) { return new Set(); } };
export function shouldPlay(post) {
  const p = S.byId[post]; if (!p || Date.now() - p.t > 15 * 60e3) return false;
  return !played().has(post);
}
export function markPlayed(post) {
  const s = played(); if (s.has(post)) return; s.add(post);
  try { localStorage.setItem(PLAYED, JSON.stringify([...s].slice(-300))); } catch (e) {}
}

/** All the person's strokes on a sheet, sent and pending, with erased ones taken out: [{...stroke, pending}] */
export function ownStrokes(th, sid) {
  const s = fold(th).sheets.get(sid), p = pend(th).sheets[sid], sentGone = new Set(), sent = [];
  if (s) for (const l of s.layers) if (l.own) { for (const e of l.erase) sentGone.add(e); for (const st of l.strokes) sent.push(st); }
  const hd = HOLD.get(th), held = hd && hd[sid], have = new Set(sent.map(x => x.id));
  if (held && (Date.now() - held.t > 120e3 || (held.strokes.every(x => have.has(x.id)) && held.erase.every(e => sentGone.has(e))))) delete hd[sid];
  const live = hd && hd[sid], gone = new Set([...sentGone, ...(p ? p.erase : []), ...(live ? live.erase : [])]);
  const out = sent.filter(st => !gone.has(st.id));
  if (live) for (const st of live.strokes) if (!have.has(st.id) && !gone.has(st.id)) out.push(st);
  if (p) for (const st of p.strokes) out.push({ ...st, pending: true });
  return out;
}
