// A Python box inside a chat message (the person, 11141319104c: "a python box like here in the dashboard chat").
// An agent makes the box with the course's `tutor.py box` and puts the line `pytutor:box/<16 hex>` in its post. For a sign-in that has
// the course's door, the message shows that box in the same locked frame as the side preview (preview/render.js `page`): the course page
// in a sandbox with no allow-same-origin, its own one-time door key, the same relay and routes. The chat runs and renders no code; only
// the 16-hex id from the post reaches the frame's address (warden 111413221f61). Everyone else sees the line as text.
import { h } from "../core/dom.js";
import { S } from "../core/store.js";
import { viewHref, openPreview, closePreview, previewKey, keyOf } from "./preview.js";
import { button } from "./kit.js";

const DOOR = "pytutor";
const MARK = /^[ \t]*`?pytutor:box\/([0-9a-f]{16})`?[ \t]*$/gm;
/** The box ids a post names, each once, at most four. */
export function boxIds(text) { const out = []; for (const m of String(text || "").matchAll(MARK)) if (!out.includes(m[1])) out.push(m[1]); return out.slice(0, 4); }
export const dropBoxLines = t => String(t).replace(MARK, "").replace(/\n{3,}/g, "\n\n").trim();
function coursePage() { const d = S.app && S.app.doors; if (!d) return null; for (const [page, name] of Object.entries(d)) if (name === DOOR) return page; return null; }
export const boxesOn = () => !!coursePage();

// One live frame per post and box, kept across the chat's redraws: morph meets a bare copy (data-own, same sig) and leaves the real one alone.
const LIVE = new Map();
// The frame can't see this page, so the dash's theme goes in its address (light or dark, nothing else) and follows a switch.
const DARK = matchMedia("(prefers-color-scheme: dark)");
const theme = () => { const t = document.documentElement.dataset.theme; return t === "light" || t === "dark" ? t : DARK.matches ? "dark" : "light"; };
const hex = n => Array.from(crypto.getRandomValues(new Uint8Array(n)), b => b.toString(16).padStart(2, "0")).join("");
export function boxEmbed(pid, id) {
  const k = pid + ":" + id, had = LIVE.get(k);
  if (had) {
    // A redraw that couldn't match the live frame (a parent rebuilt or shifted) leaves the bare copy standing in the chat, empty:
    // the box "vanishes" (the person, 111720: "it just disappeared after appearing"). Put the real one back in the copy's place.
    if (!had.el.isConnected && had.ghost && had.ghost.isConnected) had.ghost.replaceWith(had.el);
    if (!had.el.isConnected) return had.el;
    const c = had.el.cloneNode(false); c._real = had.el; had.ghost = c; return c;
  }
  const page = coursePage(); if (!page) return null;
  for (const [kk, v] of LIVE) if (LIVE.size > 24 && !v.el.isConnected) { v.ac.abort(); LIVE.delete(kk); }
  const key = hex(16), ac = new AbortController();
  const fr = h("iframe", { class: "pybox-f", title: "Python box", sandbox: "allow-scripts allow-modals allow-pointer-lock", referrerpolicy: "no-referrer", loading: "lazy" });
  // Side panel (the person, 111416220c7f: "so it stays up as i talk in chat"): the same box opens in the preview, in its own locked
  // frame (render.js `page`), and this one folds to a line until the panel closes or shows something else. Only one copy runs at a
  // time; each Run, Check and Send is saved by the course, so the copy that opens next starts from the last one.
  const it = { kb: page, name: page.split("/").pop(), box: id, label: "Python box" }, pk = keyOf(it);
  const pin = button("Open in side panel", () => openPreview([it]), { kind: "quiet", size: "s", icon: "sidebar", cls: "pybox-pin", title: "Keep this box beside the chat while you talk" });
  const fold = h("div", { class: "pybox-fold", hidden: true }, h("span", { class: "meta" }, "Python box, open in the side panel"),
    button("Bring it back here", () => { if (previewKey() === pk) closePreview(); }, { kind: "quiet", size: "s", cls: "pybox-back" }));
  const el = h("div", { class: "pybox", "data-key": "box-" + id, "data-own": "", "data-sig": id }, h("div", { class: "pybox-bar" }, pin), fr, fold);
  LIVE.set(k, { el, ac });
  import("../preview/render.js").then(m => {   // the listener is on before the page can ask anything
    m.relay(fr, DOOR, key, ac.signal, px => { fr.style.height = px + "px"; });
    const at = viewHref({ kb: page, name: page.split("/").pop() }) + "#door=" + key + "&box=" + id + "&theme=";
    const paint = () => { if (!el.hasAttribute("data-pinned")) fr.src = at + theme(); };   // only the fragment changes, so the page keeps its state and just recolours
    const sync = () => {
      const on = previewKey() === pk;
      if (on === el.hasAttribute("data-pinned")) return;
      el.toggleAttribute("data-pinned", on); fold.hidden = !on; pin.parentNode.hidden = on; fr.hidden = on;
      if (on) fr.src = "about:blank";   // unloaded, not just hidden: two live copies would each keep their own unsaved code
      else paint();   // a fresh load, from what the pinned copy saved
    };
    paint(); sync();
    addEventListener("themechange", paint, { signal: ac.signal }); DARK.addEventListener("change", paint, { signal: ac.signal });
    addEventListener("previewchange", sync, { signal: ac.signal });
  });
  return el;
}
