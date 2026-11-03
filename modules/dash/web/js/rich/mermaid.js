// Mermaid runs in a sandboxed frame with no same-origin rights, one drawing at a time. Its SVG comes back as text and goes through
// sanitize.js like any post's. The library (5.5 MB) is fetched the first time a diagram nears the screen and the frame is thrown away
// a minute after the last one, so a thread with no diagram pays nothing and one with a diagram doesn't keep it in memory.
// In the frame, computed styles are written onto the shapes, so its <style> sheet never has to reach the page.
import { h } from "../core/dom.js";
import { figure } from "./figure.js";
import { cleanSvg, STYLE_PROPS } from "./sanitize.js";

const LIB = new URL("../../vendor/mermaid/mermaid.min.js", import.meta.url).href;
const MAX_TEXT = 50000, MAX_EDGES = 500, WAIT_MS = 15000, IDLE_MS = 60000;
const FRAME_CSP = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; font-src data:";

// Runs in the frame. Takes the library as text from the parent, as a frame with no origin can't send the session cookie.
const inFrame = props => `addEventListener("message", async e => {
  if (e.source !== parent || !e.data) return;
  const m = e.data, say = o => parent.postMessage(Object.assign({ hive: 1 }, o), "*");
  if (m.type === "lib") { const s = document.createElement("script"); s.textContent = m.text; document.head.append(s); say({ type: "libok", ok: !!self.mermaid }); return; }
  if (m.type !== "draw") return;
  try {
    mermaid.initialize({ startOnLoad: false, securityLevel: "strict", htmlLabels: false, flowchart: { htmlLabels: false }, maxTextSize: ${MAX_TEXT}, maxEdges: ${MAX_EDGES},
      suppressErrorRendering: true, theme: "base", darkMode: m.dark, fontFamily: m.vars.fontFamily, themeVariables: m.vars });
    const { svg } = await mermaid.render("d" + m.id, m.src);
    const box = document.createElement("div"); box.innerHTML = svg; document.body.append(box);
    const root = box.querySelector("svg"), P = ${JSON.stringify(props)};
    const base = document.createElementNS("http://www.w3.org/2000/svg", "g"); root.append(base);
    const d = getComputedStyle(base), def = {}; for (const k of P) def[k] = d.getPropertyValue(k);
    base.remove();
    const els = [root, ...root.querySelectorAll("*")].filter(el => !el.closest("style,title,desc"));
    // Read every element first: the sheet's rules match on classes up the tree, so none can go until all are read.
    const read = els.map(el => { const c = getComputedStyle(el), out = []; for (const k of P) { const v = c.getPropertyValue(k); if (v && (v !== def[k] || k === "fill" || k === "stroke")) out.push(k + ":" + v); } return out.join(";"); });
    els.forEach((el, i) => { el.setAttribute("style", read[i]); el.removeAttribute("class"); });
    root.querySelectorAll("style").forEach(n => n.remove());
    const text = root.outerHTML; box.remove();
    say({ type: "svg", id: m.id, svg: text });
  } catch (err) { say({ type: "err", id: m.id, message: String(err && err.message || err).slice(0, 300) }); document.getElementById("d" + m.id)?.remove(); }
});
parent.postMessage({ hive: 1, type: "ready" }, "*");`;

let frame = null, ready = null, idleT = 0, seq = 0;
const waiting = new Map();
function drop() { frame?.remove(); frame = null; ready = null; for (const [, w] of waiting) w.no(new Error("The drawing frame stopped.")); waiting.clear(); }
addEventListener("message", e => {
  if (!frame || e.source !== frame.contentWindow || !e.data || e.data.hive !== 1) return;
  const m = e.data;
  if (m.type === "ready" || m.type === "libok") { frame._on?.(m); return; }
  const w = waiting.get(m.id); if (!w) return;
  waiting.delete(m.id);
  if (m.type === "svg" && typeof m.svg === "string") w.ok(m.svg); else w.no(new Error(String(m.message || "It wouldn't draw.")));
});
function openFrame() {
  return ready ||= new Promise((ok, no) => {
    const f = frame = h("iframe", { sandbox: "allow-scripts", "aria-hidden": "true", tabindex: "-1", title: "", style: "position:fixed;left:-20000px;top:0;width:1400px;height:1000px;border:0;visibility:hidden" });
    f.srcdoc = `<!doctype html><html><head><meta http-equiv="Content-Security-Policy" content="${FRAME_CSP}"></head><body><script>${inFrame(STYLE_PROPS)}<\/script></body></html>`;
    const late = setTimeout(() => { drop(); no(new Error("The diagram library didn't load.")); }, WAIT_MS * 2);
    f._on = async m => {
      if (m.type === "ready") {
        try { f.contentWindow.postMessage({ type: "lib", text: await (await fetch(LIB)).text() }, "*"); }
        catch (e) { clearTimeout(late); drop(); no(e); }
      } else { clearTimeout(late); if (m.ok) ok(); else { drop(); no(new Error("The diagram library didn't load.")); } }
    };
    document.body.append(f);
  });
}

