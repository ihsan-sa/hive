// A writing sheet's printed problem: plain lines on the rules (never markdown() inside a figure), with maths and pictures.
// Maths: $…$, $$…$$ (one line or several), \(…\), \[…\], and bare TeX an agent forgot to wrap (f_{Y|X}, x^2, \alpha).
// Pictures: a line's ![alt](src) where src is a file on this board (~/files/<post>/<name>, or the bare name of a file attached
// to the offering post) or a data: image. Anything from elsewhere stays as its words: this page loads nothing from outside.
import { h } from "../core/dom.js";
import { inlineMath } from "../rich/math.js";

const ARG = String.raw`\{(?:[^{}$\n]|\{(?:[^{}$\n]|\{[^{}$\n]*\})*\})*\}`;
const CMD = String.raw`\\(?:alpha|beta|gamma|delta|epsilon|varepsilon|zeta|eta|theta|iota|kappa|lambda|mu|nu|xi|pi|rho|sigma|tau|phi|varphi|chi|psi|omega|Gamma|Delta|Theta|Lambda|Pi|Sigma|Phi|Psi|Omega|frac|dfrac|sqrt|sum|prod|int|iint|oint|lim|infty|partial|nabla|cdot|times|leq?|geq?|neq|approx|to|mapsto|pm|in|forall|exists|mathbb|mathbf|mathrm|mathcal|bar|hat|vec|tilde|overline)(?![a-zA-Z])`;
const SCRIPT = String.raw`(?:[_^](?:${ARG}|\d+|[A-Za-z](?![A-Za-z0-9_])))`;
const BARE = [
  String.raw`(?:${CMD}(?:${ARG})*${SCRIPT}*)+`,                                   // \alpha, \frac{a}{b}, \sum_{i=1}^{n}
  String.raw`(?<![\\\w])[A-Za-z0-9)\]]+(?:[_^]${ARG})+${SCRIPT}*`,                // f_{Y|X}, e^{-x}
  String.raw`(?<![\\\w])[A-Za-z](?:\^(?:\d+|[A-Za-z])|_\d)(?![A-Za-z0-9_])`,      // x^2, x_1 (never snake_case)
].join("|");
const TOKEN = new RegExp([
  String.raw`!\[([^\]\n]*)\]\(\s*([^)\s]+)\s*\)`,   // 1 2 picture
  String.raw`\$\$([^$\n]+)\$\$`,                    // 3 display
  String.raw`\\\[([^\n]+?)\\\]`,                    // 4 display
  String.raw`\$([^$\n]+)\$`,                        // 5 inline
  String.raw`\\\(([^\n]+?)\\\)`,                    // 6 inline
  `(${BARE})`,                                      // 7 bare
  String.raw`(?:https?:\/\/|~\/|\/)[^\s]+`,         // an address or a path: left as written
].join("|"), "g");

const FILE = /^(?:~|\/home\/[\w.-]+)?\/?files\/([0-9a-f]{12})\/([A-Za-z0-9_][A-Za-z0-9_.-]*)$/;
const DATA = /^data:image\/(?:png|jpe?g|gif|webp|svg\+xml)[;,]/i;
const PIC = /\.(?:png|jpe?g|gif|webp|svg)$/i;

/** The address a picture's src loads from, or null. post: the offering post, for a bare name attached to it. */
export function picSrc(src, post) {
  if (DATA.test(src)) return src;
  const m = FILE.exec(src);
  if (m) return PIC.test(m[2]) ? `/files/${m[1]}/${encodeURIComponent(m[2])}` : null;
  const f = post && PIC.test(src) && (post.files || []).find(f => f.name === src);
  return f ? `/files/${encodeURIComponent(post.id)}/${encodeURIComponent(f.name)}` : null;
}

const display = (tex, src) => inlineMath("\\displaystyle " + tex.trim(), src);

function printedLine(line, post) {
  const out = []; let at = 0, m;
  const re = new RegExp(TOKEN.source, "g");
  while ((m = re.exec(line))) {
    let node = null;
    if (m[2] != null) { const u = picSrc(m[2], post); if (u) node = h("img", { class: "ink-pic", src: u, alt: m[1] || "Picture", draggable: "false" }); }
    else if (m[3] != null || m[4] != null) node = display(m[3] ?? m[4], m[0]);
    else if (m[5] != null || m[6] != null) node = inlineMath(m[5] ?? m[6], m[0]);
    else if (m[7] != null) node = inlineMath(m[7], m[0]);
    if (!node) continue;
    if (m.index > at) out.push(line.slice(at, m.index));
    out.push(node); at = m.index + m[0].length;
  }
  if (at < line.length) out.push(line.slice(at));
  return out.length ? out : [" "];
}

/** One <div> per line; a $$ or \[ block over several lines is one. post: the post that offered the sheet. */
export function printedNodes(text, post) {
  const ls = String(text || "").split("\n"), out = [];
  for (let i = 0; i < ls.length; i++) {
    const l = ls[i].trimEnd(), open = /^\s*(\$\$|\\\[)\s*(.*)$/.exec(l);
    if (open) {
      const close = open[1] === "$$" ? "$$" : "\\]";
      if (!open[2].includes(close)) {
        let j = i + 1; while (j < ls.length && !ls[j].includes(close)) j++;
        if (j < ls.length) {
          const end = ls[j].indexOf(close), tex = [open[2], ...ls.slice(i + 1, j), ls[j].slice(0, end)].join("\n"), rest = ls[j].slice(end + 2).trim();
          out.push(h("div", {}, display(tex, open[1] + tex + close), ...(rest ? [" ", ...printedLine(rest, post)] : []))); i = j; continue;
        }
      }
    }
    out.push(h("div", {}, ...printedLine(l, post)));
  }
  return out;
}
