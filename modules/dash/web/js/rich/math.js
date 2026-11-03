// Equations: source text first, then KaTeX (a local file, loaded the first time an equation nears the screen).
// A formula KaTeX refuses, or one too long to try, stays as its source with a quiet note.
import { h, copyText, selecting } from "../core/dom.js";
import { lazyKind } from "./lazy.js";
import { askButton } from "./ask.js";

const vendor = p => new URL("../../vendor/katex/" + p, import.meta.url).href;
const MAXTEX = 4000;
let loading = null;
function katex() {
  return loading ||= (async () => {
    document.head.append(h("link", { rel: "stylesheet", href: vendor("katex.min.css") }));
    await import(vendor("katex.min.js"));
    return self.katex;
  })();
}

export const sigOf = s => {  // FNV-1a: what morph compares to know the source changed
  let x = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) { x ^= s.charCodeAt(i); x = Math.imul(x, 0x01000193); }
  return (x >>> 0).toString(36) + "." + s.length;
};

lazyKind("tex", async el => {
  const tex = el._tex, display = el.dataset.mode === "display";
  const fail = why => { el.classList.add("rc-failinl"); el.title = why; };
  if (tex.length > MAXTEX) return fail("Too long to draw, so this is the source.");
  const k = await katex(), out = h("span");
  try {
    k.render(tex, out, { displayMode: display, throwOnError: true, trust: false, strict: "ignore", maxExpand: 1000, maxSize: 20, output: "htmlAndMathml" });
  } catch (e) { return fail("Couldn't draw this: " + String(e.message || e).slice(0, 160)); }
  if (el.isConnected) el.replaceChildren(out);
});

function copyTex(btn, tex) {
  copyText(tex).then(() => { btn.dataset.ok = ""; setTimeout(() => delete btn.dataset.ok, 1400); }, () => {});
}

/** Inline: the source as written (src, delimiters included) until it's drawn. Clicking copies the LaTeX. */
export function inlineMath(tex, src) {
  const lz = h("rc-lazy", { "data-kind": "tex", "data-mode": "inline" }, src); lz._tex = tex;
  return h("span", { class: "rc-im", "data-own": "", "data-sig": sigOf(src), title: "Click to copy the LaTeX", onclick: (e, n) => { if (!selecting()) copyTex(n, tex); } }, lz);
}

/** A display equation, with Copy LaTeX and Ask about this. */
export function equation(tex) {
  const lz = h("rc-lazy", { class: "rc-eq-b", "data-kind": "tex", "data-mode": "display" }, tex); lz._tex = tex;
  return h("div", { class: "rc-eq", "data-own": "", "data-sig": sigOf(tex) }, lz,
    h("span", { class: "rc-eq-a" },
      h("button", { type: "button", class: "rc-t", "aria-label": "Copy this equation's LaTeX", onclick: (e, b) => {
        copyTex(b, tex); b.textContent = "Copied"; setTimeout(() => { b.textContent = "Copy LaTeX"; }, 1400);
      } }, "Copy LaTeX"),
      askButton(n => "$$" + tex + "$$", "Ask about this equation")));
}

/** A `math` fence: one equation a line, unless it uses an environment that spans lines. */
export function mathFence(code) {
  const c = code.trim();
  if (!c) return [];
  return (/\\begin\{/.test(c) ? [c] : c.split("\n").map(l => l.trim()).filter(Boolean)).map(equation);
}
