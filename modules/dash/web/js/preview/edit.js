// The preview panel's Edit tab for a note or project Markdown file (js/ui/preview.js, render.js): the text in a plain editor, saved
// back with POST /api/kb/save (dash.py kb_save), and beside it, when the panel is wide enough, the pages as they will look (paged.js).
// The save carries the version the editor started from; if the file changed on disk since, the server says so (409) and nothing of
// yours is lost: your text stays here to copy or save over it. Ctrl/⌘+S saves.
// A file with Windows line ends (CRLF) is edited with plain ones and saved back with CRLF, so opening it doesn't count as a change.
// editor(it, text, version) → node (.pv-ed), with node._ed = { dirty(), version(), text(), reload(text, version) } for the panel's
// live check and its Copy, Download and Save as.
import { h, tr } from "../core/dom.js";
import { post } from "../core/store.js";
import { button } from "../ui/kit.js";
import { paged } from "./paged.js";

const WIDE = 860;

const lf = t => t.replace(/\r\n?/g, "\n");

export function editor(it, text, version) {
  const crlf = /\r\n/.test(text);
  text = lf(text);
  let base = text, ver = version, busy = false, tm = 0;
  const ta = h("textarea", { class: "pv-ed-in", spellcheck: "true", "aria-label": tr("Text of " + it.name) });
  ta.value = text;
  const state = h("span", { class: "meta pv-ed-st", role: "status", "aria-live": "polite" });
  const save = button("Save", () => go(), { size: "s", kind: "primary", cls: "pv-ed-save" });
  const over = button("Save over it", () => go(true), { size: "s", kind: "quiet", cls: "pv-ed-over" });
  over.hidden = true;
  const side = h("div", { class: "pv-ed-pv", "aria-label": tr("How it will look") });
  const node = h("div", { class: "pv-ed" },
    h("div", { class: "pv-ed-bar" }, state, h("span", { class: "pv-sp" }), over, save),
    h("div", { class: "pv-ed-two" }, ta, side));
  const dirty = () => ta.value !== base;
  const say = (t, kind) => { state.textContent = tr(t); state.dataset.k = kind || ""; };
  const mark = () => { save.disabled = busy || !dirty(); if (!busy && state.dataset.k !== "err" && state.dataset.k !== "conflict") say(dirty() ? "Not saved yet" : "Saved", dirty() ? "" : "ok"); };
  const draw = () => { if (node.dataset.wide != null) side.replaceChildren(paged(ta.value)); };

  async function go(force) {
    if (busy || (!dirty() && !force)) return;
    busy = true; save.disabled = true; say("Saving…");
    const t = ta.value, out = crlf ? t.replace(/\n/g, "\r\n") : t;
    try {
      if (force) {   // saving over a change made elsewhere: take the file's newest version, on purpose
        const o = await fetch("/api/kb/open?path=" + encodeURIComponent(it.kb), { cache: "no-store" });
        if (!o.ok) throw Error("couldn't read the file again (" + o.status + ")");
        ver = (await o.json()).version;
      }
      const r = await post("/api/kb/save", { path: it.kb, text: out, version: ver });
      base = t; ver = r.version; over.hidden = true; busy = false; say("Saved", "ok");
    } catch (e) {
      busy = false;
      if (e.status === 409) { say("This file changed while you were editing. Your text is still here: save over it, or copy it first.", "conflict"); over.hidden = false; }
      else say(tr("Couldn't save") + ": " + tr(e.message), "err");
    }
    mark();
  }
  ta.addEventListener("input", () => { if (state.dataset.k === "err") say(""); mark(); clearTimeout(tm); tm = setTimeout(draw, 350); });
  node.addEventListener("keydown", e => {
    if ((e.ctrlKey || e.metaKey) && !e.altKey && e.key.toLowerCase() === "s") { e.preventDefault(); e.stopPropagation(); go(); }
    else if (e.key === "Escape" && e.target === ta) e.stopPropagation();   // Esc in the editor doesn't close the panel under it
  });
  // Wide (expanded, or a wide panel): the text and its pages side by side. Narrow: just the text.
  new ResizeObserver(() => {
    const wide = node.clientWidth >= WIDE;
    if (wide === (node.dataset.wide != null)) return;
    if (wide) { node.dataset.wide = ""; draw(); } else { delete node.dataset.wide; side.replaceChildren(); }
  }).observe(node);
  node._ed = {
    dirty, version: () => ver, text: () => crlf ? ta.value.replace(/\n/g, "\r\n") : ta.value,
    // A newer file on disk: taken in place under clean text. While a save is under way it is ours (or the save will say it isn't).
    reload(t, v) { if (busy) return true; if (dirty()) return false; t = lf(t); const at = ta.selectionStart; ta.value = base = t; ver = v; ta.selectionStart = ta.selectionEnd = Math.min(at, t.length); draw(); mark(); return true; },
  };
  mark();
  return node;
}
