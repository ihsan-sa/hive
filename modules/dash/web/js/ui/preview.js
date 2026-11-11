// File preview: a post's file, or a file link in a message, opens in a side panel beside the conversation, the way Claude and
// ChatGPT show an artifact. On a phone (a narrow window) it is a sheet from the bottom. The panel is a fixed <aside> outside
// #main, so the view under it keeps polling and morphing; on a wide window #main and #top get a right margin and reflow beside it.
// Renderers live in js/preview/ and load the first time a file needs one. Everything a file holds goes in as text or through
// an <img>/<video>/<audio>/<iframe> pointed at /files/, which dash.py serves sandboxed: HTML runs in an iframe with an opaque
// origin (sandbox="allow-scripts", plus the server's sandbox CSP), so it never touches this page, its cookie or the API.
// openPreview(items, i): items are { id, name, size, from, t }; id is the post's 12-hex id, name the file name.
import { h } from "../core/dom.js";
import { stamp, ago } from "../core/format.js";
import { button, icon, copyPress, scrim } from "./kit.js";

/** A file's kind, from its name: picks the renderer and the badge. */
export const KINDS = [
  ["image", /\.(png|jpe?g|gif|webp|avif|bmp|ico)$/i, "IMG"],
  ["svg", /\.svg$/i, "SVG"],
  ["video", /\.(mp4|webm|mov|m4v)$/i, "VIDEO"],
  ["audio", /\.(mp3|wav|ogg|oga|opus|m4a|aac|flac|weba)$/i, "AUDIO"],
  ["pdf", /\.pdf$/i, "PDF"],
  ["html", /\.html?$/i, "PAGE"],
  ["md", /\.(md|markdown|mdown)$/i, "MD"],
  ["table", /\.(csv|tsv)$/i, null],
  ["notebook", /\.ipynb$/i, "NB"],
  ["json", /\.(json|geojson|webmanifest)$/i, "JSON"],
  ["xlsx", /\.(xlsx|xlsm)$/i, "XLSX"],
  ["docx", /\.docx$/i, "DOCX"],
  ["pptx", /\.pptx$/i, "PPTX"],
  ["diff", /\.(diff|patch)$/i, "DIFF"],
  ["mermaid", /\.(mmd|mermaid)$/i, "MMD"],
  ["latex", /\.(tex|ltx)$/i, "TEX"],
  ["touchstone", /\.s[1-4]p$/i, null],
  ["spice", /\.(sp|cir|spice|ckt)$/i, null],
  ["code", /\.(py|pyi|sh|bash|zsh|c|h|cc|cpp|cxx|hpp|hh|rs|js|mjs|cjs|jsx|ts|tsx|yaml|yml|toml|tcl|sdc|xdc|v|sv|vh|svh|go|java|kt|rb|lua|css|scss|xml|sql|r|jl|m|swift|ini|cfg|conf|env|jsonl|ndjson|mk|cmake|dockerfile|lef|def|kicad_pcb|kicad_sch|kicad_sym|net|lib|gds\.txt|vcd)$|^(makefile|dockerfile|justfile|gemfile|rakefile)$/i, null],
  ["text", /\.(txt|log|out|err|text|rst|adoc|org|bib|srt|vtt|license)$|^(readme|license|copying|changelog|notice|authors)$/i, null],
];
export function kindOf(name) { for (const [k, re] of KINDS) if (re.test(name)) return k; return "other"; }
export function badgeOf(name) {
  for (const [, re, b] of KINDS) if (re.test(name)) return b || (name.split(".").pop() || "FILE").slice(0, 5).toUpperCase();
  return (name.includes(".") ? name.split(".").pop() : "FILE").slice(0, 5).toUpperCase();
}
/** Kinds that need the server's ?view=1 (a page, a frame, or a player with seeking). */
const VIEW = new Set(["svg", "video", "audio", "pdf", "html"]);
// A project's file (it.kb, a home-relative path) comes through the Knowledge API instead, with its rules: text redacted,
// only images and PDFs as raw bytes. Such an item is { kb, name, size, t }.
const kbRaw = it => "/api/kb/raw?path=" + encodeURIComponent(it.kb) + (/\.html?$/i.test(it.name) ? "&view=1" : "");   // a page runs in the preview, redacted by the server (R111)
export const kbRawOk = it => /\.(png|jpe?g|gif|webp|pdf)$/i.test(it.name);
export const fileHref = it => (it.kb ? kbRaw(it) : `/files/${encodeURIComponent(it.id)}/${encodeURIComponent(it.name)}`);
export const viewHref = it => (it.kb ? kbRaw(it) : fileHref(it) + (VIEW.has(kindOf(it.name)) ? "?view=1" : ""));
export const keyOf = it => (it.kb ? "kb:" + it.kb : it.id + "/" + it.name);
// Every file chip drawn in a message, by key, so the Files list can show this conversation's files with their size and sender.
const KNOWN = new Map();
export const remember = it => { KNOWN.set(keyOf(it), it); return it; };
export function threadFiles() {
  const seen = new Set(), out = [];
  for (const el of document.querySelectorAll("#main [data-pv-key]")) {
    const k = el.dataset.pvKey; if (seen.has(k)) continue; seen.add(k);
    const [id, ...rest] = k.split("/");
    out.push(KNOWN.get(k) || (k.startsWith("kb:") ? { kb: k.slice(3), name: rest[rest.length - 1] } : { id, name: rest.join("/") }));
  }
  return out;
}
const fsize = n => (n == null ? "" : n >= 1048576 ? (n / 1048576).toFixed(1) + " MB" : n >= 1024 ? Math.round(n / 1024) + " kB" : n + " B");

