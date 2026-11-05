// File preview: a post's file, or a file link in a message, opens in a side panel beside the conversation, the way Claude and
// ChatGPT show an artifact. On a phone (a narrow window) it is a sheet from the bottom. The panel is a fixed <aside> outside
// #main, so the view under it keeps polling and morphing; on a wide window #main and #top get a right margin and reflow beside it.
// Renderers live in js/preview/ and load the first time a file needs one. Everything a file holds goes in as text or through
// an <img>/<video>/<audio>/<iframe> pointed at /files/, which dash.py serves sandboxed: HTML runs in an iframe with an opaque
// origin (sandbox="allow-scripts", plus the server's sandbox CSP), so it never touches this page, its cookie or the API.
// openPreview(items, i): items are { id, name, size, from, t }; id is the post's 12-hex id, name the file name.
import { h } from "../core/dom.js";
import { stamp } from "../core/format.js";
import { button, icon } from "./kit.js";

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
const kbRaw = it => "/api/kb/raw?path=" + encodeURIComponent(it.kb);
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
    out.push(KNOWN.get(k) || { id, name: rest.join("/") });
  }
  return out;
}
const fsize = n => (n == null ? "" : n >= 1048576 ? (n / 1048576).toFixed(1) + " MB" : n >= 1024 ? Math.round(n / 1024) + " kB" : n + " B");

// ---- the panel ----
const WMIN = 340, KEY = "pv.w", MODE = new Map();   // MODE: file key -> "src" when the person asked for the source
const maxW = () => Math.max(WMIN, innerWidth - 320);
const clampW = w => Math.max(WMIN, Math.min(maxW(), Math.round(w)));
function setW(w, save) { w = clampW(w); document.documentElement.style.setProperty("--pv-w", w + "px"); if (save) try { localStorage.setItem(KEY, String(w)); } catch (e) {} return w; }
const P = { root: null, head: null, body: null, grip: null, items: [], i: 0, back: null, seq: 0, ctl: null, list: false };

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
export function openFiles() {
  shell();
  P.list = true; P.seq++; P.ctl && P.ctl.abort(); P.ctl = null;
  P.root.setAttribute("aria-label", "Files"); P.root.dataset.kind = "files";
  mark();
  import("../preview/files.js").then(m => { if (P.root && P.list) m.drawFiles(P.head, P.body, { open: openPreview, close: closePreview }); });
  P.root.focus({ preventScroll: true });
}

function shell() {
  import("./panel.js").then(m => { if (m.panelOpen()) m.closePanel(); }, () => {});   // one side panel at a time
  if (!P.root) {
    P.head = h("div", { class: "pv-h" }); P.body = h("div", { class: "pv-s" });
    P.root = h("aside", { class: "pv", tabindex: -1, role: "complementary" }, h("div", { class: "pv-grab", "aria-hidden": "true" }), P.head, P.body);
    P.root.addEventListener("keydown", e => {
      if (e.target.closest("input,textarea,select,[contenteditable]")) return;
      if (!P.list && P.items.length > 1 && (e.key === "ArrowRight" || e.key === "ArrowLeft") && !e.target.closest(".pv-s")) { e.preventDefault(); show(P.i + (e.key === "ArrowRight" ? 1 : -1)); }
    });
    P.back = document.activeElement;
    // A width you set is kept; otherwise it opens near half the window, as wide as Claude's and ChatGPT's file views.
    let w = 0; try { w = +localStorage.getItem(KEY); } catch (e) {}
    setW(w || Math.max(560, innerWidth * 0.45), false);
    P.grip = grip();
    document.body.append(P.root, P.grip);
    document.body.setAttribute("data-pv", "");
  }
}

export function closePreview() {
  if (!P.root) return;
  P.ctl && P.ctl.abort();
  P.root.remove(); P.grip.remove(); document.body.removeAttribute("data-pv"); document.body.removeAttribute("data-pv-full");
  if (P.blob) { URL.revokeObjectURL(P.blob); P.blob = null; }
  const back = P.back;
  Object.assign(P, { root: null, head: null, body: null, grip: null, items: [], i: 0, back: null, ctl: null, list: false });
  mark();
  if (back && back.isConnected && back.focus) back.focus({ preventScroll: true });
}