// Mermaid wants plain hex colours; the page's are oklch, so a canvas reads them out.
function hexOf(css) {
  const p = h("span", { style: `color:${css};display:none` }); document.body.append(p);
  const c = getComputedStyle(p).color; p.remove();
  const x = document.createElement("canvas").getContext("2d", { willReadFrequently: true }); x.canvas.width = x.canvas.height = 1;
  x.fillStyle = c; x.fillRect(0, 0, 1, 1);
  const [r, g, b] = x.getImageData(0, 0, 1, 1).data;
  return "#" + [r, g, b].map(v => v.toString(16).padStart(2, "0")).join("");
}
function themeVars() {
  const v = n => hexOf(`var(--${n})`), font = getComputedStyle(document.documentElement).getPropertyValue("--font").trim() || "sans-serif";
  const surface = v("surface"), sunken = v("sunken"), ink = v("ink"), ink2 = v("ink-2"), line = v("line-2"), accent = v("tier1"), held = v("tier2");
  return { fontFamily: font, fontSize: "13px", background: surface, mainBkg: sunken, primaryColor: sunken, primaryTextColor: ink, primaryBorderColor: line, secondaryColor: surface, secondaryTextColor: ink,
    secondaryBorderColor: line, tertiaryColor: surface, tertiaryTextColor: ink, tertiaryBorderColor: line, lineColor: ink2, textColor: ink, titleColor: ink, nodeTextColor: ink, edgeLabelBackground: surface,
    clusterBkg: surface, clusterBorder: line, noteBkgColor: sunken, noteTextColor: ink, noteBorderColor: line, actorBkg: sunken, actorBorder: line, actorTextColor: ink, actorLineColor: ink2,
    signalColor: ink2, signalTextColor: ink, labelBoxBkgColor: sunken, labelBoxBorderColor: line, labelTextColor: ink, loopTextColor: ink, activationBkgColor: sunken, activationBorderColor: line,
    git0: accent, git1: held, pie1: accent, pie2: held, cScale0: accent, cScale1: held };
}
const isDark = () => document.documentElement.dataset.theme === "dark" || (document.documentElement.dataset.theme !== "light" && matchMedia("(prefers-color-scheme: dark)").matches);

// A drawing is kept as text by source and theme, so coming back to a thread draws nothing again.
const cache = new Map();
async function render(src) {
  const key = (isDark() ? "d" : "l") + src, had = cache.get(key);
  if (had) return had;
  clearTimeout(idleT);
  await openFrame();
  const id = ++seq;
  const svg = await new Promise((ok, no) => {
    const t = setTimeout(() => { waiting.delete(id); drop(); no(new Error("It took too long to draw.")); }, WAIT_MS);
    waiting.set(id, { ok: v => { clearTimeout(t); ok(v); }, no: e => { clearTimeout(t); no(e); } });
    frame.contentWindow.postMessage({ type: "draw", id, src, vars: themeVars(), dark: isDark() }, "*");
  }).finally(() => { clearTimeout(idleT); idleT = setTimeout(drop, IDLE_MS); });
  cache.set(key, svg); if (cache.size > 60) cache.delete(cache.keys().next().value);
  return svg;
}

export const mermaidFigure = (code, { title }) => figure({
  kind: "mermaid", label: "mermaid", title, src: code, themed: true, reserve: 240,
  draw: async () => {
    if (code.length > MAX_TEXT) throw new Error("It's over 50 kB.");
    if ((code.match(/--|==|-\.|\.-/g) || []).length > MAX_EDGES) throw new Error("It has over 500 edges.");
    const svg = await cleanSvg(await render(code));
    if (!svg) throw new Error("It came back empty.");
    return { svg };
  },
});