// ---- the panel ----
const WMIN = 340, KEY = "pv.w", MODE = new Map();   // MODE: file key -> "src" when the person asked for the source
const maxW = () => Math.max(WMIN, innerWidth - 320);
const clampW = w => Math.max(WMIN, Math.min(maxW(), Math.round(w)));
function setW(w, save) { w = clampW(w); document.documentElement.style.setProperty("--pv-w", w + "px"); if (save) try { localStorage.setItem(KEY, String(w)); } catch (e) {} return w; }
function fitW() { let w = 0; try { w = +localStorage.getItem(KEY); } catch (e) {} setW(w || Math.max(560, innerWidth * 0.45), false); }
const P = { root: null, head: null, body: null, grip: null, items: [], i: 0, back: null, seq: 0, ctl: null, list: false, auto: false };

export const previewOpen = () => !!P.root;
export const previewKey = () => (P.root && !P.list && P.items[P.i] ? keyOf(P.items[P.i]) : null);

function grip() {
  const g = h("div", { class: "pv-grip", role: "separator", "aria-orientation": "vertical", "aria-label": "Resize the preview", tabindex: 0, title: "Drag to resize, double-click to reset" });
  const cur = () => (P.root ? P.root.getBoundingClientRect().width : 560);
  const move = e => setW(innerWidth - e.clientX - 8, false);
  const up = () => { g.classList.remove("on"); removeEventListener("pointermove", move); removeEventListener("pointerup", up); removeEventListener("pointercancel", up); document.body.removeAttribute("data-pv-drag"); setW(cur(), true); };
  g.onpointerdown = e => { e.preventDefault(); g.classList.add("on"); document.body.setAttribute("data-pv-drag", ""); addEventListener("pointermove", move); addEventListener("pointerup", up); addEventListener("pointercancel", up); };
  g.ondblclick = () => { try { localStorage.removeItem(KEY); } catch (e) {} setW(Math.max(560, innerWidth * 0.45), false); };
  g.onkeydown = e => { const d = e.key === "ArrowLeft" ? 32 : e.key === "ArrowRight" ? -32 : 0; if (d) { e.preventDefault(); setW(cur() + d, true); } };
  return g;
}

function mark() {
  const k = previewKey();
  for (const el of document.querySelectorAll("[data-pv-key]")) el.toggleAttribute("data-pv-on", el.dataset.pvKey === k);
}