function header(it, r) {
  const n = P.items.length, k = keyOf(it), src = MODE.get(k) === "src";
  const who = it.from ? (it.from === "person" ? "you" : it.from) : null;
  const meta = [fsize(it.size), who && "from " + who, it.kb ? it.kb.replace(/\/[^/]*$/, "") : it.t && stamp(it.t)].filter(Boolean).join(" · ");
  // A project file downloads as its raw bytes only if it's an image or PDF; otherwise as the text shown, and Open goes to Knowledge.
  const dl = !it.kb || kbRawOk(it) ? fileHref(it) : r && r.text != null ? URL.createObjectURL(new Blob([r.text], { type: "text/plain" })) : null;
  if (P.blob) URL.revokeObjectURL(P.blob); P.blob = dl && dl.startsWith("blob:") ? dl : null;
  const tabs = r && r.source ? h("div", { class: "pv-tabs", role: "group", "aria-label": "Show" },
    ["Preview", "Source"].map((t, j) => h("button", { type: "button", class: "pv-tab", "aria-pressed": String((j === 1) === src), onclick: () => {
      if ((j === 1) === (MODE.get(k) === "src")) return;
      if (j) MODE.set(k, "src"); else MODE.delete(k); show(P.i);
    } }, t))) : null;
  const copy = r && r.text != null ? button("Copy", (e, b) => {
    navigator.clipboard.writeText(r.text).then(() => { b.dataset.ok = ""; b.lastChild.textContent = "Copied"; setTimeout(() => { if (b.isConnected) { delete b.dataset.ok; b.lastChild.textContent = "Copy"; } }, 1400); }, () => {});
  }, { size: "s", kind: "quiet", icon: "copy", title: "Copy the text" }) : null;
  P.head.replaceChildren(
    h("div", { class: "pv-t" },
      button("All files", openFiles, { kind: "icon", icon: "archive", size: "s", cls: "pv-files" }),
      h("b", { class: "pv-badge", "data-kind": kindOf(it.name) }, badgeOf(it.name)),
      h("div", { class: "pv-n" }, switcher(it), meta ? h("span", { class: "meta" }, meta) : null),
      n > 1 ? h("span", { class: "pv-nav" },
        button("Previous file", () => show(P.i - 1), { kind: "icon", icon: "chevron", size: "s", cls: "pv-prev" }),
        h("span", { class: "meta" }, `${P.i + 1} / ${n}`),
        button("Next file", () => show(P.i + 1), { kind: "icon", icon: "chevron", size: "s", cls: "pv-next" })) : null,
      button("Copy link", (e, b) => {
        const url = it.kb ? location.origin + location.pathname + "#knowledge/" + encodeURIComponent(it.kb) : new URL(viewHref(it), location.href).href;
        navigator.clipboard.writeText(url).then(() => { b.dataset.ok = ""; setTimeout(() => b.isConnected && delete b.dataset.ok, 1400); }, () => {});
      }, { kind: "icon", icon: "link", size: "s", cls: "pv-link" }),
      fullBtn(),
      button("Close (Esc)", closePreview, { kind: "icon", icon: "close", size: "s", cls: "pv-x" })),
    h("div", { class: "pv-a" }, tabs, h("span", { class: "pv-sp" }), copy,
      dl ? h("a", { class: "btn", "data-size": "s", "data-kind": "quiet", href: dl, download: it.name, title: "Download " + it.name }, "Download") : null,
      it.kb ? h("a", { class: "btn", "data-size": "s", "data-kind": "quiet", href: "#knowledge/" + encodeURIComponent(it.kb), title: "Open in Knowledge" }, "Open")
        : h("a", { class: "btn", "data-size": "s", "data-kind": "quiet", href: viewHref(it), target: "_blank", rel: "noopener noreferrer", title: "Open in a new tab" }, "Open")));
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
  const menu = h("div", { class: "pv-menu", role: "menu", hidden: true });
  menu._shut = () => { shut(); b.focus(); };
  const shut = () => { menu.hidden = true; b.setAttribute("aria-expanded", "false"); removeEventListener("pointerdown", away, true); };
  const away = e => { if (!b.parentNode.contains(e.target)) shut(); };
  const b = h("button", { type: "button", class: "pv-sw", "aria-haspopup": "menu", "aria-expanded": "false", title: "Switch file", onclick: () => {
    if (!menu.hidden) return shut();
    menu.replaceChildren(...list.map(x => h("button", { type: "button", role: "menuitemradio", class: "pv-mi", "aria-checked": String(keyOf(x) === k), onclick: () => {
      shut(); const j = P.items.findIndex(y => keyOf(y) === keyOf(x));
      if (j >= 0) show(j); else openPreview(list, list.indexOf(x));
    } }, h("b", { class: "pv-badge", "data-kind": kindOf(x.name) }, badgeOf(x.name)), h("span", null, x.name))));
    menu.hidden = false; b.setAttribute("aria-expanded", "true"); addEventListener("pointerdown", away, true);
    (menu.querySelector("[aria-checked=true]") || menu.firstChild).focus();
  } }, name, icon("chevron-down"));
  menu.addEventListener("keydown", e => {
    const all = [...menu.children], i = all.indexOf(document.activeElement), d = e.key === "ArrowDown" ? 1 : e.key === "ArrowUp" ? -1 : 0;
    if (d) { e.preventDefault(); e.stopPropagation(); all[(i + d + all.length) % all.length].focus(); }
  });
  return h("span", { class: "pv-swb" }, b, menu);
}

async function show(k) {
  const n = P.items.length; P.i = ((k % n) + n) % n;
  const it = P.items[P.i], my = ++P.seq;
  P.ctl && P.ctl.abort(); P.ctl = new AbortController();
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
}

// Capture, so Escape closes the preview and doesn't reach the view under it. A window open on top gets it first; so does
// the agent panel when it's open (it closes first, then a second Escape closes this).
addEventListener("keydown", e => {
  if (e.key !== "Escape" || !P.root || document.querySelector("dialog[open]") || document.querySelector("aside.panel")) return;
  if (document.fullscreenElement) return;
  e.stopPropagation();
  const m = P.root.querySelector(".pv-menu:not([hidden])");
  if (m) m._shut(); else closePreview();
}, true);
addEventListener("hashchange", () => { if (P.root) mark(); });

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
