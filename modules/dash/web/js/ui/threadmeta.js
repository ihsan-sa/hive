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
/** Archived by hand or by age, and nothing new for the person since: a post for them brings it back to the list by itself.
 *  By age, as Slack's channels do: quiet for 1 day when it was a short one-off, 7 when it ran longer (4+ posts). Pinned ones stay,
 *  and so does one the person took back out (counted from then). */
export const QUIET_DAYS = d => (d.posts.length > 3 ? 7 : 1);
export function archived(th) {
  const m = meta(th), d = S.threads.get(th); if (m.pin) return false;
  let a = m.archived;
  if (!a) {
    if (!d || !d.posts.length) return false;
    const last = Math.max(d.posts.at(-1).t, m.back || 0);
    if (Date.now() - last < QUIET_DAYS(d) * 864e5) return false;
    a = last;
    for (let i = d.posts.length - 1; i >= 0 && !OUTSIDE.has(d.posts[i].from); i--) if (isAgent(d.posts[i].from) && d.posts[i].men.includes("person")) return false;   // still waiting on the person
  }
  if (!d) return true;
  for (let i = d.posts.length - 1; i >= 0 && d.posts[i].t > a; i--) { const p = d.posts[i]; if (isAgent(p.from) && p.men.includes("person")) return false; }
  return true;
}

/** Change it here at once, then on the server; the next poll puts back what the server has. */
export async function setMeta(th, patch) {
  const was = S.threadMeta[th], next = { ...(was || {}) };
  for (const [k, v] of Object.entries(patch)) {
    if (k === "title") { if (v && v.trim() && v.trim() !== th) next.title = v.trim(); else delete next.title; }
    else if (v) { next[k] = Date.now(); delete next[k === "pin" ? "archived" : "pin"]; if (k === "archived") delete next.back; }
    else { delete next[k]; if (k === "archived") next.back = Date.now(); }
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
  const ap = approvalCard(th, update);
  const o = openFor(th); if (!o || doneFor(th) === o.last) return ap;
  const q = o.items.filter(asks).length;
  const card = h("section", { class: "tm-card", "data-key": "open:" + th, "data-tone": "info", "aria-label": "Open for you" },
    h("header", { class: "tm-card-h" }, icon("bell"), h("b", null, q ? (q > 1 ? `${q} questions for you` : "A question for you") : "For you, further up"),
      button("Got it", () => { setDone(th, o.last); update(); }, { kind: "quiet", size: "s", cls: "tm-done", title: "Hide this until something new is for you" })),
    h("ul", { class: "tm-items" }, o.items.slice(0, 4).map(p => h("li", { "data-key": p.id, "data-ask": asks(p) ? "" : null },
      h("span", { class: "tm-who" }, p.from), h("span", { class: "tm-say" }, forYouLine(p)), h("span", { class: "meta" }, ago(p.t)),
      h("span", { class: "tm-act" },
        button("Show", () => jump(p.id), { kind: "quiet", size: "s", title: "Show this message in the conversation" }),
        can("post") && asks(p) ? button("Answer", () => answer(p), { kind: "quiet", size: "s", title: "Reply to this message" }) : null)))),
    o.items.length > 4 ? h("p", { class: "meta" }, `and ${o.items.length - 4} more above`) : null);
  return ap ? h("div", null, ap, card) : card;
}

/** The machine's approval page (the person's word, 11050958dfac; rootwarden, 1105123191d8). The dash only links to it: the page shows the text and takes the passphrase, and only
 * the person's iPhone can reach it, so nothing here counts as approving. A request lasts an hour, and rootwarden's latest post in the thread is the one still in play. */
const PAGE_LIFE_MS = 36e5;
function pageRequest(th) {
  const base = S.app.word_page, d = S.threads.get(th), p = d && d.posts.findLast(x => x.from === "rootwarden");
  if (!base || !p || Date.now() - p.t > PAGE_LIFE_MS) return null;
  const m = String(p.text || "").split(base + "/?id=");
  const id = m.length > 1 && /^[A-Za-z0-9_-]{4,64}(?![^\s`.,;:)])/.exec(m.at(-1));
  return id ? { id: id[0], url: `${base}/?id=${id[0]}`, post: p } : null;
}

/** Requests an agent made for a yes no warden can give (hive approve ask). A button on text the agent wrote could be pressed without reading it,
 * so the card shows what the server recorded, and a major change asks for a typed x. */
function approvalCard(th, update) {
  const open = (S.approvals || []).filter(a => a.thread === th && a.state === "pending"), page = pageRequest(th);
  if ((!open.length && !page) || !can("stop")) return null;
  const answer = async (a, decision, confirm) => {
    try { await post("/api/approval", { id: a.id, decision, confirm }); } catch (e) { alert(e.message); }
    emit({ live: true }); update();
  };
  return h("section", { class: "tm-card", "data-key": "approve:" + th, "data-tone": "warn", "aria-label": "Approval needed" },
    h("header", { class: "tm-card-h" }, icon("bell"), h("b", null, open.length + !!page > 1 ? `${open.length + !!page} approvals needed` : "Your approval is needed")),
    h("ul", { class: "tm-items" }, page ? h("li", { "data-key": "page:" + page.id, "data-page": "" },
      h("span", { class: "tm-who" }, "rootwarden"), h("span", { class: "tm-say" }, forYouLine(page.post) || "Rootwarden asks for your yes on one action."),
      h("span", { class: "tm-act" }, h("a", { class: "btn", "data-size": "s", href: page.url, target: "_blank", rel: "noopener noreferrer", title: "Opens the approval page, which only your iPhone can reach. Read the request and type your passphrase there." }, "Approve on your iPhone"))) : null,
      open.map(a => {
      const box = a.major ? h("input", { class: "input", autocomplete: "off", spellcheck: "false", placeholder: "type x", size: 4, "aria-label": "Type x to approve this major change" }) : null;
      return h("li", { "data-key": a.id },
        h("span", { class: "tm-who" }, a.from), h("span", { class: "tm-say" }, (a.major ? "Major change: " : "") + a.why),
        h("span", { class: "tm-act" }, box,
          button("Approve", () => { if (box && box.value.trim() !== "x") { box.focus(); return; } answer(a, "approve", box ? "x" : undefined); }, { size: "s", title: a.major ? "Type x first" : "Approve this" }),
          button("Deny", () => answer(a, "deny"), { kind: "quiet", size: "s" })));
    })));
}