/** Open items[i] in the panel. Opening while it's open just switches the file. */
export function openPreview(items, i = 0) {
  items = (items || []).filter(x => x && (x.id || x.kb) && x.name);
  if (!items.length) return;
  shell();
  P.items = items; P.list = false;
  show(i);
  P.root.focus({ preventScroll: true });
}

/** The Files list in the panel: this conversation's files and a project's folders. */
export function openFiles(auto) {
  if (auto !== true) { try { localStorage.removeItem(AUTO); } catch (e) {} }   // opened by hand: it opens by itself again
  shell();
  P.list = true; P.seq++; clearTimeout(P.live); P.ctl && P.ctl.abort(); P.ctl = null;
  P.root.setAttribute("aria-label", "Files"); P.root.dataset.kind = "files"; P.foot.hidden = true;
  P.auto = auto === true; placeFiles();
  mark();
  import("../preview/files.js").then(m => { if (P.root && P.list) m.drawFiles(P.head, P.body, { open: openPreview, close: closePreview }); });
  if (auto !== true) P.root.focus({ preventScroll: true });
}

// The Files list opens by itself, small and floating, when a conversation is shown on a wide window; closing it by hand turns that off.
const AUTO = "pv.autoFiles.off";
let autoFor = null;
export function autoFiles(key) {
  if (key === autoFor) return; autoFor = key;
  if (P.root && P.list) { const by = P.auto; openFiles(true); P.auto = by; return; }   // another conversation: its files, still the hand's if the hand opened it
  let off = false; try { off = !!localStorage.getItem(AUTO); } catch (e) {}
  if (innerWidth < 1100) autoFor = null;   // seen while narrow doesn't count: the same conversation on a wider window gets its list
  if (off || innerWidth < 1100 || P.root) return;   // narrower, there is never room beside the messages
  setTimeout(() => {   // the messages draw first: open only when this conversation has files and room beside them
    if (key !== autoFor || P.root || !threadFiles().length || !filesRoom()) return;
    import("./panel.js").then(m => { if (!m.panelOpen()) openFiles(true); }, () => openFiles(true));
  }, 500);
}
// Room for the small Files list (208px wide, 16px in from the right: components.css) clear of the message column, with a gap.
const FILES_W = 208 + 16 + 16;
export function filesRoom() {
  let right = 0;
  for (const m of document.querySelectorAll("#main .cm-msgs .msg")) right = Math.max(right, m.getBoundingClientRect().right);
  return right > 0 && innerWidth - right >= FILES_W;
}
// The small list sits just under the conversation's head, whose controls it covered when placed by a fixed offset (the top bar's height changes).
function placeFiles() {
  const head = P.root && document.querySelector("#main .cm-head");
  if (head) P.root.style.setProperty("--files-top", Math.round(head.getBoundingClientRect().bottom + 8) + "px");
}
// A list that opened by itself goes when the window narrows and it would cover the messages; one opened by hand stays.
let filesResize = 0;
addEventListener("resize", () => {
  clearTimeout(filesResize);
  filesResize = setTimeout(() => {
    if (!P.root) return;
    fitW();   // opened narrow, it would otherwise stay at the minimum after widening
    if (!P.list) return;
    placeFiles();
    if (P.auto && (innerWidth < 1100 || !filesRoom())) { autoFor = null; closePreview(); }
  }, 150);
});
export function filesClosedByHand() { try { localStorage.setItem(AUTO, "1"); } catch (e) {} }

