// What the person keeps about a thread, and what's open for them in one (the person, 2026-11-04, posts 110401090fd2 and 11040112fc7b).
// Pin, rename, archive: stored by the dash in ~/.hive/thread-meta.json via /api/thread-meta, not on the board. S.threadMeta mirrors it.
// The "Open for you" card: at the end of a thread, what agents asked or told the person since they last wrote, so they needn't read back.
import { h } from "../core/dom.js";
import { S, can, OUTSIDE, post, loadThreadMeta, emit } from "../core/store.js";
import { isAgent, threadTitle } from "../core/model.js";
import { ago, short, mdPlain } from "../core/format.js";
import { button, icon } from "./kit.js";
import { confirm } from "./dialog.js";

export const meta = th => (S.threadMeta && S.threadMeta[th]) || {};
export const pinned = th => !!meta(th).pin;
/** Archived and nothing new for the person since: a post for them brings it back to the list by itself. */
export function archived(th) {
  const a = meta(th).archived; if (!a) return false;
  const d = S.threads.get(th); if (!d) return true;
  for (let i = d.posts.length - 1; i >= 0 && d.posts[i].t > a; i--) { const p = d.posts[i]; if (isAgent(p.from) && p.men.includes("person")) return false; }
  return true;
}

/** Change it here at once, then on the server; the next poll puts back what the server has. */
export async function setMeta(th, patch) {
  const was = S.threadMeta[th], next = { ...(was || {}) };
  for (const [k, v] of Object.entries(patch)) {
    if (k === "title") { if (v && v.trim() && v.trim() !== th) next.title = v.trim(); else delete next.title; }
    else if (v) { next[k] = Date.now(); delete next[k === "pin" ? "archived" : "pin"]; }
    else delete next[k];
  }
  S.threadMeta = { ...S.threadMeta }; if (Object.keys(next).length) S.threadMeta[th] = next; else delete S.threadMeta[th];
  S.ver++; emit({ names: true });
  try { await post("/api/thread-meta", { thread: th, ...patch }); }
  finally { loadThreadMeta(); }
}

export function renameDialog(th) {
  const cur = meta(th).title || "", field = h("input", { class: "input", value: cur || threadTitle(th), maxlength: "80", "aria-label": "Name", autocomplete: "off" });
  const dlg = confirm({ title: "Rename this conversation", ok: "Rename", failText: "Not renamed",
    body: [h("label", { class: "field" }, h("span", { class: "field-l" }, "Name"), field),
      h("p", { class: "meta" }, "Only the dash shows this name. Agents still see its id on the board, ", h("code", null, th), ". Leave it empty to go back to the usual name.")],
    onOk: () => setMeta(th, { title: field.value }) });
  queueMicrotask(() => { field.focus(); field.select(); });
  return dlg;
}

// ---- the "Open for you" card ----
// Only what an agent marks for the person: a line starting "For you:" (110401502e1a). The card shows that line, not the message's first words.
const FOR = /^\s*(?:[-*>]\s*)?(?:\*\*|__)?for you(?:\*\*|__)?\s*:\s*(?:\*\*|__)?\s*(.+)$/im;
export const forYouLine = p => { const m = FOR.exec((p && p.text) || ""); return m ? short(mdPlain(m[1]).replace(/\s+/g, " ").trim(), 220) : ""; };
const asks = p => /\?["')]*\s*$/.test(forYouLine(p));
const DONE_KEY = "hive.comms.cardDone", SAW_KEY = "hive.comms.cardSeen";
const load = k => { try { return JSON.parse(localStorage.getItem(k) || "{}") || {}; } catch (e) { return {}; } };
const doneFor = th => load(DONE_KEY)[th] || "";
function setDone(th, id) { try { const m = load(DONE_KEY); m[th] = id; localStorage.setItem(DONE_KEY, JSON.stringify(m)); } catch (e) { /* this page only */ } }
let saw = null, sawRaw = null;   // read again when another tab (or a test) changed it
function sawIt(id) { let raw = null; try { raw = localStorage.getItem(SAW_KEY); } catch (e) { /* none */ } if (raw !== sawRaw || !saw) { sawRaw = raw; saw = new Set(Object.keys(load(SAW_KEY))); } return saw.has(id); }
/** These messages have been on screen: they leave the card. Kept in this browser, the newest 400. */
export function markSeen(ids) {
  const add = ids.filter(id => !sawIt(id)); if (!add.length) return false;
  try { const m = load(SAW_KEY), t = Date.now(); for (const id of add) m[id] = t; const keep = Object.entries(m).sort((a, b) => b[1] - a[1]).slice(0, 400); localStorage.setItem(SAW_KEY, JSON.stringify(Object.fromEntries(keep))); } catch (e) { /* this page only */ }
  saw = null; return true;
}

/** What's open for the person in a thread: each agent's latest "For you:" line since the person last wrote, that they haven't had on screen. */
export function openFor(th, { seen = true } = {}) {
  const d = S.threads.get(th); if (!d) return null;
  let i0 = 0; for (let i = d.posts.length - 1; i >= 0; i--) if (OUTSIDE.has(d.posts[i].from)) { i0 = i + 1; break; }
  const after = d.posts.slice(i0), by = new Map();
  for (const p of after) if (isAgent(p.from) && forYouLine(p)) by.set(p.from, p);   // the latest per agent: an updated line replaces the old one
  const items = [...by.values()].filter(p => !seen || !sawIt(p.id)).sort((a, b) => asks(b) - asks(a) || b.t - a.t);
  return items.length ? { items, last: items.reduce((a, p) => (p.t > a.t ? p : a)).id } : null;
}

/** jump(id) shows a message in the list; answer(p) starts a reply to it. atEnd: the list sits at its end, so the newest post is on screen. */
export function openCard(th, { jump, answer, update, atEnd }) {
  const d = S.threads.get(th), newest = d && d.posts.at(-1);
  if (atEnd && newest) markSeen([newest.id]);
  const o = openFor(th); if (!o || doneFor(th) === o.last) return null;
  const q = o.items.filter(asks).length;
  return h("section", { class: "tm-card", "data-key": "open:" + th, "data-tone": "info", "aria-label": "Open for you" },
    h("header", { class: "tm-card-h" }, icon("bell"), h("b", null, q ? (q > 1 ? `${q} questions for you` : "A question for you") : "For you, further up"),
      button("Got it", () => { setDone(th, o.last); update(); }, { kind: "quiet", size: "s", cls: "tm-done", title: "Hide this until something new is for you" })),
    h("ul", { class: "tm-items" }, o.items.slice(0, 4).map(p => h("li", { "data-key": p.id, "data-ask": asks(p) ? "" : null },
      h("span", { class: "tm-who" }, p.from), h("span", { class: "tm-say" }, forYouLine(p)), h("span", { class: "meta" }, ago(p.t)),
      h("span", { class: "tm-act" },
        button("Show", () => jump(p.id), { kind: "quiet", size: "s", title: "Show this message in the conversation" }),
        can("post") && asks(p) ? button("Answer", () => answer(p), { kind: "quiet", size: "s", title: "Reply to this message" }) : null)))),
    o.items.length > 4 ? h("p", { class: "meta" }, `and ${o.items.length - 4} more above`) : null);
}
