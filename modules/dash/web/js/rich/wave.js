// renderAny returns a tree, never HTML. Only JSON is accepted; the library's eva and DOM helpers are never called.
import { figure } from "./figure.js";
import { cleanSvg } from "./sanitize.js";

const LIB = new URL("../../vendor/wavedrom/wavedrom.unpkg.min.js", import.meta.url).href;
let loading;
const load = () => loading ||= import(LIB).then(() => self.wavedrom);
const NS = "http://www.w3.org/2000/svg";
const TAGS = new Set("svg g defs path rect circle ellipse line polyline polygon text tspan use marker title desc".split(" "));

// A label can itself be a JSONML tree. Build only inert SVG shapes and text, even before sanitizing.
function treeNode(tree, depth = 0) {
  if (depth > 40) throw new Error("The drawing is nested too deeply.");
  if (!Array.isArray(tree)) return document.createTextNode(String(tree ?? ""));
  const [tag, attrs, ...children] = tree;
  if (!TAGS.has(tag)) return document.createTextNode("");
  const el = document.createElementNS(NS, tag);
  if (attrs && typeof attrs === "object" && !Array.isArray(attrs)) for (const [key, value] of Object.entries(attrs)) {
    if (/^on/i.test(key) || /^(src|xmlns.*)$/i.test(key)) continue;
    if (/href$/i.test(key) && !String(value).startsWith("#")) continue;
    if (key === "style" && /url\(|expression|@import|javascript:|\\/i.test(String(value))) continue;
    el.setAttribute(key, String(value));
  }
  else if (attrs !== undefined) el.append(treeNode(attrs, depth + 1));
  for (const child of children) el.append(treeNode(child, depth + 1));
  return el;
}

function validate(source) {
  if (!source || !Array.isArray(source.signal)) throw new Error("Expected a JSON object with a signal array.");
  let waves = 0, lanes = 0;
  const walk = (v, depth = 0) => {
    if (depth > 30) throw new Error("The source is nested too deeply.");
    if (!v || typeof v !== "object") return;
    for (const [k, x] of Object.entries(v)) {
      if (["period", "phase", "hscale"].includes(k) && (typeof x !== "number" || !Number.isFinite(x) || Math.abs(x) > 100 || (k !== "phase" && x < 0.1))) throw new Error("Timing scales must be finite and between 0.1 and 100.");
      if (k === "wave") { if (typeof x !== "string") throw new Error("A wave must be text."); waves += x.length; lanes++; }
      walk(x, depth + 1);
    }
  };
  walk(source);
  if (waves > 10000 || lanes > 100) throw new Error("It has too many signal samples.");
}

export const waveFigure = (code, { title }) => figure({
  kind: "wavedrom", title, src: code, reserve: 180,
  draw: async () => {
    if (new TextEncoder().encode(code).length > 50000) throw new Error("It's over 50 kB.");
    const source = JSON.parse(code); validate(source);
    const lib = await load();
    // renderAny mutates the skin: every figure needs its own definitions and a fresh copy.
    const skin = structuredClone(lib.waveSkin);
    const sheet = new CSSStyleSheet(); sheet.replaceSync(skin.default[2][2]);
    const root = treeNode(lib.renderAny(0, source, skin));
    // Inline the trusted skin's rules before cleanSvg removes styles and prefixes IDs.
    for (const el of [root, ...root.querySelectorAll("*")]) {
      const inline = el.getAttribute("style") || "";
      for (const rule of sheet.cssRules) if (rule.selectorText && el.matches(rule.selectorText)) el.style.cssText += rule.style.cssText;
      el.style.cssText += inline;
    }
    // DOMPurify drops <use> to avoid recursive-reference hangs. Expand bounded local
    // skin geometry first, so the sanitizer can keep ordinary paths instead.
    const defs = new Map([...root.querySelectorAll("defs [id]")].map(el => [el.id, el]));
    let copies = 0;
    const expand = (node, depth = 0) => {
      if (depth > 20 || ++copies > 100000) throw new Error("Too many repeated shapes.");
      for (const use of [...node.querySelectorAll("use")]) {
        const ref = use.getAttribute("href") || use.getAttribute("xlink:href") || "";
        const original = defs.get(ref.slice(1));
        if (!ref.startsWith("#") || !original) { use.remove(); continue; }
        const clone = original.cloneNode(true); expand(clone, depth + 1);
        for (const el of [clone, ...clone.querySelectorAll("[id]")]) el.removeAttribute("id");
        const group = document.createElementNS(NS, "g");
        for (const a of use.attributes) if (!["href", "xlink:href", "x", "y", "id"].includes(a.name)) group.setAttribute(a.name, a.value);
        const offset = document.createElementNS(NS, "g");
        offset.setAttribute("transform", `translate(${Number(use.getAttribute("x")) || 0},${Number(use.getAttribute("y")) || 0})`);
        offset.append(clone); group.append(offset); use.replaceWith(group);
      }
    };
    expand(root);
    const svg = await cleanSvg(new XMLSerializer().serializeToString(root));
    if (!svg) throw new Error("It came back empty.");
    // WaveDrom's standard skin is dark ink on paper; retain the paper in either page theme.
    return { svg };
  },
});
