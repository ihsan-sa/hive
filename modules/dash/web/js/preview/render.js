// One renderer per kind of file, for the preview panel (js/ui/preview.js). render(item, {source, signal}) returns
// { node, source, text }: node is what to show; source says a Preview/Source switch makes sense; text (when known) is what Copy copies.
// Rules, as everywhere in the chat: a file's contents only ever become text nodes, or go to the browser through an element
// pointed at /files/ (served sandboxed by dash.py). HTML runs only in a sandboxed frame. Nothing is fetched from elsewhere.
import { h } from "../core/dom.js";
import { said } from "../core/store.js";
import { kindOf, fileHref, viewHref } from "../ui/preview.js";
import { markdown } from "../ui/markdown.js";
import { fenceBlock } from "../rich/blocks.js";
import { codeBlock } from "../rich/code.js";
import { jsonTree } from "./json.js";

const TEXT_MAX = 8 * 1048576, SHOW_MAX = 1500000;
const LANG = { py: "python", pyi: "python", sh: "bash", bash: "bash", zsh: "bash", c: "c", h: "c", cc: "cpp", cpp: "cpp", cxx: "cpp", hpp: "cpp", hh: "cpp",
  rs: "rust", js: "javascript", mjs: "javascript", cjs: "javascript", jsx: "javascript", ts: "typescript", tsx: "typescript", json: "json", jsonl: "json", ndjson: "json",
  yaml: "yaml", yml: "yaml", toml: "toml", tcl: "tcl", sdc: "tcl", xdc: "tcl", v: "verilog", sv: "verilog", vh: "verilog", svh: "verilog", mk: "makefile", makefile: "makefile",
  sp: "spice", cir: "spice", spice: "spice", ckt: "spice", diff: "diff", patch: "diff", html: "html", htm: "html", svg: "xml", xml: "xml", md: "markdown", markdown: "markdown", tex: "latex", ltx: "latex" };
const extOf = name => (/\./.test(name) ? name.split(".").pop() : name).toLowerCase();
const langOf = name => LANG[extOf(name)] || "";

async function fetchOk(it, signal) {
  const r = await fetch(fileHref(it), { cache: "no-store", signal });
  if (!r.ok) throw new Error(r.status === 404 ? "it isn't on this machine any more (404)" : said(r.status));
  const n = +r.headers.get("Content-Length");
  if (n > TEXT_MAX) { r.body && r.body.cancel(); throw new Error(`it's ${Math.round(n / 1048576)} MB, too big to show here; download it instead`); }
  return r;
}
async function textOf(it, signal) {
  if (it.kb) {   // a project's file: the Knowledge API's redacted text, cut at 512 kB by the server
    const r = await fetch("/api/kb/open?path=" + encodeURIComponent(it.kb), { cache: "no-store", signal });
    if (!r.ok) throw new Error(r.status === 404 ? "it isn't there any more, or isn't shown" : r.status === 403 ? "this sign-in can't read project files" : said(r.status));
    const d = await r.json();
    if (d.type !== "md" && d.type !== "text") return { bin: true, t: "", all: "" };
    return { t: d.text || "", cut: !!d.truncated, all: d.truncated ? null : d.text || "" };
  }
  const t = await (await fetchOk(it, signal)).text();
  return t.length > SHOW_MAX ? { t: t.slice(0, SHOW_MAX), cut: true, all: t } : { t, cut: false, all: t };
}
const cutNote = () => h("p", { class: "pv-cut meta" }, "Cut here: the file is longer. Download it for the rest.");
const wrap = (...kids) => h("div", { class: "pv-doc" }, ...kids);
const asCode = (lang, name, tx) => wrap(codeBlock(lang, name, tx.t), tx.cut ? cutNote() : null);

