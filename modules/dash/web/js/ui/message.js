// One board post: author, recipients, time, body, files, read marks. Used by Comms and the Agent page.
// message(post, {showThread}) returns the whole article. messageHead(post, opts) is its first child, on its own,
// so a long list can refresh names and read marks without touching the bodies.
import { h } from "../core/dom.js";
import { S, can, jget, SYS } from "../core/store.js";
import { isAgent, isRunning } from "../core/model.js";
import { clock, stamp, now, DAY } from "../core/format.js";
import { markdown, mdCut } from "./markdown.js";
import { agentRef, threadRef } from "./refs.js";
import { button } from "./kit.js";
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

function receipt(p, m) {
  if (!isAgent(m) || now() - p.t > 3 * DAY || p.from === m) return null;
  const q = S.pending[m], queued = q && (q.ids || []).includes(p.id);
  const set = DELIV.sets[m], last = S.lastSess[m];
  const read = (set && set.has(p.id)) || (!queued && last && last.start >= p.t - 2000);
  const [mark, tone, t] = read ? ["✓", "good", `${m} has this`]
    : queued ? ["…", "info", `queued: it reaches ${m} when it starts`]
    : isRunning(m) ? ["…", "warn", `waiting: ${m} sees this after its current step`]
    : ["○", "bad", `hasn't reached ${m}`];
  return h("span", { class: "msg-r", "data-tone": tone, title: t, "aria-label": t }, mark);
}

// ---- attachments: images as thumbnails, text files open in place, anything else downloads ----
// All open in one viewer window. Text is shown through textContent only, and svg is never rendered.
const IMG = /\.(png|jpe?g|gif)$/i, TXT = /\.(txt|md|log|json|jsonl|csv|tsv|py|js|mjs|ts|sh|c|h|cpp|v|sv|vh|yaml|yml|toml|ini|cfg|xml|html|css|go|rs|kicad_pcb|kicad_sch|net|sp|cir|spice|lef|def|sdc|tcl)$/i;
const fileUrl = (p, f) => `/files/${encodeURIComponent(p.id)}/${encodeURIComponent(f.name)}`;
export const fsize = n => (n == null ? "" : n >= 1048576 ? (n / 1048576).toFixed(1) + " MB" : n >= 1024 ? Math.round(n / 1024) + " kB" : n + " B");

function attachment(p, f) {
  const u = fileUrl(p, f);
  if (IMG.test(f.name)) return h("button", { type: "button", class: "msg-thumb", title: f.name + ", click to view", onclick: () => viewFile(p, f) },
    h("img", { src: u, alt: f.name, loading: "lazy" }), h("span", null, f.name));
  const txt = TXT.test(f.name) && (f.size == null || f.size < 2 * 1048576);
  const props = txt ? { type: "button", onclick: () => viewFile(p, f) } : { href: u, download: f.name };
  return h(txt ? "button" : "a", { class: "msg-file", title: txt ? "Click to read" : "Download", ...props },
    h("b", null, (f.name.split(".").pop() || "file").slice(0, 5).toUpperCase()),
    h("span", null, h("span", { class: "msg-fn" }, f.name), h("span", { class: "meta" }, fsize(f.size) + (txt ? " · click to read" : " · download"))));
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
    open.href = u; cap.textContent = `${g.name} · ${fsize(g.size)} · from ${p.from}, ${stamp(p.t)}`;
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

// ---- the post ----
const LONG = 700, CUT = 600, OPEN = new Set();
const bodyText = p => (p.text.length > LONG && !OPEN.has(p.id) ? mdCut(p.text, CUT) : p.text);

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

export function messageHead(p, { showThread = true } = {}) {
  const woke = (S.woke[p.id] || []).filter(s => !p.men.includes(s.name) || !s.resumed), tos = p.men.filter(m => m !== "new");
  return h("div", { class: "msg-h" },
    agentRef(p.from, { copy: true }),
    tos.length ? h("span", { class: "msg-to", "aria-label": "to" }, "→") : null,
    tos.flatMap(m => [agentRef(m, { copy: true }), receipt(p, m)]),
    showThread ? threadRef(p.th) : null,
    p.reply_to ? h("span", { class: "meta", title: `This answers post ${p.reply_to}` }, `↩ ${p.reply_to}`) : null,
    woke.length ? h("span", { class: "meta" }, woke.map(s => (s.resumed ? "woke " : "started ") + s.name).join(", ")) : null,
    h("span", { class: "msg-end" },
      S.app.app && can("post") ? button("Reply", () => replyTo(p), { kind: "quiet", size: "s", cls: "msg-reply", title: "Reply to this post; wakes its author" }) : null,
      h("time", { class: "meta num", datetime: p.at, title: p.at }, clock(p.t))));
}

export function message(p, opts) {
  const long = p.text.length > LONG, files = p.files || [], ws = opts && opts.mark;
  // A word found past the cut would look like a false match: open the post so it shows.
  if (long && ws && ws.length && !OPEN.has(p.id)) {
    const lc = p.text.toLowerCase(), cut = mdCut(p.text, CUT).toLowerCase();
    if (ws.some(w => lc.includes(w)) && !ws.some(w => cut.includes(w))) OPEN.add(p.id);
  }
  return h("article", { class: "msg", "data-key": p.id, "data-who": SYS.has(p.from) ? "sys" : p.from === "person" || p.from === "box" ? "person" : null },
    messageHead(p, opts),
    markBody(markdown(bodyText(p)), ws),
    files.length ? h("div", { class: "msg-files" }, files.map(f => attachment(p, f))) : null,
    long ? button(OPEN.has(p.id) ? "Show less" : `Show all ${p.text.length} characters`, (e, b) => {
      if (OPEN.has(p.id)) OPEN.delete(p.id); else OPEN.add(p.id);
      b.closest(".msg").querySelector(".md").replaceWith(markBody(markdown(bodyText(p)), ws));
      b.textContent = OPEN.has(p.id) ? "Show less" : `Show all ${p.text.length} characters`;
    }, { kind: "quiet", size: "s", cls: "msg-more" }) : null);
}