function shell() {
  import("./panel.js").then(m => { if (m.panelOpen()) m.closePanel(); }, () => {});   // one side panel at a time
  if (!P.root) {
    P.head = h("div", { class: "pv-h" }); P.body = h("div", { class: "pv-s" }); P.foot = h("div", { class: "pv-f", hidden: true });
    P.root = h("aside", { class: "pv", tabindex: -1, role: "complementary" }, h("div", { class: "pv-grab", "aria-hidden": "true" }), P.head, P.body, P.foot);
    P.root.addEventListener("keydown", e => {
      if (e.target.closest("input,textarea,select,[contenteditable]")) return;
      if (!P.list && P.items.length > 1 && (e.key === "ArrowRight" || e.key === "ArrowLeft") && !e.target.closest(".pv-s")) { e.preventDefault(); show(P.i + (e.key === "ArrowRight" ? 1 : -1)); }
    });
    P.back = document.activeElement;
    // A width you set is kept; otherwise it opens near half the window, as wide as Claude's and ChatGPT's file views.
    fitW();
    P.grip = grip();
    document.body.append(P.root, P.grip);
    document.body.setAttribute("data-pv", "");
  }
}

export function closePreview() {
  if (!P.root) return;
  P.ctl && P.ctl.abort(); clearTimeout(P.live);
  P.root.remove(); P.grip.remove(); document.body.removeAttribute("data-pv"); document.body.removeAttribute("data-pv-full");
  if (P.blob) { URL.revokeObjectURL(P.blob); P.blob = null; }
  const back = P.back;
  Object.assign(P, { root: null, head: null, body: null, foot: null, grip: null, items: [], i: 0, back: null, ctl: null, list: false });
  mark();
  if (back && back.isConnected && back.focus) back.focus({ preventScroll: true });
}

// The word for a file's kind, first in the header's meta line (R7: "Type · size · from agent, when").
const TYPE = { image: "Image", svg: "Drawing", video: "Video", audio: "Sound", pdf: "PDF", html: "Web page", md: "Note", table: "Table", notebook: "Notebook",
  json: "Data", xlsx: "Spreadsheet", docx: "Document", pptx: "Slides", diff: "Change", mermaid: "Diagram", latex: "LaTeX", touchstone: "Network data",
  spice: "Circuit", code: "Code", text: "Text" };