// ---- media ----
function image(it, src) {
  const img = h("img", { src, alt: it.name, draggable: "false" });
  const st = h("div", { class: "pv-img", "data-fit": "" }, img);
  let z = 1;
  const pct = h("span", { class: "meta pv-zp" }, "");
  const set = v => {
    if (v === "fit") { st.setAttribute("data-fit", ""); img.style.width = ""; requestAnimationFrame(() => (pct.textContent = Math.round(cur() * 100) + "%")); return; }
    z = Math.max(0.1, Math.min(8, v)); st.removeAttribute("data-fit");
    img.style.width = Math.round(img.naturalWidth * z) + "px"; pct.textContent = Math.round(z * 100) + "%";
  };
  const cur = () => (st.hasAttribute("data-fit") ? img.getBoundingClientRect().width / (img.naturalWidth || 1) : z);
  img.addEventListener("click", () => set(st.hasAttribute("data-fit") ? 1 : "fit"));
  st.addEventListener("wheel", e => { if (!e.ctrlKey && !e.metaKey) return; e.preventDefault(); set(cur() * (e.deltaY < 0 ? 1.15 : 1 / 1.15)); }, { passive: false });
  const zb = (label, f) => h("button", { type: "button", class: "pv-zb", "aria-label": label, title: label, onclick: f }, label === "Zoom in" ? "+" : label === "Zoom out" ? "−" : label);
  const tools = h("div", { class: "pv-zoom" }, zb("Zoom out", () => set(cur() / 1.25)), pct, zb("Zoom in", () => set(cur() * 1.25)), zb("Fit", () => set("fit")), zb("1:1", () => set(1)));
  img.addEventListener("load", () => { pct.title = `${img.naturalWidth} × ${img.naturalHeight} px`; if (st.hasAttribute("data-fit")) set("fit"); }, { once: true });
  return h("div", { class: "pv-media" }, tools, st);
}

// ---- html: the page runs in a sandboxed frame with an opaque origin (no cookie, no API), from the server's ?view=1 ----
function page(it) {
  const fr = h("iframe", { class: "pv-frame", src: viewHref(it), title: it.name, sandbox: "allow-scripts allow-modals allow-pointer-lock", referrerpolicy: "no-referrer", loading: "lazy" });
  return h("div", { class: "pv-page" },
    h("div", { class: "pv-note meta" }, h("span", null, "Interactive page, sandboxed: it can't reach this dashboard or the network."),
      h("button", { type: "button", class: "pv-zb", onclick: () => { fr.src = viewHref(it); } }, "Reload")),
    fr);
}

// ---- notebooks ----
const joinSrc = s => (Array.isArray(s) ? s.join("") : s || "");
async function notebook(tx, name) {
  const nb = JSON.parse(tx.t);
  const lang = ((nb.metadata && (nb.metadata.kernelspec?.language || nb.metadata.language_info?.name)) || "python").toLowerCase();
  const purify = await import("./clean.js");
  const out = [];
  for (const c of (nb.cells || nb.worksheets?.[0]?.cells || []).slice(0, 2000)) {
    const src = joinSrc(c.source ?? c.input);
    if (c.cell_type === "markdown") { out.push(h("div", { class: "pv-nbc", "data-t": "md" }, markdown(src))); continue; }
    if (c.cell_type !== "code") { out.push(h("div", { class: "pv-nbc", "data-t": "raw" }, codeBlock("", null, src))); continue; }
    const n = c.execution_count ?? c.prompt_number;
    out.push(h("div", { class: "pv-nbc", "data-t": "code" }, h("span", { class: "pv-nbn meta" }, `In [${n ?? " "}]`), codeBlock(lang, null, src)));
    for (const o of c.outputs || []) {
      const d = o.data || {}, box = h("div", { class: "pv-nbo" });
      if (o.output_type === "stream") box.append(h("pre", { class: "pv-pre", "data-stream": o.name || null }, joinSrc(o.text)));
      else if (o.output_type === "error") box.append(codeBlock("ansi", null, (o.traceback || [o.ename + ": " + o.evalue]).join("\n")));
      else {
        const png = d["image/png"], jpg = d["image/jpeg"], svg = d["image/svg+xml"], html = d["text/html"], tex = d["text/latex"], txt = d["text/plain"];
        if (png || jpg) box.append(h("img", { src: `data:image/${png ? "png" : "jpeg"};base64,${joinSrc(png || jpg).replace(/\s+/g, "")}`, alt: "output" }));
        else if (svg) box.append(h("img", { src: "data:image/svg+xml;charset=utf-8," + encodeURIComponent(joinSrc(svg)), alt: "output" }));
        else if (html) box.append(await purify.cleanHtml(joinSrc(html)));
        else if (tex) box.append(markdown(joinSrc(tex)));
        else if (txt) box.append(h("pre", { class: "pv-pre" }, joinSrc(txt)));
        else if (d["application/json"]) box.append(jsonTree(d["application/json"]));
        else continue;
      }
      out.push(box);
    }
  }
  if (!out.length) out.push(h("p", { class: "meta" }, "This notebook has no cells."));
  return wrap(h("div", { class: "pv-nb" }, out));
}

