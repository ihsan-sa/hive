// A Python box inside a chat message (the person, 11141319104c: "a python box like here in the dashboard chat").
// An agent makes the box with the course's `tutor.py box` and puts the line `pytutor:box/<16 hex>` in its post. For a sign-in that has
// the course's door, the message shows that box in the same locked frame as the side preview (preview/render.js `page`): the course page
// in a sandbox with no allow-same-origin, its own one-time door key, the same relay and routes. The chat runs and renders no code; only
// the 16-hex id from the post reaches the frame's address (warden 111413221f61). Everyone else sees the line as text.
import { h } from "../core/dom.js";
import { S } from "../core/store.js";
import { viewHref } from "./preview.js";

const DOOR = "pytutor";
const MARK = /^[ \t]*`?pytutor:box\/([0-9a-f]{16})`?[ \t]*$/gm;
/** The box ids a post names, each once, at most four. */
export function boxIds(text) { const out = []; for (const m of String(text || "").matchAll(MARK)) if (!out.includes(m[1])) out.push(m[1]); return out.slice(0, 4); }
export const dropBoxLines = t => String(t).replace(MARK, "").replace(/\n{3,}/g, "\n\n").trim();
function coursePage() { const d = S.app && S.app.doors; if (!d) return null; for (const [page, name] of Object.entries(d)) if (name === DOOR) return page; return null; }
export const boxesOn = () => !!coursePage();

// One live frame per post and box, kept across the chat's redraws: morph meets a bare copy (data-own, same sig) and leaves the real one alone.
const LIVE = new Map();
const hex = n => Array.from(crypto.getRandomValues(new Uint8Array(n)), b => b.toString(16).padStart(2, "0")).join("");
export function boxEmbed(pid, id) {
  const k = pid + ":" + id, had = LIVE.get(k);
  if (had) return had.el.isConnected ? had.el.cloneNode(false) : had.el;
  const page = coursePage(); if (!page) return null;
  for (const [kk, v] of LIVE) if (LIVE.size > 24 && !v.el.isConnected) { v.ac.abort(); LIVE.delete(kk); }
  const key = hex(16), ac = new AbortController();
  const fr = h("iframe", { class: "pybox-f", title: "Python box", sandbox: "allow-scripts allow-modals allow-pointer-lock", referrerpolicy: "no-referrer", loading: "lazy" });
  const el = h("div", { class: "pybox", "data-key": "box-" + id, "data-own": "", "data-sig": id }, fr);
  LIVE.set(k, { el, ac });
  import("../preview/render.js").then(m => {   // the listener is on before the page can ask anything
    m.relay(fr, DOOR, key, ac.signal, px => { fr.style.height = px + "px"; });
    fr.src = viewHref({ kb: page, name: page.split("/").pop() }) + "#door=" + key + "&box=" + id;
  });
  return el;
}