export const typeOf = name => TYPE[kindOf(name)] || "File";
function header(it, r) {
  const n = P.items.length, k = keyOf(it), src = MODE.get(k) === "src";
  const who = it.from ? (it.from === "person" ? "you" : it.from) : null;
  const when = it.t ? stamp(it.t) : null;
  const meta = [typeOf(it.name), fsize(it.size), it.kb ? it.kb.replace(/\/[^/]*$/, "") : who ? "from " + who + (when ? ", " + when : "") : when].filter(Boolean).join(" · ");
  // A project file downloads as its raw bytes only if it's an image or PDF; otherwise as the text shown, and Open goes to Knowledge.
  const dl = !it.kb || kbRawOk(it) ? fileHref(it) : r && r.text != null ? URL.createObjectURL(new Blob([r.text], { type: "text/plain" })) : null;
  if (P.blob) URL.revokeObjectURL(P.blob); P.blob = dl && dl.startsWith("blob:") ? dl : null;
  // Preview (and Source when the file has one), then "All files here · N": this conversation's files, in the same panel.
  const here = it.kb ? 0 : threadFiles().length;
  const tabs = r && r.source || here > 1 ? h("div", { class: "pv-tabs", role: "group", "aria-label": "Show" },
    (r && r.source ? ["Preview", "Source"] : ["Preview"]).map((t, j) => h("button", { type: "button", class: "pv-tab", "aria-pressed": String((j === 1) === src), onclick: () => {
      if ((j === 1) === (MODE.get(k) === "src")) return;
      if (j) MODE.set(k, "src"); else MODE.delete(k); show(P.i);
    } }, t)),
    here > 1 ? h("button", { type: "button", class: "pv-tab pv-here", "aria-pressed": "false", title: "This conversation's files", onclick: () => openFiles() }, `All files here · ${here}`) : null) : null;
  // The footer: where the file is, in mono, and Copy path.
  const path = it.kb ? "~/" + it.kb : `files/${it.id}/${it.name}`;
  P.foot.hidden = false;
  P.foot.replaceChildren(h("code", { class: "pv-path", title: path }, path), button("Copy path", (e, b) => copyPress(b, path), { size: "s", kind: "quiet", icon: "copy", cls: "pv-cp" }));
  const copy = r && r.text != null ? button("Copy", (e, b) => copyPress(b, r.text), { size: "s", kind: "quiet", icon: "copy", title: "Copy the text" }) : null;
  P.head.replaceChildren(
    h("div", { class: "pv-t" },
      button("All files", openFiles, { kind: "icon", icon: "folder", size: "s", cls: "pv-files" }),
      h("b", { class: "pv-badge", "data-kind": kindOf(it.name) }, badgeOf(it.name)),
      h("div", { class: "pv-n" }, switcher(it), meta ? h("span", { class: "meta" }, meta) : null),
      n > 1 ? h("span", { class: "pv-nav" },
        button("Previous file", () => show(P.i - 1), { kind: "icon", icon: "chevron", size: "s", cls: "pv-prev" }),
        h("span", { class: "meta" }, `${P.i + 1} / ${n}`),
        button("Next file", () => show(P.i + 1), { kind: "icon", icon: "chevron", size: "s", cls: "pv-next" })) : null,
      button("Copy link", (e, b) => {
        const url = it.kb ? location.origin + location.pathname + "#knowledge/" + encodeURIComponent(it.kb) : new URL(viewHref(it), location.href).href;
        copyPress(b, url);
      }, { kind: "icon", icon: "link", size: "s", cls: "pv-link" }),
      fullBtn(),
      button("Close (Esc)", closePreview, { kind: "icon", icon: "close", size: "s", cls: "pv-x" })),
    h("div", { class: "pv-a" }, tabs, it.kb ? h("span", { class: "meta pv-live", title: "This panel shows the file's newest version and updates itself" }, "") : null, h("span", { class: "pv-sp" }), copy, saveAs(it, r),
      dl ? h("a", { class: "btn", "data-size": "s", "data-kind": "quiet", href: dl, download: it.name, title: "Download " + it.name }, "Download") : null,
      it.kb && kindOf(it.name) !== "html" ? h("a", { class: "btn", "data-size": "s", "data-kind": "quiet", href: "#knowledge/" + encodeURIComponent(it.kb), title: "Open in Knowledge" }, "Open")   // a page opens running, like an attached one; Knowledge would show its code
        : h("a", { class: "btn", "data-size": "s", "data-kind": "quiet", href: viewHref(it), target: "_blank", rel: "noopener noreferrer", title: "Open in a new tab" }, "Open")));
}

// Save as: Markdown (the text as shown), Word and PDF (the rendered document). Only for a shown Markdown or text file.
function saveAs(it, r) {
  if (!r || r.text == null || !["md", "text"].includes(kindOf(it.name)) && !/\.(md|markdown|txt)$/i.test(it.name)) return null;
  const doc = () => P.body.querySelector(".pv-md") || P.body.querySelector(".pv-doc") || P.body;
  const go = f => e => { e.currentTarget.closest("details").open = false; import("../preview/docexport.js").then(m => f(m)); };
  const item = (label, f) => h("button", { type: "button", class: "pv-mi", role: "button", onclick: go(f) }, label);
  return h("details", { class: "pv-save" },
    h("summary", { class: "btn", "data-size": "s", "data-kind": "quiet", title: "Save this document" }, icon("download"), h("span", null, "Save as")),
    h("div", { class: "pv-menu pv-sv", role: "group" },
      item("PDF", m => m.savePdf(it.name, doc())), item("Word (.doc)", m => m.saveWord(it.name, doc())), item("Markdown (.md)", () => import("../preview/docexport.js").then(m => m.saveMarkdown(it.name, r.text)))));
}