// ---- the dispatcher ----
const noPreview = (it, why) => ({ node: h("div", { class: "pv-err" }, h("p", null, why),
  it.kb ? h("a", { class: "btn", "data-size": "s", href: "#knowledge/" + encodeURIComponent(it.kb) }, "Open in Knowledge")
    : h("a", { class: "btn", "data-size": "s", href: fileHref(it), download: it.name }, "Download it")) });

export async function render(it, { source, signal } = {}) {
  let k = kindOf(it.name);
  if (it.kb) {   // the Knowledge API gives images and PDFs as bytes and everything else as text, so the rest shows as text
    if (k === "pdf") return noPreview(it, "A project's PDF opens in Knowledge or downloads; it doesn't show in this panel.");
    if (["video", "audio", "xlsx", "docx", "pptx"].includes(k)) return noPreview(it, "This kind of project file doesn't show here yet. Attach it to a message to preview it.");
    if (k === "svg" || k === "html") k = "code";
  }
  if (k === "image") return { node: image(it, fileHref(it)) };
  if (k === "video") return { node: h("div", { class: "pv-av" }, h("video", { src: viewHref(it), controls: true, playsinline: true, preload: "metadata", title: it.name })) };
  if (k === "audio") return { node: h("div", { class: "pv-av", "data-a": "" }, h("audio", { src: viewHref(it), controls: true, preload: "metadata", title: it.name })) };
  if (k === "pdf") return { node: h("iframe", { class: "pv-frame", "data-pdf": "", src: viewHref(it), title: it.name }) };
  if (k === "xlsx" || k === "docx" || k === "pptx") {
    const office = await import("./office.js");
    const buf = await (await fetchOk(it, signal)).arrayBuffer();
    return { node: wrap(await office[k](buf, it.name)) };
  }
  if (k === "svg" && !source) return { node: image(it, viewHref(it)), source: true };
  if (k === "html" && !source) return { node: page(it), source: true };

  const tx = await textOf(it, signal);
  if (tx.bin || (k === "other" && /[\0-\x08\x0e-\x1a]/.test(tx.t.slice(0, 4096)))) return noPreview(it, "There's no preview for this kind of file.");
  const text = tx.all != null && tx.all.length <= 4 * 1048576 ? tx.all : null;
  const rich = (node) => ({ node, source: true, text });
  if (source || k === "svg" || k === "html") return { node: asCode(langOf(it.name), it.name, tx), source: k !== "code" && k !== "text" && k !== "other" && k !== "diff", text };
  switch (k) {
    case "md": return rich(wrap(h("div", { class: "pv-md" }, markdown(tx.t)), tx.cut ? cutNote() : null));
    case "table": return rich(wrap(fenceBlock(extOf(it.name) + " title=" + it.name.replace(/\s/g, "_"), tx.t), tx.cut ? cutNote() : null));
    case "mermaid": return rich(wrap(fenceBlock("mermaid", tx.t)));
    case "latex": {   // \includegraphics finds pictures sent in the same message
      const sibling = n => (!it.kb && it.id && /^[A-Za-z0-9_][A-Za-z0-9_.-]*$/.test(n) ? fileHref({ id: it.id, name: n }) : null);
      return rich(wrap((await import("./latex.js")).latex(tx.t, { sibling }), tx.cut ? cutNote() : null));
    }
    case "touchstone": return rich(wrap(fenceBlock(extOf(it.name) + " title=" + it.name.replace(/\s/g, "_"), tx.t)));
    case "spice": return rich(wrap(fenceBlock("spice title=" + it.name.replace(/\s/g, "_"), tx.t)));
    case "json": {
      if (tx.cut) return { node: asCode("json", it.name, tx), text };
      let v; try { v = JSON.parse(tx.t); } catch (e) { return { node: wrap(h("p", { class: "pv-cut meta" }, "Not valid JSON (" + e.message + "), so here it is as text."), codeBlock("json", it.name, tx.t)), text }; }
      return rich(wrap(jsonTree(v)));
    }
    case "notebook": {
      if (tx.cut) return { node: asCode("json", it.name, tx), text };
      return rich(await notebook(tx, it.name));
    }
    case "diff": return { node: asCode("diff", it.name, tx), text };
    default: return { node: asCode(langOf(it.name), it.name, tx), text };
  }
}