// A project file stays live: while it is open the panel asks for it again every few seconds and redraws when it changed,
// keeping your place. The head says when it last changed.
const LIVE_MS = 3000;
function live(it, my, known) {
  clearTimeout(P.live);
  if (!it.kb) return;
  const tick = async () => {
    if (my !== P.seq || !P.root || P.list) return;
    const w = P.head && P.head.querySelector(".pv-live");
    if (w && known.t) w.textContent = "changed " + ago(known.t * 1000);
    if (document.hidden) { P.live = setTimeout(tick, LIVE_MS); return; }
    try {
      const r = await fetch("/api/kb/open?path=" + encodeURIComponent(it.kb), { cache: "no-store" });
      if (my !== P.seq) return;
      if (r.ok) {
        const d = await r.json();
        if (known.t && (d.t !== known.t || d.size !== known.size)) {
          Object.assign(known, { t: d.t, size: d.size });
          const top = P.body.scrollTop, m = await import("../preview/render.js");
          const x = await m.render(it, { source: MODE.get(keyOf(it)) === "src" });
          if (my !== P.seq || !P.root || P.list) return;
          header(it, x); P.body.replaceChildren(x.node); P.body.scrollTop = top;
          const w2 = P.head.querySelector(".pv-live"); if (w2) { w2.textContent = "changed just now"; w2.dataset.fresh = ""; setTimeout(() => w2.isConnected && delete w2.dataset.fresh, 4000); }
        } else if (!known.t) { Object.assign(known, { t: d.t, size: d.size }); const w3 = P.head.querySelector(".pv-live"); if (w3) w3.textContent = "changed " + ago(d.t * 1000); }
      }
    } catch (e) {}
    P.live = setTimeout(tick, LIVE_MS);
  };
  tick();
}

// Expand gives the file the whole width, like Claude's expanded artifact; pressed again it goes back beside the chat.
function fullBtn() {
  const on = () => document.body.hasAttribute("data-pv-full");
  const b = button(on() ? "Back to the side" : "Expand", () => {
    document.body.toggleAttribute("data-pv-full", !on());
    b.setAttribute("aria-pressed", String(on())); b.title = on() ? "Back to the side" : "Expand"; b.setAttribute("aria-label", b.title);
  }, { kind: "icon", icon: "expand", size: "s", cls: "pv-full" });
  b.setAttribute("aria-pressed", String(on()));
  return b;
}

// The file name is a switcher, like the title of Claude's artifact view: it lists the files you can move between here (the
// ones opened together, then the rest of this conversation's) and opens the one you pick.
function switcher(it) {
  const k = keyOf(it), seen = new Set(), list = [];
  for (const x of [...P.items, ...threadFiles()]) { const kx = keyOf(x); if (!seen.has(kx)) { seen.add(kx); list.push(x); } }
  const name = h("span", { class: "pv-fn", title: it.name }, it.name);
  if (list.length < 2) return name;
  const menu = h("div", { class: "pv-menu menu", role: "menu", hidden: true }), sc = scrim(() => shut());
  sc.hidden = true;
  menu._shut = () => { shut(); b.focus(); };
  const shut = () => { menu.hidden = true; sc.hidden = true; b.setAttribute("aria-expanded", "false"); removeEventListener("pointerdown", away, true); };
  const away = e => { if (!b.parentNode.contains(e.target)) shut(); };
  const b = h("button", { type: "button", class: "pv-sw", "aria-haspopup": "menu", "aria-expanded": "false", title: "Switch file", onclick: () => {
    if (!menu.hidden) return shut();
    menu.replaceChildren(...list.map(x => h("button", { type: "button", role: "menuitemradio", class: "pv-mi menu-i", "aria-checked": String(keyOf(x) === k), onclick: () => {
      shut(); const j = P.items.findIndex(y => keyOf(y) === keyOf(x));
      if (j >= 0) show(j); else openPreview(list, list.indexOf(x));
    } }, h("b", { class: "pv-badge", "data-kind": kindOf(x.name) }, badgeOf(x.name)), h("span", null, x.name))));
    menu.hidden = false; sc.hidden = false; b.setAttribute("aria-expanded", "true"); addEventListener("pointerdown", away, true);
    (menu.querySelector("[aria-checked=true]") || menu.firstChild).focus();
  } }, name, icon("chevron-down"));
  menu.addEventListener("keydown", e => {
    const all = [...menu.children], i = all.indexOf(document.activeElement), d = e.key === "ArrowDown" ? 1 : e.key === "ArrowUp" ? -1 : 0;
    if (d) { e.preventDefault(); e.stopPropagation(); all[(i + d + all.length) % all.length].focus(); }
  });
  return h("span", { class: "pv-swb pop" }, b, sc, menu);
}

async function show(k) {
  const n = P.items.length; P.i = ((k % n) + n) % n;
  const it = P.items[P.i], my = ++P.seq;
  P.ctl && P.ctl.abort(); P.ctl = new AbortController(); clearTimeout(P.live);
  P.root.setAttribute("aria-label", "Preview of " + it.name);
  P.root.dataset.kind = kindOf(it.name);
  header(it, null);
  P.body.replaceChildren(h("div", { class: "pv-wait meta" }, "Loading…"));
  P.body.scrollTop = 0;
  mark();
  let r;
  try {
    const m = await import("../preview/render.js");
    r = await m.render(it, { source: MODE.get(keyOf(it)) === "src", signal: P.ctl.signal });
  } catch (e) {
    if (e && e.name === "AbortError") return;
    r = { node: h("div", { class: "pv-err" }, h("p", null, "Couldn't show this file: " + (e && e.message || e)),
      it.kb ? h("a", { class: "btn", "data-size": "s", href: "#knowledge/" + encodeURIComponent(it.kb) }, "Open in Knowledge") : h("a", { class: "btn", "data-size": "s", href: fileHref(it), download: it.name }, "Download it")) };
  }
  if (my !== P.seq || !P.root || P.list) return;
  header(it, r);
  P.body.replaceChildren(r.node);
  live(it, my, { t: 0, size: 0 });
}

// Capture, so Escape closes the preview and doesn't reach the view under it. A window open on top gets it first; so does
// the agent panel and a full-screen figure when they're open (they close first, then a second Escape closes this).
addEventListener("keydown", e => {
  if (e.key !== "Escape" || !P.root || document.querySelector("dialog[open]") || document.querySelector("aside.panel") || document.querySelector(".rc-fig[data-full]")) return;   // a full-screen figure is on top: its own Escape handler closes it first
  if (document.fullscreenElement) return;
  e.stopPropagation();
  const m = P.root.querySelector(".pv-menu:not([hidden]):not(.pv-sv)");
  if (m) m._shut(); else closePreview();
}, true);
addEventListener("hashchange", () => { if (P.root && P.list && !/^#comms\/./.test(location.hash)) { closePreview(); return; } if (P.root) mark(); });   // the Files list belongs to a conversation: leaving it closes it

// A project path in a message (markdown.js: data-kind=kbfile) opens the live panel on that file.
document.addEventListener("click", e => {
  const a = e.target.closest && e.target.closest("a.ref[data-kind=kbfile]");
  if (!a || e.defaultPrevented || e.button !== 0 || e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return;
  e.preventDefault();
  const kb = a.dataset.pvKb; openPreview([{ kb, name: kb.split("/").pop() }], 0);
});

// A file link in a message (markdown.js draws them with data-pv-key) opens here, with the message's other file links as Prev/Next.
document.addEventListener("click", e => {
  const a = e.target.closest && e.target.closest("a.ref[data-kind=file]");
  if (!a || e.defaultPrevented || e.button !== 0 || e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return;
  e.preventDefault();
  const box = a.closest(".msg,.md") || a.parentNode, seen = new Set(), items = [];
  for (const x of box.querySelectorAll("a.ref[data-kind=file]")) {
    if (seen.has(x.dataset.pvKey)) continue; seen.add(x.dataset.pvKey);
    const [id, ...rest] = x.dataset.pvKey.split("/"); items.push({ id, name: rest.join("/") });
  }
  openPreview(items, Math.max(0, [...seen].indexOf(a.dataset.pvKey)));
});
